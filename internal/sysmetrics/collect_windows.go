//go:build windows

package sysmetrics

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/jxburros/GWatch/internal/model"
)

// Windows exposes these readings through kernel32, iphlpapi and a disk ioctl.
// None of them is wrapped by golang.org/x/sys/windows, so each is called
// through a lazily resolved system DLL and a struct laid out to match the SDK
// header. Every section fails on its own: a machine where the disk performance
// ioctl is refused still reports processor, memory, filesystem and network.

var (
	kernel32               = windows.NewLazySystemDLL("kernel32.dll")
	procGetSystemTimes     = kernel32.NewProc("GetSystemTimes")
	procGlobalMemoryStatus = kernel32.NewProc("GlobalMemoryStatusEx")
	procGetTickCount64     = kernel32.NewProc("GetTickCount64")

	iphlpapi       = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetIfTable = iphlpapi.NewProc("GetIfTable")
)

func collect(ctx context.Context) (*raw, error) {
	r := &raw{at: time.Now(), hostname: hostname(), cores: runtime.NumCPU()}
	r.platform, r.kernel, r.cpuModel = windowsIdentity()

	if t, err := windowsCPUTimes(); err != nil {
		r.addWarning("processor times: %v", err)
	} else {
		r.cpu, r.haveCPU = t, true
	}
	// Windows has no load average; utilisation above covers the same ground.

	if mem, err := windowsMemory(); err != nil {
		r.addWarning("memory: %v", err)
	} else {
		r.memory, r.haveMem = mem, true
	}
	if bt, err := windowsBootTime(); err != nil {
		r.addWarning("uptime: %v", err)
	} else {
		r.bootTime = bt
	}

	fs, warn := windowsFilesystems()
	r.filesystems = fs
	for _, w := range warn {
		r.addWarning("%s", w)
	}
	if nics, err := windowsInterfaces(); err != nil {
		r.addWarning("network counters: %v", err)
	} else {
		r.interfaces = nics
	}
	disks, derr := windowsDiskIO()
	r.disks = disks
	if derr != nil {
		r.addWarning("disk counters: %v", derr)
	}
	return r, ctx.Err()
}

// filetimeSeconds converts a FILETIME pair (100-nanosecond units) to seconds.
func filetimeSeconds(ft windows.Filetime) float64 {
	return float64(uint64(ft.HighDateTime)<<32|uint64(ft.LowDateTime)) / 1e7
}

// windowsCPUTimes reads system-wide processor time. Note the SDK's quirk: the
// kernel total already includes idle, so kernel-minus-idle is the real time
// spent in kernel mode.
func windowsCPUTimes() (cpuTimes, error) {
	var idle, kernel, user windows.Filetime
	rc, _, err := procGetSystemTimes.Call(
		uintptr(unsafe.Pointer(&idle)),
		uintptr(unsafe.Pointer(&kernel)),
		uintptr(unsafe.Pointer(&user)),
	)
	if rc == 0 {
		return cpuTimes{}, err
	}
	idleSecs := filetimeSeconds(idle)
	kernelSecs := filetimeSeconds(kernel)
	if kernelSecs < idleSecs {
		return cpuTimes{}, fmt.Errorf("implausible processor times")
	}
	return cpuTimes{
		user:   filetimeSeconds(user),
		system: kernelSecs - idleSecs,
		idle:   idleSecs,
	}, nil
}

// memoryStatusEx mirrors MEMORYSTATUSEX from sysinfoapi.h.
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

// windowsMemory reads physical memory and the page file. Windows reports the
// commit limit (RAM plus page file) rather than the page file alone, so swap
// here is the part of that limit beyond physical memory.
func windowsMemory() (mem model.HostMemory, err error) {
	var st memoryStatusEx
	st.Length = uint32(unsafe.Sizeof(st))
	rc, _, callErr := procGlobalMemoryStatus.Call(uintptr(unsafe.Pointer(&st)))
	if rc == 0 {
		return mem, callErr
	}
	mem.TotalBytes = st.TotalPhys
	mem.AvailableBytes = st.AvailPhys
	if st.AvailPhys <= st.TotalPhys {
		mem.UsedBytes = st.TotalPhys - st.AvailPhys
	}
	if st.TotalPageFile > st.TotalPhys {
		mem.SwapTotalBytes = st.TotalPageFile - st.TotalPhys
		committed := st.TotalPageFile - st.AvailPageFile
		if committed > mem.UsedBytes {
			used := committed - mem.UsedBytes
			if used > mem.SwapTotalBytes {
				used = mem.SwapTotalBytes
			}
			mem.SwapUsedBytes = used
		}
	}
	return mem, nil
}

func windowsBootTime() (time.Time, error) {
	ms, _, err := procGetTickCount64.Call()
	if ms == 0 {
		return time.Time{}, err
	}
	return time.Now().Add(-time.Duration(ms) * time.Millisecond), nil
}

// windowsFilesystems reports space on every fixed drive letter. Removable,
// optical and network drives are left out: they come and go, and a full DVD
// is not a hardware problem.
func windowsFilesystems() ([]model.HostFilesystem, []string) {
	buf := make([]uint16, 512)
	n, err := windows.GetLogicalDriveStrings(uint32(len(buf)), &buf[0])
	if err != nil {
		return nil, []string{"filesystems: " + err.Error()}
	}
	if int(n) > len(buf) {
		buf = make([]uint16, n+1)
		if n, err = windows.GetLogicalDriveStrings(uint32(len(buf)), &buf[0]); err != nil {
			return nil, []string{"filesystems: " + err.Error()}
		}
	}

	var out []model.HostFilesystem
	var warnings []string
	for _, root := range splitNullTerminated(buf[:n]) {
		rootPtr, err := windows.UTF16PtrFromString(root)
		if err != nil {
			continue
		}
		if windows.GetDriveType(rootPtr) != windows.DRIVE_FIXED {
			continue
		}
		var freeToCaller, total, free uint64
		if err := windows.GetDiskFreeSpaceEx(rootPtr, &freeToCaller, &total, &free); err != nil {
			if len(warnings) < 3 {
				warnings = append(warnings, "filesystem "+root+": "+err.Error())
			}
			continue
		}
		if total == 0 {
			continue
		}
		used := total - free
		out = append(out, model.HostFilesystem{
			Mount:      strings.TrimSuffix(root, `\`),
			Device:     strings.TrimSuffix(root, `\`),
			FSType:     volumeFSType(rootPtr),
			TotalBytes: total,
			FreeBytes:  freeToCaller,
			UsedBytes:  used,
			UsedPct:    usedPct(used, total),
		})
	}
	return out, warnings
}

func volumeFSType(root *uint16) string {
	var fsName [windows.MAX_PATH + 1]uint16
	err := windows.GetVolumeInformation(root, nil, 0, nil, nil, nil, &fsName[0], uint32(len(fsName)))
	if err != nil {
		return ""
	}
	return windows.UTF16ToString(fsName[:])
}

// splitNullTerminated splits the NUL-separated, double-NUL-terminated string
// list that several Windows APIs return.
func splitNullTerminated(b []uint16) []string {
	var out []string
	start := 0
	for i, c := range b {
		if c != 0 {
			continue
		}
		if i > start {
			out = append(out, windows.UTF16ToString(b[start:i]))
		}
		start = i + 1
	}
	return out
}

const (
	maxInterfaceNameLen = 256
	maxlenPhysAddr      = 8
	maxlenIfDescr       = 256
	ifOperStatusUp      = 5
)

// mibIfRow mirrors MIB_IFROW from ifmib.h. Its counters are 32-bit and so wrap
// roughly every 4 GiB; a wrap shows up as a counter going backwards, which the
// rate helper already reports as "no rate for this interval" rather than as a
// spike. GetIfTable2 has 64-bit counters but a far more intricate struct, and
// a skipped sample every few gigabytes is the better trade for an agent.
type mibIfRow struct {
	Name            [maxInterfaceNameLen]uint16
	Index           uint32
	Type            uint32
	Mtu             uint32
	Speed           uint32
	PhysAddrLen     uint32
	PhysAddr        [maxlenPhysAddr]byte
	AdminStatus     uint32
	OperStatus      uint32
	LastChange      uint32
	InOctets        uint32
	InUcastPkts     uint32
	InNUcastPkts    uint32
	InDiscards      uint32
	InErrors        uint32
	InUnknownProtos uint32
	OutOctets       uint32
	OutUcastPkts    uint32
	OutNUcastPkts   uint32
	OutDiscards     uint32
	OutErrors       uint32
	OutQLen         uint32
	DescrLen        uint32
	Descr           [maxlenIfDescr]byte
}

// windowsInterfaces reads per-interface counters from GetIfTable and names
// them the way the Go standard library does, so the name on a reading matches
// the name everywhere else in GWatch.
func windowsInterfaces() ([]ifaceCounters, error) {
	size := uint32(0)
	rc, _, _ := procGetIfTable.Call(0, uintptr(unsafe.Pointer(&size)), 0)
	if rc != uintptr(windows.ERROR_INSUFFICIENT_BUFFER) {
		return nil, fmt.Errorf("GetIfTable sizing failed (%d)", rc)
	}
	buf := make([]byte, size)
	rc, _, _ = procGetIfTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0)
	if rc != 0 {
		return nil, fmt.Errorf("GetIfTable failed (%d)", rc)
	}
	if len(buf) < int(unsafe.Sizeof(uint32(0))) {
		return nil, fmt.Errorf("GetIfTable returned no table")
	}

	count := *(*uint32)(unsafe.Pointer(&buf[0]))
	rowSize := unsafe.Sizeof(mibIfRow{})
	// MIB_IFTABLE is { DWORD dwNumEntries; MIB_IFROW table[1]; }, so the first
	// row starts wherever the compiler pads the count out to the row's
	// alignment. Asking Go for that offset on an equivalent struct gets it
	// right without hard-coding a number that differs between architectures.
	offset := unsafe.Offsetof(struct {
		N   uint32
		Row mibIfRow
	}{}.Row)

	names, addrs := windowsInterfaceNames()
	var out []ifaceCounters
	for i := uint32(0); i < count; i++ {
		start := offset + uintptr(i)*rowSize
		if start+rowSize > uintptr(len(buf)) {
			break
		}
		row := (*mibIfRow)(unsafe.Pointer(&buf[start]))
		if row.Type == windows.IF_TYPE_SOFTWARE_LOOPBACK {
			continue
		}
		name, ok := names[row.Index]
		if !ok {
			// No Go-visible interface with this index: fall back to the
			// adapter description so the reading is still identifiable.
			name = strings.TrimRight(string(row.Descr[:min(int(row.DescrLen), len(row.Descr))]), "\x00")
		}
		if name == "" {
			continue
		}
		out = append(out, ifaceCounters{
			name:      name,
			up:        row.OperStatus == ifOperStatusUp,
			addresses: addrs[name],
			speedMbit: uint64(row.Speed) / 1_000_000,
			rxBytes:   uint64(row.InOctets),
			txBytes:   uint64(row.OutOctets),
			rxErrors:  uint64(row.InErrors),
			txErrors:  uint64(row.OutErrors),
			rxDropped: uint64(row.InDiscards),
			txDropped: uint64(row.OutDiscards),
		})
	}
	return out, nil
}

// windowsInterfaceNames maps interface index to the name net.Interfaces uses.
func windowsInterfaceNames() (map[uint32]string, map[string][]string) {
	names := map[uint32]string{}
	ifaces, err := netInterfaces()
	if err != nil {
		return names, map[string][]string{}
	}
	for _, n := range ifaces {
		if n.Index > 0 {
			names[uint32(n.Index)] = n.Name
		}
	}
	return names, interfaceAddresses()
}

// diskPerformance mirrors DISK_PERFORMANCE from winioctl.h.
type diskPerformance struct {
	BytesRead           int64
	BytesWritten        int64
	ReadTime            int64
	WriteTime           int64
	IdleTime            int64
	ReadCount           uint32
	WriteCount          uint32
	QueueDepth          uint32
	SplitCount          uint32
	QueryTime           int64
	StorageDeviceNumber uint32
	StorageManagerName  [8]uint16
}

// ioctlDiskPerformance is IOCTL_DISK_PERFORMANCE, built from the CTL_CODE
// macro: IOCTL_DISK_BASE (0x00000007) << 16 | FILE_READ_ACCESS << 14 |
// 0x0008 << 2 | METHOD_BUFFERED.
const ioctlDiskPerformance = 0x00070020

// maxPhysicalDrives bounds the probe. Opening a drive that is not there fails
// immediately, so the loop stops at the first gap rather than at this number
// on an ordinary machine.
const maxPhysicalDrives = 16

// windowsDiskIO reads cumulative throughput from each physical drive. The
// ioctl needs the process to be able to open \\.\PhysicalDriveN, which the
// GWatch service can as LocalSystem; an agent running as an ordinary user
// cannot, and gets a warning instead of readings.
func windowsDiskIO() ([]diskCounters, error) {
	var out []diskCounters
	var firstErr error
	misses := 0
	for i := 0; i < maxPhysicalDrives && misses < 2; i++ {
		name := fmt.Sprintf(`\\.\PhysicalDrive%d`, i)
		path, err := windows.UTF16PtrFromString(name)
		if err != nil {
			continue
		}
		h, err := windows.CreateFile(path, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
			nil, windows.OPEN_EXISTING, 0, 0)
		if err != nil {
			misses++
			if firstErr == nil {
				firstErr = fmt.Errorf("open PhysicalDrive%d: %w", i, err)
			}
			continue
		}
		misses = 0
		var perf diskPerformance
		var returned uint32
		err = windows.DeviceIoControl(h, ioctlDiskPerformance, nil, 0,
			(*byte)(unsafe.Pointer(&perf)), uint32(unsafe.Sizeof(perf)), &returned, nil)
		windows.CloseHandle(h)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("read PhysicalDrive%d: %w", i, err)
			}
			continue
		}
		// ReadTime and WriteTime are in 100-nanosecond units.
		busy := (perf.ReadTime + perf.WriteTime) / 10_000
		out = append(out, diskCounters{
			name:       fmt.Sprintf("PhysicalDrive%d", i),
			readBytes:  uint64(perf.BytesRead),
			writeBytes: uint64(perf.BytesWritten),
			readOps:    uint64(perf.ReadCount),
			writeOps:   uint64(perf.WriteCount),
			busyMillis: uint64(busy),
		})
	}
	if len(out) > 0 {
		return out, nil
	}
	return nil, firstErr
}

// windowsIdentity reads the product name, build and processor description that
// Windows records in the registry.
func windowsIdentity() (platform, kernel, cpu string) {
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE); err == nil {
		defer k.Close()
		platform, _, _ = k.GetStringValue("ProductName")
		build, _, _ := k.GetStringValue("CurrentBuild")
		if display, _, err := k.GetStringValue("DisplayVersion"); err == nil && display != "" {
			platform = strings.TrimSpace(platform + " " + display)
		}
		kernel = build
	}
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`HARDWARE\DESCRIPTION\System\CentralProcessor\0`, registry.QUERY_VALUE); err == nil {
		defer k.Close()
		cpu, _, _ = k.GetStringValue("ProcessorNameString")
	}
	if platform == "" {
		platform = "Windows"
	}
	return platform, kernel, strings.TrimSpace(cpu)
}

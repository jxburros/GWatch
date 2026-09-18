//go:build darwin

package sysmetrics

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// macOS keeps most of what we want behind sysctl, which the standard library
// exposes, and the rest behind Mach calls, which it does not. Rather than pull
// in cgo for a monitoring agent, the two Mach-only readings are taken from the
// command line tools that already print them: vm_stat for memory and
// netstat for interface counters. Processor utilisation has no such fallback
// that is cheap enough to run every interval, so on macOS the load average per
// core stands in for it — see collectCPU below.

func collect(ctx context.Context) (*raw, error) {
	r := &raw{at: time.Now(), hostname: hostname(), cores: runtime.NumCPU()}
	r.cpuModel, _ = syscall.Sysctl("machdep.cpu.brand_string")
	r.platform = darwinPlatform()
	r.kernel, _ = syscall.Sysctl("kern.osrelease")

	if l1, l5, l15, err := darwinLoadAvg(); err != nil {
		r.addWarning("load average: %v", err)
	} else {
		r.load1, r.load5, r.load15, r.haveLoad = l1, l5, l15, true
	}
	// Processor time per mode lives behind host_processor_info, a Mach call
	// with no cgo-free equivalent. Load average is reported instead and the
	// system check falls back to it, so this is a gap, not a failure.
	r.addWarning("processor utilisation is not available on macOS; load average per core is used instead")

	if mem, err := darwinMemory(); err != nil {
		r.addWarning("memory: %v", err)
	} else {
		r.memory, r.haveMem = mem, true
	}
	if bt, err := darwinBootTime(); err != nil {
		r.addWarning("uptime: %v", err)
	} else {
		r.bootTime = bt
	}

	fs, warn := darwinFilesystems()
	r.filesystems = fs
	for _, w := range warn {
		r.addWarning("%s", w)
	}
	if nics, err := darwinInterfaces(ctx); err != nil {
		r.addWarning("network counters: %v", err)
	} else {
		r.interfaces = nics
	}
	// Per-device disk throughput needs IOKit, which is cgo-only.
	return r, ctx.Err()
}

// sysctlRaw returns a sysctl value as bytes, for the ones that hold a C struct
// rather than a string.
func sysctlRaw(name string) ([]byte, error) {
	s, err := syscall.Sysctl(name)
	if err != nil {
		return nil, err
	}
	// syscall.Sysctl trims a trailing NUL, which a binary value may legitimately
	// end with; every caller below reads from the front, so that is harmless.
	return []byte(s), nil
}

// darwinLoadAvg decodes struct loadavg: three fixed-point averages and the
// scale to divide them by.
func darwinLoadAvg() (l1, l5, l15 float64, err error) {
	b, err := sysctlRaw("vm.loadavg")
	if err != nil {
		return 0, 0, 0, err
	}
	if len(b) < 16 {
		return 0, 0, 0, syscall.EINVAL
	}
	scale := float64(binary.LittleEndian.Uint32(b[12:16]))
	if scale == 0 {
		scale = 2048
	}
	return float64(binary.LittleEndian.Uint32(b[0:4])) / scale,
		float64(binary.LittleEndian.Uint32(b[4:8])) / scale,
		float64(binary.LittleEndian.Uint32(b[8:12])) / scale, nil
}

// darwinBootTime decodes struct timeval from kern.boottime.
func darwinBootTime() (time.Time, error) {
	b, err := sysctlRaw("kern.boottime")
	if err != nil {
		return time.Time{}, err
	}
	if len(b) < 8 {
		return time.Time{}, syscall.EINVAL
	}
	return time.Unix(int64(binary.LittleEndian.Uint64(b[0:8])), 0), nil
}

// darwinMemory reads the total from sysctl and the breakdown from vm_stat.
// "Used" is wired + active + compressed, which is what Activity Monitor calls
// memory used; everything else is reclaimable.
func darwinMemory() (mem model.HostMemory, err error) {
	total, err := sysctlRaw("hw.memsize")
	if err != nil {
		return mem, err
	}
	if len(total) < 8 {
		return mem, syscall.EINVAL
	}
	mem.TotalBytes = binary.LittleEndian.Uint64(total[0:8])

	out, err := exec.Command("/usr/bin/vm_stat").Output()
	if err != nil {
		return mem, err
	}
	pageSize := uint64(4096)
	pages := map[string]uint64{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if i := strings.Index(line, "page size of "); i >= 0 {
			if v, err := strconv.ParseUint(strings.Fields(line[i+len("page size of "):])[0], 10, 64); err == nil && v > 0 {
				pageSize = v
			}
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(value), "."), 10, 64)
		if err != nil {
			continue
		}
		pages[strings.TrimSpace(name)] = v
	}
	used := (pages["Pages wired down"] + pages["Pages active"] + pages["Pages occupied by compressor"]) * pageSize
	if used > mem.TotalBytes {
		used = mem.TotalBytes
	}
	mem.UsedBytes = used
	mem.AvailableBytes = mem.TotalBytes - used
	mem.CachedBytes = (pages["Pages inactive"] + pages["Pages speculative"] + pages["Pages purgeable"]) * pageSize

	// struct xsw_usage: total, available and used, each a 64-bit byte count.
	if sw, err := sysctlRaw("vm.swapusage"); err == nil && len(sw) >= 24 {
		mem.SwapTotalBytes = binary.LittleEndian.Uint64(sw[0:8])
		mem.SwapUsedBytes = binary.LittleEndian.Uint64(sw[16:24])
	}
	return mem, nil
}

// mntNoWait is MNT_NOWAIT: return the cached statistics rather than asking
// every filesystem to refresh them, so a stalled network mount cannot block
// the reading. The standard library's syscall package does not define it.
const mntNoWait = 0x2

func darwinPlatform() string {
	out, err := exec.Command("/usr/bin/sw_vers", "-productVersion").Output()
	if err != nil {
		return "macOS"
	}
	return "macOS " + strings.TrimSpace(string(out))
}

// darwinFilesystems lists mounted volumes through getfsstat, skipping the
// synthetic and read-only system volumes that are always full by design.
func darwinFilesystems() ([]model.HostFilesystem, []string) {
	n, err := syscall.Getfsstat(nil, mntNoWait)
	if err != nil {
		return nil, []string{"filesystems: " + err.Error()}
	}
	buf := make([]syscall.Statfs_t, n)
	n, err = syscall.Getfsstat(buf, mntNoWait)
	if err != nil {
		return nil, []string{"filesystems: " + err.Error()}
	}

	var out []model.HostFilesystem
	seen := map[string]bool{}
	for _, st := range buf[:n] {
		fsType := utsToString(st.Fstypename[:])
		mount := utsToString(st.Mntonname[:])
		device := utsToString(st.Mntfromname[:])
		if darwinPseudoFS[fsType] || !strings.HasPrefix(device, "/") || st.Blocks == 0 || seen[device] {
			continue
		}
		seen[device] = true
		bs := uint64(st.Bsize)
		free := st.Bavail * bs
		used := (st.Blocks - st.Bfree) * bs
		fs := model.HostFilesystem{
			Mount: mount, Device: device, FSType: fsType,
			TotalBytes: st.Blocks * bs, FreeBytes: free, UsedBytes: used,
			UsedPct: usedPct(used, used+free),
		}
		if st.Files > 0 {
			fs.InodesTotal = st.Files
			fs.InodesUsed = st.Files - st.Ffree
			pct := usedPct(fs.InodesUsed, fs.InodesTotal)
			fs.InodesUsedPct = &pct
		}
		out = append(out, fs)
	}
	return out, nil
}

var darwinPseudoFS = map[string]bool{
	"devfs": true, "autofs": true, "lifs": true, "nullfs": true, "fdesc": true,
}

// utsToString turns a fixed-size C char array into a Go string. The element
// type differs between architectures, hence the type parameter.
func utsToString[T int8 | uint8](b []T) string {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		if c == 0 {
			break
		}
		out = append(out, byte(c))
	}
	return string(out)
}

// darwinInterfaces reads cumulative per-interface byte counts from netstat.
// The leading columns of its output vary (an interface with no address prints
// fewer), so the byte columns are located by counting back from the end, using
// the header to learn how many columns follow Obytes.
func darwinInterfaces(ctx context.Context) ([]ifaceCounters, error) {
	out, err := exec.CommandContext(ctx, "/usr/sbin/netstat", "-i", "-b", "-n").Output()
	if err != nil {
		return nil, err
	}
	up, loopback := upInterfaces()
	addrs := interfaceAddresses()

	var trailing = -1 // columns after Obytes in the header
	byName := map[string]ifaceCounters{}
	var order []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 6 {
			continue
		}
		if fields[0] == "Name" {
			for i, f := range fields {
				if f == "Obytes" {
					trailing = len(fields) - 1 - i
				}
			}
			continue
		}
		if trailing < 0 {
			continue
		}
		obytes := len(fields) - 1 - trailing
		ibytes := obytes - 3 // Ibytes, Opkts, Oerrs, Obytes
		if ibytes < 1 {
			continue
		}
		name := strings.TrimSuffix(fields[0], "*")
		if loopback[name] {
			continue
		}
		rx, err1 := strconv.ParseUint(fields[ibytes], 10, 64)
		tx, err2 := strconv.ParseUint(fields[obytes], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		// netstat prints one row per address family; the counters repeat, so
		// the first row for an interface is the one to keep.
		if _, ok := byName[name]; ok {
			continue
		}
		c := ifaceCounters{name: name, up: up[name], addresses: addrs[name], rxBytes: rx, txBytes: tx}
		if ierr := ibytes - 1; ierr >= 1 {
			c.rxErrors, _ = strconv.ParseUint(fields[ierr], 10, 64)
		}
		c.txErrors, _ = strconv.ParseUint(fields[obytes-1], 10, 64)
		byName[name] = c
		order = append(order, name)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	result := make([]ifaceCounters, 0, len(order))
	for _, n := range order {
		result = append(result, byName[n])
	}
	return result, nil
}

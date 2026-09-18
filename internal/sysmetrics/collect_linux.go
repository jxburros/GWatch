//go:build linux

package sysmetrics

import (
	"bufio"
	"context"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// procRoot is "/proc" in production. Tests point it at a directory of captured
// procfs files, which is the only way to assert the parsers against known
// numbers rather than against whatever this machine is doing.
var procRoot = "/proc"

// statfs is syscall.Statfs behind a variable so a test can answer for mount
// points that do not exist on the machine running the tests.
var statfs = syscall.Statfs

// sectorSize is the unit /proc/diskstats counts in, fixed at 512 bytes
// regardless of the device's real sector size.
const sectorSize = 512

// userHZ converts /proc/stat's clock ticks to seconds. Utilisation is a ratio
// of two tick counts so the constant cancels out, but keeping it makes the
// stored values seconds, which is what cpuTimes documents.
const userHZ = 100

func collect(ctx context.Context) (*raw, error) {
	r := &raw{at: time.Now(), hostname: hostname(), cores: runtime.NumCPU()}
	r.platform, r.kernel = linuxPlatform()
	r.cpuModel = linuxCPUModel()

	if t, err := readProcStat(); err != nil {
		r.addWarning("processor times: %v", err)
	} else {
		r.cpu, r.haveCPU = t, true
	}
	if l1, l5, l15, err := readLoadAvg(); err != nil {
		r.addWarning("load average: %v", err)
	} else {
		r.load1, r.load5, r.load15, r.haveLoad = l1, l5, l15, true
	}
	if mem, err := readMemInfo(); err != nil {
		r.addWarning("memory: %v", err)
	} else {
		r.memory, r.haveMem = mem, true
	}
	if bt, err := readBootTime(); err != nil {
		r.addWarning("uptime: %v", err)
	} else {
		r.bootTime = bt
	}

	fs, warn := readMounts()
	r.filesystems = fs
	for _, w := range warn {
		r.addWarning("%s", w)
	}
	if nics, err := readNetDev(); err != nil {
		r.addWarning("network counters: %v", err)
	} else {
		r.interfaces = nics
	}
	if disks, err := readDiskStats(); err != nil {
		r.addWarning("disk counters: %v", err)
	} else {
		r.disks = disks
	}
	return r, ctx.Err()
}

// readProcStat parses the aggregate "cpu" line of /proc/stat.
func readProcStat() (cpuTimes, error) {
	f, err := os.Open(procRoot + "/stat")
	if err != nil {
		return cpuTimes{}, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 5 || fields[0] != "cpu" {
			continue
		}
		vals := make([]float64, 0, 8)
		for _, s := range fields[1:] {
			v, err := strconv.ParseFloat(s, 64)
			if err != nil {
				break
			}
			vals = append(vals, v/userHZ)
			if len(vals) == 8 {
				break
			}
		}
		for len(vals) < 8 {
			vals = append(vals, 0)
		}
		return cpuTimes{
			user: vals[0], nice: vals[1], system: vals[2], idle: vals[3],
			iowait: vals[4], irq: vals[5], softirq: vals[6], steal: vals[7],
		}, nil
	}
	if err := sc.Err(); err != nil {
		return cpuTimes{}, err
	}
	return cpuTimes{}, os.ErrNotExist
}

func readLoadAvg() (l1, l5, l15 float64, err error) {
	b, err := os.ReadFile(procRoot + "/loadavg")
	if err != nil {
		return 0, 0, 0, err
	}
	fields := strings.Fields(string(b))
	if len(fields) < 3 {
		return 0, 0, 0, os.ErrInvalid
	}
	if l1, err = strconv.ParseFloat(fields[0], 64); err != nil {
		return 0, 0, 0, err
	}
	if l5, err = strconv.ParseFloat(fields[1], 64); err != nil {
		return 0, 0, 0, err
	}
	if l15, err = strconv.ParseFloat(fields[2], 64); err != nil {
		return 0, 0, 0, err
	}
	return l1, l5, l15, nil
}

// readMemInfo parses /proc/meminfo. Used memory is total minus MemAvailable
// rather than total minus MemFree: page cache is reclaimable, and counting it
// as used makes every healthy Linux box look like it is out of memory.
func readMemInfo() (mem model.HostMemory, err error) {
	f, err := os.Open(procRoot + "/meminfo")
	if err != nil {
		return mem, err
	}
	defer f.Close()

	vals := map[string]uint64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		v, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		if len(fields) > 1 && strings.EqualFold(fields[1], "kB") {
			v *= 1024
		}
		vals[name] = v
	}
	if err := sc.Err(); err != nil {
		return mem, err
	}
	if vals["MemTotal"] == 0 {
		return mem, os.ErrInvalid
	}

	mem.TotalBytes = vals["MemTotal"]
	mem.AvailableBytes = vals["MemAvailable"]
	if mem.AvailableBytes == 0 {
		// Pre-3.14 kernels have no MemAvailable; approximate it the way the
		// kernel's own commit message describes.
		mem.AvailableBytes = vals["MemFree"] + vals["Buffers"] + vals["Cached"] + vals["SReclaimable"]
	}
	if mem.AvailableBytes > mem.TotalBytes {
		mem.AvailableBytes = mem.TotalBytes
	}
	mem.UsedBytes = mem.TotalBytes - mem.AvailableBytes
	mem.CachedBytes = vals["Cached"] + vals["SReclaimable"]
	mem.SwapTotalBytes = vals["SwapTotal"]
	if mem.SwapTotalBytes > 0 {
		free := vals["SwapFree"]
		if free > mem.SwapTotalBytes {
			free = mem.SwapTotalBytes
		}
		mem.SwapUsedBytes = mem.SwapTotalBytes - free
	}
	return mem, nil
}

func readBootTime() (time.Time, error) {
	b, err := os.ReadFile(procRoot + "/uptime")
	if err != nil {
		return time.Time{}, err
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return time.Time{}, os.ErrInvalid
	}
	secs, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Now().Add(-time.Duration(secs * float64(time.Second))), nil
}

// pseudoFS lists filesystem types that do not describe real storage. Space on
// them is either meaningless or permanently 100% used (squashfs, the format
// snap packages are mounted from), which would trip a disk alert forever.
var pseudoFS = map[string]bool{
	"autofs": true, "binfmt_misc": true, "bpf": true, "cgroup": true, "cgroup2": true,
	"configfs": true, "debugfs": true, "devpts": true, "devtmpfs": true, "efivarfs": true,
	"fuse.gvfsd-fuse": true, "fusectl": true, "hugetlbfs": true, "mqueue": true,
	"nsfs": true, "overlay": true, "proc": true, "pstore": true, "ramfs": true,
	"rpc_pipefs": true, "securityfs": true, "selinuxfs": true, "squashfs": true,
	"sysfs": true, "tmpfs": true, "tracefs": true,
}

// readMounts lists real filesystems with their space usage. A device mounted
// more than once (a bind mount) is reported once, under the first mount point.
func readMounts() ([]model.HostFilesystem, []string) {
	f, err := os.Open(procRoot + "/mounts")
	if err != nil {
		return nil, []string{"filesystems: " + err.Error()}
	}
	defer f.Close()

	var out []model.HostFilesystem
	var warnings []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 {
			continue
		}
		device, mount, fsType := unescapeMount(fields[0]), unescapeMount(fields[1]), fields[2]
		if pseudoFS[fsType] || !strings.HasPrefix(device, "/") {
			continue
		}
		if seen[device] {
			continue
		}
		var st syscall.Statfs_t
		if err := statfs(mount, &st); err != nil {
			if len(warnings) < 3 {
				warnings = append(warnings, "filesystem "+mount+": "+err.Error())
			}
			continue
		}
		if st.Blocks == 0 {
			continue
		}
		seen[device] = true
		bs := uint64(st.Bsize)
		total := st.Blocks * bs
		// Free space excludes the root reservation, so "used" matches df: what
		// an ordinary process can still write is Bavail, not Bfree.
		free := st.Bavail * bs
		used := (st.Blocks - st.Bfree) * bs
		fs := model.HostFilesystem{
			Mount: mount, Device: device, FSType: fsType,
			TotalBytes: total, FreeBytes: free, UsedBytes: used,
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
	return out, warnings
}

// unescapeMount decodes the octal escapes /proc/mounts uses for spaces and
// other separators in device and mount paths.
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// readNetDev parses the per-interface counters in /proc/net/dev. Loopback is
// left out: its traffic is this machine talking to itself and would dominate
// the totals the dashboard shows.
func readNetDev() ([]ifaceCounters, error) {
	f, err := os.Open(procRoot + "/net/dev")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	up, loopback := upInterfaces()
	addrs := interfaceAddresses()

	var out []ifaceCounters
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue // the two header lines
		}
		name = strings.TrimSpace(name)
		if name == "" || loopback[name] {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 16 {
			continue
		}
		num := func(i int) uint64 {
			v, _ := strconv.ParseUint(fields[i], 10, 64)
			return v
		}
		out = append(out, ifaceCounters{
			name:      name,
			up:        up[name],
			addresses: addrs[name],
			speedMbit: readIfaceSpeed(name),
			rxBytes:   num(0), rxErrors: num(2), rxDropped: num(3),
			txBytes: num(8), txErrors: num(10), txDropped: num(11),
		})
	}
	return out, sc.Err()
}

// readIfaceSpeed reports the negotiated link speed in Mbit/s, or 0 when the
// kernel does not know it (virtual interfaces, or a down link).
func readIfaceSpeed(name string) uint64 {
	b, err := os.ReadFile("/sys/class/net/" + name + "/speed")
	if err != nil {
		return 0
	}
	v, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil || v <= 0 {
		return 0
	}
	return uint64(v)
}

// readDiskStats parses /proc/diskstats, keeping whole devices and dropping
// their partitions so a write is not counted twice.
func readDiskStats() ([]diskCounters, error) {
	f, err := os.Open(procRoot + "/diskstats")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	type row struct {
		name string
		c    diskCounters
	}
	var rows []row
	names := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 14 {
			continue
		}
		name := fields[2]
		if skipBlockDevice(name) {
			continue
		}
		num := func(i int) uint64 {
			v, _ := strconv.ParseUint(fields[i], 10, 64)
			return v
		}
		if num(3) == 0 && num(7) == 0 && num(5) == 0 && num(9) == 0 {
			continue // never used; almost always an empty card reader slot
		}
		names[name] = true
		rows = append(rows, row{name, diskCounters{
			name:       name,
			readOps:    num(3),
			readBytes:  num(5) * sectorSize,
			writeOps:   num(7),
			writeBytes: num(9) * sectorSize,
			busyMillis: num(12),
		}})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	out := make([]diskCounters, 0, len(rows))
	for _, r := range rows {
		if parentDevice(r.name, names) == "" {
			out = append(out, r.c)
		}
	}
	return out, nil
}

// skipBlockDevice drops devices whose throughput says nothing about hardware
// health: loop mounts (snap packages), RAM disks and optical drives.
func skipBlockDevice(name string) bool {
	for _, prefix := range []string{"loop", "ram", "zram", "sr", "fd", "dm-"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// parentDevice returns the whole-disk device a partition belongs to, or "" if
// name is itself a whole disk. "sda1" belongs to "sda"; "nvme0n1p3" belongs to
// "nvme0n1"; "mmcblk0p1" belongs to "mmcblk0".
func parentDevice(name string, known map[string]bool) string {
	i := len(name)
	for i > 0 && name[i-1] >= '0' && name[i-1] <= '9' {
		i--
	}
	if i == len(name) || i == 0 {
		return ""
	}
	if base := name[:i]; known[base] {
		return base
	}
	if i > 1 && name[i-1] == 'p' {
		if base := name[:i-1]; known[base] {
			return base
		}
	}
	return ""
}

// linuxPlatform reads the distribution name from /etc/os-release and the
// kernel release from uname.
func linuxPlatform() (platform, kernel string) {
	if b, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
				platform = strings.Trim(strings.TrimSpace(v), `"`)
				break
			}
		}
	}
	var un syscall.Utsname
	if err := syscall.Uname(&un); err == nil {
		kernel = utsToString(un.Release[:])
	}
	return platform, kernel
}

// utsToString turns uname's fixed-size C char array into a Go string. The
// element type is int8 on most architectures and uint8 on others, so the
// conversion goes through a type parameter rather than being written twice.
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

func linuxCPUModel() string {
	b, err := os.ReadFile(procRoot + "/cpuinfo")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(name) {
		case "model name", "Model", "Hardware":
			return strings.TrimSpace(value)
		}
	}
	return ""
}

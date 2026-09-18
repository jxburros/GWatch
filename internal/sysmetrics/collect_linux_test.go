//go:build linux

package sysmetrics

import (
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// fakeProc writes a directory of procfs files and points the collector at it,
// so the parsers are checked against known numbers instead of against whatever
// the machine running the tests happens to be doing.
func fakeProc(t *testing.T, files map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := procRoot
	procRoot = dir
	t.Cleanup(func() { procRoot = old })
}

func TestReadProcStat(t *testing.T) {
	fakeProc(t, map[string]string{"stat": `cpu  100 200 300 400 500 600 700 800 900 1000
cpu0 1 2 3 4 5 6 7 8
intr 12345
`})
	got, err := readProcStat()
	if err != nil {
		t.Fatal(err)
	}
	want := cpuTimes{user: 1, nice: 2, system: 3, idle: 4, iowait: 5, irq: 6, softirq: 7, steal: 8}
	if got != want {
		t.Fatalf("want %+v, got %+v", want, got)
	}
	// guest and guest_nice are already counted inside user and nice, so the
	// parser must stop at steal rather than adding them again.
	if got.total() != 36 {
		t.Errorf("total: want 36 seconds, got %v", got.total())
	}
}

func TestReadMemInfoUsesAvailableNotFree(t *testing.T) {
	fakeProc(t, map[string]string{"meminfo": `MemTotal:       16000000 kB
MemFree:          500000 kB
MemAvailable:   12000000 kB
Buffers:          100000 kB
Cached:          9000000 kB
SReclaimable:     400000 kB
SwapTotal:       2000000 kB
SwapFree:        1500000 kB
`})
	mem, err := readMemInfo()
	if err != nil {
		t.Fatal(err)
	}
	if mem.TotalBytes != 16_000_000*1024 {
		t.Errorf("total: got %d", mem.TotalBytes)
	}
	// 4 GB used, not the 15.5 GB that MemFree alone would suggest: page cache
	// is reclaimable and counting it as used makes every Linux box look full.
	if want := uint64(4_000_000 * 1024); mem.UsedBytes != want {
		t.Errorf("used: want %d, got %d", want, mem.UsedBytes)
	}
	if want := uint64(9_400_000 * 1024); mem.CachedBytes != want {
		t.Errorf("cached: want %d, got %d", want, mem.CachedBytes)
	}
	if want := uint64(500_000 * 1024); mem.SwapUsedBytes != want {
		t.Errorf("swap used: want %d, got %d", want, mem.SwapUsedBytes)
	}
}

// Kernels before 3.14 have no MemAvailable line; the collector approximates it
// rather than reporting the machine as nearly full.
func TestReadMemInfoWithoutMemAvailable(t *testing.T) {
	fakeProc(t, map[string]string{"meminfo": `MemTotal:       1000 kB
MemFree:          100 kB
Buffers:           50 kB
Cached:           300 kB
SReclaimable:      50 kB
`})
	mem, err := readMemInfo()
	if err != nil {
		t.Fatal(err)
	}
	if want := uint64(500 * 1024); mem.AvailableBytes != want {
		t.Errorf("available: want %d, got %d", want, mem.AvailableBytes)
	}
	if want := uint64(500 * 1024); mem.UsedBytes != want {
		t.Errorf("used: want %d, got %d", want, mem.UsedBytes)
	}
}

func TestReadNetDevSkipsLoopback(t *testing.T) {
	fakeProc(t, map[string]string{"net/dev": `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 1000 10 0 0 0 0 0 0 1000 10 0 0 0 0 0 0
  eth0: 5000 50 1 2 0 0 0 0 7000 70 3 4 0 0 0 0
  ifb0: 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
  eth1: 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0
`})
	// lo is loopback; ifb0 is a kernel shaping device that is down and has
	// never carried a byte; eth1 is an idle but connected port.
	restore := netInterfaces
	netInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{
			{Index: 1, Name: "lo", Flags: net.FlagUp | net.FlagLoopback},
			{Index: 2, Name: "eth0", Flags: net.FlagUp},
			{Index: 3, Name: "ifb0"},
			{Index: 4, Name: "eth1", Flags: net.FlagUp},
		}, nil
	}
	t.Cleanup(func() { netInterfaces = restore })

	nics, err := readNetDev()
	if err != nil {
		t.Fatal(err)
	}
	if len(nics) != 2 || nics[0].name != "eth0" || nics[1].name != "eth1" {
		t.Fatalf("want eth0 and eth1, got %+v", nics)
	}
	n := nics[0]
	if n.rxBytes != 5000 || n.txBytes != 7000 {
		t.Errorf("bytes: got rx=%d tx=%d", n.rxBytes, n.txBytes)
	}
	if n.rxErrors != 1 || n.rxDropped != 2 || n.txErrors != 3 || n.txDropped != 4 {
		t.Errorf("error counters: %+v", n)
	}
	if !n.up {
		t.Error("eth0 should be reported as up")
	}
}

func TestReadDiskStatsDropsPartitionsAndPseudoDevices(t *testing.T) {
	// Columns: major minor name reads merged sectorsRead msRead writes merged
	// sectorsWritten msWrite inFlight msIO weightedMsIO
	fakeProc(t, map[string]string{"diskstats": `   8       0 sda 100 0 2000 50 200 0 4000 60 0 900 0
   8       1 sda1 90 0 1800 40 180 0 3600 50 0 800 0
 259       0 nvme0n1 10 0 200 5 20 0 400 6 0 90 0
 259       1 nvme0n1p1 9 0 180 4 18 0 360 5 0 80 0
   7       0 loop0 5 0 100 1 0 0 0 0 0 10 0
 179       0 mmcblk0 1 0 20 1 1 0 20 1 0 2 0
 179       1 mmcblk0p1 1 0 20 1 1 0 20 1 0 2 0
   8      16 sdb 0 0 0 0 0 0 0 0 0 0 0
`})
	disks, err := readDiskStats()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range disks {
		names = append(names, d.name)
	}
	want := []string{"sda", "nvme0n1", "mmcblk0"}
	if len(names) != len(want) {
		t.Fatalf("want %v, got %v", want, names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("want %v, got %v", want, names)
		}
	}
	// Sectors are always 512 bytes in diskstats whatever the device's real
	// sector size, so 2000 sectors read is 1 MiB.
	if disks[0].readBytes != 2000*512 || disks[0].writeBytes != 4000*512 {
		t.Errorf("sda bytes: %+v", disks[0])
	}
	if disks[0].busyMillis != 900 {
		t.Errorf("sda busy time: got %d", disks[0].busyMillis)
	}
}

func TestParentDevice(t *testing.T) {
	known := map[string]bool{"sda": true, "nvme0n1": true, "mmcblk0": true, "vda": true}
	tests := []struct{ name, want string }{
		{"sda", ""},
		{"sda1", "sda"},
		{"sda15", "sda"},
		{"nvme0n1", ""},
		{"nvme0n1p3", "nvme0n1"},
		{"mmcblk0p1", "mmcblk0"},
		{"vdb", ""},  // a whole disk we have not seen listed
		{"vdb1", ""}, // its partition, with no known parent, so kept
	}
	for _, tt := range tests {
		if got := parentDevice(tt.name, known); got != tt.want {
			t.Errorf("%s: want %q, got %q", tt.name, tt.want, got)
		}
	}
}

func TestUnescapeMountDecodesOctal(t *testing.T) {
	if got := unescapeMount(`/mnt/My\040Drive`); got != "/mnt/My Drive" {
		t.Errorf("got %q", got)
	}
	if got := unescapeMount("/srv"); got != "/srv" {
		t.Errorf("got %q", got)
	}
}

func TestReadMountsSkipsPseudoFilesystems(t *testing.T) {
	fakeProc(t, map[string]string{"mounts": `proc /proc proc rw 0 0
sysfs /sys sysfs rw 0 0
/dev/loop0 /snap/core sqaushfs ro 0 0
/dev/loop1 /snap/gwatch squashfs ro 0 0
tmpfs /run tmpfs rw 0 0
/dev/root / ext4 rw 0 0
/dev/root /var/lib ext4 rw 0 0
`})
	// Answer for mount points that do not exist here: 100 blocks of 1 KiB,
	// 40 of them free to an unprivileged writer.
	restore := statfs
	statfs = func(path string, st *syscall.Statfs_t) error {
		*st = syscall.Statfs_t{Bsize: 1024, Blocks: 100, Bfree: 45, Bavail: 40, Files: 10, Ffree: 8}
		return nil
	}
	t.Cleanup(func() { statfs = restore })

	fs, _ := readMounts()
	// /proc, /sys, tmpfs and squashfs are not storage; /dev/root is bind
	// mounted twice and must be counted once. "sqaushfs" is a deliberate typo
	// standing in for an unknown type, which is kept rather than guessed at.
	var mounts []string
	for _, f := range fs {
		mounts = append(mounts, f.Mount)
	}
	if len(mounts) != 2 || mounts[0] != "/snap/core" || mounts[1] != "/" {
		t.Fatalf("got %v", mounts)
	}
	// df's arithmetic: used is total minus free blocks, but the percentage is
	// measured against what a process can actually still write, so the root
	// reservation does not read as space the user has.
	root := fs[1]
	if root.UsedBytes != 55*1024 || root.FreeBytes != 40*1024 {
		t.Errorf("root usage: %+v", root)
	}
	if root.UsedPct != usedPct(55*1024, 95*1024) {
		t.Errorf("root used pct: got %v", root.UsedPct)
	}
	if root.InodesUsed != 2 {
		t.Errorf("root inodes used: got %d", root.InodesUsed)
	}
}

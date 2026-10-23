//go:build linux

package sysmetrics

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// fakeTree writes files (and, for names ending in "@", symlinks whose body is
// the target) under a fresh directory and returns it.
func fakeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if link, ok := cutSuffix(name, "@"); ok {
			path := filepath.Join(dir, link)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(body, path); err != nil {
				t.Fatal(err)
			}
			continue
		}
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func cutSuffix(s, suffix string) (string, bool) {
	if len(s) > len(suffix) && s[len(s)-len(suffix):] == suffix {
		return s[:len(s)-len(suffix)], true
	}
	return s, false
}

// fakeSys points sysRoot at a directory of captured sysfs files.
func fakeSys(t *testing.T, files map[string]string) {
	t.Helper()
	dir := fakeTree(t, files)
	old := sysRoot
	sysRoot = dir
	t.Cleanup(func() { sysRoot = old })
}

// A desktop as the kernel describes it: an Intel package sensor with its
// limits, two NVMe drives (same chip name, told apart by device), an ACPI
// zone present both as hwmon and as a thermal zone, a disconnected input
// reading -128 °C, and a Raspberry-Pi-style zone that hwmon does not cover.
func TestReadTemperatures(t *testing.T) {
	fakeSys(t, map[string]string{
		"class/hwmon/hwmon0/name":                       "acpitz\n",
		"class/hwmon/hwmon0/temp1_input":                "27800\n",
		"class/hwmon/hwmon1/name":                       "coretemp\n",
		"class/hwmon/hwmon1/temp1_input":                "45000\n",
		"class/hwmon/hwmon1/temp1_label":                "Package id 0\n",
		"class/hwmon/hwmon1/temp1_max":                  "80000\n",
		"class/hwmon/hwmon1/temp1_crit":                 "100000\n",
		"class/hwmon/hwmon1/temp2_input":                "-128000\n",
		"class/hwmon/hwmon2/name":                       "nvme\n",
		"class/hwmon/hwmon2/temp1_input":                "38850\n",
		"class/hwmon/hwmon2/temp1_label":                "Composite\n",
		"class/hwmon/hwmon2/device@":                    "../../nvme0",
		"class/hwmon/hwmon3/name":                       "nvme\n",
		"class/hwmon/hwmon3/temp1_input":                "41850\n",
		"class/hwmon/hwmon3/temp1_label":                "Composite\n",
		"class/hwmon/hwmon3/device@":                    "../../nvme1",
		"class/thermal/thermal_zone0/type":              "acpitz\n",
		"class/thermal/thermal_zone0/temp":              "27800\n",
		"class/thermal/thermal_zone1/type":              "cpu-thermal\n",
		"class/thermal/thermal_zone1/temp":              "52123\n",
		"class/thermal/thermal_zone1/trip_point_0_type": "passive\n",
		"class/thermal/thermal_zone1/trip_point_0_temp": "75000\n",
		"class/thermal/thermal_zone1/trip_point_1_type": "critical\n",
		"class/thermal/thermal_zone1/trip_point_1_temp": "90000\n",
	})
	got := readTemperatures()
	byName := map[string]model.HostTemperature{}
	for _, tt := range got {
		byName[tt.Name] = tt
	}
	want := map[string]float64{
		"acpitz/temp1":          27.8,
		"coretemp/Package id 0": 45,
		"nvme-nvme0/Composite":  38.85,
		"nvme-nvme1/Composite":  41.85,
		"thermal/cpu-thermal":   52.123,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d sensors %+v, want %v", len(got), got, want)
	}
	for name, c := range want {
		tt, ok := byName[name]
		if !ok || tt.Celsius != c {
			t.Errorf("%s: got %+v, want %v °C", name, tt, c)
		}
	}
	pkg := byName["coretemp/Package id 0"]
	if pkg.HighCelsius == nil || *pkg.HighCelsius != 80 || pkg.CritCelsius == nil || *pkg.CritCelsius != 100 {
		t.Errorf("package limits: %+v", pkg)
	}
	if z := byName["thermal/cpu-thermal"]; z.CritCelsius == nil || *z.CritCelsius != 90 {
		t.Errorf("zone critical trip: %+v", z)
	}
}

func TestReadTemperaturesNoSensors(t *testing.T) {
	fakeSys(t, map[string]string{"class/net/eth0/speed": "1000\n"})
	if got := readTemperatures(); len(got) != 0 {
		t.Errorf("got %+v from a machine with no sensors", got)
	}
}

func TestParsePressure(t *testing.T) {
	p, ok := parsePressure("memory", "some avg10=1.50 avg60=0.75 avg300=0.10 total=123\nfull avg10=0.50 avg60=0.25 avg300=0.00 total=45\n")
	if !ok || p.Avg10 != 1.5 || p.Avg60 != 0.75 || p.FullAvg10 == nil || *p.FullAvg10 != 0.5 || *p.FullAvg60 != 0.25 {
		t.Errorf("memory: %+v ok=%v", p, ok)
	}
	// The machine-wide cpu "full" line is always zero; it is not kept.
	p, ok = parsePressure("cpu", "some avg10=12.00 avg60=8.00 avg300=1.00 total=9\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n")
	if !ok || p.Avg10 != 12 || p.FullAvg10 != nil {
		t.Errorf("cpu: %+v ok=%v", p, ok)
	}
	if _, ok := parsePressure("io", "garbage\n"); ok {
		t.Error("an unreadable file produced a reading")
	}
}

func TestReadPressure(t *testing.T) {
	fakeProc(t, map[string]string{
		"pressure/cpu":    "some avg10=2.00 avg60=1.00 avg300=0.50 total=1\n",
		"pressure/memory": "some avg10=0.00 avg60=0.00 avg300=0.00 total=0\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n",
	})
	got := readPressure()
	if len(got) != 2 || got[0].Resource != "cpu" || got[1].Resource != "memory" {
		t.Fatalf("got %+v; io is missing from this kernel and should simply be absent", got)
	}
}

func TestParseCgroupFiles(t *testing.T) {
	if v, ok := parseCPUMax("150000 100000"); !ok || v != 1.5 {
		t.Errorf("cpu.max 1.5 cores: %v %v", v, ok)
	}
	if _, ok := parseCPUMax("max 100000"); ok {
		t.Error("an unlimited cpu.max was read as a limit")
	}
	if v, ok := parseMemoryMax("2147483648"); !ok || v != 2<<30 {
		t.Errorf("memory.max: %v %v", v, ok)
	}
	if _, ok := parseMemoryMax("max"); ok {
		t.Error("an unlimited memory.max was read as a limit")
	}
	if got := ownCgroup("12:pids:/docker/abc\n0::/system.slice/docker-abc.scope\n"); got != "system.slice/docker-abc.scope" {
		t.Errorf("own cgroup: %q", got)
	}
}

// fakeContainer arranges a Docker container with the given cgroup files at
// the root of its cgroup namespace.
func fakeContainer(t *testing.T, cgroup map[string]string) {
	t.Helper()
	files := map[string]string{"fs/cgroup/cgroup.controllers": "cpu memory io\n"}
	for k, v := range cgroup {
		files["fs/cgroup/"+k] = v
	}
	fakeSys(t, files)
	fakeProc(t, map[string]string{"self/cgroup": "0::/\n", "1/cgroup": "0::/\n"})
	root := fakeTree(t, map[string]string{".dockerenv": ""})
	old := fsRoot
	fsRoot = root
	t.Cleanup(func() { fsRoot = old })
}

func TestContainerLimitsReadAgainstTheCgroup(t *testing.T) {
	fakeContainer(t, map[string]string{
		"memory.max":     "1073741824\n",
		"memory.current": "629145600\n",
		"memory.stat":    "anon 400000000\nfile 200000000\ninactive_file 104857600\n",
		"cpu.max":        "50000 100000\n",
		"cpu.stat":       "usage_usec 1000000\nuser_usec 700000\nsystem_usec 300000\n",
	})
	cg, err := readContainerLimits()
	if err != nil || cg == nil {
		t.Fatalf("got %+v, %v", cg, err)
	}
	if cg.kind != "docker" || cg.memLimit != 1<<30 || cg.memUsed != 524288000 || cg.cpuLimit != 0.5 || cg.usageUsec != 1000000 {
		t.Errorf("reading: %+v", cg)
	}

	// Two readings one second apart, the container having used a quarter of
	// a core: that is half of its half-core quota, whatever the 8-core host
	// is doing.
	prev := &raw{cores: 8, cgroup: &cgroupReading{kind: "docker", cpuLimit: 0.5, usageUsec: 1000000, userUsec: 700000, sysUsec: 300000, haveCPU: true}}
	cur := &raw{cores: 8, haveMem: true, memory: model.HostMemory{TotalBytes: 16 << 30, UsedBytes: 8 << 30}, cgroup: cg}
	cur.cgroup.usageUsec, cur.cgroup.userUsec, cur.cgroup.sysUsec = 1250000, 900000, 350000
	cur.at = time.Unix(101, 0)
	prev.at = time.Unix(100, 0)
	m := snapshot("local", "", prev, cur)
	if m.Container == nil || m.Container.MemoryLimitBytes != 1<<30 || m.Container.CPULimitCores == nil || *m.Container.CPULimitCores != 0.5 {
		t.Fatalf("container: %+v", m.Container)
	}
	if m.Memory.TotalBytes != 1<<30 || m.Memory.UsedBytes != 524288000 {
		t.Errorf("memory measured against the host: %+v", m.Memory)
	}
	if m.CPU.UsagePct == nil || *m.CPU.UsagePct != 50 {
		t.Errorf("processor use: %v", m.CPU.UsagePct)
	}
	if m.CPU.IOWaitPct != nil {
		t.Error("the host's iowait was left beside the container's figures")
	}
}

func TestNoContainerNoLimits(t *testing.T) {
	fakeSys(t, map[string]string{"fs/cgroup/cgroup.controllers": "cpu memory\n", "fs/cgroup/memory.max": "1073741824\n"})
	fakeProc(t, map[string]string{"self/cgroup": "0::/\n", "1/cgroup": "0::/init.scope\n"})
	old := fsRoot
	fsRoot = t.TempDir()
	t.Cleanup(func() { fsRoot = old })
	// A process on the host is there to report the machine, even when its
	// own service has a memory cap.
	if cg, err := readContainerLimits(); err != nil || cg != nil {
		t.Errorf("got %+v, %v outside a container", cg, err)
	}
}

func TestContainerWithoutLimits(t *testing.T) {
	fakeContainer(t, map[string]string{"memory.max": "max\n", "cpu.max": "max 100000\n"})
	if cg, err := readContainerLimits(); err != nil || cg != nil {
		t.Errorf("an unlimited container still got limits: %+v, %v", cg, err)
	}
}

func TestParseZpoolList(t *testing.T) {
	got := parseZpoolList("tank\t3985729650688\t1979120929996\t2006608720692\tONLINE\nbackup\t1000\t900\t100\tDEGRADED\n\n")
	if len(got) != 2 || got[0].Name != "tank" || got[0].SizeBytes != 3985729650688 || got[0].Health != "ONLINE" || got[1].Health != "DEGRADED" {
		t.Errorf("got %+v", got)
	}
	if model.PoolHealthValue("ONLINE") != 0 || model.PoolHealthValue("DEGRADED") != 1 || model.PoolHealthValue("FAULTED") != 2 || model.PoolHealthValue("UNAVAIL") != 2 {
		t.Error("pool health values")
	}
}

// ZFS datasets are folded into their pool, btrfs subvolumes are one device
// counted once, and an overlay at / (Docker) is measured while every other
// overlay is skipped. zpool is asked for the pool's health only because ZFS
// is mounted.
func TestReadMountsZFSBtrfsOverlay(t *testing.T) {
	fakeProc(t, map[string]string{"mounts": `overlay / overlay rw,lowerdir=/x 0 0
overlay /var/lib/docker/overlay2/abc/merged overlay rw 0 0
tank /tank zfs rw 0 0
tank/media /tank/media zfs rw 0 0
tank/home /home zfs rw 0 0
backup/snaps /backup zfs rw 0 0
/dev/sdb2 /data btrfs rw,subvol=/@data 0 0
/dev/sdb2 /data/snapshots btrfs rw,subvol=/@snaps 0 0
`})
	// Every ZFS dataset of tank sees 300 KiB of pool space left; each refers
	// to a different amount itself.
	used := map[string]uint64{"/tank": 10, "/tank/media": 500, "/home": 90, "/backup": 50}
	restore := statfs
	statfs = func(path string, st *syscall.Statfs_t) error {
		u := used[path]
		if u == 0 {
			u = 40
		}
		*st = syscall.Statfs_t{Bsize: 1024, Blocks: u + 300, Bfree: 300, Bavail: 300, Files: 1000, Ffree: 900}
		return nil
	}
	t.Cleanup(func() { statfs = restore })

	fs, warnings := readMounts()
	if len(warnings) > 0 {
		t.Errorf("warnings: %v", warnings)
	}
	byMount := map[string]model.HostFilesystem{}
	for _, f := range fs {
		byMount[f.Mount] = f
	}
	if len(fs) != 4 {
		t.Fatalf("got %d filesystems %+v; want /, /data, /tank and /backup", len(fs), fs)
	}
	if _, ok := byMount["/"]; !ok {
		t.Error("the container's overlay root was not reported")
	}
	tank := byMount["/tank"]
	if tank.Device != "tank" || tank.Datasets != 3 || tank.UsedBytes != 600*1024 || tank.FreeBytes != 300*1024 || tank.TotalBytes != 900*1024 || tank.InodesUsedPct != nil {
		t.Errorf("tank: %+v", tank)
	}
	if b := byMount["/backup"]; b.Device != "backup" || b.Datasets != 1 {
		t.Errorf("backup: %+v", b)
	}
	if d := byMount["/data"]; d.Device != "/dev/sdb2" {
		t.Errorf("btrfs: %+v", d)
	}
}

func TestReadPoolsWithoutZpool(t *testing.T) {
	restore := runZpool
	t.Cleanup(func() { runZpool = restore })
	runZpool = func(context.Context) ([]byte, error) { return nil, errNoZpool }
	if pools, err := readPools(context.Background()); err != nil || pools != nil {
		t.Errorf("a machine without zpool: %+v, %v", pools, err)
	}
	runZpool = func(context.Context) ([]byte, error) { return nil, errors.New("signal: killed") }
	if _, err := readPools(context.Background()); err == nil {
		t.Error("a zpool that timed out was not reported")
	}
}

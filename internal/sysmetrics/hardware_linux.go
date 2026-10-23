//go:build linux

package sysmetrics

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// The readings in this file are the ones only Linux exposes in a form worth
// taking: the temperature of every sensor the kernel knows about, the
// kernel's pressure-stall figures, the limits of the container a reading is
// taken in, and the health of ZFS pools. Each is best effort and quiet about
// it — a machine with no sensors, a kernel built without PSI, or a box that
// has never heard of ZFS is ordinary, not a collector problem, so only a
// reading that exists and cannot be read lands in the warnings.

/* ---------------- Temperatures ---------------- */

// tempPlausible bounds what is kept from a sensor. An unconnected input on a
// motherboard chip typically reads -128 °C or 127 °C, or something equally
// physical-looking and impossible; a reading outside this range is a sensor
// that is not there, and alerting on it would be alerting on a ghost.
const (
	tempMinPlausible = -40
	tempMaxPlausible = 150
)

// readTemperatures lists every hwmon temperature input and every thermal zone
// that hwmon does not already cover. hwmon is the richer of the two (each
// chip names its sensors, and declares its own limits), so a thermal zone is
// kept only when no hwmon chip carries the same name: on most PCs "acpitz" and
// "x86_pkg_temp" appear in both places, while on a Raspberry Pi or a router
// the thermal zone is all there is.
func readTemperatures() []model.HostTemperature {
	hw, chips := readHwmon(sysRoot + "/class/hwmon")
	return append(hw, readThermalZones(sysRoot+"/class/thermal", chips)...)
}

// readHwmon reads /sys/class/hwmon/hwmon*/temp*_input. A sensor is named
// "<chip>/<label>": the chip's name file ("coretemp", "nvme", "k10temp") and
// the input's label file ("Package id 0", "Composite"), or the input's own
// name ("temp1") when the chip gives no label. Two chips with the same name —
// two NVMe drives, two processor sockets — are told apart by the device they
// belong to ("nvme-nvme0", "nvme-nvme1"), so each keeps its own metric key
// from one reading to the next. It also returns the chip names it saw, in the
// form the thermal zones are compared against.
func readHwmon(dir string) ([]model.HostTemperature, map[string]bool) {
	chips := map[string]bool{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, chips
	}
	type chip struct{ dir, name, device string }
	var found []chip
	count := map[string]int{}
	for _, e := range entries {
		d := filepath.Join(dir, e.Name())
		name := readTrimmed(filepath.Join(d, "name"))
		if name == "" {
			name = e.Name()
		}
		device := ""
		if target, err := os.Readlink(filepath.Join(d, "device")); err == nil {
			device = filepath.Base(target)
		}
		found = append(found, chip{d, name, device})
		count[name]++
		chips[zoneKey(name)] = true
	}
	sort.Slice(found, func(i, j int) bool { return found[i].dir < found[j].dir })

	var out []model.HostTemperature
	for _, c := range found {
		prefix := c.name
		if count[c.name] > 1 {
			suffix := c.device
			if suffix == "" {
				suffix = filepath.Base(c.dir)
			}
			prefix += "-" + suffix
		}
		// Older drivers put the inputs on the device rather than the hwmon
		// node itself; the newer layout wins when both exist.
		inputs, _ := filepath.Glob(filepath.Join(c.dir, "temp*_input"))
		base := c.dir
		if len(inputs) == 0 {
			inputs, _ = filepath.Glob(filepath.Join(c.dir, "device", "temp*_input"))
			base = filepath.Join(c.dir, "device")
		}
		sort.Strings(inputs)
		for _, in := range inputs {
			stem := strings.TrimSuffix(filepath.Base(in), "_input") // "temp1"
			v, ok := readMilli(in)
			if !ok {
				continue
			}
			label := readTrimmed(filepath.Join(base, stem+"_label"))
			if label == "" {
				label = stem
			}
			t := model.HostTemperature{Name: prefix + "/" + label, Celsius: v}
			if hi, ok := readMilli(filepath.Join(base, stem+"_max")); ok && hi > 0 {
				t.HighCelsius = &hi
			}
			if crit, ok := readMilli(filepath.Join(base, stem+"_crit")); ok && crit > 0 {
				t.CritCelsius = &crit
			}
			out = append(out, t)
		}
	}
	return dedupeTemperatures(out), chips
}

// readThermalZones reads /sys/class/thermal/thermal_zone*/temp, naming each
// zone "thermal/<type>". Zones whose type is already a hwmon chip are left
// out (see readTemperatures). A zone's trip point of type "critical" is the
// limit it declares.
func readThermalZones(dir string, hwmonChips map[string]bool) []model.HostTemperature {
	zones, _ := filepath.Glob(filepath.Join(dir, "thermal_zone*"))
	sort.Slice(zones, func(i, j int) bool { return zoneNumber(zones[i]) < zoneNumber(zones[j]) })
	var out []model.HostTemperature
	for _, z := range zones {
		typ := readTrimmed(filepath.Join(z, "type"))
		if typ == "" {
			typ = filepath.Base(z)
		}
		if hwmonChips[zoneKey(typ)] {
			continue
		}
		v, ok := readMilli(filepath.Join(z, "temp"))
		if !ok {
			continue
		}
		t := model.HostTemperature{Name: "thermal/" + typ, Celsius: v}
		trips, _ := filepath.Glob(filepath.Join(z, "trip_point_*_type"))
		for _, tp := range trips {
			if readTrimmed(tp) != "critical" {
				continue
			}
			if crit, ok := readMilli(strings.TrimSuffix(tp, "_type") + "_temp"); ok && crit > 0 {
				t.CritCelsius = &crit
				break
			}
		}
		out = append(out, t)
	}
	return dedupeTemperatures(out)
}

// zoneKey normalises a chip or zone name for comparison: the Raspberry Pi's
// thermal zone is "cpu-thermal" and its hwmon chip "cpu_thermal".
func zoneKey(name string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), "-", "_"))
}

func zoneNumber(path string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(filepath.Base(path), "thermal_zone"))
	return n
}

// dedupeTemperatures numbers repeated names ("thermal/acpitz", "thermal/acpitz
// 2") so every sensor keeps a key of its own.
func dedupeTemperatures(in []model.HostTemperature) []model.HostTemperature {
	seen := map[string]int{}
	for i := range in {
		seen[in[i].Name]++
		if n := seen[in[i].Name]; n > 1 {
			in[i].Name = fmt.Sprintf("%s %d", in[i].Name, n)
		}
	}
	return in
}

// readMilli reads a sysfs millidegree value as degrees, rejecting the
// readings tempMinPlausible and tempMaxPlausible describe. A sensor whose
// driver cannot reach it right now fails the read itself (EIO, ENODATA),
// which lands here as not ok too.
func readMilli(path string) (float64, bool) {
	s := readTrimmed(path)
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	c := v / 1000
	if c < tempMinPlausible || c > tempMaxPlausible {
		return 0, false
	}
	return c, true
}

func readTrimmed(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

/* ---------------- Pressure stall information ---------------- */

// readPressure reads /proc/pressure/{cpu,memory,io}. A kernel without PSI
// (before 4.20, or built or booted without it) has no such directory, which
// is not worth a warning.
func readPressure() []model.HostPressure {
	var out []model.HostPressure
	for _, res := range []string{"cpu", "memory", "io"} {
		b, err := os.ReadFile(procRoot + "/pressure/" + res)
		if err != nil {
			continue
		}
		if p, ok := parsePressure(res, string(b)); ok {
			out = append(out, p)
		}
	}
	return out
}

// parsePressure reads one PSI file:
//
//	some avg10=0.12 avg60=0.40 avg300=0.33 total=123456
//	full avg10=0.00 avg60=0.10 avg300=0.08 total=6543
//
// The "full" line is kept for memory and io only. The kernel prints one for
// cpu as well since 5.13, but at the level of the whole machine it is always
// zero and would only be a reading that never moves.
func parsePressure(resource, text string) (model.HostPressure, bool) {
	p := model.HostPressure{Resource: resource}
	haveSome := false
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		vals := map[string]float64{}
		for _, f := range fields[1:] {
			k, v, ok := strings.Cut(f, "=")
			if !ok {
				continue
			}
			if n, err := strconv.ParseFloat(v, 64); err == nil {
				vals[k] = n
			}
		}
		a10, ok10 := vals["avg10"]
		a60, ok60 := vals["avg60"]
		if !ok10 || !ok60 {
			continue
		}
		switch fields[0] {
		case "some":
			p.Avg10, p.Avg60, haveSome = clampPct(a10), clampPct(a60), true
		case "full":
			if resource != "cpu" {
				f10, f60 := clampPct(a10), clampPct(a60)
				p.FullAvg10, p.FullAvg60 = &f10, &f60
			}
		}
	}
	return p, haveSome
}

/* ---------------- Container limits ---------------- */

// fsRoot is where the files that betray a container (/.dockerenv and
// friends) are looked for. Tests point it at a temporary directory.
var fsRoot = "/"

// containerKind reports what sort of container this process runs in, or ""
// when it does not appear to run in one. The markers are the ones the
// runtimes leave on purpose: Docker's /.dockerenv, Podman's
// /run/.containerenv, and systemd's /run/systemd/container, which LXC,
// systemd-nspawn and most others write. Failing those, the cgroup path of
// pid 1 still usually names the runtime.
func containerKind() string {
	if _, err := os.Stat(filepath.Join(fsRoot, ".dockerenv")); err == nil {
		return "docker"
	}
	if _, err := os.Stat(filepath.Join(fsRoot, "run", ".containerenv")); err == nil {
		return "podman"
	}
	if s := readTrimmed(filepath.Join(fsRoot, "run", "systemd", "container")); s != "" {
		return strings.ToLower(s)
	}
	cg := readTrimmed(procRoot + "/1/cgroup")
	switch {
	case strings.Contains(cg, "kubepods"):
		return "kubernetes"
	case strings.Contains(cg, "docker"):
		return "docker"
	case strings.Contains(cg, "libpod"):
		return "podman"
	case strings.Contains(cg, "/lxc"):
		return "lxc"
	}
	return ""
}

// readContainerLimits reads the cgroup v2 limits that apply to this process,
// when it runs in a container. A process on the host is not measured this way
// even if a systemd unit gives it a memory cap: it is there to report the
// machine, and the machine is what /proc describes. Under a host root the
// agent is deliberately reading the host around it, so this is skipped too.
//
// The limits are the tightest found on the way from the process's own cgroup
// up to the root of the hierarchy it can see, since a pod or a parent slice
// can carry the cap rather than the container's leaf. cgroup v1 is not read:
// every distribution of the last few years defaults to v2, and v1's split
// hierarchies would double this code for the machines least likely to run a
// current agent.
func readContainerLimits() (*cgroupReading, error) {
	if hostRoot != "" {
		return nil, nil
	}
	kind := containerKind()
	if kind == "" {
		return nil, nil
	}
	root := sysRoot + "/fs/cgroup"
	if _, err := os.Stat(filepath.Join(root, "cgroup.controllers")); err != nil {
		return nil, nil // cgroup v1, or no cgroup filesystem mounted
	}
	leaf := root
	if rel := ownCgroup(readTrimmed(procRoot + "/self/cgroup")); rel != "" {
		if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
			leaf = filepath.Join(root, rel)
		}
	}

	cg := &cgroupReading{kind: kind}
	for dir := leaf; ; dir = filepath.Dir(dir) {
		if v, ok := parseMemoryMax(readTrimmed(filepath.Join(dir, "memory.max"))); ok && (cg.memLimit == 0 || v < cg.memLimit) {
			cg.memLimit = v
		}
		if v, ok := parseCPUMax(readTrimmed(filepath.Join(dir, "cpu.max"))); ok && (cg.cpuLimit == 0 || v < cg.cpuLimit) {
			cg.cpuLimit = v
		}
		if dir == root || len(dir) <= len(root) {
			break
		}
	}
	if cg.memLimit == 0 && cg.cpuLimit == 0 {
		return nil, nil
	}

	if cg.memLimit > 0 {
		cur, err := strconv.ParseUint(readTrimmed(filepath.Join(leaf, "memory.current")), 10, 64)
		if err != nil {
			return nil, errors.New("the container's memory.current cannot be read")
		}
		stat := parseFlatKeyed(readTrimmed(filepath.Join(leaf, "memory.stat")))
		// What docker stats and the kernel's own OOM accounting call used:
		// the cgroup's charge less the file cache it can drop without
		// killing anything — the same reasoning as MemAvailable on the host.
		inactive := stat["inactive_file"]
		if inactive > cur {
			inactive = cur
		}
		cg.memUsed = cur - inactive
		cg.memCache = stat["file"]
	}
	if cg.cpuLimit > 0 {
		stat := parseFlatKeyed(readTrimmed(filepath.Join(leaf, "cpu.stat")))
		if u, ok := stat["usage_usec"]; ok {
			cg.usageUsec, cg.userUsec, cg.sysUsec, cg.haveCPU = u, stat["user_usec"], stat["system_usec"], true
		}
	}
	return cg, nil
}

// ownCgroup picks the unified-hierarchy path out of /proc/self/cgroup, whose
// v2 line is "0::/some/path". Inside a container with its own cgroup
// namespace the path is "/", and /sys/fs/cgroup is already the container's.
func ownCgroup(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "0::"); ok {
			return strings.TrimPrefix(rest, "/")
		}
	}
	return ""
}

// parseMemoryMax reads memory.max: a byte count, or "max" for no limit.
func parseMemoryMax(s string) (uint64, bool) {
	if s == "" || s == "max" {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil || v == 0 {
		return 0, false
	}
	return v, true
}

// parseCPUMax reads cpu.max, "<quota> <period>" in microseconds or "max
// <period>" for no limit, as a number of cores: "150000 100000" is 1.5.
func parseCPUMax(s string) (float64, bool) {
	fields := strings.Fields(s)
	if len(fields) == 0 || fields[0] == "max" {
		return 0, false
	}
	quota, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || quota <= 0 {
		return 0, false
	}
	period := 100000.0
	if len(fields) > 1 {
		if p, err := strconv.ParseFloat(fields[1], 64); err == nil && p > 0 {
			period = p
		}
	}
	return quota / period, true
}

// parseFlatKeyed reads the "name value" lines of memory.stat and cpu.stat.
func parseFlatKeyed(text string) map[string]uint64 {
	out := map[string]uint64{}
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 2 {
			continue
		}
		if v, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
			out[fields[0]] = v
		}
	}
	return out
}

/* ---------------- ZFS ---------------- */

// zpoolTimeout bounds the one external command the collector runs. zpool
// list reads state the kernel module already holds and normally answers in a
// few milliseconds; a pool whose disks have stopped answering can make it
// hang, and the reading must not hang with it.
const zpoolTimeout = 3 * time.Second

// runZpool runs `zpool list` for the pools' health and raw sizes. It is a
// variable so a test can answer for a machine with no ZFS.
var runZpool = func(ctx context.Context) ([]byte, error) {
	path, err := exec.LookPath("zpool")
	if err != nil {
		return nil, errNoZpool
	}
	ctx, cancel := context.WithTimeout(ctx, zpoolTimeout)
	defer cancel()
	return exec.CommandContext(ctx, path, "list", "-H", "-p", "-o", "name,size,alloc,free,health").Output()
}

var errNoZpool = errors.New("zpool is not installed")

// readPools asks zpool for each pool's health. It is only called when a ZFS
// filesystem is mounted, so a machine without ZFS never runs anything; and a
// machine with ZFS but without the zpool command (an agent in a container,
// typically) simply has no pool readings.
func readPools(ctx context.Context) ([]model.HostPool, error) {
	out, err := runZpool(ctx)
	if errors.Is(err, errNoZpool) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parseZpoolList(string(out)), nil
}

// parseZpoolList reads `zpool list -H -p -o name,size,alloc,free,health`:
// one tab-separated line per pool, sizes in bytes.
func parseZpoolList(text string) []model.HostPool {
	var out []model.HostPool
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Split(strings.TrimSpace(line), "\t")
		if len(fields) < 5 || fields[0] == "" {
			continue
		}
		num := func(s string) uint64 {
			v, _ := strconv.ParseUint(s, 10, 64)
			return v
		}
		out = append(out, model.HostPool{
			Name: fields[0], SizeBytes: num(fields[1]), AllocBytes: num(fields[2]), FreeBytes: num(fields[3]),
			Health: strings.ToUpper(fields[4]),
		})
	}
	return out
}

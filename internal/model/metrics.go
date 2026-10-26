package model

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// A hardware check reads a whole machine in one go, but what it reads are
// separate things: the processor, the memory, each disk, each interface. Each
// of those is a metric with a key, a unit, its own thresholds and its own
// verdict, so that "disk /srv is filling" and "memory is fine" can be true at
// the same time on the same check and each can be charted, alerted on and
// asked about by name.
//
// A key is the family on its own for the readings a machine has exactly one
// of ("cpu", "memory", "swap", "load"), or family:instance for the ones it may
// have several of: "disk:/srv", "inodes:/srv", "net:eth0.rx", "net:eth0.tx",
// "diskio:sda.read", "diskio:sda.write", "diskio:sda.busy",
// "temp:coretemp/Package id 0", "psi:memory.avg10", "pool:tank",
// "service:nginx", "process:plex", "docker:web", "docker-restarts:web",
// "docker-cpu:web", "docker-memory:web", "vm:dc01", "vm-cpu:dc01",
// "vm-memory:dc01".

// The metric families a hardware check reports.
const (
	MetricCPU    = "cpu"    // processor use, %
	MetricMemory = "memory" // memory use, %
	MetricSwap   = "swap"   // swap use, %
	MetricLoad   = "load"   // load average per core
	MetricDisk   = "disk"   // filesystem space used, % — one instance per mount
	MetricInodes = "inodes" // filesystem inodes used, % — one instance per mount
	MetricNet    = "net"    // interface throughput, bytes/s — instances <name>.rx and <name>.tx
	MetricDiskIO = "diskio" // block device throughput — instances <name>.read, <name>.write (bytes/s) and <name>.busy (%)
	MetricTemp   = "temp"   // sensor temperature, °C — one instance per sensor (Linux)
	MetricPSI    = "psi"    // pressure stall, % — instances <cpu|memory|io>.avg10 and .avg60 (Linux)
	MetricPool   = "pool"   // ZFS pool health — one instance per pool: 0 healthy, 1 degraded, 2 faulted (Linux)

	// Services and processes are only read for the names a machine is told
	// to watch (CollectSelection.Services and .Processes), so a machine
	// nobody has asked about reports none and spends nothing finding them.
	MetricService = "service" // a watched service — one instance per service: 1 running, 0 stopped or missing
	MetricProcess = "process" // a watched program — one instance per executable name: how many copies are running

	// Containers and virtual machines follow the same rule: only the names a
	// machine is told to watch (CollectSelection.Containers and .VMs). Each
	// container or guest is a state reading and up to three more, each its
	// own family so that its thresholds are its own: a restart count and a
	// memory percentage cannot share a level.
	MetricDocker         = "docker"          // a watched container — 2 running, 1 unhealthy, paused or restarting, 0 stopped or missing
	MetricDockerRestarts = "docker-restarts" // a watched container's restarts in the last hour
	MetricDockerCPU      = "docker-cpu"      // a watched container's processor use, % of its limit (or of the machine)
	MetricDockerMemory   = "docker-memory"   // a watched container's memory use, % of its limit (or of the machine)
	MetricVM             = "vm"              // a watched guest — 2 running, 1 paused, saved or changing state, 0 off or missing
	MetricVMCPU          = "vm-cpu"          // a watched guest's processor use, % of its virtual processors
	MetricVMMemory       = "vm-memory"       // a watched guest's memory use, % of what the host assigned it
)

// MetricFamilies lists every family, in the order the editor shows them.
var MetricFamilies = []string{MetricCPU, MetricMemory, MetricSwap, MetricLoad, MetricDisk, MetricInodes, MetricNet, MetricDiskIO, MetricTemp, MetricPSI, MetricPool, MetricService, MetricProcess,
	MetricDocker, MetricDockerRestarts, MetricDockerCPU, MetricDockerMemory, MetricVM, MetricVMCPU, MetricVMMemory}

// MetricFamilyLabel is the family's name as a person reads it.
func MetricFamilyLabel(family string) string {
	switch family {
	case MetricCPU:
		return "Processor"
	case MetricMemory:
		return "Memory"
	case MetricSwap:
		return "Swap"
	case MetricLoad:
		return "Load per core"
	case MetricDisk:
		return "Disk"
	case MetricInodes:
		return "Inodes"
	case MetricNet:
		return "Network"
	case MetricDiskIO:
		return "Disk I/O"
	case MetricTemp:
		return "Temperature"
	case MetricPSI:
		return "Pressure stall"
	case MetricPool:
		return "Storage pool health"
	case MetricService:
		return "Service"
	case MetricProcess:
		return "Process"
	case MetricDocker:
		return "Container"
	case MetricDockerRestarts:
		return "Container restarts"
	case MetricDockerCPU:
		return "Container processor"
	case MetricDockerMemory:
		return "Container memory"
	case MetricVM:
		return "Virtual machine"
	case MetricVMCPU:
		return "Virtual machine processor"
	case MetricVMMemory:
		return "Virtual machine memory"
	}
	return family
}

// MetricFamilyHasInstances reports whether a family is keyed per disk,
// interface or device rather than being one reading for the whole machine.
func MetricFamilyHasInstances(family string) bool {
	switch family {
	case MetricDisk, MetricInodes, MetricNet, MetricDiskIO, MetricTemp, MetricPSI, MetricPool, MetricService, MetricProcess,
		MetricDocker, MetricDockerRestarts, MetricDockerCPU, MetricDockerMemory, MetricVM, MetricVMCPU, MetricVMMemory:
		return true
	}
	return false
}

// SplitMetricKey separates "disk:/srv" into its family and instance. A key
// with no colon is a family on its own and has an empty instance.
func SplitMetricKey(key string) (family, instance string) {
	family, instance, _ = strings.Cut(key, ":")
	return family, instance
}

// MetricKey joins a family and an instance the way keys are written.
func MetricKey(family, instance string) string {
	if instance == "" {
		return family
	}
	return family + ":" + instance
}

// SystemMetricUnit is the unit of a hardware metric, worked out from its key.
// Percentages and rates never share an axis, so a chart needs to know which
// one a key is before it has seen a single value.
func SystemMetricUnit(key string) string {
	family, instance := SplitMetricKey(key)
	switch family {
	case MetricCPU, MetricMemory, MetricSwap, MetricDisk, MetricInodes, MetricPSI,
		MetricDockerCPU, MetricDockerMemory, MetricVMCPU, MetricVMMemory:
		return "%"
	case MetricTemp:
		return "°C"
	case MetricPool:
		// A state rather than a measurement: 0, 1 or 2 (PoolHealthValue).
		return "state"
	case MetricService:
		// Also a state: 1 running, 0 not (ServiceValue).
		return "running"
	case MetricProcess, MetricDockerRestarts:
		return "count"
	case MetricDocker:
		// States again: 2 running, 1 not well, 0 stopped (DockerValue,
		// VMValue), each worded in its own way.
		return "container"
	case MetricVM:
		return "guest"
	case MetricLoad:
		return ""
	case MetricNet:
		return "B/s"
	case MetricDiskIO:
		if strings.HasSuffix(instance, ".busy") {
			return "%"
		}
		return "B/s"
	}
	return ""
}

// metricIsPercentage reports whether a key's readings are 0..100, which is
// what bounds its thresholds.
func metricIsPercentage(key string) bool {
	return SystemMetricUnit(key) == "%"
}

// MetricResult is one metric's reading and verdict within a single run.
type MetricResult struct {
	Key    string  `json:"key"`   // "cpu", "memory", "swap", "load", "disk:/srv", "inodes:/srv", "net:eth0.rx", "net:eth0.tx", "diskio:nvme0n1.read", ...
	Label  string  `json:"label"` // "Processor", "Disk /srv"
	Value  float64 `json:"value"`
	Unit   string  `json:"unit,omitempty"`
	Status Status  `json:"status"` // this metric's own verdict
	Reason string  `json:"reason,omitempty"`
}

// MetricThreshold is the warning and critical levels for one metric family
// ("disk", applying to every filesystem) or one instance ("disk:/srv", which
// then overrides the family entry for that filesystem alone). A nil level is
// off. Levels are crossed at or above the number, or at or below it when
// Below is set — for a reading where less is worse.
type MetricThreshold struct {
	Metric string   `json:"metric"`
	Warn   *float64 `json:"warn,omitempty"`
	Crit   *float64 `json:"crit,omitempty"`
	Below  bool     `json:"below,omitempty"`
}

// Off reports whether the entry sets no level at all.
func (t MetricThreshold) Off() bool { return t.Warn == nil && t.Crit == nil }

// Judge returns the verdict for a reading against this threshold: down at or
// past the critical level, degraded at or past the warning level, otherwise
// up. A level set to 0 on an "above" threshold is off, the way the flat
// fields that preceded the list treated 0, so an old configuration that said
// "0 means off" still does.
func (t MetricThreshold) Judge(value float64) Status {
	past := func(level *float64) bool {
		if level == nil {
			return false
		}
		if t.Below {
			return value <= *level
		}
		return *level > 0 && value >= *level
	}
	switch {
	case past(t.Crit):
		return StatusDown
	case past(t.Warn):
		return StatusDegraded
	}
	return StatusUp
}

// EffectiveMetricThresholds is the list the check actually evaluates: the
// configured list when there is one, otherwise the flat fields a hardware
// check had before the list existed, converted with the same meaning (0 is
// off; inodes followed the disk pair, and were only watched when the disk
// critical level was set).
func (c CheckConfig) EffectiveMetricThresholds() []MetricThreshold {
	if len(c.MetricThresholds) > 0 {
		return c.MetricThresholds
	}
	return LegacyMetricThresholds(c)
}

// LegacyMetricThresholds converts the deprecated flat threshold fields into
// list entries, one per family that had any level set.
func LegacyMetricThresholds(c CheckConfig) []MetricThreshold {
	level := func(v float64) *float64 {
		if v <= 0 {
			return nil
		}
		return Float(v)
	}
	var out []MetricThreshold
	add := func(metric string, warn, crit float64) {
		t := MetricThreshold{Metric: metric, Warn: level(warn), Crit: level(crit)}
		if !t.Off() {
			out = append(out, t)
		}
	}
	add(MetricCPU, c.CPUWarnPct, c.CPUCritPct)
	add(MetricMemory, c.MemWarnPct, c.MemCritPct)
	add(MetricSwap, c.SwapWarnPct, 0)
	add(MetricLoad, c.LoadWarnPerCore, c.LoadCritPerCore)
	add(MetricDisk, c.DiskWarnPct, c.DiskCritPct)
	if c.DiskCritPct > 0 {
		add(MetricInodes, c.DiskWarnPct, c.DiskCritPct)
	}
	return out
}

// ClearLegacyThresholds zeroes the deprecated flat fields, for a
// configuration that has been converted to the list.
func (c *CheckConfig) ClearLegacyThresholds() {
	c.CPUWarnPct, c.CPUCritPct = 0, 0
	c.MemWarnPct, c.MemCritPct = 0, 0
	c.SwapWarnPct = 0
	c.DiskWarnPct, c.DiskCritPct = 0, 0
	c.LoadWarnPerCore, c.LoadCritPerCore = 0, 0
}

// NormalizeMetricThresholds moves a hardware configuration onto the list once
// and for all: a configuration with only the flat fields gets them converted,
// and the flat fields are cleared either way so the two can never disagree.
func (c *CheckConfig) NormalizeMetricThresholds() {
	if len(c.MetricThresholds) == 0 {
		c.MetricThresholds = LegacyMetricThresholds(*c)
	}
	c.ClearLegacyThresholds()
}

// ThresholdFor finds the entry that governs one metric key: the instance's own
// entry when there is one, else its family's, else nothing (ok false). For a
// network or disk-I/O direction ("net:eth0.rx"), or a pressure average
// ("psi:memory.avg10"), an entry for the whole interface ("net:eth0") or
// resource ("psi:memory") counts as the instance's own.
func (c CheckConfig) ThresholdFor(key string) (MetricThreshold, bool) {
	list := c.EffectiveMetricThresholds()
	find := func(metric string) (MetricThreshold, bool) {
		for _, t := range list {
			if t.Metric == metric {
				return t, true
			}
		}
		return MetricThreshold{}, false
	}
	if t, ok := find(key); ok {
		return t, true
	}
	family, instance := SplitMetricKey(key)
	if i := strings.LastIndex(instance, "."); i > 0 && (family == MetricNet || family == MetricDiskIO || family == MetricPSI) {
		if t, ok := find(MetricKey(family, instance[:i])); ok {
			return t, true
		}
	}
	if instance != "" {
		return find(family)
	}
	return MetricThreshold{}, false
}

// ValidateMetricThresholds reports a threshold list a hardware check could
// not honour: an unknown family, an instance on a family that has none, a
// percentage outside 0..100, a negative rate, a duplicate, or a critical
// level that would fire before its warning.
func ValidateMetricThresholds(list []MetricThreshold) error {
	seen := map[string]bool{}
	known := map[string]bool{}
	for _, f := range MetricFamilies {
		known[f] = true
	}
	for _, t := range list {
		key := strings.TrimSpace(t.Metric)
		if key == "" {
			return errors.New("a threshold must name the metric it applies to")
		}
		family, instance := SplitMetricKey(key)
		if !known[family] {
			return fmt.Errorf("unknown hardware metric %q (use %s)", key, strings.Join(MetricFamilies, ", "))
		}
		if instance != "" && !MetricFamilyHasInstances(family) {
			return fmt.Errorf("%s is one reading for the whole machine, so %q cannot name an instance", MetricFamilyLabel(family), key)
		}
		if seen[key] {
			return fmt.Errorf("two thresholds both apply to %q", key)
		}
		seen[key] = true
		label := MetricFamilyLabel(family)
		if instance != "" {
			label += " " + instance
		}
		for _, l := range []struct {
			name  string
			level *float64
		}{{"warning", t.Warn}, {"critical", t.Crit}} {
			if l.level == nil {
				continue
			}
			switch {
			case metricIsPercentage(key) && (*l.level < 0 || *l.level > 100):
				return fmt.Errorf("the %s %s threshold must be between 0 (off) and 100", label, l.name)
			case *l.level < 0:
				return fmt.Errorf("the %s %s threshold cannot be negative", label, l.name)
			}
		}
		// A critical threshold that fires before its warning would report
		// the machine down before it ever reported it degraded, which is not
		// what anyone means by those two words.
		if t.Warn != nil && t.Crit != nil && *t.Warn > 0 && *t.Crit > 0 {
			if !t.Below && *t.Crit < *t.Warn {
				return fmt.Errorf("the %s critical threshold must be at or above its warning threshold", label)
			}
			if t.Below && *t.Crit > *t.Warn {
				return fmt.Errorf("the %s critical threshold must be at or below its warning threshold when less is worse", label)
			}
		}
	}
	return nil
}

// SortMetricThresholds orders a list families first, in the order the
// editor shows them, then instances alphabetically within their family, so a
// saved configuration reads the same way twice.
func SortMetricThresholds(list []MetricThreshold) {
	rank := map[string]int{}
	for i, f := range MetricFamilies {
		rank[f] = i
	}
	sort.SliceStable(list, func(i, j int) bool {
		fi, ii := SplitMetricKey(list[i].Metric)
		fj, ij := SplitMetricKey(list[j].Metric)
		if fi != fj {
			return rank[fi] < rank[fj]
		}
		if (ii == "") != (ij == "") {
			return ii == ""
		}
		return ii < ij
	})
}

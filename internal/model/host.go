package model

import (
	"fmt"
	"strings"
	"time"
)

// HostKeyLocal identifies the machine GWatch itself runs on. Every other host
// is keyed "agent:<id>" (a machine that pushes to us) or "url:<check id>" (a
// machine we scrape).
const HostKeyLocal = "local"

// AgentHostKey returns the host key for a registered agent.
func AgentHostKey(agentID int64) string { return fmt.Sprintf("agent:%d", agentID) }

// URLHostKey returns the host key for a scraped endpoint belonging to a check.
func URLHostKey(checkID int64) string { return fmt.Sprintf("url:%d", checkID) }

// ParseAgentHostKey returns the agent id behind an "agent:<id>" key.
func ParseAgentHostKey(key string) (int64, bool) {
	rest, ok := strings.CutPrefix(key, "agent:")
	if !ok {
		return 0, false
	}
	var id int64
	if _, err := fmt.Sscanf(rest, "%d", &id); err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// HostSource says where a system check gets its hardware readings.
type HostSource string

const (
	// HostSourceLocal reads the machine GWatch runs on.
	HostSourceLocal HostSource = "local"
	// HostSourceAgent reads the newest report pushed by a registered agent.
	// The agent connects to GWatch; GWatch never connects to it.
	HostSourceAgent HostSource = "agent"
	// HostSourceURL scrapes a metrics endpoint the machine exposes, such as
	// gwatch-agent running in serve mode.
	HostSourceURL HostSource = "url"
)

// Valid reports whether the source is one the runner understands.
func (h HostSource) Valid() bool {
	switch h {
	case HostSourceLocal, HostSourceAgent, HostSourceURL:
		return true
	}
	return false
}

// HostMetrics is one hardware-health observation for a machine. It is the wire
// format shared by the collector, the agent, the ingest API, the database and
// the web interface, so a field added here shows up in all of them.
//
// Rates (anything named …PerSec or UsagePct) are derived by the collector from
// two consecutive readings on the machine being measured, never by subtracting
// across the network: only the machine itself sees an unbroken counter series.
// They are pointers because the very first reading has nothing to compare
// against, and because a platform may not expose that counter at all.
type HostMetrics struct {
	Key          string     `json:"key"`
	Hostname     string     `json:"hostname"`
	OS           string     `json:"os"`
	Arch         string     `json:"arch"`
	Platform     string     `json:"platform,omitempty"` // "Ubuntu 24.04", "Windows 11 Pro"
	Kernel       string     `json:"kernel,omitempty"`
	AgentVersion string     `json:"agentVersion,omitempty"`
	Timestamp    time.Time  `json:"ts"`
	BootTime     *time.Time `json:"bootTime,omitempty"`
	UptimeSecs   float64    `json:"uptimeSeconds,omitempty"`

	CPU         HostCPU          `json:"cpu"`
	Memory      HostMemory       `json:"memory"`
	Filesystems []HostFilesystem `json:"filesystems,omitempty"`
	Interfaces  []HostInterface  `json:"interfaces,omitempty"`
	Disks       []HostDiskIO     `json:"disks,omitempty"`

	// Temperatures, Pressure and Pools are Linux readings: every hardware
	// monitoring chip and thermal zone the kernel exposes, the kernel's
	// pressure-stall figures, and the health of each ZFS pool. A platform or
	// a machine without them simply leaves them out.
	Temperatures []HostTemperature `json:"temperatures,omitempty"`
	Pressure     []HostPressure    `json:"pressure,omitempty"`
	Pools        []HostPool        `json:"pools,omitempty"`

	// Services and Processes are the ones this machine was told to watch,
	// and only those: each named service with whether it is running, and
	// each named program with how many copies of it are. Both are empty
	// unless someone chose names (CollectSelection.Services, .Processes),
	// which is what keeps them free for every machine nobody asked about.
	Services  []HostService `json:"services,omitempty"`
	Processes []HostProcess `json:"processes,omitempty"`

	// Container is set when the reading was taken inside a container that has
	// its own memory or processor limit. Memory and processor use are then
	// measured against that limit rather than the host's totals: a container
	// capped at 2 GiB is out of memory at 2 GiB, whatever the machine around
	// it has spare.
	Container *HostContainer `json:"container,omitempty"`

	// Warnings records collector problems — a counter this platform does not
	// expose, a filesystem that could not be stat'ed. A partial reading is
	// still worth having, so these never fail the whole collection.
	Warnings []string `json:"warnings,omitempty"`
}

// HostCPU is processor utilisation and load.
type HostCPU struct {
	Cores     int      `json:"cores"`
	Model     string   `json:"model,omitempty"`
	UsagePct  *float64 `json:"usagePct,omitempty"` // 0..100 across all cores
	UserPct   *float64 `json:"userPct,omitempty"`
	SystemPct *float64 `json:"systemPct,omitempty"`
	IOWaitPct *float64 `json:"ioWaitPct,omitempty"`
	StealPct  *float64 `json:"stealPct,omitempty"`
	// Load averages, Unix only. LoadPerCore is Load1 divided by Cores, which
	// is the form worth alerting on since it means the same thing on a 2-core
	// box and a 64-core one.
	Load1       *float64 `json:"load1,omitempty"`
	Load5       *float64 `json:"load5,omitempty"`
	Load15      *float64 `json:"load15,omitempty"`
	LoadPerCore *float64 `json:"loadPerCore,omitempty"`
}

// HostMemory is RAM and swap usage in bytes.
//
// UsedBytes deliberately excludes reclaimable cache: on Linux it is derived
// from MemAvailable, so a box with 30 GB of page cache does not read as full.
type HostMemory struct {
	TotalBytes     uint64   `json:"totalBytes"`
	UsedBytes      uint64   `json:"usedBytes"`
	AvailableBytes uint64   `json:"availableBytes"`
	UsedPct        float64  `json:"usedPct"`
	CachedBytes    uint64   `json:"cachedBytes,omitempty"`
	SwapTotalBytes uint64   `json:"swapTotalBytes,omitempty"`
	SwapUsedBytes  uint64   `json:"swapUsedBytes,omitempty"`
	SwapUsedPct    *float64 `json:"swapUsedPct,omitempty"`
}

// HostFilesystem is space usage for one mounted filesystem.
type HostFilesystem struct {
	Mount         string   `json:"mount"`
	Device        string   `json:"device,omitempty"`
	FSType        string   `json:"fsType,omitempty"`
	TotalBytes    uint64   `json:"totalBytes"`
	UsedBytes     uint64   `json:"usedBytes"`
	FreeBytes     uint64   `json:"freeBytes"`
	UsedPct       float64  `json:"usedPct"`
	InodesTotal   uint64   `json:"inodesTotal,omitempty"`
	InodesUsed    uint64   `json:"inodesUsed,omitempty"`
	InodesUsedPct *float64 `json:"inodesUsedPct,omitempty"`
	// Datasets is how many ZFS datasets were folded into this entry. Every
	// dataset of a pool is its own mount sharing the pool's free space, so
	// listing them separately shows the same free figure a dozen times; they
	// are reported once, as the pool, under the dataset mounted closest to the
	// root. Zero for anything that is not ZFS.
	Datasets int `json:"datasets,omitempty"`
}

// HostTemperature is one temperature sensor. Name is the sensor's metric
// instance ("coretemp/Package id 0", "nvme/Composite", "thermal/acpitz"): the
// chip or zone it belongs to, then the sensor's own label.
type HostTemperature struct {
	Name    string  `json:"name"`
	Celsius float64 `json:"celsius"`
	// HighCelsius and CritCelsius are the limits the chip itself declares,
	// shown beside the reading. They are not thresholds: the hardware check's
	// configuration decides what alerts, and plenty of chips declare limits
	// that are nonsense.
	HighCelsius *float64 `json:"highCelsius,omitempty"`
	CritCelsius *float64 `json:"critCelsius,omitempty"`
}

// HostPressure is the kernel's pressure-stall information for one resource
// (cpu, memory or io): the share of recent wall-clock time in which at least
// one task was stalled waiting for it ("some"), and — for memory and io —
// the share in which every task was ("full"). Averaged over 10 and 60
// seconds, 0..100.
type HostPressure struct {
	Resource  string   `json:"resource"`
	Avg10     float64  `json:"avg10"`
	Avg60     float64  `json:"avg60"`
	FullAvg10 *float64 `json:"fullAvg10,omitempty"`
	FullAvg60 *float64 `json:"fullAvg60,omitempty"`
}

// HostPool is one ZFS pool as zpool reports it. The sizes are the pool's raw
// figures, parity and all, which is why usable space is read from the
// filesystem instead and these are only shown beside it.
type HostPool struct {
	Name       string `json:"name"`
	Health     string `json:"health"` // ONLINE, DEGRADED, FAULTED, UNAVAIL, ...
	SizeBytes  uint64 `json:"sizeBytes,omitempty"`
	AllocBytes uint64 `json:"allocBytes,omitempty"`
	FreeBytes  uint64 `json:"freeBytes,omitempty"`
}

// PoolHealthValue turns a pool's state into the number its metric carries: 0
// for a healthy pool, 1 for one still serving data with its redundancy gone
// (DEGRADED), 2 for one that is not serving it (FAULTED, UNAVAIL, SUSPENDED,
// and anything else zpool may print). A threshold of warn 1, crit 2 then
// reads "tell me when a disk drops out, and shout when the pool goes".
func PoolHealthValue(health string) float64 {
	switch strings.ToUpper(strings.TrimSpace(health)) {
	case "ONLINE":
		return 0
	case "DEGRADED":
		return 1
	}
	return 2
}

// HostService is one watched service: a systemd unit on Linux, a service in
// the Service Control Manager on Windows, a launchd job on macOS. Name is the
// name as it was asked for ("nginx"), which is also the metric instance, so
// the key stays what someone typed even where the system knows the service
// by a longer name ("nginx.service").
type HostService struct {
	Name    string `json:"name"`
	Running bool   `json:"running"`
	// State is the system's own word for it, shown beside the verdict:
	// "active (running)", "failed", "inactive (dead)" on Linux; "running",
	// "stopped", "start pending" on Windows; "running" or "not running"
	// (with the last exit status) on macOS. "not found" means the system
	// has no service by that name, which is reported as stopped: something
	// that was meant to be running is not, whatever the reason.
	State string `json:"state,omitempty"`
	// Description is the service's own one-line description, or its display
	// name on Windows, when the system has one.
	Description string `json:"description,omitempty"`
}

// ServiceValue is a service's metric: 1 running, 0 not. The way round is
// chosen for the threshold it makes natural — "critical at or below 0" is
// "critical when stopped" — and so that a chart of it reads up as up.
func ServiceValue(s HostService) float64 {
	if s.Running {
		return 1
	}
	return 0
}

// HostProcess is one watched program: how many processes are running with
// that executable name. Zero is a reading like any other — the program is
// not running — and the default threshold makes it critical.
type HostProcess struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// HostContainer describes the container a reading was taken in and the
// limits it was measured against. A limit left at zero (or nil) is no limit
// of that kind, and that reading is the host's.
type HostContainer struct {
	Kind             string   `json:"kind"` // docker, podman, lxc, kubernetes, container
	MemoryLimitBytes uint64   `json:"memoryLimitBytes,omitempty"`
	CPULimitCores    *float64 `json:"cpuLimitCores,omitempty"`
	// HostMemoryBytes and HostCores are what the machine itself has, kept so
	// a limit can be shown in proportion to it.
	HostMemoryBytes uint64 `json:"hostMemoryBytes,omitempty"`
	HostCores       int    `json:"hostCores,omitempty"`
}

// HostInterface is throughput and error counts for one network interface.
type HostInterface struct {
	Name          string   `json:"name"`
	Up            bool     `json:"up"`
	Addresses     []string `json:"addresses,omitempty"`
	SpeedMbit     uint64   `json:"speedMbit,omitempty"`
	RxBytes       uint64   `json:"rxBytes"`
	TxBytes       uint64   `json:"txBytes"`
	RxBytesPerSec *float64 `json:"rxBytesPerSec,omitempty"`
	TxBytesPerSec *float64 `json:"txBytesPerSec,omitempty"`
	RxErrors      uint64   `json:"rxErrors,omitempty"`
	TxErrors      uint64   `json:"txErrors,omitempty"`
	RxDropped     uint64   `json:"rxDropped,omitempty"`
	TxDropped     uint64   `json:"txDropped,omitempty"`
}

// HostDiskIO is throughput for one block device.
type HostDiskIO struct {
	Name             string   `json:"name"`
	ReadBytes        uint64   `json:"readBytes"`
	WriteBytes       uint64   `json:"writeBytes"`
	ReadBytesPerSec  *float64 `json:"readBytesPerSec,omitempty"`
	WriteBytesPerSec *float64 `json:"writeBytesPerSec,omitempty"`
	ReadOpsPerSec    *float64 `json:"readOpsPerSec,omitempty"`
	WriteOpsPerSec   *float64 `json:"writeOpsPerSec,omitempty"`
	// BusyPct is the share of wall-clock time the device spent servicing I/O.
	// Sustained values near 100 mean the device, not the CPU, is the limit.
	BusyPct *float64 `json:"busyPct,omitempty"`
}

// TotalNetBytesPerSec sums receive and transmit rates across every interface
// that reported one. The second return is false when no interface did.
func (h HostMetrics) TotalNetBytesPerSec() (rx, tx float64, ok bool) {
	for _, n := range h.Interfaces {
		if n.RxBytesPerSec != nil {
			rx, ok = rx+*n.RxBytesPerSec, true
		}
		if n.TxBytesPerSec != nil {
			tx, ok = tx+*n.TxBytesPerSec, true
		}
	}
	return rx, tx, ok
}

// TotalDiskBytesPerSec sums read and write rates across every block device
// that reported one. The second return is false when none did.
func (h HostMetrics) TotalDiskBytesPerSec() (read, write float64, ok bool) {
	for _, d := range h.Disks {
		if d.ReadBytesPerSec != nil {
			read, ok = read+*d.ReadBytesPerSec, true
		}
		if d.WriteBytesPerSec != nil {
			write, ok = write+*d.WriteBytesPerSec, true
		}
	}
	return read, write, ok
}

// FullestFilesystem returns the mounted filesystem closest to full, which is
// the one worth alerting on. ok is false when no filesystem was readable.
func (h HostMetrics) FullestFilesystem() (fs HostFilesystem, ok bool) {
	for _, f := range h.Filesystems {
		if f.TotalBytes == 0 {
			continue
		}
		if !ok || f.UsedPct > fs.UsedPct {
			fs, ok = f, true
		}
	}
	return fs, ok
}

// Describe is a one-line summary used in check messages and the event log.
func (h HostMetrics) Describe() string {
	parts := make([]string, 0, 3)
	if h.CPU.UsagePct != nil {
		parts = append(parts, fmt.Sprintf("CPU %.0f%%", *h.CPU.UsagePct))
	}
	if h.Memory.TotalBytes > 0 {
		parts = append(parts, fmt.Sprintf("memory %.0f%%", h.Memory.UsedPct))
	}
	if fs, ok := h.FullestFilesystem(); ok {
		parts = append(parts, fmt.Sprintf("disk %.0f%% (%s)", fs.UsedPct, fs.Mount))
	}
	if len(parts) == 0 {
		return "no hardware readings available"
	}
	return strings.Join(parts, ", ")
}

// Agent is a machine registered to push its hardware readings to GWatch.
//
// An agent credential is deliberately not an API key: it may do exactly one
// thing, submit readings for its own machine, and it grants GWatch no access
// to the machine it came from. The token itself is stored only as a hash.
type Agent struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	NodeID      *int64     `json:"nodeId"` // node this machine's readings belong to
	Prefix      string     `json:"prefix"` // first characters of the token, for display
	Enabled     bool       `json:"enabled"`
	CreatedBy   string     `json:"createdBy"`
	CreatedAt   time.Time  `json:"createdAt"`
	RevokedAt   *time.Time `json:"revokedAt,omitempty"`
	LastSeenAt  *time.Time `json:"lastSeenAt,omitempty"`
	LastAddr    string     `json:"lastAddr,omitempty"`
	LastVersion string     `json:"lastVersion,omitempty"`
	Hostname    string     `json:"hostname,omitempty"`
	OS          string     `json:"os,omitempty"`
	Arch        string     `json:"arch,omitempty"`

	// TokenViewable is whether an administrator can read this agent's token
	// back out of GWatch. A token minted by this version is kept sealed with
	// the server's own key so it can be compared with the one in the agent's
	// configuration file; one registered before that, or restored from a
	// backup, is only a digest and has to be rotated to get a viewable one.
	TokenViewable bool `json:"tokenViewable"`
	// RotationPending is set while a replacement token has been issued and the
	// agent has not yet used it. The old token works until then.
	RotationPending bool       `json:"rotationPending,omitempty"`
	RotationSince   *time.Time `json:"rotationSince,omitempty"`
	// Collect is the selection an administrator made for this machine; zero
	// means automatic. Inventory is what the agent last said it could collect,
	// and CheckedInAt when it last asked GWatch for its settings.
	Collect     CollectSelection `json:"collect"`
	Inventory   *Inventory       `json:"inventory,omitempty"`
	CheckedInAt *time.Time       `json:"checkedInAt,omitempty"`
}

// Revoked reports whether the agent's token can no longer be used.
func (a Agent) Revoked() bool { return a.RevokedAt != nil }

// HostKey returns the metrics key this agent's readings are stored under.
func (a Agent) HostKey() string { return AgentHostKey(a.ID) }

// HostSample is one stored reading. The numeric columns are what the charts
// read; Metrics is the full snapshot kept for the inspector.
type HostSample struct {
	Key            string      `json:"key"`
	Timestamp      time.Time   `json:"ts"`
	CPUPct         *float64    `json:"cpuPct,omitempty"`
	MemPct         *float64    `json:"memPct,omitempty"`
	SwapPct        *float64    `json:"swapPct,omitempty"`
	DiskPct        *float64    `json:"diskPct,omitempty"` // fullest filesystem
	LoadPerCore    *float64    `json:"loadPerCore,omitempty"`
	NetRxBytesSec  *float64    `json:"netRxBytesPerSec,omitempty"`
	NetTxBytesSec  *float64    `json:"netTxBytesPerSec,omitempty"`
	DiskReadBytes  *float64    `json:"diskReadBytesPerSec,omitempty"`
	DiskWriteBytes *float64    `json:"diskWriteBytesPerSec,omitempty"`
	Metrics        HostMetrics `json:"metrics,omitempty"`
}

// SampleFrom reduces a snapshot to the numeric series worth storing per point.
func SampleFrom(m HostMetrics) HostSample {
	s := HostSample{Key: m.Key, Timestamp: m.Timestamp, Metrics: m}
	s.CPUPct = m.CPU.UsagePct
	s.LoadPerCore = m.CPU.LoadPerCore
	if m.Memory.TotalBytes > 0 {
		pct := m.Memory.UsedPct
		s.MemPct = &pct
	}
	s.SwapPct = m.Memory.SwapUsedPct
	if fs, ok := m.FullestFilesystem(); ok {
		pct := fs.UsedPct
		s.DiskPct = &pct
	}
	if rx, tx, ok := m.TotalNetBytesPerSec(); ok {
		s.NetRxBytesSec, s.NetTxBytesSec = &rx, &tx
	}
	if rd, wr, ok := m.TotalDiskBytesPerSec(); ok {
		s.DiskReadBytes, s.DiskWriteBytes = &rd, &wr
	}
	return s
}

// HostSummary is what the hardware view lists: an agent (or the local machine)
// together with its newest reading.
type HostSummary struct {
	Key      string       `json:"key"`
	Name     string       `json:"name"`
	Source   HostSource   `json:"source"`
	Agent    *Agent       `json:"agent,omitempty"`
	NodeID   *int64       `json:"nodeId,omitempty"`
	NodeName string       `json:"nodeName,omitempty"`
	Status   Status       `json:"status"`
	Stale    bool         `json:"stale"`
	Metrics  *HostMetrics `json:"metrics,omitempty"`
	Warnings []string     `json:"warnings,omitempty"`
	// Watch is, for the computer GWatch runs on, the services and programs
	// it watches (an agent's are in Agent.Collect). Absent when it watches
	// none.
	Watch *CollectSelection `json:"watch,omitempty"`
}

// Package sysmetrics reads hardware health — processor, memory, filesystem
// space, network and disk throughput — from the machine it runs on.
//
// The same code serves two callers: GWatch itself, reading the computer it is
// installed on, and gwatch-agent, reading a machine that reports in. Both hold
// a Collector, because throughput and utilisation are rates: they only exist
// as the difference between two readings taken on the same machine, and a
// Collector is what remembers the previous one. Nothing here ever subtracts
// counters across a network — a restarted machine or a dropped report would
// turn into a nonsense spike.
//
// Collect never returns a partial failure as an error. A counter this platform
// does not expose, or a filesystem that cannot be stat'ed, is recorded in
// Warnings and the rest of the reading is returned: knowing the disk is full
// is useful even when the network counters are missing.
package sysmetrics

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// ErrUnsupported is returned by Collect on a platform with no implementation.
var ErrUnsupported = errors.New("hardware readings are not available on " + runtime.GOOS)

// primeWindow is how long the first Collect waits between its two readings so
// that rates are present immediately rather than on the second call. It is
// short enough not to stall a check and long enough to be more than noise.
const primeWindow = 300 * time.Millisecond

// raw is one platform reading: the cumulative counters plus everything that is
// already absolute (sizes, load averages). Rates are derived from two of these.
type raw struct {
	at time.Time

	hostname string
	platform string
	kernel   string

	cores    int
	cpuModel string
	cpu      cpuTimes
	haveCPU  bool

	load1, load5, load15 float64
	haveLoad             bool

	memory  model.HostMemory
	haveMem bool

	bootTime time.Time

	filesystems []model.HostFilesystem
	interfaces  []ifaceCounters
	disks       []diskCounters

	warnings []string
}

// cpuTimes holds cumulative processor time in seconds, split by mode.
type cpuTimes struct {
	user, nice, system, idle, iowait, irq, softirq, steal float64
}

// total is the sum of every mode, which is the denominator for utilisation.
func (c cpuTimes) total() float64 {
	return c.user + c.nice + c.system + c.idle + c.iowait + c.irq + c.softirq + c.steal
}

// busy is time spent doing anything other than idling.
func (c cpuTimes) busy() float64 { return c.total() - c.idle - c.iowait }

type ifaceCounters struct {
	name      string
	up        bool
	addresses []string
	speedMbit uint64
	rxBytes   uint64
	txBytes   uint64
	rxErrors  uint64
	txErrors  uint64
	rxDropped uint64
	txDropped uint64
}

type diskCounters struct {
	name       string
	readBytes  uint64
	writeBytes uint64
	readOps    uint64
	writeOps   uint64
	busyMillis uint64
}

// Collector turns successive platform readings into a snapshot with rates.
// The zero value is ready to use and is safe for concurrent use.
type Collector struct {
	// Key is copied onto every snapshot. It defaults to model.HostKeyLocal.
	Key string
	// AgentVersion is copied onto every snapshot so the server can tell which
	// build of gwatch-agent a reading came from. It is empty for GWatch itself.
	AgentVersion string

	mu   sync.Mutex
	prev *raw
}

// NewCollector returns a Collector labelling its snapshots with key.
func NewCollector(key string) *Collector {
	if key == "" {
		key = model.HostKeyLocal
	}
	return &Collector{Key: key}
}

// Collect takes a reading. The first call on a Collector takes two readings
// separated by primeWindow so that utilisation and throughput are populated
// straight away; later calls compare against the previous call, so the rates
// cover the real interval between them.
//
// Collect returns an error only when the platform has no implementation at all
// or when ctx is cancelled. Everything else lands in Warnings.
func (c *Collector) Collect(ctx context.Context) (model.HostMetrics, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cur, err := collect(ctx)
	if err != nil {
		return model.HostMetrics{}, err
	}

	c.mu.Lock()
	prev := c.prev
	if prev == nil {
		c.mu.Unlock()
		// Nothing to compare against yet. Wait a beat and read again so the
		// caller's very first snapshot still carries rates.
		select {
		case <-ctx.Done():
			return model.HostMetrics{}, ctx.Err()
		case <-time.After(primeWindow):
		}
		second, err := collect(ctx)
		if err != nil {
			return model.HostMetrics{}, err
		}
		prev, cur = cur, second
		c.mu.Lock()
	}
	c.prev = cur
	key, version := c.Key, c.AgentVersion
	c.mu.Unlock()

	if key == "" {
		key = model.HostKeyLocal
	}
	return snapshot(key, version, prev, cur), nil
}

// Reset forgets the previous reading, so the next Collect primes itself again.
// Call it after a gap long enough that the old counters are misleading.
func (c *Collector) Reset() {
	c.mu.Lock()
	c.prev = nil
	c.mu.Unlock()
}

// snapshot combines two readings into the wire format.
func snapshot(key, agentVersion string, prev, cur *raw) model.HostMetrics {
	elapsed := cur.at.Sub(prev.at).Seconds()
	m := model.HostMetrics{
		Key:          key,
		Hostname:     cur.hostname,
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
		Platform:     cur.platform,
		Kernel:       cur.kernel,
		AgentVersion: agentVersion,
		Timestamp:    cur.at,
		Filesystems:  cur.filesystems,
		Warnings:     cur.warnings,
	}
	if !cur.bootTime.IsZero() {
		bt := cur.bootTime
		m.BootTime = &bt
		m.UptimeSecs = cur.at.Sub(bt).Seconds()
	}

	m.CPU = model.HostCPU{Cores: cur.cores, Model: cur.cpuModel}
	if cur.haveCPU && prev.haveCPU {
		if span := cur.cpu.total() - prev.cpu.total(); span > 0 {
			pct := func(delta float64) *float64 {
				v := clampPct(delta / span * 100)
				return &v
			}
			m.CPU.UsagePct = pct(cur.cpu.busy() - prev.cpu.busy())
			m.CPU.UserPct = pct((cur.cpu.user - prev.cpu.user) + (cur.cpu.nice - prev.cpu.nice))
			m.CPU.SystemPct = pct((cur.cpu.system - prev.cpu.system) + (cur.cpu.irq - prev.cpu.irq) + (cur.cpu.softirq - prev.cpu.softirq))
			m.CPU.IOWaitPct = pct(cur.cpu.iowait - prev.cpu.iowait)
			m.CPU.StealPct = pct(cur.cpu.steal - prev.cpu.steal)
		}
	}
	if cur.haveLoad {
		l1, l5, l15 := cur.load1, cur.load5, cur.load15
		m.CPU.Load1, m.CPU.Load5, m.CPU.Load15 = &l1, &l5, &l15
		if cur.cores > 0 {
			per := l1 / float64(cur.cores)
			m.CPU.LoadPerCore = &per
		}
	}

	if cur.haveMem {
		m.Memory = cur.memory
		if m.Memory.TotalBytes > 0 {
			m.Memory.UsedPct = clampPct(float64(m.Memory.UsedBytes) / float64(m.Memory.TotalBytes) * 100)
		}
		if m.Memory.SwapTotalBytes > 0 {
			pct := clampPct(float64(m.Memory.SwapUsedBytes) / float64(m.Memory.SwapTotalBytes) * 100)
			m.Memory.SwapUsedPct = &pct
		}
	}

	prevIf := make(map[string]ifaceCounters, len(prev.interfaces))
	for _, n := range prev.interfaces {
		prevIf[n.name] = n
	}
	for _, n := range cur.interfaces {
		iface := model.HostInterface{
			Name:      n.name,
			Up:        n.up,
			Addresses: n.addresses,
			SpeedMbit: n.speedMbit,
			RxBytes:   n.rxBytes,
			TxBytes:   n.txBytes,
			RxErrors:  n.rxErrors,
			TxErrors:  n.txErrors,
			RxDropped: n.rxDropped,
			TxDropped: n.txDropped,
		}
		if p, ok := prevIf[n.name]; ok {
			iface.RxBytesPerSec = rate(p.rxBytes, n.rxBytes, elapsed)
			iface.TxBytesPerSec = rate(p.txBytes, n.txBytes, elapsed)
		}
		m.Interfaces = append(m.Interfaces, iface)
	}

	prevDisk := make(map[string]diskCounters, len(prev.disks))
	for _, d := range prev.disks {
		prevDisk[d.name] = d
	}
	for _, d := range cur.disks {
		disk := model.HostDiskIO{Name: d.name, ReadBytes: d.readBytes, WriteBytes: d.writeBytes}
		if p, ok := prevDisk[d.name]; ok {
			disk.ReadBytesPerSec = rate(p.readBytes, d.readBytes, elapsed)
			disk.WriteBytesPerSec = rate(p.writeBytes, d.writeBytes, elapsed)
			disk.ReadOpsPerSec = rate(p.readOps, d.readOps, elapsed)
			disk.WriteOpsPerSec = rate(p.writeOps, d.writeOps, elapsed)
			if busy := rate(p.busyMillis, d.busyMillis, elapsed); busy != nil {
				pct := clampPct(*busy / 10) // ms of busy time per second of wall clock
				disk.BusyPct = &pct
			}
		}
		m.Disks = append(m.Disks, disk)
	}

	sort.Slice(m.Filesystems, func(i, j int) bool { return m.Filesystems[i].Mount < m.Filesystems[j].Mount })
	sort.Slice(m.Interfaces, func(i, j int) bool { return m.Interfaces[i].Name < m.Interfaces[j].Name })
	sort.Slice(m.Disks, func(i, j int) bool { return m.Disks[i].Name < m.Disks[j].Name })
	return m
}

// rate converts two cumulative counter readings into a per-second rate. It
// returns nil when the counter went backwards, which means the machine or the
// device restarted and the difference would be meaningless.
func rate(prev, cur uint64, elapsed float64) *float64 {
	if elapsed <= 0 || cur < prev {
		return nil
	}
	v := float64(cur-prev) / elapsed
	return &v
}

func clampPct(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// usedPct is the share of a filesystem or pool that is in use.
func usedPct(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return clampPct(float64(used) / float64(total) * 100)
}

// hostname reports this machine's name, falling back to "unknown" so a
// snapshot is never anonymous.
func hostname() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "unknown"
}

// addWarning appends a collector problem, keeping the list short enough that a
// misbehaving platform cannot fill the database with the same line.
func (r *raw) addWarning(format string, args ...any) {
	if len(r.warnings) >= 8 {
		return
	}
	r.warnings = append(r.warnings, fmt.Sprintf(format, args...))
}

// interfaceAddresses returns the IP addresses configured on each interface,
// keyed by interface name. It is shared by every platform.
func interfaceAddresses() map[string][]string {
	out := map[string][]string{}
	ifaces, err := netInterfaces()
	if err != nil {
		return out
	}
	for _, n := range ifaces {
		addrs, err := n.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			out[n.Name] = append(out[n.Name], a.String())
		}
	}
	return out
}

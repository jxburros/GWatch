package sysmetrics

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

func f64(v float64) *float64 { return &v }

func TestRateSkipsCounterResets(t *testing.T) {
	tests := []struct {
		name      string
		prev, cur uint64
		elapsed   float64
		want      *float64
	}{
		{"steady counter", 1000, 2000, 2, f64(500)},
		{"counter went backwards", 2000, 1000, 2, nil},
		{"no time passed", 1000, 2000, 0, nil},
		{"negative interval", 1000, 2000, -1, nil},
		{"unchanged counter", 1000, 1000, 5, f64(0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rate(tt.prev, tt.cur, tt.elapsed)
			switch {
			case tt.want == nil && got != nil:
				t.Fatalf("want no rate, got %v", *got)
			case tt.want != nil && got == nil:
				t.Fatalf("want %v, got no rate", *tt.want)
			case tt.want != nil && *got != *tt.want:
				t.Fatalf("want %v, got %v", *tt.want, *got)
			}
		})
	}
}

// A machine that reboots between two readings resets every counter. The
// snapshot must report no throughput for that interval rather than a spike.
func TestSnapshotIgnoresRebootedCounters(t *testing.T) {
	at := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	prev := &raw{
		at:         at,
		haveCPU:    true,
		cpu:        cpuTimes{user: 500, idle: 500},
		interfaces: []ifaceCounters{{name: "eth0", rxBytes: 9_000_000, txBytes: 4_000_000}},
		disks:      []diskCounters{{name: "sda", readBytes: 8_000_000, writeBytes: 2_000_000}},
	}
	cur := &raw{
		at:         at.Add(10 * time.Second),
		haveCPU:    true,
		cpu:        cpuTimes{user: 1, idle: 9},
		interfaces: []ifaceCounters{{name: "eth0", rxBytes: 1_000, txBytes: 500}},
		disks:      []diskCounters{{name: "sda", readBytes: 100, writeBytes: 50}},
	}

	m := snapshot(model.HostKeyLocal, "", prev, cur)
	if m.CPU.UsagePct != nil {
		t.Errorf("processor usage should be absent when the tick counters reset, got %v", *m.CPU.UsagePct)
	}
	if got := m.Interfaces[0].RxBytesPerSec; got != nil {
		t.Errorf("interface rate should be absent after a reset, got %v", *got)
	}
	if got := m.Disks[0].WriteBytesPerSec; got != nil {
		t.Errorf("disk rate should be absent after a reset, got %v", *got)
	}
}

func TestSnapshotDerivesRatesAndPercentages(t *testing.T) {
	at := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	boot := at.Add(-2 * time.Hour)
	prev := &raw{
		at:      at,
		haveCPU: true,
		// 100 seconds of processor time, three quarters of it idle.
		cpu:        cpuTimes{user: 20, system: 5, idle: 70, iowait: 5},
		interfaces: []ifaceCounters{{name: "eth0", rxBytes: 1_000, txBytes: 2_000}},
		disks:      []diskCounters{{name: "sda", readBytes: 0, writeBytes: 0, busyMillis: 0}},
	}
	cur := &raw{
		at:       at.Add(10 * time.Second),
		hostname: "nas",
		cores:    4,
		bootTime: boot,
		haveCPU:  true,
		// 40 of the next 100 seconds were busy (30 user, 10 system) plus 10
		// in iowait, which counts against utilisation too.
		cpu:      cpuTimes{user: 50, system: 15, idle: 120, iowait: 15},
		haveLoad: true,
		load1:    2, load5: 1, load15: 0.5,
		haveMem: true,
		memory:  model.HostMemory{TotalBytes: 1000, UsedBytes: 250, SwapTotalBytes: 100, SwapUsedBytes: 5},
		filesystems: []model.HostFilesystem{
			{Mount: "/data", TotalBytes: 100, UsedBytes: 91, UsedPct: 91},
			{Mount: "/", TotalBytes: 100, UsedBytes: 10, UsedPct: 10},
		},
		interfaces: []ifaceCounters{{name: "eth0", rxBytes: 11_000, txBytes: 2_500}},
		disks:      []diskCounters{{name: "sda", readBytes: 20_000, writeBytes: 5_000, busyMillis: 2_000}},
	}

	m := snapshot("agent:7", "1.2.3", prev, cur)

	if m.Key != "agent:7" || m.AgentVersion != "1.2.3" {
		t.Fatalf("key/version not carried through: %q %q", m.Key, m.AgentVersion)
	}
	// 100 further seconds of processor time: 30 user, 10 system and 10
	// iowait, so 40% of the interval was not idle.
	assertPct(t, "cpu usage", m.CPU.UsagePct, 40)
	assertPct(t, "cpu user", m.CPU.UserPct, 30)
	assertPct(t, "cpu iowait", m.CPU.IOWaitPct, 10)
	assertPct(t, "load per core", m.CPU.LoadPerCore, 0.5)

	if m.Memory.UsedPct != 25 {
		t.Errorf("memory used: want 25%%, got %v", m.Memory.UsedPct)
	}
	assertPct(t, "swap used", m.Memory.SwapUsedPct, 5)

	// 10_000 bytes received across a 10 second interval.
	assertPct(t, "rx rate", m.Interfaces[0].RxBytesPerSec, 1000)
	assertPct(t, "tx rate", m.Interfaces[0].TxBytesPerSec, 50)
	assertPct(t, "disk read rate", m.Disks[0].ReadBytesPerSec, 2000)
	// 2000ms of the 10s interval spent servicing I/O is 20% busy.
	assertPct(t, "disk busy", m.Disks[0].BusyPct, 20)

	if m.UptimeSecs != (2*time.Hour + 10*time.Second).Seconds() {
		t.Errorf("uptime: got %v", m.UptimeSecs)
	}
	// Filesystems are sorted so the UI order does not depend on mount order.
	if m.Filesystems[0].Mount != "/" {
		t.Errorf("filesystems not sorted: %v", m.Filesystems[0].Mount)
	}
	if fs, ok := m.FullestFilesystem(); !ok || fs.Mount != "/data" {
		t.Errorf("fullest filesystem: got %+v ok=%v", fs, ok)
	}
}

// Utilisation is a ratio, so a busier-than-total reading (which a
// virtualised clock can produce) must be clamped, not reported as 140%.
func TestSnapshotClampsImplausibleUtilisation(t *testing.T) {
	at := time.Now()
	prev := &raw{at: at, haveCPU: true, cpu: cpuTimes{user: 0, idle: 100}}
	cur := &raw{at: at.Add(time.Second), haveCPU: true, cpu: cpuTimes{user: 200, idle: 100}}
	m := snapshot(model.HostKeyLocal, "", prev, cur)
	assertPct(t, "cpu usage", m.CPU.UsagePct, 100)
}

func TestCollectorPrimesRatesOnFirstCall(t *testing.T) {
	c := NewCollector("")
	m, err := c.Collect(context.Background())
	if err != nil {
		t.Skipf("no hardware readings on this platform: %v", err)
	}
	if m.Key != model.HostKeyLocal {
		t.Errorf("empty key should default to %q, got %q", model.HostKeyLocal, m.Key)
	}
	if m.Hostname == "" {
		t.Error("hostname missing")
	}
	if m.CPU.Cores <= 0 {
		t.Errorf("cores: got %d", m.CPU.Cores)
	}
	if m.Memory.TotalBytes == 0 {
		t.Error("total memory is zero")
	}
	// The point of priming: the very first snapshot already carries a rate,
	// so a check does not have to run twice before it can report anything.
	if len(m.Interfaces) > 0 && m.Interfaces[0].RxBytesPerSec == nil {
		t.Error("first snapshot should already carry interface rates")
	}
}

func TestSampleFromReducesSnapshot(t *testing.T) {
	m := model.HostMetrics{
		Key:    "local",
		CPU:    model.HostCPU{UsagePct: f64(42)},
		Memory: model.HostMemory{TotalBytes: 100, UsedBytes: 60, UsedPct: 60},
		Filesystems: []model.HostFilesystem{
			{Mount: "/", TotalBytes: 10, UsedPct: 20},
			{Mount: "/srv", TotalBytes: 10, UsedPct: 80},
		},
		Interfaces: []model.HostInterface{
			{Name: "eth0", RxBytesPerSec: f64(100), TxBytesPerSec: f64(10)},
			{Name: "eth1", RxBytesPerSec: f64(50), TxBytesPerSec: f64(5)},
		},
	}
	s := model.SampleFrom(m)
	assertPct(t, "cpu", s.CPUPct, 42)
	assertPct(t, "memory", s.MemPct, 60)
	assertPct(t, "fullest disk", s.DiskPct, 80)
	assertPct(t, "total rx", s.NetRxBytesSec, 150)
	if s.DiskReadBytes != nil {
		t.Errorf("no disk reported a rate, want nil, got %v", *s.DiskReadBytes)
	}
}

func assertPct(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Errorf("%s: want %v, got nothing", name, want)
		return
	}
	if math.Abs(*got-want) > 1e-6 {
		t.Errorf("%s: want %v, got %v", name, want, *got)
	}
}

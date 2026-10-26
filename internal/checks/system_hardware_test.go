package checks

import (
	"strings"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// Temperatures, pressure and pool health are per-metric readings like the
// rest: each sensor has its own key and verdict, a family threshold covers
// every sensor, a resource threshold ("psi:memory") covers both of its
// averages, and a degraded ZFS pool trips the default pool threshold.
func TestEvaluateHostLinuxHardwareReadings(t *testing.T) {
	now := time.Now()
	m := healthyReading(now)
	m.Temperatures = []model.HostTemperature{{Name: "coretemp/Package id 0", Celsius: 91}, {Name: "nvme/Composite", Celsius: 40}}
	m.Pressure = []model.HostPressure{{Resource: "memory", Avg10: 30, Avg60: 12}}
	m.Pools = []model.HostPool{{Name: "tank", Health: "DEGRADED"}}

	cfg := model.SystemDefaults()
	cfg.MetricThresholds = append(cfg.MetricThresholds,
		model.MetricThreshold{Metric: model.MetricTemp, Warn: pct(80), Crit: pct(95)},
		model.MetricThreshold{Metric: "psi:memory", Warn: pct(25)})
	if err := model.ValidateMetricThresholds(cfg.MetricThresholds); err != nil {
		t.Fatal(err)
	}
	res := evaluateHost(systemCheck(cfg), m, now)
	if !res.Success {
		t.Fatalf("nothing is critical: %s", res.Error)
	}
	if got := metricByKey(t, res, "temp:coretemp/Package id 0"); got.Status != model.StatusDegraded || got.Unit != "°C" || !strings.Contains(got.Reason, "91 °C") {
		t.Errorf("hot package: %+v", got)
	}
	if got := metricByKey(t, res, "temp:nvme/Composite"); got.Status != model.StatusUp {
		t.Errorf("cool drive: %+v", got)
	}
	if got := metricByKey(t, res, "psi:memory.avg10"); got.Status != model.StatusDegraded || got.Unit != "%" {
		t.Errorf("memory pressure: %+v", got)
	}
	if got := metricByKey(t, res, "psi:memory.avg60"); got.Status != model.StatusUp {
		t.Errorf("memory pressure over a minute: %+v", got)
	}
	pool := metricByKey(t, res, "pool:tank")
	if pool.Status != model.StatusDegraded || !strings.Contains(pool.Reason, "Pool tank is degraded") {
		t.Errorf("degraded pool: %+v", pool)
	}

	m.Pools[0].Health = "FAULTED"
	if res := evaluateHost(systemCheck(cfg), m, now); res.Success {
		t.Error("a faulted pool should take the check down")
	}
}

// Watched services and programs (#62): a stopped service and a program with
// no copy running are each critical under the default thresholds, by name,
// and the reason says which and in words.
func TestEvaluateHostWatchedServicesAndProcesses(t *testing.T) {
	now := time.Now()
	m := healthyReading(now)
	m.Services = []model.HostService{{Name: "nginx", Running: true, State: "active (running)"}, {Name: "smbd", State: "failed"}}
	m.Processes = []model.HostProcess{{Name: "plex", Count: 2}, {Name: "backup", Count: 0}}

	res := evaluateHost(systemCheck(model.SystemDefaults()), m, now)
	if res.Success {
		t.Fatal("a stopped service should take the check down")
	}
	if got := metricByKey(t, res, "service:nginx"); got.Status != model.StatusUp || got.Value != 1 || got.Unit != "running" {
		t.Errorf("running service: %+v", got)
	}
	smbd := metricByKey(t, res, "service:smbd")
	if smbd.Status != model.StatusDown || !strings.Contains(smbd.Reason, "Service smbd is stopped, at or below the stopped critical threshold") {
		t.Errorf("stopped service: %+v", smbd)
	}
	if got := metricByKey(t, res, "process:plex"); got.Status != model.StatusUp || got.Value != 2 {
		t.Errorf("running program: %+v", got)
	}
	backup := metricByKey(t, res, "process:backup")
	if backup.Status != model.StatusDown || !strings.Contains(backup.Reason, "Copies of backup running is 0") {
		t.Errorf("missing program: %+v", backup)
	}

	// An instance entry with no level stops one service from alerting.
	cfg := model.SystemDefaults()
	cfg.MetricThresholds = append(cfg.MetricThresholds, model.MetricThreshold{Metric: "service:smbd", Below: true},
		model.MetricThreshold{Metric: "process:backup", Below: true})
	if err := model.ValidateMetricThresholds(cfg.MetricThresholds); err != nil {
		t.Fatal(err)
	}
	if res := evaluateHost(systemCheck(cfg), m, now); !res.Success {
		t.Errorf("both were switched off: %s", res.Error)
	}
}

// Watched containers: under the default thresholds a stopped or missing
// container is critical, an unhealthy, paused or restarting one is a warning,
// a restart loop is a warning even while the container happens to be up, and
// memory against the container's own limit is judged like a machine's.
func TestEvaluateHostWatchedContainers(t *testing.T) {
	now := time.Now()
	m := healthyReading(now)
	m.Docker = []model.HostDockerContainer{
		{Name: "web", State: "running", Health: "healthy", CPUPct: pct(12), MemoryPct: pct(40)},
		{Name: "db", State: "exited", ExitCode: 137},
		{Name: "api", State: "running", Health: "unhealthy"},
		{Name: "worker", State: "running", RecentRestarts: 5, RestartCount: 41},
		{Name: "cache", State: "running", MemoryPct: pct(98)},
		{Name: "gone", State: "not found"},
	}
	res := evaluateHost(systemCheck(model.SystemDefaults()), m, now)
	if res.Success {
		t.Fatal("a stopped container should take the check down")
	}
	if got := metricByKey(t, res, "docker:web"); got.Status != model.StatusUp || got.Value != 2 || got.Unit != "container" {
		t.Errorf("healthy container: %+v", got)
	}
	if got := metricByKey(t, res, "docker-cpu:web"); got.Status != model.StatusUp || got.Unit != "%" {
		t.Errorf("container processor: %+v", got)
	}
	if got := metricByKey(t, res, "docker:db"); got.Status != model.StatusDown || !strings.Contains(got.Reason, "Container db is stopped, at or below the stopped critical threshold") {
		t.Errorf("stopped container: %+v", got)
	}
	if got := metricByKey(t, res, "docker:api"); got.Status != model.StatusDegraded || !strings.Contains(got.Reason, "not healthy") {
		t.Errorf("unhealthy container: %+v", got)
	}
	if got := metricByKey(t, res, "docker:worker"); got.Status != model.StatusUp {
		t.Errorf("a looping container that is up right now is up by its state: %+v", got)
	}
	if got := metricByKey(t, res, "docker-restarts:worker"); got.Status != model.StatusDegraded || got.Value != 5 || !strings.Contains(got.Reason, "Restarts of worker in the last hour is 5") {
		t.Errorf("restart loop: %+v", got)
	}
	if got := metricByKey(t, res, "docker-memory:cache"); got.Status != model.StatusDown {
		t.Errorf("container at its memory limit: %+v", got)
	}
	if got := metricByKey(t, res, "docker:gone"); got.Status != model.StatusDown || got.Value != 0 {
		t.Errorf("missing container: %+v", got)
	}
	for _, r := range res.Details.MetricResults {
		if r.Key == "docker-restarts:gone" || r.Key == "docker-cpu:db" || r.Key == "docker-memory:db" {
			t.Errorf("a container that is not there or not running has no %s", r.Key)
		}
	}
	for _, state := range []string{"paused", "restarting"} {
		m.Docker = []model.HostDockerContainer{{Name: "web", State: state}}
		res := evaluateHost(systemCheck(model.SystemDefaults()), m, now)
		if got := metricByKey(t, res, "docker:web"); got.Status != model.StatusDegraded || got.Value != 1 {
			t.Errorf("%s container: %+v", state, got)
		}
	}
}

// Watched guests: off (or missing) is critical, paused, saved or part way
// between states is a warning, and a guest someone keeps off on purpose can
// be given an entry of its own with no levels.
func TestEvaluateHostWatchedVMs(t *testing.T) {
	now := time.Now()
	m := healthyReading(now)
	m.VMs = []model.HostVM{
		{Name: "dc01", State: "running", CPUPct: pct(7), MemoryPct: pct(55), Processors: 2},
		{Name: "lab", State: "off"},
		{Name: "build", State: "paused"},
		{Name: "media", State: "saved"},
		{Name: "old", State: "not found"},
	}
	res := evaluateHost(systemCheck(model.SystemDefaults()), m, now)
	if res.Success {
		t.Fatal("a guest that is off should take the check down")
	}
	if got := metricByKey(t, res, "vm:dc01"); got.Status != model.StatusUp || got.Value != 2 || got.Unit != "guest" {
		t.Errorf("running guest: %+v", got)
	}
	if got := metricByKey(t, res, "vm-memory:dc01"); got.Status != model.StatusUp || got.Value != 55 {
		t.Errorf("guest memory: %+v", got)
	}
	if got := metricByKey(t, res, "vm:lab"); got.Status != model.StatusDown || !strings.Contains(got.Reason, "Virtual machine lab is off, at or below the off critical threshold") {
		t.Errorf("guest that is off: %+v", got)
	}
	for _, name := range []string{"build", "media"} {
		if got := metricByKey(t, res, "vm:"+name); got.Status != model.StatusDegraded || !strings.Contains(got.Reason, "paused or saved") {
			t.Errorf("%s: %+v", name, got)
		}
	}
	if got := metricByKey(t, res, "vm:old"); got.Status != model.StatusDown {
		t.Errorf("missing guest: %+v", got)
	}

	cfg := model.SystemDefaults()
	cfg.MetricThresholds = append(cfg.MetricThresholds,
		model.MetricThreshold{Metric: "vm:lab", Below: true}, model.MetricThreshold{Metric: "vm:old", Below: true})
	if err := model.ValidateMetricThresholds(cfg.MetricThresholds); err != nil {
		t.Fatal(err)
	}
	if res := evaluateHost(systemCheck(cfg), m, now); !res.Success {
		t.Errorf("the guests kept off on purpose were switched off: %s", res.Error)
	}
}

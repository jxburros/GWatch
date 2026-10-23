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

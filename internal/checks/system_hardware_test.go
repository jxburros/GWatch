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

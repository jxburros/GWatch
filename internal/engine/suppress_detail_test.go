package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// A check with its own cooldown is suppressed for that long, and the
// timeline says so rather than quoting the global cooldown.
func TestCooldownSuppressionNamesTheChecksOwnCooldown(t *testing.T) {
	s := model.Settings{}
	s.Alerts.Enabled = true
	s.Alerts.CooldownMinutes = 60
	ten := 10
	c := model.Check{Alerts: &model.AlertOverride{CooldownMinutes: &ten}}
	now := time.Now()
	last := now.Add(-5 * time.Minute)
	st := &model.CheckState{LastAlertAt: &last}
	e, _, _, _ := setup(t)
	d := e.evaluateDownLocked(c, model.Node{}, st, s, now, false)
	if d.reason != "cooldown" {
		t.Fatalf("want a cooldown suppression, got %+v", d)
	}
	if got := suppressDetail(d, st, s); !strings.Contains(got, "less than 10 minutes ago") {
		t.Errorf("detail quotes the wrong cooldown: %q", got)
	}
}

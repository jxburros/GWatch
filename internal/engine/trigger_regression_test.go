package engine

import (
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

func TestAutomaticTriggersRespectSuppressionAndRunNodeCooldown(t *testing.T) {
	for _, reason := range []string{"maintenance", "silence", "runNode cooldown"} {
		t.Run(reason, func(t *testing.T) {
			e, _, _, _ := setup(t)
			now := time.Now()
			tc := triggerContext{node: model.Node{ID: 1, Enabled: true}, check: model.Check{ID: 2, NodeID: 1}, result: model.Result{Success: false}}
			trigger := model.Trigger{ID: 3, NodeID: 1, Enabled: true, On: []string{"any_failure"}}
			switch reason {
			case "maintenance":
				e.windows = []model.MaintenanceWindow{{Enabled: true, StartAt: now.Add(-time.Hour), EndAt: now.Add(time.Hour)}}
			case "silence":
				until := now.Add(time.Hour)
				tc.state.SilencedUntil = &until
			case "runNode cooldown":
				trigger.Action.Type = model.ActionRunNode
				e.triggerLast[3] = now
			}
			e.triggers[1] = []model.Trigger{trigger}
			before := e.triggerLast[3]
			e.fireTriggers(tc)
			e.wg.Wait()
			if !e.triggerLast[3].Equal(before) {
				t.Fatal("suppressed trigger ran")
			}
		})
	}
}

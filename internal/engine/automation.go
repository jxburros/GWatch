package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jxburros/GWatch/internal/actions"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
)

// Actions returns the action runner used for triggers and endpoints.
func (e *Engine) Actions() *actions.Runner {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.runner == nil {
		e.runner = &actions.Runner{RunNode: func(ctx context.Context, id int64) error {
			_, err := e.RunNodeNow(ctx, id)
			return err
		}}
	}
	return e.runner
}

// reloadTriggersLocked refreshes the in-memory trigger index (caller holds e.mu).
func (e *Engine) loadTriggers(ctx context.Context) error {
	list, err := e.store.ListTriggers(ctx, nil)
	if err != nil {
		return err
	}
	byNode := map[int64][]model.Trigger{}
	for _, t := range list {
		byNode[t.NodeID] = append(byNode[t.NodeID], t)
	}
	e.mu.Lock()
	e.triggers = byNode
	if e.triggerLast == nil {
		e.triggerLast = map[int64]time.Time{}
	}
	for _, t := range list {
		if t.LastRunAt != nil {
			if _, ok := e.triggerLast[t.ID]; !ok {
				e.triggerLast[t.ID] = *t.LastRunAt
			}
		}
	}
	e.mu.Unlock()
	e.lintEndpoints(ctx)
	return nil
}

// endpointLintOnce keeps the tokenless-endpoint notice to one event per process
// start: loadTriggers runs again on every configuration reload, and an existing
// install should be told once, not every time something is saved. It lives here
// rather than on Engine because the struct is defined in engine.go.
var endpointLintOnce sync.Once

// lintEndpoints records a one-time notice listing custom endpoints that can be
// called by anyone who can reach the port. /hook/ is exempt from the LAN access
// password, so a tokenless endpoint is fully open.
func (e *Engine) lintEndpoints(ctx context.Context) {
	endpointLintOnce.Do(func() {
		list, err := e.store.ListEndpoints(ctx)
		if err != nil {
			return
		}
		var open []string
		for _, ep := range list {
			if strings.TrimSpace(ep.Token) == "" {
				open = append(open, "/hook/"+ep.Slug)
			}
		}
		if len(open) == 0 {
			return
		}
		e.recordEvent(model.Event{Type: model.EventConfigChanged, Title: "Custom endpoints without a token",
			Detail: fmt.Sprintf("%s can be called by anyone who can reach this port: they are not covered by the access password. Open Settings → Automation and give each one a token.", strings.Join(open, ", "))})
	})
}

// triggerContext describes what just happened on a check.
type triggerContext struct {
	check  model.Check
	node   model.Node
	result model.Result
	state  model.CheckState
	prev   model.Status
	events []model.Event
}

// conditionsMet returns the conditions of the trigger satisfied by the context.
func conditionsMet(t model.Trigger, tc triggerContext) []string {
	if t.CheckID != nil && *t.CheckID != tc.check.ID {
		return nil
	}
	has := func(et model.EventType) bool {
		for _, ev := range tc.events {
			if ev.Type == et {
				return true
			}
		}
		return false
	}
	var met []string
	for _, cond := range t.On {
		ok := false
		switch cond {
		case "down":
			ok = has(model.EventDown)
		case "recovered":
			ok = has(model.EventRecovered)
		case "degraded":
			ok = has(model.EventWarning)
		case "warning_cleared":
			ok = has(model.EventWarningCleared)
		case "cert_warning":
			ok = has(model.EventCertWarning)
		case "content_changed":
			ok = has(model.EventContentChanged)
		case "affected_by_parent":
			ok = has(model.EventAffectedByParent)
		case "status_change":
			ok = tc.prev != tc.state.Status
		case "any_failure":
			ok = !tc.result.Success
		case "any_success":
			ok = tc.result.Success
		case "latency_over":
			ok = t.LatencyOverMS > 0 && tc.result.LatencyMS != nil && *tc.result.LatencyMS > t.LatencyOverMS
		}
		if ok {
			met = append(met, cond)
		}
	}
	return met
}

// TriggerVars builds the placeholder values for an action fired by a check result.
func TriggerVars(n model.Node, c model.Check, r model.Result, st model.CheckState, event string) actions.Vars {
	v := actions.Vars{
		"event":        event,
		"node.id":      strconv.FormatInt(n.ID, 10),
		"node.name":    n.Name,
		"node.host":    n.Host,
		"node.group":   n.Group,
		"check.id":     strconv.FormatInt(c.ID, 10),
		"check.name":   c.Name,
		"check.type":   string(c.Type),
		"target":       c.Target(n.Host),
		"status":       string(st.Status),
		"message":      r.Message,
		"error":        r.Error,
		"success":      strconv.FormatBool(r.Success),
		"ts":           r.Timestamp.Format(time.RFC3339),
		"failures":     strconv.Itoa(st.ConsecutiveFailures),
		"latencyMs":    "",
		"lossPct":      "",
		"statusCode":   "",
		"prev_status":  "",
		"node.tags":    strings.Join(n.Tags, ","),
		"node.notes":   n.Notes,
		"instance":     "",
		"affected_by":  st.AffectedByNodeName,
		"check.target": c.Config.Target,
	}
	if r.LatencyMS != nil {
		v["latencyMs"] = strconv.FormatFloat(*r.LatencyMS, 'f', 1, 64)
	}
	if r.LossPct != nil {
		v["lossPct"] = strconv.FormatFloat(*r.LossPct, 'f', 0, 64)
	}
	if r.Details.StatusCode != 0 {
		v["statusCode"] = strconv.Itoa(r.Details.StatusCode)
	}
	return v
}

// fireTriggers evaluates the node's triggers against what just happened and
// runs the matching ones in the background.
func (e *Engine) fireTriggers(tc triggerContext) {
	e.mu.Lock()
	list := e.triggers[tc.node.ID]
	if len(list) == 0 {
		e.mu.Unlock()
		return
	}
	now := time.Now()
	instance := e.settings.General.InstanceName
	var due []struct {
		t    model.Trigger
		cond string
	}
	for _, t := range list {
		if !t.Enabled || !tc.node.Enabled {
			continue
		}
		met := conditionsMet(t, tc)
		if len(met) == 0 {
			continue
		}
		if t.CooldownMinutes > 0 {
			if last, ok := e.triggerLast[t.ID]; ok && now.Sub(last) < time.Duration(t.CooldownMinutes)*time.Minute {
				continue
			}
		}
		e.triggerLast[t.ID] = now
		due = append(due, struct {
			t    model.Trigger
			cond string
		}{t, met[0]})
	}
	e.mu.Unlock()
	for _, d := range due {
		vars := TriggerVars(tc.node, tc.check, tc.result, tc.state, d.cond)
		vars["prev_status"] = string(tc.prev)
		vars["instance"] = instance
		vars["trigger.name"] = d.t.Name
		e.wg.Add(1)
		go func(t model.Trigger, vars actions.Vars, cond string) {
			defer e.wg.Done()
			e.executeTrigger(context.Background(), t, tc.node, tc.check, vars, cond)
		}(d.t, vars, d.cond)
	}
}

// executeTrigger runs a trigger's action, records the outcome and the timeline event.
func (e *Engine) executeTrigger(ctx context.Context, t model.Trigger, n model.Node, c model.Check, vars actions.Vars, cond string) model.ActionResult {
	res := e.Actions().Run(ctx, t.Action, vars)
	summary := res.Output
	if res.Error != "" {
		summary = res.Error
		if res.Output != "" {
			summary += "\n" + res.Output
		}
	}
	if err := e.store.RecordTriggerRun(ctx, t.ID, res.StartedAt, res.OK, summary); err != nil {
		e.log.Errorf("record trigger run: %v", err)
	}
	e.mu.Lock()
	e.triggerLast[t.ID] = res.StartedAt
	e.mu.Unlock()
	label := "Trigger ran"
	if !res.OK {
		label = "Trigger failed"
	}
	ev := model.Event{Type: model.EventTriggerFired, NodeID: ptrInt64(n.ID), NodeName: n.Name, Title: fmt.Sprintf("%s: %s", label, t.Name),
		Detail: fmt.Sprintf("%s action on %s (%s) — %s", t.Action.Type, cond, humanDuration(time.Duration(res.DurationMS)*time.Millisecond), firstLine(summary))}
	if c.ID != 0 {
		ev.CheckID = ptrInt64(c.ID)
		ev.CheckName = c.Name
	}
	if !res.OK {
		e.log.Errorf("trigger %q (%s) failed: %s", t.Name, t.Action.Type, res.Error)
	} else {
		e.log.Printf("trigger %q ran (%s, %d ms)", t.Name, t.Action.Type, res.DurationMS)
	}
	e.recordEvent(ev)
	e.broadcast(Update{Kind: "trigger", NodeID: n.ID})
	return res
}

// RunTrigger executes a trigger on demand with the node's current state.
func (e *Engine) RunTrigger(ctx context.Context, id int64) (model.ActionResult, error) {
	t, err := e.store.GetTrigger(ctx, id)
	if err != nil {
		return model.ActionResult{}, err
	}
	e.mu.Lock()
	n, ok := e.nodes[t.NodeID]
	instance := e.settings.General.InstanceName
	var c model.Check
	var st model.CheckState
	if ok {
		for _, cc := range n.Checks {
			if t.CheckID == nil || cc.ID == *t.CheckID {
				c = cc
				if s := e.states[cc.ID]; s != nil {
					st = *s
				}
				break
			}
		}
	}
	e.mu.Unlock()
	if !ok {
		return model.ActionResult{}, store.ErrNotFound
	}
	r := model.Result{Timestamp: time.Now(), Success: st.Status != model.StatusDown, Message: st.LastMessage, LatencyMS: st.LastLatencyMS}
	vars := TriggerVars(n, c, r, st, "manual")
	vars["instance"] = instance
	vars["trigger.name"] = t.Name
	return e.executeTrigger(ctx, t, n, c, vars, "manual"), nil
}

// TestAction runs an action once without recording anything, using sample
// values (or the given node's current state) for the placeholders.
func (e *Engine) TestAction(ctx context.Context, a model.Action, nodeID *int64) model.ActionResult {
	vars := actions.Vars{"event": "test", "node.name": "Example node", "node.host": "192.168.1.1", "check.name": "Ping", "check.type": "ping", "status": "down", "message": "This is a test", "ts": time.Now().Format(time.RFC3339), "latencyMs": "12.3", "target": "192.168.1.1", "success": "false"}
	if nodeID != nil {
		e.mu.Lock()
		if n, ok := e.nodes[*nodeID]; ok {
			var c model.Check
			var st model.CheckState
			if len(n.Checks) > 0 {
				c = n.Checks[0]
				if s := e.states[c.ID]; s != nil {
					st = *s
				}
			}
			r := model.Result{Timestamp: time.Now(), Success: st.Status != model.StatusDown, Message: st.LastMessage, LatencyMS: st.LastLatencyMS}
			vars = TriggerVars(n, c, r, st, "test")
		}
		vars["instance"] = e.settings.General.InstanceName
		e.mu.Unlock()
	}
	return e.Actions().Run(ctx, a, vars)
}

// RunEndpoint executes a custom endpoint's action for an incoming request.
func (e *Engine) RunEndpoint(ctx context.Context, ep model.Endpoint, vars actions.Vars) model.ActionResult {
	if vars == nil {
		vars = actions.Vars{}
	}
	vars["event"] = "endpoint"
	vars["endpoint.name"] = ep.Name
	vars["endpoint.slug"] = ep.Slug
	e.mu.Lock()
	vars["instance"] = e.settings.General.InstanceName
	e.mu.Unlock()
	res := e.Actions().Run(ctx, ep.Action, vars)
	summary := res.Output
	if res.Error != "" {
		summary = res.Error
		if res.Output != "" {
			summary += "\n" + res.Output
		}
	}
	if err := e.store.RecordEndpointCall(ctx, ep.ID, res.StartedAt, res.OK, summary); err != nil {
		e.log.Errorf("record endpoint call: %v", err)
	}
	label := "Endpoint called"
	if !res.OK {
		label = "Endpoint failed"
	}
	e.recordEvent(model.Event{Type: model.EventEndpointCalled, Title: fmt.Sprintf("%s: %s", label, ep.Name), Detail: fmt.Sprintf("/hook/%s ran a %s action (%s) — %s", ep.Slug, ep.Action.Type, humanDuration(time.Duration(res.DurationMS)*time.Millisecond), firstLine(summary))})
	e.broadcast(Update{Kind: "endpoint"})
	return res
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	if s == "" {
		return "no output"
	}
	return s
}

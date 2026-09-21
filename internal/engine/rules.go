package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/actions"
	"github.com/jxburros/GWatch/internal/model"
)

// Notification rules (#31) look at several checks at once where a trigger
// looks at one node: "two of my three DNS servers are down". The engine
// keeps the rules and a small state per rule (met or not, since when, when
// the actions last ran), re-evaluates the rules that mention a check
// whenever that check's status changes, and fires a rule's actions once on
// the way from not-met to met. The state is stored, so a restart neither
// fires a rule a second time nor forgets that it is waiting to clear.
//
// Rules sit beside the per-node alerts, triggers and dependency suppression
// and change none of them. A check in maintenance or silenced does not
// count towards a rule while that lasts, which is how "do not fire while a
// matched check is in maintenance or silenced" is kept.

// loadRules refreshes the in-memory rules and their states from the store.
// On the first load (start-up) a stored state is trusted and a rule without
// one is recorded at whatever it evaluates to, silently: the checks have
// just been read back too, and nothing has happened yet. Every later load
// is a configuration change — a rule saved, a node deleted — and re-evaluates
// the rules, so a rule that is met the moment it is created fires right
// away, and one that no longer holds after an edit clears.
func (e *Engine) loadRules(ctx context.Context) error {
	list, err := e.store.ListRules(ctx)
	if err != nil {
		return err
	}
	stored, err := e.store.RuleStates(ctx)
	if err != nil {
		return err
	}
	e.mu.Lock()
	first := !e.rulesLoaded
	e.rulesLoaded = true
	e.rules = list
	if e.ruleStates == nil {
		e.ruleStates = map[int64]*model.RuleState{}
	}
	keep := map[int64]bool{}
	for _, r := range list {
		keep[r.ID] = true
	}
	for id := range e.ruleStates {
		if !keep[id] {
			delete(e.ruleStates, id)
		}
	}
	var fresh []model.RuleState
	now := time.Now()
	for _, r := range list {
		if _, ok := e.ruleStates[r.ID]; ok {
			continue
		}
		if st, ok := stored[r.ID]; ok {
			s := st
			e.ruleStates[r.ID] = &s
			continue
		}
		st := &model.RuleState{RuleID: r.ID}
		if first && r.Enabled {
			count, _ := e.ruleMetLocked(r, now)
			if count >= r.Needed() {
				st.Met = true
				st.Since = ptrTime(now)
			}
		}
		e.ruleStates[r.ID] = st
		fresh = append(fresh, *st)
	}
	e.mu.Unlock()
	for _, st := range fresh {
		if err := e.store.PutRuleState(ctx, st); err != nil {
			e.log.Errorf("save rule state: %v", err)
		}
	}
	if !first {
		e.evaluateRules(ctx, nil)
	}
	return nil
}

// RuleStates returns where every rule stands, by rule id.
func (e *Engine) RuleStates() map[int64]model.RuleState {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(map[int64]model.RuleState, len(e.ruleStates))
	for id, st := range e.ruleStates {
		out[id] = *st
	}
	return out
}

// ruleRefersToLocked reports whether a rule looks at the check, directly or
// through its node (caller holds e.mu).
func (e *Engine) ruleRefersToLocked(r model.Rule, checkID int64) bool {
	c, ok := e.checks[checkID]
	for _, cond := range r.Conditions {
		if cond.CheckID != nil && *cond.CheckID == checkID {
			return true
		}
		if ok && cond.NodeID != nil && *cond.NodeID == c.NodeID {
			return true
		}
	}
	return false
}

// conditionHoldsLocked says whether one check satisfies a condition right
// now: it has to be enabled, not in maintenance, not silenced, and at the
// status the condition names or worse (caller holds e.mu).
func (e *Engine) conditionHoldsLocked(cond model.RuleCondition, c model.Check, now time.Time) bool {
	n, ok := e.nodes[c.NodeID]
	st := e.states[c.ID]
	if !ok || st == nil || !c.Enabled || !n.Enabled {
		return false
	}
	if e.inMaintenanceLocked(n, now) || (st.SilencedUntil != nil && st.SilencedUntil.After(now)) {
		return false
	}
	return cond.Holds(st.Status)
}

// ruleMetLocked counts the conditions of a rule that hold and describes
// each one ("Pi-hole › DNS is down"), in the rule's order (caller holds e.mu).
func (e *Engine) ruleMetLocked(r model.Rule, now time.Time) (int, []string) {
	var met []string
	for _, cond := range r.Conditions {
		if cond.Kind != model.RuleConditionStatus {
			continue
		}
		switch {
		case cond.CheckID != nil:
			c, ok := e.checks[*cond.CheckID]
			if ok && e.conditionHoldsLocked(cond, c, now) {
				met = append(met, e.describeLocked(c))
			}
		case cond.NodeID != nil:
			n, ok := e.nodes[*cond.NodeID]
			if !ok {
				continue
			}
			for _, c := range n.Checks {
				if e.conditionHoldsLocked(cond, c, now) {
					met = append(met, e.describeLocked(c))
					break
				}
			}
		}
	}
	return len(met), met
}

func (e *Engine) describeLocked(c model.Check) string {
	n := e.nodes[c.NodeID]
	status := model.StatusUnknown
	if st := e.states[c.ID]; st != nil {
		status = st.Status
	}
	return fmt.Sprintf("%s › %s is %s", n.Name, c.Name, status)
}

// ruleOutcome is one rule whose state changed in an evaluation and what is
// to be done about it once the lock is released.
type ruleOutcome struct {
	rule     model.Rule
	state    model.RuleState
	met      []string
	fired    bool // not-met → met
	cleared  bool // met → not-met
	notify   bool // run the actions
	cooldown string
}

// evaluateRules re-evaluates the enabled rules — all of them, or only those
// that look at the given check — and acts on the ones whose state changed:
// stores the new state, records a rule_fired or rule_cleared event, and
// runs the actions in the background. A disabled rule is put back to
// not-met without a word.
func (e *Engine) evaluateRules(ctx context.Context, changedCheck *int64) {
	e.mu.Lock()
	now := time.Now()
	instance := e.settings.General.InstanceName
	var out []ruleOutcome
	for _, r := range e.rules {
		st := e.ruleStates[r.ID]
		if st == nil {
			continue
		}
		if !r.Enabled {
			if st.Met {
				st.Met, st.Since = false, nil
				out = append(out, ruleOutcome{rule: r, state: *st})
			}
			continue
		}
		if changedCheck != nil && !e.ruleRefersToLocked(r, *changedCheck) {
			continue
		}
		count, met := e.ruleMetLocked(r, now)
		isMet := count >= r.Needed()
		if isMet == st.Met {
			continue
		}
		o := ruleOutcome{rule: r, met: met}
		prevSince, prevFired := st.Since, st.LastFiredAt
		st.Met, st.Since = isMet, ptrTime(now)
		if isMet {
			o.fired = true
			if r.CooldownMinutes > 0 && prevFired != nil && now.Sub(*prevFired) < time.Duration(r.CooldownMinutes)*time.Minute {
				o.cooldown = humanDuration(now.Sub(*prevFired))
			} else {
				o.notify = true
				st.LastFiredAt = ptrTime(now)
			}
		} else {
			o.cleared = true
			// The clearing is worth a message only when the firing that
			// opened this episode sent one; a firing the cooldown held back
			// clears as quietly as it came.
			o.notify = r.NotifyCleared && prevFired != nil && prevSince != nil && !prevFired.Before(*prevSince)
		}
		o.state = *st
		out = append(out, o)
	}
	e.mu.Unlock()
	for _, o := range out {
		if err := e.store.PutRuleState(ctx, o.state); err != nil {
			e.log.Errorf("save rule state: %v", err)
		}
		if !o.fired && !o.cleared {
			continue
		}
		event := model.EventRuleFired
		if o.cleared {
			event = model.EventRuleCleared
		}
		vars := e.ruleVars(o, string(event), now, instance)
		e.wg.Add(1)
		go func(o ruleOutcome, vars actions.Vars) {
			defer e.wg.Done()
			e.runRule(context.Background(), o, vars, now)
		}(o, vars)
	}
	if len(out) > 0 {
		e.broadcast(Update{Kind: "rule"})
	}
}

// ruleSentence is what a rule's message says: how many conditions hold and
// which ones. "2 of 3 conditions met — Pi-hole › DNS is down, Router › DNS
// is down"; on clearing, what is still failing, if anything.
func ruleSentence(r model.Rule, met []string, cleared bool) string {
	total := len(r.Conditions)
	if cleared {
		if len(met) == 0 {
			return fmt.Sprintf("none of the %d conditions is met any more", total)
		}
		return fmt.Sprintf("only %d of %d conditions still met (%d needed) — %s", len(met), total, r.Needed(), strings.Join(met, ", "))
	}
	return fmt.Sprintf("%d of %d conditions met — %s", len(met), total, strings.Join(met, ", "))
}

// ruleVars builds the placeholder values for a rule's actions. node.name
// carries the rule's name and status is "met" or "cleared", so the default
// notification text ("{{node.name}} is {{status}}: {{message}}") reads as
// a sentence without a rule-specific template.
func (e *Engine) ruleVars(o ruleOutcome, event string, now time.Time, instance string) actions.Vars {
	r := o.rule
	status := "met"
	if o.cleared {
		status = "cleared"
	}
	sentence := ruleSentence(r, o.met, o.cleared)
	return actions.Vars{
		"event":           event,
		"rule.id":         strconv.FormatInt(r.ID, 10),
		"rule.name":       r.Name,
		"rule.join":       r.Join,
		"rule.needed":     strconv.Itoa(r.Needed()),
		"rule.total":      strconv.Itoa(len(r.Conditions)),
		"rule.met":        strconv.Itoa(len(o.met)),
		"rule.conditions": strings.Join(o.met, ", "),
		"rule.summary":    fmt.Sprintf("Rule '%s' %s: %s", r.Name, map[bool]string{true: "cleared", false: "fired"}[o.cleared], sentence),
		"node.name":       r.Name,
		"node.host":       "",
		"check.name":      "",
		"check.type":      "",
		"target":          "",
		"status":          status,
		"prev_status":     "",
		"message":         sentence,
		"error":           "",
		"success":         strconv.FormatBool(o.cleared),
		"ts":              now.Format(time.RFC3339),
		"instance":        instance,
	}
}

// runRule runs a rule's actions (when the outcome asks for it) and records
// the timeline event for the state change, dated to when it happened rather
// than to when the last action finished.
func (e *Engine) runRule(ctx context.Context, o ruleOutcome, vars actions.Vars, at time.Time) {
	r := o.rule
	title := "Rule fired: " + r.Name
	detail := ruleSentence(r, o.met, o.cleared)
	event := model.EventRuleFired
	if o.cleared {
		title = "Rule cleared: " + r.Name
		event = model.EventRuleCleared
	}
	detail = strings.ToUpper(detail[:1]) + detail[1:] + "."
	switch {
	case o.notify:
		var ran, failed []string
		for _, a := range r.Actions {
			res := e.Actions().Run(ctx, a, vars)
			if res.OK {
				ran = append(ran, string(a.Type))
				e.log.Printf("rule %q ran %s (%d ms)", r.Name, a.Type, res.DurationMS)
			} else {
				failed = append(failed, fmt.Sprintf("%s: %s", a.Type, firstLine(res.Error)))
				e.log.Errorf("rule %q action %s failed: %s", r.Name, a.Type, res.Error)
			}
		}
		if len(ran) > 0 {
			detail += " Ran " + strings.Join(ran, ", ") + "."
		}
		if len(failed) > 0 {
			detail += " Failed — " + strings.Join(failed, "; ") + "."
		}
	case o.fired && o.cooldown != "":
		detail += fmt.Sprintf(" Actions held back: they last ran %s ago and the cooldown is %d min.", o.cooldown, r.CooldownMinutes)
	case o.cleared && r.NotifyCleared:
		detail += " No notification: the firing before it was held back by the cooldown."
	}
	e.recordEvent(model.Event{Timestamp: at, Type: event, Title: title, Detail: detail})
	e.broadcast(Update{Kind: "rule"})
}

// TestRule runs a rule's actions once with sample values, recording
// nothing, so a freshly saved rule can be tried without waiting for an
// outage.
func (e *Engine) TestRule(ctx context.Context, id int64) ([]model.ActionResult, error) {
	r, err := e.store.GetRule(ctx, id)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	instance := e.settings.General.InstanceName
	var met []string
	for i, cond := range r.Conditions {
		if i >= r.Needed() {
			break
		}
		name := "Example node › Ping"
		switch {
		case cond.CheckID != nil:
			if c, ok := e.checks[*cond.CheckID]; ok {
				name = e.nodes[c.NodeID].Name + " › " + c.Name
			}
		case cond.NodeID != nil:
			if n, ok := e.nodes[*cond.NodeID]; ok && len(n.Checks) > 0 {
				name = n.Name + " › " + n.Checks[0].Name
			}
		}
		met = append(met, fmt.Sprintf("%s is %s", name, cond.Status))
	}
	e.mu.Unlock()
	o := ruleOutcome{rule: r, met: met, fired: true, notify: true}
	vars := e.ruleVars(o, "test", time.Now(), instance)
	out := make([]model.ActionResult, 0, len(r.Actions))
	for _, a := range r.Actions {
		out = append(out, e.Actions().Run(ctx, a, vars))
	}
	return out, nil
}

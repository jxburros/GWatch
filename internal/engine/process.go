package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/actions"
	"github.com/jxburros/GWatch/internal/mailer"
	"github.com/jxburros/GWatch/internal/model"
)

// mailTask is one alert: the e-mail and, alongside it, the Settings › Alerts
// "also notify" channels that want it (see sendMail). The result and state are
// kept so the channels get the same {{placeholders}} a trigger would.
type mailTask struct {
	kind    string
	check   model.Check
	node    model.Node
	status  model.Status
	message string
	details map[string]string
	to      []string
	result  model.Result
	state   model.CheckState
	// channelsOnly marks a recovery that is sent only to the incident
	// channels (PagerDuty, Opsgenie): recovery e-mails are switched off, but
	// the page those channels opened would otherwise never be resolved.
	channelsOnly bool
	// skipEmail marks a warning whose e-mail went into the warning digest
	// (see alertbatch.go): the channels still get it, the e-mail does not.
	skipEmail bool
	// group, when set, makes this task stand for a grouped alert about
	// several checks on one node (see alertbatch.go): it carries the
	// summary for the channels, and every member's timeline gets the
	// channel's outcome.
	group []mailTask
}

type downDecision struct {
	send        bool
	reason      string // maintenance | silenced | dependency | cooldown | disabled
	needProbe   bool
	parentCheck *model.Check
	parentNode  *model.Node
	cooldown    int // minutes, the check's own when it overrides the global one
}

// process applies a result to the check state, decides on alerts and persists
// everything. depth guards recursive dependency probing.
func (e *Engine) process(ctx context.Context, c model.Check, n model.Node, r model.Result, depth int) (model.Result, error) {
	now := r.Timestamp
	e.mu.Lock()
	if pending := e.mailDone[c.ID]; pending != nil {
		e.mu.Unlock()
		select {
		case <-ctx.Done():
			return r, ctx.Err()
		case <-pending:
		}
		e.mu.Lock()
	}
	st, ok := e.states[c.ID]
	if !ok {
		e.mu.Unlock()
		return r, nil // check was deleted while running
	}
	// Refresh check/node in case config changed while the check ran.
	if cc, ok := e.checks[c.ID]; ok {
		c = cc
	}
	if nn, ok := e.nodes[c.NodeID]; ok {
		n = nn
	}
	if !c.Enabled || !n.Enabled {
		e.mu.Unlock()
		return r, nil
	}
	// Stage transitions separately; a failed transaction must remain retryable.
	live := st
	staged := *st
	st = &staged
	settings := e.settings
	var events []model.Event
	var mails []mailTask

	e.lastCheckAt = ptrTime(now)
	if r.Success {
		e.lastOKAt = ptrTime(now)
	}
	st.LastRunAt = ptrTime(now)
	if c.Enabled && n.Enabled {
		st.NextRunAt = ptrTime(now.Add(time.Duration(c.IntervalSeconds) * time.Second))
	}
	st.LastMessage = r.Message
	if r.Error != "" && r.Message == "" {
		st.LastMessage = r.Error
	}
	st.LastLatencyMS = r.LatencyMS

	threshold := c.FailureThreshold
	if threshold <= 0 {
		threshold = settings.Alerts.FailureThreshold
	}
	if threshold <= 0 {
		threshold = 1
	}
	resumeFromCollector(st, threshold)
	prev := st.Status
	evaluateDown := false
	ev := func(t model.EventType, title, detail string, meta any) {
		e := model.Event{Timestamp: now, Type: t, NodeID: ptrInt64(n.ID), CheckID: ptrInt64(c.ID), NodeName: n.Name, CheckName: c.Name, Title: title, Detail: detail}
		if meta != nil {
			if b, err := json.Marshal(meta); err == nil {
				e.Meta = b
			}
		}
		events = append(events, e)
	}

	if !r.Success {
		st.ConsecutiveFailures++
		if st.Status != model.StatusDown && st.ConsecutiveFailures >= threshold {
			st.Status = model.StatusDown
			st.LastChangeAt = ptrTime(now)
			st.WarningActive = false
			ev(model.EventDown, "Down", failureDetail(r, st.ConsecutiveFailures), nil)
			evaluateDown = true
		} else if st.Status == model.StatusDown && !st.AlertActive {
			// Still down and no notification was sent yet (suppressed): re-evaluate,
			// the suppression reason may no longer apply.
			evaluateDown = true
		}
	} else {
		st.ConsecutiveFailures = 0
		st.LastSuccessAt = ptrTime(now)
		if st.Status == model.StatusDown {
			dur := ""
			if st.LastChangeAt != nil {
				dur = " after " + humanDuration(now.Sub(*st.LastChangeAt))
			}
			ev(model.EventRecovered, "Recovered"+dur, r.Message, nil)
			if st.AlertActive {
				if e.notifyRecovery(c, settings) {
					mails = append(mails, e.newMail("recovered", c, n, r, st, settings))
				} else if settings.Alerts.Enabled && hasIncidentChannel(settings.Alerts.Channels, n) {
					m := e.newMail("recovered", c, n, r, st, settings)
					m.channelsOnly = true
					mails = append(mails, m)
				}
			}
			st.AlertActive = false
			st.AlertSuppressed = false
			st.SuppressReason = ""
			st.AffectedByCheckID = nil
			st.AffectedByNodeName = ""
			st.LastChangeAt = ptrTime(now)
		}
		newStatus := r.Status
		if newStatus != model.StatusDegraded {
			newStatus = model.StatusUp
		}
		// Certificate warnings (tracked separately so the timeline shows begin/clear).
		certWarn := false
		if r.Details.Cert != nil {
			warnDays := effectiveCertWarnDays(c, settings)
			certWarn = r.Details.Cert.DaysRemaining <= warnDays || !r.Details.Cert.Valid
		}
		if certWarn && !st.CertWarningActive {
			st.CertWarningActive = true
			detail := fmt.Sprintf("Certificate for %s expires in %d days (%s).", r.Details.Cert.Subject, r.Details.Cert.DaysRemaining, r.Details.Cert.NotAfter.Local().Format("2006-01-02"))
			if !r.Details.Cert.Valid {
				detail = "Certificate is not valid: " + r.Details.Cert.Error
			}
			ev(model.EventCertWarning, "Certificate warning", detail, r.Details.Cert)
			if e.canWarn(c, n, st, settings, now) {
				m := e.newMail("warning", c, n, r, st, settings)
				m.message = detail
				mails = append(mails, m)
			}
		} else if !certWarn && st.CertWarningActive {
			st.CertWarningActive = false
			ev(model.EventCertWarningCleared, "Certificate warning cleared", "The certificate is valid and not close to expiry.", nil)
		}
		// Content change detection: one event per change, then re-baseline.
		if r.Details.ContentChanged {
			detail := "The response/content differs from the previous observation. This is not a security conclusion: dynamic pages, ads, timestamps and login pages also cause changes."
			ev(model.EventContentChanged, "Response/content changed", detail, map[string]string{"previous": st.LastContentValue, "current": r.Details.ContentValue})
			if e.canWarn(c, n, st, settings, now) {
				m := e.newMail("warning", c, n, r, st, settings)
				m.message = "Response/content changed"
				mails = append(mails, m)
			}
		}
		if r.Details.ContentHash != "" {
			st.LastContentHash = r.Details.ContentHash
		}
		if r.Details.ContentHash != "" || r.Details.ContentValue != "" {
			st.LastContentValue = r.Details.ContentValue
		}
		// Generic degraded warnings (latency, packet loss ...). A check that
		// judges its metrics one by one has its warnings tracked per metric
		// below instead, so the same disk is not reported twice.
		if len(r.Details.MetricResults) > 0 {
			st.WarningActive = false
		} else if newStatus == model.StatusDegraded && !st.WarningActive {
			st.WarningActive = true
			detail := strings.Join(r.Warnings, "; ")
			if detail == "" {
				detail = r.Message
			}
			ev(model.EventWarning, "Degraded", detail, nil)
			if !r.Details.ContentChanged && !(certWarn && onlyCertWarning(r)) && e.canWarn(c, n, st, settings, now) {
				m := e.newMail("warning", c, n, r, st, settings)
				m.message = detail
				mails = append(mails, m)
			}
		} else if newStatus == model.StatusUp && st.WarningActive {
			st.WarningActive = false
			ev(model.EventWarningCleared, "Back to normal", r.Message, nil)
		}
		if st.Status != newStatus {
			st.Status = newStatus
			st.LastChangeAt = ptrTime(now)
		}
	}

	// Metrics judged one by one (a hardware check's processor, memory, each
	// disk ...). Each has its own warning that begins and clears on its own
	// timeline, the way certificate warnings do, so that "disk /srv is
	// filling" stays open while "memory is high" comes and goes. This runs
	// whether or not the check as a whole passed: a critical disk fails the
	// check, and is still the disk's own incident.
	if len(r.Details.MetricResults) > 0 {
		var began []string
		for _, mr := range r.Details.MetricResults {
			prev := st.MetricStatus[mr.Key]
			if prev == "" || prev == model.StatusUnknown {
				prev = model.StatusUp
			}
			if mr.Status == prev {
				continue
			}
			switch {
			case mr.Status == model.StatusUp:
				evm(&events, now, n, c, mr.Key, model.EventWarningCleared, mr.Label+" back to normal", fmt.Sprintf("%s is %s.", mr.Label, metricValue(mr)))
			case prev == model.StatusUp || mr.Status == model.StatusDown:
				// A new warning, or one that has escalated to critical: both
				// are worth a line in the timeline.
				title := mr.Label + " warning"
				if mr.Status == model.StatusDown {
					title = mr.Label + " critical"
				}
				evm(&events, now, n, c, mr.Key, model.EventWarning, title, mr.Reason)
				if prev == model.StatusUp {
					began = append(began, mr.Reason)
				}
			}
		}
		// A metric the machine no longer reports (an unplugged volume) has
		// nothing left to warn about, so its incident closes rather than
		// staying open forever.
		seen := make(map[string]bool, len(r.Details.MetricResults))
		for _, mr := range r.Details.MetricResults {
			seen[mr.Key] = true
		}
		for key, status := range st.MetricStatus {
			if !seen[key] && status != model.StatusUp {
				evm(&events, now, n, c, key, model.EventWarningCleared, key+" no longer reported", "The machine stopped reporting this reading, so its warning is closed.")
			}
		}
		st.MetricStatus = nil
		for _, mr := range r.Details.MetricResults {
			if mr.Status != model.StatusUp {
				if st.MetricStatus == nil {
					st.MetricStatus = map[string]model.Status{}
				}
				st.MetricStatus[mr.Key] = mr.Status
			}
		}
		// One warning email for everything that began this run. A metric that
		// went straight to critical is covered by the down alert instead.
		if len(began) > 0 && r.Success && e.canWarn(c, n, st, settings, now) {
			m := e.newMail("warning", c, n, r, st, settings)
			m.message = strings.Join(began, "; ")
			mails = append(mails, m)
		}
	}

	if evaluateDown {
		d := e.evaluateDownLocked(c, n, st, settings, now, depth < 3)
		if d.needProbe {
			e.mu.Unlock()
			e.probeParent(ctx, n, depth)
			e.mu.Lock()
			if current := e.states[c.ID]; current != live || !e.checks[c.ID].Enabled || !e.nodes[c.NodeID].Enabled {
				e.mu.Unlock()
				return r, nil
			}
			st.SilencedUntil = live.SilencedUntil
			d = e.evaluateDownLocked(c, n, st, settings, now, false)
		}
		if d.send {
			st.AlertActive = true
			st.AlertSuppressed = false
			st.SuppressReason = ""
			st.AffectedByCheckID = nil
			st.AffectedByNodeName = ""
			st.LastAlertAt = ptrTime(now)
			mails = append(mails, e.newMail("down", c, n, r, st, settings))
		} else if d.reason != "" {
			changed := !st.AlertSuppressed || st.SuppressReason != d.reason
			st.AlertSuppressed = true
			st.SuppressReason = d.reason
			if d.reason == "dependency" && d.parentCheck != nil {
				if st.AffectedByCheckID == nil || *st.AffectedByCheckID != d.parentCheck.ID {
					ev(model.EventAffectedByParent, fmt.Sprintf("%s unavailable because %s is down", n.Name, d.parentNode.Name),
						fmt.Sprintf("%s — %s is failing while its dependency %s (%s) is down. Recorded as affected by the parent outage.", n.Name, c.Name, d.parentNode.Name, d.parentCheck.Name),
						map[string]any{"parentNodeId": d.parentNode.ID, "parentCheckId": d.parentCheck.ID})
				}
				st.AffectedByCheckID = ptrInt64(d.parentCheck.ID)
				st.AffectedByNodeName = d.parentNode.Name
			}
			if changed && d.reason != "disabled" {
				ev(model.EventAlertSuppressed, "Alert suppressed ("+suppressLabel(d.reason)+")", suppressDetail(d, st, settings), map[string]string{"reason": d.reason})
			}
		}
	}

	snapshot := *st
	stored, err := e.recordResult(ctx, r, snapshot)
	if err == nil {
		*live = snapshot
	}
	e.mu.Unlock()
	if err != nil {
		e.log.Errorf("record result for check %d: %v", c.ID, err)
		return r, err
	}
	for _, evt := range events {
		e.recordEvent(evt)
	}
	e.broadcast(Update{Kind: "result", CheckID: c.ID, NodeID: n.ID})
	if prev != snapshot.Status {
		e.broadcast(Update{Kind: "state", CheckID: c.ID, NodeID: n.ID})
	}
	e.dispatchAlerts(c, prev, snapshot, mails)
	e.fireTriggers(triggerContext{check: c, node: n, result: stored, state: snapshot, prev: prev, events: events})
	// A status change may bring a notification rule's conditions together
	// or apart; only the rules that look at this check need another look.
	if prev != snapshot.Status {
		e.evaluateRules(ctx, ptrInt64(c.ID))
	}
	return stored, nil
}

// evm appends an event about one of a check's metrics: the same shape as the
// check-level events process writes, with Metric naming which reading it is
// about so the timeline and the triggers can tell a disk from the memory.
func evm(events *[]model.Event, now time.Time, n model.Node, c model.Check, metric string, t model.EventType, title, detail string) {
	*events = append(*events, model.Event{Timestamp: now, Type: t, NodeID: ptrInt64(n.ID), CheckID: ptrInt64(c.ID), NodeName: n.Name, CheckName: c.Name, Title: title, Detail: detail, Metric: metric})
}

// metricValue renders a metric's reading with its unit, for event details and
// email rows.
func metricValue(mr model.MetricResult) string {
	switch mr.Unit {
	case "%":
		return fmt.Sprintf("%.0f%%", mr.Value)
	case "":
		return fmt.Sprintf("%.2f", mr.Value)
	}
	return fmt.Sprintf("%.0f %s", mr.Value, mr.Unit)
}

func onlyCertWarning(r model.Result) bool {
	for _, w := range r.Warnings {
		if !strings.Contains(strings.ToLower(w), "certificate") {
			return false
		}
	}
	return len(r.Warnings) > 0
}

func failureDetail(r model.Result, failures int) string {
	msg := r.Message
	if r.Error != "" {
		if msg == "" {
			msg = r.Error
		} else if !strings.Contains(msg, r.Error) && !strings.Contains(r.Error, msg) {
			msg = msg + " — " + r.Error
		}
	}
	if msg == "" {
		msg = "Check failed"
	}
	return fmt.Sprintf("%s (%d consecutive failures)", msg, failures)
}

func suppressLabel(reason string) string {
	switch reason {
	case "dependency":
		return "parent is down"
	}
	return reason
}

func suppressDetail(d downDecision, st *model.CheckState, s model.Settings) string {
	switch d.reason {
	case "maintenance":
		return "A maintenance window is active; no email was sent."
	case "silenced":
		until := ""
		if st.SilencedUntil != nil {
			until = " until " + st.SilencedUntil.Format("15:04")
		}
		return "This check was manually silenced" + until + "; no email was sent."
	case "dependency":
		if d.parentNode != nil {
			return fmt.Sprintf("%s is down, so this failure is treated as a downstream effect; no separate email was sent.", d.parentNode.Name)
		}
		return "A dependency is down; no separate email was sent."
	case "cooldown":
		return fmt.Sprintf("An alert for this check was sent less than %d minutes ago (cooldown); no email was sent.", d.cooldown)
	case "disabled":
		return "Email alerts are disabled for this check."
	}
	return "No email was sent."
}

// evaluateDownLocked decides whether a down notification should be sent.
func (e *Engine) evaluateDownLocked(c model.Check, n model.Node, st *model.CheckState, s model.Settings, now time.Time, allowProbe bool) downDecision {
	if acknowledged, err := e.store.NodeIncidentAcknowledged(context.Background(), n.ID); err == nil && acknowledged {
		return downDecision{reason: "acknowledged"}
	}
	if !s.Alerts.Enabled || (c.Alerts != nil && c.Alerts.Enabled != nil && !*c.Alerts.Enabled) {
		return downDecision{reason: "disabled"}
	}
	if e.inMaintenanceLocked(n, now) {
		return downDecision{reason: "maintenance"}
	}
	if st.SilencedUntil != nil && st.SilencedUntil.After(now) {
		return downDecision{reason: "silenced"}
	}
	if n.DependsOnNode != nil {
		pc, pn := e.parentDownLocked(n)
		if pc != nil {
			return downDecision{reason: "dependency", parentCheck: pc, parentNode: pn}
		}
		if allowProbe {
			return downDecision{needProbe: true}
		}
	}
	cooldown := s.Alerts.CooldownMinutes
	if c.Alerts != nil && c.Alerts.CooldownMinutes != nil {
		cooldown = *c.Alerts.CooldownMinutes
	}
	if cooldown > 0 && st.LastAlertAt != nil && now.Sub(*st.LastAlertAt) < time.Duration(cooldown)*time.Minute {
		return downDecision{reason: "cooldown", cooldown: cooldown}
	}
	return downDecision{send: true}
}

// canWarn reports whether a warning email may be sent for the check now.
func (e *Engine) canWarn(c model.Check, n model.Node, st *model.CheckState, s model.Settings, now time.Time) bool {
	if !s.Alerts.Enabled {
		return false
	}
	if c.Alerts != nil && c.Alerts.Enabled != nil && !*c.Alerts.Enabled {
		return false
	}
	notify := s.Alerts.NotifyWarnings
	if c.Alerts != nil && c.Alerts.NotifyWarnings != nil {
		notify = *c.Alerts.NotifyWarnings
	}
	if !notify || e.inMaintenanceLocked(n, now) || (st.SilencedUntil != nil && st.SilencedUntil.After(now)) {
		return false
	}
	cooldown := s.Alerts.CooldownMinutes
	if c.Alerts != nil && c.Alerts.CooldownMinutes != nil {
		cooldown = *c.Alerts.CooldownMinutes
	}
	if cooldown > 0 && st.LastWarnAt != nil && now.Sub(*st.LastWarnAt) < time.Duration(cooldown)*time.Minute {
		return false
	}
	st.LastWarnAt = ptrTime(now)
	return true
}

func (e *Engine) notifyRecovery(c model.Check, s model.Settings) bool {
	if !s.Alerts.Enabled {
		return false
	}
	if c.Alerts != nil && c.Alerts.NotifyRecovery != nil {
		return *c.Alerts.NotifyRecovery
	}
	return s.Alerts.NotifyRecovery
}

// parentDownLocked walks up the dependency chain and returns the first parent
// check that is currently down.
func (e *Engine) parentDownLocked(n model.Node) (*model.Check, *model.Node) {
	visited := map[int64]bool{n.ID: true}
	cur := n
	for cur.DependsOnNode != nil && !visited[*cur.DependsOnNode] {
		p, ok := e.nodes[*cur.DependsOnNode]
		if !ok {
			return nil, nil
		}
		visited[p.ID] = true
		for _, pc := range p.Checks {
			if !p.Enabled || !pc.Enabled || (pc.Type != model.CheckPing && pc.Type != model.CheckTCP) {
				continue
			}
			// A parent that is down, or that is currently failing (its own
			// threshold may not be reached yet right after a probe), explains
			// the child failure. If the parent turns out fine the child is
			// re-evaluated on its next result and alerted on its own.
			if st := e.states[pc.ID]; st != nil && (st.Status == model.StatusDown || st.ConsecutiveFailures > 0) {
				pcCopy, pCopy := pc, p
				return &pcCopy, &pCopy
			}
		}
		cur = p
	}
	return nil, nil
}

// probeParent runs the parent's checks right now so a dependency outage is
// known before the child alert is decided.
func (e *Engine) probeParent(ctx context.Context, n model.Node, depth int) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	e.mu.Lock()
	var ids []int64
	if n.DependsOnNode != nil {
		if p, ok := e.nodes[*n.DependsOnNode]; ok && p.Enabled {
			for _, pc := range p.Checks {
				// A parent checked by a remote collector cannot be probed
				// from here; its state is whatever the collector last sent.
				if _, collected := p.CollectorFor(pc); collected {
					continue
				}
				if pc.Enabled && (pc.Type == model.CheckPing || pc.Type == model.CheckTCP) {
					ids = append(ids, pc.ID)
				}
			}
		}
	}
	e.mu.Unlock()
	for _, id := range ids {
		e.log.Printf("probing dependency check %d before alerting for %s", id, n.Name)
		_, _ = e.runAndProcess(ctx, id, depth+1)
	}
}

// inMaintenanceLocked reports whether any active window covers the node.
func (e *Engine) inMaintenanceLocked(n model.Node, now time.Time) bool {
	for _, w := range e.windows {
		if !w.Active(now) {
			continue
		}
		if windowCovers(w, n) {
			return true
		}
	}
	return false
}

func windowCovers(w model.MaintenanceWindow, n model.Node) bool {
	if w.NodeID != nil {
		return *w.NodeID == n.ID
	}
	// A window still names one group, and covers every node that has that
	// group among its own — being in a group is being in a group, whether or
	// not the node is in others as well.
	if strings.TrimSpace(w.Group) != "" {
		return n.InGroup(w.Group)
	}
	return true
}

// ---- email ----

func (e *Engine) newMail(kind string, c model.Check, n model.Node, r model.Result, st *model.CheckState, s model.Settings) mailTask {
	to := s.Alerts.Recipients
	if c.Alerts != nil && len(c.Alerts.Recipients) > 0 {
		to = c.Alerts.Recipients
	}
	details := map[string]string{
		"Node":   n.Name,
		"Check":  fmt.Sprintf("%s (%s)", c.Name, c.Type.Label()),
		"Target": c.Target(n.Host),
		"Time":   r.Timestamp.Format("2006-01-02 15:04:05"),
	}
	if r.Error != "" {
		details["Error"] = r.Error
	}
	if r.LatencyMS != nil {
		details["Latency"] = fmt.Sprintf("%.1f ms", *r.LatencyMS)
	}
	if r.Details.StatusCode != 0 {
		details["HTTP status"] = fmt.Sprintf("%d", r.Details.StatusCode)
	}
	if r.Details.FinalURL != "" {
		details["Final URL"] = r.Details.FinalURL
	}
	if r.LossPct != nil {
		details["Packet loss"] = fmt.Sprintf("%.0f%%", *r.LossPct)
	}
	if r.Details.Cert != nil {
		details["Certificate expires"] = fmt.Sprintf("%s (%d days)", r.Details.Cert.NotAfter.Local().Format("2006-01-02"), r.Details.Cert.DaysRemaining)
	}
	// One row per metric that is not within its thresholds, so a hardware
	// alert says which disk rather than only that "the check" is degraded.
	for _, mr := range r.Details.MetricResults {
		if mr.Status == model.StatusUp {
			continue
		}
		verdict := "warning"
		if mr.Status == model.StatusDown {
			verdict = "critical"
		}
		details[mr.Label] = fmt.Sprintf("%s (%s)", metricValue(mr), verdict)
	}
	if st.ConsecutiveFailures > 0 {
		details["Consecutive failures"] = fmt.Sprintf("%d", st.ConsecutiveFailures)
	}
	if groups := n.GroupList(); len(groups) > 0 {
		details["Group"] = strings.Join(groups, ", ")
	}
	msg := r.Message
	if r.Error != "" && msg == "" {
		msg = r.Error
	}
	return mailTask{kind: kind, check: c, node: n, status: st.Status, message: msg, details: details, to: to, result: r, state: *st}
}

// sendMail delivers one alert: the e-mail to its recipients and the same
// alert to every Settings › Alerts "also notify" channel that wants it (by
// severity and node group, see model.AlertChannel.Wants). Both go out from
// this one place, which is reached only after process has decided that an
// alert is due, so a channel is held back by exactly what holds back the
// e-mail — a maintenance window, a silence, a parent that is down, an
// acknowledged incident, the cooldown, alerts switched off for the check —
// without repeating any of those rules.
//
// A down alert counts as delivered when the e-mail or at least one channel
// got through. Only when nothing did is the alert re-armed (failedMail), so
// the next failing result tries again; re-arming after a failed e-mail while
// Slack or PagerDuty already had it would page those channels twice.
func (e *Engine) sendMail(m mailTask) {
	e.mu.Lock()
	s := e.settings
	e.mu.Unlock()
	channels := alertChannelsFor(s.Alerts.Channels, m)
	email := !m.channelsOnly && !m.skipEmail && len(m.to) > 0
	if !email && len(channels) == 0 {
		if m.channelsOnly || m.skipEmail {
			return
		}
		e.failedMail(m)
		e.recordEvent(model.Event{Type: model.EventAlertFailed, NodeID: ptrInt64(m.node.ID), CheckID: ptrInt64(m.check.ID), NodeName: m.node.Name, CheckName: m.check.Name,
			Title: "Alert not sent: no recipients configured", Detail: "Add recipient addresses or a notification channel under Settings › Alerts."})
		return
	}
	var msg mailer.Message
	if email {
		msg = mailer.BuildAlertEmail(m.kind, s.General.InstanceName, m.node.Name, m.check.Name, m.status, m.message, m.details, time.Now())
		msg.To = m.to
	}
	e.mu.Lock()
	done := make(chan struct{})
	e.mailDone[m.check.ID] = done
	e.mu.Unlock()
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		defer e.releaseMailDone(done, m)
		label := alertLabel(m.kind)
		delivered := false
		if email {
			err := e.deliverMail(s.Alerts.SMTP, msg)
			e.mu.Lock()
			e.lastAlertAt = ptrTime(time.Now())
			if err != nil {
				e.lastAlertErr = err.Error()
			} else {
				e.lastAlertErr = ""
			}
			e.mu.Unlock()
			if err != nil {
				e.log.Errorf("send %s email for %s/%s: %v", m.kind, m.node.Name, m.check.Name, err)
				e.recordEvent(model.Event{Type: model.EventAlertFailed, NodeID: ptrInt64(m.node.ID), CheckID: ptrInt64(m.check.ID), NodeName: m.node.Name, CheckName: m.check.Name,
					Title: label + " email failed", Detail: err.Error()})
			} else {
				delivered = true
				e.recordEvent(model.Event{Type: model.EventAlertSent, NodeID: ptrInt64(m.node.ID), CheckID: ptrInt64(m.check.ID), NodeName: m.node.Name, CheckName: m.check.Name,
					Title: label + " emailed", Detail: "Sent to " + strings.Join(m.to, ", ")})
			}
		}
		for _, ch := range channels {
			if e.deliverChannel(ch, m, s.General.InstanceName, label) {
				delivered = true
			}
		}
		if !delivered {
			e.failedMail(m)
		}
	}()
}

// alertLabel is how an alert of a kind is named in the timeline.
func alertLabel(kind string) string {
	if l := map[string]string{"down": "Down alert", "recovered": "Recovery notification", "warning": "Warning", "warning_cleared": "Warning cleared"}[kind]; l != "" {
		return l
	}
	return "Alert"
}

// alertChannelsFor picks the "also notify" channels an alert goes to. A
// channels-only recovery (see mailTask.channelsOnly) goes to the incident
// channels alone.
func alertChannelsFor(all []model.AlertChannel, m mailTask) []model.AlertChannel {
	var out []model.AlertChannel
	for _, ch := range all {
		if !ch.Wants(m.kind, m.node) {
			continue
		}
		if m.channelsOnly && !ch.Action.Type.IsIncident() {
			continue
		}
		out = append(out, ch)
	}
	return out
}

// hasIncidentChannel reports whether a PagerDuty or Opsgenie channel would
// receive the recovery of an outage on node n.
func hasIncidentChannel(all []model.AlertChannel, n model.Node) bool {
	for _, ch := range all {
		if ch.Action.Type.IsIncident() && ch.Wants("recovered", n) {
			return true
		}
	}
	return false
}

// alertVars are the placeholders an alert channel is run with: the same set
// a trigger gets (TriggerVars), with {{event}} the alert kind (down,
// recovered, warning) so the incident channels resolve on a recovery, and
// {{status}} saying what the check is now rather than what it was when the
// alert was decided.
func alertVars(m mailTask, instance, channel string) actions.Vars {
	vars := TriggerVars(m.node, m.check, m.result, m.state, m.kind)
	vars["instance"] = instance
	vars["message"] = m.message
	vars["alert.kind"] = m.kind
	vars["alert.channel"] = channel
	// {{alert.count}} and {{alert.checks}} say how many checks, and which,
	// the alert is about: one for an ordinary alert, several for a grouped
	// one (Settings › Alerts › group alerts per node), whose {{check.*}}
	// placeholders are left empty because no single check is the subject.
	vars["alert.count"] = "1"
	vars["alert.checks"] = m.check.Name
	if len(m.group) > 0 {
		names := make([]string, 0, len(m.group))
		for _, t := range m.group {
			names = append(names, t.check.Name)
		}
		vars["alert.count"] = strconv.Itoa(len(m.group))
		vars["alert.checks"] = strings.Join(names, ", ")
		for _, k := range []string{"check.id", "check.name", "check.type", "check.target", "target"} {
			vars[k] = ""
		}
	}
	switch m.kind {
	case "down":
		vars["status"] = string(model.StatusDown)
	case "recovered", "warning_cleared":
		vars["status"] = string(model.StatusUp)
	case "warning":
		if m.result.Status != "" {
			vars["status"] = string(m.result.Status)
		}
	}
	return vars
}

// deliverChannel runs one "also notify" channel for an alert, through the
// same slots as alert e-mails and with the same single retry, and records the
// outcome in the timeline. It reports whether the channel took the alert.
func (e *Engine) deliverChannel(ch model.AlertChannel, m mailTask, instance, label string) bool {
	name := ch.Name
	if strings.TrimSpace(name) == "" {
		name = string(ch.Action.Type)
	}
	vars := alertVars(m, instance, name)
	work := e.workContext()
	attempt := func() model.ActionResult {
		select {
		case e.mailSem <- struct{}{}:
		case <-work.Done():
			return model.ActionResult{Error: work.Err().Error()}
		}
		defer func() { <-e.mailSem }()
		return e.Actions().Run(work, ch.Action, vars)
	}
	res := attempt()
	if !res.OK && mailRetryAfter > 0 && work.Err() == nil {
		select {
		case <-time.After(mailRetryAfter):
			res = attempt()
			if !res.OK && res.Error != "" {
				res.Error += " (tried twice)"
			}
		case <-work.Done():
		}
	}
	// A grouped alert is one message, but each of its checks has its own
	// timeline, so each gets the outcome.
	about := m.group
	if len(about) == 0 {
		about = []mailTask{m}
	}
	if !res.OK {
		e.log.Errorf("send %s to channel %q (%s) for %s/%s: %s", m.kind, name, ch.Action.Type, m.node.Name, vars["alert.checks"], res.Error)
	}
	for _, t := range about {
		ev := model.Event{NodeID: ptrInt64(t.node.ID), CheckID: ptrInt64(t.check.ID), NodeName: t.node.Name, CheckName: t.check.Name}
		if !res.OK {
			ev.Type, ev.Title, ev.Detail = model.EventAlertFailed, fmt.Sprintf("%s to %s failed", label, name), firstLine(res.Error)
		} else {
			ev.Type, ev.Title, ev.Detail = model.EventAlertSent, fmt.Sprintf("%s sent to %s", label, name), fmt.Sprintf("%s — %s", ch.Action.Type, firstLine(res.Output))
		}
		e.recordEvent(ev)
	}
	return res.OK
}

// TestAlertChannel sends a sample alert through one "also notify" channel,
// with the placeholders a real down alert would have, without recording
// anything. It backs the channel's Test button in Settings › Alerts.
func (e *Engine) TestAlertChannel(ctx context.Context, ch model.AlertChannel) model.ActionResult {
	e.mu.Lock()
	instance := e.settings.General.InstanceName
	e.mu.Unlock()
	now := time.Now()
	m := mailTask{
		kind:    "down",
		node:    model.Node{Name: "Example node", Host: "192.168.1.1"},
		check:   model.Check{Name: "Ping", Type: model.CheckPing},
		message: "This is a test alert from GWatch",
		result:  model.Result{Timestamp: now, Message: "This is a test alert from GWatch"},
		state:   model.CheckState{Status: model.StatusDown},
	}
	vars := alertVars(m, instance, ch.Name)
	vars["event"] = "test"
	return e.Actions().Run(ctx, ch.Action, vars)
}

// mailSlots bounds how many alert e-mails are sent at once. A router going
// down with a hundred checks behind it used to open a hundred SMTP
// connections together, which is how Gmail and Microsoft 365 rate limits are
// tripped and alerts lost.
const mailSlots = 3

// mailRetryAfter is how long a failed alert e-mail waits before its one
// retry: long enough for a greylisting server or a brief outage to pass, short
// enough that the check's next result (which waits for this e-mail) is not
// held up for long. A message that still fails re-arms the alert, so the next
// failing result sends it again.
var mailRetryAfter = 15 * time.Second

// deliverMail sends one alert e-mail through the shared slots, trying once
// more after mailRetryAfter if the first attempt fails.
func (e *Engine) deliverMail(smtp model.SMTPSettings, msg mailer.Message) error {
	work := e.workContext()
	attempt := func() error {
		select {
		case e.mailSem <- struct{}{}:
		case <-work.Done():
			return work.Err()
		}
		defer func() { <-e.mailSem }()
		ctx, cancel := context.WithTimeout(work, 45*time.Second)
		defer cancel()
		return e.opts.Send(ctx, smtp, msg)
	}
	err := attempt()
	if err == nil || mailRetryAfter <= 0 {
		return err
	}
	select {
	case <-time.After(mailRetryAfter):
	case <-work.Done():
		return err
	}
	if err2 := attempt(); err2 != nil {
		return fmt.Errorf("%w (tried twice)", err2)
	}
	return nil
}

// SendTestEmail sends a test message using the current SMTP settings.
func (e *Engine) SendTestEmail(ctx context.Context, to []string) error {
	s := e.Settings()
	if len(to) == 0 {
		to = s.Alerts.Recipients
	}
	if len(to) == 0 {
		return fmt.Errorf("no recipients configured")
	}
	if err := mailer.Validate(s.Alerts.SMTP); err != nil {
		return err
	}
	msg := mailer.BuildAlertEmail("test", s.General.InstanceName, "", "", model.StatusUp, "This is a test email from GWatch. If you can read this, email alerts are working.", map[string]string{"Sent at": time.Now().Format("2006-01-02 15:04:05")}, time.Now())
	msg.To = to
	err := e.opts.Send(ctx, s.Alerts.SMTP, msg)
	e.mu.Lock()
	e.lastAlertAt = ptrTime(time.Now())
	if err != nil {
		e.lastAlertErr = err.Error()
	} else {
		e.lastAlertErr = ""
	}
	e.mu.Unlock()
	return err
}

// ---- silence ----

// Silence mutes alerts for a check for the given duration (0 = unsilence).
func (e *Engine) Silence(ctx context.Context, checkID int64, d time.Duration) (model.CheckState, error) {
	e.mu.Lock()
	st, ok := e.states[checkID]
	if !ok {
		e.mu.Unlock()
		return model.CheckState{}, fmt.Errorf("check %d not found", checkID)
	}
	live := st
	staged := *st
	st = &staged
	c := e.checks[checkID]
	n := e.nodes[c.NodeID]
	var evt model.Event
	if d <= 0 {
		st.SilencedUntil = nil
		evt = model.Event{Type: model.EventUnsilenced, Title: "Unsilenced", Detail: "Alerts are active again."}
	} else {
		until := time.Now().Add(d)
		st.SilencedUntil = &until
		evt = model.Event{Type: model.EventSilenced, Title: "Silenced for " + humanDuration(d), Detail: "No email alerts until " + until.Format("2006-01-02 15:04") + ". Results are still recorded."}
	}
	snapshot := *st
	if err := e.store.SaveState(ctx, snapshot); err != nil {
		original := *live
		e.mu.Unlock()
		return original, err
	}
	*live = snapshot
	e.mu.Unlock()
	evt.NodeID, evt.CheckID, evt.NodeName, evt.CheckName = ptrInt64(n.ID), ptrInt64(c.ID), n.Name, c.Name
	e.recordEvent(evt)
	e.broadcast(Update{Kind: "state", CheckID: c.ID, NodeID: n.ID})
	// A silenced check stops counting towards the notification rules that
	// look at it, and counts again once the silence is lifted.
	e.evaluateRules(ctx, ptrInt64(c.ID))
	return snapshot, nil
}

func effectiveCertWarnDays(c model.Check, s model.Settings) int {
	if c.Config.CertWarnDays > 0 {
		return c.Config.CertWarnDays
	}
	if s.Alerts.CertWarnDays > 0 {
		return s.Alerts.CertWarnDays
	}
	return 14
}

// A failed down delivery must not consume the outage or its cooldown. Results
// wait for the preceding delivery before applying recovery, so this state cannot
// be mistaken for an alert the recipient actually received.
func (e *Engine) failedMail(m mailTask) {
	if m.kind != "down" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if st := e.states[m.check.ID]; st != nil && st.Status == model.StatusDown {
		st.AlertActive = false
		st.LastAlertAt = nil
		if err := e.store.SaveState(context.Background(), *st); err != nil {
			e.log.Errorf("save failed alert state: %v", err)
		}
	}
}

package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/mailer"
	"github.com/jxburros/GWatch/internal/model"
)

type mailTask struct {
	kind    string
	check   model.Check
	node    model.Node
	status  model.Status
	message string
	details map[string]string
	to      []string
}

type downDecision struct {
	send        bool
	reason      string // maintenance | silenced | dependency | cooldown | disabled
	needProbe   bool
	parentCheck *model.Check
	parentNode  *model.Node
}

// process applies a result to the check state, decides on alerts and persists
// everything. depth guards recursive dependency probing.
func (e *Engine) process(ctx context.Context, c model.Check, n model.Node, r model.Result, depth int) (model.Result, error) {
	now := r.Timestamp
	e.mu.Lock()
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
			ev(model.EventDown, fmt.Sprintf("%s — %s is down", n.Name, c.Name), failureDetail(r, st.ConsecutiveFailures), nil)
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
			ev(model.EventRecovered, fmt.Sprintf("%s — %s recovered%s", n.Name, c.Name, dur), r.Message, nil)
			if st.AlertActive && e.notifyRecovery(c, settings) {
				mails = append(mails, e.newMail("recovered", c, n, r, st, settings))
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
			warnDays := c.Config.CertWarnDays
			if warnDays <= 0 {
				warnDays = settings.Alerts.CertWarnDays
			}
			if warnDays <= 0 {
				warnDays = 14
			}
			certWarn = r.Details.Cert.DaysRemaining <= warnDays || !r.Details.Cert.Valid
		}
		if certWarn && !st.CertWarningActive {
			st.CertWarningActive = true
			detail := fmt.Sprintf("Certificate for %s expires in %d days (%s).", r.Details.Cert.Subject, r.Details.Cert.DaysRemaining, r.Details.Cert.NotAfter.Local().Format("2006-01-02"))
			if !r.Details.Cert.Valid {
				detail = "Certificate is not valid: " + r.Details.Cert.Error
			}
			ev(model.EventCertWarning, fmt.Sprintf("%s — certificate warning", n.Name), detail, r.Details.Cert)
			if e.canWarn(c, n, st, settings, now) {
				m := e.newMail("warning", c, n, r, st, settings)
				m.message = detail
				mails = append(mails, m)
			}
		} else if !certWarn && st.CertWarningActive {
			st.CertWarningActive = false
			ev(model.EventCertWarningCleared, fmt.Sprintf("%s — certificate warning cleared", n.Name), "The certificate is valid and not close to expiry.", nil)
		}
		// Content change detection: one event per change, then re-baseline.
		if r.Details.ContentChanged {
			detail := "The response/content differs from the previous observation. This is not a security conclusion: dynamic pages, ads, timestamps and login pages also cause changes."
			ev(model.EventContentChanged, fmt.Sprintf("%s — %s response/content changed", n.Name, c.Name), detail, map[string]string{"previous": st.LastContentValue, "current": r.Details.ContentValue})
			if e.canWarn(c, n, st, settings, now) {
				m := e.newMail("warning", c, n, r, st, settings)
				m.message = "Response/content changed"
				mails = append(mails, m)
			}
		}
		if r.Details.ContentHash != "" {
			st.LastContentHash = r.Details.ContentHash
		}
		if r.Details.ContentValue != "" {
			st.LastContentValue = r.Details.ContentValue
		}
		// Generic degraded warnings (latency, packet loss ...).
		if newStatus == model.StatusDegraded && !st.WarningActive {
			st.WarningActive = true
			detail := strings.Join(r.Warnings, "; ")
			if detail == "" {
				detail = r.Message
			}
			ev(model.EventWarning, fmt.Sprintf("%s — %s degraded", n.Name, c.Name), detail, nil)
			if !r.Details.ContentChanged && !(certWarn && onlyCertWarning(r)) && e.canWarn(c, n, st, settings, now) {
				m := e.newMail("warning", c, n, r, st, settings)
				m.message = detail
				mails = append(mails, m)
			}
		} else if newStatus == model.StatusUp && st.WarningActive {
			st.WarningActive = false
			ev(model.EventWarningCleared, fmt.Sprintf("%s — %s back to normal", n.Name, c.Name), r.Message, nil)
		}
		if st.Status != newStatus {
			st.Status = newStatus
			st.LastChangeAt = ptrTime(now)
		}
	}

	if evaluateDown {
		d := e.evaluateDownLocked(c, n, st, settings, now, depth < 3)
		if d.needProbe {
			e.mu.Unlock()
			e.probeParent(ctx, n, depth)
			e.mu.Lock()
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
				ev(model.EventAlertSuppressed, fmt.Sprintf("Alert suppressed for %s — %s", n.Name, c.Name), suppressDetail(d, st, settings), map[string]string{"reason": d.reason})
			}
		}
	}

	snapshot := *st
	e.mu.Unlock()

	stored, err := e.store.RecordResult(ctx, r, snapshot)
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
	for _, m := range mails {
		e.sendMail(m)
	}
	return stored, nil
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
		if msg != "" && msg != r.Error {
			msg = msg + " — " + r.Error
		} else {
			msg = r.Error
		}
	}
	if msg == "" {
		msg = "Check failed"
	}
	return fmt.Sprintf("%s (%d consecutive failures)", msg, failures)
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
		return fmt.Sprintf("An alert for this check was sent less than %d minutes ago (cooldown); no email was sent.", s.Alerts.CooldownMinutes)
	case "disabled":
		return "Email alerts are disabled for this check."
	}
	return "No email was sent."
}

// evaluateDownLocked decides whether a down notification should be sent.
func (e *Engine) evaluateDownLocked(c model.Check, n model.Node, st *model.CheckState, s model.Settings, now time.Time, allowProbe bool) downDecision {
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
		return downDecision{reason: "cooldown"}
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
	if cooldown > 0 && st.LastAlertAt != nil && now.Sub(*st.LastAlertAt) < time.Duration(cooldown)*time.Minute {
		return false
	}
	st.LastAlertAt = ptrTime(now)
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
			if !pc.Enabled {
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
	e.mu.Lock()
	var ids []int64
	if n.DependsOnNode != nil {
		if p, ok := e.nodes[*n.DependsOnNode]; ok && p.Enabled {
			for _, pc := range p.Checks {
				if pc.Enabled && !e.running[pc.ID] {
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
	if strings.TrimSpace(w.Group) != "" {
		return strings.EqualFold(strings.TrimSpace(w.Group), strings.TrimSpace(n.Group))
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
	if st.ConsecutiveFailures > 0 {
		details["Consecutive failures"] = fmt.Sprintf("%d", st.ConsecutiveFailures)
	}
	if n.Group != "" {
		details["Group"] = n.Group
	}
	msg := r.Message
	if r.Error != "" && msg == "" {
		msg = r.Error
	}
	return mailTask{kind: kind, check: c, node: n, status: st.Status, message: msg, details: details, to: to}
}

func (e *Engine) sendMail(m mailTask) {
	e.mu.Lock()
	s := e.settings
	e.mu.Unlock()
	if len(m.to) == 0 {
		e.recordEvent(model.Event{Type: model.EventAlertFailed, NodeID: ptrInt64(m.node.ID), CheckID: ptrInt64(m.check.ID), NodeName: m.node.Name, CheckName: m.check.Name,
			Title: "Alert not sent: no recipients configured", Detail: "Add recipient addresses under Settings › Alerts."})
		return
	}
	msg := mailer.BuildAlertEmail(m.kind, s.General.InstanceName, m.node.Name, m.check.Name, m.status, m.message, m.details, time.Now())
	msg.To = m.to
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		err := e.opts.Send(ctx, s.Alerts.SMTP, msg)
		e.mu.Lock()
		e.lastAlertAt = ptrTime(time.Now())
		if err != nil {
			e.lastAlertErr = err.Error()
		} else {
			e.lastAlertErr = ""
		}
		e.mu.Unlock()
		label := map[string]string{"down": "Down alert", "recovered": "Recovery notification", "warning": "Warning", "warning_cleared": "Warning cleared"}[m.kind]
		if label == "" {
			label = "Alert"
		}
		if err != nil {
			e.log.Errorf("send %s email for %s/%s: %v", m.kind, m.node.Name, m.check.Name, err)
			e.recordEvent(model.Event{Type: model.EventAlertFailed, NodeID: ptrInt64(m.node.ID), CheckID: ptrInt64(m.check.ID), NodeName: m.node.Name, CheckName: m.check.Name,
				Title: fmt.Sprintf("%s email failed for %s — %s", label, m.node.Name, m.check.Name), Detail: err.Error()})
			return
		}
		e.recordEvent(model.Event{Type: model.EventAlertSent, NodeID: ptrInt64(m.node.ID), CheckID: ptrInt64(m.check.ID), NodeName: m.node.Name, CheckName: m.check.Name,
			Title: fmt.Sprintf("%s emailed for %s — %s", label, m.node.Name, m.check.Name), Detail: "Sent to " + strings.Join(m.to, ", ")})
	}()
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
	c := e.checks[checkID]
	n := e.nodes[c.NodeID]
	var evt model.Event
	if d <= 0 {
		st.SilencedUntil = nil
		evt = model.Event{Type: model.EventUnsilenced, Title: fmt.Sprintf("%s — %s unsilenced", n.Name, c.Name), Detail: "Alerts are active again."}
	} else {
		until := time.Now().Add(d)
		st.SilencedUntil = &until
		evt = model.Event{Type: model.EventSilenced, Title: fmt.Sprintf("%s — %s silenced for %s", n.Name, c.Name, humanDuration(d)), Detail: "No email alerts until " + until.Format("2006-01-02 15:04") + ". Results are still recorded."}
	}
	snapshot := *st
	e.mu.Unlock()
	evt.NodeID, evt.CheckID, evt.NodeName, evt.CheckName = ptrInt64(n.ID), ptrInt64(c.ID), n.Name, c.Name
	if err := e.store.SaveState(ctx, snapshot); err != nil {
		return snapshot, err
	}
	e.recordEvent(evt)
	e.broadcast(Update{Kind: "state", CheckID: c.ID, NodeID: n.ID})
	return snapshot, nil
}

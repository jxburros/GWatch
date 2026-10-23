package engine

// Alert grouping and the warning digest (issue #99).
//
// Both sit between process, which decides that an alert is due, and
// sendMail, which delivers it. Neither changes what is decided: by the time an
// alert reaches this file its check's state (AlertActive, LastAlertAt,
// LastWarnAt and so the cooldown) is already committed exactly as it would be
// without grouping, and a grouped e-mail that fails re-arms each of its down
// checks through failedMail just as a single one would. What changes is only
// how many e-mails carry those alerts and when they leave.
//
// Grouping. With Settings › Alerts "group alerts for the same node within N
// seconds" set, the first alert about a node opens a window of N seconds.
// Alerts of the same kind (down, recovered, warning) about that node's other
// checks that arrive before it closes join it, and when it closes one e-mail
// lists them all and each non-incident "also notify" channel gets one
// message. A window that closes with a single alert in it sends that alert
// the ordinary way, so a grouped install looks no different on a quiet day.
// Alerts that go to different recipients (a check with its own recipient
// list) are grouped apart, since one e-mail cannot go to both lists.
//
// PagerDuty and Opsgenie channels are the exception: they key their incidents
// by check, so that a recovery resolves exactly the incident its outage
// opened, and a single grouped page could not be resolved check by check.
// They still get one event per check, sent when the group is.
//
// A grouped alert waits in its window without registering in mailDone, so a
// check's next result is not held up by the window (process waits only for
// e-mails actually on their way). The one ordering this opens up is a check
// that recovers while its down alert is still waiting: dispatchAlerts then
// takes the down alert out of its group and drops the recovery with it, so
// nobody is told about an outage that was over before they heard of it, and
// LastAlertAt is cleared so the cooldown does not hold back the next real
// outage on account of an alert that was never sent.
//
// The warning digest. With "send warnings as a digest" set to hourly or daily,
// a warning's e-mail is not sent but added to a list kept in the database (so
// a restart loses nothing) and sent as one summary on the hour, or daily at
// digestDailyHour. Down and recovery alerts are never digested. The channels
// still get each warning as it happens: they have their own severity filter
// (a channel set to "down" ignores warnings) for anyone who wants them
// quieter, and a phone push an hour late is not a notification.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/mailer"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
)

// groupWindowUnit is how long one second of the grouping window lasts. It is
// a second; tests shorten it so a window closes in milliseconds.
var groupWindowUnit = time.Second

// groupKey is what alerts must share to be grouped: the node, the kind, the
// recipient list, and whether the e-mail part went to the digest.
type groupKey struct {
	node      int64
	kind      string
	to        string
	skipEmail bool
}

// alertGroup is a window that is open, and the alerts that have joined it.
type alertGroup struct {
	key   groupKey
	tasks []mailTask
	timer *time.Timer
}

func groupableKind(kind string) bool {
	switch kind {
	case "down", "recovered", "warning", "warning_cleared":
		return true
	}
	return false
}

// dispatchAlerts hands the alerts one processed result produced to the
// digest, a group or sendMail. prev and st are the check's status before the
// result and its committed state after it.
func (e *Engine) dispatchAlerts(c model.Check, prev model.Status, st model.CheckState, mails []mailTask) {
	if prev == model.StatusDown && st.Status != model.StatusDown {
		if e.dropPendingDown(c.ID) {
			kept := mails[:0:0]
			for _, m := range mails {
				if m.kind != "recovered" || m.check.ID != c.ID {
					kept = append(kept, m)
				}
			}
			mails = kept
		} else {
			// The down alert may have left its group between this result's
			// start (where process waits for e-mails on their way) and now.
			// The recovery must not overtake it: an incident channel that
			// got the resolve before the trigger would keep a page open.
			e.mu.Lock()
			pending := e.mailDone[c.ID]
			e.mu.Unlock()
			if pending != nil {
				select {
				case <-pending:
				case <-e.workContext().Done():
				}
			}
		}
	}
	for _, m := range mails {
		e.queueAlert(m)
	}
}

// queueAlert sends one alert, by way of the digest and the grouping window
// when those are switched on.
func (e *Engine) queueAlert(m mailTask) {
	e.mu.Lock()
	s := e.settings
	e.mu.Unlock()
	if m.kind == "warning" && !m.channelsOnly && len(m.to) > 0 && digestOn(s.Alerts.WarningDigest) {
		if e.addToDigest(m, s.Alerts.WarningDigest) {
			m.skipEmail = true
			if len(alertChannelsFor(s.Alerts.Channels, m)) == 0 {
				return
			}
		}
	}
	window := s.Alerts.GroupWindowSeconds
	if window <= 0 || m.channelsOnly || !groupableKind(m.kind) {
		e.sendMail(m)
		return
	}
	e.mu.Lock()
	if e.stopping {
		// Stop has flushed the groups, or is about to; a window opened now
		// would outlast the shutdown.
		e.mu.Unlock()
		e.sendMail(m)
		return
	}
	key := groupKey{node: m.node.ID, kind: m.kind, to: strings.Join(m.to, "\x00"), skipEmail: m.skipEmail}
	if g := e.alertGroups[key]; g != nil {
		g.tasks = append(g.tasks, m)
		e.mu.Unlock()
		return
	}
	if e.alertGroups == nil {
		e.alertGroups = map[groupKey]*alertGroup{}
	}
	g := &alertGroup{key: key, tasks: []mailTask{m}}
	e.alertGroups[key] = g
	// An open window is in-flight work: Stop waits for it (having flushed
	// it first), and whoever takes the group out of the map calls Done.
	e.wg.Add(1)
	g.timer = time.AfterFunc(time.Duration(window)*groupWindowUnit, func() { e.closeGroup(g) })
	e.mu.Unlock()
}

// closeGroup is the window's timer: it sends the group, unless Stop or a
// recovery got to it first.
func (e *Engine) closeGroup(g *alertGroup) {
	e.mu.Lock()
	if e.alertGroups[g.key] != g {
		e.mu.Unlock()
		return
	}
	delete(e.alertGroups, g.key)
	tasks := g.tasks
	e.mu.Unlock()
	e.sendBatch(tasks)
	e.wg.Done()
}

// flushAlertGroups sends every group whose window is still open, at once.
// Stop calls it so a shutdown neither waits out a window nor loses its alerts.
func (e *Engine) flushAlertGroups() {
	e.mu.Lock()
	groups := make([]*alertGroup, 0, len(e.alertGroups))
	for _, g := range e.alertGroups {
		g.timer.Stop()
		groups = append(groups, g)
	}
	e.alertGroups = nil
	e.mu.Unlock()
	for _, g := range groups {
		e.sendBatch(g.tasks)
		e.wg.Done()
	}
}

// dropPendingDown takes a check's down alert out of the group it is waiting
// in, if it is waiting in one, and reports whether it did. See the comment
// at the top of the file.
func (e *Engine) dropPendingDown(checkID int64) bool {
	e.mu.Lock()
	var dropped *mailTask
	emptied := false
	for key, g := range e.alertGroups {
		if key.kind != "down" {
			continue
		}
		for i, t := range g.tasks {
			if t.check.ID == checkID {
				dropped = &t
				g.tasks = append(g.tasks[:i:i], g.tasks[i+1:]...)
				break
			}
		}
		if dropped != nil {
			if len(g.tasks) == 0 {
				g.timer.Stop()
				delete(e.alertGroups, key)
				emptied = true
			}
			break
		}
	}
	if dropped == nil {
		e.mu.Unlock()
		return false
	}
	if st := e.states[checkID]; st != nil && st.LastAlertAt != nil && st.LastAlertAt.Equal(dropped.result.Timestamp) {
		st.LastAlertAt = nil
		if err := e.store.SaveState(context.Background(), *st); err != nil {
			e.log.Errorf("save state after dropping an unsent alert: %v", err)
		}
	}
	e.mu.Unlock()
	if emptied {
		e.wg.Done()
	}
	e.recordEvent(model.Event{Type: model.EventAlertSuppressed, NodeID: ptrInt64(dropped.node.ID), CheckID: ptrInt64(checkID), NodeName: dropped.node.Name, CheckName: dropped.check.Name,
		Title: "Alert not sent (recovered within the grouping window)", Detail: "The check recovered while its down alert was waiting to be grouped with others, so neither the down alert nor the recovery was sent.",
		Meta: []byte(`{"reason":"grouping"}`)})
	return true
}

// sendBatch sends what a closed window held: a lone alert the ordinary way,
// several as one grouped alert.
func (e *Engine) sendBatch(tasks []mailTask) {
	switch len(tasks) {
	case 0:
	case 1:
		e.sendMail(tasks[0])
	default:
		e.sendGroup(tasks)
	}
}

// sendGroup delivers a grouped alert: one e-mail and one message per
// non-incident channel for all of tasks, which share a node, a kind and a
// recipient list, and one event per check to the incident channels. A check
// counts as alerted when the e-mail, a group channel or one of its own
// incident events got through; a down check for which nothing did is
// re-armed, exactly as sendMail does for a single alert.
func (e *Engine) sendGroup(tasks []mailTask) {
	e.mu.Lock()
	s := e.settings
	e.mu.Unlock()
	first := tasks[0]
	kind := first.kind
	node := tasks[len(tasks)-1].node // the newest copy of the node's settings
	var groupChannels, incidentChannels []model.AlertChannel
	for _, ch := range alertChannelsFor(s.Alerts.Channels, first) {
		if ch.Action.Type.IsIncident() {
			incidentChannels = append(incidentChannels, ch)
		} else {
			groupChannels = append(groupChannels, ch)
		}
	}
	email := !first.skipEmail && len(first.to) > 0
	if !email && len(groupChannels) == 0 && len(incidentChannels) == 0 {
		if first.skipEmail {
			return
		}
		for _, t := range tasks {
			e.failedMail(t)
			e.recordEvent(model.Event{Type: model.EventAlertFailed, NodeID: ptrInt64(t.node.ID), CheckID: ptrInt64(t.check.ID), NodeName: t.node.Name, CheckName: t.check.Name,
				Title: "Alert not sent: no recipients configured", Detail: "Add recipient addresses or a notification channel under Settings › Alerts."})
		}
		return
	}
	items := make([]mailer.AlertItem, 0, len(tasks))
	for _, t := range tasks {
		items = append(items, mailer.AlertItem{NodeName: t.node.Name, CheckName: t.check.Name, Status: t.status, Message: t.message, Details: t.details, When: t.result.Timestamp})
	}
	var msg mailer.Message
	if email {
		msg = mailer.BuildGroupedAlertEmail(kind, s.General.InstanceName, node.Name, items, first.result.Timestamp)
		msg.To = first.to
	}
	summary := first
	summary.node = node
	summary.message = mailer.GroupSummary(kind, items)
	summary.group = tasks

	done := make(chan struct{})
	e.mu.Lock()
	for _, t := range tasks {
		e.mailDone[t.check.ID] = done
	}
	e.mu.Unlock()
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		defer e.releaseMailDone(done, tasks...)
		label := alertLabel(kind)
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
				e.log.Errorf("send grouped %s email for %s (%d checks): %v", kind, node.Name, len(tasks), err)
			}
			delivered = err == nil
			for _, t := range tasks {
				ev := model.Event{NodeID: ptrInt64(t.node.ID), CheckID: ptrInt64(t.check.ID), NodeName: t.node.Name, CheckName: t.check.Name}
				if err != nil {
					ev.Type, ev.Title, ev.Detail = model.EventAlertFailed, label+" email failed", err.Error()
				} else {
					ev.Type, ev.Title = model.EventAlertSent, label+" emailed"
					ev.Detail = fmt.Sprintf("Sent to %s, in one e-mail about %d checks on %s", strings.Join(first.to, ", "), len(tasks), node.Name)
				}
				e.recordEvent(ev)
			}
		}
		for _, ch := range groupChannels {
			if e.deliverChannel(ch, summary, s.General.InstanceName, label) {
				delivered = true
			}
		}
		for _, t := range tasks {
			ok := delivered
			for _, ch := range incidentChannels {
				if e.deliverChannel(ch, t, s.General.InstanceName, label) {
					ok = true
				}
			}
			if !ok {
				e.failedMail(t)
			}
		}
	}()
}

// releaseMailDone closes an alert's done channel and takes it out of
// mailDone for each of its checks, unless a newer alert about the check has
// replaced it there.
func (e *Engine) releaseMailDone(done chan struct{}, tasks ...mailTask) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, t := range tasks {
		if e.mailDone[t.check.ID] == done {
			delete(e.mailDone, t.check.ID)
		}
	}
	close(done)
}

// ---- warning digest ----

// digestKey is where the collected warnings are kept between digests.
const digestKey = "alertDigest"

// maxDigestEntries bounds the digest. A flapping check under a daily digest
// with no cooldown could otherwise collect thousands of warnings into an
// e-mail nobody reads; the oldest are left out (and counted) instead.
const maxDigestEntries = 500

// digestDailyHour is the local hour a daily digest is sent at: early enough
// to be waiting at the start of the day.
const digestDailyHour = 8

// digestRetryAfter is how long a digest that could not be sent waits before
// it is tried again (deliverMail has already retried it once).
var digestRetryAfter = 15 * time.Minute

type digestEntry struct {
	Seq       int64             `json:"seq"`
	NodeID    int64             `json:"nodeId"`
	CheckID   int64             `json:"checkId"`
	NodeName  string            `json:"nodeName"`
	CheckName string            `json:"checkName"`
	Status    model.Status      `json:"status"`
	Message   string            `json:"message"`
	Details   map[string]string `json:"details,omitempty"`
	At        time.Time         `json:"at"`
	To        []string          `json:"to"`
}

type digestState struct {
	Next    int64          `json:"next"`
	Entries []digestEntry  `json:"entries"`
	Dropped map[string]int `json:"dropped,omitempty"` // by recipient list
	Attempt time.Time      `json:"attempt,omitempty"`
}

func digestOn(mode string) bool {
	return mode == model.WarningDigestHourly || mode == model.WarningDigestDaily
}

func recipientKey(to []string) string { return strings.Join(to, "\x00") }

// addToDigest files a warning's e-mail for the next digest and reports
// whether it did. When the database cannot take it the warning is e-mailed
// at once instead, so a warning is never lost to the digest.
func (e *Engine) addToDigest(m mailTask, mode string) bool {
	ctx := context.Background()
	e.digestMu.Lock()
	defer e.digestMu.Unlock()
	var d digestState
	if err := e.store.GetSetting(ctx, digestKey, &d); err != nil && !errors.Is(err, store.ErrNotFound) {
		e.log.Errorf("read warning digest: %v", err)
		return false
	}
	d.Next++
	d.Entries = append(d.Entries, digestEntry{Seq: d.Next, NodeID: m.node.ID, CheckID: m.check.ID, NodeName: m.node.Name, CheckName: m.check.Name,
		Status: m.status, Message: m.message, Details: m.details, At: m.result.Timestamp, To: m.to})
	for len(d.Entries) > maxDigestEntries {
		if d.Dropped == nil {
			d.Dropped = map[string]int{}
		}
		d.Dropped[recipientKey(d.Entries[0].To)]++
		d.Entries = d.Entries[1:]
	}
	if err := e.store.PutSetting(ctx, digestKey, d); err != nil {
		e.log.Errorf("save warning digest: %v", err)
		return false
	}
	return true
}

// digestDue reports whether a digest whose oldest warning is from first is
// due at now. A digest that has been switched off with warnings still in it
// is due at once, so they are not kept for ever.
func digestDue(mode string, first, now time.Time) bool {
	f := first.Local()
	var at time.Time
	switch mode {
	case model.WarningDigestHourly:
		at = time.Date(f.Year(), f.Month(), f.Day(), f.Hour()+1, 0, 0, 0, f.Location())
	case model.WarningDigestDaily:
		at = time.Date(f.Year(), f.Month(), f.Day(), digestDailyHour, 0, 0, 0, f.Location())
		if !at.After(f) {
			at = at.AddDate(0, 0, 1)
		}
	default:
		return true
	}
	return !now.Before(at)
}

// runDigest sends the warning digest when it is due: one e-mail per
// recipient list, holding every warning collected for it. A list whose
// e-mail fails keeps its warnings for the next attempt, digestRetryAfter
// later. The reports loop calls it every minute.
func (e *Engine) runDigest(ctx context.Context, now time.Time) error {
	s := e.Settings()
	e.digestMu.Lock()
	var d digestState
	if err := e.store.GetSetting(ctx, digestKey, &d); errors.Is(err, store.ErrNotFound) {
		e.digestMu.Unlock()
		return nil
	} else if err != nil {
		e.digestMu.Unlock()
		return err
	}
	if len(d.Entries) == 0 || !digestDue(s.Alerts.WarningDigest, d.Entries[0].At, now) || (!d.Attempt.IsZero() && now.Sub(d.Attempt) < digestRetryAfter) {
		e.digestMu.Unlock()
		return nil
	}
	d.Attempt = now
	if err := e.store.PutSetting(ctx, digestKey, d); err != nil {
		e.digestMu.Unlock()
		return err
	}
	batch := d.Entries
	dropped := d.Dropped
	e.digestMu.Unlock()

	var order []string
	byTo := map[string][]digestEntry{}
	for _, en := range batch {
		k := recipientKey(en.To)
		if _, ok := byTo[k]; !ok {
			order = append(order, k)
		}
		byTo[k] = append(byTo[k], en)
	}
	period := s.Alerts.WarningDigest
	label := map[string]string{model.WarningDigestHourly: "hourly", model.WarningDigestDaily: "daily"}[period]
	sent := map[string]bool{}
	for _, k := range order {
		entries := byTo[k]
		items := make([]mailer.AlertItem, 0, len(entries))
		for _, en := range entries {
			items = append(items, mailer.AlertItem{NodeName: en.NodeName, CheckName: en.CheckName, Status: en.Status, Message: en.Message, Details: en.Details, When: en.At})
		}
		msg := mailer.BuildWarningDigestEmail(s.General.InstanceName, period, items, dropped[k], entries[0].At, now)
		msg.To = entries[0].To
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
			e.log.Errorf("send warning digest to %s: %v", strings.Join(msg.To, ", "), err)
			e.recordEvent(model.Event{Type: model.EventAlertFailed, Title: "Warning digest email failed", Detail: fmt.Sprintf("%d warnings for %s are kept and tried again in %s: %v", len(entries), strings.Join(msg.To, ", "), digestRetryAfter, err)})
			continue
		}
		sent[k] = true
		title := "Warning emailed in the digest"
		if label != "" {
			title = "Warning emailed in the " + label + " digest"
		}
		for _, en := range entries {
			e.recordEvent(model.Event{Type: model.EventAlertSent, NodeID: ptrInt64(en.NodeID), CheckID: ptrInt64(en.CheckID), NodeName: en.NodeName, CheckName: en.CheckName,
				Title: title, Detail: "Sent to " + strings.Join(en.To, ", ")})
		}
	}

	// Take out what went, keeping whatever arrived while it was being sent.
	last := batch[len(batch)-1].Seq
	e.digestMu.Lock()
	defer e.digestMu.Unlock()
	var cur digestState
	if err := e.store.GetSetting(ctx, digestKey, &cur); err != nil {
		return err
	}
	kept := cur.Entries[:0:0]
	for _, en := range cur.Entries {
		if en.Seq <= last && sent[recipientKey(en.To)] {
			continue
		}
		kept = append(kept, en)
	}
	cur.Entries = kept
	for k := range sent {
		delete(cur.Dropped, k)
	}
	if len(sent) == len(order) {
		cur.Attempt = time.Time{}
	}
	return e.store.PutSetting(ctx, digestKey, cur)
}

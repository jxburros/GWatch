package engine

import (
	"context"
	"errors"
	"fmt"
	"github.com/jxburros/GWatch/internal/mailer"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/reports"
	"github.com/jxburros/GWatch/internal/store"
	"time"
)

func (e *Engine) reportLoop() {
	defer e.wg.Done()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-e.ctx.Done():
			return
		case now := <-ticker.C:
			if err := e.runReports(e.ctx, now); err != nil {
				e.RecordError("scheduled reports", err)
			}
			// The warning digest (alertbatch.go) is due on the hour, or
			// once a day, so the reports' minute tick serves it too.
			// Like the reports, it waits out a restore (pause.go) and
			// catches up on the next tick.
			if e.enterWork() {
				if err := e.runDigest(e.ctx, now); err != nil {
					e.RecordError("warning digest", err)
				}
				e.leaveWork()
			}
		}
	}
}
func (e *Engine) runReports(ctx context.Context, now time.Time) error {
	// A report due during a restore (pause.go) is sent on the first pass
	// after it: delivery catches up on any period not yet sent.
	if !e.enterWork() {
		return nil
	}
	defer e.leaveWork()
	var defs []model.ReportDefinition
	if err := e.store.GetSetting(ctx, "reports", &defs); errors.Is(err, store.ErrNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	var failures []error
	for _, d := range defs {
		if !d.Enabled {
			continue
		}
		if err := e.deliverReport(ctx, d, now); err != nil {
			failures = append(failures, fmt.Errorf("report %s: %w", d.ID, err))
		}
	}
	return errors.Join(failures...)
}

func (e *Engine) deliverReport(ctx context.Context, d model.ReportDefinition, now time.Time) error {
	if err := reports.Validate([]model.ReportDefinition{d}); err != nil {
		return err
	}
	from, to := reports.Period(now, d.Period)
	if !d.CreatedAt.Before(to) {
		return nil
	}
	var state struct {
		Sent    time.Time
		Attempt time.Time
	}
	key := "reportDelivery:" + d.ID
	if err := e.store.GetSetting(ctx, key, &state); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if !state.Sent.Before(to) || (!state.Attempt.IsZero() && now.Sub(state.Attempt) < time.Hour) {
		return nil
	}
	state.Attempt = now
	if err := e.store.PutSetting(ctx, key, state); err != nil {
		return err
	}
	html, err := reports.Generate(ctx, e.store, d, from, to)
	if err != nil {
		return err
	}
	// The period ends at midnight on to, so its last day is the one before.
	msg := mailer.Message{To: d.Recipients, Subject: d.Name + " — " + from.Format("2006-01-02") + " to " + to.AddDate(0, 0, -1).Format("2006-01-02"), TextBody: "Your GWatch availability report is attached as printable HTML.", HTMLBody: html, HTMLAttachment: html}
	sendCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err := e.opts.Send(sendCtx, e.Settings().Alerts.SMTP, msg); err != nil {
		return err
	}
	state.Sent = to
	return e.store.PutSetting(ctx, key, state)
}

package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/mailer"
	"github.com/jxburros/GWatch/internal/model"
)

func TestScheduledReportCadence(t *testing.T) {
	for _, period := range []string{"weekly", "monthly"} {
		t.Run(period, func(t *testing.T) {
			e, st, _, mb := setup(t)
			ctx := context.Background()
			now := time.Date(2026, 10, 5, 10, 0, 0, 0, time.Local)
			defs := []model.ReportDefinition{{ID: "report", Name: "Report", Period: period, Enabled: true, Recipients: []string{"reader@example.com"}, CreatedAt: now.AddDate(0, -2, 0)}}
			if err := st.PutSetting(ctx, "reports", defs); err != nil {
				t.Fatal(err)
			}
			for _, at := range []time.Time{now, now.Add(time.Minute), now.Add(2 * time.Hour)} {
				if err := e.runReports(ctx, at); err != nil {
					t.Fatal(err)
				}
			}
			if mb.count() != 1 {
				t.Fatalf("sent %d reports within same period", mb.count())
			}
			next := now.AddDate(0, 0, 7)
			if period == "monthly" {
				next = now.AddDate(0, 1, 0)
			}
			if err := e.runReports(ctx, next); err != nil {
				t.Fatal(err)
			}
			if mb.count() != 2 {
				t.Fatalf("next period sent %d", mb.count())
			}
			mb.mu.Lock()
			msg := mb.sent[0]
			mb.mu.Unlock()
			if msg.HTMLAttachment == "" || msg.HTMLAttachment != msg.HTMLBody || !strings.Contains(msg.HTMLAttachment, "Availability") {
				t.Fatal("report attachment missing")
			}
		})
	}
}

func TestReportRetriesAndIsolatesDeliveryErrors(t *testing.T) {
	e, st, _, mb := setup(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 5, 10, 0, 0, 0, time.Local)
	defs := []model.ReportDefinition{
		{ID: "bad", Name: "Bad", Period: "weekly", Enabled: true, Recipients: []string{"bad@example.com"}, CreatedAt: now.AddDate(0, -2, 0)},
		{ID: "good", Name: "Good", Period: "weekly", Enabled: true, Recipients: []string{"good@example.com"}, CreatedAt: now.AddDate(0, -2, 0)},
		{ID: "off", Name: "Disabled", Period: "weekly", Enabled: false, Recipients: []string{"off@example.com"}, CreatedAt: now.AddDate(0, -2, 0)},
		{ID: "new", Name: "New", Period: "weekly", Enabled: true, Recipients: []string{"new@example.com"}, CreatedAt: now},
	}
	if err := st.PutSetting(ctx, "reports", defs); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	fail := true
	e.opts.Send = func(ctx context.Context, s model.SMTPSettings, m mailer.Message) error {
		if m.To[0] == "bad@example.com" {
			attempts++
			if fail {
				return errors.New("SMTP unavailable")
			}
		}
		return mb.send(ctx, s, m)
	}
	if err := e.runReports(ctx, now); err == nil {
		t.Fatal("expected delivery failure")
	}
	if mb.count() != 1 || attempts != 1 {
		t.Fatal("bad report starved later schedule")
	}
	if err := e.runReports(ctx, now.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || mb.count() != 1 {
		t.Fatal("retried too early or sent disabled/new report")
	}
	fail = false
	if err := e.runReports(ctx, now.Add(61*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || mb.count() != 2 {
		t.Fatal("failed delivery did not retry")
	}
	if err := e.runReports(ctx, now.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if mb.count() != 2 {
		t.Fatal("successful retry sent twice")
	}
}

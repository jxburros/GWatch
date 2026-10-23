package mailer

import (
	"fmt"
	"html"
	"sort"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// Alert kinds accepted by BuildAlertEmail.
const (
	KindDown           = "down"
	KindRecovered      = "recovered"
	KindWarning        = "warning"
	KindWarningCleared = "warning_cleared"
	KindTest           = "test"
)

type kindStyle struct {
	label    string // subject label
	headline string
	color    string // accent colour
	bg       string // accent background
}

func styleFor(kind string, status model.Status) kindStyle {
	switch kind {
	case KindDown:
		return kindStyle{"DOWN", "Check is down", "#ff5c5c", "#3a1a1a"}
	case KindRecovered:
		return kindStyle{"RECOVERED", "Check recovered", "#3ddc84", "#15301f"}
	case KindWarning:
		return kindStyle{"WARNING", "Check needs attention", "#ffb020", "#3a2c12"}
	case KindWarningCleared:
		return kindStyle{"WARNING CLEARED", "Warning cleared", "#3ddc84", "#15301f"}
	case KindTest:
		return kindStyle{"Test email", "Email delivery works", "#4c9aff", "#142338"}
	}
	switch status {
	case model.StatusDown:
		return kindStyle{"DOWN", "Check is down", "#ff5c5c", "#3a1a1a"}
	case model.StatusDegraded:
		return kindStyle{"WARNING", "Check needs attention", "#ffb020", "#3a2c12"}
	}
	return kindStyle{strings.ToUpper(kind), "Status update", "#4c9aff", "#142338"}
}

// BuildAlertEmail produces the plain text and HTML bodies for an alert.
// kind is one of down, recovered, warning, warning_cleared or test.
func BuildAlertEmail(kind string, instance string, nodeName, checkName string, status model.Status, message string, details map[string]string, when time.Time) Message {
	instance = strings.TrimSpace(instance)
	if instance == "" {
		instance = "GWatch"
	}
	st := styleFor(kind, status)
	if when.IsZero() {
		when = time.Now()
	}
	whenText := when.Local().Format("Mon 2 Jan 2006 15:04:05 MST")

	subjectTarget := strings.TrimSpace(nodeName)
	if strings.TrimSpace(checkName) != "" {
		if subjectTarget != "" {
			subjectTarget += " — " + strings.TrimSpace(checkName)
		} else {
			subjectTarget = strings.TrimSpace(checkName)
		}
	}
	var subject string
	if kind == KindTest {
		subject = fmt.Sprintf("[%s] Test email", instance)
	} else if subjectTarget != "" {
		subject = fmt.Sprintf("[%s] %s: %s", instance, st.label, subjectTarget)
	} else {
		subject = fmt.Sprintf("[%s] %s", instance, st.label)
	}

	if message == "" && kind == KindTest {
		message = "This is a test email from " + instance + ". If you can read this, alert emails are set up correctly."
	}

	keys := make([]string, 0, len(details))
	for k := range details {
		if strings.TrimSpace(details[k]) != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	// Plain text.
	var t strings.Builder
	fmt.Fprintf(&t, "%s: %s\n\n", st.label, st.headline)
	if nodeName != "" {
		fmt.Fprintf(&t, "Node:    %s\n", nodeName)
	}
	if checkName != "" {
		fmt.Fprintf(&t, "Check:   %s\n", checkName)
	}
	if status != "" {
		fmt.Fprintf(&t, "Status:  %s\n", strings.ToUpper(string(status)))
	}
	fmt.Fprintf(&t, "When:    %s\n", whenText)
	if message != "" {
		fmt.Fprintf(&t, "\n%s\n", message)
	}
	if len(keys) > 0 {
		t.WriteString("\nDetails\n")
		for _, k := range keys {
			fmt.Fprintf(&t, "  %s: %s\n", k, details[k])
		}
	}
	fmt.Fprintf(&t, "\n-- \nSent by %s, your local network monitor.\n", instance)

	// HTML (table based, inline styles, dark but readable in every client).
	var h strings.Builder
	h.WriteString(`<!DOCTYPE html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>`)
	h.WriteString(html.EscapeString(subject))
	h.WriteString(`</title></head><body style="margin:0;padding:0;background-color:#0f1419;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;color:#e6edf3;">`)
	h.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="background-color:#0f1419;"><tr><td align="center" style="padding:24px 12px;">`)
	h.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="max-width:560px;background-color:#161b22;border:1px solid #30363d;border-radius:8px;">`)
	// Accent band.
	fmt.Fprintf(&h, `<tr><td style="padding:16px 24px;background-color:%s;border-left:4px solid %s;border-radius:8px 8px 0 0;">`, st.bg, st.color)
	fmt.Fprintf(&h, `<div style="font-size:12px;letter-spacing:1px;text-transform:uppercase;color:%s;font-weight:bold;">%s</div>`, st.color, html.EscapeString(st.label))
	fmt.Fprintf(&h, `<div style="font-size:20px;font-weight:bold;color:#ffffff;margin-top:4px;">%s</div>`, html.EscapeString(st.headline))
	h.WriteString(`</td></tr>`)
	// Summary rows.
	h.WriteString(`<tr><td style="padding:20px 24px 8px 24px;"><table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="font-size:14px;">`)
	row := func(label, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		fmt.Fprintf(&h, `<tr><td style="padding:4px 12px 4px 0;color:#8b949e;white-space:nowrap;vertical-align:top;">%s</td><td style="padding:4px 0;color:#e6edf3;">%s</td></tr>`, html.EscapeString(label), html.EscapeString(value))
	}
	row("Node", nodeName)
	row("Check", checkName)
	if status != "" {
		row("Status", strings.ToUpper(string(status)))
	}
	row("When", whenText)
	h.WriteString(`</table></td></tr>`)
	if message != "" {
		fmt.Fprintf(&h, `<tr><td style="padding:8px 24px 16px 24px;"><div style="padding:12px 14px;background-color:#0f1419;border:1px solid #30363d;border-radius:6px;font-size:14px;line-height:1.5;color:#e6edf3;">%s</div></td></tr>`, html.EscapeString(message))
	}
	if len(keys) > 0 {
		h.WriteString(`<tr><td style="padding:0 24px 16px 24px;"><div style="font-size:12px;text-transform:uppercase;letter-spacing:1px;color:#8b949e;margin-bottom:6px;">Details</div><table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="font-size:13px;">`)
		for _, k := range keys {
			fmt.Fprintf(&h, `<tr><td style="padding:3px 12px 3px 0;color:#8b949e;white-space:nowrap;vertical-align:top;">%s</td><td style="padding:3px 0;color:#e6edf3;word-break:break-word;">%s</td></tr>`, html.EscapeString(k), html.EscapeString(details[k]))
		}
		h.WriteString(`</table></td></tr>`)
	}
	fmt.Fprintf(&h, `<tr><td style="padding:12px 24px 18px 24px;border-top:1px solid #30363d;font-size:12px;color:#8b949e;">Sent by %s, your local network monitor.</td></tr>`, html.EscapeString(instance))
	h.WriteString(`</table></td></tr></table></body></html>`)

	return Message{Subject: subject, TextBody: t.String(), HTMLBody: h.String()}
}

// AlertItem is one check's part of a grouped alert or of a warning digest:
// what BuildAlertEmail would have said about it in an e-mail of its own.
type AlertItem struct {
	NodeName  string
	CheckName string
	Status    model.Status
	Message   string
	Details   map[string]string
	When      time.Time
}

// groupPhrase is how a grouped e-mail sums its checks up: "3 checks down".
func groupPhrase(kind string, count int) string {
	noun := "checks"
	if count == 1 {
		noun = "check"
	}
	switch kind {
	case KindDown:
		return fmt.Sprintf("%d %s down", count, noun)
	case KindRecovered:
		return fmt.Sprintf("%d %s recovered", count, noun)
	case KindWarning:
		return fmt.Sprintf("%d %s need attention", count, noun)
	case KindWarningCleared:
		return fmt.Sprintf("%d %s back to normal", count, noun)
	}
	return fmt.Sprintf("%d %s", count, noun)
}

// GroupSummary is the one-line summary of a grouped alert, "3 checks down:
// HTTP, JSON API, TCP 443". The engine hands it to the "also notify"
// channels as their {{message}}, so a chat message says what the e-mail's
// subject and list say.
func GroupSummary(kind string, items []AlertItem) string {
	names := make([]string, 0, len(items))
	for _, it := range items {
		names = append(names, it.CheckName)
	}
	return groupPhrase(kind, len(items)) + ": " + strings.Join(names, ", ")
}

// BuildGroupedAlertEmail produces one e-mail about several checks on the same
// node that changed the same way within the grouping window (issue #99). The
// subject names the node and the count ("[GWatch] DOWN: Web server — 3
// checks down") and the body lists every check with its own status, message
// and details, so nothing an e-mail per check would have said is lost.
func BuildGroupedAlertEmail(kind string, instance string, nodeName string, items []AlertItem, when time.Time) Message {
	instance = strings.TrimSpace(instance)
	if instance == "" {
		instance = "GWatch"
	}
	st := styleFor(kind, "")
	if when.IsZero() {
		when = time.Now()
	}
	whenText := when.Local().Format("Mon 2 Jan 2006 15:04:05 MST")
	phrase := groupPhrase(kind, len(items))
	nodeName = strings.TrimSpace(nodeName)
	subject := fmt.Sprintf("[%s] %s: %s", instance, st.label, phrase)
	headline := phrase
	if nodeName != "" {
		subject = fmt.Sprintf("[%s] %s: %s — %s", instance, st.label, nodeName, phrase)
		headline = nodeName + ": " + phrase
	}

	var t strings.Builder
	fmt.Fprintf(&t, "%s: %s\n\n", st.label, headline)
	if nodeName != "" {
		fmt.Fprintf(&t, "Node:    %s\n", nodeName)
	}
	fmt.Fprintf(&t, "When:    %s\n", whenText)
	for _, it := range items {
		writeItemText(&t, it, false)
	}
	fmt.Fprintf(&t, "\n-- \nSent by %s, your local network monitor.\n", instance)

	var h strings.Builder
	openHTML(&h, subject, st, headline)
	summaryRows(&h, [][2]string{{"Node", nodeName}, {"Checks", fmt.Sprint(len(items))}, {"When", whenText}})
	for _, it := range items {
		writeItemHTML(&h, it, st, false)
	}
	closeHTML(&h, instance)
	return Message{Subject: subject, TextBody: t.String(), HTMLBody: h.String()}
}

// BuildWarningDigestEmail produces the warning digest (issue #99): every
// warning collected since the last digest, oldest first, each with its node,
// check, time, message and details. dropped counts the warnings left out
// because the digest reached its size bound, so the reader knows there were
// more and that the timeline has them.
func BuildWarningDigestEmail(instance string, period string, items []AlertItem, dropped int, from, to time.Time) Message {
	instance = strings.TrimSpace(instance)
	if instance == "" {
		instance = "GWatch"
	}
	st := styleFor(KindWarning, "")
	st.label = "WARNING DIGEST"
	count := len(items) + dropped
	noun := "warnings"
	if count == 1 {
		noun = "warning"
	}
	title := "Warning digest"
	switch period {
	case model.WarningDigestHourly:
		title = "Hourly warning digest"
	case model.WarningDigestDaily:
		title = "Daily warning digest"
	}
	headline := fmt.Sprintf("%d %s", count, noun)
	subject := fmt.Sprintf("[%s] %s: %s", instance, title, headline)
	span := fmt.Sprintf("%s – %s", from.Local().Format("Mon 2 Jan 15:04"), to.Local().Format("Mon 2 Jan 15:04 MST"))
	note := ""
	if dropped > 0 {
		note = fmt.Sprintf("%d earlier warnings were left out to keep this e-mail a readable size; the timeline has them all.", dropped)
	}

	var t strings.Builder
	fmt.Fprintf(&t, "%s: %s\n\n", title, headline)
	fmt.Fprintf(&t, "Period:  %s\n", span)
	if note != "" {
		fmt.Fprintf(&t, "\n%s\n", note)
	}
	for _, it := range items {
		writeItemText(&t, it, true)
	}
	fmt.Fprintf(&t, "\n-- \nSent by %s, your local network monitor.\n", instance)

	var h strings.Builder
	openHTML(&h, subject, st, title+": "+headline)
	summaryRows(&h, [][2]string{{"Period", span}, {"Warnings", fmt.Sprint(count)}})
	if note != "" {
		fmt.Fprintf(&h, `<tr><td style="padding:0 24px 12px 24px;font-size:13px;color:#8b949e;">%s</td></tr>`, html.EscapeString(note))
	}
	for _, it := range items {
		writeItemHTML(&h, it, st, true)
	}
	closeHTML(&h, instance)
	return Message{Subject: subject, TextBody: t.String(), HTMLBody: h.String()}
}

func sortedDetailKeys(details map[string]string) []string {
	keys := make([]string, 0, len(details))
	for k := range details {
		if strings.TrimSpace(details[k]) != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// itemTitle names one item of a group or a digest: the check alone in a
// group, whose node the whole e-mail is about, and "node — check" in a
// digest, which spans every node.
func itemTitle(it AlertItem, withNode bool) string {
	if withNode && strings.TrimSpace(it.NodeName) != "" {
		return strings.TrimSpace(it.NodeName) + " — " + strings.TrimSpace(it.CheckName)
	}
	return strings.TrimSpace(it.CheckName)
}

func writeItemText(t *strings.Builder, it AlertItem, withNode bool) {
	fmt.Fprintf(t, "\n== %s ==\n", itemTitle(it, withNode))
	if it.Status != "" {
		fmt.Fprintf(t, "Status:  %s\n", strings.ToUpper(string(it.Status)))
	}
	if !it.When.IsZero() {
		fmt.Fprintf(t, "When:    %s\n", it.When.Local().Format("Mon 2 Jan 2006 15:04:05 MST"))
	}
	if it.Message != "" {
		fmt.Fprintf(t, "%s\n", it.Message)
	}
	for _, k := range sortedDetailKeys(it.Details) {
		fmt.Fprintf(t, "  %s: %s\n", k, it.Details[k])
	}
}

func writeItemHTML(h *strings.Builder, it AlertItem, st kindStyle, withNode bool) {
	fmt.Fprintf(h, `<tr><td style="padding:8px 24px 8px 24px;"><div style="border:1px solid #30363d;border-left:3px solid %s;border-radius:6px;padding:12px 14px;background-color:#0f1419;">`, st.color)
	fmt.Fprintf(h, `<div style="font-size:15px;font-weight:bold;color:#ffffff;">%s</div>`, html.EscapeString(itemTitle(it, withNode)))
	var meta []string
	if it.Status != "" {
		meta = append(meta, strings.ToUpper(string(it.Status)))
	}
	if !it.When.IsZero() {
		meta = append(meta, it.When.Local().Format("Mon 2 Jan 15:04:05"))
	}
	if len(meta) > 0 {
		fmt.Fprintf(h, `<div style="font-size:12px;color:#8b949e;margin-top:2px;">%s</div>`, html.EscapeString(strings.Join(meta, " · ")))
	}
	if it.Message != "" {
		fmt.Fprintf(h, `<div style="font-size:14px;line-height:1.5;color:#e6edf3;margin-top:8px;">%s</div>`, html.EscapeString(it.Message))
	}
	if keys := sortedDetailKeys(it.Details); len(keys) > 0 {
		h.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="font-size:13px;margin-top:8px;">`)
		for _, k := range keys {
			fmt.Fprintf(h, `<tr><td style="padding:2px 12px 2px 0;color:#8b949e;white-space:nowrap;vertical-align:top;">%s</td><td style="padding:2px 0;color:#e6edf3;word-break:break-word;">%s</td></tr>`, html.EscapeString(k), html.EscapeString(it.Details[k]))
		}
		h.WriteString(`</table>`)
	}
	h.WriteString(`</div></td></tr>`)
}

// openHTML, summaryRows and closeHTML draw the frame BuildAlertEmail draws,
// shared by the grouped alert and the digest so that all three look alike.
func openHTML(h *strings.Builder, subject string, st kindStyle, headline string) {
	h.WriteString(`<!DOCTYPE html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>`)
	h.WriteString(html.EscapeString(subject))
	h.WriteString(`</title></head><body style="margin:0;padding:0;background-color:#0f1419;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;color:#e6edf3;">`)
	h.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="background-color:#0f1419;"><tr><td align="center" style="padding:24px 12px;">`)
	h.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="max-width:560px;background-color:#161b22;border:1px solid #30363d;border-radius:8px;">`)
	fmt.Fprintf(h, `<tr><td style="padding:16px 24px;background-color:%s;border-left:4px solid %s;border-radius:8px 8px 0 0;">`, st.bg, st.color)
	fmt.Fprintf(h, `<div style="font-size:12px;letter-spacing:1px;text-transform:uppercase;color:%s;font-weight:bold;">%s</div>`, st.color, html.EscapeString(st.label))
	fmt.Fprintf(h, `<div style="font-size:20px;font-weight:bold;color:#ffffff;margin-top:4px;">%s</div>`, html.EscapeString(headline))
	h.WriteString(`</td></tr>`)
}

func summaryRows(h *strings.Builder, rows [][2]string) {
	h.WriteString(`<tr><td style="padding:20px 24px 8px 24px;"><table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="font-size:14px;">`)
	for _, r := range rows {
		if strings.TrimSpace(r[1]) == "" {
			continue
		}
		fmt.Fprintf(h, `<tr><td style="padding:4px 12px 4px 0;color:#8b949e;white-space:nowrap;vertical-align:top;">%s</td><td style="padding:4px 0;color:#e6edf3;">%s</td></tr>`, html.EscapeString(r[0]), html.EscapeString(r[1]))
	}
	h.WriteString(`</table></td></tr>`)
}

func closeHTML(h *strings.Builder, instance string) {
	fmt.Fprintf(h, `<tr><td style="padding:12px 24px 18px 24px;border-top:1px solid #30363d;font-size:12px;color:#8b949e;">Sent by %s, your local network monitor.</td></tr>`, html.EscapeString(instance))
	h.WriteString(`</table></td></tr></table></body></html>`)
}

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

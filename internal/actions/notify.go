// Notification channels: Slack and Microsoft Teams incoming webhooks, ntfy
// push topics and Pushover. Each is a thin, purpose-built wrapper over the
// same HTTP client and timeout machinery as the generic "http" action
// (client(), placeholder Expand()), but builds its payload with
// encoding/json (or net/url for form data) instead of string-splicing, so a
// {{message}} containing quotes or newlines can never break the request.
package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/jxburros/GWatch/internal/model"
)

// defaultTitle/defaultMessage are used when the action leaves title/message
// blank; both support {{placeholders}}.
const (
	defaultNotifyTitle   = "GWatch {{instance}}"
	defaultNotifyMessage = "{{node.name}} is {{status}}: {{message}}"
)

var ntfyTopicRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// notifyTitle/notifyMessage expand the action's title/message, falling back
// to the defaults above, and trim the result.
func notifyTitle(a model.Action, vars Vars) string {
	t := a.Title
	if strings.TrimSpace(t) == "" {
		t = defaultNotifyTitle
	}
	return strings.TrimSpace(Expand(t, vars))
}

func notifyMessage(a model.Action, vars Vars) string {
	m := a.Message
	if strings.TrimSpace(m) == "" {
		m = defaultNotifyMessage
	}
	return strings.TrimSpace(Expand(m, vars))
}

// looksLikeHTTPS reports whether s (before placeholder expansion) starts
// with https:// or still contains a placeholder that might expand into one.
func looksLikeHTTPS(s string) bool {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "{{") {
		return true
	}
	return strings.HasPrefix(strings.ToLower(s), "https://")
}

// looksLikeHTTPURL is like looksLikeHTTPS but also allows http://, used for
// the optional ntfy server override.
func looksLikeHTTPURL(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || strings.Contains(s, "{{") {
		return true
	}
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

// ValidateNotify checks the fields of a slack/teams/ntfy/pushover action. It
// is called from Validate for those action types.
func ValidateNotify(a model.Action) error {
	switch a.Type {
	case model.ActionSlack:
		if strings.TrimSpace(a.WebhookURL) == "" {
			return fmt.Errorf("a Slack webhook URL is required")
		}
		if !looksLikeHTTPS(a.WebhookURL) {
			return fmt.Errorf("the Slack webhook URL must start with https://")
		}
	case model.ActionTeams:
		if strings.TrimSpace(a.WebhookURL) == "" {
			return fmt.Errorf("a Teams webhook URL is required")
		}
		if !looksLikeHTTPS(a.WebhookURL) {
			return fmt.Errorf("the Teams webhook URL must start with https://")
		}
	case model.ActionNtfy:
		topic := strings.TrimSpace(a.Topic)
		if topic == "" {
			return fmt.Errorf("an ntfy topic is required")
		}
		if !strings.Contains(topic, "{{") && !ntfyTopicRe.MatchString(topic) {
			return fmt.Errorf("the ntfy topic may only contain letters, digits, - and _ (max 64 characters)")
		}
		if !looksLikeHTTPURL(a.Server) {
			return fmt.Errorf("the ntfy server must start with http:// or https://")
		}
	case model.ActionPushover:
		if strings.TrimSpace(a.Token) == "" {
			return fmt.Errorf("a Pushover application token is required")
		}
		if strings.TrimSpace(a.UserKey) == "" {
			return fmt.Errorf("a Pushover user key is required")
		}
	}
	return nil
}

// postJSON POSTs a JSON-encoded payload and returns the raw response body
// alongside the *http.Response (body already read and response closed).
func (r *Runner) postJSON(ctx context.Context, a model.Action, u string, payload any) (*http.Response, []byte, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(string(b)))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "GWatch-automation")
	resp, err := r.client(a).Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxOutput))
	return resp, data, nil
}

func excerpt(data []byte) string {
	s := strings.TrimSpace(string(data))
	if len(s) > 500 {
		s = s[:500] + "…"
	}
	return s
}

// ---- slack ----

func (r *Runner) runSlack(ctx context.Context, a model.Action, vars Vars, res model.ActionResult) model.ActionResult {
	u := strings.TrimSpace(Expand(a.WebhookURL, vars))
	if !strings.HasPrefix(strings.ToLower(u), "https://") {
		res.Error = "the Slack webhook URL must start with https:// (after expanding placeholders): " + u
		return res
	}
	msg := notifyMessage(a, vars)
	resp, data, err := r.postJSON(ctx, a, u, map[string]string{"text": msg})
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.StatusCode = resp.StatusCode
	res.Output = fmt.Sprintf("slack → %s\n%s", resp.Status, excerpt(data))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		res.Error = fmt.Sprintf("unexpected HTTP status %d from Slack", resp.StatusCode)
		return res
	}
	res.OK = true
	return res
}

// ---- teams ----

type teamsCard struct {
	Type        string        `json:"type"`
	Attachments []teamsAttach `json:"attachments"`
}

type teamsAttach struct {
	ContentType string `json:"contentType"`
	Content     any    `json:"content"`
}

func adaptiveCard(title, message string) map[string]any {
	return map[string]any{
		"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
		"type":    "AdaptiveCard",
		"version": "1.4",
		"body": []map[string]any{
			{"type": "TextBlock", "text": title, "weight": "bolder", "size": "medium", "wrap": true},
			{"type": "TextBlock", "text": message, "wrap": true},
		},
	}
}

func (r *Runner) runTeams(ctx context.Context, a model.Action, vars Vars, res model.ActionResult) model.ActionResult {
	u := strings.TrimSpace(Expand(a.WebhookURL, vars))
	if !strings.HasPrefix(strings.ToLower(u), "https://") {
		res.Error = "the Teams webhook URL must start with https:// (after expanding placeholders): " + u
		return res
	}
	title := notifyTitle(a, vars)
	msg := notifyMessage(a, vars)
	payload := teamsCard{
		Type: "message",
		Attachments: []teamsAttach{{
			ContentType: "application/vnd.microsoft.card.adaptive",
			Content:     adaptiveCard(title, msg),
		}},
	}
	resp, data, err := r.postJSON(ctx, a, u, payload)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.StatusCode = resp.StatusCode
	res.Output = fmt.Sprintf("teams → %s\n%s", resp.Status, excerpt(data))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		res.Error = fmt.Sprintf("unexpected HTTP status %d from Teams", resp.StatusCode)
		return res
	}
	res.OK = true
	return res
}

// ---- ntfy ----

func (r *Runner) runNtfy(ctx context.Context, a model.Action, vars Vars, res model.ActionResult) model.ActionResult {
	server := strings.TrimSpace(Expand(a.Server, vars))
	if server == "" {
		server = "https://ntfy.sh"
	}
	server = strings.TrimRight(server, "/")
	if !strings.HasPrefix(strings.ToLower(server), "http://") && !strings.HasPrefix(strings.ToLower(server), "https://") {
		res.Error = "the ntfy server must start with http:// or https:// (after expanding placeholders): " + server
		return res
	}
	topic := strings.TrimSpace(Expand(a.Topic, vars))
	if topic == "" {
		res.Error = "the ntfy topic is empty (after expanding placeholders)"
		return res
	}
	u := server + "/" + topic
	title := notifyTitle(a, vars)
	msg := notifyMessage(a, vars)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(msg))
	if err != nil {
		res.Error = err.Error()
		return res
	}
	req.Header.Set("User-Agent", "GWatch-automation")
	if title != "" {
		req.Header.Set("Title", headerSafe(title))
	}
	if p := strings.TrimSpace(Expand(a.Priority, vars)); p != "" {
		req.Header.Set("Priority", headerSafe(p))
	}
	if tags := strings.TrimSpace(Expand(a.Tags, vars)); tags != "" {
		req.Header.Set("Tags", headerSafe(tags))
	}
	if tok := strings.TrimSpace(Expand(a.Token, vars)); tok != "" {
		req.Header.Set("Authorization", "Bearer "+headerSafe(tok))
	}
	resp, err := r.client(a).Do(req)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxOutput))
	res.StatusCode = resp.StatusCode
	res.Output = fmt.Sprintf("ntfy → %s\n%s", resp.Status, excerpt(data))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		res.Error = fmt.Sprintf("unexpected HTTP status %d from ntfy", resp.StatusCode)
		return res
	}
	res.OK = true
	return res
}

// ---- pushover ----

type pushoverResponse struct {
	Status int `json:"status"`
}

// pushoverURLForTest overrides Pushover's fixed API endpoint in tests; it is
// never changed outside internal/actions/notify_test.go.
var pushoverURLForTest = "https://api.pushover.net/1/messages.json"

func (r *Runner) runPushover(ctx context.Context, a model.Action, vars Vars, res model.ActionResult) model.ActionResult {
	token := strings.TrimSpace(Expand(a.Token, vars))
	user := strings.TrimSpace(Expand(a.UserKey, vars))
	if token == "" || user == "" {
		res.Error = "a Pushover application token and user key are required (after expanding placeholders)"
		return res
	}
	title := notifyTitle(a, vars)
	msg := notifyMessage(a, vars)
	form := url.Values{}
	form.Set("token", token)
	form.Set("user", user)
	form.Set("title", title)
	form.Set("message", msg)
	if p := strings.TrimSpace(Expand(a.Priority, vars)); p != "" {
		form.Set("priority", p)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, pushoverURLForTest, strings.NewReader(form.Encode()))
	if err != nil {
		res.Error = err.Error()
		return res
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "GWatch-automation")
	resp, err := r.client(a).Do(req)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxOutput))
	res.StatusCode = resp.StatusCode
	res.Output = fmt.Sprintf("pushover → %s\n%s", resp.Status, excerpt(data))
	if resp.StatusCode != http.StatusOK {
		res.Error = fmt.Sprintf("unexpected HTTP status %d from Pushover", resp.StatusCode)
		return res
	}
	var pr pushoverResponse
	if err := json.Unmarshal(data, &pr); err != nil || pr.Status != 1 {
		res.Error = "Pushover did not report success: " + excerpt(data)
		return res
	}
	res.OK = true
	return res
}

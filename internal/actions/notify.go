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
	"errors"
	"fmt"
	"io"
	"mime"
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

// looksLikeHTTPS reports whether s is an https:// URL whose scheme and
// authority are written out in full. Placeholders may appear in the path,
// query or fragment, where expandURL escapes them as data; one in the
// scheme, user info, host or port would let whoever supplies the value
// (a hook caller, a monitored device) choose where the request goes.
func looksLikeHTTPS(s string) bool {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(strings.ToLower(s), "https://") {
		return false
	}
	_, err := expandURL(s, nil)
	return err == nil
}

// looksLikeHTTPURL is like looksLikeHTTPS but also allows http://, and an
// empty value, used for the optional ntfy server override.
func looksLikeHTTPURL(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return true
	}
	l := strings.ToLower(s)
	if !strings.HasPrefix(l, "http://") && !strings.HasPrefix(l, "https://") {
		return false
	}
	_, err := expandURL(s, nil)
	return err == nil
}

// sameAuthorityOnly returns c with redirects limited so a credential set on
// the first request never follows a redirect to another scheme, host or
// port. The http package already drops Authorization for an unrelated host,
// but keeps it for a subdomain and across an https → http downgrade.
func sameAuthorityOnly(c *http.Client) *http.Client {
	cc := *c
	cc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		first := via[0].URL
		if req.URL.Scheme != first.Scheme || req.URL.Host != first.Host {
			req.Header.Del("Authorization")
		}
		return nil
	}
	return &cc
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
			return fmt.Errorf("the Slack webhook URL must start with https:// and name its host without placeholders")
		}
	case model.ActionTeams:
		if strings.TrimSpace(a.WebhookURL) == "" {
			return fmt.Errorf("a Teams webhook URL is required")
		}
		if !looksLikeHTTPS(a.WebhookURL) {
			return fmt.Errorf("the Teams webhook URL must start with https:// and name its host without placeholders")
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
			return fmt.Errorf("the ntfy server must start with http:// or https:// and name its host without placeholders")
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

// webhookURL expands a Slack or Teams webhook URL with the authority fixed
// as written; placeholders only ever fill the path and query.
func webhookURL(template string, vars Vars) (string, error) {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(template)), "https://") {
		return "", errors.New("must start with https://")
	}
	u, err := expandURL(template, vars)
	if err != nil {
		return "", errors.New("is invalid: " + err.Error())
	}
	return u, nil
}

// ---- slack ----

func (r *Runner) runSlack(ctx context.Context, a model.Action, vars Vars, res model.ActionResult) model.ActionResult {
	u, err := webhookURL(a.WebhookURL, vars)
	if err != nil {
		res.Error = "the Slack webhook URL " + err.Error()
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
	u, err := webhookURL(a.WebhookURL, vars)
	if err != nil {
		res.Error = "the Teams webhook URL " + err.Error()
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
	server := strings.TrimRight(strings.TrimSpace(a.Server), "/")
	if server == "" {
		server = "https://ntfy.sh"
	}
	if !looksLikeHTTPURL(server) {
		res.Error = "the ntfy server must start with http:// or https:// and name its host without placeholders"
		return res
	}
	if strings.TrimSpace(Expand(a.Topic, vars)) == "" {
		res.Error = "the ntfy topic is empty (after expanding placeholders)"
		return res
	}
	// The server is used as written (it is where the token goes), and the
	// topic is expanded as one escaped path segment beneath it.
	u, err := expandURL(server+"/"+strings.TrimSpace(a.Topic), vars)
	if err != nil {
		res.Error = "the ntfy server is invalid: " + err.Error()
		return res
	}
	title := notifyTitle(a, vars)
	msg := notifyMessage(a, vars)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(msg))
	if err != nil {
		res.Error = err.Error()
		return res
	}
	req.Header.Set("User-Agent", "GWatch-automation")
	if title != "" {
		req.Header.Set("Title", ntfyHeader(title))
	}
	if p := strings.TrimSpace(Expand(a.Priority, vars)); p != "" {
		req.Header.Set("Priority", headerSafe(p))
	}
	if tags := strings.TrimSpace(Expand(a.Tags, vars)); tags != "" {
		req.Header.Set("Tags", ntfyHeader(tags))
	}
	if tok := strings.TrimSpace(Expand(a.Token, vars)); tok != "" {
		req.Header.Set("Authorization", "Bearer "+headerSafe(tok))
	}
	resp, err := sameAuthorityOnly(r.client(a)).Do(req)
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

// ntfyHeader makes a header value ntfy reads as the text it was. HTTP headers
// are Latin-1 to ntfy, so a title with an accent, an em dash or an emoji in it
// arrived garbled; ntfy decodes RFC 2047 encoded words, so any
// value that is not plain ASCII is sent as one.
func ntfyHeader(v string) string {
	v = headerSafe(v)
	for i := 0; i < len(v); i++ {
		if v[i] >= 0x80 {
			return mime.BEncoding.Encode("utf-8", v)
		}
	}
	return v
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

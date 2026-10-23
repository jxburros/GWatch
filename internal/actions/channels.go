// More first-class notification channels (issue #122): Telegram, Discord,
// Gotify, Matrix, Twilio SMS, PagerDuty and Opsgenie. They follow the pattern
// notify.go set for Slack, Teams, ntfy and Pushover exactly: the payload is
// built with encoding/json (or net/url for Twilio's form posts) rather than by
// splicing text, so a {{message}} that contains quotes, newlines or JSON can
// never change the shape of the request; every field is validated once when
// the action is saved and again after placeholders are expanded at run time;
// and the same Runner.client() with its timeouts and per-action TLS opt-out is
// underneath every request.
//
// Two things are stricter here than in notify.go, because these channels put
// a credential somewhere a redirect or an error message could carry it off.
// First, every request goes through channelClient, which strips the
// credential headers (Authorization and Gotify's X-Gotify-Key) from a
// redirect to a different scheme, host or port, as notify.go's
// sameAuthorityOnly already does for Authorization; Go's own client would
// forward a custom header such as X-Gotify-Key to any host. Servers the user
// types in (Discord webhook, Gotify, Matrix) are pinned the same way the ntfy
// server and the Slack and Teams webhooks are: the scheme and host must be
// written out without placeholders, and placeholders only ever fill the path
// and query as escaped data (expandURL). Second, the Telegram bot token is scrubbed from any error text
// before it is stored as the run's result, because Go's transport errors quote
// the full request URL ("Post \"https://api.telegram.org/bot123:abc/...\"")
// and that result ends up in the timeline and the trigger's last-run summary.
//
// Providers whose API lives at one fixed address (Telegram, Twilio, PagerDuty,
// Opsgenie) are pinned to it: the address is a constant in this file, not a
// field of the action, so an action can never be pointed at another host to
// collect the credential. Tests swap the constant through the *ForTest
// variables below, the way pushoverURLForTest already does.
package actions

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// Fixed provider endpoints. They are variables only so the tests in
// channels_test.go can point them at an httptest server; nothing outside
// tests ever assigns them.
var (
	telegramAPIForTest     = "https://api.telegram.org"
	twilioAPIForTest       = "https://api.twilio.com"
	pagerDutyURLForTest    = "https://events.pagerduty.com/v2/enqueue"
	opsgenieUSAPIForTest   = "https://api.opsgenie.com"
	opsgenieEUAPIForTest   = "https://api.eu.opsgenie.com"
	telegramTokenRe        = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`)
	telegramChatRe         = regexp.MustCompile(`^(-?[0-9]+|@[A-Za-z][A-Za-z0-9_]{3,31})$`)
	matrixRoomRe           = regexp.MustCompile(`^![^:\s]+:[^\s]+$`)
	twilioSIDRe            = regexp.MustCompile(`^AC[0-9A-Za-z]{32}$`)
	e164Re                 = regexp.MustCompile(`^\+[1-9][0-9]{6,14}$`)
	opsgeniePriorityRe     = regexp.MustCompile(`^P[1-5]$`)
	pagerDutySeverities    = map[string]bool{"critical": true, "error": true, "warning": true, "info": true}
	incidentEventActionsOK = map[string]bool{"": true, "auto": true, "trigger": true, "resolve": true}
)

// Events that close an incident rather than open one when an incident
// channel (PagerDuty, Opsgenie) runs in "auto" mode. They are the trigger
// conditions and rule events that mean "the thing is fine again", plus the
// alert-channel kinds the engine passes as {{event}}.
var resolvingEvents = map[string]bool{
	"recovered":       true,
	"warning_cleared": true,
	"rule_cleared":    true,
}

// Events about a warning rather than an outage. An incident channel files
// them under a separate default key, so a cleared latency warning resolves
// the warning's incident and not the outage's.
var warningEvents = map[string]bool{
	"warning": true, "degraded": true, "warning_cleared": true, "cert_warning": true, "content_changed": true,
}

// validateChannel checks the fields of the channel types added for issue
// #122. ValidateNotify calls it for every type it does not handle itself.
// As in notify.go, a field that still contains a {{placeholder}} is accepted
// here and checked again once the placeholder has been expanded.
func validateChannel(a model.Action) error {
	switch a.Type {
	case model.ActionTelegram:
		tok := strings.TrimSpace(a.Token)
		if tok == "" {
			return errors.New("a Telegram bot token is required")
		}
		if !hasPlaceholder(tok) && !telegramTokenRe.MatchString(tok) {
			return errors.New("the Telegram bot token should look like 123456789:AA… (as BotFather gives it)")
		}
		chat := strings.TrimSpace(a.ChatID)
		if chat == "" {
			return errors.New("a Telegram chat ID is required")
		}
		if !hasPlaceholder(chat) && !telegramChatRe.MatchString(chat) {
			return errors.New("the Telegram chat ID must be a number (e.g. -1001234567890) or a public @channelname")
		}
	case model.ActionDiscord:
		if strings.TrimSpace(a.WebhookURL) == "" {
			return errors.New("a Discord webhook URL is required")
		}
		if !looksLikeHTTPS(a.WebhookURL) {
			return errors.New("the Discord webhook URL must start with https:// and name its host without placeholders")
		}
	case model.ActionGotify:
		if strings.TrimSpace(a.Server) == "" {
			return errors.New("the Gotify server URL is required")
		}
		if !looksLikeHTTPURL(a.Server) {
			return errors.New("the Gotify server must start with http:// or https:// and name its host without placeholders")
		}
		if strings.TrimSpace(a.Token) == "" {
			return errors.New("a Gotify application token is required")
		}
		if p := strings.TrimSpace(a.Priority); p != "" && !hasPlaceholder(p) {
			if _, err := gotifyPriority(p); err != nil {
				return err
			}
		}
	case model.ActionMatrix:
		if strings.TrimSpace(a.Server) == "" {
			return errors.New("the Matrix homeserver URL is required")
		}
		if !looksLikeHTTPURL(a.Server) {
			return errors.New("the Matrix homeserver must start with http:// or https:// and name its host without placeholders")
		}
		if strings.TrimSpace(a.Token) == "" {
			return errors.New("a Matrix access token is required")
		}
		room := strings.TrimSpace(a.RoomID)
		if room == "" {
			return errors.New("a Matrix room ID is required")
		}
		if !hasPlaceholder(room) && !matrixRoomRe.MatchString(room) {
			return errors.New("the Matrix room ID must look like !abcdef:example.org (Room settings › Advanced; an #alias will not do)")
		}
	case model.ActionTwilio:
		sid := strings.TrimSpace(a.AccountSID)
		if sid == "" {
			return errors.New("a Twilio account SID is required")
		}
		if !hasPlaceholder(sid) && !twilioSIDRe.MatchString(sid) {
			return errors.New("the Twilio account SID starts with AC followed by 32 letters and digits")
		}
		if strings.TrimSpace(a.Token) == "" {
			return errors.New("a Twilio auth token is required")
		}
		if strings.TrimSpace(a.From) == "" {
			return errors.New("a Twilio sender (a phone number or a messaging service SID starting MG) is required")
		}
		to := splitList(a.To)
		if len(to) == 0 {
			return errors.New("at least one phone number to send to is required")
		}
		for _, n := range to {
			if !hasPlaceholder(n) && !e164Re.MatchString(n) {
				return fmt.Errorf("%q is not a phone number in international format (+15551234567)", n)
			}
		}
	case model.ActionPagerDuty:
		if strings.TrimSpace(a.RoutingKey) == "" {
			return errors.New("a PagerDuty integration (routing) key is required")
		}
		if !incidentEventActionsOK[strings.ToLower(strings.TrimSpace(a.EventAction))] {
			return errors.New(`the PagerDuty event action must be "auto", "trigger" or "resolve"`)
		}
		if s := strings.ToLower(strings.TrimSpace(a.Severity)); s != "" && s != "auto" && !hasPlaceholder(s) && !pagerDutySeverities[s] {
			return errors.New(`the PagerDuty severity must be "auto", "critical", "error", "warning" or "info"`)
		}
	case model.ActionOpsgenie:
		if strings.TrimSpace(a.Token) == "" {
			return errors.New("an Opsgenie API key is required")
		}
		switch strings.ToLower(strings.TrimSpace(a.Region)) {
		case "", "us", "eu":
		default:
			return errors.New(`the Opsgenie region must be "us" or "eu"`)
		}
		if !incidentEventActionsOK[opsgenieEventAction(a.EventAction)] {
			return errors.New(`the Opsgenie event action must be "auto", "create" or "close"`)
		}
		if p := strings.TrimSpace(a.Priority); p != "" && !hasPlaceholder(p) && !opsgeniePriorityRe.MatchString(strings.ToUpper(p)) {
			return errors.New("the Opsgenie priority must be one of P1 to P5")
		}
	}
	return nil
}

// opsgenieEventAction maps Opsgenie's own vocabulary (create/close) onto the
// trigger/resolve words the rest of this file uses, so both are accepted.
func opsgenieEventAction(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "create":
		return "trigger"
	case "close":
		return "resolve"
	}
	return s
}

// splitList splits a comma-, semicolon- or whitespace-separated list.
func splitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t' || r == '\r' })
}

func gotifyPriority(p string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(p))
	if err != nil || n < 0 || n > 10 {
		return 0, errors.New("the Gotify priority must be a whole number from 0 to 10")
	}
	return n, nil
}

// truncate shortens s to at most n runes, marking the cut with an ellipsis.
// Providers reject (rather than trim) a message over their limit, and an
// alert that arrives cut short is better than one that does not arrive.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

// channelClient is Runner.client with notify.go's sameAuthorityOnly redirect
// policy, extended to the one credential header of these channels that is not
// Authorization: Gotify's X-Gotify-Key. Go's own client forwards custom
// headers across a redirect to any host, so without this a Gotify server that
// answered with a redirect could hand the application token to another one.
// sameAuthorityOnly returns a copy, so Runner.HTTPClient (which may be shared
// with the plain HTTP action, whose redirects are the user's call) is never
// changed.
func (r *Runner) channelClient(a model.Action) *http.Client {
	c := sameAuthorityOnly(r.client(a))
	inner := c.CheckRedirect
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := inner(req, via); err != nil {
			return err
		}
		first := via[0].URL
		if req.URL.Scheme != first.Scheme || req.URL.Host != first.Host {
			req.Header.Del("X-Gotify-Key")
		}
		return nil
	}
	return c
}

// sendChannel performs one request for a channel and returns the response
// with its body read (up to maxOutput) and closed.
func (r *Runner) sendChannel(a model.Action, req *http.Request) (*http.Response, []byte, error) {
	req.Header.Set("User-Agent", "GWatch-automation")
	resp, err := r.channelClient(a).Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxOutput))
	return resp, data, nil
}

// jsonRequest builds a request with a JSON-encoded body.
func jsonRequest(ctx context.Context, method, u string, payload any) (*http.Request, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, u, strings.NewReader(string(b)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

// finish fills in the common parts of a channel's result: the status line and
// response excerpt as output, and an error for a status outside 2xx.
func finish(res model.ActionResult, label string, resp *http.Response, data []byte) model.ActionResult {
	res.StatusCode = resp.StatusCode
	res.Output = fmt.Sprintf("%s → %s\n%s", strings.ToLower(label), resp.Status, excerpt(data))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		res.Error = fmt.Sprintf("unexpected HTTP status %d from %s", resp.StatusCode, label)
		return res
	}
	res.OK = true
	return res
}

// titledMessage joins the title and the message for channels that have no
// separate title field, leaving the title out when it is empty.
func titledMessage(title, msg string) string {
	if title == "" {
		return msg
	}
	if msg == "" {
		return title
	}
	return title + "\n" + msg
}

// ---- telegram ----

type telegramResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
}

func (r *Runner) runTelegram(ctx context.Context, a model.Action, vars Vars, res model.ActionResult) model.ActionResult {
	token := strings.TrimSpace(Expand(a.Token, vars))
	if !telegramTokenRe.MatchString(token) {
		res.Error = "the Telegram bot token is not valid (after expanding placeholders)"
		return res
	}
	chat := strings.TrimSpace(Expand(a.ChatID, vars))
	if !telegramChatRe.MatchString(chat) {
		res.Error = "the Telegram chat ID is not valid (after expanding placeholders): " + chat
		return res
	}
	text := truncate(titledMessage(notifyTitle(a, vars), notifyMessage(a, vars)), 4096)
	// Plain text on purpose: with parse_mode set, a message that happens to
	// contain an unbalanced * or < is rejected by Telegram outright.
	payload := map[string]any{"chat_id": chat, "text": text, "disable_web_page_preview": true}
	req, err := jsonRequest(ctx, http.MethodPost, telegramAPIForTest+"/bot"+token+"/sendMessage", payload)
	if err != nil {
		res.Error = strings.ReplaceAll(err.Error(), token, "<bot token>")
		return res
	}
	resp, data, err := r.sendChannel(a, req)
	if err != nil {
		res.Error = strings.ReplaceAll(err.Error(), token, "<bot token>")
		return res
	}
	res = finish(res, "Telegram", resp, data)
	if !res.OK {
		return res
	}
	var tr telegramResponse
	if err := json.Unmarshal(data, &tr); err != nil || !tr.OK {
		res.OK = false
		res.Error = "Telegram did not report success: " + excerpt(data)
	}
	return res
}

// ---- discord ----

func (r *Runner) runDiscord(ctx context.Context, a model.Action, vars Vars, res model.ActionResult) model.ActionResult {
	u, err := webhookURL(a.WebhookURL, vars)
	if err != nil {
		res.Error = "the Discord webhook URL " + err.Error()
		return res
	}
	title := notifyTitle(a, vars)
	content := notifyMessage(a, vars)
	if title != "" {
		content = "**" + title + "**\n" + content
	}
	// allowed_mentions with an empty parse list stops a message that
	// contains "@everyone" (a node name, a response body) from pinging the
	// whole server.
	payload := map[string]any{
		"content":          truncate(content, 2000),
		"allowed_mentions": map[string]any{"parse": []string{}},
	}
	req, err := jsonRequest(ctx, http.MethodPost, u, payload)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	resp, data, err := r.sendChannel(a, req)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	return finish(res, "Discord", resp, data)
}

// ---- gotify ----

func (r *Runner) runGotify(ctx context.Context, a model.Action, vars Vars, res model.ActionResult) model.ActionResult {
	// The server is used as written: it is where the token goes, so its
	// scheme and host can never come from a placeholder.
	server := strings.TrimRight(strings.TrimSpace(a.Server), "/")
	u, err := expandURL(server+"/message", vars)
	if server == "" || !looksLikeHTTPURL(server) || err != nil {
		res.Error = "the Gotify server must start with http:// or https:// and name its host without placeholders"
		return res
	}
	token := strings.TrimSpace(Expand(a.Token, vars))
	if token == "" {
		res.Error = "the Gotify application token is empty (after expanding placeholders)"
		return res
	}
	payload := map[string]any{"title": notifyTitle(a, vars), "message": notifyMessage(a, vars)}
	if p := strings.TrimSpace(Expand(a.Priority, vars)); p != "" {
		n, err := gotifyPriority(p)
		if err != nil {
			res.Error = err.Error()
			return res
		}
		payload["priority"] = n
	}
	req, err := jsonRequest(ctx, http.MethodPost, u, payload)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	// The header rather than the ?token= query parameter, so the token is
	// never part of a URL that a proxy log or an error message could show.
	req.Header.Set("X-Gotify-Key", headerSafe(token))
	resp, data, err := r.sendChannel(a, req)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	return finish(res, "Gotify", resp, data)
}

// ---- matrix ----

// matrixTxnID is the transaction ID of a Matrix send. The client-server API
// treats a repeated ID as a retry of the same message, so each run needs one
// of its own.
func matrixTxnID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("gwatch-%d-%s", time.Now().UnixNano(), hex.EncodeToString(b))
}

func (r *Runner) runMatrix(ctx context.Context, a model.Action, vars Vars, res model.ActionResult) model.ActionResult {
	// As for Gotify, the homeserver is used as written, never expanded into
	// another host; the room ID and transaction ID are escaped path segments.
	server := strings.TrimRight(strings.TrimSpace(a.Server), "/")
	base, err := expandURL(server, vars)
	if server == "" || !looksLikeHTTPURL(server) || err != nil {
		res.Error = "the Matrix homeserver must start with http:// or https:// and name its host without placeholders"
		return res
	}
	token := strings.TrimSpace(Expand(a.Token, vars))
	if token == "" {
		res.Error = "the Matrix access token is empty (after expanding placeholders)"
		return res
	}
	room := strings.TrimSpace(Expand(a.RoomID, vars))
	if !matrixRoomRe.MatchString(room) {
		res.Error = "the Matrix room ID is not valid (after expanding placeholders): " + room
		return res
	}
	u := strings.TrimRight(base, "/") + "/_matrix/client/v3/rooms/" + url.PathEscape(room) + "/send/m.room.message/" + url.PathEscape(matrixTxnID())
	payload := map[string]any{"msgtype": "m.text", "body": titledMessage(notifyTitle(a, vars), notifyMessage(a, vars))}
	req, err := jsonRequest(ctx, http.MethodPut, u, payload)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	req.Header.Set("Authorization", "Bearer "+headerSafe(token))
	resp, data, err := r.sendChannel(a, req)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	return finish(res, "Matrix", resp, data)
}

// ---- twilio ----

func (r *Runner) runTwilio(ctx context.Context, a model.Action, vars Vars, res model.ActionResult) model.ActionResult {
	sid := strings.TrimSpace(Expand(a.AccountSID, vars))
	if !twilioSIDRe.MatchString(sid) {
		res.Error = "the Twilio account SID is not valid (after expanding placeholders)"
		return res
	}
	token := strings.TrimSpace(Expand(a.Token, vars))
	from := strings.TrimSpace(Expand(a.From, vars))
	if token == "" || from == "" {
		res.Error = "a Twilio auth token and sender are required (after expanding placeholders)"
		return res
	}
	var to []string
	for _, n := range splitList(Expand(a.To, vars)) {
		if !e164Re.MatchString(n) {
			res.Error = fmt.Sprintf("%q is not a phone number in international format (after expanding placeholders)", n)
			return res
		}
		to = append(to, n)
	}
	if len(to) == 0 {
		res.Error = "no phone number to send to (after expanding placeholders)"
		return res
	}
	// An SMS has no title; the message alone is sent, kept to the 1600
	// characters Twilio accepts in one request.
	body := truncate(notifyMessage(a, vars), 1600)
	u := twilioAPIForTest + "/2010-04-01/Accounts/" + url.PathEscape(sid) + "/Messages.json"
	var out []string
	sent := 0
	for _, number := range to {
		form := url.Values{}
		form.Set("To", number)
		// A messaging service SID (MG…) lets Twilio pick the sender from a
		// pool; anything else is a phone number or alphanumeric sender ID.
		if strings.HasPrefix(from, "MG") {
			form.Set("MessagingServiceSid", from)
		} else {
			form.Set("From", from)
		}
		form.Set("Body", body)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
		if err != nil {
			res.Error = err.Error()
			return res
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetBasicAuth(sid, token)
		resp, data, err := r.sendChannel(a, req)
		if err != nil {
			out = append(out, fmt.Sprintf("twilio → %s: %v", number, err))
			if res.Error == "" {
				res.Error = fmt.Sprintf("sending to %s failed: %v", number, err)
			}
			continue
		}
		res.StatusCode = resp.StatusCode
		out = append(out, fmt.Sprintf("twilio → %s: %s\n%s", number, resp.Status, excerpt(data)))
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if res.Error == "" {
				res.Error = fmt.Sprintf("unexpected HTTP status %d from Twilio for %s", resp.StatusCode, number)
			}
			continue
		}
		sent++
	}
	res.Output = strings.Join(out, "\n")
	// Every number has to receive the message for the run to count as a
	// success; a partial send is reported as a failure that names who missed out.
	res.OK = sent == len(to)
	return res
}

// ---- incidents: pagerduty and opsgenie ----

// resolves reports whether this run should close the incident rather than
// open it: always for "resolve", never for "trigger", and in auto mode when
// the event that fired the action means the problem is over.
func resolves(eventAction string, vars Vars) bool {
	switch strings.ToLower(strings.TrimSpace(eventAction)) {
	case "resolve":
		return true
	case "trigger":
		return false
	}
	return resolvingEvents[vars["event"]]
}

// incidentKey is the PagerDuty dedup_key or Opsgenie alias. A key set on the
// action wins (placeholders expanded); otherwise it is one key per rule, or
// per check with warnings filed apart from outages, so that the recovery
// resolves exactly the incident the outage opened and nothing else.
func incidentKey(a model.Action, vars Vars) string {
	if k := strings.TrimSpace(Expand(a.DedupKey, vars)); k != "" {
		return truncate(k, 255)
	}
	if id := vars["rule.id"]; id != "" {
		return "gwatch-rule-" + id
	}
	node := vars["node.id"]
	if node == "" || node == "0" {
		return "gwatch-test"
	}
	key := "gwatch-node-" + node
	if c := vars["check.id"]; c != "" && c != "0" {
		key += "-check-" + c
	}
	if warningEvents[vars["event"]] {
		key += "-warning"
	}
	return key
}

// incidentSource names what the incident is about, for PagerDuty's required
// payload.source: the host if there is one, else the node (or rule) name.
func incidentSource(vars Vars) string {
	for _, k := range []string{"node.host", "node.name"} {
		if v := strings.TrimSpace(vars[k]); v != "" {
			return v
		}
	}
	return "GWatch"
}

func incidentDetails(vars Vars, msg string) map[string]string {
	d := map[string]string{"message": msg}
	for _, k := range []string{"event", "status", "node.name", "check.name", "target", "instance", "rule.name"} {
		if v := strings.TrimSpace(vars[k]); v != "" {
			d[k] = v
		}
	}
	return d
}

type pagerDutyResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

func (r *Runner) runPagerDuty(ctx context.Context, a model.Action, vars Vars, res model.ActionResult) model.ActionResult {
	key := strings.TrimSpace(Expand(a.RoutingKey, vars))
	if key == "" {
		res.Error = "the PagerDuty routing key is empty (after expanding placeholders)"
		return res
	}
	dedup := incidentKey(a, vars)
	msg := notifyMessage(a, vars)
	payload := map[string]any{"routing_key": key, "dedup_key": dedup, "client": "GWatch"}
	action := "trigger"
	if resolves(a.EventAction, vars) {
		action = "resolve"
	} else {
		sev := strings.ToLower(strings.TrimSpace(Expand(a.Severity, vars)))
		if sev == "" || sev == "auto" {
			sev = "critical"
			if warningEvents[vars["event"]] {
				sev = "warning"
			}
		}
		if !pagerDutySeverities[sev] {
			res.Error = "the PagerDuty severity is not valid (after expanding placeholders): " + sev
			return res
		}
		body := map[string]any{
			"summary":        truncate(msg, 1024),
			"source":         truncate(incidentSource(vars), 255),
			"severity":       sev,
			"custom_details": incidentDetails(vars, msg),
		}
		if v := vars["check.name"]; v != "" {
			body["component"] = v
		}
		if v := vars["node.group"]; v != "" {
			body["group"] = v
		}
		if v := vars["check.type"]; v != "" {
			body["class"] = v
		}
		payload["payload"] = body
	}
	payload["event_action"] = action
	req, err := jsonRequest(ctx, http.MethodPost, pagerDutyURLForTest, payload)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	resp, data, err := r.sendChannel(a, req)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res = finish(res, "PagerDuty", resp, data)
	res.Output = fmt.Sprintf("pagerduty %s %s → %s\n%s", action, dedup, resp.Status, excerpt(data))
	if !res.OK {
		return res
	}
	var pr pagerDutyResponse
	if err := json.Unmarshal(data, &pr); err != nil || pr.Status != "success" {
		res.OK = false
		res.Error = "PagerDuty did not report success: " + excerpt(data)
	}
	return res
}

func (r *Runner) runOpsgenie(ctx context.Context, a model.Action, vars Vars, res model.ActionResult) model.ActionResult {
	key := strings.TrimSpace(Expand(a.Token, vars))
	if key == "" {
		res.Error = "the Opsgenie API key is empty (after expanding placeholders)"
		return res
	}
	base := opsgenieUSAPIForTest
	if strings.EqualFold(strings.TrimSpace(a.Region), "eu") {
		base = opsgenieEUAPIForTest
	}
	alias := truncate(incidentKey(a, vars), 512)
	msg := notifyMessage(a, vars)
	source := "GWatch"
	if inst := strings.TrimSpace(vars["instance"]); inst != "" {
		source = "GWatch " + inst
	}
	var (
		u       string
		payload map[string]any
		action  string
	)
	if resolves(opsgenieEventAction(a.EventAction), vars) {
		action = "close"
		u = base + "/v2/alerts/" + url.PathEscape(alias) + "/close?identifierType=alias"
		payload = map[string]any{"source": source, "note": truncate(msg, 25000)}
	} else {
		action = "create"
		u = base + "/v2/alerts"
		payload = map[string]any{
			"message":     truncate(msg, 130),
			"alias":       alias,
			"description": truncate(msg, 15000),
			"source":      source,
			"details":     incidentDetails(vars, msg),
		}
		if v := strings.TrimSpace(vars["node.name"]); v != "" {
			payload["entity"] = truncate(v, 512)
		}
		if p := strings.ToUpper(strings.TrimSpace(Expand(a.Priority, vars))); p != "" {
			if !opsgeniePriorityRe.MatchString(p) {
				res.Error = "the Opsgenie priority is not valid (after expanding placeholders): " + p
				return res
			}
			payload["priority"] = p
		}
		if tags := splitList(strings.ReplaceAll(Expand(a.Tags, vars), " ", "")); len(tags) > 0 {
			payload["tags"] = tags
		}
	}
	req, err := jsonRequest(ctx, http.MethodPost, u, payload)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	req.Header.Set("Authorization", "GenieKey "+headerSafe(key))
	resp, data, err := r.sendChannel(a, req)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res = finish(res, "Opsgenie", resp, data)
	res.Output = fmt.Sprintf("opsgenie %s %s → %s\n%s", action, alias, resp.Status, excerpt(data))
	return res
}

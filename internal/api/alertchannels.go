package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/jxburros/GWatch/internal/model"
)

// Settings › Alerts › Also notify (issue #122). Each channel is an ordinary
// notification action (Slack, Teams, Telegram, PagerDuty …) that the engine
// sends to at the moments it sends an alert e-mail. Its credentials are
// handled exactly like the SMTP password beside it: sealed at rest by the
// store, and never sent back out by GET /api/settings, which returns the
// mask in their place. A save that sends the mask back keeps the stored
// value, matched by the channel's server-assigned ID.

// maxAlertChannels is a sanity bound, not a product limit: every channel is
// a request per alert, and a list longer than this is a mistake.
const maxAlertChannels = 50

// maskAlertChannels returns a copy of the channels with every secret field
// replaced by the mask. The copy matters: the slice handed in is the engine's
// live settings, whose actions must keep their real credentials.
func maskAlertChannels(in []model.AlertChannel) []model.AlertChannel {
	if in == nil {
		return []model.AlertChannel{}
	}
	out := make([]model.AlertChannel, len(in))
	copy(out, in)
	for i := range out {
		out[i].Groups = append([]string(nil), in[i].Groups...)
		_ = out[i].Action.TransformSecrets(maskSecret)
	}
	return out
}

// blankAlertChannels empties the secret fields, for the shareable config
// export, which leaves every credential out.
func blankAlertChannels(in []model.AlertChannel) []model.AlertChannel {
	out := make([]model.AlertChannel, len(in))
	copy(out, in)
	for i := range out {
		_ = out[i].Action.TransformSecrets(blankSecret)
	}
	return out
}

func newChannelID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "ch-" + hex.EncodeToString(b)
}

// restoreChannelSecrets puts the stored credential back wherever the client
// returned the mask, field by field through Action.SecretFields, so a field
// the user did change is kept and one they left alone is not lost.
func restoreChannelSecrets(ch *model.AlertChannel, stored *model.AlertChannel) {
	now := ch.Action.SecretFields()
	var prev []*string
	if stored != nil {
		prev = stored.Action.SecretFields()
	}
	for i, p := range now {
		if *p != passwordMask {
			continue
		}
		if prev != nil {
			*p = *prev[i]
		} else {
			*p = ""
		}
	}
	var prevHeaders map[string]string
	if stored != nil {
		prevHeaders = stored.Action.Headers
	}
	if ch.Action.Headers != nil {
		restoreSecretMap(&ch.Action.Headers, prevHeaders)
	}
}

// normalizeAlertChannels validates and tidies the channel list of a settings
// save: every channel gets an ID (kept if it already has one the server
// issued), its masked secrets restored from the current settings, a severity
// of "warning" unless it asked for "down", trimmed groups, and an action that
// passes the same validation a trigger's would. Only notification types are
// accepted; a script or a git command is not an alert channel.
func (s *Server) normalizeAlertChannels(in []model.AlertChannel, current []model.AlertChannel) ([]model.AlertChannel, error) {
	if len(in) > maxAlertChannels {
		return nil, fmt.Errorf("at most %d alert channels can be configured", maxAlertChannels)
	}
	byID := map[string]*model.AlertChannel{}
	for i := range current {
		byID[current[i].ID] = &current[i]
	}
	out := make([]model.AlertChannel, 0, len(in))
	seen := map[string]bool{}
	for i, ch := range in {
		ch.Name = strings.TrimSpace(ch.Name)
		label := ch.Name
		if label == "" {
			label = fmt.Sprintf("channel %d", i+1)
		}
		stored := byID[strings.TrimSpace(ch.ID)]
		if stored == nil || seen[stored.ID] {
			ch.ID = newChannelID()
			stored = nil
		} else {
			ch.ID = stored.ID
		}
		seen[ch.ID] = true
		if ch.Name == "" {
			ch.Name = string(ch.Action.Type)
		}
		switch ch.MinSeverity {
		case model.AlertSeverityDown, model.AlertSeverityWarning:
		case "":
			ch.MinSeverity = model.AlertSeverityWarning
		default:
			return nil, fmt.Errorf("alert channel %s: the minimum severity must be %q or %q", label, model.AlertSeverityWarning, model.AlertSeverityDown)
		}
		groups := make([]string, 0, len(ch.Groups))
		dup := map[string]bool{}
		for _, g := range ch.Groups {
			g = strings.TrimSpace(g)
			if g != "" && !dup[strings.ToLower(g)] {
				dup[strings.ToLower(g)] = true
				groups = append(groups, g)
			}
		}
		ch.Groups = groups
		if !ch.Action.Type.IsNotify() {
			return nil, fmt.Errorf("alert channel %s: %q is not a notification channel (use Slack, Teams, ntfy, Pushover, Telegram, Discord, Gotify, Matrix, Twilio SMS, PagerDuty or Opsgenie)", label, ch.Action.Type)
		}
		restoreChannelSecrets(&ch, stored)
		if err := s.normalizeAction(&ch.Action); err != nil {
			return nil, fmt.Errorf("alert channel %s: %w", label, err)
		}
		out = append(out, ch)
	}
	return out, nil
}

// handleTestAlertChannel sends a sample down alert through one channel as it
// stands in the form, which may still carry masked secrets from GET
// /api/settings: they are restored from the saved channel with the same ID
// before it runs, so a saved channel can be tested without retyping its key.
func (s *Server) handleTestAlertChannel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Channel model.AlertChannel `json:"channel"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeDecodeError(w, err)
		return
	}
	ch := body.Channel
	var stored *model.AlertChannel
	current := s.Engine.Settings().Alerts.Channels
	for i := range current {
		if current[i].ID != "" && current[i].ID == strings.TrimSpace(ch.ID) {
			c := current[i]
			stored = &c
			break
		}
	}
	if !ch.Action.Type.IsNotify() {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("%q is not a notification channel", ch.Action.Type))
		return
	}
	restoreChannelSecrets(&ch, stored)
	if err := s.normalizeAction(&ch.Action); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.Engine.TestAlertChannel(r.Context(), ch))
}

// alertChannelsSummary is a digest of a channel list that is only ever
// compared, never written anywhere, to tell whether a settings save changed
// the channels; the audit line itself just says that they changed and how
// many are on. Encoding the whole channel as JSON covers every field,
// credentials included, so a rotated token counts as a change too.
func alertChannelsSummary(chs []model.AlertChannel) string {
	b, _ := json.Marshal(chs)
	return string(b)
}

// enabledAlertChannels counts the channels that are switched on.
func enabledAlertChannels(chs []model.AlertChannel) int {
	n := 0
	for _, ch := range chs {
		if ch.Enabled {
			n++
		}
	}
	return n
}

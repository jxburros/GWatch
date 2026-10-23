package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/auth"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
)

// This file is the part of the agent story that goes beyond "post a reading":
// letting an administrator read a machine's token back and replace it, and the
// agent's occasional question to GWatch about its own settings.
//
// What an agent may be told is a closed list (model.AgentSettings). GWatch has
// no way to make an agent do anything; the agent asks, the answer is data, and
// everything in it is something the agent checks and applies on its own terms.

// maxCheckInBody bounds a check-in. The inventory is the only sizeable part: a
// few hundred names at most, which is well under this.
const maxCheckInBody = 512 << 10

// authenticateAgent resolves the bearer token on an agent route to a machine.
//
// A token is a credential, so a wrong one counts against the same failure
// budget as a wrong password, and a correct one gives its budget back, so an
// agent reporting every minute never exhausts it. It also finishes a rotation:
// a request made with the replacement token is the proof that the machine has
// it, so that request is what retires the old one. usedCurrent says which token
// the caller held when it arrived, because only a caller holding the token
// being replaced is handed the new one.
func (s *Server) authenticateAgent(w http.ResponseWriter, r *http.Request, rejected string) (agent model.Agent, usedCurrent, ok bool) {
	ctx := r.Context()
	token := bearerToken(r)
	if token == "" {
		writeError(w, http.StatusUnauthorized, "an agent token is required")
		return model.Agent{}, false, false
	}
	ip := s.clientIP(r)
	if allowed, wait := s.failLimiter.Allow(limiterKey(limiterIngest, ip)); !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds()+0.999)))
		writeError(w, http.StatusTooManyRequests, "too many failed attempts; try again shortly")
		return model.Agent{}, false, false
	}
	hash := auth.HashToken(token)
	agent, pending, err := s.Store.AgentByAnyTokenHash(ctx, hash)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.auditAuthFailure(ctx, rejected,
				fmt.Sprintf("An unknown or revoked agent token was presented from %s.", ip), ip)
			writeError(w, http.StatusUnauthorized, "this agent token is not registered, or has been revoked")
			return model.Agent{}, false, false
		}
		s.fail(w, err)
		return model.Agent{}, false, false
	}
	s.failLimiter.Reset(limiterKey(limiterIngest, ip))

	if pending {
		if err := s.Store.PromoteAgentToken(ctx, agent.ID, hash, auth.KeyPrefix(token)); err != nil {
			s.fail(w, err)
			return model.Agent{}, false, false
		}
		s.auditAgent(ctx, "Agent token replaced: "+agent.Name,
			"The machine presented its replacement token, so the previous one no longer works.")
		if fresh, err := s.Store.GetAgent(ctx, agent.ID); err == nil {
			agent = fresh
		}
	}
	return agent, !pending, true
}

// auditAgent records an authentication event that has no acting user
// and is not a failure: something a machine did with its own credential.
func (s *Server) auditAgent(ctx context.Context, title, detail string) {
	s.Engine.RecordEvent(model.Event{Type: model.EventAuth, Title: title, Detail: detail, Actor: "agent"})
}

// handleAgentCheckIn answers an agent's occasional question about its own
// settings. Like the ingest route it sits outside the authorization table and
// is reached with an agent token and nothing else.
func (s *Server) handleAgentCheckIn(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "an agent check-in is submitted with POST")
		return
	}
	if s.Engine == nil {
		writeError(w, http.StatusServiceUnavailable, "the monitoring engine is not running")
		return
	}
	agent, usedCurrent, ok := s.authenticateAgent(w, r, "Agent check-in rejected")
	if !ok {
		return
	}

	var in model.AgentCheckIn
	if err := json.NewDecoder(io.LimitReader(r.Body, maxCheckInBody)).Decode(&in); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "the body is not a GWatch agent check-in")
		return
	}
	if err := s.Store.RecordAgentCheckIn(ctx, agent.ID, in.Inventory); err != nil {
		s.fail(w, err)
		return
	}

	out := model.AgentSettings{
		CheckInSeconds: int(model.CheckInDefault.Seconds()),
		Collect:        agent.Collect,
	}
	if s.Updater != nil {
		lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		out.LatestAgent = s.Updater.AgentLatest(lctx).Version
		cancel()
	}
	// The replacement goes only to a caller that holds the token it replaces. A
	// caller that already holds the replacement has nothing left to be told, and
	// a rotation started with "retire the old token now" has no old token to
	// hold.
	if agent.RotationPending && usedCurrent {
		if tokens, err := s.Store.RevealAgentToken(ctx, agent.ID); err == nil && tokens.Pending != "" {
			out.Token = tokens.Pending
			s.auditAgent(ctx, "Replacement token delivered to "+agent.Name,
				"The machine asked for its settings while a rotation was waiting, and was given the new token. The old one stops working when the new one is first used.")
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleRevealAgentToken shows an administrator a machine's token, and the
// replacement waiting beside it if there is one. It is a POST, and it writes to
// the audit trail, because reading a credential back is an event that someone
// should be able to find later.
func (s *Server) handleRevealAgentToken(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	agent, err := s.Store.GetAgent(ctx, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	tokens, err := s.Store.RevealAgentToken(ctx, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.auditAuth(ctx, "Agent token viewed: "+agent.Name,
		"The token was displayed so it could be compared with the one in the machine's configuration file.")
	writeJSON(w, http.StatusOK, map[string]any{
		"agent":   agent,
		"token":   tokens.Current,
		"pending": tokens.Pending,
		// Unreadable is not an error: a machine registered before tokens were
		// kept, or restored from a backup, only has a digest.
		"viewable": tokens.Current != "",
	})
}

// handleRotateAgentToken issues a replacement token for a machine.
//
// There are two ways to do it and they answer different worries. The default
// leaves the old token working until the new one is first used, and lets the
// agent collect the new one the next time it checks in — nothing to copy, and
// no gap in readings. "retireOld" is for a token that may have leaked: the old
// one stops working this instant, nothing is delivered over the network, and
// the new token has to be put on the machine by hand.
func (s *Server) handleRotateAgentToken(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var body struct {
		RetireOld bool `json:"retireOld"`
	}
	if err := decodeJSON(r, &body); err != nil && !errors.Is(err, io.EOF) {
		writeDecodeError(w, err)
		return
	}
	agent, err := s.Store.GetAgent(ctx, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if agent.Revoked() {
		writeError(w, http.StatusConflict, "this machine has been revoked; register it again instead of rotating its token")
		return
	}
	token, prefix, err := auth.NewAgentToken()
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Store.StartAgentRotation(ctx, id, token, prefix, body.RetireOld); err != nil {
		s.fail(w, err)
		return
	}
	if body.RetireOld {
		s.auditAuth(ctx, "Agent token replaced now: "+agent.Name,
			"The old token stopped working immediately. The new one has to be put on the machine by hand.")
	} else {
		s.auditAuth(ctx, "Agent token rotation started: "+agent.Name,
			"A replacement token is waiting. The machine collects it the next time it checks in, and the old token stops working when the new one is first used.")
	}
	updated, err := s.Store.GetAgent(ctx, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "retiredOld": body.RetireOld, "agent": updated})
}

// handleCancelAgentRotation drops a replacement token nobody has used yet.
func (s *Server) handleCancelAgentRotation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	agent, err := s.Store.GetAgent(ctx, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Store.CancelAgentRotation(ctx, id); err != nil {
		s.fail(w, err)
		return
	}
	s.auditAuth(ctx, "Agent token rotation cancelled: "+agent.Name, "The machine keeps the token it has.")
	w.WriteHeader(http.StatusNoContent)
}

// handleSetAgentCollect stores which readings an administrator wants from a
// machine. The agent learns of it the next time it checks in.
func (s *Server) handleSetAgentCollect(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var sel model.CollectSelection
	if err := decodeJSON(r, &sel); err != nil {
		writeDecodeError(w, err)
		return
	}
	sel = sel.Normalize()
	if err := sel.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	agent, err := s.Store.GetAgent(ctx, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Store.SetAgentCollect(ctx, id, sel); err != nil {
		s.fail(w, err)
		return
	}
	s.auditAuth(ctx, "Collection changed for "+agent.Name, describeSelection(sel))
	updated, err := s.Store.GetAgent(ctx, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// describeSelection words a selection for the audit trail.
func describeSelection(sel model.CollectSelection) string {
	if sel.IsZero() {
		return "Back to automatic: every metric family, the automatic interfaces, disks and filesystems."
	}
	var parts []string
	add := func(label string, list []string) {
		if len(list) > 0 {
			parts = append(parts, label+": "+strings.Join(list, ", "))
		}
	}
	add("metrics", sel.Metrics)
	add("interfaces", sel.Interfaces)
	add("disks", sel.Disks)
	add("filesystems", sel.Mounts)
	add("services", sel.Services)
	add("programs", sel.Processes)
	return strings.Join(parts, "; ") + ". The machine applies this the next time it checks in."
}

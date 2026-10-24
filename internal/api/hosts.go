package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/auth"
	"github.com/jxburros/GWatch/internal/hostmon"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
	"github.com/jxburros/GWatch/internal/sysmetrics"
)

// maxIngestBody caps a pushed reading. A hardware reading is a few kilobytes;
// the limit is what keeps a machine holding a valid token from using it to
// push megabytes at the database.
const maxIngestBody = 256 << 10

// agentStaleAfter is how long a machine may go quiet before the hardware list
// marks it as not reporting. It is a display default; a check that watches the
// machine has its own, configurable, staleness limit.
const agentStaleAfter = 5 * time.Minute

// maxHostChartPoints bounds a history response. A machine's results over a year
// are far more points than a chart can draw, so they are averaged into buckets
// as they are read rather than sent as they are.
const maxHostChartPoints = 600

// ---- reading hardware ----

// handleListHosts returns every machine GWatch has readings for: this
// computer, and each registered agent whether or not it has reported yet.
func (s *Server) handleListHosts(w http.ResponseWriter, r *http.Request) {
	summaries, err := s.hostSummaries(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, summaries)
}

// handleGetHost returns one machine with its newest reading.
func (s *Server) handleGetHost(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	summaries, err := s.hostSummaries(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	for _, h := range summaries {
		if h.Key == key {
			writeJSON(w, http.StatusOK, h)
			return
		}
	}
	writeError(w, http.StatusNotFound, "no such machine")
}

// handleLocalWatchable lists every service on the computer GWatch runs on
// and every program running on it now, for choosing which to watch. The list
// is made when this is asked and never otherwise: it is the whole machine,
// and nothing but the person choosing needs it. An agent's equivalent arrives
// with its check-in (Agent.Inventory).
func (s *Server) handleLocalWatchable(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	out := map[string]any{}
	var problems []string
	if svcs, err := sysmetrics.ListServices(ctx); err != nil {
		problems = append(problems, "services: "+err.Error())
	} else {
		out["services"] = svcs
	}
	if procs, err := sysmetrics.ListProcesses(ctx); err != nil {
		problems = append(problems, "programs: "+err.Error())
	} else {
		out["processes"] = procs
	}
	out["problems"] = problems
	writeJSON(w, http.StatusOK, out)
}

// handleSetLocalWatch sets the services and programs the computer GWatch
// runs on watches. Its hardware check judges them from the next sample on.
func (s *Server) handleSetLocalWatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var sel model.CollectSelection
	if err := decodeJSON(r, &sel); err != nil {
		writeDecodeError(w, err)
		return
	}
	if err := sel.Normalize().Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := s.Engine.Hosts().SetLocalWatch(ctx, sel)
	if err != nil {
		s.fail(w, err)
		return
	}
	detail := "Nothing is watched now."
	if saved.Watching() {
		var parts []string
		if len(saved.Services) > 0 {
			parts = append(parts, "services: "+strings.Join(saved.Services, ", "))
		}
		if len(saved.Processes) > 0 {
			parts = append(parts, "programs: "+strings.Join(saved.Processes, ", "))
		}
		detail = strings.Join(parts, "; ") + ". The next reading reports them."
	}
	s.auditAuth(ctx, "Watched services and programs changed for this computer", detail)
	writeJSON(w, http.StatusOK, saved)
}

// handleHostHistory returns the numeric series for one machine over a range.
//
// The series is read from the results of the hardware check that watches the
// machine — the same results every other view of that check reads — and not
// from a store of readings of its own. A machine no hardware check watches has
// no history, only its newest reading.
func (s *Server) handleHostHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	key := r.PathValue("key")
	rng, err := store.ParseRange(r.URL.Query().Get("range"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	now := time.Now()
	from := now.Add(-rng.Duration)
	samples, err := s.Store.HostSeries(ctx, s.Engine.HostChecks(key), from, now, maxHostChartPoints)
	if err != nil {
		s.fail(w, err)
		return
	}
	for i := range samples {
		samples[i].Key = key
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"key":     key,
		"range":   rng.Name,
		"from":    from,
		"to":      now,
		"samples": samples,
	})
}

// latestReadings is every machine's newest reading: what is held in memory,
// and for a machine with none (the first look after a restart) what its check
// last recorded.
func (s *Server) latestReadings(ctx context.Context, keys []string) map[string]model.HostMetrics {
	latest := s.Engine.Hosts().All()
	for _, key := range keys {
		if _, ok := latest[key]; ok {
			continue
		}
		if m, ok := s.Engine.RestoreHost(ctx, key); ok {
			latest[key] = m
		}
	}
	return latest
}

// hostSummaries builds the hardware list: this computer first, then every
// registered machine, each with its newest reading.
func (s *Server) hostSummaries(r *http.Request) ([]model.HostSummary, error) {
	ctx := r.Context()
	agents, err := s.Store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	nodeNames, err := s.nodeNames(r)
	if err != nil {
		return nil, err
	}
	keys := []string{model.HostKeyLocal}
	for _, a := range agents {
		keys = append(keys, a.HostKey())
	}
	latest := s.latestReadings(ctx, keys)

	out := make([]model.HostSummary, 0, len(agents)+1)
	local := hostSummary(model.HostSummary{
		Key:    model.HostKeyLocal,
		Name:   s.localHostName(ctx, latest),
		Source: model.HostSourceLocal,
	}, latest[model.HostKeyLocal])
	if w := s.Engine.Hosts().LocalWatch(); w.Watching() {
		local.Watch = &w
	}
	out = append(out, local)

	// An agent's inventory is every service and program on that machine. The
	// same list for this computer is administrator-only and never an API key's
	// (handleLocalWatchable), and only the collection picker, which reads
	// /api/agents, needs it; the hardware list, open to every viewer and every
	// read-only key, leaves it out for anyone who is not an administrator.
	admin := auth.FromContext(ctx).IsAdmin()
	for _, a := range agents {
		agent := a
		if !admin {
			agent.Inventory = nil
		}
		summary := model.HostSummary{
			Key:    agent.HostKey(),
			Name:   agent.Name,
			Source: model.HostSourceAgent,
			Agent:  &agent,
			NodeID: agent.NodeID,
		}
		if agent.NodeID != nil {
			summary.NodeName = nodeNames[*agent.NodeID]
		}
		out = append(out, hostSummary(summary, latest[agent.HostKey()]))
	}

	// Machines that only exist as a scraped endpoint have readings but no
	// agent row. They are listed under the check that fetched them.
	scraped := make([]model.HostSummary, 0)
	for key, m := range latest {
		if key == model.HostKeyLocal || strings.HasPrefix(key, "agent:") {
			continue
		}
		name := m.Hostname
		if name == "" {
			name = key
		}
		scraped = append(scraped, hostSummary(model.HostSummary{Key: key, Name: name, Source: model.HostSourceURL}, m))
	}
	sort.Slice(scraped, func(i, j int) bool { return scraped[i].Name < scraped[j].Name })
	return append(out, scraped...), nil
}

// hostSummary fills in the reading and the derived status for one machine.
func hostSummary(summary model.HostSummary, metrics model.HostMetrics) model.HostSummary {
	if summary.Agent != nil && (summary.Agent.Revoked() || !summary.Agent.Enabled) {
		summary.Status = model.StatusPaused
	} else {
		summary.Status = model.StatusUnknown
	}
	if metrics.Timestamp.IsZero() {
		return summary
	}
	summary.Metrics = &metrics
	summary.Warnings = metrics.Warnings
	if summary.Status == model.StatusPaused {
		return summary
	}
	// The list's own idea of health is only "is this machine still talking to
	// us". What counts as too hot or too full is a check's business, because
	// only a check knows the thresholds someone chose.
	summary.Stale = time.Since(metrics.Timestamp) > agentStaleAfter
	if summary.Stale {
		summary.Status = model.StatusDown
	} else {
		summary.Status = model.StatusUp
	}
	return summary
}

// localHostName names this computer from its newest reading, falling back to
// the instance name so the row is never blank on a fresh install.
func (s *Server) localHostName(ctx context.Context, latest map[string]model.HostMetrics) string {
	if m, ok := latest[model.HostKeyLocal]; ok && m.Hostname != "" {
		return m.Hostname
	}
	if st, err := s.Store.LoadSettings(ctx); err == nil && st.General.InstanceName != "" {
		return st.General.InstanceName
	}
	return "This computer"
}

func (s *Server) nodeNames(r *http.Request) (map[int64]string, error) {
	nodes, err := s.Store.ListNodes(r.Context())
	if err != nil {
		return nil, err
	}
	out := make(map[int64]string, len(nodes))
	for _, n := range nodes {
		out[n.ID] = n.Name
	}
	return out, nil
}

// ---- registered machines ----

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := s.Store.ListAgents(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	for i := range agents {
		agents[i] = s.withCollectorState(agents[i])
	}
	writeJSON(w, http.StatusOK, agents)
}

// handleCreateAgent registers a machine and mints its token. The token is
// returned exactly once, here; only its hash is stored, so GWatch cannot show
// it again and a lost token means registering the machine afresh.
func (s *Server) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body struct {
		Name   string `json:"name"`
		NodeID *int64 `json:"nodeId"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeDecodeError(w, err)
		return
	}
	name := strings.TrimSpace(body.Name)
	switch {
	case name == "":
		writeError(w, http.StatusBadRequest, "give the machine a name so you can recognise it later")
		return
	case len(name) > 100:
		writeError(w, http.StatusBadRequest, "the name is too long")
		return
	}
	if body.NodeID != nil {
		if _, err := s.Store.GetNode(ctx, *body.NodeID); err != nil {
			writeError(w, http.StatusBadRequest, "the node this machine belongs to does not exist")
			return
		}
	} else {
		// A machine is a node like any other, so one is made for it here: a
		// machine registered without a node of its own would otherwise be
		// watched by nothing and appear nowhere.
		node, err := s.createMachineNode(ctx, name)
		if err != nil {
			s.fail(w, err)
			return
		}
		body.NodeID = &node.ID
	}

	token, prefix, err := auth.NewAgentToken()
	if err != nil {
		s.fail(w, err)
		return
	}
	created, err := s.Store.CreateAgent(ctx, name, body.NodeID, prefix, auth.HashToken(token), auth.FromContext(ctx).Label())
	if err != nil {
		s.fail(w, err)
		return
	}
	s.keepAgentToken(ctx, created.ID, token)
	if body.NodeID != nil {
		if err := s.bindMachineCheck(ctx, *body.NodeID, created); err != nil {
			s.Log.Errorf("bind hardware check to agent %d: %v", created.ID, err)
		}
	}
	s.auditAuth(ctx, "Hardware agent registered: "+created.Name,
		"The machine may submit its own hardware readings and nothing else.")
	writeJSON(w, http.StatusCreated, map[string]any{"token": token, "agent": created})
}

func (s *Server) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var body struct {
		Name string `json:"name"`
		// NodeID stays raw so that a body that leaves it out (one that only
		// switches the machine off, say) keeps the machine on its node, while
		// an explicit null still detaches it.
		NodeID  json.RawMessage `json:"nodeId"`
		Enabled *bool           `json:"enabled"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeDecodeError(w, err)
		return
	}
	current, err := s.Store.GetAgent(ctx, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = current.Name
	}
	enabled := current.Enabled
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	nodeID := current.NodeID
	if len(body.NodeID) > 0 {
		nodeID = nil
		if err := json.Unmarshal(body.NodeID, &nodeID); err != nil {
			writeError(w, http.StatusBadRequest, "nodeId must be a node id or null")
			return
		}
		if nodeID != nil {
			if _, err := s.Store.GetNode(ctx, *nodeID); err != nil {
				writeError(w, http.StatusBadRequest, "the node this machine belongs to does not exist")
				return
			}
		}
	}
	updated, err := s.Store.UpdateAgent(ctx, id, name, nodeID, enabled)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !enabled {
		s.forgetHost(updated.HostKey())
	}
	s.reloadCollectors(ctx)
	writeJSON(w, http.StatusOK, s.withCollectorState(updated))
}

// handleRevokeAgent stops accepting readings from a machine. The registration
// is kept, so past readings still say where they came from; deleting is a
// separate, destructive action.
func (s *Server) handleRevokeAgent(w http.ResponseWriter, r *http.Request) {
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
	if r.URL.Query().Get("purge") == "1" {
		if err := s.Store.DeleteAgent(ctx, id); err != nil {
			s.fail(w, err)
			return
		}
		s.forgetHost(agent.HostKey())
		s.reloadCollectors(ctx)
		s.auditAuth(ctx, "Hardware agent deleted: "+agent.Name, "Its stored readings were deleted with it.")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.Store.RevokeAgent(ctx, id); err != nil {
		s.fail(w, err)
		return
	}
	s.forgetHost(agent.HostKey())
	s.reloadCollectors(ctx)
	s.auditAuth(ctx, "Hardware agent revoked: "+agent.Name, "Its token no longer works; its past readings are kept.")
	w.WriteHeader(http.StatusNoContent)
}

// forgetHost drops a machine's cached reading so a revoked agent stops
// answering checks from memory.
func (s *Server) forgetHost(key string) {
	if s.Engine != nil {
		s.Engine.Hosts().Forget(key)
	}
}

// ---- pairing codes ----

// pairingCodeLifetime is how long a minted pairing code stays redeemable.
//
// Fifteen minutes is chosen for the job the code actually does: someone is
// standing at the GWatch screen, walking to another machine (or reading the
// code to whoever is), and typing it into an installer prompt. A quarter of an
// hour covers that with room for a wrong turn, and it is short enough that a
// code left on a screen, in a chat message or on a sticky note is worthless by
// the time anyone else finds it. Making it longer would buy nothing: a code
// that has gone stale is replaced with two clicks.
const pairingCodeLifetime = 15 * time.Minute

// maxPairBody caps the enrolment request. It carries a code and a few words of
// self-description, so a kilobyte is generous — and unlike the ingest route
// this one is reached with no credential at all, which is exactly why the body
// is bounded before anything is parsed.
const maxPairBody = 4 << 10

// pairingRejected is the single answer to every unusable code: unknown,
// mistyped, expired, cancelled, or already used by another machine. Telling
// them apart would turn the endpoint into an oracle — a guesser could learn
// that a code exists but has run out, which is most of the way to knowing the
// shape of the codes GWatch mints — so the person who mistyped theirs and the
// person fishing for one get the same sentence.
const pairingRejected = "that pairing code is not valid; ask for a fresh one in GWatch (Nodes › Add node › GWatch agent)"

// handleAgentLatest reports the newest published agent release, so the
// interface can mark machines whose agent is behind.
//
// Nothing follows from the answer on the server's side. Agents keep themselves
// up to date from signed releases they fetch and verify themselves; GWatch has
// no way to update one and is deliberately never given one, so this is a label
// on a screen and nothing more (docs/HARDWARE.md#keeping-agents-up-to-date).
func (s *Server) handleAgentLatest(w http.ResponseWriter, r *http.Request) {
	if s.Updater == nil {
		// A build without the updater still runs agents; it just cannot say
		// what the newest one is, which the interface treats as "do not mark
		// anything" rather than as an error on the page.
		writeJSON(w, http.StatusOK, model.AgentRelease{})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, s.Updater.AgentLatest(ctx))
}

func (s *Server) handleListPairings(w http.ResponseWriter, r *http.Request) {
	codes, err := s.Store.ListPairingCodes(r.Context(), 0)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, codes)
}

// handleCreatePairing mints a pairing code for a named machine. Like the token
// minted by handleCreateAgent the code is returned exactly once, here; only its
// hash is stored, so GWatch cannot show it again and a lost code is replaced
// rather than recovered.
func (s *Server) handleCreatePairing(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body struct {
		Name   string `json:"name"`
		NodeID *int64 `json:"nodeId"`
		// Collect is the readings chosen while adding the machine; the agent
		// starts with them once the code is redeemed.
		Collect model.CollectSelection `json:"collect"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeDecodeError(w, err)
		return
	}
	name := strings.TrimSpace(body.Name)
	switch {
	case name == "":
		writeError(w, http.StatusBadRequest, "give the machine a name so you can recognise it later")
		return
	case len(name) > 100:
		writeError(w, http.StatusBadRequest, "the name is too long")
		return
	}
	body.Collect = body.Collect.Normalize()
	if err := body.Collect.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.NodeID != nil {
		if _, err := s.Store.GetNode(ctx, *body.NodeID); err != nil {
			writeError(w, http.StatusBadRequest, "the node this machine belongs to does not exist")
			return
		}
	} else {
		// The machine gets its node now, while there is a name to give it. Its
		// hardware check is left waiting and is completed with the agent's id
		// once the code is redeemed and the agent exists (see handlePair).
		node, err := s.createMachineNode(ctx, name)
		if err != nil {
			s.fail(w, err)
			return
		}
		body.NodeID = &node.ID
	}

	code, err := auth.NewPairingCode()
	if err != nil {
		s.fail(w, err)
		return
	}
	created, err := s.Store.CreatePairingCode(ctx, name, body.NodeID, auth.HashToken(code),
		auth.FromContext(ctx).Label(), time.Now().Add(pairingCodeLifetime))
	if err != nil {
		s.fail(w, err)
		return
	}
	if !body.Collect.IsZero() {
		if err := s.Store.SetPairingCollect(ctx, created.ID, body.Collect); err != nil {
			s.Log.Errorf("keep the chosen readings with pairing code %d: %v", created.ID, err)
		}
	}
	s.auditAuth(ctx, "Pairing code created for "+created.Name,
		fmt.Sprintf("Whoever types it within %s enrols one machine, which may then submit that machine's hardware readings and nothing else.",
			pairingCodeLifetime))
	writeJSON(w, http.StatusCreated, map[string]any{"code": code, "pairing": created})
}

// handleGetPairing reports how one pairing code has got on, which is what the
// "Type this code on the other machine" dialog watches so it can say the
// moment the machine comes in (#69) instead of leaving the person who typed
// the code to go and look for it. The answer walks the same road the machine
// does: the code is waiting, then it has been redeemed and a machine exists,
// then that machine's first reading has arrived. It never carries the code
// itself — GWatch does not have it — nor the agent's token.
func (s *Server) handleGetPairing(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	pairing, err := s.Store.GetPairingCode(ctx, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	out := map[string]any{"pairing": pairing, "state": pairingState(pairing, nil, time.Now())}
	if pairing.AgentID != nil {
		if agent, err := s.Store.GetAgent(ctx, *pairing.AgentID); err == nil {
			out["agent"] = agent
			out["state"] = pairingState(pairing, &agent, time.Now())
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// pairingState names where a pairing has got to: "pending" while the code
// can still be typed, "paired" once a machine has redeemed it, "reporting"
// once that machine's first reading has been accepted, and "expired" or
// "cancelled" for a code that ended without a machine.
func pairingState(p model.PairingCode, agent *model.Agent, now time.Time) string {
	switch {
	case p.Redeemed() && agent != nil && agent.LastSeenAt != nil:
		return "reporting"
	case p.Redeemed():
		return "paired"
	case p.Revoked():
		return "cancelled"
	case p.Expired(now):
		return "expired"
	}
	return "pending"
}

// handleRevokePairing cancels an unused pairing code.
func (s *Server) handleRevokePairing(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	pairing, err := s.Store.GetPairingCode(ctx, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Store.RevokePairingCode(ctx, id); err != nil {
		s.fail(w, err)
		return
	}
	s.auditAuth(ctx, "Pairing code cancelled for "+pairing.Name, "Typing it now enrols nothing.")
	w.WriteHeader(http.StatusNoContent)
}

// handlePair exchanges a pairing code for a real agent token.
//
// This endpoint takes no credential, and it cannot: obtaining one is the whole
// reason it exists. What stands in for the credential is the code itself —
// short-lived, single-use, and cancellable — together with the same per-IP
// failure budget a wrong password or a wrong agent token is counted against,
// so guessing runs out of attempts long before it runs out of codes. Every
// attempt, successful or not, is written to the audit trail.
func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ip := s.clientIP(r)
	if allowed, wait := s.failLimiter.Allow(limiterKey(limiterPair, ip)); !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds()+0.999)))
		writeError(w, http.StatusTooManyRequests, "too many failed attempts; try again shortly")
		return
	}

	var body struct {
		Code     string `json:"code"`
		Hostname string `json:"hostname"`
		OS       string `json:"os"`
		Arch     string `json:"arch"`
		Version  string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxPairBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "the body is not a GWatch pairing request")
		return
	}

	// A code that is not even the right shape is refused with the same words
	// as one that is: "you typed something that is not a code" and "you typed
	// a code that has expired" must not be tellable apart from out here.
	normalized, ok := auth.NormalizePairingCode(body.Code)
	if !ok {
		s.rejectPairing(ctx, w, ip, "The code presented was not in the right form.")
		return
	}
	// Redeeming claims the code and hands back the digest that was stored with
	// it. The claim is atomic, so two machines racing with the same code
	// cannot both be enrolled; the digest comes back so the decision to trust
	// this caller rests on a comparison made here, in constant time, rather
	// than on the index lookup SQLite used to find the row.
	want := auth.HashToken(normalized)
	pairing, stored, err := s.Store.RedeemPairingCode(ctx, want, time.Now(), ip)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.rejectPairing(ctx, w, ip, "The code presented is unknown, expired, cancelled or already used.")
			return
		}
		s.fail(w, err)
		return
	}
	if subtle.ConstantTimeCompare([]byte(stored), []byte(want)) != 1 {
		s.rejectPairing(ctx, w, ip, "The code presented did not match the one it was looked up by.")
		return
	}
	s.failLimiter.Reset(limiterKey(limiterPair, ip))

	// From here the code is spent whatever happens next. That is the right way
	// round: a failure after the claim costs the administrator a fresh code,
	// while a failure that gave the code back would hand a machine that can
	// make registration fail an unlimited number of attempts at it.
	token, prefix, err := auth.NewAgentToken()
	if err != nil {
		s.fail(w, err)
		return
	}
	created, err := s.Store.CreateAgent(ctx, pairing.Name, pairing.NodeID, prefix, auth.HashToken(token),
		fmt.Sprintf("pairing code (%s)", pairing.CreatedBy))
	if err != nil {
		s.fail(w, err)
		return
	}
	s.keepAgentToken(ctx, created.ID, token)
	if sel, err := s.Store.PairingCollect(ctx, pairing.ID); err == nil && !sel.IsZero() {
		if err := s.Store.SetAgentCollect(ctx, created.ID, sel); err != nil {
			s.Log.Errorf("apply the chosen readings to agent %d: %v", created.ID, err)
		}
		created.Collect = sel
	}
	if err := s.Store.AttachPairingAgent(ctx, pairing.ID, created.ID); err != nil {
		s.Log.Errorf("attach pairing agent: %v", err)
	}
	// What the machine says it is goes on the record as its own claim, exactly
	// as it does for a reading: GWatch did not go and look any of this up.
	hostname := trimTo(body.Hostname, 200)
	if err := s.Store.SetAgentIdentity(ctx, created.ID, hostname, trimTo(body.OS, 60), trimTo(body.Arch, 30)); err != nil {
		s.Log.Errorf("record agent identity: %v", err)
	}
	created.Hostname, created.OS, created.Arch = hostname, trimTo(body.OS, 60), trimTo(body.Arch, 30)

	if pairing.NodeID != nil {
		if err := s.bindMachineCheck(ctx, *pairing.NodeID, created); err != nil {
			s.Log.Errorf("bind hardware check to agent %d: %v", created.ID, err)
		}
	}

	s.auditAuth(ctx, "Machine paired: "+created.Name,
		fmt.Sprintf("A pairing code was redeemed from %s by %s. The machine may submit its own hardware readings and nothing else.",
			ip, describeMachine(hostname, body.OS, body.Arch, body.Version)))
	writeJSON(w, http.StatusCreated, map[string]any{
		"token":           token,
		"agent":           created,
		"intervalSeconds": int(hostmon.DefaultSampleInterval.Seconds()),
	})
}

// keepAgentToken stores a sealed copy of a freshly minted token, which is what
// lets an administrator read it back later. Failing to is not worth failing
// the registration over: the machine has its token and works, and the only
// loss is that the token cannot be viewed until it is rotated.
func (s *Server) keepAgentToken(ctx context.Context, id int64, token string) {
	if err := s.Store.SetAgentToken(ctx, id, token); err != nil {
		s.Log.Errorf("keep a viewable copy of the token for agent %d: %v", id, err)
	}
}

// rejectPairing answers every unusable code identically and records the reason
// where only an administrator can read it.
func (s *Server) rejectPairing(ctx context.Context, w http.ResponseWriter, ip, detail string) {
	s.auditAuthFailure(ctx, "Pairing rejected", detail+" It came from "+ip+".", ip)
	writeError(w, http.StatusUnauthorized, pairingRejected)
}

// describeMachine names the machine in the audit entry the way it described
// itself, falling back to something honest when it said nothing at all.
func describeMachine(hostname, os, arch, version string) string {
	parts := []string{}
	for _, p := range []string{hostname, os, arch} {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	if v := strings.TrimSpace(version); v != "" {
		parts = append(parts, "agent "+v)
	}
	if len(parts) == 0 {
		return "a machine that did not describe itself"
	}
	return strings.Join(parts, "/")
}

// createMachineNode makes the node a machine is watched as. It carries one
// hardware check whose agent is filled in by bindMachineCheck once the machine
// has actually paired; until then the check has nothing to read and stays
// disabled rather than failing every minute against an agent that may never
// arrive.
func (s *Server) createMachineNode(ctx context.Context, name string) (model.Node, error) {
	node := model.Node{
		Name:       name,
		Group:      machineGroup,
		Tags:       []string{},
		Importance: model.ImportanceNormal,
		Enabled:    true,
		Template:   "agent-machine",
		Checks:     []model.Check{machineCheck(false)},
	}
	created, err := s.Store.CreateNode(ctx, node)
	if err != nil {
		return created, err
	}
	s.configChanged(ctx, &created, "Added node "+created.Name, "Created for a machine being paired; its hardware check starts once the machine reports.")
	return created, nil
}

// bindMachineCheck points the node's hardware check at the agent that has just
// been enrolled, and switches it on. A check that is waiting for an agent (the
// one made with the node, or one left unbound) is completed. A node an
// administrator chose themselves usually has no such check, so one is added:
// choosing "add the machine to this node" has to end with the node watching the
// machine. A check that already reads this agent, or one that reads somewhere
// else, is left as it is.
func (s *Server) bindMachineCheck(ctx context.Context, nodeID int64, agent model.Agent) error {
	node, err := s.Store.GetNode(ctx, nodeID)
	if err != nil {
		return err
	}
	changed := false
	reads := false
	for i, c := range node.Checks {
		if c.Type != model.CheckSystem || c.Config.HostSource != model.HostSourceAgent {
			continue
		}
		if c.Config.AgentID == agent.ID {
			reads = true
			continue
		}
		if c.Config.AgentID != 0 || changed {
			continue
		}
		node.Checks[i].Config.AgentID = agent.ID
		node.Checks[i].Enabled = true
		changed = true
	}
	if !changed && !reads {
		check := machineCheck(true)
		check.Config.AgentID = agent.ID
		node.Checks = append(node.Checks, check)
		changed = true
	}
	if !changed {
		return nil
	}
	updated, _, err := s.Store.UpdateNode(ctx, node)
	if err != nil {
		return err
	}
	s.configChanged(ctx, &updated, "Machine "+agent.Name+" attached to node "+updated.Name, "Its hardware check now reads the readings this machine sends.")
	return nil
}

// machineCheck is the hardware check a machine is watched by. It starts
// switched off when there is no machine to read yet.
func machineCheck(enabled bool) model.Check {
	cfg := model.SystemDefaults()
	cfg.HostSource = model.HostSourceAgent
	return model.Check{
		Type:             model.CheckSystem,
		Name:             "Hardware health",
		Enabled:          enabled,
		IntervalSeconds:  60,
		TimeoutSeconds:   10,
		Retries:          1,
		FailureThreshold: 2,
		Config:           cfg,
	}
}

// machineGroup is the group a node created for a paired machine lands in, so
// the machines of a network sort together in the node list.
const machineGroup = "Hardware"

// trimTo bounds a string the machine sent about itself. None of these fields
// are trusted for anything, but they are shown in the UI and written to the
// event log, so their length is ours to decide rather than the caller's.
func trimTo(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) > max {
		return s[:max]
	}
	return s
}

// ---- the ingest endpoint ----

// handleIngestMetrics accepts a reading pushed by a registered machine.
//
// This route sits outside the normal authorization table on purpose. It is
// reached with an agent token and nothing else: the token identifies exactly
// one machine, the reading is filed under that machine whatever the payload
// claims, and the route can do nothing else. An agent therefore never holds a
// credential that could read or change anything in GWatch, which is the point
// of installing one on a machine you would rather not hand over.
func (s *Server) handleIngestMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "hardware readings are submitted with POST")
		return
	}
	if s.Engine == nil {
		writeError(w, http.StatusServiceUnavailable, "the monitoring engine is not running")
		return
	}

	agent, _, ok := s.authenticateAgent(w, r, "Hardware reading rejected")
	if !ok {
		return
	}

	var metrics model.HostMetrics
	if err := json.NewDecoder(io.LimitReader(r.Body, maxIngestBody)).Decode(&metrics); err != nil {
		writeError(w, http.StatusBadRequest, "the body is not a GWatch hardware reading")
		return
	}
	// A reading the agent held through an outage is worth having only at the
	// time it was taken, so one whose time cannot be believed is refused (422,
	// which the agent takes as "drop it", not "try again") rather than filed as
	// current. See hostmon.CheckHeldTimestamp.
	if heldReading(r) {
		if err := hostmon.CheckHeldTimestamp(metrics.Timestamp, time.Now()); err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
	}
	stored, err := s.Engine.IngestHostReading(ctx, agent, metrics, s.remoteAddr(r))
	if err != nil {
		s.fail(w, err)
		return
	}
	// The interval is echoed so an agent does not need its own configuration
	// to match how often GWatch wants to hear from it.
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted":        true,
		"ts":              stored.Timestamp,
		"intervalSeconds": int(hostmon.DefaultSampleInterval.Seconds()),
	})
}

// heldReading is whether the agent marked a reading as delivered late, after
// holding it through an outage (the X-GWatch-Held header). An agent that does
// not send the mark — an older one, or a curl one-liner — is treated as
// reporting live, and its timestamp is settled the way it always was.
func heldReading(r *http.Request) bool {
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("X-GWatch-Held"))) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// bearerToken reads the credential from an Authorization header, accepting the
// bare token as well so a curl one-liner works.
func bearerToken(r *http.Request) string {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if h == "" {
		return strings.TrimSpace(r.Header.Get("X-GWatch-Agent-Token"))
	}
	if rest, ok := strings.CutPrefix(h, "Bearer "); ok {
		return strings.TrimSpace(rest)
	}
	return h
}

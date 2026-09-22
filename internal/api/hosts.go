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
)

// maxIngestBody caps a pushed reading. A hardware reading is a few kilobytes;
// the limit is what keeps a machine holding a valid token from using it to
// push megabytes at the database.
const maxIngestBody = 256 << 10

// agentStaleAfter is how long a machine may go quiet before the hardware list
// marks it as not reporting. It is a display default; a check that watches the
// machine has its own, configurable, staleness limit.
const agentStaleAfter = 5 * time.Minute

// maxHostChartPoints bounds a history response. Readings are stored as taken,
// which over a year is far more points than a chart can draw, so the handler
// averages them into buckets rather than sending everything.
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

// handleHostHistory returns the numeric series for one machine over a range.
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
	samples, err := s.Store.HostSamples(ctx, key, from, now, 0)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"key":     key,
		"range":   rng.Name,
		"from":    from,
		"to":      now,
		"samples": downsampleHostSamples(samples, maxHostChartPoints),
	})
}

// hostSummaries builds the hardware list: this computer first, then every
// registered machine, each with its newest reading.
func (s *Server) hostSummaries(r *http.Request) ([]model.HostSummary, error) {
	ctx := r.Context()
	latest, err := s.Store.LatestHostSamples(ctx)
	if err != nil {
		return nil, err
	}
	agents, err := s.Store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	nodeNames, err := s.nodeNames(r)
	if err != nil {
		return nil, err
	}

	out := make([]model.HostSummary, 0, len(agents)+1)
	out = append(out, hostSummary(model.HostSummary{
		Key:    model.HostKeyLocal,
		Name:   s.localHostName(ctx, latest),
		Source: model.HostSourceLocal,
	}, latest[model.HostKeyLocal]))

	for _, a := range agents {
		agent := a
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
	for key, sample := range latest {
		if key == model.HostKeyLocal || strings.HasPrefix(key, "agent:") {
			continue
		}
		name := sample.Metrics.Hostname
		if name == "" {
			name = key
		}
		scraped = append(scraped, hostSummary(model.HostSummary{
			Key: key, Name: name, Source: model.HostSourceURL,
		}, sample))
	}
	sort.Slice(scraped, func(i, j int) bool { return scraped[i].Name < scraped[j].Name })
	return append(out, scraped...), nil
}

// hostSummary fills in the reading and the derived status for one machine.
func hostSummary(summary model.HostSummary, sample model.HostSample) model.HostSummary {
	if summary.Agent != nil && (summary.Agent.Revoked() || !summary.Agent.Enabled) {
		summary.Status = model.StatusPaused
	} else {
		summary.Status = model.StatusUnknown
	}
	if sample.Timestamp.IsZero() {
		return summary
	}
	metrics := sample.Metrics
	summary.Metrics = &metrics
	summary.Warnings = metrics.Warnings
	if summary.Status == model.StatusPaused {
		return summary
	}
	// The list's own idea of health is only "is this machine still talking to
	// us". What counts as too hot or too full is a check's business, because
	// only a check knows the thresholds someone chose.
	summary.Stale = time.Since(sample.Timestamp) > agentStaleAfter
	if summary.Stale {
		summary.Status = model.StatusDown
	} else {
		summary.Status = model.StatusUp
	}
	return summary
}

// localHostName names this computer from its newest reading, falling back to
// the instance name so the row is never blank on a fresh install.
func (s *Server) localHostName(ctx context.Context, latest map[string]model.HostSample) string {
	if sample, ok := latest[model.HostKeyLocal]; ok && sample.Metrics.Hostname != "" {
		return sample.Metrics.Hostname
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

// downsampleHostSamples averages readings into at most limit buckets. A year
// of minute-by-minute readings is half a million points; a chart can draw a
// few hundred, and averaging is what keeps a spike visible rather than
// dropping whichever readings happen to fall between the points kept.
func downsampleHostSamples(samples []model.HostSample, limit int) []model.HostSample {
	if limit <= 0 || len(samples) <= limit {
		return samples
	}
	out := make([]model.HostSample, 0, limit)
	per := float64(len(samples)) / float64(limit)
	for i := 0; i < limit; i++ {
		start := int(float64(i) * per)
		end := int(float64(i+1) * per)
		if end > len(samples) {
			end = len(samples)
		}
		if start >= end {
			continue
		}
		out = append(out, averageHostSamples(samples[start:end]))
	}
	return out
}

// averageHostSamples reduces a bucket to one point, timestamped at the middle
// of the bucket. A metric missing from every reading in the bucket stays
// missing rather than being averaged into a zero.
func averageHostSamples(bucket []model.HostSample) model.HostSample {
	out := model.HostSample{
		Key:       bucket[0].Key,
		Timestamp: bucket[len(bucket)/2].Timestamp,
	}
	fields := []struct {
		in  func(model.HostSample) *float64
		out **float64
	}{
		{func(s model.HostSample) *float64 { return s.CPUPct }, &out.CPUPct},
		{func(s model.HostSample) *float64 { return s.MemPct }, &out.MemPct},
		{func(s model.HostSample) *float64 { return s.SwapPct }, &out.SwapPct},
		{func(s model.HostSample) *float64 { return s.DiskPct }, &out.DiskPct},
		{func(s model.HostSample) *float64 { return s.LoadPerCore }, &out.LoadPerCore},
		{func(s model.HostSample) *float64 { return s.NetRxBytesSec }, &out.NetRxBytesSec},
		{func(s model.HostSample) *float64 { return s.NetTxBytesSec }, &out.NetTxBytesSec},
		{func(s model.HostSample) *float64 { return s.DiskReadBytes }, &out.DiskReadBytes},
		{func(s model.HostSample) *float64 { return s.DiskWriteBytes }, &out.DiskWriteBytes},
	}
	for _, f := range fields {
		var sum float64
		var n int
		for _, sample := range bucket {
			if v := f.in(sample); v != nil {
				sum += *v
				n++
			}
		}
		if n > 0 {
			avg := sum / float64(n)
			*f.out = &avg
		}
	}
	return out
}

// ---- registered machines ----

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := s.Store.ListAgents(r.Context())
	if err != nil {
		s.fail(w, err)
		return
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
		Name    string `json:"name"`
		NodeID  *int64 `json:"nodeId"`
		Enabled *bool  `json:"enabled"`
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
	if body.NodeID != nil {
		if _, err := s.Store.GetNode(ctx, *body.NodeID); err != nil {
			writeError(w, http.StatusBadRequest, "the node this machine belongs to does not exist")
			return
		}
	}
	updated, err := s.Store.UpdateAgent(ctx, id, name, body.NodeID, enabled)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !enabled {
		s.forgetHost(updated.HostKey())
	}
	writeJSON(w, http.StatusOK, updated)
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
		s.auditAuth(ctx, "Hardware agent deleted: "+agent.Name, "Its stored readings were deleted with it.")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.Store.RevokeAgent(ctx, id); err != nil {
		s.fail(w, err)
		return
	}
	s.forgetHost(agent.HostKey())
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
const pairingRejected = "that pairing code is not valid; ask for a fresh one in GWatch under Hardware"

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
	s.auditAuth(ctx, "Pairing code created for "+created.Name,
		fmt.Sprintf("Whoever types it within %s enrols one machine, which may then submit that machine's hardware readings and nothing else.",
			pairingCodeLifetime))
	writeJSON(w, http.StatusCreated, map[string]any{"code": code, "pairing": created})
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
	if allowed, wait := s.failLimiter.Allow(ip); !allowed {
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
	s.failLimiter.Reset(ip)

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
	cfg := model.SystemDefaults()
	cfg.HostSource = model.HostSourceAgent
	node := model.Node{
		Name:       name,
		Group:      machineGroup,
		Tags:       []string{},
		Importance: model.ImportanceNormal,
		Enabled:    true,
		Template:   "agent-machine",
		Checks: []model.Check{{
			Type:             model.CheckSystem,
			Name:             "Hardware health",
			Enabled:          false,
			IntervalSeconds:  60,
			TimeoutSeconds:   10,
			Retries:          1,
			FailureThreshold: 2,
			Config:           cfg,
		}},
	}
	created, err := s.Store.CreateNode(ctx, node)
	if err != nil {
		return created, err
	}
	s.configChanged(ctx, &created, "Added node "+created.Name, "Created for a machine being paired; its hardware check starts once the machine reports.")
	return created, nil
}

// bindMachineCheck points the node's hardware check at the agent that has just
// been enrolled, and switches it on. A node the administrator chose themselves
// may already have a hardware check for this machine, or none at all; both are
// left as they are, since only a check that is waiting for an agent is ours to
// complete.
func (s *Server) bindMachineCheck(ctx context.Context, nodeID int64, agent model.Agent) error {
	node, err := s.Store.GetNode(ctx, nodeID)
	if err != nil {
		return err
	}
	changed := false
	for i, c := range node.Checks {
		if c.Type != model.CheckSystem || c.Config.HostSource != model.HostSourceAgent || c.Config.AgentID != 0 {
			continue
		}
		node.Checks[i].Config.AgentID = agent.ID
		node.Checks[i].Enabled = true
		changed = true
		break
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

	token := bearerToken(r)
	if token == "" {
		writeError(w, http.StatusUnauthorized, "an agent token is required")
		return
	}
	// A token is a credential, so a wrong one counts against the same failure
	// budget as a wrong password. A correct one gives its budget back, so an
	// agent reporting every minute never exhausts it.
	ip := s.clientIP(r)
	if allowed, wait := s.failLimiter.Allow(ip); !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds()+0.999)))
		writeError(w, http.StatusTooManyRequests, "too many failed attempts; try again shortly")
		return
	}
	agent, err := s.Store.AgentByTokenHash(ctx, auth.HashToken(token))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.auditAuthFailure(ctx, "Hardware reading rejected",
				fmt.Sprintf("An unknown or revoked agent token was presented from %s.", ip), ip)
			writeError(w, http.StatusUnauthorized, "this agent token is not registered, or has been revoked")
			return
		}
		s.fail(w, err)
		return
	}
	s.failLimiter.Reset(ip)

	var metrics model.HostMetrics
	if err := json.NewDecoder(io.LimitReader(r.Body, maxIngestBody)).Decode(&metrics); err != nil {
		writeError(w, http.StatusBadRequest, "the body is not a GWatch hardware reading")
		return
	}
	stored, err := s.Engine.Hosts().Ingest(ctx, agent, metrics, s.remoteAddr(r))
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

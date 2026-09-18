package api

import (
	"context"
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
		writeError(w, http.StatusBadRequest, err.Error())
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
		writeError(w, http.StatusBadRequest, err.Error())
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

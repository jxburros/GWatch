package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
)

// Remote collectors (issue #123): the routes a collector talks to and the one
// an administrator uses to make an agent one. What a collector is and why it
// is shaped the way it is are in internal/model/collector.go and
// docs/AGENT-DECISIONS.md, entry 16.

// maxResultsBody bounds a batch of results. A result is well under a
// kilobyte, more for an SNMP check with many OIDs; MaxCollectorBatch of them
// fits easily.
const maxResultsBody = 4 << 20

// handleIngestResults accepts a batch of check results from a collector.
//
// Like /ingest/metrics it sits outside the authorization table and is reached
// with an agent token and nothing else, and it can do one thing: file results
// for the checks this agent is currently assigned. A result for any other
// check is refused one by one (the engine checks each against the node's
// "check from"), so a collector can never write into a check it was not
// given, and an agent that is not a collector — either half of the agreement
// missing — is refused outright.
func (s *Server) handleIngestResults(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "check results are submitted with POST")
		return
	}
	if s.Engine == nil {
		writeError(w, http.StatusServiceUnavailable, "the monitoring engine is not running")
		return
	}
	agent, _, ok := s.authenticateAgent(w, r, "Collector results rejected")
	if !ok {
		return
	}
	if !agent.Collector {
		writeError(w, http.StatusForbidden, "collecting is turned off for this machine in GWatch")
		return
	}
	if !agent.CollectorAllowed {
		writeError(w, http.StatusForbidden, "this machine has not said it allows collecting; it says so at its next check-in")
		return
	}
	var batch model.CollectorBatch
	if err := json.NewDecoder(io.LimitReader(r.Body, maxResultsBody)).Decode(&batch); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "the body is not a batch of GWatch check results")
		return
	}
	if len(batch.Results) > model.MaxCollectorBatch {
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("at most %d results per batch", model.MaxCollectorBatch))
		return
	}
	reply, err := s.Engine.IngestCollectorResults(ctx, agent, batch)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, reply)
}

// collectorAssignment is the collector part of a check-in answer: the checks
// to run, but only when both halves of the agreement are there. An agent that
// has not said its own configuration allows collecting is told nothing — in
// particular none of the credentials some of those checks carry.
func (s *Server) collectorAssignment(agent model.Agent, allowed bool) *model.CollectorAssignment {
	if !agent.Collector || !allowed || !agent.Enabled || agent.Revoked() {
		return nil
	}
	a := s.Engine.CollectorAssignment(agent.ID)
	return &a
}

// handleSetAgentCollector turns an agent's collecting on or off: the
// administrator's half of the agreement. The machine's half is its own
// agent.json; the agent card shows both.
func (s *Server) handleSetAgentCollector(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeDecodeError(w, err)
		return
	}
	agent, err := s.Store.GetAgent(ctx, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if body.Enabled && agent.Revoked() {
		writeError(w, http.StatusConflict, "this machine has been revoked; it cannot collect")
		return
	}
	if err := s.Store.SetAgentCollector(ctx, id, body.Enabled); err != nil {
		s.fail(w, err)
		return
	}
	if body.Enabled {
		s.auditAuth(ctx, "Remote collecting turned on for "+agent.Name,
			"Nodes can now be checked from this machine. It runs them only if its own agent.json also says \"collector\": true, and it is given the checks (with any SNMP community or HTTP headers they carry) only then.")
	} else {
		s.auditAuth(ctx, "Remote collecting turned off for "+agent.Name,
			"The machine stops running checks at its next check-in. Nodes that check from it show their checks as unknown until they are moved.")
	}
	s.reloadCollectors(ctx)
	updated, err := s.Store.GetAgent(ctx, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.withCollectorState(updated))
}

// reloadCollectors tells the engine the agents have changed.
func (s *Server) reloadCollectors(ctx context.Context) {
	if s.Engine == nil {
		return
	}
	if err := s.Engine.ReloadCollectors(context.WithoutCancel(ctx)); err != nil {
		s.Log.Errorf("reload collectors: %v", err)
	}
}

// withCollectorState fills in what only the engine knows about an agent as a
// collector: whether results are arriving, and how many checks it runs.
func (s *Server) withCollectorState(a model.Agent) model.Agent {
	if s.Engine != nil {
		a.CollectorStatus, a.CollectorChecks = s.Engine.CollectorState(a)
	}
	return a
}

// checkCollector refuses a node whose "check from" names an agent that does
// not exist or has been revoked. An agent whose collecting is off is allowed,
// so the node can be set up first, and shows on the node as unknown with the
// reason; refusing it would make an unrelated edit to the node fail later on,
// when someone turns collecting off.
func (s *Server) checkCollector(n *model.Node) error {
	if n.CollectorID == nil || *n.CollectorID <= 0 {
		n.CollectorID = nil
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a, err := s.Store.GetAgent(ctx, *n.CollectorID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("the collector this node is to be checked from (agent %d) does not exist", *n.CollectorID)
	case err != nil:
		return err
	case a.Revoked():
		return fmt.Errorf("%s has been revoked, so it cannot run this node's checks", a.Name)
	}
	return nil
}

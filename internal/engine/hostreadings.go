package engine

// Hardware readings and the checks that judge them.
//
// A machine is a node with a hardware check, and what the check measures is
// recorded like what any other check measures. For a machine GWatch reads (this
// computer, a metrics URL) that is the ordinary scheduled run. A machine that
// pushes its readings is the other way round — the reading arrives when the
// machine decides — so the check is judged when it does, here, and its schedule
// is kept only to notice when readings stop (see tick).
//
// Nothing about a reading is stored apart from the result of judging it.

import (
	"context"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/checks"
	"github.com/jxburros/GWatch/internal/model"
)

// IngestHostReading accepts a reading pushed by an agent and has the machine's
// hardware check judge it.
//
// A reading that is current goes through the check's whole machinery — state,
// incidents, alerts — exactly as if the check had run and taken it. A reading
// that is not (one the agent held back through an outage and has just delivered)
// is added to the check's history at the time it was taken, and nothing else:
// the machine being back is the news, not a verdict on what it measured while
// it could not be reached, and an alert for a disk that was full yesterday would
// only be noise.
func (e *Engine) IngestHostReading(ctx context.Context, agent model.Agent, metrics model.HostMetrics, remoteAddr string) (model.HostMetrics, error) {
	stored, err := e.hosts.Accept(ctx, agent, metrics, remoteAddr)
	if err != nil {
		return model.HostMetrics{}, err
	}
	// While a restore runs (pause.go) the reading is kept as the machine's
	// latest, for the hardware views, but not judged or recorded: the checks
	// it would be recorded against are being replaced. The agent sends
	// another within its interval.
	if !e.enterWork() {
		return stored, nil
	}
	defer e.leaveWork()
	type target struct {
		check model.Check
		node  model.Node
		last  *time.Time
	}
	var targets []target
	e.mu.Lock()
	for _, c := range e.checks {
		if !c.Enabled || !checks.PushesReadings(c) || c.Config.AgentID != agent.ID {
			continue
		}
		n, ok := e.nodes[c.NodeID]
		if !ok || !n.Enabled {
			continue
		}
		var last *time.Time
		if st := e.states[c.ID]; st != nil {
			last = st.LastRunAt
		}
		targets = append(targets, target{c, n, last})
	}
	e.mu.Unlock()
	for _, t := range targets {
		e.judgeReading(ctx, t.check, t.node, t.last, stored)
	}
	return stored, nil
}

// judgeReading has one check judge one pushed reading.
func (e *Engine) judgeReading(ctx context.Context, c model.Check, n model.Node, last *time.Time, m model.HostMetrics) {
	interval := time.Duration(c.IntervalSeconds) * time.Second
	// Results are recorded about as often as the check's interval, whatever
	// the machine's own: one that reports every ten seconds does not turn a
	// once-a-minute check into six results a minute.
	spacing := interval * 4 / 5

	res := checks.EvaluateHost(c, m, m.Timestamp)
	res.CheckID = c.ID

	if time.Since(m.Timestamp) <= checks.StaleAfter(c) {
		if last != nil && !m.Timestamp.Before(*last) && m.Timestamp.Sub(*last) < spacing {
			return
		}
		if _, err := e.process(ctx, c, n, res, 0); err != nil {
			e.RecordError("judge a pushed hardware reading", err)
		}
		return
	}
	near, err := e.store.CountResultsNear(ctx, c.ID, m.Timestamp, spacing)
	if err != nil {
		e.RecordError("judge a held-back hardware reading", err)
		return
	}
	if near > 0 {
		return
	}
	if _, err := e.store.InsertResult(ctx, res); err != nil {
		e.RecordError("record a held-back hardware reading", err)
	}
}

// hostReadingFreshLocked reports whether a push-fed check has a reading recent
// enough that it should not be run on its schedule. The caller holds e.mu.
func (e *Engine) hostReadingFreshLocked(c model.Check, now time.Time) bool {
	m, ok := e.hosts.Latest(model.AgentHostKey(c.Config.AgentID))
	return ok && now.Sub(m.Timestamp) <= checks.StaleAfter(c)
}

// hostChecks lists the hardware checks that watch the machine behind a host key.
func (e *Engine) hostChecks(key string) []int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	var ids []int64
	for id, c := range e.checks {
		if c.Type != model.CheckSystem {
			continue
		}
		switch {
		case key == model.HostKeyLocal:
			if c.Config.HostSource == model.HostSourceLocal || !c.Config.HostSource.Valid() {
				ids = append(ids, id)
			}
		case strings.HasPrefix(key, "agent:"):
			if c.Config.HostSource == model.HostSourceAgent && c.Config.AgentID > 0 && model.AgentHostKey(c.Config.AgentID) == key {
				ids = append(ids, id)
			}
		case strings.HasPrefix(key, "url:"):
			if c.Config.HostSource == model.HostSourceURL && model.URLHostKey(id) == key {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// HostChecks is hostChecks for the API: the checks whose results are a
// machine's history.
func (e *Engine) HostChecks(key string) []int64 { return e.hostChecks(key) }

// restoreHost finds a machine's newest reading in what its checks recorded.
func (e *Engine) restoreHost(ctx context.Context, key string) (model.HostMetrics, bool) {
	ids := e.hostChecks(key)
	if len(ids) == 0 {
		return model.HostMetrics{}, false
	}
	m, ok, err := e.store.LatestHostFromResults(ctx, ids)
	if err != nil {
		e.RecordError("find the newest hardware reading", err)
		return model.HostMetrics{}, false
	}
	return m, ok
}

// RestoreHost is restoreHost for the API.
func (e *Engine) RestoreHost(ctx context.Context, key string) (model.HostMetrics, bool) {
	return e.restoreHost(ctx, key)
}

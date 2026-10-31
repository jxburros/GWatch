package engine

// Remote collectors (issue #123): checks this server does not run itself.
//
// A node can be checked from a paired agent instead of from here — a second
// office, a parent's house — and then that node's network checks are run by
// the agent, on their own intervals, and the results arrive over the agent's
// own connection (IngestCollectorResults). The engine's part is small and
// deliberately the same as for every other check: such a check is left out of
// the schedule, and each result it receives goes through process exactly as if
// the check had run here, so its state, its incidents, its alerts, the rules
// that watch it, maintenance windows and silences all behave as they do for a
// check run locally. The engine never contacts a collector; it has no address
// for one. What it adds is noticing when a collector goes quiet: then the
// collector's checks are marked unknown — nobody knows whether those devices
// are up — and one event and one alert say the collector is unreachable,
// rather than every check behind it raising its own (the same reasoning as a
// parent dependency, applied to the thing doing the checking).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/hostmon"
	"github.com/jxburros/GWatch/internal/model"
)

// ErrCollected is returned when something asks this server to run a check
// that a remote collector runs. The point of a collector is that this server
// may not be able to reach the device at all, so running it here would
// record a failure that says nothing about the device.
var ErrCollected = errors.New("this check runs on a remote collector, on its own schedule; it cannot be run from this server")

// suppressCollector is the SuppressReason of a check whose collector has gone
// quiet: its status is unknown, and the reason says why.
const suppressCollector = "collector"

// collectorWatch is what the engine remembers about one collector between
// results.
type collectorWatch struct {
	lastSeen  time.Time // last batch (results or a heartbeat)
	persisted time.Time // last time lastSeen was written to the store
	down      bool      // the unreachable event has been recorded
}

// collectorTouchEvery bounds how often a collector's last-seen time is
// written to the database. A collector posts every few seconds while it has
// results; the agent card needs the time to the minute.
const collectorTouchEvery = 30 * time.Second

// collectedLocked reports which collector runs check c, if one does. The
// caller holds e.mu.
func (e *Engine) collectedLocked(c model.Check) (int64, bool) {
	n, ok := e.nodes[c.NodeID]
	if !ok {
		return 0, false
	}
	return n.CollectorFor(c)
}

// CollectorFor is collectedLocked for callers outside the engine.
func (e *Engine) CollectorFor(checkID int64) (int64, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.checks[checkID]
	if !ok {
		return 0, false
	}
	return e.collectedLocked(c)
}

// CollectorAssignment is what a collector is told to run: every enabled check
// of a collectable type on an enabled node that checks from it, with the
// target resolved and only the settings its type reads. The caller decides
// whether the agent is to be told at all (both sides have to have agreed).
func (e *Engine) CollectorAssignment(agentID int64) model.CollectorAssignment {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := model.CollectorAssignment{
		Checks: []model.CollectorCheck{},
		Options: model.CollectorOptions{
			DefaultCertWarn:   e.settings.Alerts.CertWarnDays,
			LatencyWarnMS:     e.settings.General.LatencyWarnMS,
			PacketLossWarnPct: e.settings.General.PacketLossWarnPct,
			PingMethod:        e.settings.General.PingMethod,
		},
	}
	for _, c := range e.assignedLocked(agentID) {
		if len(out.Checks) == model.MaxCollectorChecks {
			break
		}
		cc := model.NewCollectorCheck(c, e.nodes[c.NodeID].Host)
		// A ping check without a count of its own follows Settings ›
		// General; the count is written into what the collector is handed,
		// so an agent that predates the setting sends the same number.
		if cc.Type == model.CheckPing && cc.Config.PingCount <= 0 && e.settings.General.PingCount > 0 {
			cc.Config.PingCount = min(e.settings.General.PingCount, model.MaxPingCount)
		}
		out.Checks = append(out.Checks, cc)
	}
	return out
}

// assignedLocked lists the enabled checks a collector runs, in id order.
func (e *Engine) assignedLocked(agentID int64) []model.Check {
	var out []model.Check
	for _, c := range e.checks {
		n, ok := e.nodes[c.NodeID]
		if !ok || !n.Enabled || !c.Enabled {
			continue
		}
		if id, ok := n.CollectorFor(c); ok && id == agentID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// CollectorState is how a collector looks from here: one of the
// model.Collector* states and how many checks it runs. The agreement itself
// (both halves) is on the agent record; this adds what only the engine
// knows, which is whether results are arriving.
func (e *Engine) CollectorState(a model.Agent) (string, int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := len(e.assignedLocked(a.ID))
	switch {
	case !a.Collector:
		return model.CollectorOff, n
	case !a.CollectorAllowed || a.Revoked() || !a.Enabled:
		return model.CollectorWaiting, n
	case n == 0:
		return model.CollectorIdle, n
	}
	if w := e.collectors[a.ID]; w != nil && w.down {
		return model.CollectorUnreachable, n
	}
	return model.CollectorRunning, n
}

// collectorGrace is how long a collector may go without sending
// anything before it is called unreachable: a few of its shortest check
// interval, and never less than CollectorGraceMin.
func collectorGrace(checks []model.Check) time.Duration {
	shortest := time.Duration(0)
	for _, c := range checks {
		d := time.Duration(c.IntervalSeconds) * time.Second
		if d > 0 && (shortest == 0 || d < shortest) {
			shortest = d
		}
	}
	grace := shortest * model.CollectorMissedIntervals
	if grace < model.CollectorGraceMin {
		grace = model.CollectorGraceMin
	}
	return grace
}

// IngestCollectorResults accepts a batch of results from a collector.
//
// Each result has to be for a check this collector is currently assigned —
// an enabled check of a collectable type on an enabled node that checks from
// this agent — or it is refused, so a collector can never write into a check
// it was not given. A live result goes through process like a result taken
// here. A held one (kept by the collector through an outage and delivered
// late) is added to the history at the time it was taken and judges nothing,
// which is what a held hardware reading does: the collector being back is the
// news, and an alert for something that failed and recovered an hour ago
// would only be noise.
func (e *Engine) IngestCollectorResults(ctx context.Context, agent model.Agent, batch model.CollectorBatch) (model.CollectorBatchReply, error) {
	now := time.Now()
	var reply model.CollectorBatchReply
	// While a restore runs (pause.go) nothing is written: the batch is refused
	// with ErrPaused, which the API answers with a 503 the collector holds its
	// results through and retries. The collector is still remembered as alive,
	// in memory only, so a restore that takes a few minutes does not end with
	// every collector called unreachable.
	if !e.enterWork() {
		e.mu.Lock()
		if w := e.collectors[agent.ID]; w != nil {
			w.lastSeen = now
		} else {
			e.collectors[agent.ID] = &collectorWatch{lastSeen: now}
		}
		e.mu.Unlock()
		return reply, ErrPaused
	}
	defer e.leaveWork()
	e.noteCollectorSeen(ctx, agent, now)

	type item struct {
		check model.Check
		node  model.Node
		res   model.Result
		held  bool
	}
	var items []item
	e.mu.Lock()
	for _, in := range batch.Results {
		c, ok := e.checks[in.Result.CheckID]
		if !ok {
			reply.Rejected++
			continue
		}
		n, ok := e.nodes[c.NodeID]
		id, collected := n.CollectorFor(c)
		if !ok || !collected || id != agent.ID || !c.Enabled || !n.Enabled {
			reply.Rejected++
			continue
		}
		r := cleanCollectorResult(in.Result, c.ID)
		held := in.Held
		if !held {
			// A live result is filed when it arrived if the collector's clock
			// cannot be believed, the way a live hardware reading is; and one
			// that is older than what the check last recorded (batches can
			// cross) is history, not news.
			if r.Timestamp.IsZero() || r.Timestamp.After(now.Add(time.Minute)) || r.Timestamp.Before(now.Add(-5*time.Minute)) {
				r.Timestamp = now
			}
			if st := e.states[c.ID]; st != nil && st.LastRunAt != nil && r.Timestamp.Before(*st.LastRunAt) {
				held = true
			}
		}
		items = append(items, item{c, n, r, held})
	}
	e.mu.Unlock()

	for _, it := range items {
		if it.held {
			if err := hostmon.CheckHeldTimestamp(it.res.Timestamp, now); err != nil {
				reply.Rejected++
				continue
			}
			spacing := time.Duration(it.check.IntervalSeconds) * time.Second * 4 / 5
			near, err := e.store.CountResultsNear(ctx, it.check.ID, it.res.Timestamp, spacing)
			if err != nil {
				return reply, err
			}
			if near == 0 {
				if _, err := e.store.InsertResult(ctx, it.res); err != nil {
					return reply, err
				}
				e.noteHeldResult(it.res.Timestamp)
			}
			reply.Accepted++
			continue
		}
		if _, err := e.process(ctx, it.check, it.node, it.res, 0); err != nil {
			return reply, err
		}
		reply.Accepted++
	}
	return reply, nil
}

// cleanCollectorResult bounds a result that arrived from another machine.
// None of it is trusted for anything but display and judgement, but it is
// stored and shown, so its size is ours to decide.
func cleanCollectorResult(r model.Result, checkID int64) model.Result {
	r.ID = 0
	r.CheckID = checkID
	r.Message = clip(r.Message, 2000)
	r.Error = clip(r.Error, 2000)
	if len(r.Warnings) > 20 {
		r.Warnings = r.Warnings[:20]
	}
	for i := range r.Warnings {
		r.Warnings[i] = clip(r.Warnings[i], 500)
	}
	if len(r.Metrics) > 200 {
		trimmed := make(map[string]float64, 200)
		for k, v := range r.Metrics {
			if len(trimmed) == 200 {
				break
			}
			trimmed[k] = v
		}
		r.Metrics = trimmed
	}
	r.Details = cleanCollectorDetails(r.Details)
	if r.Attempts < 1 {
		r.Attempts = 1
	}
	switch {
	case !r.Success:
		r.Status = model.StatusDown
	case r.Status != model.StatusDegraded:
		r.Status = model.StatusUp
	}
	return r
}

// cleanCollectorDetails bounds a collected result's diagnostics the same way.
// They are stored with every result and sent with every node a viewer opens,
// and the batch limit alone would let one result carry megabytes. The parts
// only a check a collector never runs fills in (hardware, Matter, a custom
// check's output) are dropped.
func cleanCollectorDetails(d model.ResultDetails) model.ResultDetails {
	d.Host, d.HostAgeSec, d.MetricResults = nil, nil, nil
	d.Matter, d.MatterServer, d.Output = nil, "", ""
	d.FinalURL = clip(d.FinalURL, 2000)
	d.ContentHash = clip(d.ContentHash, 128)
	d.ContentValue = clip(d.ContentValue, 2000)
	d.JSONValue = clip(d.JSONValue, 2000)
	d.Resolver = clip(d.Resolver, 200)
	d.RemoteAddr = clip(d.RemoteAddr, 200)
	if len(d.RTTs) > 100 {
		d.RTTs = d.RTTs[:100]
	}
	d.ResolvedValues = clipList(d.ResolvedValues, 100, 500)
	if len(d.SNMP) > 200 {
		d.SNMP = d.SNMP[:200]
	}
	for i := range d.SNMP {
		v := &d.SNMP[i]
		v.OID, v.Name, v.Raw, v.Unit = clip(v.OID, 200), clip(v.Name, 200), clip(v.Raw, 500), clip(v.Unit, 50)
	}
	if d.Cert != nil {
		c := *d.Cert
		c.Subject, c.Issuer, c.Serial, c.Error = clip(c.Subject, 500), clip(c.Issuer, 500), clip(c.Serial, 200), clip(c.Error, 500)
		c.DNSNames = clipList(c.DNSNames, 100, 300)
		d.Cert = &c
	}
	return d
}

func clipList(in []string, n, each int) []string {
	if in == nil {
		return nil
	}
	if len(in) > n {
		in = in[:n]
	}
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = clip(v, each)
	}
	return out
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// noteCollectorSeen records that a collector sent something, and if it had
// been called unreachable, says it is back.
func (e *Engine) noteCollectorSeen(ctx context.Context, agent model.Agent, now time.Time) {
	e.mu.Lock()
	w := e.collectors[agent.ID]
	if w == nil {
		w = &collectorWatch{}
		e.collectors[agent.ID] = w
	}
	w.lastSeen = now
	wasDown := w.down
	w.down = false
	persist := now.Sub(w.persisted) >= collectorTouchEvery
	if persist {
		w.persisted = now
	}
	node, settings := e.collectorNodeLocked(agent), e.settings
	e.mu.Unlock()
	if persist {
		if err := e.store.TouchCollector(ctx, agent.ID, now); err != nil {
			e.log.Errorf("record collector contact: %v", err)
		}
	}
	if wasDown {
		e.recordEvent(model.Event{Type: model.EventCollectorUp, NodeID: nodeIDOf(node), NodeName: node.Name,
			Title:  "Collector " + agent.Name + " is back",
			Detail: "Results are arriving again. Its checks take up their own status as each one reports."})
		if settings.Alerts.Enabled && settings.Alerts.NotifyRecovery {
			e.sendMail(e.collectorMail("recovered", agent, node, now, "The collector is sending results again.", settings))
		}
		e.broadcast(Update{Kind: "state"})
	}
}

// collectorNodeLocked is the node an event about a collector is filed under:
// the machine's own node when it has one, otherwise a stand-in carrying the
// agent's name so the timeline and the e-mail still say which collector.
func (e *Engine) collectorNodeLocked(agent model.Agent) model.Node {
	if agent.NodeID != nil {
		if n, ok := e.nodes[*agent.NodeID]; ok {
			return n
		}
	}
	return model.Node{Name: agent.Name}
}

func nodeIDOf(n model.Node) *int64 {
	if n.ID == 0 {
		return nil
	}
	return ptrInt64(n.ID)
}

// collectorMail is the one alert about a collector. It goes through sendMail
// like every other alert, so the "also notify" channels get it too; the check
// it names is a stand-in with a negative id, which no state belongs to, so a
// failed delivery re-arms nothing.
func (e *Engine) collectorMail(kind string, agent model.Agent, node model.Node, now time.Time, message string, s model.Settings) mailTask {
	c := model.Check{ID: -agent.ID, NodeID: node.ID, Name: "Remote collector " + agent.Name}
	r := model.Result{Timestamp: now, Message: message}
	st := &model.CheckState{Status: model.StatusDown}
	if kind == "recovered" {
		st.Status = model.StatusUp
		r.Success = true
	}
	m := e.newMail(kind, c, node, r, st, s)
	m.details["Check"] = c.Name
	delete(m.details, "Target")
	return m
}

// watchCollectorsLocked notices collectors that have gone quiet. It runs on
// the scheduler's tick, holding e.mu, and returns the events and alerts to
// send once the lock is released.
func (e *Engine) watchCollectorsLocked(now time.Time) (events []model.Event, mails []mailTask) {
	byCollector := map[int64][]model.Check{}
	for _, c := range e.checks {
		n, ok := e.nodes[c.NodeID]
		if !ok || !n.Enabled || !c.Enabled {
			continue
		}
		if id, ok := n.CollectorFor(c); ok {
			byCollector[id] = append(byCollector[id], c)
		}
	}
	for id, list := range byCollector {
		w := e.collectors[id]
		if w == nil {
			// Never heard from since this server started: the start is when
			// the waiting began, so a restart does not call every collector
			// unreachable at once.
			since := e.startedAt
			if since.IsZero() {
				since = now
			}
			w = &collectorWatch{lastSeen: since}
			e.collectors[id] = w
		}
		if w.down || now.Sub(w.lastSeen) <= collectorGrace(list) {
			continue
		}
		w.down = true
		agent := e.collectorAgents[id]
		if agent.ID == 0 {
			agent = model.Agent{ID: id, Name: fmt.Sprintf("agent %d", id)}
		}
		why := fmt.Sprintf("Waiting for the remote collector %s, which has sent nothing since %s.", agent.Name, w.lastSeen.Format("2006-01-02 15:04"))
		switch {
		case e.collectorAgents[id].ID == 0:
			why = fmt.Sprintf("The remote collector this node checks from (agent %d) no longer exists; choose another under Check from.", id)
		case agent.Revoked() || !agent.Enabled:
			why = fmt.Sprintf("The remote collector %s has been revoked or disabled, so nothing runs these checks.", agent.Name)
		case !agent.Collector:
			why = fmt.Sprintf("Collecting is turned off for %s in GWatch, so nothing runs these checks.", agent.Name)
		case !agent.CollectorAllowed:
			why = fmt.Sprintf("%s has not allowed collecting in its own agent.json (\"collector\": true), so nothing runs these checks.", agent.Name)
		}
		var names []string
		for _, c := range list {
			st := e.states[c.ID]
			if st == nil {
				continue
			}
			st.Status = model.StatusUnknown
			st.SuppressReason = suppressCollector
			st.AlertSuppressed = true
			st.LastMessage = why
			if err := e.store.SaveState(context.Background(), *st); err != nil {
				e.log.Errorf("mark collector check unknown: %v", err)
			}
			n := e.nodes[c.NodeID]
			names = append(names, n.Name+" — "+c.Name)
		}
		node := e.collectorNodeLocked(agent)
		detail := fmt.Sprintf("%s %d check(s) are unknown until it reports again: %s.", why, len(list), strings.Join(firstN(names, 10), "; "))
		events = append(events, model.Event{Timestamp: now, Type: model.EventCollectorDown, NodeID: nodeIDOf(node), NodeName: node.Name,
			Title: "Collector " + agent.Name + " unreachable", Detail: detail,
			Meta: mustJSON(map[string]any{"agentId": id, "checks": len(list)})})
		if e.settings.Alerts.Enabled && !e.inMaintenanceLocked(node, now) {
			mails = append(mails, e.collectorMail("down", agent, node, now, detail, e.settings))
		}
	}
	return events, mails
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func firstN(s []string, n int) []string {
	if len(s) > n {
		return append(s[:n:n], fmt.Sprintf("and %d more", len(s)-n))
	}
	return s
}

// setCollectorAgentsLocked keeps the agents that nodes check from, so the
// watch can say why a collector is quiet. The caller holds e.mu.
func (e *Engine) setCollectorAgentsLocked(agents []model.Agent) {
	e.collectorAgents = map[int64]model.Agent{}
	for _, a := range agents {
		e.collectorAgents[a.ID] = a
	}
}

// ReloadCollectors re-reads the agents, after one is turned into a collector
// or back, revoked or renamed. It is cheaper than ReloadConfig and touches
// nothing else.
func (e *Engine) ReloadCollectors(ctx context.Context) error {
	agents, err := e.store.ListAgents(ctx)
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.setCollectorAgentsLocked(agents)
	e.mu.Unlock()
	return nil
}

// resumeFromCollector undoes what watchCollectorsLocked did to a
// check's state, as its first result after the quiet spell is processed: a
// check that was down before is down again — not newly down, which would send
// its alert a second time — and anything else starts from unknown and takes
// whatever this result says.
func resumeFromCollector(st *model.CheckState, threshold int) {
	if st.SuppressReason != suppressCollector {
		return
	}
	st.SuppressReason = ""
	st.AlertSuppressed = false
	if st.ConsecutiveFailures >= threshold {
		st.Status = model.StatusDown
	}
}

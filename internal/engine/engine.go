// Package engine is the monitoring core: it schedules checks, processes
// results, evaluates alert rules (thresholds, dependencies, maintenance,
// cooldowns), records the incident timeline and runs the retention jobs.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/jxburros/GWatch/internal/actions"
	"github.com/jxburros/GWatch/internal/checks"
	"github.com/jxburros/GWatch/internal/hostmon"
	"github.com/jxburros/GWatch/internal/logging"
	"github.com/jxburros/GWatch/internal/mailer"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
)

// RunFunc executes a check. It is a variable so tests can stub the runners.
type RunFunc func(ctx context.Context, check model.Check, opts checks.Options) model.Result

// SendFunc delivers an email. It is a variable so tests can capture mail.
type SendFunc func(ctx context.Context, settings model.SMTPSettings, msg mailer.Message) error

// Update is broadcast to API subscribers whenever something changes.
type Update struct {
	// Kind is one of: result | state | event | config | health | host |
	// maintenance | trigger | endpoint | discovery | rule.
	Kind    string `json:"kind"`
	CheckID int64  `json:"checkId,omitempty"`
	NodeID  int64  `json:"nodeId,omitempty"`
	// Discovery carries a subnet sweep's progress. A sweep is the one thing
	// the interface watches that has no check and no node behind it, so its
	// counters ride on the update rather than sending the browser back to the
	// API for them several times a second.
	Discovery *DiscoveryProgress `json:"discovery,omitempty"`
}

// DiscoveryProgress is how far a discovery run has got. The engine neither
// starts nor owns those runs — see internal/discovery — it only carries their
// progress to whoever is watching the stream.
type DiscoveryProgress struct {
	ID         string `json:"id"`
	State      string `json:"state"` // running | done | cancelled | failed
	Scanned    int    `json:"scanned"`
	Total      int    `json:"total"`
	Responders int    `json:"responders"`
}

// Options configure the engine.
type Options struct {
	Version     string
	ServiceMode string // "service" | "console"
	ListenAddr  string
	DataDir     string
	Run         RunFunc
	Send        SendFunc
}

// Engine is the monitoring core.
type Engine struct {
	store *store.Store
	log   *logging.Logger
	opts  Options

	reloadMu sync.Mutex
	mu       sync.Mutex
	settings model.Settings
	nodes    map[int64]model.Node
	checks   map[int64]model.Check
	states   map[int64]*model.CheckState
	mailDone map[int64]chan struct{}
	mailSem  chan struct{} // see mailSlots
	// alertGroups are the per-node alert groups whose window is open, and
	// digestMu serialises the warning digest; see alertbatch.go.
	alertGroups  map[groupKey]*alertGroup
	digestMu     sync.Mutex
	inflight     map[int64]*checkFlight
	recordResult func(context.Context, model.Result, model.CheckState) (model.Result, error)
	running      map[int64]bool
	windows      []model.MaintenanceWindow
	activeMW     map[int64]bool

	triggers    map[int64][]model.Trigger // by node id
	triggerLast map[int64]time.Time       // last run per trigger (cooldowns)
	runner      *actions.Runner

	// Notification rules (#31) and where each stands; see rules.go.
	rules       []model.Rule
	ruleStates  map[int64]*model.RuleState
	rulesLoaded bool
	rulesEvalMu sync.Mutex // serialises evaluateRules; taken before mu

	// hosts samples this computer's hardware and holds the newest reading
	// from every machine that reports in.
	hosts *hostmon.Monitor

	// Remote collectors (collectors.go): when each last sent anything, and
	// the agents nodes check from, by id.
	collectors      map[int64]*collectorWatch
	collectorAgents map[int64]model.Agent

	sem    chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
	// work is what alert e-mails, trigger actions and rule actions run
	// under. It outlives ctx, so an alert being sent as the service stops
	// still goes out, but Stop cancels it once the grace period is over.
	work       context.Context
	cancelWork context.CancelFunc
	wg         sync.WaitGroup
	started    bool
	stopping   bool

	startedAt    time.Time
	lastTick     time.Time
	lastGap      *model.GapInfo
	lastCheckAt  *time.Time
	lastOKAt     *time.Time
	lastAlertAt  *time.Time
	lastAlertErr string

	retention model.RetentionStatus
	backup    model.BackupStatus
	// heldSince is the oldest held-back result recorded since the last
	// rollup, which the next one rolls up from; see noteHeldResult.
	heldSince time.Time

	// gate keeps the engine's own writes out of the way of a restore; see
	// pause.go.
	gate workGate

	subMu sync.Mutex
	subs  map[chan Update]struct{}
}

// New creates an engine. Call Start to begin scheduling.
func New(st *store.Store, log *logging.Logger, opts Options) *Engine {
	if opts.Run == nil {
		opts.Run = checks.Run
	}
	if opts.Send == nil {
		opts.Send = mailer.Send
	}
	if opts.ServiceMode == "" {
		opts.ServiceMode = "console"
	}
	e := &Engine{
		store:        st,
		log:          log,
		opts:         opts,
		nodes:        map[int64]model.Node{},
		checks:       map[int64]model.Check{},
		states:       map[int64]*model.CheckState{},
		running:      map[int64]bool{},
		mailDone:     map[int64]chan struct{}{},
		mailSem:      make(chan struct{}, mailSlots),
		inflight:     map[int64]*checkFlight{},
		recordResult: st.RecordResult,
		activeMW:     map[int64]bool{},
		subs:         map[chan Update]struct{}{},

		triggers:    map[int64][]model.Trigger{},
		triggerLast: map[int64]time.Time{},

		collectors:      map[int64]*collectorWatch{},
		collectorAgents: map[int64]model.Agent{},
	}
	e.hosts = hostmon.New(st)
	e.hosts.OnSample = func(key string) { e.broadcast(Update{Kind: "host"}) }
	e.hosts.OnError = func(err error) { e.RecordError("hardware readings", err) }
	e.hosts.Restore = e.restoreHost
	return e
}

// Hosts exposes the hardware monitor, which the API uses to accept readings
// pushed by agents and to answer the hardware views.
func (e *Engine) Hosts() *hostmon.Monitor { return e.hosts }

// Store exposes the underlying store.
func (e *Engine) Store() *store.Store { return e.store }

// Log exposes the logger.
func (e *Engine) Log() *logging.Logger { return e.log }

// Start loads configuration and launches the background loops.
func (e *Engine) Start(parent context.Context) error {
	e.mu.Lock()
	if e.started {
		e.mu.Unlock()
		return nil
	}
	e.started = true
	e.startedAt = time.Now()
	e.lastTick = e.startedAt
	e.ctx, e.cancel = context.WithCancel(parent)
	e.work, e.cancelWork = context.WithCancel(context.WithoutCancel(parent))
	e.mu.Unlock()

	if err := e.ReloadConfig(e.ctx); err != nil {
		return err
	}
	_ = e.store.GetSetting(e.ctx, "backupStatus", &e.backup)
	_ = e.store.GetSetting(e.ctx, "retentionStatus", &e.retention)
	e.recordEvent(model.Event{Type: model.EventServiceStarted, Title: "Monitoring service started", Detail: fmt.Sprintf("GWatch %s on %s/%s (%s mode)", e.opts.Version, runtime.GOOS, runtime.GOARCH, e.opts.ServiceMode)})

	e.mu.Lock()
	e.log.Printf("engine started: %d node(s), %d check(s), max %d concurrent", len(e.nodes), len(e.checks), cap(e.sem))
	e.mu.Unlock()
	e.wg.Add(6)
	go e.schedulerLoop()
	go e.maintenanceLoop()
	go e.retentionLoop()
	go e.backupLoop()
	go e.reportLoop()
	go e.hostSampleLoop()
	return nil
}

// Stop halts the loops and waits for in-flight checks.
func (e *Engine) Stop() {
	e.mu.Lock()
	if !e.started || e.stopping {
		e.mu.Unlock()
		return
	}
	e.stopping = true
	e.mu.Unlock()
	e.recordEvent(model.Event{Type: model.EventServiceStopped, Title: "Monitoring service stopping"})
	e.cancel()
	// Alerts waiting in a grouping window go out now rather than when their
	// window closes, which may be minutes away; their e-mails then get the
	// same grace period as any other in-flight delivery.
	e.flushAlertGroups()
	// Stopping has an upper bound. A script action may run for ten minutes
	// and a hung custom check for ever, and the service manager (the Windows
	// SCM, systemd) kills a service that takes much longer than 20–90 s to
	// stop — possibly in the middle of a database write. In-flight actions
	// get a grace period to finish, are then cancelled, and are then left.
	done := make(chan struct{})
	go func() { e.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(stopGrace):
		e.log.Printf("engine stop: work still running after %s; cancelling it", stopGrace)
		e.cancelWork()
		select {
		case <-done:
		case <-time.After(stopAfterCancel):
			e.log.Printf("engine stop: giving up waiting for in-flight work after a further %s", stopAfterCancel)
			return
		}
	}
	e.cancelWork()
	e.log.Printf("engine stopped")
}

// stopGrace is how long Stop lets in-flight work finish before cancelling it,
// and stopAfterCancel how long it then waits for the cancellation to land.
// Together they stay inside the stop timeouts of the Windows SCM and systemd.
var (
	stopGrace       = 15 * time.Second
	stopAfterCancel = 5 * time.Second
)

// workContext is the context actions and alert e-mails run under: e.work once
// the engine has started, and a plain background context for an engine used
// without Start (as tests do).
func (e *Engine) workContext() context.Context {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.work == nil {
		return context.Background()
	}
	return e.work
}

// Running reports whether the scheduler is active.
func (e *Engine) Running() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.started && !e.stopping
}

// Settings returns a copy of the current settings.
func (e *Engine) Settings() model.Settings {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.settings
}

// ReloadConfig re-reads nodes, checks, settings and maintenance windows from
// the store, keeping in-memory states for checks that still exist.
func (e *Engine) ReloadConfig(ctx context.Context) error {
	// A committed save must reach the scheduler even if its HTTP client leaves.
	// Serialize the reads as well as the application so older snapshots cannot win.
	e.reloadMu.Lock()
	defer e.reloadMu.Unlock()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	settings, err := e.store.LoadSettings(ctx)
	if err != nil {
		return err
	}
	nodes, err := e.store.ListNodes(ctx)
	if err != nil {
		return err
	}
	states, err := e.store.ListStates(ctx)
	if err != nil {
		return err
	}
	windows, err := e.store.ListMaintenance(ctx)
	if err != nil {
		return err
	}
	agents, err := e.store.ListAgents(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	e.mu.Lock()
	e.settings = settings
	e.windows = windows
	e.setCollectorAgentsLocked(agents)
	oldNodes := e.nodes
	oldChecks := e.checks
	e.nodes = map[int64]model.Node{}
	e.checks = map[int64]model.Check{}
	seen := map[int64]bool{}
	for _, n := range nodes {
		e.nodes[n.ID] = n
		for _, c := range n.Checks {
			e.checks[c.ID] = c
			seen[c.ID] = true
			st, ok := e.states[c.ID]
			if !ok {
				if s, found := states[c.ID]; found {
					st = &s
				} else {
					st = &model.CheckState{CheckID: c.ID, Status: model.StatusUnknown}
				}
				e.states[c.ID] = st
			}
			enabled := c.Enabled && n.Enabled
			if !enabled {
				if st.Status != model.StatusPaused {
					st.Status = model.StatusPaused
					st.NextRunAt = nil
					st.ConsecutiveFailures = 0
					st.AlertActive = false
					st.AlertSuppressed = false
					st.AffectedByCheckID = nil
					st.WarningActive = false
					_ = e.store.SaveState(ctx, *st)
				}
				continue
			}
			if st.Status == model.StatusPaused {
				st.Status = model.StatusUnknown
				st.NextRunAt = nil
			}
			// Schedule: keep the existing next run when the interval did not
			// change, otherwise (or for new checks) stagger the first run.
			old, had := oldChecks[c.ID]
			if st.NextRunAt == nil || !had || old.IntervalSeconds != c.IntervalSeconds || !e.sameConfig(old, c) {
				delay := time.Duration(rand.Int63n(int64(minDuration(time.Duration(c.IntervalSeconds)*time.Second, 30*time.Second)) + 1))
				if !had {
					delay = time.Duration(rand.Int63n(int64(2 * time.Second)))
				}
				next := now.Add(delay)
				st.NextRunAt = &next
			}
			if !e.sameConfig(old, c) && had {
				// Configuration changed: reset content-change baseline.
				st.LastContentHash = ""
				st.LastContentValue = ""
			}
		}
	}
	for id := range e.states {
		if !seen[id] {
			delete(e.states, id)
			delete(e.running, id)
		}
	}
	// Set the semaphore size on first load or when the setting changed.
	size := settings.General.MaxConcurrentChecks
	if size <= 0 {
		size = 8
	}
	if e.sem == nil || cap(e.sem) != size {
		e.sem = make(chan struct{}, size)
	}
	_ = oldNodes
	e.mu.Unlock()
	if err := e.loadTriggers(ctx); err != nil {
		return err
	}
	if err := e.loadRules(ctx); err != nil {
		return err
	}
	e.broadcast(Update{Kind: "config"})
	return nil
}

func (e *Engine) sameConfig(a, b model.Check) bool {
	if a.ID == 0 {
		return false
	}
	ja, _ := json.Marshal(a.Config)
	jb, _ := json.Marshal(b.Config)
	return string(ja) == string(jb) && a.Type == b.Type
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// ---- scheduler ----

func (e *Engine) schedulerLoop() {
	defer e.wg.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-e.ctx.Done():
			return
		case now := <-ticker.C:
			e.tick(now)
		}
	}
}

func (e *Engine) tick(now time.Time) {
	e.mu.Lock()
	// Detect the computer having been asleep / offline: the ticker should fire
	// every second, so a large gap means we were not running. The gap is
	// measured on the wall clock (Round(0) drops the monotonic reading): on
	// Linux and macOS the monotonic clock stops while the machine sleeps, so
	// measured on it a night asleep looked like one second.
	if gap := now.Round(0).Sub(e.lastTick.Round(0)); gap > 90*time.Second {
		e.lastGap = &model.GapInfo{From: e.lastTick, To: now, Seconds: int64(gap.Seconds())}
		e.mu.Unlock()
		e.recordEvent(model.Event{Type: model.EventMonitorGap, Title: "Monitoring paused (computer asleep or offline)",
			Detail: fmt.Sprintf("No checks ran for %s, between %s and %s.", humanDuration(gap), e.lastGap.From.Format("15:04:05"), now.Format("15:04:05"))})
		e.mu.Lock()
	}
	e.lastTick = now
	// While the engine is paused for a restore (pause.go) the clock above
	// keeps moving, so the pause is not mistaken for sleep, but nothing is
	// scheduled and no state is written.
	if !e.enterWork() {
		e.mu.Unlock()
		return
	}
	defer e.leaveWork()
	var due []model.Check
	for id, c := range e.checks {
		st := e.states[id]
		n, ok := e.nodes[c.NodeID]
		if !ok || !c.Enabled || !n.Enabled || st == nil || st.NextRunAt == nil || e.running[id] {
			continue
		}
		// A check a remote collector runs is not this server's to run: its
		// results arrive from the collector (collectors.go).
		if _, collected := n.CollectorFor(c); collected {
			continue
		}
		if !st.NextRunAt.After(now) {
			// A machine that pushes its readings has them judged as they arrive
			// (IngestHostReading). While they keep arriving the schedule has
			// nothing to add — a run would only judge the same reading again —
			// so it moves on; it matters only once readings have stopped.
			if checks.PushesReadings(c) && e.hostReadingFreshLocked(c, now) {
				next := now.Add(time.Duration(c.IntervalSeconds) * time.Second)
				st.NextRunAt = &next
				continue
			}
			due = append(due, c)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		a, b := e.states[due[i].ID].NextRunAt, e.states[due[j].ID].NextRunAt
		if a.Equal(*b) {
			return due[i].ID < due[j].ID
		}
		return a.Before(*b)
	})
	expiredSilences := false
	for _, st := range e.states {
		if st.SilencedUntil != nil && !st.SilencedUntil.After(now) {
			st.SilencedUntil = nil
			expiredSilences = true
			if err := e.store.SaveState(e.ctx, *st); err != nil {
				e.log.Errorf("persist expired silence: %v", err)
			}
		}
	}
	collectorEvents, collectorMails := e.watchCollectorsLocked(now)
	sem := e.sem
	for _, c := range due {
		select {
		case sem <- struct{}{}:
			// Each run holds the gate itself, since it outlives this tick.
			if !e.enterWork() {
				<-sem
				continue
			}
			e.running[c.ID] = true
			e.states[c.ID].Running = true
			e.wg.Add(1)
			go func(c model.Check) {
				defer e.wg.Done()
				defer e.leaveWork()
				defer func() { <-sem }()
				e.runAndProcess(e.ctx, c.ID, 0)
			}(c)
		default:
			// All workers busy: leave the check due for the next tick.
		}
	}
	e.mu.Unlock()
	for _, ev := range collectorEvents {
		e.recordEvent(ev)
	}
	for _, m := range collectorMails {
		e.sendMail(m)
	}
	if expiredSilences || len(collectorEvents) > 0 {
		e.evaluateRules(e.ctx, nil)
		e.broadcast(Update{Kind: "state"})
	}
}

type checkFlight struct {
	done   chan struct{}
	result model.Result
	err    error
}

// runAndProcess executes a check by id and feeds the result through the
// processor. depth limits recursive dependency probing.
func (e *Engine) runAndProcess(ctx context.Context, checkID int64, depth int) (model.Result, error) {
	e.mu.Lock()
	if active := e.inflight[checkID]; active != nil {
		e.mu.Unlock()
		select {
		case <-ctx.Done():
			return model.Result{}, ctx.Err()
		case <-active.done:
			return active.result, active.err
		}
	}
	c, ok := e.checks[checkID]
	if !ok {
		e.clearRunningLocked(checkID)
		e.mu.Unlock()
		return model.Result{}, fmt.Errorf("check %d not found", checkID)
	}
	if _, collected := e.collectedLocked(c); collected {
		// A reload can hand the check to a collector between tick marking
		// it running and this run starting; without clearing the mark it
		// would never be scheduled again.
		e.clearRunningLocked(checkID)
		e.mu.Unlock()
		return model.Result{}, ErrCollected
	}
	flight := &checkFlight{done: make(chan struct{})}
	e.inflight[checkID] = flight
	n := e.nodes[c.NodeID]
	st := e.states[checkID]
	opts := checks.Options{
		NodeHost:          n.Host,
		DefaultCertWarn:   e.settings.Alerts.CertWarnDays,
		LatencyWarnMS:     e.settings.General.LatencyWarnMS,
		PacketLossWarnPct: e.settings.General.PacketLossWarnPct,
		PingMethod:        e.settings.General.PingMethod,
		Hosts:             e.hosts,
	}
	if st != nil {
		opts.PreviousHash = st.LastContentHash
		opts.PreviousValue = st.LastContentValue
		st.Running = true
	}
	e.running[checkID] = true
	e.mu.Unlock()

	// A check saved without a timeout runs each attempt for the checks
	// package's 10 s default, so the whole run must allow for that too.
	perAttempt := c.TimeoutSeconds
	if perAttempt <= 0 {
		perAttempt = 10
	}
	timeout := time.Duration(perAttempt)*time.Second*time.Duration(c.Retries+1) + 15*time.Second
	rctx, cancel := context.WithTimeout(ctx, timeout)
	result := e.opts.Run(rctx, c, opts)
	cancel()
	result.CheckID = checkID
	if result.Timestamp.IsZero() {
		result.Timestamp = time.Now()
	}
	stored, err := e.process(ctx, c, n, result, depth)
	e.mu.Lock()
	flight.result, flight.err = stored, err
	delete(e.inflight, checkID)
	close(flight.done)
	e.clearRunningLocked(checkID)
	e.mu.Unlock()
	return stored, err
}

// clearRunningLocked drops the running mark tick or runAndProcess set on a
// check. The caller holds e.mu.
func (e *Engine) clearRunningLocked(checkID int64) {
	delete(e.running, checkID)
	if st := e.states[checkID]; st != nil {
		st.Running = false
	}
}

// RunNow executes a check immediately, records the result and returns it.
func (e *Engine) RunNow(ctx context.Context, checkID int64) (model.Result, error) {
	if !e.enterWork() {
		return model.Result{}, ErrPaused
	}
	defer e.leaveWork()
	return e.runAndProcess(ctx, checkID, 0)
}

// RunNodeNow runs all enabled checks of a node.
func (e *Engine) RunNodeNow(ctx context.Context, nodeID int64) ([]model.Result, error) {
	return e.RunNodeChecks(ctx, nodeID, nil)
}

// RunNodeChecks runs the enabled checks of a node that allow accepts (all of
// them when allow is nil) and returns their results.
//
// Each run takes a slot of the same semaphore the scheduler uses, so a node
// with hundreds of checks runs at most MaxConcurrentChecks of them at once,
// alongside — not on top of — whatever the schedule has in flight. A caller
// that gives up while waiting for a slot gets the results finished so far.
func (e *Engine) RunNodeChecks(ctx context.Context, nodeID int64, allow func(model.Check) bool) ([]model.Result, error) {
	// The runs below finish before this returns (wg.Wait), so holding the
	// gate here covers them all.
	if !e.enterWork() {
		return nil, ErrPaused
	}
	defer e.leaveWork()
	e.mu.Lock()
	n, ok := e.nodes[nodeID]
	sem := e.sem
	e.mu.Unlock()
	if !ok {
		return nil, store.ErrNotFound
	}
	if sem == nil {
		// Not loaded yet (no ReloadConfig): bound the run on its own.
		sem = make(chan struct{}, model.DefaultSettings().General.MaxConcurrentChecks)
	}
	var out []model.Result
	var wg sync.WaitGroup
	var omu sync.Mutex
admit:
	for _, c := range n.Checks {
		if !c.Enabled || (allow != nil && !allow(c)) {
			continue
		}
		// "Run now" on a node runs what this server runs; the checks a
		// remote collector runs report on their own schedule.
		if _, collected := n.CollectorFor(c); collected {
			continue
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break admit
		}
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			defer func() { <-sem }()
			r, err := e.runAndProcess(ctx, id, 0)
			if err == nil {
				omu.Lock()
				out = append(out, r)
				omu.Unlock()
			}
		}(c.ID)
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].CheckID < out[j].CheckID })
	if out == nil {
		out = []model.Result{}
	}
	return out, nil
}

// TestCheck runs an unsaved check definition without recording anything.
func (e *Engine) TestCheck(ctx context.Context, c model.Check, nodeHost string) model.Result {
	e.mu.Lock()
	opts := checks.Options{
		NodeHost:          nodeHost,
		Hosts:             e.hosts,
		DefaultCertWarn:   e.settings.Alerts.CertWarnDays,
		LatencyWarnMS:     e.settings.General.LatencyWarnMS,
		PacketLossWarnPct: e.settings.General.PacketLossWarnPct,
		PingMethod:        e.settings.General.PingMethod,
	}
	e.mu.Unlock()
	if c.TimeoutSeconds <= 0 {
		c.TimeoutSeconds = 10
	}
	rctx, cancel := context.WithTimeout(ctx, time.Duration(c.TimeoutSeconds)*time.Second*time.Duration(c.Retries+1)+15*time.Second)
	defer cancel()
	r := e.opts.Run(rctx, c, opts)
	if r.Timestamp.IsZero() {
		r.Timestamp = time.Now()
	}
	return r
}

// ---- subscriptions ----

// Subscribe returns a channel receiving updates until Unsubscribe.
func (e *Engine) Subscribe() chan Update {
	ch := make(chan Update, 64)
	e.subMu.Lock()
	e.subs[ch] = struct{}{}
	e.subMu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber.
func (e *Engine) Unsubscribe(ch chan Update) {
	e.subMu.Lock()
	delete(e.subs, ch)
	e.subMu.Unlock()
}

// Broadcast sends an update to every subscriber. It is here for the things
// that happen beside the monitoring loop rather than inside it — a discovery
// sweep, so far — which have news for the interface but nothing to record.
func (e *Engine) Broadcast(u Update) { e.broadcast(u) }

func (e *Engine) broadcast(u Update) {
	e.subMu.Lock()
	defer e.subMu.Unlock()
	for ch := range e.subs {
		select {
		case ch <- u:
		default:
		}
	}
}

// ---- events ----

// RecordEvent appends an event to the timeline and notifies subscribers.
func (e *Engine) RecordEvent(ev model.Event) model.Event {
	return e.recordEvent(ev)
}

func (e *Engine) recordEvent(ev model.Event) model.Event {
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now()
	}
	saved, err := e.store.InsertEvent(context.Background(), ev)
	if err != nil {
		e.log.Errorf("record event: %v", err)
		return ev
	}
	u := Update{Kind: "event"}
	if ev.NodeID != nil {
		u.NodeID = *ev.NodeID
	}
	if ev.CheckID != nil {
		u.CheckID = *ev.CheckID
	}
	e.broadcast(u)
	return saved
}

// RecordError logs an internal error and records it in the timeline.
func (e *Engine) RecordError(context string, err error) {
	e.log.Errorf("%s: %v", context, err)
	e.recordEvent(model.Event{Type: model.EventInternalError, Title: "Internal error: " + context, Detail: err.Error()})
}

func humanDuration(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
}

func ptrTime(t time.Time) *time.Time { return &t }
func ptrInt64(v int64) *int64        { return &v }

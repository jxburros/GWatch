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
	Kind    string `json:"kind"` // result | state | event | config | maintenance | health
	CheckID int64  `json:"checkId,omitempty"`
	NodeID  int64  `json:"nodeId,omitempty"`
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

	mu       sync.Mutex
	settings model.Settings
	nodes    map[int64]model.Node
	checks   map[int64]model.Check
	states   map[int64]*model.CheckState
	running  map[int64]bool
	windows  []model.MaintenanceWindow
	activeMW map[int64]bool

	triggers    map[int64][]model.Trigger // by node id
	triggerLast map[int64]time.Time       // last run per trigger (cooldowns)
	runner      *actions.Runner

	sem      chan struct{}
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	started  bool
	stopping bool

	startedAt    time.Time
	lastTick     time.Time
	lastGap      *model.GapInfo
	lastCheckAt  *time.Time
	lastOKAt     *time.Time
	lastAlertAt  *time.Time
	lastAlertErr string

	retention model.RetentionStatus
	backup    model.BackupStatus

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
		store:    st,
		log:      log,
		opts:     opts,
		nodes:    map[int64]model.Node{},
		checks:   map[int64]model.Check{},
		states:   map[int64]*model.CheckState{},
		running:  map[int64]bool{},
		activeMW: map[int64]bool{},
		subs:     map[chan Update]struct{}{},

		triggers:    map[int64][]model.Trigger{},
		triggerLast: map[int64]time.Time{},
	}
	return e
}

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
	e.wg.Add(4)
	go e.schedulerLoop()
	go e.maintenanceLoop()
	go e.retentionLoop()
	go e.backupLoop()
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
	e.wg.Wait()
	e.log.Printf("engine stopped")
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
	now := time.Now()
	e.mu.Lock()
	e.settings = settings
	e.windows = windows
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
	// every second, so a large gap means we were not running.
	if gap := now.Sub(e.lastTick); gap > 90*time.Second {
		e.lastGap = &model.GapInfo{From: e.lastTick, To: now, Seconds: int64(gap.Seconds())}
		e.mu.Unlock()
		e.recordEvent(model.Event{Type: model.EventMonitorGap, Title: "Monitoring paused (computer asleep or offline)",
			Detail: fmt.Sprintf("No checks ran for %s, between %s and %s.", humanDuration(gap), e.lastGap.From.Format("15:04:05"), now.Format("15:04:05"))})
		e.mu.Lock()
	}
	e.lastTick = now
	var due []model.Check
	for id, c := range e.checks {
		st := e.states[id]
		n, ok := e.nodes[c.NodeID]
		if !ok || !c.Enabled || !n.Enabled || st == nil || st.NextRunAt == nil || e.running[id] {
			continue
		}
		if !st.NextRunAt.After(now) {
			due = append(due, c)
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].ID < due[j].ID })
	sem := e.sem
	for _, c := range due {
		select {
		case sem <- struct{}{}:
			e.running[c.ID] = true
			e.states[c.ID].Running = true
			e.wg.Add(1)
			go func(c model.Check) {
				defer e.wg.Done()
				defer func() { <-sem }()
				e.runAndProcess(e.ctx, c.ID, 0)
			}(c)
		default:
			// All workers busy: leave the check due for the next tick.
		}
	}
	e.mu.Unlock()
}

// runAndProcess executes a check by id and feeds the result through the
// processor. depth limits recursive dependency probing.
func (e *Engine) runAndProcess(ctx context.Context, checkID int64, depth int) (model.Result, error) {
	e.mu.Lock()
	c, ok := e.checks[checkID]
	if !ok {
		e.mu.Unlock()
		return model.Result{}, fmt.Errorf("check %d not found", checkID)
	}
	n := e.nodes[c.NodeID]
	st := e.states[checkID]
	opts := checks.Options{
		NodeHost:          n.Host,
		DefaultCertWarn:   e.settings.Alerts.CertWarnDays,
		LatencyWarnMS:     e.settings.General.LatencyWarnMS,
		PacketLossWarnPct: e.settings.General.PacketLossWarnPct,
	}
	if st != nil {
		opts.PreviousHash = st.LastContentHash
		opts.PreviousValue = st.LastContentValue
		st.Running = true
	}
	e.running[checkID] = true
	e.mu.Unlock()

	timeout := time.Duration(c.TimeoutSeconds)*time.Second*time.Duration(c.Retries+1) + 15*time.Second
	rctx, cancel := context.WithTimeout(ctx, timeout)
	result := e.opts.Run(rctx, c, opts)
	cancel()
	result.CheckID = checkID
	if result.Timestamp.IsZero() {
		result.Timestamp = time.Now()
	}
	stored, err := e.process(ctx, c, n, result, depth)
	e.mu.Lock()
	delete(e.running, checkID)
	if st := e.states[checkID]; st != nil {
		st.Running = false
	}
	e.mu.Unlock()
	return stored, err
}

// RunNow executes a check immediately, records the result and returns it.
func (e *Engine) RunNow(ctx context.Context, checkID int64) (model.Result, error) {
	return e.runAndProcess(ctx, checkID, 0)
}

// RunNodeNow runs all enabled checks of a node.
func (e *Engine) RunNodeNow(ctx context.Context, nodeID int64) ([]model.Result, error) {
	e.mu.Lock()
	n, ok := e.nodes[nodeID]
	e.mu.Unlock()
	if !ok {
		return nil, store.ErrNotFound
	}
	var out []model.Result
	var wg sync.WaitGroup
	var omu sync.Mutex
	for _, c := range n.Checks {
		if !c.Enabled {
			continue
		}
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
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
		DefaultCertWarn:   e.settings.Alerts.CertWarnDays,
		LatencyWarnMS:     e.settings.General.LatencyWarnMS,
		PacketLossWarnPct: e.settings.General.PacketLossWarnPct,
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

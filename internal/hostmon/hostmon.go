// Package hostmon owns the newest hardware reading of every machine: it samples
// the computer GWatch runs on, accepts readings pushed by registered agents, and
// answers the hardware check and the API with the newest reading for any
// machine.
//
// It does not keep history. A machine's readings are recorded the way any
// check's are — as results of the machine's hardware check, with the check's
// retention, charts and backups — and what this package holds is only what has
// not yet been through a check: the latest reading, in memory. After a restart
// that reading is read back out of the check's results (Restore).
//
// It is the boundary the agent design turns on. An agent connects outwards to
// GWatch and hands over a reading; GWatch never dials back, holds no
// credential for the reporting machine, and can do nothing with an agent's
// token but accept readings filed under that one machine. Losing the token
// lets someone submit fictitious readings for that machine, and nothing else.
package hostmon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
	"github.com/jxburros/GWatch/internal/sysmetrics"
)

// DefaultSampleInterval is how often this computer is read when nothing says
// otherwise. It is independent of any check, so a hardware check on this
// computer always has a fresh reading to judge.
const DefaultSampleInterval = time.Minute

// maxClockSkew bounds how far ahead of GWatch a reporting machine's clock may
// be before its timestamp is replaced with arrival time. Without this a
// machine whose clock is years fast would park a reading at the right-hand
// edge of every chart forever.
const maxClockSkew = 5 * time.Minute

// maxReportAge bounds how far behind a reading may be. An agent that has been
// queueing readings while GWatch was down is welcome to catch up; one
// reporting from last year is a misconfiguration.
const maxReportAge = 48 * time.Hour

// ErrNoReading is returned when a machine has never reported.
var ErrNoReading = errors.New("no hardware reading for this machine yet")

// Monitor samples this computer and keeps the newest reading for every
// machine. It is safe for concurrent use.
type Monitor struct {
	store     *store.Store
	collector *sysmetrics.Collector

	// Interval is how often this computer is sampled. Zero means the default.
	Interval time.Duration

	// OnSample is called after each reading is taken or accepted, so the engine
	// can tell subscribers that the hardware page has something new. It may be
	// nil.
	OnSample func(key string)

	// Restore finds the newest reading of a machine when none is in memory — the
	// first look after a restart — from what its hardware check recorded. It may
	// be nil, in which case there is simply no reading until one arrives.
	Restore func(ctx context.Context, key string) (model.HostMetrics, bool)

	// OnError reports a sampling failure once per transition rather than on
	// every tick, so a machine with no readable hardware does not fill the
	// log. It may be nil.
	OnError func(err error)

	mu      sync.RWMutex
	latest  map[string]model.HostMetrics
	lastErr string
}

// New returns a Monitor reading this computer and storing into st.
func New(st *store.Store) *Monitor {
	return &Monitor{
		store:     st,
		collector: sysmetrics.NewCollector(model.HostKeyLocal),
		latest:    map[string]model.HostMetrics{},
	}
}

// Run samples this computer until ctx is cancelled. The first sample is taken
// immediately so there is a reading as soon as GWatch starts.
func (m *Monitor) Run(ctx context.Context) {
	interval := m.Interval
	if interval <= 0 {
		interval = DefaultSampleInterval
	}
	m.sampleLocal(ctx)

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.sampleLocal(ctx)
		}
	}
}

// sampleLocal takes one reading of this computer and stores it.
func (m *Monitor) sampleLocal(ctx context.Context) {
	metrics, err := m.collector.Collect(ctx)
	if err != nil {
		if ctx.Err() == nil {
			m.reportError(err)
		}
		return
	}
	m.reportError(nil)
	m.remember(metrics)
	m.notify(model.HostKeyLocal)
}

// reportError calls OnError only when the error differs from the last one, so
// a persistent failure is logged once rather than every interval.
func (m *Monitor) reportError(err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	m.mu.Lock()
	changed := msg != m.lastErr
	m.lastErr = msg
	m.mu.Unlock()
	if changed && err != nil && m.OnError != nil {
		m.OnError(err)
	}
}

func (m *Monitor) notify(key string) {
	if m.OnSample != nil {
		m.OnSample(key)
	}
}

// remember keeps a reading as the machine's newest, unless one that is newer is
// already held: readings a machine held back through an outage arrive after the
// ones taken since, and must not push the newest backwards.
func (m *Monitor) remember(metrics model.HostMetrics) {
	m.mu.Lock()
	if cur, ok := m.latest[metrics.Key]; !ok || !metrics.Timestamp.Before(cur.Timestamp) {
		m.latest[metrics.Key] = metrics
	}
	m.mu.Unlock()
}

// Accept takes in a reading pushed by a registered machine and records that the
// machine reported in. The reading is filed under the agent's own key whatever
// the payload claims, so a token can only ever write that machine's readings.
//
// Accepting is not recording: the reading is held as the machine's newest and
// handed back, and whoever called (the engine) runs the machine's hardware check
// over it, which is what puts it in the history.
func (m *Monitor) Accept(ctx context.Context, agent model.Agent, metrics model.HostMetrics, remoteAddr string) (model.HostMetrics, error) {
	metrics.Key = agent.HostKey()
	metrics.Timestamp = m.settleTimestamp(metrics.Timestamp)
	metrics.Warnings = trimStrings(metrics.Warnings, 8, 200)

	m.remember(metrics)
	if err := m.store.TouchAgent(ctx, agent.ID, remoteAddr,
		trim(metrics.AgentVersion, 40), trim(metrics.Hostname, 120),
		trim(metrics.OS, 40), trim(metrics.Arch, 40)); err != nil {
		return model.HostMetrics{}, fmt.Errorf("record the report: %w", err)
	}
	m.notify(metrics.Key)
	return metrics, nil
}

// settleTimestamp keeps a reporting machine's clock from placing readings
// outside the window a chart can show. A plausible timestamp is kept — it is
// when the machine actually took the reading — and an implausible one is
// replaced with now.
func (m *Monitor) settleTimestamp(ts time.Time) time.Time {
	now := time.Now()
	if ts.IsZero() || ts.After(now.Add(maxClockSkew)) || ts.Before(now.Add(-maxReportAge)) {
		return now
	}
	return ts
}

// LocalHost returns the newest reading for the computer GWatch runs on. It
// takes one on demand if the sampler has not produced one yet, so a check that
// runs before the first tick still has something to evaluate.
func (m *Monitor) LocalHost(ctx context.Context) (model.HostMetrics, error) {
	if metrics, ok := m.Latest(model.HostKeyLocal); ok {
		return metrics, nil
	}
	metrics, err := m.collector.Collect(ctx)
	if err != nil {
		return model.HostMetrics{}, err
	}
	m.remember(metrics)
	return metrics, nil
}

// AgentHost returns the newest reading a registered machine pushed.
func (m *Monitor) AgentHost(ctx context.Context, agentID int64) (model.HostMetrics, error) {
	key := model.AgentHostKey(agentID)
	if metrics, ok := m.Latest(key); ok {
		return metrics, nil
	}
	// Nothing in memory: this is the first look after a restart, so the reading
	// is whatever the machine's check last recorded rather than lost.
	if m.Restore != nil {
		if metrics, ok := m.Restore(ctx, key); ok {
			metrics.Key = key
			m.remember(metrics)
			return metrics, nil
		}
	}
	return model.HostMetrics{}, ErrNoReading
}

// Latest returns the newest in-memory reading for a machine.
func (m *Monitor) Latest(key string) (model.HostMetrics, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	metrics, ok := m.latest[key]
	return metrics, ok
}

// Hold keeps a reading as its machine's newest, as the sampler does for this
// computer and Accept does for an agent's, without anything else happening. It
// is for callers that already have a reading in hand — tests, mostly — and want
// a check to find it.
func (m *Monitor) Hold(metrics model.HostMetrics) {
	if strings.TrimSpace(metrics.Key) == "" {
		return
	}
	if metrics.Timestamp.IsZero() {
		metrics.Timestamp = time.Now()
	}
	m.remember(metrics)
}

// All returns the newest in-memory reading of every machine that has one.
func (m *Monitor) All() map[string]model.HostMetrics {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]model.HostMetrics, len(m.latest))
	for k, v := range m.latest {
		out[k] = v
	}
	return out
}

// Forget drops the cached reading for a machine, so a revoked or deleted agent
// does not keep answering from memory.
func (m *Monitor) Forget(key string) {
	m.mu.Lock()
	delete(m.latest, key)
	m.mu.Unlock()
}

// trim shortens a string a reporting machine supplied. Everything an agent
// sends is stored and later displayed, so nothing arrives unbounded.
func trim(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) > max {
		return s[:max]
	}
	return s
}

func trimStrings(in []string, maxCount, maxLen int) []string {
	if len(in) > maxCount {
		in = in[:maxCount]
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = trim(s, maxLen); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

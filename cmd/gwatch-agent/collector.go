// Remote collector mode (issue #123): running some of GWatch's network checks
// from where this machine is.
//
// It is off unless two people have agreed. This machine's owner says
// `"collector": true` in agent.json (or GWATCH_AGENT_COLLECTOR=true), and an
// administrator turns collecting on for this agent in GWatch. The agent says
// at each check-in whether its own file allows it; GWatch answers with a list
// of checks only when both halves are there. Nothing else can start it:
// without the local setting the agent ignores whatever a check-in says about
// checks, and this file's code never runs.
//
// What can be asked of it is narrow on purpose (docs/AGENT-DECISIONS.md,
// entry 16). A check definition is data: one of the built-in network check
// types (ping, TCP, HTTP and its keyword/JSON/certificate forms, DNS, SNMP),
// an address, and the settings that type reads. A script, a hardware check or
// anything else is refused here when the list arrives
// (model.CollectorCheck.Validate) and refused again by the runner
// (checks.RunCollected), and nothing in a definition names a command, a file
// or code to fetch. The agent still opens every connection: the list arrives
// as part of the answer to its own check-in, and the results go out to
// /ingest/results with its own token. GWatch has no way to reach it between
// check-ins and no way to make it do anything but open the connections a
// definition describes.
//
// The definitions are kept in memory only. Some of them carry credentials —
// an SNMP community, an HTTP header with an API key — and the agent writes
// nothing it was handed by GWatch to disk; after a restart it runs nothing
// until its first check-in, a few seconds later. Results are a different
// matter: those it holds through an outage exactly as it holds readings
// (spool.go), in agent-results.jsonl, so a site whose link to GWatch is down
// for an hour still has that hour's history once it is back.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jxburros/GWatch/internal/checks"
	"github.com/jxburros/GWatch/internal/model"
)

// How results leave: collected for up to collectorSendEvery and sent in one
// batch, sooner once collectorSendAt have piled up. A heartbeat (an empty
// batch) goes at least every model.CollectorHeartbeat, so GWatch can tell a
// collector with nothing due from one that has gone.
var (
	collectorSendEvery = 5 * time.Second
	// firstRunSpread is the most a newly assigned check waits before its
	// first run (and never more than its interval).
	firstRunSpread = 30 * time.Second
)

const (
	collectorSendAt = 100
	// collectorSlots bounds how many checks run at once, so a collector with
	// a few hundred checks on the same interval is a steady trickle of
	// connections rather than a burst.
	collectorSlots = 8
	// heldAfter is how old a result has to be, by this machine's clock, to be
	// marked as held: it is being delivered late, after waiting out a failed
	// delivery, and GWatch files it at its own time without judging it.
	heldAfter = 2 * time.Minute
)

// runCollected runs one check. Tests replace it.
var runCollected = checks.RunCollected

// resultsFile is where results are held while GWatch cannot be reached.
func resultsFile() string { return filepath.Join(filepath.Dir(configFile()), "agent-results.jsonl") }

type resultQueue = spool[model.Result]

// newResultQueue holds results in memory. Its count limit is the hard one
// rather than one derived from an interval, because results come at many
// intervals at once; the age and size limits are the readings' own.
func newResultQueue(cfg config) *resultQueue {
	return &resultQueue{max: maxQueued, maxAge: cfg.buffer, maxBytes: maxQueueBytes,
		stamp: func(r model.Result) time.Time { return r.Timestamp }, noun: "result"}
}

// collectorJob is one check being run on its interval.
type collectorJob struct {
	def    model.CollectorCheck
	key    string // the definition as JSON, to notice a change
	cancel context.CancelFunc
}

// collectorRunner runs the assigned checks and sends their results.
type collectorRunner struct {
	cfg     config
	rt      *agentRuntime
	client  *http.Client
	ctx     context.Context
	results chan model.Result
	sem     chan struct{}

	mu       sync.Mutex
	jobs     map[int64]*collectorJob
	opts     model.CollectorOptions
	active   bool // GWatch has assigned checks (possibly none) at the last check-in
	lastSent time.Time
	lastErr  string
	held     int
}

func newCollectorRunner(ctx context.Context, cfg config, rt *agentRuntime, client *http.Client) *collectorRunner {
	return &collectorRunner{
		cfg: cfg, rt: rt, client: client, ctx: ctx,
		results: make(chan model.Result, 256),
		sem:     make(chan struct{}, collectorSlots),
		jobs:    map[int64]*collectorJob{},
	}
}

// assign takes up the collector part of a check-in answer. Nil means GWatch
// has not made this machine a collector (or has stopped), and everything stops.
func (c *collectorRunner) assign(a *model.CollectorAssignment) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if a == nil {
		if c.active || len(c.jobs) > 0 {
			log.Printf("GWatch has not turned collecting on for this machine (or has turned it off); running no checks for it")
		}
		for id, j := range c.jobs {
			j.cancel()
			delete(c.jobs, id)
		}
		c.active = false
		return
	}
	wanted := map[int64]model.CollectorCheck{}
	refused := 0
	for _, def := range a.Checks {
		if len(wanted) == model.MaxCollectorChecks {
			break
		}
		if err := def.Validate(); err != nil {
			refused++
			log.Printf("not running a check GWatch assigned: %v", err)
			continue
		}
		wanted[def.ID] = def
	}
	changed := !c.active || c.opts != a.Options
	c.opts = a.Options
	for id, j := range c.jobs {
		def, ok := wanted[id]
		if ok && definitionKey(def) == j.key {
			continue
		}
		j.cancel()
		delete(c.jobs, id)
		changed = true
	}
	for id, def := range wanted {
		if _, ok := c.jobs[id]; ok {
			continue
		}
		jctx, cancel := context.WithCancel(c.ctx)
		j := &collectorJob{def: def, key: definitionKey(def), cancel: cancel}
		c.jobs[id] = j
		go c.runJob(jctx, def)
		changed = true
	}
	c.active = true
	if changed {
		msg := fmt.Sprintf("running %d check(s) for GWatch as a remote collector", len(c.jobs))
		if refused > 0 {
			msg += fmt.Sprintf(" (%d refused)", refused)
		}
		log.Print(msg)
	}
}

func definitionKey(def model.CollectorCheck) string {
	b, _ := json.Marshal(def)
	return string(b)
}

// options are the runner's defaults, as GWatch last sent them.
func (c *collectorRunner) options() checks.Options {
	c.mu.Lock()
	defer c.mu.Unlock()
	return checks.Options{
		DefaultCertWarn:   c.opts.DefaultCertWarn,
		LatencyWarnMS:     c.opts.LatencyWarnMS,
		PacketLossWarnPct: c.opts.PacketLossWarnPct,
		PingMethod:        c.opts.PingMethod,
	}
}

// runJob runs one check on its interval until it is cancelled. The first run
// is spread over the first half-minute so a collector given fifty checks at
// once does not open fifty connections in the same second. What a check
// remembers from one run to the next (the content it last saw, for content
// watching) is kept here, as the engine keeps it in the check's state.
func (c *collectorRunner) runJob(ctx context.Context, def model.CollectorCheck) {
	check := def.Check()
	interval := time.Duration(def.IntervalSeconds) * time.Second
	wait := time.Duration(rand.Int63n(int64(min(interval, firstRunSpread)) + 1))
	var prevHash, prevValue string
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		select {
		case c.sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		opts := c.options()
		opts.PreviousHash, opts.PreviousValue = prevHash, prevValue
		timeout := time.Duration(max(def.TimeoutSeconds, 10))*time.Second*time.Duration(def.Retries+1) + 15*time.Second
		rctx, cancel := context.WithTimeout(ctx, timeout)
		res := runCollected(rctx, check, opts)
		cancel()
		<-c.sem
		if ctx.Err() != nil {
			return
		}
		res.CheckID = def.ID
		if res.Timestamp.IsZero() {
			res.Timestamp = time.Now()
		}
		if res.Details.ContentHash != "" {
			prevHash = res.Details.ContentHash
		}
		if res.Details.ContentHash != "" || res.Details.ContentValue != "" {
			prevValue = res.Details.ContentValue
		}
		select {
		case c.results <- res:
		case <-ctx.Done():
			return
		}
		wait = interval
	}
}

// sendLoop delivers results until ctx ends. It is the queue's only user.
func (c *collectorRunner) sendLoop(ctx context.Context) {
	q := openSpool(newResultQueue(c.cfg), c.cfg.buffer, resultsFile())
	defer q.sync()
	tick := time.NewTicker(collectorSendEvery)
	defer tick.Stop()
	var lastPost, nextAttempt time.Time
	backoff := time.Duration(0)
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return
		case r := <-c.results:
			q.add(r)
			if q.len() < collectorSendAt {
				continue
			}
		case <-tick.C:
		}
		c.mu.Lock()
		active := c.active
		c.mu.Unlock()
		now := time.Now()
		heartbeat := active && now.Sub(lastPost) >= model.CollectorHeartbeat
		if now.Before(nextAttempt) || (q.len() == 0 && !heartbeat) {
			q.sync()
			c.noteHeld(q.len())
			continue
		}
		var err error
		if q.len() == 0 {
			err = c.post(ctx, nil)
		} else {
			// A few batches per pass, so catching up after an outage is quick
			// without turning into one enormous request.
			for i := 0; i < 5 && q.len() > 0 && err == nil; i++ {
				_, err = q.flushBatch(ctx, model.MaxCollectorBatch, func(rs []model.Result) error { return c.post(ctx, rs) })
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			failures++
			backoff = nextBackoff(backoff, collectorSendEvery*2)
			nextAttempt = now.Add(backoff)
			if failures == 1 || failures%10 == 0 {
				log.Printf("could not deliver check results (attempt %d, trying again in %s, %d held): %v", failures, backoff, q.len(), err)
			}
			c.noteSend(err)
		} else {
			if failures > 0 {
				log.Printf("delivering check results again after %d failed attempt(s)", failures)
			}
			failures, backoff, nextAttempt = 0, 0, time.Time{}
			lastPost = now
			c.noteSend(nil)
		}
		q.sync()
		c.noteHeld(q.len())
	}
}

func (c *collectorRunner) noteSend(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.lastErr = err.Error()
		return
	}
	c.lastErr = ""
	c.lastSent = time.Now()
}

func (c *collectorRunner) noteHeld(n int) {
	c.mu.Lock()
	c.held = n
	c.mu.Unlock()
}

// post sends one batch. A failure says whether sending it again can help, as a
// reading's does: an unreachable or busy server, or a rejected token, can mend;
// a refusal (collecting turned off, a batch GWatch calls malformed) will not,
// and the batch is dropped rather than blocking everything behind it.
func (c *collectorRunner) post(ctx context.Context, rs []model.Result) error {
	now := time.Now()
	batch := model.CollectorBatch{Results: make([]model.CollectorResult, 0, len(rs))}
	for _, r := range rs {
		batch.Results = append(batch.Results, model.CollectorResult{Result: r, Held: now.Sub(r.Timestamp) > heldAfter})
	}
	body, err := json.Marshal(batch)
	if err != nil {
		return &sendFailure{err: err}
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.baseURL()+"/ingest/results", bytes.NewReader(body))
	if err != nil {
		return &sendFailure{err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.rt.currentToken())
	req.Header.Set("User-Agent", userAgent())
	resp, err := c.client.Do(req)
	if err != nil {
		return &sendFailure{err: err, retry: true}
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode/100 == 2 {
		var reply model.CollectorBatchReply
		if json.Unmarshal(payload, &reply) == nil && reply.Rejected > 0 {
			// Usually a check moved off this collector between check-ins.
			log.Printf("GWatch did not take %d result(s): their checks are no longer assigned to this machine, or their time could not be believed", reply.Rejected)
		}
		return nil
	}
	retry := resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests ||
		resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusUnauthorized
	return &sendFailure{retry: retry, err: fmt.Errorf("the server answered HTTP %d: %s", resp.StatusCode, serverMessage(resp.StatusCode, payload))}
}

// collectorStatus is what the status file says about collecting, for the
// control panel and the tray.
type collectorStatus struct {
	Allowed  bool       `json:"allowed"`
	Active   bool       `json:"active"`
	Checks   int        `json:"checks"`
	Held     int        `json:"held,omitempty"`
	LastSent *time.Time `json:"lastSent,omitempty"`
	Error    string     `json:"error,omitempty"`
}

func (c *collectorRunner) status() *collectorStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := &collectorStatus{Allowed: true, Active: c.active, Checks: len(c.jobs), Held: c.held, Error: c.lastErr}
	if !c.lastSent.IsZero() {
		t := c.lastSent
		st.LastSent = &t
	}
	return st
}

// describeCollector is the one line the agent logs at start about collecting.
func describeCollector(cfg config) string {
	if !cfg.collector {
		return ""
	}
	return strings.TrimSpace(`remote collecting is allowed by this machine's settings; it runs checks only if GWatch has also turned it on for this agent`)
}

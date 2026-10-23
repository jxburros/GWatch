// The agent's occasional question to GWatch, and the queue that keeps readings
// through an outage.
//
// The agent still only ever dials out. Every so often it asks GWatch a small,
// fixed set of questions and applies the answers, each on its own terms: which
// readings the administrator wants (always within what this machine's own
// configuration allows), when to ask again, which agent release is newest (a
// hint; releases are still fetched and verified from the release feed, never
// from here), and — only while an administrator is rotating this machine's
// token — the replacement token. GWatch's answer has no field for a URL, a
// command, a file or a version to install, so there is nothing in it that could
// make this machine run something it did not choose to (docs/AGENT-DECISIONS.md,
// entry 12).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/permissions"
	"github.com/jxburros/GWatch/internal/sysmetrics"
	"github.com/jxburros/GWatch/internal/update"
)

// firstCheckIn is how long after starting the agent first asks. The first
// reading has been taken by then, so the inventory it carries is real, and a
// machine that has just paired shows its checkboxes in GWatch within moments.
const firstCheckIn = 8 * time.Second

// maxFlushPerCycle bounds how many held readings are sent in one go, so an
// agent that has been offline for hours catches up over a few cycles rather
// than in one burst.
const maxFlushPerCycle = 120

// maxQueued bounds the held readings by count whatever the buffer setting
// says, so a very short interval with a long buffer cannot grow without limit.
const maxQueued = 4000

// agentRuntime is the part of the agent that changes while it runs: the token it
// presents (it can be rotated), what GWatch wants collected, and what this
// machine could collect.
type agentRuntime struct {
	mu            sync.Mutex
	token         string
	tokenFromFile bool
	ceiling       model.CollectSelection // from agent.json: the machine owner's limit
	selection     model.CollectSelection // from GWatch: the administrator's choice within it
	inventory     model.Inventory        // what GWatch is offered: within the machine's own limit
	haveInventory bool
	inventoryAll  model.Inventory // everything this machine has, for the control panel
	haveAll       bool
	checkIn       time.Duration
	latestSeen    string
	warnedToken   bool

	// What the control panel is told (status.go).
	startedAt      time.Time
	lastReport     time.Time
	lastReportOK   bool
	lastReportErr  string
	failures       int
	held           int
	lastCheckIn    time.Time
	lastCheckInErr string
}

func newRuntime(cfg config) *agentRuntime {
	rt := &agentRuntime{
		token:         cfg.token,
		tokenFromFile: cfg.tokenSource == srcFile,
		ceiling:       cfg.collect.Normalize(),
		checkIn:       cfg.checkIn,
		startedAt:     time.Now(),
	}
	if st, err := readAgentState(); err == nil {
		rt.selection = st.Collect
	}
	return rt
}

func (rt *agentRuntime) currentToken() string {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.token
}

// effective is the selection to collect with: the machine's limit, narrowed to
// what GWatch asked for within it.
func (rt *agentRuntime) effective() model.CollectSelection {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.ceiling.Effective(rt.selection)
}

func (rt *agentRuntime) setInventory(inv model.Inventory) {
	rt.mu.Lock()
	rt.inventory, rt.haveInventory = inv, true
	rt.mu.Unlock()
}

// shape narrows a finished reading to what this machine is willing to send, and
// notes what it could have sent. The inventory is built after the machine's own
// limit and before GWatch's choice, so what GWatch offers to tick is exactly
// what ticking it could deliver.
func (rt *agentRuntime) shape(m model.HostMetrics) model.HostMetrics {
	rt.mu.Lock()
	ceiling := rt.ceiling
	rt.mu.Unlock()
	visible := sysmetrics.Apply(m, ceiling, false)
	rt.mu.Lock()
	rt.inventoryAll, rt.haveAll = sysmetrics.InventoryOf(m), true
	rt.mu.Unlock()
	rt.setInventory(sysmetrics.InventoryOf(visible))
	return sysmetrics.Apply(visible, rt.effective(), true)
}

// ---- state kept between runs ----

// agentState is what the agent remembers of GWatch's answers, so a restart
// while GWatch is unreachable still collects what the administrator chose.
// It is the agent's own bookkeeping, not configuration; agent.json is the file
// to edit.
type agentState struct {
	Collect   model.CollectSelection `json:"collect"`
	CheckedAt time.Time              `json:"checkedAt"`
}

func stateFile() string { return filepath.Join(filepath.Dir(configFile()), "agent-state.json") }

func readAgentState() (agentState, error) {
	var st agentState
	b, err := os.ReadFile(stateFile())
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return agentState{}, err
	}
	if err := st.Collect.Validate(); err != nil {
		return agentState{}, err
	}
	st.Collect = st.Collect.Normalize()
	return st, nil
}

func writeAgentState(st agentState) error {
	path := stateFile()
	if err := permissions.EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(path, append(b, '\n')); err != nil {
		return err
	}
	restrictWindowsACL(path)
	return nil
}

// ---- the check-in ----

func (c config) checkInURL() string { return c.baseURL() + "/ingest/checkin" }

// maxSettingsBody bounds GWatch's answer. It is a few hundred bytes.
const maxSettingsBody = 256 << 10

// askSettings makes one check-in.
func askSettings(ctx context.Context, client *http.Client, cfg config, token string, req model.AgentCheckIn) (model.AgentSettings, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return model.AgentSettings{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.checkInURL(), bytes.NewReader(body))
	if err != nil {
		return model.AgentSettings{}, err
	}
	hr.Header.Set("Content-Type", "application/json")
	hr.Header.Set("Authorization", "Bearer "+token)
	hr.Header.Set("User-Agent", userAgent())
	resp, err := client.Do(hr)
	if err != nil {
		return model.AgentSettings{}, err
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, maxSettingsBody))
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		// A GWatch from before check-ins existed. Not an error worth shouting
		// about: it just has nothing to say.
		return model.AgentSettings{}, errNoCheckIn
	}
	if resp.StatusCode/100 != 2 {
		return model.AgentSettings{}, fmt.Errorf("the server answered HTTP %d: %s", resp.StatusCode, serverMessage(resp.StatusCode, payload))
	}
	var out model.AgentSettings
	if err := json.Unmarshal(payload, &out); err != nil {
		return model.AgentSettings{}, errors.New("the server's answer was not a GWatch settings reply")
	}
	return out, nil
}

var errNoCheckIn = errors.New("this GWatch does not answer check-ins")

// acceptableToken is whether a string is plausibly an agent token. A reply is
// data from the network and the token goes into a file, so it is checked
// rather than trusted.
func acceptableToken(t string) bool {
	if !strings.HasPrefix(t, "gwa_") || len(t) < 16 || len(t) > 128 {
		return false
	}
	for _, r := range t {
		if !(r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// checkInLoop asks GWatch for settings now and then until ctx ends.
func (p *program) checkInLoop(ctx context.Context, rt *agentRuntime, client *http.Client) {
	wait := firstCheckIn
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		next, err := p.checkInOnce(ctx, rt, client)
		switch {
		case errors.Is(err, errNoCheckIn):
			// Try again rarely; the server may be upgraded.
			wait = model.CheckInMax / 4
			continue
		case err != nil:
			if ctx.Err() != nil {
				return
			}
			failures++
			wait = min(rt.checkIn, time.Duration(failures)*time.Minute)
			if wait < time.Minute {
				wait = time.Minute
			}
			if failures == 1 || failures%10 == 0 {
				log.Printf("could not ask GWatch for settings (this changes nothing; trying again in %s): %v", wait, err)
			}
		default:
			failures = 0
			wait = next
		}
	}
}

// checkInOnce asks once, applies the answer and returns when to ask again.
func (p *program) checkInOnce(ctx context.Context, rt *agentRuntime, client *http.Client) (time.Duration, error) {
	rt.mu.Lock()
	req := model.AgentCheckIn{Version: version}
	if rt.haveInventory {
		inv := rt.inventory
		req.Inventory = &inv
	}
	token := rt.token
	rt.mu.Unlock()

	settings, err := askSettings(ctx, client, p.cfg, token, req)
	rt.noteCheckIn(err)
	rt.writeStatus(p.cfg, -1)
	if err != nil {
		return 0, err
	}
	next := rt.apply(p.cfg, settings)
	rt.writeStatus(p.cfg, -1)
	return next, nil
}

// apply takes up GWatch's answer, checking each part, and returns how long to
// wait before asking again.
func (rt *agentRuntime) apply(cfg config, s model.AgentSettings) time.Duration {
	next := time.Duration(s.CheckInSeconds) * time.Second
	if cfg.sources["checkIn"] != srcDefault || next <= 0 {
		// The machine's own setting wins over a suggestion.
		next = cfg.checkIn
	}
	next = max(model.CheckInMin, min(model.CheckInMax, next))
	rt.mu.Lock()
	rt.checkIn = next
	rt.mu.Unlock()

	// What to collect.
	sel := s.Collect.Normalize()
	if err := sel.Validate(); err != nil {
		log.Printf("ignoring the collection settings GWatch sent: %v", err)
	} else {
		rt.mu.Lock()
		changed := !sameSelection(rt.selection, sel)
		rt.selection = sel
		rt.mu.Unlock()
		if changed {
			log.Printf("collection settings changed in GWatch: %s", describeCollect(sel))
			if err := writeAgentState(agentState{Collect: sel, CheckedAt: time.Now()}); err != nil {
				log.Printf("could not remember the collection settings (%v); they are used until this agent restarts", err)
			}
		}
	}

	// A newer agent exists. This is only said: the agent looks for releases on
	// its own schedule, from the release feed, and verifies what it downloads.
	if v := strings.TrimSpace(s.LatestAgent); v != "" && v != rt.latestSeen && update.CompareVersions(v, version) > 0 {
		rt.latestSeen = v
		log.Printf("GWatch reports that agent %s is available; this agent (%s) updates itself from signed releases, and %s", v, version, updateAdvice())
	}

	// A replacement token.
	if tok := strings.TrimSpace(s.Token); tok != "" {
		rt.takeToken(cfg, tok)
	}
	return next
}

func updateAdvice() string {
	if pk, ok := currentPackaging(); ok {
		return "its package manager does this: " + pk.updateCommand("")
	}
	return "nothing needs doing here"
}

// takeToken switches to a replacement token during a rotation. It does so only
// when the token it replaces came from agent.json, because that is the one
// place this agent writes: a token set by an environment variable or a flag
// would come straight back at the next restart, after GWatch had retired the
// copy the file now holds.
func (rt *agentRuntime) takeToken(cfg config, tok string) {
	if !acceptableToken(tok) {
		log.Printf("ignoring a replacement token from GWatch that does not look like an agent token")
		return
	}
	rt.mu.Lock()
	fromFile := rt.tokenFromFile
	already := rt.token == tok
	rt.mu.Unlock()
	if already {
		return
	}
	if !fromFile {
		rt.mu.Lock()
		warned := rt.warnedToken
		rt.warnedToken = true
		rt.mu.Unlock()
		if !warned {
			log.Printf("GWatch has issued a replacement token for this machine, but this agent's token comes from the %s, which it does not rewrite; put the new token there (GWatch shows it on this machine's node) and restart the agent", cfg.sources["token"])
		}
		return
	}
	if err := storeToken(cfg.configPathOrDefault(), tok); err != nil {
		log.Printf("could not save the replacement token to %s (%v); keeping the current one", cfg.configPathOrDefault(), err)
		return
	}
	rt.mu.Lock()
	rt.token = tok
	rt.mu.Unlock()
	log.Printf("this machine's token was rotated from GWatch and the new one is saved in %s", cfg.configPathOrDefault())
}

func sameSelection(a, b model.CollectSelection) bool {
	x, _ := json.Marshal(a.Normalize())
	y, _ := json.Marshal(b.Normalize())
	return bytes.Equal(x, y)
}

// ---- holding readings through an outage ----

// sendFailure is a report that did not go through, with whether trying the same
// reading again later can help.
type sendFailure struct {
	err   error
	retry bool
}

func (f *sendFailure) Error() string { return f.err.Error() }
func (f *sendFailure) Unwrap() error { return f.err }

// readingQueue holds readings that could not be delivered, oldest first, so a
// gap in the network leaves a gap in the history no longer than the gap itself.
// It lives in memory only: the agent stores nothing on this machine beyond its
// configuration, and a reading that is hours old and lost to a restart is not
// worth a spool file.
type readingQueue struct {
	max  int
	held []model.HostMetrics
}

func newReadingQueue(cfg config) *readingQueue {
	n := 0
	if cfg.buffer > 0 && cfg.interval > 0 {
		n = int(cfg.buffer / cfg.interval)
	}
	return &readingQueue{max: max(0, min(n, maxQueued))}
}

func (q *readingQueue) len() int { return len(q.held) }

// add puts a new reading at the back, dropping the oldest if the queue is full.
// With no buffer at all the queue holds only what is about to be sent.
func (q *readingQueue) add(m model.HostMetrics) {
	q.held = append(q.held, m)
	if limit := max(q.max, 1); len(q.held) > limit {
		q.held = q.held[len(q.held)-limit:]
	}
}

// flush sends held readings oldest first and stops at the first that cannot go.
// It reports how many were delivered, and the failure if there was one. A
// reading the server refuses outright (not an outage, a verdict) is dropped,
// because holding it would block everything behind it for ever.
func (q *readingQueue) flush(ctx context.Context, sendOne func(model.HostMetrics) error) (sent int, err error) {
	for len(q.held) > 0 && sent < maxFlushPerCycle {
		if ctx.Err() != nil {
			return sent, ctx.Err()
		}
		err := sendOne(q.held[0])
		if err == nil {
			q.held = q.held[1:]
			sent++
			continue
		}
		var sf *sendFailure
		if errors.As(err, &sf) && !sf.retry {
			log.Printf("dropping a reading GWatch refused: %v", err)
			q.held = q.held[1:]
			continue
		}
		return sent, err
	}
	return sent, nil
}

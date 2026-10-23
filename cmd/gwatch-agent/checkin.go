// The agent's occasional question to GWatch. (The queue that keeps readings
// through an outage is in spool.go.)
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

// watch points the collector at the services and programs to watch now: the
// machine's own list, narrowed to GWatch's choice. It is called before every
// reading, so a change GWatch sends takes effect on the next one.
func (rt *agentRuntime) watch(c *sysmetrics.Collector) {
	eff := rt.effective()
	c.SetWatch(eff.WatchedServices(), eff.WatchedProcesses())
}

// watchInventory lists the services and programs this machine could watch,
// for GWatch to offer as choices. It runs at check-in, not with each
// reading: listing every service is a systemctl call or a walk of the
// Service Control Manager, cheap once every quarter of an hour and pointless
// every minute. When the machine's own file names services or programs,
// those are all GWatch may choose from, so they are all it is offered.
func (rt *agentRuntime) watchInventory(ctx context.Context) (services, processes []model.InventoryItem) {
	rt.mu.Lock()
	ceiling := rt.ceiling
	rt.mu.Unlock()
	services, _ = sysmetrics.ListServices(ctx)
	processes, _ = sysmetrics.ListProcesses(ctx)
	return withinCeiling(services, ceiling.WatchedServices()), withinCeiling(processes, ceiling.WatchedProcesses())
}

// withinCeiling narrows an inventory list to the names a machine's own file
// allows, keeping each allowed name even when the machine does not have it
// now (the picker marks it "not seen now"). An empty ceiling allows anything.
func withinCeiling(items []model.InventoryItem, ceiling []string) []model.InventoryItem {
	if len(ceiling) == 0 {
		return items
	}
	out := make([]model.InventoryItem, 0, len(ceiling))
	for _, name := range ceiling {
		item := model.InventoryItem{Name: name}
		for _, it := range items {
			if strings.EqualFold(it.Name, name) {
				item.Detail = it.Detail
				break
			}
		}
		out = append(out, item)
	}
	return out
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
	if req.Inventory != nil {
		req.Inventory.Services, req.Inventory.Processes = rt.watchInventory(ctx)
		// Bounded here as GWatch would bound it, so a machine with
		// thousands of services still sends a check-in GWatch accepts.
		*req.Inventory = req.Inventory.Normalize()
	}

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

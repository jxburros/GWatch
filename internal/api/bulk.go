package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/jxburros/GWatch/internal/checks"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
)

// Bulk editing: one change, many nodes and checks.
//
// Changing the check frequency on thirty nodes through PUT /api/nodes/{id}
// means thirty round trips, thirty engine reloads and thirty audit entries —
// and if the tenth one fails, no way to tell from the timeline what state the
// configuration is now in. PATCH /api/nodes/bulk does the same work as one
// transaction, one reload and one entry in the timeline.
//
// The body is a *patch*, not a record: only the fields it actually carries are
// changed, which is why the structures below are built from pointers and raw
// messages rather than plain values. Without that, "enabled": false and a
// missing "enabled" would arrive identically and a bulk edit of the interval
// would quietly disable everything it touched.

// bulkRequest is the body of PATCH /api/nodes/bulk.
type bulkRequest struct {
	NodeIDs  []int64 `json:"nodeIds"`
	CheckIDs []int64 `json:"checkIds"`

	Node  *bulkNodePatch  `json:"node"`
	Check *bulkCheckPatch `json:"check"`

	CheckFilter *bulkCheckFilter `json:"checkFilter"`
}

// bulkCheckFilter narrows which of the selected checks a check patch reaches.
type bulkCheckFilter struct {
	Types []string `json:"types"`
}

// bulkNodePatch is the set of node fields a bulk edit may change. Name, host
// and notes are deliberately absent: they are what tells one node from
// another, and nothing useful comes of setting thirty of them to one value.
type bulkNodePatch struct {
	Groups       *[]string `json:"groups"` // replace the whole list
	AddGroups    []string  `json:"addGroups"`
	RemoveGroups []string  `json:"removeGroups"`
	Tags         *[]string `json:"tags"` // replace the whole list
	AddTags      []string  `json:"addTags"`
	RemoveTags   []string  `json:"removeTags"`

	Importance *model.Importance `json:"importance"`
	Enabled    *bool             `json:"enabled"`

	// DependsOn stays raw so that an absent field, an explicit null (clear the
	// dependency) and a node id can be told apart.
	DependsOn json.RawMessage `json:"dependsOnNodeId"`
}

func (p *bulkNodePatch) empty() bool {
	return p == nil || (p.Groups == nil && len(p.AddGroups) == 0 && len(p.RemoveGroups) == 0 &&
		p.Tags == nil && len(p.AddTags) == 0 && len(p.RemoveTags) == 0 &&
		p.Importance == nil && p.Enabled == nil && len(p.DependsOn) == 0)
}

// bulkCheckPatch is the set of check fields a bulk edit may change. The check
// *type* is not among them: a type carries its own configuration, and changing
// it would leave every check it touched pointing at settings that mean nothing
// for the new type. That is an edit to make one check at a time, with the
// editor in front of you.
type bulkCheckPatch struct {
	IntervalSeconds  *int  `json:"intervalSeconds"`
	TimeoutSeconds   *int  `json:"timeoutSeconds"`
	Retries          *int  `json:"retries"`
	FailureThreshold *int  `json:"failureThreshold"`
	Enabled          *bool `json:"enabled"`

	// Alerts is the whole per-check override. An explicit null clears it; an
	// object replaces it, so a field the object leaves out goes back to
	// following the global setting.
	Alerts json.RawMessage `json:"alerts"`

	// Config is a whitelist of configuration keys that mean the same thing
	// across check types. Anything else is refused by name rather than
	// silently dropped — see bulkConfigKeys.
	Config map[string]json.RawMessage `json:"config"`
}

func (p *bulkCheckPatch) empty() bool {
	return p == nil || (p.IntervalSeconds == nil && p.TimeoutSeconds == nil && p.Retries == nil &&
		p.FailureThreshold == nil && p.Enabled == nil && len(p.Alerts) == 0 && len(p.Config) == 0)
}

// bulkConfigKeys are the check-configuration keys a bulk edit may set. They
// are the ones whose meaning does not depend on the check type, so applying
// one across a mixed selection still says something true.
var bulkConfigKeys = []string{"certWarnDays", "latencyWarnMs", "metricThresholds", "packetLossWarnPct", "pingMethod"}

// bulkChange is one line of "what this edit did", kept with the scope it
// applied to so the audit entry can say "on 14 checks" rather than "on 14".
type bulkChange struct {
	text  string
	nodes bool
}

// bulkResponse is what the caller gets back.
type bulkResponse struct {
	Nodes   int      `json:"nodes"`
	Checks  int      `json:"checks"`
	Changes []string `json:"changes"`
}

func (s *Server) handleBulkUpdateNodes(w http.ResponseWriter, r *http.Request) {
	req, err := decodeBulkRequest(r)
	if err != nil {
		writeDecodeError(w, err)
		return
	}
	if len(req.NodeIDs) == 0 && len(req.CheckIDs) == 0 {
		writeError(w, http.StatusBadRequest, "choose at least one node or check to change")
		return
	}
	if req.Node.empty() && req.Check.empty() {
		writeError(w, http.StatusBadRequest, "choose at least one setting to change")
		return
	}

	all, err := s.Store.ListNodes(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	byNode := map[int64]model.Node{}
	nodeOfCheck := map[int64]model.Node{}
	checkByID := map[int64]model.Check{}
	for _, n := range all {
		byNode[n.ID] = n
		for _, c := range n.Checks {
			nodeOfCheck[c.ID] = n
			checkByID[c.ID] = c
		}
	}

	// The selection, resolved. A stale id is worth saying out loud: a bulk
	// screen left open while something was deleted elsewhere is exactly how
	// one arrives here, and "node 7 no longer exists" is the only answer that
	// tells the person what to do next.
	selectedNodes := make([]model.Node, 0, len(req.NodeIDs))
	seenNode := map[int64]bool{}
	for _, id := range req.NodeIDs {
		n, ok := byNode[id]
		if !ok {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("node %d no longer exists", id))
			return
		}
		if seenNode[id] {
			continue
		}
		seenNode[id] = true
		selectedNodes = append(selectedNodes, n)
	}

	typeFilter, err := bulkTypeFilter(req.CheckFilter)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Every listed check, plus every check of every listed node. The type
	// filter narrows the whole selection, listed checks included, so what the
	// screen says it will touch and what it touches are the same set.
	selectedChecks := make([]model.Check, 0, len(req.CheckIDs))
	seenCheck := map[int64]bool{}
	addCheck := func(c model.Check) {
		if seenCheck[c.ID] || (typeFilter != nil && !typeFilter[c.Type]) {
			return
		}
		seenCheck[c.ID] = true
		selectedChecks = append(selectedChecks, c)
	}
	for _, id := range req.CheckIDs {
		c, ok := checkByID[id]
		if !ok {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("check %d no longer exists", id))
			return
		}
		addCheck(c)
	}
	for _, n := range selectedNodes {
		for _, c := range n.Checks {
			addCheck(c)
		}
	}

	// A patch with nothing to land on is a mistake worth naming rather than a
	// no-op worth reporting as success — most often a type filter that has
	// quietly excluded everything that was ticked.
	if !req.Node.empty() && len(selectedNodes) == 0 {
		writeError(w, http.StatusBadRequest, "the node settings have no nodes to apply to: select some nodes as well as checks")
		return
	}
	if !req.Check.empty() && len(selectedChecks) == 0 {
		if typeFilter != nil {
			writeError(w, http.StatusBadRequest, "nothing to change: the selection holds no checks of the chosen type(s)")
		} else {
			writeError(w, http.StatusBadRequest, "nothing to change: the selected nodes have no checks")
		}
		return
	}

	var changes []bulkChange
	nodesOut := []model.Node{}
	if !req.Node.empty() {
		changes = append(changes, describeNodePatch(req.Node, byNode)...)
		for _, n := range selectedNodes {
			if err := s.applyNodePatch(r.Context(), req.Node, &n); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			nodesOut = append(nodesOut, n)
		}
	}

	checksOut := []model.Check{}
	if !req.Check.empty() {
		changes = append(changes, describeCheckPatch(req.Check)...)
		minInterval := s.Engine.Settings().General.MinIntervalSecs
		for _, c := range selectedChecks {
			if err := applyCheckPatch(req.Check, &c); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			host := nodeOfCheck[c.ID].Host
			if c.IntervalSeconds < minInterval {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("check %q: interval must be at least %d seconds (see Settings › General to change the minimum)", c.Name, minInterval))
				return
			}
			if err := checks.Validate(c, host); err != nil {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("check %q: %v", c.Name, err))
				return
			}
			checksOut = append(checksOut, c)
		}
	}

	res, err := s.Store.BulkUpdate(r.Context(), nodesOut, checksOut)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusBadRequest, err.Error()+" — nothing was changed")
			return
		}
		s.fail(w, err)
		return
	}

	// One reload and one entry in the timeline, however many rows moved: the
	// person made one change and should be able to read it back as one.
	if err := s.Engine.ReloadConfig(r.Context()); err != nil {
		s.Log.Errorf("reload config: %v", err)
	}
	s.recordEvent(r.Context(), model.Event{
		Type:   model.EventConfigChanged,
		Title:  bulkTitle(changes, res),
		Detail: fmt.Sprintf("Applied to %d node(s) and %d check(s).", res.Nodes, res.Checks),
	})

	texts := make([]string, 0, len(changes))
	for _, c := range changes {
		texts = append(texts, c.text)
	}
	writeJSON(w, http.StatusOK, bulkResponse{Nodes: res.Nodes, Checks: res.Checks, Changes: texts})
}

// decodeBulkRequest reads the body and refuses a field it does not know. A
// misspelled key in a patch is not a harmless extra: it means the change the
// caller asked for is not going to happen, and they would never find out.
func decodeBulkRequest(r *http.Request) (*bulkRequest, error) {
	if err := requireJSONBody(r); err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("invalid JSON body: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var req bulkRequest
	if err := dec.Decode(&req); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %w", err)
	}
	return &req, nil
}

// bulkTypeFilter turns checkFilter.types into a set, refusing a type nothing
// can run. Nil means "no filter".
func bulkTypeFilter(f *bulkCheckFilter) (map[model.CheckType]bool, error) {
	if f == nil || len(f.Types) == 0 {
		return nil, nil
	}
	set := map[model.CheckType]bool{}
	for _, t := range f.Types {
		ct := model.CheckType(strings.TrimSpace(t))
		if !ct.Valid() {
			return nil, fmt.Errorf("unknown check type %q", t)
		}
		set[ct] = true
	}
	return set, nil
}

// dependency reads the patch's dependsOnNodeId: present reports whether the
// field was sent at all, and a present-but-nil value means "clear it".
func (p *bulkNodePatch) dependency() (id *int64, present bool, err error) {
	if len(p.DependsOn) == 0 {
		return nil, false, nil
	}
	if string(bytes.TrimSpace(p.DependsOn)) == "null" {
		return nil, true, nil
	}
	var v int64
	if err := json.Unmarshal(p.DependsOn, &v); err != nil {
		return nil, true, fmt.Errorf("dependsOnNodeId: %v", err)
	}
	if v <= 0 {
		return nil, true, nil
	}
	return &v, true, nil
}

// applyNodePatch merges the patch into one node and validates what it changed.
func (s *Server) applyNodePatch(ctx context.Context, p *bulkNodePatch, n *model.Node) error {
	// Groups are read through GroupList once, up front: a row written before
	// the groups column existed says what it meant through the deprecated
	// single group, and reading it again between the remove and the add would
	// bring a group back that was just taken away.
	groups := append([]string{}, n.GroupList()...)
	if p.Groups != nil {
		groups = append([]string{}, (*p.Groups)...)
	}
	groups = withoutFold(groups, p.RemoveGroups)
	groups = append(groups, p.AddGroups...)
	n.Groups = groups
	// SyncGroups trims, folds case-insensitive duplicates onto the spelling
	// already in use, caps the list and keeps the deprecated alias in step,
	// exactly as the single-node path does.
	n.SyncGroups()

	tags := append([]string{}, n.Tags...)
	if p.Tags != nil {
		tags = append([]string{}, (*p.Tags)...)
	}
	tags = withoutFold(tags, p.RemoveTags)
	tags = append(tags, p.AddTags...)
	n.Tags = normalizeTags(tags)

	if p.Importance != nil {
		switch *p.Importance {
		case model.ImportanceLow, model.ImportanceNormal, model.ImportanceHigh, model.ImportanceCritical:
			n.Importance = *p.Importance
		default:
			return fmt.Errorf("invalid importance %q", *p.Importance)
		}
	}
	if p.Enabled != nil {
		n.Enabled = *p.Enabled
	}

	dep, present, err := p.dependency()
	if err != nil {
		return err
	}
	if present {
		if dep != nil && *dep == n.ID {
			return fmt.Errorf("%s cannot depend on itself", n.Name)
		}
		if dep != nil {
			if err := s.checkDependencyCycle(ctx, n.ID, *dep); err != nil {
				return fmt.Errorf("%s: %v", n.Name, err)
			}
		}
		n.DependsOnNode = dep
	}
	return nil
}

// withoutFold drops from list every entry that matches one of remove, ignoring
// case and surrounding space — the same way groups and tags are matched
// everywhere else.
func withoutFold(list, remove []string) []string {
	if len(remove) == 0 {
		return list
	}
	drop := map[string]bool{}
	for _, r := range remove {
		drop[strings.ToLower(strings.TrimSpace(r))] = true
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		if !drop[strings.ToLower(strings.TrimSpace(v))] {
			out = append(out, v)
		}
	}
	return out
}

// applyCheckPatch merges the patch into one check. Validation of the result is
// the caller's job, so that it can name the check in the message.
func applyCheckPatch(p *bulkCheckPatch, c *model.Check) error {
	if p.IntervalSeconds != nil {
		c.IntervalSeconds = *p.IntervalSeconds
	}
	if p.TimeoutSeconds != nil {
		c.TimeoutSeconds = *p.TimeoutSeconds
	}
	if p.Retries != nil {
		c.Retries = *p.Retries
	}
	if p.FailureThreshold != nil {
		c.FailureThreshold = *p.FailureThreshold
	}
	if p.Enabled != nil {
		c.Enabled = *p.Enabled
	}
	if len(p.Alerts) > 0 {
		if string(bytes.TrimSpace(p.Alerts)) == "null" {
			c.Alerts = nil
		} else {
			dec := json.NewDecoder(bytes.NewReader(p.Alerts))
			dec.DisallowUnknownFields()
			var a model.AlertOverride
			if err := dec.Decode(&a); err != nil {
				return fmt.Errorf("alerts: %v", err)
			}
			c.Alerts = &a
		}
	}
	if len(p.Config) == 0 {
		return nil
	}
	var unknown []string
	for k := range p.Config {
		raw := p.Config[k]
		var err error
		switch k {
		case "latencyWarnMs":
			err = json.Unmarshal(raw, &c.Config.LatencyWarnMS)
		case "packetLossWarnPct":
			err = json.Unmarshal(raw, &c.Config.PacketLossWarnPct)
		case "certWarnDays":
			err = json.Unmarshal(raw, &c.Config.CertWarnDays)
		case "pingMethod":
			err = json.Unmarshal(raw, &c.Config.PingMethod)
		case "metricThresholds":
			// Hardware thresholds, merged by metric key: an entry in the
			// patch replaces the check's entry for that key and leaves the
			// rest alone, so "raise the disk warning on every NAS" does not
			// also reset their memory thresholds. Only a hardware check has
			// these; on any other type the key means nothing and is skipped.
			if c.Type != model.CheckSystem {
				continue
			}
			var list []model.MetricThreshold
			if err = json.Unmarshal(raw, &list); err == nil {
				err = model.ValidateMetricThresholds(list)
			}
			if err == nil {
				c.Config.NormalizeMetricThresholds()
				c.Config.MetricThresholds = mergeMetricThresholds(c.Config.MetricThresholds, list)
			}
		default:
			unknown = append(unknown, k)
			continue
		}
		if err != nil {
			return fmt.Errorf("config.%s: %v", k, err)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("unknown config key(s) %s; a bulk edit may set %s",
			strings.Join(quoteAll(unknown), ", "), strings.Join(quoteAll(bulkConfigKeys), ", "))
	}
	return nil
}

// mergeMetricThresholds replaces, in the check's list, every entry whose key
// the patch names, and appends the keys it did not have. An entry in the
// patch with neither level clears that key's thresholds. The result is sorted
// the way the editor lists them.
func mergeMetricThresholds(have, patch []model.MetricThreshold) []model.MetricThreshold {
	out := make([]model.MetricThreshold, 0, len(have)+len(patch))
	replaced := map[string]bool{}
	for _, p := range patch {
		replaced[strings.TrimSpace(p.Metric)] = true
	}
	for _, t := range have {
		if !replaced[t.Metric] {
			out = append(out, t)
		}
	}
	for _, p := range patch {
		p.Metric = strings.TrimSpace(p.Metric)
		out = append(out, p)
	}
	model.SortMetricThresholds(out)
	return out
}

// ---- saying what happened ----

// describeNodePatch turns the node half of a patch into the short phrases the
// response and the audit entry both use. The phrasing is the screen's: an
// arrow for a value replaced, a plus or minus for a list added to or taken
// from.
func describeNodePatch(p *bulkNodePatch, byNode map[int64]model.Node) []bulkChange {
	var out []bulkChange
	add := func(text string) { out = append(out, bulkChange{text: text, nodes: true}) }
	if p.Groups != nil {
		if list := model.NormalizeGroups(*p.Groups); len(list) > 0 {
			add("groups → " + strings.Join(list, ", "))
		} else {
			add("groups cleared")
		}
	}
	if len(p.AddGroups) > 0 {
		add("groups +" + strings.Join(model.NormalizeGroups(p.AddGroups), ", +"))
	}
	if len(p.RemoveGroups) > 0 {
		add("groups −" + strings.Join(model.NormalizeGroups(p.RemoveGroups), ", −"))
	}
	if p.Tags != nil {
		if list := normalizeTags(*p.Tags); len(list) > 0 {
			add("tags → " + strings.Join(list, ", "))
		} else {
			add("tags cleared")
		}
	}
	if len(p.AddTags) > 0 {
		add("tags +" + strings.Join(normalizeTags(p.AddTags), ", +"))
	}
	if len(p.RemoveTags) > 0 {
		add("tags −" + strings.Join(normalizeTags(p.RemoveTags), ", −"))
	}
	if p.Importance != nil {
		add("importance → " + string(*p.Importance))
	}
	if p.Enabled != nil {
		add(map[bool]string{true: "enabled", false: "disabled"}[*p.Enabled])
	}
	if dep, present, err := p.dependency(); err == nil && present {
		switch {
		case dep == nil:
			add("dependency cleared")
		case byNode[*dep].Name != "":
			add("depends on → " + byNode[*dep].Name)
		default:
			add(fmt.Sprintf("depends on → node %d", *dep))
		}
	}
	return out
}

// describeCheckPatch does the same for the check half.
func describeCheckPatch(p *bulkCheckPatch) []bulkChange {
	var out []bulkChange
	add := func(text string) { out = append(out, bulkChange{text: text}) }
	if p.IntervalSeconds != nil {
		add(fmt.Sprintf("interval → %d s", *p.IntervalSeconds))
	}
	if p.TimeoutSeconds != nil {
		add(fmt.Sprintf("timeout → %d s", *p.TimeoutSeconds))
	}
	if p.Retries != nil {
		add(fmt.Sprintf("retries → %d", *p.Retries))
	}
	if p.FailureThreshold != nil {
		if *p.FailureThreshold == 0 {
			add("failures before down → global default")
		} else {
			add(fmt.Sprintf("failures before down → %d", *p.FailureThreshold))
		}
	}
	if p.Enabled != nil {
		add(map[bool]string{true: "enabled", false: "disabled"}[*p.Enabled])
	}
	if len(p.Alerts) > 0 {
		if string(bytes.TrimSpace(p.Alerts)) == "null" {
			add("alert overrides cleared")
		} else {
			add("alert overrides replaced")
		}
	}
	for _, k := range bulkConfigKeys {
		raw, ok := p.Config[k]
		if !ok {
			continue
		}
		value := strings.Trim(string(bytes.TrimSpace(raw)), `"`)
		switch k {
		case "latencyWarnMs":
			add("latency warning → " + value + " ms")
		case "packetLossWarnPct":
			add("packet loss warning → " + value + " %")
		case "certWarnDays":
			add("certificate warning → " + value + " days")
		case "pingMethod":
			if value == "" {
				add("ping method → global setting")
			} else {
				add("ping method → " + value)
			}
		case "metricThresholds":
			var list []model.MetricThreshold
			_ = json.Unmarshal(raw, &list)
			for _, t := range list {
				level := func(v *float64) string {
					if v == nil {
						return "off"
					}
					return strconv.FormatFloat(*v, 'f', -1, 64)
				}
				add(fmt.Sprintf("%s thresholds → warning %s, critical %s", t.Metric, level(t.Warn), level(t.Crit)))
			}
		}
	}
	return out
}

// bulkTitle is the one line the timeline shows: what changed and on how many
// things, e.g. "Bulk edit: interval → 120 s on 14 checks; tags +critical on 5
// nodes". It is capped, because an audit entry is a sentence and not a report.
func bulkTitle(changes []bulkChange, res store.BulkResult) string {
	parts := make([]string, 0, len(changes))
	for _, c := range changes {
		n, word := res.Checks, "check"
		if c.nodes {
			n, word = res.Nodes, "node"
		}
		parts = append(parts, fmt.Sprintf("%s on %d %s%s", c.text, n, word, map[bool]string{true: "", false: "s"}[n == 1]))
	}
	title := "Bulk edit: " + strings.Join(parts, "; ")
	if len(title) > 220 {
		title = title[:219] + "…"
	}
	return title
}

func quoteAll(vs []string) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, fmt.Sprintf("%q", v))
	}
	return out
}

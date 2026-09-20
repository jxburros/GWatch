package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/jxburros/GWatch/mcp/internal/gwatch"
)

// maxPoints caps how many history points a single tool answer carries. A year
// of daily rollups is already under this, but 24h of raw ping results is not:
// striding keeps the shape of the series without handing a language model tens
// of thousands of numbers.
const maxPoints = 200

// maxIncidents caps the incident and attention lists in the overview.
const maxIncidents = 20

func (s *Set) readTools() []Tool {
	return []Tool{
		{
			Name: "gwatch_overview",
			Description: "The current state of everything GWatch monitors: the up/degraded/down/unknown tally, " +
				"per-group status, the checks that need attention, open incidents and expiring certificates. " +
				"Start here when asked how the network is doing.",
			InputSchema: object(map[string]any{
				"limit": integer("How many attention items and incidents to include (default 20, max 100)."),
			}),
			Handler: s.overview,
		},
		{
			Name: "gwatch_list_nodes",
			Description: "List the monitored nodes with their current status and a one-line state per check. " +
				"Optional group, tag and status filters narrow the list; a node in several groups matches any one of them. " +
				"Use gwatch_get_node for a node's full configuration.",
			InputSchema: object(map[string]any{
				"group":  str("Only nodes that belong to this group (exact, case-insensitive; a node may be in several)."),
				"tag":    str("Only nodes carrying this tag (exact, case-insensitive)."),
				"status": enum("Only nodes in this status.", "up", "degraded", "down", "unknown", "paused", "maintenance"),
				"q":      str("Case-insensitive substring matched against the node name and host."),
			}),
			Handler: s.listNodes,
		},
		{
			Name:        "gwatch_get_node",
			Description: "One node in full: its stored configuration, every check with its config, and the live state of each check.",
			InputSchema: object(map[string]any{
				"id": integer("The node id, as returned by gwatch_list_nodes."),
			}, "id"),
			Handler: s.getNode,
		},
		{
			Name:        "gwatch_check_results",
			Description: "The most recent individual results of one check, newest first — latency, status, message and error for each run.",
			InputSchema: object(map[string]any{
				"checkId": integer("The check id, as returned by gwatch_list_nodes or gwatch_get_node."),
				"limit":   integer("How many results to return (default 50, max 500)."),
			}, "checkId"),
			Handler: s.checkResults,
		},
		{
			Name: "gwatch_history",
			Description: "Aggregated history for one or more checks over a time range: availability, average/min/max latency " +
				"and a downsampled series of points. Use this for trends and for questions like \"was it slow last night\".",
			InputSchema: object(map[string]any{
				"checkId":  integer("A single check id. Use checkIds for several."),
				"checkIds": arrayOf(map[string]any{"type": "integer"}, "Several check ids to compare in one answer."),
				"range":    enum("The time range to cover (default 24h).", "1h", "24h", "7d", "30d", "1y"),
				"maxPoints": integer(fmt.Sprintf(
					"Cap on the number of points per series; the series is strided down to fit (default and maximum %d).", maxPoints)),
			}),
			Handler: s.history,
		},
		{
			Name: "gwatch_events",
			Description: "The incident and activity timeline, newest first: outages, recoveries, warnings, certificate warnings, " +
				"maintenance, notes and configuration changes. Filter by node, check, type, free text or time window.",
			InputSchema: object(map[string]any{
				"nodeId":  integer("Only events for this node."),
				"checkId": integer("Only events for this check."),
				"type": str("Only events of this type, e.g. down, recovered, warning, cert_warning, note, config_changed. " +
					"A type filter also includes its counterpart (down also returns recovered) unless exact is true."),
				"exact": boolean("Match the type exactly instead of also including its counterpart."),
				"q":     str("Case-insensitive search over the title, detail, node name and check name."),
				"since": str("Only events at or after this time (RFC 3339, 2006-01-02T15:04 or 2006-01-02)."),
				"until": str("Only events at or before this time (same formats as since)."),
				"limit": integer("How many events to return (default 100, max 500)."),
			}),
			Handler: s.events,
		},
		{
			Name: "gwatch_health",
			Description: "The health of the GWatch service itself: whether the scheduler is running, when it last ran a check, " +
				"how many checks exist, database size and any recent internal errors. This is about the monitor, not the network.",
			InputSchema: object(map[string]any{}),
			Handler:     s.health,
		},
		{
			Name:        "gwatch_templates",
			Description: "The node templates GWatch ships (website, home server, router, API endpoint, TCP service, DNS) with the checks each one creates. Useful before gwatch_create_node.",
			InputSchema: object(map[string]any{}),
			Handler:     s.templates,
		},
		{
			Name: "gwatch_groups",
			Description: "The groups and tags in use, with how many nodes carry each. A node in several groups is counted " +
				"in each of them, so the group counts can add up to more than the number of nodes. Useful for filtering gwatch_list_nodes.",
			InputSchema: object(map[string]any{}),
			Handler:     s.groups,
		},
	}
}

// ---- overview ------------------------------------------------------------

func (s *Set) overview(ctx context.Context, args json.RawMessage) (Result, error) {
	var in struct {
		Limit int `json:"limit"`
	}
	if err := decode(args, &in); err != nil {
		return Result{}, err
	}
	limit := clamp(in.Limit, maxIncidents, 1, 100)

	var ov gwatch.Overview
	if err := s.client.Get(ctx, "/overview", nil, &ov); err != nil {
		return Result{}, err
	}
	out := map[string]any{
		"summary":     ov.Summary,
		"groups":      ov.Groups,
		"attention":   head(ov.Attention, limit),
		"incidents":   head(ov.Incidents, limit),
		"generatedAt": ov.GeneratedAt,
	}
	if len(ov.CertWarnings) > 0 {
		out["certWarnings"] = head(ov.CertWarnings, limit)
	}
	sum := ov.Summary
	line := fmt.Sprintf("%d of %d checks up", sum.Up, sum.Total)
	var bad []string
	if sum.Down > 0 {
		bad = append(bad, fmt.Sprintf("%d down", sum.Down))
	}
	if sum.Degraded > 0 {
		bad = append(bad, fmt.Sprintf("%d degraded", sum.Degraded))
	}
	if sum.Unknown > 0 {
		bad = append(bad, fmt.Sprintf("%d unknown", sum.Unknown))
	}
	if sum.Maintenance > 0 {
		bad = append(bad, fmt.Sprintf("%d in maintenance", sum.Maintenance))
	}
	if len(bad) > 0 {
		line += "; " + strings.Join(bad, ", ")
	}
	line += fmt.Sprintf(". %s needing attention, %s open.",
		plural(len(ov.Attention), "check", "checks"), plural(len(ov.Incidents), "incident", "incidents"))
	return Result{Summary: line, Data: out}, nil
}

// ---- nodes ---------------------------------------------------------------

// nodeSummary is the trimmed node shape gwatch_list_nodes returns.
type nodeSummary struct {
	ID     int64          `json:"id"`
	Name   string         `json:"name"`
	Host   string         `json:"host"`
	Groups []string       `json:"groups,omitempty"`
	Tags   []string       `json:"tags,omitempty"`
	Status string         `json:"status"`
	Checks []checkSummary `json:"checks"`
}

type checkSummary struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Status      string `json:"status"`
	LastMessage string `json:"lastMessage,omitempty"`
}

func (s *Set) listNodes(ctx context.Context, args json.RawMessage) (Result, error) {
	var in struct {
		Group  string `json:"group"`
		Tag    string `json:"tag"`
		Status string `json:"status"`
		Q      string `json:"q"`
	}
	if err := decode(args, &in); err != nil {
		return Result{}, err
	}
	var nodes []gwatch.Node
	if err := s.client.Get(ctx, "/nodes", nil, &nodes); err != nil {
		return Result{}, err
	}

	// The filters are applied here rather than as query parameters: GWatch's
	// /nodes route takes none, and doing it client-side keeps the tool honest
	// about what it is actually asking the server for.
	out := make([]nodeSummary, 0, len(nodes))
	for _, n := range nodes {
		// A node in several groups matches a filter naming any one of them.
		if in.Group != "" && !n.InGroup(in.Group) {
			continue
		}
		if in.Status != "" && !strings.EqualFold(n.Status, in.Status) {
			continue
		}
		if in.Tag != "" && !hasTag(n.Tags, in.Tag) {
			continue
		}
		if in.Q != "" {
			q := strings.ToLower(in.Q)
			if !strings.Contains(strings.ToLower(n.Name), q) && !strings.Contains(strings.ToLower(n.Host), q) {
				continue
			}
		}
		ns := nodeSummary{ID: n.ID, Name: n.Name, Host: n.Host, Groups: n.GroupList(), Tags: n.Tags, Status: n.Status}
		for _, c := range n.Checks {
			cs := checkSummary{ID: c.ID, Name: c.Name, Type: c.Type}
			if st, ok := n.StateByCheck[strconv.FormatInt(c.ID, 10)]; ok {
				cs.Status, cs.LastMessage = st.Status, st.LastMessage
			}
			if !c.Enabled {
				cs.Status = "paused"
			}
			ns.Checks = append(ns.Checks, cs)
		}
		out = append(out, ns)
	}

	down := 0
	for _, n := range out {
		if n.Status == "down" || n.Status == "degraded" {
			down++
		}
	}
	line := fmt.Sprintf("%s", plural(len(out), "node", "nodes"))
	if f := describeFilters(in.Group, in.Tag, in.Status, in.Q); f != "" {
		line += " " + f
	}
	if down > 0 {
		line += fmt.Sprintf("; %d not fully up", down)
	}
	return Result{Summary: line + ".", Data: map[string]any{"nodes": out}}, nil
}

func describeFilters(group, tag, status, q string) string {
	var parts []string
	if group != "" {
		parts = append(parts, "in group "+strconv.Quote(group))
	}
	if tag != "" {
		parts = append(parts, "tagged "+strconv.Quote(tag))
	}
	if status != "" {
		parts = append(parts, "with status "+status)
	}
	if q != "" {
		parts = append(parts, "matching "+strconv.Quote(q))
	}
	return strings.Join(parts, ", ")
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if strings.EqualFold(strings.TrimSpace(t), want) {
			return true
		}
	}
	return false
}

func (s *Set) getNode(ctx context.Context, args json.RawMessage) (Result, error) {
	var in struct {
		ID int64 `json:"id"`
	}
	if err := decode(args, &in); err != nil {
		return Result{}, err
	}
	if in.ID <= 0 {
		return Result{}, usage("id is required and must be a positive node id")
	}
	// Passed through verbatim: a caller asking for the full node wants every
	// field GWatch stores, including ones this client does not model.
	var node map[string]any
	if err := s.client.Get(ctx, "/nodes/"+strconv.FormatInt(in.ID, 10), nil, &node); err != nil {
		return Result{}, err
	}
	name, _ := node["name"].(string)
	status, _ := node["status"].(string)
	checks := 0
	if cs, ok := node["checks"].([]any); ok {
		checks = len(cs)
	}
	return Result{
		Summary: fmt.Sprintf("Node %d %q is %s with %s.", in.ID, name, orUnknown(status), plural(checks, "check", "checks")),
		Data:    node,
	}, nil
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// ---- results -------------------------------------------------------------

func (s *Set) checkResults(ctx context.Context, args json.RawMessage) (Result, error) {
	var in struct {
		CheckID int64 `json:"checkId"`
		Limit   int   `json:"limit"`
	}
	if err := decode(args, &in); err != nil {
		return Result{}, err
	}
	if in.CheckID <= 0 {
		return Result{}, usage("checkId is required and must be a positive check id")
	}
	limit := clamp(in.Limit, 50, 1, 500)
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	var results []gwatch.Result
	if err := s.client.Get(ctx, "/checks/"+strconv.FormatInt(in.CheckID, 10)+"/results", q, &results); err != nil {
		return Result{}, err
	}
	failures := 0
	for _, r := range results {
		if !r.Success {
			failures++
		}
	}
	line := fmt.Sprintf("%s for check %d, newest first; %d failed.", plural(len(results), "result", "results"), in.CheckID, failures)
	return Result{Summary: line, Data: map[string]any{"checkId": in.CheckID, "results": results}}, nil
}

// ---- history -------------------------------------------------------------

func (s *Set) history(ctx context.Context, args json.RawMessage) (Result, error) {
	var in struct {
		CheckID   int64   `json:"checkId"`
		CheckIDs  []int64 `json:"checkIds"`
		Range     string  `json:"range"`
		MaxPoints int     `json:"maxPoints"`
	}
	if err := decode(args, &in); err != nil {
		return Result{}, err
	}
	ids := append([]int64(nil), in.CheckIDs...)
	if in.CheckID > 0 {
		ids = append(ids, in.CheckID)
	}
	if len(ids) == 0 {
		return Result{}, usage("give checkId, or checkIds for several checks")
	}
	rng := strings.TrimSpace(in.Range)
	if rng == "" {
		rng = "24h"
	}
	switch rng {
	case "1h", "24h", "7d", "30d", "1y":
	default:
		return Result{}, usage("range must be one of 1h, 24h, 7d, 30d, 1y (got %q)", rng)
	}
	cap := clamp(in.MaxPoints, maxPoints, 2, maxPoints)

	q := url.Values{"range": {rng}}
	for _, id := range ids {
		if id <= 0 {
			return Result{}, usage("check ids must be positive (got %d)", id)
		}
		q.Add("checkId", strconv.FormatInt(id, 10))
	}
	// /history/multi always answers with an array, whatever the number of ids,
	// which keeps this tool's output shape stable.
	var series []gwatch.HistorySeries
	if err := s.client.Get(ctx, "/history/multi", q, &series); err != nil {
		return Result{}, err
	}
	trimmed := 0
	for i := range series {
		if n := len(series[i].Points); n > cap {
			series[i].Points = stride(series[i].Points, cap)
			trimmed += n - len(series[i].Points)
		}
	}
	var names []string
	for _, ser := range series {
		names = append(names, fmt.Sprintf("%s/%s %.1f%% available", ser.NodeName, ser.CheckName, ser.Summary.Availability))
	}
	line := fmt.Sprintf("%s over %s: %s.", plural(len(series), "series", "series"), rng, strings.Join(names, "; "))
	if trimmed > 0 {
		line += fmt.Sprintf(" %d points were dropped by even striding to stay under %d per series.", trimmed, cap)
	}
	return Result{Summary: line, Data: map[string]any{"range": rng, "series": series}}, nil
}

// stride reduces points to at most n by taking an evenly spaced subset, always
// keeping the first and last point so the range still reads correctly.
func stride[T any](points []T, n int) []T {
	if len(points) <= n || n < 2 {
		return points
	}
	out := make([]T, 0, n)
	last := len(points) - 1
	for i := 0; i < n-1; i++ {
		idx := i * last / (n - 1)
		out = append(out, points[idx])
	}
	return append(out, points[last])
}

// ---- events --------------------------------------------------------------

func (s *Set) events(ctx context.Context, args json.RawMessage) (Result, error) {
	var in struct {
		NodeID  int64  `json:"nodeId"`
		CheckID int64  `json:"checkId"`
		Type    string `json:"type"`
		Exact   bool   `json:"exact"`
		Q       string `json:"q"`
		Since   string `json:"since"`
		Until   string `json:"until"`
		Limit   int    `json:"limit"`
	}
	if err := decode(args, &in); err != nil {
		return Result{}, err
	}
	q := url.Values{"limit": {strconv.Itoa(clamp(in.Limit, 100, 1, 500))}}
	if in.NodeID > 0 {
		q.Set("nodeId", strconv.FormatInt(in.NodeID, 10))
	}
	if in.CheckID > 0 {
		q.Set("checkId", strconv.FormatInt(in.CheckID, 10))
	}
	if t := strings.TrimSpace(in.Type); t != "" {
		q.Set("type", t)
	}
	if in.Exact {
		q.Set("exact", "1")
	}
	for k, v := range map[string]string{"q": in.Q, "since": in.Since, "until": in.Until} {
		if v = strings.TrimSpace(v); v != "" {
			q.Set(k, v)
		}
	}
	var events []gwatch.Event
	if err := s.client.Get(ctx, "/events", q, &events); err != nil {
		return Result{}, err
	}
	byType := map[string]int{}
	for _, e := range events {
		byType[e.Type]++
	}
	kinds := make([]string, 0, len(byType))
	for t := range byType {
		kinds = append(kinds, t)
	}
	sort.Slice(kinds, func(i, j int) bool {
		if byType[kinds[i]] != byType[kinds[j]] {
			return byType[kinds[i]] > byType[kinds[j]]
		}
		return kinds[i] < kinds[j]
	})
	parts := make([]string, 0, len(kinds))
	for _, t := range kinds {
		parts = append(parts, fmt.Sprintf("%d %s", byType[t], t))
	}
	line := fmt.Sprintf("%s, newest first", plural(len(events), "event", "events"))
	if len(parts) > 0 {
		line += " (" + strings.Join(parts, ", ") + ")"
	}
	return Result{Summary: line + ".", Data: map[string]any{"events": events}}, nil
}

// ---- health, templates, groups -------------------------------------------

func (s *Set) health(ctx context.Context, _ json.RawMessage) (Result, error) {
	var h gwatch.Health
	if err := s.client.Get(ctx, "/health", nil, &h); err != nil {
		return Result{}, err
	}
	state := "running"
	if !h.SchedulerRunning {
		state = "NOT running"
	}
	line := fmt.Sprintf("GWatch %s on %s: scheduler %s, %d of %d checks enabled.",
		orUnknown(h.Version), orUnknown(h.Platform), state, h.ChecksEnabled, h.ChecksTotal)
	if n := len(h.RecentErrors); n > 0 {
		line += fmt.Sprintf(" %s recorded.", plural(n, "recent internal error", "recent internal errors"))
	}
	return Result{Summary: line, Data: h}, nil
}

func (s *Set) templates(ctx context.Context, _ json.RawMessage) (Result, error) {
	var tpl []gwatch.NodeTemplate
	if err := s.client.Get(ctx, "/templates", nil, &tpl); err != nil {
		return Result{}, err
	}
	names := make([]string, 0, len(tpl))
	for _, t := range tpl {
		names = append(names, t.ID)
	}
	return Result{
		Summary: fmt.Sprintf("%s: %s.", plural(len(tpl), "node template", "node templates"), strings.Join(names, ", ")),
		Data:    map[string]any{"templates": tpl},
	}, nil
}

func (s *Set) groups(ctx context.Context, _ json.RawMessage) (Result, error) {
	var g gwatch.Groups
	if err := s.client.Get(ctx, "/groups", nil, &g); err != nil {
		return Result{}, err
	}
	return Result{
		Summary: fmt.Sprintf("%s and %s in use.", plural(len(g.Groups), "group", "groups"), plural(len(g.Tags), "tag", "tags")),
		Data:    g,
	}, nil
}

// ---- small helpers -------------------------------------------------------

func clamp(v, def, lo, hi int) int {
	if v <= 0 {
		v = def
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func head[T any](s []T, n int) []T {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// The write tools cover exactly the routes a readwrite API key is allowed to
// call: nodes and their checks, notes, and the safe "test this check" probe.
//
// There is deliberately nothing here for settings, backups, triggers,
// endpoints, users or API keys. GWatch denies those to every key whatever its
// scope, so a tool for them could only ever return 403 — and an AI assistant
// that cannot reconfigure the machine GWatch runs on is the point of the
// boundary, not a limitation to work around.

// checkInput is a check as a tool caller writes it.
type checkInput struct {
	ID               int64          `json:"id,omitempty"`
	Type             string         `json:"type"`
	Name             string         `json:"name"`
	Enabled          *bool          `json:"enabled,omitempty"`
	IntervalSeconds  int            `json:"intervalSeconds,omitempty"`
	TimeoutSeconds   int            `json:"timeoutSeconds,omitempty"`
	Retries          int            `json:"retries,omitempty"`
	FailureThreshold int            `json:"failureThreshold,omitempty"`
	Config           map[string]any `json:"config,omitempty"`
}

// toAPI renders the check the way GWatch's model expects it, filling the
// defaults a caller is likely to leave out.
func (c checkInput) toAPI() map[string]any {
	out := map[string]any{
		"type":    c.Type,
		"name":    c.Name,
		"enabled": c.Enabled == nil || *c.Enabled,
		"config":  orEmpty(c.Config),
	}
	if c.ID > 0 {
		out["id"] = c.ID
	}
	out["intervalSeconds"] = firstPositive(c.IntervalSeconds, 60)
	out["timeoutSeconds"] = firstPositive(c.TimeoutSeconds, 10)
	if c.Retries > 0 {
		out["retries"] = c.Retries
	}
	if c.FailureThreshold > 0 {
		out["failureThreshold"] = c.FailureThreshold
	}
	return out
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func firstPositive(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

func checkSchema(withID bool) map[string]any {
	props := map[string]any{
		"type": enum("The check type.",
			"ping", "http", "keyword", "json", "tcp", "dns", "cert", "custom"),
		"name":             str("A short name for the check, e.g. \"Ping\" or \"Homepage\"."),
		"enabled":          boolean("Whether the check runs (default true)."),
		"intervalSeconds":  integer("How often the check runs, in seconds (minimum 10, default 60)."),
		"timeoutSeconds":   integer("Per-attempt timeout in seconds (default 10)."),
		"retries":          integer("Immediate retries inside one run before the run counts as failed."),
		"failureThreshold": integer("Consecutive failed runs before the check is reported down (0 = the global default)."),
		"config": freeObject("Type-specific settings. Common keys: target (overrides the node host), port (tcp/cert), " +
			"method, expectedStatus, headers, body, ignoreTlsErrors (http family), keyword, keywordAbsent (keyword), " +
			"jsonPath, jsonExpected (json), recordType, dnsServer, expectedIps (dns), certWarnDays (cert), " +
			"pingCount (ping), latencyWarnMs and packetLossWarnPct (thresholds). " +
			"The custom type runs a command on the GWatch host and is refused to API keys in most installs — prefer the built-in types."),
	}
	required := []string{"type", "name"}
	if withID {
		props["id"] = integer("The id of an existing check to update. Omit to create a new one.")
	}
	return object(props, required...)
}

func (s *Set) writeTools() []Tool {
	return []Tool{
		{
			Name: "gwatch_create_node",
			Description: "Create a new monitored node together with its checks. Use gwatch_templates first to see the " +
				"shapes GWatch ships, and gwatch_test_check to validate a check's configuration before saving it.",
			Write: true,
			InputSchema: object(map[string]any{
				"name":   str("The display name of the node."),
				"host":   str("The default target for its checks: a hostname, IP address or URL."),
				"groups": arrayOf(map[string]any{"type": "string"}, "The groups the node belongs to, e.g. [\"Home Network\", \"Critical\"]. A node may be in several."),
				"group":  str("Deprecated: a single group, used only when groups is not given."),
				"tags":   arrayOf(map[string]any{"type": "string"}, "Free-form tags for filtering."),
				"notes":  str("Free-text notes stored with the node."),
				"importance": enum("How much this node matters; it drives alert prioritisation.",
					"low", "normal", "high", "critical"),
				"enabled": boolean("Whether the node's checks run (default true)."),
				"checks":  arrayOf(checkSchema(false), "The checks to create on the node."),
			}, "name", "host"),
			Handler: s.createNode,
		},
		{
			Name: "gwatch_update_node",
			Description: "Change an existing node. Only the fields given are changed; the rest are left as they are. " +
				"Giving checks REPLACES the node's whole check list — checks with an id are updated, checks without one " +
				"are created, and any existing check left out is deleted. Read the node with gwatch_get_node first.",
			Write: true,
			InputSchema: object(map[string]any{
				"id":     integer("The node id."),
				"name":   str("New display name."),
				"host":   str("New default target."),
				"groups": arrayOf(map[string]any{"type": "string"}, "Replacement list of groups the node belongs to."),
				"group":  str("Deprecated: a single group, used only when groups is not given. It replaces the node's whole group list."),
				"tags":   arrayOf(map[string]any{"type": "string"}, "Replacement tag list."),
				"notes":  str("Replacement notes."),
				"importance": enum("New importance.",
					"low", "normal", "high", "critical"),
				"enabled": boolean("Enable or disable the node."),
				"checks":  arrayOf(checkSchema(true), "Replacement check list. Omit to leave the checks untouched."),
			}, "id"),
			Handler: s.updateNode,
		},
		{
			Name: "gwatch_delete_node",
			Description: "Delete a node and everything recorded for it — its checks, their results and their history. " +
				"This cannot be undone, so it refuses to run unless confirm is true.",
			Write: true,
			InputSchema: object(map[string]any{
				"id":      integer("The node id to delete."),
				"confirm": boolean("Must be true. Without it the deletion is refused."),
			}, "id", "confirm"),
			Handler: s.deleteNode,
		},
		{
			Name:        "gwatch_set_node_enabled",
			Description: "Enable or disable a node. A disabled node keeps its configuration and history but stops running checks.",
			Write:       true,
			InputSchema: object(map[string]any{
				"id":      integer("The node id."),
				"enabled": boolean("true to resume checking, false to pause it."),
			}, "id", "enabled"),
			Handler: s.setNodeEnabled,
		},
		{
			Name:        "gwatch_run_node",
			Description: "Run every enabled check of a node right now and return the results. The results are recorded and go through alerting as usual.",
			Write:       true,
			InputSchema: object(map[string]any{
				"id": integer("The node id."),
			}, "id"),
			Handler: s.runNode,
		},
		{
			Name: "gwatch_silence_node",
			Description: "Silence alerts for every check of a node for a number of minutes. The checks keep running and " +
				"keep recording results; only the notifications are suppressed. Pass 0 minutes to unsilence.",
			Write: true,
			InputSchema: object(map[string]any{
				"id":      integer("The node id."),
				"minutes": integer("How many minutes to silence for; 0 removes an existing silence."),
			}, "id", "minutes"),
			Handler: s.silenceNode,
		},
		{
			Name: "gwatch_add_note",
			Description: "Add a note to the event timeline, optionally attached to a node — for recording what was done and why " +
				"(\"restarted the router\", \"ISP maintenance\"), so the next person reading the timeline has the context.",
			Write: true,
			InputSchema: object(map[string]any{
				"text":   str("The note text."),
				"nodeId": integer("Attach the note to this node. Omit for a note about the whole install."),
			}, "text"),
			Handler: s.addNote,
		},
		{
			Name: "gwatch_test_check",
			Description: "Run a check configuration once without saving it and return the result. Nothing is recorded and no " +
				"alerting happens. Use this to validate a check before gwatch_create_node or gwatch_update_node.",
			Write: true,
			InputSchema: object(map[string]any{
				"check":    checkSchema(false),
				"nodeHost": str("The host the check runs against when its config has no target of its own."),
			}, "check"),
			Handler: s.testCheck,
		},
	}
}

// ---- handlers ------------------------------------------------------------

func (s *Set) createNode(ctx context.Context, args json.RawMessage) (Result, error) {
	var in struct {
		Name       string       `json:"name"`
		Host       string       `json:"host"`
		Groups     []string     `json:"groups"`
		Group      string       `json:"group"`
		Tags       []string     `json:"tags"`
		Notes      string       `json:"notes"`
		Importance string       `json:"importance"`
		Enabled    *bool        `json:"enabled"`
		Checks     []checkInput `json:"checks"`
	}
	if err := decode(args, &in); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(in.Name) == "" {
		return Result{}, usage("name is required")
	}
	if strings.TrimSpace(in.Host) == "" {
		return Result{}, usage("host is required — it is the default target for the node's checks")
	}
	checks := make([]map[string]any, 0, len(in.Checks))
	for i, c := range in.Checks {
		if strings.TrimSpace(c.Type) == "" || strings.TrimSpace(c.Name) == "" {
			return Result{}, usage("checks[%d] needs both a type and a name", i)
		}
		checks = append(checks, c.toAPI())
	}
	// groups is what GWatch reads; group is sent alongside it for an older
	// GWatch, and is the one the caller gave if they used the old field.
	groups := in.Groups
	if len(groups) == 0 && strings.TrimSpace(in.Group) != "" {
		groups = []string{in.Group}
	}
	body := map[string]any{
		"name":       in.Name,
		"host":       in.Host,
		"groups":     orEmptySlice(groups),
		"group":      firstOr(groups, in.Group),
		"tags":       orEmptySlice(in.Tags),
		"notes":      in.Notes,
		"importance": orDefault(in.Importance, "normal"),
		"enabled":    in.Enabled == nil || *in.Enabled,
		"checks":     checks,
	}
	var node map[string]any
	if err := s.client.Post(ctx, "/nodes", body, &node); err != nil {
		return Result{}, err
	}
	id, _ := node["id"].(float64)
	return Result{
		Summary: fmt.Sprintf("Created node %d %q (%s) with %s.", int64(id), in.Name, in.Host, plural(len(checks), "check", "checks")),
		Data:    node,
	}, nil
}

func (s *Set) updateNode(ctx context.Context, args json.RawMessage) (Result, error) {
	// The API's PUT takes a whole node, so a partial update is a fetch, a
	// merge and a PUT. Fetching into a generic map keeps every field this
	// client does not model (per-check alert overrides, dependsOnNodeId,
	// sortOrder) instead of resetting it to a zero value.
	var in struct {
		ID         int64        `json:"id"`
		Name       *string      `json:"name"`
		Host       *string      `json:"host"`
		Groups     *[]string    `json:"groups"`
		Group      *string      `json:"group"`
		Tags       *[]string    `json:"tags"`
		Notes      *string      `json:"notes"`
		Importance *string      `json:"importance"`
		Enabled    *bool        `json:"enabled"`
		Checks     []checkInput `json:"checks"`
	}
	if err := decode(args, &in); err != nil {
		return Result{}, err
	}
	if in.ID <= 0 {
		return Result{}, usage("id is required and must be a positive node id")
	}
	path := "/nodes/" + strconv.FormatInt(in.ID, 10)
	var node map[string]any
	if err := s.client.Get(ctx, path, nil, &node); err != nil {
		return Result{}, err
	}
	// Live state the API adds on read and does not accept on write.
	for _, k := range []string{"status", "stateByCheck", "lastResults", "inMaintenance"} {
		delete(node, k)
	}

	var changed []string
	setStr := func(key string, v *string) {
		if v != nil {
			node[key] = *v
			changed = append(changed, key)
		}
	}
	setStr("name", in.Name)
	setStr("host", in.Host)
	setStr("notes", in.Notes)
	// Either field replaces the node's whole group list, so both are written:
	// leaving the old group behind would let it win on a server that reads it.
	if in.Groups != nil {
		node["groups"] = orEmptySlice(*in.Groups)
		node["group"] = firstOr(*in.Groups, "")
		changed = append(changed, "groups")
	} else if in.Group != nil {
		node["groups"] = orEmptySlice(nonEmpty(*in.Group))
		node["group"] = *in.Group
		changed = append(changed, "group")
	}
	setStr("importance", in.Importance)
	if in.Tags != nil {
		node["tags"] = orEmptySlice(*in.Tags)
		changed = append(changed, "tags")
	}
	if in.Enabled != nil {
		node["enabled"] = *in.Enabled
		changed = append(changed, "enabled")
	}
	if in.Checks != nil {
		checks := make([]map[string]any, 0, len(in.Checks))
		for i, c := range in.Checks {
			if strings.TrimSpace(c.Type) == "" || strings.TrimSpace(c.Name) == "" {
				return Result{}, usage("checks[%d] needs both a type and a name", i)
			}
			checks = append(checks, c.toAPI())
		}
		node["checks"] = checks
		changed = append(changed, fmt.Sprintf("checks (replaced with %d)", len(checks)))
	}
	if len(changed) == 0 {
		return Result{}, usage("nothing to change: give at least one of name, host, groups, tags, notes, importance, enabled or checks")
	}

	var updated map[string]any
	if err := s.client.Put(ctx, path, node, &updated); err != nil {
		return Result{}, err
	}
	name, _ := updated["name"].(string)
	return Result{
		Summary: fmt.Sprintf("Updated node %d %q: %s.", in.ID, name, strings.Join(changed, ", ")),
		Data:    updated,
	}, nil
}

func (s *Set) deleteNode(ctx context.Context, args json.RawMessage) (Result, error) {
	var in struct {
		ID      int64 `json:"id"`
		Confirm bool  `json:"confirm"`
	}
	if err := decode(args, &in); err != nil {
		return Result{}, err
	}
	if in.ID <= 0 {
		return Result{}, usage("id is required and must be a positive node id")
	}
	if !in.Confirm {
		return Result{}, usage("refusing to delete node %d: deleting a node also deletes its checks, results and history, "+
			"so this tool needs confirm: true in the arguments. Confirm with the person first", in.ID)
	}
	if err := s.client.Delete(ctx, "/nodes/"+strconv.FormatInt(in.ID, 10), nil); err != nil {
		return Result{}, err
	}
	return Result{
		Summary: fmt.Sprintf("Deleted node %d along with its checks, results and history.", in.ID),
		Data:    map[string]any{"ok": true, "id": in.ID},
	}, nil
}

func (s *Set) setNodeEnabled(ctx context.Context, args json.RawMessage) (Result, error) {
	var in struct {
		ID      int64 `json:"id"`
		Enabled *bool `json:"enabled"`
	}
	if err := decode(args, &in); err != nil {
		return Result{}, err
	}
	if in.ID <= 0 {
		return Result{}, usage("id is required and must be a positive node id")
	}
	if in.Enabled == nil {
		return Result{}, usage("enabled is required (true to resume checking, false to pause)")
	}
	var node map[string]any
	if err := s.client.Post(ctx, "/nodes/"+strconv.FormatInt(in.ID, 10)+"/enable",
		map[string]any{"enabled": *in.Enabled}, &node); err != nil {
		return Result{}, err
	}
	word := "disabled"
	if *in.Enabled {
		word = "enabled"
	}
	name, _ := node["name"].(string)
	return Result{Summary: fmt.Sprintf("Node %d %q is now %s.", in.ID, name, word), Data: node}, nil
}

func (s *Set) runNode(ctx context.Context, args json.RawMessage) (Result, error) {
	var in struct {
		ID int64 `json:"id"`
	}
	if err := decode(args, &in); err != nil {
		return Result{}, err
	}
	if in.ID <= 0 {
		return Result{}, usage("id is required and must be a positive node id")
	}
	var results []map[string]any
	if err := s.client.Post(ctx, "/nodes/"+strconv.FormatInt(in.ID, 10)+"/run", struct{}{}, &results); err != nil {
		return Result{}, err
	}
	failed := 0
	for _, r := range results {
		if ok, _ := r["success"].(bool); !ok {
			failed++
		}
	}
	return Result{
		Summary: fmt.Sprintf("Ran %s on node %d; %d failed.", plural(len(results), "check", "checks"), in.ID, failed),
		Data:    map[string]any{"nodeId": in.ID, "results": results},
	}, nil
}

func (s *Set) silenceNode(ctx context.Context, args json.RawMessage) (Result, error) {
	var in struct {
		ID      int64 `json:"id"`
		Minutes *int  `json:"minutes"`
	}
	if err := decode(args, &in); err != nil {
		return Result{}, err
	}
	if in.ID <= 0 {
		return Result{}, usage("id is required and must be a positive node id")
	}
	if in.Minutes == nil {
		return Result{}, usage("minutes is required (0 removes an existing silence)")
	}
	if *in.Minutes < 0 {
		return Result{}, usage("minutes cannot be negative")
	}
	var node map[string]any
	if err := s.client.Post(ctx, "/nodes/"+strconv.FormatInt(in.ID, 10)+"/silence",
		map[string]any{"minutes": *in.Minutes}, &node); err != nil {
		return Result{}, err
	}
	line := fmt.Sprintf("Silenced every check of node %d for %d minutes; the checks keep running.", in.ID, *in.Minutes)
	if *in.Minutes == 0 {
		line = fmt.Sprintf("Removed the silence on node %d.", in.ID)
	}
	return Result{Summary: line, Data: node}, nil
}

func (s *Set) addNote(ctx context.Context, args json.RawMessage) (Result, error) {
	var in struct {
		Text   string `json:"text"`
		NodeID int64  `json:"nodeId"`
	}
	if err := decode(args, &in); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(in.Text) == "" {
		return Result{}, usage("text is required")
	}
	body := map[string]any{"text": in.Text, "nodeId": nil}
	if in.NodeID > 0 {
		body["nodeId"] = in.NodeID
	}
	var event map[string]any
	if err := s.client.Post(ctx, "/events/note", body, &event); err != nil {
		return Result{}, err
	}
	where := "the timeline"
	if in.NodeID > 0 {
		where = fmt.Sprintf("node %d's timeline", in.NodeID)
	}
	return Result{Summary: "Added a note to " + where + ".", Data: event}, nil
}

func (s *Set) testCheck(ctx context.Context, args json.RawMessage) (Result, error) {
	var in struct {
		Check    *checkInput `json:"check"`
		NodeHost string      `json:"nodeHost"`
	}
	if err := decode(args, &in); err != nil {
		return Result{}, err
	}
	if in.Check == nil {
		return Result{}, usage("check is required")
	}
	if strings.TrimSpace(in.Check.Type) == "" {
		return Result{}, usage("check.type is required")
	}
	body := map[string]any{"check": in.Check.toAPI(), "nodeHost": in.NodeHost}
	var res map[string]any
	if err := s.client.Post(ctx, "/checks/test", body, &res); err != nil {
		return Result{}, err
	}
	status, _ := res["status"].(string)
	msg, _ := res["message"].(string)
	line := fmt.Sprintf("Test run of a %s check: %s", in.Check.Type, orUnknown(status))
	if msg != "" {
		line += " — " + msg
	}
	return Result{Summary: line + ". Nothing was recorded.", Data: res}, nil
}

func orEmptySlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// firstOr returns the first group of a list, or the fallback when it is empty.
// It fills the deprecated single group field, which GWatch derives the same way.
func firstOr(list []string, fallback string) string {
	if len(list) > 0 {
		return list[0]
	}
	return fallback
}

// nonEmpty turns one group name into a list of nought or one, so clearing a
// node's group with an empty string clears the list rather than adding a blank.
func nonEmpty(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return []string{v}
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

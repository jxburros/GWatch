package gwatch

import (
	"encoding/json"
	"strings"
	"time"
)

// The types below mirror the JSON shapes documented in docs/API.md (which in
// turn mirror internal/model/model.go in the GWatch module). They are copied
// on purpose rather than imported: this module must not depend on GWatch's
// internal packages. Only the fields the MCP tools actually use are declared;
// unknown fields are ignored on decode, so a new field in GWatch cannot break
// this client.
//
// Where a payload is passed straight through to the model (a full node, a
// check config) the raw JSON is kept instead, so nothing is silently dropped
// on a fetch-merge-PUT round trip.

// Check is one monitor attached to a node.
type Check struct {
	ID              int64          `json:"id"`
	NodeID          int64          `json:"nodeId"`
	Type            string         `json:"type"`
	Name            string         `json:"name"`
	Enabled         bool           `json:"enabled"`
	IntervalSeconds int            `json:"intervalSeconds"`
	TimeoutSeconds  int            `json:"timeoutSeconds"`
	Config          map[string]any `json:"config,omitempty"`
}

// CheckState is the live state of a check.
type CheckState struct {
	CheckID             int64      `json:"checkId"`
	Status              string     `json:"status"`
	ConsecutiveFailures int        `json:"consecutiveFailures"`
	LastRunAt           *time.Time `json:"lastRunAt"`
	LastChangeAt        *time.Time `json:"lastChangeAt"`
	NextRunAt           *time.Time `json:"nextRunAt"`
	LastMessage         string     `json:"lastMessage"`
	LastLatencyMS       *float64   `json:"lastLatencyMs"`
	SilencedUntil       *time.Time `json:"silencedUntil"`
	AffectedByNodeName  string     `json:"affectedByNodeName,omitempty"`
}

// Node is a device, service or endpoint with its checks. The API adds live
// state (status, stateByCheck, inMaintenance) to the stored record.
type Node struct {
	ID     int64    `json:"id"`
	Name   string   `json:"name"`
	Host   string   `json:"host"`
	Groups []string `json:"groups"`
	// Group is the first of Groups. GWatch keeps sending it for one release
	// so clients written before a node could be in several groups still work.
	// Deprecated: read Groups.
	Group        string                `json:"group"`
	Tags         []string              `json:"tags"`
	Notes        string                `json:"notes,omitempty"`
	Importance   string                `json:"importance"`
	Enabled      bool                  `json:"enabled"`
	Template     string                `json:"template,omitempty"`
	Checks       []Check               `json:"checks"`
	Status       string                `json:"status"`
	StateByCheck map[string]CheckState `json:"stateByCheck,omitempty"`
	Maintenance  bool                  `json:"inMaintenance"`
}

// GroupList returns the groups the node belongs to. An older GWatch answers
// with group alone, and this reads that as the one group it is.
func (n Node) GroupList() []string {
	if len(n.Groups) > 0 {
		return n.Groups
	}
	if g := strings.TrimSpace(n.Group); g != "" {
		return []string{g}
	}
	return nil
}

// InGroup reports whether the node is in the named group, matching any of its
// groups and ignoring case.
func (n Node) InGroup(group string) bool {
	group = strings.TrimSpace(group)
	if group == "" {
		return false
	}
	for _, g := range n.GroupList() {
		if strings.EqualFold(strings.TrimSpace(g), group) {
			return true
		}
	}
	return false
}

// Result is one observation produced by running a check.
type Result struct {
	ID        int64     `json:"id"`
	CheckID   int64     `json:"checkId"`
	Timestamp time.Time `json:"ts"`
	Success   bool      `json:"success"`
	Status    string    `json:"status"`
	Message   string    `json:"message"`
	Error     string    `json:"error,omitempty"`
	LatencyMS *float64  `json:"latencyMs"`
	LossPct   *float64  `json:"lossPct,omitempty"`
	Attempts  int       `json:"attempts"`
	Warnings  []string  `json:"warnings,omitempty"`
}

// Event is one entry in the incident timeline.
type Event struct {
	ID        int64     `json:"id"`
	Timestamp time.Time `json:"ts"`
	Type      string    `json:"type"`
	NodeID    *int64    `json:"nodeId"`
	CheckID   *int64    `json:"checkId"`
	NodeName  string    `json:"nodeName,omitempty"`
	CheckName string    `json:"checkName,omitempty"`
	Title     string    `json:"title"`
	Detail    string    `json:"detail"`
	Actor     string    `json:"actor,omitempty"`
}

// Summary is the up/down tally carried by the overview.
type Summary struct {
	Up          int `json:"up"`
	Degraded    int `json:"degraded"`
	Down        int `json:"down"`
	Unknown     int `json:"unknown"`
	Paused      int `json:"paused"`
	Maintenance int `json:"maintenance"`
	Total       int `json:"total"`
}

// GroupStatus is one row of the overview's per-group tally.
type GroupStatus struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Up       int    `json:"up"`
	Degraded int    `json:"degraded"`
	Down     int    `json:"down"`
	Unknown  int    `json:"unknown"`
	Total    int    `json:"total"`
}

// AttentionItem is one check the overview says a human should look at.
type AttentionItem struct {
	NodeID     int64      `json:"nodeId"`
	NodeName   string     `json:"nodeName"`
	CheckID    int64      `json:"checkId"`
	CheckName  string     `json:"checkName"`
	Status     string     `json:"status"`
	Message    string     `json:"message"`
	Since      *time.Time `json:"since"`
	AffectedBy string     `json:"affectedBy,omitempty"`
}

// CertWarning is an expiring certificate reported by the overview.
type CertWarning struct {
	NodeID        int64  `json:"nodeId"`
	NodeName      string `json:"nodeName"`
	CheckID       int64  `json:"checkId"`
	CheckName     string `json:"checkName"`
	DaysRemaining int    `json:"daysRemaining"`
	Subject       string `json:"subject,omitempty"`
}

// Overview is GET /api/v1/overview.
type Overview struct {
	Summary      Summary         `json:"summary"`
	Groups       []GroupStatus   `json:"groups"`
	Incidents    []Event         `json:"incidents"`
	CertWarnings []CertWarning   `json:"certWarnings"`
	Attention    []AttentionItem `json:"attention"`
	GeneratedAt  time.Time       `json:"generatedAt"`
}

// HistoryPoint is one chart point (a raw result or a rollup bucket).
type HistoryPoint struct {
	Timestamp    time.Time `json:"ts"`
	AvgMS        *float64  `json:"avgMs"`
	MinMS        *float64  `json:"minMs"`
	MaxMS        *float64  `json:"maxMs"`
	JitterMS     *float64  `json:"jitterMs"`
	LossPct      *float64  `json:"lossPct"`
	Availability float64   `json:"availability"`
	Count        int       `json:"count"`
	Failures     int       `json:"failures"`
}

// HistorySummary aggregates a series.
type HistorySummary struct {
	Availability float64  `json:"availability"`
	AvgMS        *float64 `json:"avgMs"`
	MinMS        *float64 `json:"minMs"`
	MaxMS        *float64 `json:"maxMs"`
	Count        int      `json:"count"`
	Failures     int      `json:"failures"`
}

// HistorySeries is GET /api/v1/history for one check.
type HistorySeries struct {
	CheckID    int64          `json:"checkId"`
	CheckName  string         `json:"checkName"`
	NodeName   string         `json:"nodeName"`
	CheckType  string         `json:"checkType"`
	Range      string         `json:"range"`
	Source     string         `json:"source"`
	BucketSecs int            `json:"bucketSeconds"`
	From       time.Time      `json:"from"`
	To         time.Time      `json:"to"`
	Points     []HistoryPoint `json:"points"`
	Summary    HistorySummary `json:"summary"`
}

// Health is GET /api/v1/health, trimmed to the self-observability fields that
// mean something to a remote caller. The machine-local paths and sizes are
// kept because they are the ones that explain "the monitor itself is unwell".
type Health struct {
	Version          string     `json:"version"`
	ServiceMode      string     `json:"serviceMode"`
	ServiceRunning   bool       `json:"serviceRunning"`
	SchedulerRunning bool       `json:"schedulerRunning"`
	StartedAt        time.Time  `json:"startedAt"`
	UptimeSeconds    int64      `json:"uptimeSeconds"`
	Now              time.Time  `json:"now"`
	LastCheckAt      *time.Time `json:"lastCheckAt"`
	NextCheckAt      *time.Time `json:"nextCheckAt"`
	ChecksTotal      int        `json:"checksTotal"`
	ChecksEnabled    int        `json:"checksEnabled"`
	ChecksRunning    int        `json:"checksRunning"`
	DatabaseBytes    int64      `json:"databaseBytes"`
	AlertsEnabled    bool       `json:"alertsEnabled"`
	SMTPConfigured   bool       `json:"smtpConfigured"`
	LastAlertError   string     `json:"lastAlertError,omitempty"`
	Platform         string     `json:"platform,omitempty"`
	RecentErrors     []Event    `json:"recentErrors"`
}

// NodeTemplate prefills a node and its checks.
type NodeTemplate struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Node        Node    `json:"node"`
	Checks      []Check `json:"checks"`
}

// NameCount is one entry of GET /api/v1/groups.
type NameCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// Groups is GET /api/v1/groups.
type Groups struct {
	Groups []NameCount `json:"groups"`
	Tags   []NameCount `json:"tags"`
}

// RawNode is a node as GWatch sent it, kept verbatim so gwatch_update_node can
// fetch, merge and PUT without dropping fields this client does not model.
type RawNode map[string]json.RawMessage

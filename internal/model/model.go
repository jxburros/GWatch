// Package model holds the shared data types used by the storage layer, the
// monitoring engine, the check runners, the HTTP API and the web interface.
//
// Everything here is plain data. JSON tags define the wire format used by the
// localhost API, so changes must be mirrored in web/app.js.
package model

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// CheckType identifies which runner executes a check.
type CheckType string

const (
	CheckPing    CheckType = "ping"    // ICMP reachability and latency
	CheckHTTP    CheckType = "http"    // HTTP/S request with status expectations and timing
	CheckCert    CheckType = "cert"    // TLS certificate validity / expiry for host:port
	CheckTCP     CheckType = "tcp"     // TCP connect to host:port
	CheckDNS     CheckType = "dns"     // hostname resolves (optionally to expected values)
	CheckKeyword CheckType = "keyword" // HTTP/S response contains / does not contain text
	CheckJSON    CheckType = "json"    // HTTP/S JSON response has expected value at a path
	CheckCustom  CheckType = "custom"  // user-supplied command/script, output parsed for status/metrics
	CheckSystem  CheckType = "system"  // hardware health of a machine: processor, memory, disk space, throughput
)

// AllCheckTypes lists the supported check types in display order.
var AllCheckTypes = []CheckType{CheckPing, CheckHTTP, CheckCert, CheckTCP, CheckDNS, CheckKeyword, CheckJSON, CheckCustom, CheckSystem}

// Valid reports whether the type is one the engine can run.
func (t CheckType) Valid() bool {
	for _, k := range AllCheckTypes {
		if k == t {
			return true
		}
	}
	return false
}

// Label returns a human readable name for the check type.
func (t CheckType) Label() string {
	switch t {
	case CheckPing:
		return "Ping"
	case CheckHTTP:
		return "HTTP/S"
	case CheckCert:
		return "HTTPS certificate"
	case CheckTCP:
		return "TCP port"
	case CheckDNS:
		return "DNS"
	case CheckKeyword:
		return "Keyword"
	case CheckJSON:
		return "JSON"
	case CheckCustom:
		return "Custom script"
	case CheckSystem:
		return "Hardware health"
	}
	return string(t)
}

// Status is the evaluated health of a check or node.
type Status string

const (
	StatusUp          Status = "up"
	StatusDegraded    Status = "degraded" // warning: high latency, packet loss, cert expiring, content changed
	StatusDown        Status = "down"
	StatusUnknown     Status = "unknown"     // no result yet
	StatusPaused      Status = "paused"      // disabled by the user
	StatusMaintenance Status = "maintenance" // inside a maintenance window
)

// Severity orders statuses so that the "worst" can be chosen for a node.
func (s Status) Severity() int {
	switch s {
	case StatusDown:
		return 5
	case StatusDegraded:
		return 4
	case StatusUnknown:
		return 3
	case StatusMaintenance:
		return 2
	case StatusUp:
		return 1
	case StatusPaused:
		return 0
	}
	return 3
}

// Worst returns the more severe of two statuses.
func Worst(a, b Status) Status {
	if b.Severity() > a.Severity() {
		return b
	}
	return a
}

// Importance is the optional criticality level of a node.
type Importance string

const (
	ImportanceLow      Importance = "low"
	ImportanceNormal   Importance = "normal"
	ImportanceHigh     Importance = "high"
	ImportanceCritical Importance = "critical"
)

// Node is a device, service, website, endpoint, router, NAS, server,
// application or API. A node owns zero or more checks.
type Node struct {
	ID            int64      `json:"id"`
	Name          string     `json:"name"`
	Host          string     `json:"host"` // default target for checks (hostname, IP or URL)
	Group         string     `json:"group"`
	Tags          []string   `json:"tags"`
	Notes         string     `json:"notes"`
	Importance    Importance `json:"importance"`
	Enabled       bool       `json:"enabled"`
	DependsOnNode *int64     `json:"dependsOnNodeId"` // parent node for dependency-aware alerting
	Template      string     `json:"template"`        // template used when created (informational)
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`

	// Checks is populated by the API when returning a full node.
	Checks []Check `json:"checks"`
}

// Check is one monitor attached to a node.
type Check struct {
	ID               int64          `json:"id"`
	NodeID           int64          `json:"nodeId"`
	Type             CheckType      `json:"type"`
	Name             string         `json:"name"`
	Enabled          bool           `json:"enabled"`
	IntervalSeconds  int            `json:"intervalSeconds"`  // how often the check runs (min 10s)
	TimeoutSeconds   int            `json:"timeoutSeconds"`   // per attempt timeout
	Retries          int            `json:"retries"`          // immediate retries inside one run before declaring failure
	FailureThreshold int            `json:"failureThreshold"` // consecutive failed runs before the check is DOWN (0 = use global default)
	Config           CheckConfig    `json:"config"`
	Alerts           *AlertOverride `json:"alerts,omitempty"` // per-check alert overrides
	SortOrder        int            `json:"sortOrder"`
	CreatedAt        time.Time      `json:"createdAt"`
	UpdatedAt        time.Time      `json:"updatedAt"`
}

// Target returns the effective target for the check: the check's own target
// when set, otherwise the node host.
func (c Check) Target(nodeHost string) string {
	if t := strings.TrimSpace(c.Config.Target); t != "" {
		return t
	}
	return strings.TrimSpace(nodeHost)
}

// CheckConfig holds type-specific settings. Unused fields are left zero and
// omitted from JSON.
type CheckConfig struct {
	// Target overrides the node host. Hostname/IP for ping, tcp, dns, cert;
	// URL for http, keyword, json (scheme optional, https assumed).
	Target string `json:"target,omitempty"`

	// Ping
	PingCount int `json:"pingCount,omitempty"` // packets per run (default 4, max 20)

	// HTTP / keyword / JSON
	Method          string            `json:"method,omitempty"`          // default GET
	ExpectedStatus  string            `json:"expectedStatus,omitempty"`  // "200", "200-299", "200,301,302" (default "200-399")
	FollowRedirects *bool             `json:"followRedirects,omitempty"` // default true
	Headers         map[string]string `json:"headers,omitempty"`
	Body            string            `json:"body,omitempty"`
	IgnoreTLSErrors bool              `json:"ignoreTlsErrors,omitempty"`
	Keyword         string            `json:"keyword,omitempty"`       // keyword check: text that must be present
	KeywordAbsent   bool              `json:"keywordAbsent,omitempty"` // keyword check: text must NOT be present
	JSONPath        string            `json:"jsonPath,omitempty"`      // json check: dotted path e.g. "status" or "data.items[0].name"
	JSONExpected    string            `json:"jsonExpected,omitempty"`  // json check: expected value as string (empty = path must exist)
	CertWarnDays    int               `json:"certWarnDays,omitempty"`  // warn when certificate expires within N days (default 14)
	CertCheck       *bool             `json:"certCheck,omitempty"`     // http: also evaluate certificate expiry (default true for https)
	ContentWatch    string            `json:"contentWatch,omitempty"`  // "" | "hash" | "header" | "redirect" | "keyword" | "json"
	ContentHeader   string            `json:"contentHeader,omitempty"` // header name when ContentWatch == "header"

	// TCP / cert
	Port int `json:"port,omitempty"` // tcp: required; cert: default 443

	// DNS
	ExpectedIPs []string `json:"expectedIps,omitempty"` // optional list; any resolved value must be in this list
	DNSServer   string   `json:"dnsServer,omitempty"`   // optional resolver host[:port]
	RecordType  string   `json:"recordType,omitempty"`  // "A" (default, A+AAAA), "CNAME", "MX", "TXT"

	// Warning thresholds (0 = disabled)
	LatencyWarnMS     float64 `json:"latencyWarnMs,omitempty"`
	PacketLossWarnPct float64 `json:"packetLossWarnPct,omitempty"`

	// Custom: a command/script GWatch runs on schedule. See docs/API.md for
	// the output contract (status=/message=/latency_ms=/error= lines).
	Command string            `json:"command,omitempty"`
	WorkDir string            `json:"workDir,omitempty"`
	Env     map[string]string `json:"env,omitempty"`

	// System (hardware health). HostSource says which machine to read; see
	// HostSource for what each one means.
	HostSource   HostSource `json:"hostSource,omitempty"`
	AgentID      int64      `json:"agentId,omitempty"`      // HostSourceAgent: which registered machine
	MetricsURL   string     `json:"metricsUrl,omitempty"`   // HostSourceURL: the endpoint to read
	MetricsToken string     `json:"metricsToken,omitempty"` // HostSourceURL: bearer token for that endpoint

	// Hardware thresholds, all percentages except LoadWarnPerCore. A warning
	// threshold makes the check degraded, a critical one makes it down; 0
	// turns that threshold off. Thresholds is what the check is for, so a new
	// system check is created with SystemDefaults rather than with none.
	CPUWarnPct      float64  `json:"cpuWarnPct,omitempty"`
	CPUCritPct      float64  `json:"cpuCritPct,omitempty"`
	MemWarnPct      float64  `json:"memWarnPct,omitempty"`
	MemCritPct      float64  `json:"memCritPct,omitempty"`
	SwapWarnPct     float64  `json:"swapWarnPct,omitempty"`
	DiskWarnPct     float64  `json:"diskWarnPct,omitempty"`
	DiskCritPct     float64  `json:"diskCritPct,omitempty"`
	DiskMounts      []string `json:"diskMounts,omitempty"` // only these mount points; empty means every one
	LoadWarnPerCore float64  `json:"loadWarnPerCore,omitempty"`
	LoadCritPerCore float64  `json:"loadCritPerCore,omitempty"`
	// StaleAfterSeconds is how old a reading may be before the check reports
	// the machine as down. 0 means three times the check interval.
	StaleAfterSeconds int `json:"staleAfterSeconds,omitempty"`
}

// SystemDefaults are the thresholds a new hardware check starts with. They are
// written into the check's configuration rather than applied at run time, so
// what the check does is what the edit screen shows.
func SystemDefaults() CheckConfig {
	return CheckConfig{
		CPUWarnPct:      90,
		MemWarnPct:      90,
		MemCritPct:      97,
		SwapWarnPct:     50,
		DiskWarnPct:     85,
		DiskCritPct:     95,
		LoadWarnPerCore: 2,
	}
}

// HasSystemThresholds reports whether any hardware threshold is set. A check
// with none would never alert, so the API fills in SystemDefaults instead.
func (c CheckConfig) HasSystemThresholds() bool {
	return c.CPUWarnPct > 0 || c.CPUCritPct > 0 || c.MemWarnPct > 0 || c.MemCritPct > 0 ||
		c.SwapWarnPct > 0 || c.DiskWarnPct > 0 || c.DiskCritPct > 0 ||
		c.LoadWarnPerCore > 0 || c.LoadCritPerCore > 0
}

// AlertOverride lets a check override global alert defaults. Nil pointers
// mean "use the global setting".
type AlertOverride struct {
	Enabled         *bool    `json:"enabled,omitempty"`
	CooldownMinutes *int     `json:"cooldownMinutes,omitempty"`
	NotifyRecovery  *bool    `json:"notifyRecovery,omitempty"`
	NotifyWarnings  *bool    `json:"notifyWarnings,omitempty"`
	Recipients      []string `json:"recipients,omitempty"` // replaces global recipients when non-empty
}

// Result is a single observation produced by running a check.
type Result struct {
	ID        int64         `json:"id"`
	CheckID   int64         `json:"checkId"`
	Timestamp time.Time     `json:"ts"`
	Success   bool          `json:"success"`
	Status    Status        `json:"status"` // up, degraded or down
	Message   string        `json:"message"`
	Error     string        `json:"error,omitempty"`
	LatencyMS *float64      `json:"latencyMs"` // primary metric: avg RTT, total HTTP time, connect time, resolve time
	MinMS     *float64      `json:"minMs,omitempty"`
	MaxMS     *float64      `json:"maxMs,omitempty"`
	JitterMS  *float64      `json:"jitterMs,omitempty"`
	LossPct   *float64      `json:"lossPct,omitempty"`
	Details   ResultDetails `json:"details"`
	Attempts  int           `json:"attempts"`
	Warnings  []string      `json:"warnings,omitempty"` // degraded reasons
}

// ResultDetails carries the type-specific diagnostics shown in the
// "last result" inspector.
type ResultDetails struct {
	// HTTP family
	StatusCode     int      `json:"statusCode,omitempty"`
	FinalURL       string   `json:"finalUrl,omitempty"`
	Redirects      int      `json:"redirects,omitempty"`
	DNSMs          *float64 `json:"dnsMs,omitempty"`
	ConnectMs      *float64 `json:"connectMs,omitempty"`
	TLSMs          *float64 `json:"tlsMs,omitempty"`
	FirstByteMs    *float64 `json:"firstByteMs,omitempty"`
	TotalMs        *float64 `json:"totalMs,omitempty"`
	ContentLength  int64    `json:"contentLength,omitempty"`
	ContentHash    string   `json:"contentHash,omitempty"`  // sha256 of normalized body when ContentWatch is on
	ContentValue   string   `json:"contentValue,omitempty"` // watched header / redirect / json / keyword value
	ContentChanged bool     `json:"contentChanged,omitempty"`
	UnexpectedCode bool     `json:"unexpectedCode,omitempty"`
	KeywordFound   *bool    `json:"keywordFound,omitempty"`
	JSONValue      string   `json:"jsonValue,omitempty"`
	JSONMatched    *bool    `json:"jsonMatched,omitempty"`

	// Certificate (HTTP and cert checks)
	Cert *CertInfo `json:"cert,omitempty"`

	// Ping
	PacketsSent     int       `json:"packetsSent,omitempty"`
	PacketsReceived int       `json:"packetsReceived,omitempty"`
	RTTs            []float64 `json:"rtts,omitempty"`

	// DNS
	ResolvedValues []string `json:"resolvedValues,omitempty"`
	ExpectedMatch  *bool    `json:"expectedMatch,omitempty"`
	Resolver       string   `json:"resolver,omitempty"`

	// TCP
	RemoteAddr string `json:"remoteAddr,omitempty"`

	// Custom: combined stdout/stderr of the command (after stripping the
	// key=value control lines), capped at 8 KiB.
	Output string `json:"output,omitempty"`

	// System: the hardware reading the check evaluated, and how old it was.
	Host       *HostMetrics `json:"host,omitempty"`
	HostAgeSec *float64     `json:"hostAgeSeconds,omitempty"`
}

// CertInfo describes the leaf certificate presented by a TLS server.
type CertInfo struct {
	Subject       string    `json:"subject"`
	Issuer        string    `json:"issuer"`
	NotBefore     time.Time `json:"notBefore"`
	NotAfter      time.Time `json:"notAfter"`
	DaysRemaining int       `json:"daysRemaining"`
	DNSNames      []string  `json:"dnsNames,omitempty"`
	Serial        string    `json:"serial,omitempty"`
	Valid         bool      `json:"valid"`           // chain verified and within validity period
	Error         string    `json:"error,omitempty"` // verification error when Valid is false
}

// CheckState is the live state of a check maintained by the engine.
type CheckState struct {
	CheckID             int64      `json:"checkId"`
	Status              Status     `json:"status"`
	ConsecutiveFailures int        `json:"consecutiveFailures"`
	LastRunAt           *time.Time `json:"lastRunAt"`
	LastSuccessAt       *time.Time `json:"lastSuccessAt"`
	LastChangeAt        *time.Time `json:"lastChangeAt"` // when Status last changed
	NextRunAt           *time.Time `json:"nextRunAt"`
	LastMessage         string     `json:"lastMessage"`
	LastLatencyMS       *float64   `json:"lastLatencyMs"`
	AlertActive         bool       `json:"alertActive"` // a down notification was sent and no recovery yet
	AlertSuppressed     bool       `json:"alertSuppressed"`
	SuppressReason      string     `json:"suppressReason,omitempty"` // maintenance | dependency | silenced | cooldown | disabled
	LastAlertAt         *time.Time `json:"lastAlertAt"`
	SilencedUntil       *time.Time `json:"silencedUntil"`
	AffectedByCheckID   *int64     `json:"affectedByCheckId"` // set when a parent dependency explains this failure
	AffectedByNodeName  string     `json:"affectedByNodeName,omitempty"`
	WarningActive       bool       `json:"warningActive"`
	CertWarningActive   bool       `json:"certWarningActive"`
	LastContentHash     string     `json:"-"`
	LastContentValue    string     `json:"-"`
	Running             bool       `json:"running"`
}

// EventType enumerates the incident timeline entries.
type EventType string

const (
	EventDown               EventType = "down"
	EventRecovered          EventType = "recovered"
	EventWarning            EventType = "warning"
	EventWarningCleared     EventType = "warning_cleared"
	EventCertWarning        EventType = "cert_warning"
	EventCertWarningCleared EventType = "cert_warning_cleared"
	EventContentChanged     EventType = "content_changed"
	EventAlertSent          EventType = "alert_sent"
	EventAlertSuppressed    EventType = "alert_suppressed"
	EventAlertFailed        EventType = "alert_failed"
	EventSilenced           EventType = "silenced"
	EventUnsilenced         EventType = "unsilenced"
	EventMaintenanceBegan   EventType = "maintenance_began"
	EventMaintenanceEnded   EventType = "maintenance_ended"
	EventConfigChanged      EventType = "config_changed"
	EventAffectedByParent   EventType = "affected_by_parent"
	EventServiceStarted     EventType = "service_started"
	EventServiceStopped     EventType = "service_stopped"
	EventMonitorGap         EventType = "monitor_gap" // the monitoring computer was asleep/offline
	EventInternalError      EventType = "internal_error"
	EventBackup             EventType = "backup"
	EventRestore            EventType = "restore"
	EventRetention          EventType = "retention"
	EventNote               EventType = "note"
	EventTriggerFired       EventType = "trigger_fired"   // an automation trigger ran an action
	EventEndpointCalled     EventType = "endpoint_called" // a custom endpoint was invoked
	EventUpdate             EventType = "update"          // application update checked / applied
	EventAuth               EventType = "auth"            // sign-in, sign-out, account or API-key change
	EventDiscovery          EventType = "discovery"       // a subnet was swept, or nodes were added from a sweep
)

// Event is one entry in the incident/event timeline.
type Event struct {
	ID        int64           `json:"id"`
	Timestamp time.Time       `json:"ts"`
	Type      EventType       `json:"type"`
	NodeID    *int64          `json:"nodeId"`
	CheckID   *int64          `json:"checkId"`
	NodeName  string          `json:"nodeName,omitempty"`
	CheckName string          `json:"checkName,omitempty"`
	Title     string          `json:"title"`
	Detail    string          `json:"detail"`
	Meta      json.RawMessage `json:"meta,omitempty"`
	// Actor names who caused the event, e.g. "local", "pat (admin)" or
	// "api key Home Assistant (read-write)". It is empty for events the
	// monitoring engine produces by itself (check results, the scheduler).
	Actor string `json:"actor,omitempty"`
}

// User is a GWatch account. The password hash never leaves the store.
type User struct {
	ID          int64      `json:"id"`
	Username    string     `json:"username"`
	Role        string     `json:"role"` // "admin" | "viewer"
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	LastLoginAt *time.Time `json:"lastLoginAt,omitempty"`
}

// APIKey is a minted API key. The secret itself is shown once, at creation,
// and only its sha256 digest is stored.
type APIKey struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"` // first characters of the key, for display
	Scope      string     `json:"scope"`  // "read" | "readwrite"
	CreatedBy  string     `json:"createdBy"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
}

// Revoked reports whether the key can no longer be used.
func (k APIKey) Revoked() bool { return k.RevokedAt != nil }

// Principal is the JSON shape of the identity behind the current request,
// served by GET /api/me. It mirrors internal/auth.Principal plus the few
// display fields the web interface needs before it can load settings.
type Principal struct {
	Kind        string `json:"kind"` // "" | "local" | "user" | "apikey" | "password"
	Name        string `json:"name,omitempty"`
	Role        string `json:"role,omitempty"`
	Scope       string `json:"scope,omitempty"`
	UserID      int64  `json:"userId,omitempty"`
	IsAdmin     bool   `json:"isAdmin"`
	CanWrite    bool   `json:"canWrite"`
	SignedIn    bool   `json:"signedIn"`
	Theme       string `json:"theme,omitempty"`
	AccentColor string `json:"accentColor,omitempty"`
	// Indicators are the header's status-orb rules, already normalised. They
	// travel with the identity for the same reason the theme does: a viewer
	// may not read settings, and every account should see the same header.
	Indicators []IndicatorRule `json:"indicators,omitempty"`
}

// MaintenanceWindow silences alerts for a node, a group or everything.
type MaintenanceWindow struct {
	ID      int64     `json:"id"`
	Name    string    `json:"name"`
	NodeID  *int64    `json:"nodeId"` // nil + empty Group = all nodes
	Group   string    `json:"group"`
	Enabled bool      `json:"enabled"`
	StartAt time.Time `json:"startAt"`
	EndAt   time.Time `json:"endAt"`
	// Recurring weekly windows: when Weekdays is non-empty the window repeats
	// on those days (0 = Sunday) at StartAt's local time-of-day for
	// DurationMinutes.
	Weekdays        []int     `json:"weekdays,omitempty"`
	DurationMinutes int       `json:"durationMinutes,omitempty"`
	Notes           string    `json:"notes"`
	CreatedAt       time.Time `json:"createdAt"`
}

// Active reports whether the window covers time t.
func (m MaintenanceWindow) Active(t time.Time) bool {
	if !m.Enabled {
		return false
	}
	if len(m.Weekdays) == 0 {
		return !t.Before(m.StartAt) && t.Before(m.EndAt)
	}
	// Recurring weekly: compare local time of day.
	start := m.StartAt.In(t.Location())
	dur := time.Duration(m.DurationMinutes) * time.Minute
	if dur <= 0 {
		dur = time.Hour
	}
	// Check today and yesterday (a window can cross midnight).
	for back := 0; back <= 1; back++ {
		day := t.AddDate(0, 0, -back)
		if !containsInt(m.Weekdays, int(day.Weekday())) {
			continue
		}
		windowStart := time.Date(day.Year(), day.Month(), day.Day(), start.Hour(), start.Minute(), 0, 0, t.Location())
		if !t.Before(windowStart) && t.Before(windowStart.Add(dur)) {
			return true
		}
	}
	return false
}

func containsInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// Dashboard is a named, user-arranged collection of widgets.
type Dashboard struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	SortOrder int       `json:"sortOrder"`
	Widgets   []Widget  `json:"widgets"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Widget is one dashboard tile. Config is interpreted by the web UI.
type Widget struct {
	ID     string          `json:"id"`
	Type   string          `json:"type"` // see docs/API.md for the widget catalogue
	Title  string          `json:"title"`
	X      *int            `json:"x"`      // grid column (0-based); nil = place automatically
	Y      *int            `json:"y"`      // grid row (0-based); nil = place automatically
	Width  int             `json:"width"`  // grid columns (1..4)
	Height int             `json:"height"` // grid rows (1..6)
	Config json.RawMessage `json:"config"`
}

// SMTPSettings configures the outgoing mail account.
type SMTPSettings struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
	Security string `json:"security"` // "starttls" (default), "tls", "none"
}

// AlertSettings are the global alert defaults.
type AlertSettings struct {
	Enabled          bool         `json:"enabled"`
	Recipients       []string     `json:"recipients"`
	FailureThreshold int          `json:"failureThreshold"` // default 2
	CooldownMinutes  int          `json:"cooldownMinutes"`  // default 60
	NotifyRecovery   bool         `json:"notifyRecovery"`
	NotifyWarnings   bool         `json:"notifyWarnings"`
	CertWarnDays     int          `json:"certWarnDays"` // default 14
	SMTP             SMTPSettings `json:"smtp"`
}

// RetentionSettings control raw retention and rollups. 0 days = keep forever.
type RetentionSettings struct {
	RawDays     int `json:"rawDays"`     // default 30
	FiveMinDays int `json:"fiveMinDays"` // default 180
	HourlyDays  int `json:"hourlyDays"`  // default 730
	DailyDays   int `json:"dailyDays"`   // default 0 (forever)
	EventDays   int `json:"eventDays"`   // default 730
	// HostDays bounds the hardware readings. They are kept for less time than
	// check results by default: a reading is a whole snapshot rather than a
	// single latency number, and a year of them at one a minute is a lot of
	// database for a question nobody asks about last March's disk usage.
	HostDays int `json:"hostDays"` // default 90
}

// GeneralSettings are miscellaneous application settings.
type GeneralSettings struct {
	InstanceName         string  `json:"instanceName"`
	DefaultIntervalSecs  int     `json:"defaultIntervalSeconds"` // default 60
	DefaultTimeoutSecs   int     `json:"defaultTimeoutSeconds"`  // default 10
	MaxConcurrentChecks  int     `json:"maxConcurrentChecks"`    // default 8
	MinIntervalSecs      int     `json:"minIntervalSeconds"`     // default 10 (overload protection)
	WallboardRefreshSecs int     `json:"wallboardRefreshSeconds"`
	LatencyWarnMS        float64 `json:"latencyWarnMs"`     // global default; 0 = off
	PacketLossWarnPct    float64 `json:"packetLossWarnPct"` // global default; 0 = off
	Theme                string  `json:"theme"`             // "dark" | "light" | "system"
	AccentColor          string  `json:"accentColor"`       // hex colour used for the accent, e.g. "#43c9c0"
	RemoteAccess         bool    `json:"remoteAccess"`      // listen on every interface so other devices on the LAN can open the UI
	AccessPassword       string  `json:"accessPassword"`    // legacy: password required from non-loopback clients (HTTP basic auth)
	UpdateRepo           string  `json:"updateRepo"`        // GitHub "owner/repo" checked for new releases
	// RequireLoginLocally makes a browser on this computer sign in like every
	// other client. It only takes effect once at least one account exists, so
	// it can never lock the owner out of a fresh install.
	RequireLoginLocally bool `json:"requireLoginLocally"`
}

// BackupSettings controls scheduled, unattended backups. Backups are always
// encrypted, so a password is required to enable them.
type BackupSettings struct {
	Enabled        bool   `json:"enabled"`
	IntervalHours  int    `json:"intervalHours"` // default 24, 1-720 (30 days)
	Keep           int    `json:"keep"`          // how many archives to retain, default 7, 1-365
	IncludeHistory bool   `json:"includeHistory"`
	Password       string `json:"password"`
}

// Settings is the complete settings document.
type Settings struct {
	General   GeneralSettings   `json:"general"`
	Alerts    AlertSettings     `json:"alerts"`
	Retention RetentionSettings `json:"retention"`
	Backups   BackupSettings    `json:"backups"`
	Updates   UpdateSettings    `json:"updates"`
	// Indicators are the rules behind the status orbs in the header. They sit
	// beside the other sections rather than inside General because they are a
	// list: describeSettingsChange and the tests compare GeneralSettings with
	// ==, which only works while every field in it is comparable.
	Indicators []IndicatorRule `json:"indicators"`
}

// UpdateSettings controls how GWatch looks for new releases of itself. It is
// the only part of GWatch that reaches the internet on its own, so it is
// switchable off in one place and off means off: no periodic check, no check
// when the interface is opened, no prompt.
type UpdateSettings struct {
	CheckAutomatically bool `json:"checkAutomatically"` // periodic checks and the check when the interface is opened
	CheckIntervalHours int  `json:"checkIntervalHours"` // default 24, 1-720 (30 days)
	IncludePrerelease  bool `json:"includePrerelease"`  // offer pre-releases as well as stable releases
	PromptOnOpen       bool `json:"promptOnOpen"`       // offer the update in a dialog when the interface is opened
}

// ---- header indicators ----

// The three severities an indicator rule may carry. Green and blue are not
// among them on purpose: those two are the states GWatch works out for itself
// — green when nothing fires, blue when there is nothing to report on yet —
// and neither is anything a rule could usefully be pointed at.
const (
	IndicatorYellow = "yellow"
	IndicatorOrange = "orange"
	IndicatorRed    = "red"
)

// The conditions a rule may test. The list is short because the rules are
// evaluated in the browser against the one summary document GET /api/status
// already returns; anything not answerable from that summary would need a
// second request on every poll, for every viewer.
const (
	// IndicatorNodesInStatus counts nodes sitting in one status.
	IndicatorNodesInStatus = "nodesInStatus"
	// IndicatorCertWarnings counts certificates that are expiring or invalid.
	IndicatorCertWarnings = "certWarnings"
	// IndicatorAttention counts the entries on the attention list: the checks
	// that are down or degraded right now.
	IndicatorAttention = "attention"
	// IndicatorServiceHealth fires when the monitor itself is unwell — the
	// scheduler stopped, retention failed, a backup failed, alerts bounced.
	IndicatorServiceHealth = "serviceHealth"
)

// IndicatorCondition is what a rule tests. Which fields mean anything depends
// on Kind: only nodesInStatus reads Status, and serviceHealth reads neither.
type IndicatorCondition struct {
	Kind string `json:"kind"`
	// Status is one of down, degraded, unknown or maintenance. "unknown" is
	// the useful one on a young install: it means a node exists but has not
	// produced a result yet.
	Status string `json:"status,omitempty"`
	// MinCount is how many it takes before the rule fires. Zero means one.
	MinCount int `json:"minCount,omitempty"`
}

// IndicatorRule is one configurable orb in the header. A rule that fires puts
// an orb of its colour under the page title; several firing at once stack side
// by side, reddest first.
type IndicatorRule struct {
	ID        string             `json:"id"`
	Name      string             `json:"name"`
	Enabled   bool               `json:"enabled"`
	Colour    string             `json:"colour"`
	Condition IndicatorCondition `json:"condition"`
}

// DefaultIndicators is the set every install starts with, so that the header
// says something useful before anybody has opened the settings. The severities
// follow how much of an answer the monitor has: red for a node that is
// definitely not answering and for the monitor being broken itself, orange for
// something answering badly or about to expire, yellow for what it simply does
// not know yet or has been told to ignore.
func DefaultIndicators() []IndicatorRule {
	return []IndicatorRule{
		{ID: "nodes-down", Name: "Nodes down", Enabled: true, Colour: IndicatorRed, Condition: IndicatorCondition{Kind: IndicatorNodesInStatus, Status: string(StatusDown), MinCount: 1}},
		{ID: "monitor-unwell", Name: "Monitor trouble", Enabled: true, Colour: IndicatorRed, Condition: IndicatorCondition{Kind: IndicatorServiceHealth}},
		{ID: "nodes-degraded", Name: "Nodes degraded", Enabled: true, Colour: IndicatorOrange, Condition: IndicatorCondition{Kind: IndicatorNodesInStatus, Status: string(StatusDegraded), MinCount: 1}},
		{ID: "certs-expiring", Name: "Certificates expiring", Enabled: true, Colour: IndicatorOrange, Condition: IndicatorCondition{Kind: IndicatorCertWarnings, MinCount: 1}},
		{ID: "nodes-unknown", Name: "Waiting for first results", Enabled: true, Colour: IndicatorYellow, Condition: IndicatorCondition{Kind: IndicatorNodesInStatus, Status: string(StatusUnknown), MinCount: 1}},
		{ID: "nodes-maintenance", Name: "In maintenance", Enabled: true, Colour: IndicatorYellow, Condition: IndicatorCondition{Kind: IndicatorNodesInStatus, Status: string(StatusMaintenance), MinCount: 1}},
	}
}

// ValidIndicatorColour reports whether c is one of the three severities.
func ValidIndicatorColour(c string) bool {
	switch c {
	case IndicatorYellow, IndicatorOrange, IndicatorRed:
		return true
	}
	return false
}

// ValidIndicatorStatus reports whether s is a status nodesInStatus can count.
// Up is missing deliberately: an indicator that fires when things are well
// would be a second green, and green is not a rule's to give.
func ValidIndicatorStatus(s string) bool {
	switch Status(s) {
	case StatusDown, StatusDegraded, StatusUnknown, StatusMaintenance:
		return true
	}
	return false
}

// ValidateIndicators reports the first rule the server will not store. It is
// strict about the closed vocabulary — an unknown kind would simply never fire
// in the browser, which looks like a bug rather than a rejected setting — and
// lenient about everything a normalisation can fix.
func ValidateIndicators(rules []IndicatorRule) error {
	seen := make(map[string]bool, len(rules))
	for i, r := range rules {
		where := strings.TrimSpace(r.Name)
		if where == "" {
			where = fmt.Sprintf("indicator %d", i+1)
		}
		if !ValidIndicatorColour(r.Colour) {
			return fmt.Errorf("%s: colour must be yellow, orange or red", where)
		}
		switch r.Condition.Kind {
		case IndicatorNodesInStatus:
			if !ValidIndicatorStatus(r.Condition.Status) {
				return fmt.Errorf("%s: status must be down, degraded, unknown or maintenance", where)
			}
		case IndicatorCertWarnings, IndicatorAttention, IndicatorServiceHealth:
		default:
			return fmt.Errorf("%s: %q is not a condition GWatch can evaluate", where, r.Condition.Kind)
		}
		if r.Condition.MinCount < 0 || r.Condition.MinCount > 100000 {
			return fmt.Errorf("%s: the count must be between 1 and 100000", where)
		}
		if id := strings.TrimSpace(r.ID); id != "" {
			if seen[id] {
				return fmt.Errorf("%s: two indicators share the id %q", where, id)
			}
			seen[id] = true
		}
	}
	return nil
}

// NormalizeIndicators fills in what a rule may leave out and seeds the
// defaults when there are none. It runs when settings are loaded as well as
// when they are saved, so an install that predates indicators picks them up
// without anybody having to visit the settings page.
func NormalizeIndicators(rules []IndicatorRule) []IndicatorRule {
	if len(rules) == 0 {
		return DefaultIndicators()
	}
	out := make([]IndicatorRule, 0, len(rules))
	used := make(map[string]bool, len(rules))
	for i, r := range rules {
		r.Name = strings.TrimSpace(r.Name)
		if r.Name == "" {
			r.Name = "Indicator"
		}
		r.ID = strings.TrimSpace(r.ID)
		if r.ID == "" || used[r.ID] {
			r.ID = fmt.Sprintf("indicator-%d", i+1)
			for used[r.ID] {
				r.ID += "x"
			}
		}
		used[r.ID] = true
		if r.Condition.MinCount < 1 {
			r.Condition.MinCount = 1
		}
		if r.Condition.Kind != IndicatorNodesInStatus {
			r.Condition.Status = ""
		}
		if r.Condition.Kind == IndicatorServiceHealth {
			// It is either unwell or it is not; a count would suggest the
			// number of complaints matters, and it does not.
			r.Condition.MinCount = 1
		}
		out = append(out, r)
	}
	return out
}

// DefaultSettings returns the settings used on first run.
func DefaultSettings() Settings {
	return Settings{
		General: GeneralSettings{
			InstanceName:         "GWatch",
			DefaultIntervalSecs:  60,
			DefaultTimeoutSecs:   10,
			MaxConcurrentChecks:  8,
			MinIntervalSecs:      10,
			WallboardRefreshSecs: 15,
			LatencyWarnMS:        0,
			PacketLossWarnPct:    0,
			Theme:                "dark",
			AccentColor:          "#43c9c0",
			UpdateRepo:           "jxburros/GWatch",
		},
		Alerts: AlertSettings{
			Enabled:          false,
			Recipients:       []string{},
			FailureThreshold: 2,
			CooldownMinutes:  60,
			NotifyRecovery:   true,
			NotifyWarnings:   true,
			CertWarnDays:     14,
			SMTP:             SMTPSettings{Port: 587, Security: "starttls"},
		},
		Retention: RetentionSettings{
			RawDays:     30,
			FiveMinDays: 180,
			HourlyDays:  730,
			DailyDays:   0,
			EventDays:   730,
			HostDays:    90,
		},
		Backups: BackupSettings{
			Enabled:        false,
			IntervalHours:  24,
			Keep:           7,
			IncludeHistory: true,
		},
		Updates: UpdateSettings{
			CheckAutomatically: true,
			CheckIntervalHours: 24,
			IncludePrerelease:  false,
			PromptOnOpen:       true,
		},
		Indicators: DefaultIndicators(),
	}
}

// Rollup is an aggregated bucket of results.
type Rollup struct {
	CheckID      int64     `json:"checkId"`
	BucketSecs   int       `json:"bucketSeconds"` // 300, 3600 or 86400
	BucketStart  time.Time `json:"bucketStart"`
	Count        int       `json:"count"`
	SuccessCount int       `json:"successCount"`
	FailCount    int       `json:"failCount"`
	MinMS        *float64  `json:"minMs"`
	MaxMS        *float64  `json:"maxMs"`
	AvgMS        *float64  `json:"avgMs"`
	AvgJitterMS  *float64  `json:"avgJitterMs"`
	AvgLossPct   *float64  `json:"avgLossPct"`
	Availability float64   `json:"availability"` // percent 0-100
}

// HistoryPoint is a chart point (raw result or rollup bucket).
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

// HistorySeries is the response of the history endpoint.
type HistorySeries struct {
	CheckID    int64          `json:"checkId"`
	CheckName  string         `json:"checkName"`
	NodeName   string         `json:"nodeName"`
	CheckType  CheckType      `json:"checkType"`
	Range      string         `json:"range"`
	Source     string         `json:"source"` // raw | 5m | 1h | 1d
	BucketSecs int            `json:"bucketSeconds"`
	From       time.Time      `json:"from"`
	To         time.Time      `json:"to"`
	Points     []HistoryPoint `json:"points"`
	Summary    HistorySummary `json:"summary"`
}

// HistorySummary aggregates a series for stat tiles.
type HistorySummary struct {
	Availability float64  `json:"availability"`
	AvgMS        *float64 `json:"avgMs"`
	MinMS        *float64 `json:"minMs"`
	MaxMS        *float64 `json:"maxMs"`
	Count        int      `json:"count"`
	Failures     int      `json:"failures"`
}

// NodeTemplate prefills a node and its checks.
type NodeTemplate struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Icon        string  `json:"icon"`
	Node        Node    `json:"node"`
	Checks      []Check `json:"checks"`
}

// BackupInfo describes a backup archive on disk.
type BackupInfo struct {
	FileName       string    `json:"fileName"`
	CreatedAt      time.Time `json:"createdAt"`
	SizeBytes      int64     `json:"sizeBytes"`
	IncludeHistory bool      `json:"includeHistory"`
	Encrypted      bool      `json:"encrypted"`
}

// BackupStatus is the last backup outcome shown in Monitor Health.
type BackupStatus struct {
	LastBackupAt   *time.Time `json:"lastBackupAt"`
	LastBackupOK   bool       `json:"lastBackupOk"`
	LastBackupFile string     `json:"lastBackupFile"`
	LastError      string     `json:"lastError,omitempty"`
	LastRestoreAt  *time.Time `json:"lastRestoreAt"`
}

// RetentionStatus reports rollup / cleanup progress for Monitor Health.
type RetentionStatus struct {
	LastRunAt      *time.Time `json:"lastRunAt"`
	LastDurationMS int64      `json:"lastDurationMs"`
	LastError      string     `json:"lastError,omitempty"`
	RawRows        int64      `json:"rawRows"`
	RollupRows5m   int64      `json:"rollupRows5m"`
	RollupRows1h   int64      `json:"rollupRows1h"`
	RollupRows1d   int64      `json:"rollupRows1d"`
	EventRows      int64      `json:"eventRows"`
	OldestRaw      *time.Time `json:"oldestRaw"`
	DeletedLastRun int64      `json:"deletedLastRun"`
	Plan           []string   `json:"plan"` // human readable description of what is kept / rolled up / deleted
}

// Health is the self-observability document.
type Health struct {
	Version          string          `json:"version"`
	ServiceMode      string          `json:"serviceMode"` // "service" | "console"
	ServiceRunning   bool            `json:"serviceRunning"`
	StartedAt        time.Time       `json:"startedAt"`
	UptimeSeconds    int64           `json:"uptimeSeconds"`
	Now              time.Time       `json:"now"`
	SchedulerRunning bool            `json:"schedulerRunning"`
	LastCheckAt      *time.Time      `json:"lastCheckAt"`
	LastSuccessAt    *time.Time      `json:"lastSuccessAt"`
	NextCheckAt      *time.Time      `json:"nextCheckAt"`
	ChecksTotal      int             `json:"checksTotal"`
	ChecksEnabled    int             `json:"checksEnabled"`
	ChecksRunning    int             `json:"checksRunning"`
	LastGap          *GapInfo        `json:"lastGap"`
	DatabasePath     string          `json:"databasePath"`
	DatabaseBytes    int64           `json:"databaseBytes"`
	DataDir          string          `json:"dataDir"`
	KeyPath          string          `json:"keyPath"`
	BackupDir        string          `json:"backupDir"`
	Retention        RetentionStatus `json:"retention"`
	Backup           BackupStatus    `json:"backup"`
	RecentErrors     []Event         `json:"recentErrors"`
	AlertsEnabled    bool            `json:"alertsEnabled"`
	SMTPConfigured   bool            `json:"smtpConfigured"`
	LastAlertAt      *time.Time      `json:"lastAlertAt"`
	LastAlertError   string          `json:"lastAlertError,omitempty"`
	ListenAddress    string          `json:"listenAddress"`
	Platform         string          `json:"platform"`
}

// GapInfo records a detected period where the scheduler did not run
// (system asleep, rebooting or the service stopped).
type GapInfo struct {
	From    time.Time `json:"from"`
	To      time.Time `json:"to"`
	Seconds int64     `json:"seconds"`
}

// ---- automation: triggers, endpoints and the actions they run ----

// ActionType selects what an automation does when it fires.
type ActionType string

const (
	ActionHTTP     ActionType = "http"     // send an HTTP request (webhook)
	ActionGit      ActionType = "git"      // run a git command in a repository
	ActionScript   ActionType = "script"   // run custom code with an interpreter
	ActionRunNode  ActionType = "run_node" // run every check of a node right now
	ActionSlack    ActionType = "slack"    // post to a Slack incoming webhook
	ActionTeams    ActionType = "teams"    // post an Adaptive Card to a Teams webhook
	ActionNtfy     ActionType = "ntfy"     // publish to an ntfy topic
	ActionPushover ActionType = "pushover" // send a Pushover notification
)

// Valid reports whether the action type is known.
func (t ActionType) Valid() bool {
	switch t {
	case ActionHTTP, ActionGit, ActionScript, ActionRunNode, ActionSlack, ActionTeams, ActionNtfy, ActionPushover:
		return true
	}
	return false
}

// Action describes one thing to execute. String fields may contain
// {{placeholders}} (node.name, check.name, status, message, event, latencyMs,
// ts, node.host, target, body, query.<name>) that are expanded at run time.
type Action struct {
	Type ActionType `json:"type"`

	// http
	Method          string            `json:"method,omitempty"`
	URL             string            `json:"url,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	Body            string            `json:"body,omitempty"`
	IgnoreTLSErrors bool              `json:"ignoreTlsErrors,omitempty"`
	ExpectedStatus  string            `json:"expectedStatus,omitempty"` // default 200-399

	// git
	Repo    string `json:"repo,omitempty"`    // working directory of the repository
	GitArgs string `json:"gitArgs,omitempty"` // e.g. "pull --ff-only" or "commit -am {{message}}"

	// script (custom code)
	Interpreter string `json:"interpreter,omitempty"` // sh | bash | powershell | cmd | python | node | custom
	Command     string `json:"command,omitempty"`     // custom interpreter command line; {{file}} is the script path
	Code        string `json:"code,omitempty"`
	WorkDir     string `json:"workDir,omitempty"`
	// AllowUntrustedInput acknowledges that placeholder values may contain
	// anything the caller chooses. It is only consulted for the "custom"
	// interpreter, where GWatch cannot know how to quote a value safely and
	// therefore refuses to splice placeholders into the code without it.
	AllowUntrustedInput bool `json:"allowUntrustedInput,omitempty"`

	// run_node
	NodeID *int64 `json:"nodeId,omitempty"`

	// slack, teams: WebhookURL. ntfy: Server + Topic (+ optional Token/Priority/Tags).
	// pushover: Token + UserKey (+ optional Priority). Title/Message are shared by
	// slack, teams, ntfy and pushover.
	WebhookURL string `json:"webhookUrl,omitempty"`
	Title      string `json:"title,omitempty"`
	Message    string `json:"message,omitempty"`
	Topic      string `json:"topic,omitempty"`
	Server     string `json:"server,omitempty"`
	Priority   string `json:"priority,omitempty"`
	Tags       string `json:"tags,omitempty"`
	Token      string `json:"token,omitempty"`
	UserKey    string `json:"userKey,omitempty"`

	TimeoutSeconds int `json:"timeoutSeconds,omitempty"` // default 30
}

// ActionResult is the outcome of executing an action.
type ActionResult struct {
	OK         bool      `json:"ok"`
	Output     string    `json:"output"`
	Error      string    `json:"error,omitempty"`
	StatusCode int       `json:"statusCode,omitempty"`
	StartedAt  time.Time `json:"startedAt"`
	DurationMS int64     `json:"durationMs"`
}

// TriggerConditions lists the conditions a trigger can react to.
var TriggerConditions = []string{
	"down", "recovered", "degraded", "warning_cleared", "cert_warning", "content_changed",
	"affected_by_parent", "status_change", "any_failure", "any_success", "latency_over",
}

// Trigger runs an action when something happens on a node.
type Trigger struct {
	ID              int64      `json:"id"`
	NodeID          int64      `json:"nodeId"`
	Name            string     `json:"name"`
	Description     string     `json:"description"`
	Enabled         bool       `json:"enabled"`
	On              []string   `json:"on"`                      // conditions, see TriggerConditions
	CheckID         *int64     `json:"checkId"`                 // optional: only this check
	LatencyOverMS   float64    `json:"latencyOverMs,omitempty"` // for the latency_over condition
	Action          Action     `json:"action"`
	CooldownMinutes int        `json:"cooldownMinutes"`
	LastRunAt       *time.Time `json:"lastRunAt"`
	LastStatus      string     `json:"lastStatus"` // "" | ok | failed
	LastOutput      string     `json:"lastOutput"`
	RunCount        int        `json:"runCount"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}

// Endpoint is a user-defined HTTP endpoint served at /hook/{slug} that runs
// an action when called.
type Endpoint struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
	Method      string `json:"method"` // GET | POST | ANY
	Token       string `json:"token"`  // shared secret (X-GWatch-Token header, ?token= or Bearer)
	// AllowNoToken is the explicit acknowledgement that this endpoint may be
	// called by anyone who can reach the port. Without it an empty token is
	// rejected when saving and refused when called.
	AllowNoToken bool       `json:"allowNoToken"`
	Action       Action     `json:"action"`
	LastCalledAt *time.Time `json:"lastCalledAt"`
	LastStatus   string     `json:"lastStatus"`
	LastOutput   string     `json:"lastOutput"`
	CallCount    int        `json:"callCount"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
}

// SavedChart is a chart configuration kept on the Charts page. Config is
// interpreted by the web UI.
type SavedChart struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Config    json.RawMessage `json:"config"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

// UpdateInfo describes the result of a GitHub release check.
type UpdateInfo struct {
	Repo            string     `json:"repo"`
	CurrentVersion  string     `json:"currentVersion"`
	LatestVersion   string     `json:"latestVersion"`
	UpdateAvailable bool       `json:"updateAvailable"`
	CurrentIsDev    bool       `json:"currentIsDev"`
	ReleaseURL      string     `json:"releaseUrl"`
	ReleaseNotes    string     `json:"releaseNotes"`
	PublishedAt     *time.Time `json:"publishedAt"`
	AssetName       string     `json:"assetName"`
	AssetURL        string     `json:"assetUrl"`
	AssetSize       int64      `json:"assetSize"`
	CheckedAt       time.Time  `json:"checkedAt"`
	Prerelease      bool       `json:"prerelease"`
	Error           string     `json:"error,omitempty"`
}

// Release is one published release of GWatch, as offered to the user to pick
// from. Installable is false when the release carries no executable for this
// platform, which is what an older release predating a platform looks like.
type Release struct {
	Version     string     `json:"version"`
	Tag         string     `json:"tag"`
	Name        string     `json:"name"`
	Prerelease  bool       `json:"prerelease"`
	Notes       string     `json:"notes"`
	URL         string     `json:"url"`
	PublishedAt *time.Time `json:"publishedAt"`
	AssetName   string     `json:"assetName"`
	AssetURL    string     `json:"assetUrl"`
	AssetSize   int64      `json:"assetSize"`
	Installable bool       `json:"installable"`
	Newer       bool       `json:"newer"`   // newer than the running version
	Running     bool       `json:"running"` // this is the running version
}

// UpdateStatus is the state of the self-updater.
type UpdateStatus struct {
	Last         *UpdateInfo `json:"last"`
	Applying     bool        `json:"applying"`
	Applied      bool        `json:"applied"`    // the new executable is in place; a restart is pending / happened
	Restarting   bool        `json:"restarting"` // the service is about to restart
	LastApplyAt  *time.Time  `json:"lastApplyAt"`
	LastError    string      `json:"lastError,omitempty"`
	Executable   string      `json:"executable"`
	CanApply     bool        `json:"canApply"` // the executable directory is writable
	LastCheckAt  *time.Time  `json:"lastCheckAt"`
	NextCheckAt  *time.Time  `json:"nextCheckAt"` // nil when automatic checks are off
	AutoCheck    bool        `json:"autoCheck"`
	PromptOnOpen bool        `json:"promptOnOpen"`
}

// NetworkInfo tells the UI how the interface is reachable.
type NetworkInfo struct {
	ListenAddress string   `json:"listenAddress"` // effective bind address
	RemoteAccess  bool     `json:"remoteAccess"`  // reachable from other devices
	PasswordSet   bool     `json:"passwordSet"`   // basic auth is required from other devices
	Port          int      `json:"port"`
	LocalURL      string   `json:"localUrl"`
	LANURLs       []string `json:"lanUrls"` // one per non-loopback interface address
	Hostname      string   `json:"hostname"`
	RestartNeeded bool     `json:"restartNeeded"` // listen change could not be applied live
}

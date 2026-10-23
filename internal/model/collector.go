package model

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Remote collectors (issue #123).
//
// A paired agent can be asked to run some of GWatch's network checks from
// where it is — a second office, a parent's house, a client's site — and send
// the results back, so one GWatch watches places it cannot reach itself. It is
// off unless two people agree: an administrator turns it on for the agent in
// GWatch, and the machine's own agent.json says `"collector": true`. Either
// one alone does nothing, and in particular GWatch hands no check definitions
// (and so no SNMP community or HTTP header) to an agent whose machine has not
// opted in.
//
// The shape of it keeps the agent's trust model (docs/AGENT-DECISIONS.md,
// entries 12 and 16). The agent still opens every connection: the checks are
// part of GWatch's answer to the agent's own check-in, the results go out on a
// route of their own authenticated by the agent's token, and GWatch has no way
// to make the agent do anything between check-ins. What it can be given is a
// list of definitions of built-in network checks, which are data — a type
// from the closed list below, an address and the settings that type reads —
// and never a command, a script, a path or a URL to fetch code from. The agent
// refuses anything outside that list again on its own side, and the runner it
// calls refuses it a third time, so no one place has to be right.

// CollectorCheckTypes are the check types a collector may run: the built-in
// network checks. HTTP's three relatives (keyword, JSON and certificate) are
// the HTTP runner reading the same response differently, so they are here
// with it. Custom scripts are not and never will be, because running one on
// another machine is exactly the remote execution entry 1 refuses; hardware
// checks read the machine an agent is already reporting; Matter needs a
// sidecar on GWatch's own network.
var CollectorCheckTypes = []CheckType{CheckPing, CheckTCP, CheckHTTP, CheckKeyword, CheckJSON, CheckCert, CheckDNS, CheckSNMP}

// Collectable reports whether a collector may run checks of this type.
func (t CheckType) Collectable() bool {
	for _, k := range CollectorCheckTypes {
		if k == t {
			return true
		}
	}
	return false
}

// Limits on what a collector is handed and what it may send back. They bound
// both sides the way the collection limits do: a confused server cannot make
// an agent hold an unreasonable list, and a confused agent cannot make the
// server swallow an unreasonable batch.
const (
	MaxCollectorChecks = 500
	MaxCollectorBatch  = 500

	// CollectorMissedIntervals is how many of its shortest check interval a
	// collector may go without sending anything before GWatch calls it
	// unreachable, and CollectorGraceMin is the least it is ever given. The
	// agent batches results for a few seconds and backs off for up to two
	// minutes when a delivery fails, so a short interval needs the floor.
	CollectorMissedIntervals = 3
	CollectorGraceMin        = 3 * time.Minute

	// CollectorHeartbeat is the longest a running collector goes without
	// posting, results or not. It is what tells "no checks are due" apart
	// from "the collector has gone".
	CollectorHeartbeat = time.Minute
)

// CollectorFor returns the collector that runs check c of node n, if any:
// the node's collector when it has one and the check is of a type a collector
// may run. Every other check of the node — a hardware check, a script —
// still runs here.
func (n Node) CollectorFor(c Check) (int64, bool) {
	if n.CollectorID == nil || *n.CollectorID <= 0 || !c.Type.Collectable() {
		return 0, false
	}
	return *n.CollectorID, true
}

// CollectorCheck is one check as a collector is given it: what to run, how
// often, and against what. The target is resolved here so the agent never
// needs the node, and Config holds only the settings a network check reads
// (ForCollector).
type CollectorCheck struct {
	ID              int64       `json:"id"`
	Type            CheckType   `json:"type"`
	Name            string      `json:"name"`
	IntervalSeconds int         `json:"intervalSeconds"`
	TimeoutSeconds  int         `json:"timeoutSeconds"`
	Retries         int         `json:"retries"`
	Target          string      `json:"target"`
	Config          CheckConfig `json:"config"`
}

// CollectorOptions are the global defaults the check runners fall back on,
// so a check run by a collector is judged exactly as it would be here.
type CollectorOptions struct {
	DefaultCertWarn   int     `json:"defaultCertWarn,omitempty"`
	LatencyWarnMS     float64 `json:"latencyWarnMs,omitempty"`
	PacketLossWarnPct float64 `json:"packetLossWarnPct,omitempty"`
	PingMethod        string  `json:"pingMethod,omitempty"`
}

// CollectorAssignment is the part of GWatch's answer to a check-in that tells
// a collector what to run. It is present only when both sides have agreed;
// an empty Checks list with the struct present means "you are a collector
// with nothing to do", and its absence means "you are not one".
type CollectorAssignment struct {
	Checks  []CollectorCheck `json:"checks"`
	Options CollectorOptions `json:"options"`
}

// NewCollectorCheck builds what a collector is given for a check.
func NewCollectorCheck(c Check, nodeHost string) CollectorCheck {
	return CollectorCheck{
		ID:              c.ID,
		Type:            c.Type,
		Name:            c.Name,
		IntervalSeconds: c.IntervalSeconds,
		TimeoutSeconds:  c.TimeoutSeconds,
		Retries:         c.Retries,
		Target:          c.Target(nodeHost),
		Config:          c.Config.ForCollector(),
	}
}

// Validate is the agent's own check of one definition it was handed. It is
// data from the network, and what the agent does with it is open a
// connection, so the type has to be one of the network checks, the target an
// address rather than anything a shell or a file system could read, and the
// numbers sane.
func (cc CollectorCheck) Validate() error {
	if cc.ID <= 0 {
		return errors.New("a check without an id")
	}
	if !cc.Type.Collectable() {
		return fmt.Errorf("check %d is of type %q, which a collector does not run", cc.ID, cc.Type)
	}
	t := strings.TrimSpace(cc.Target)
	if t == "" {
		return fmt.Errorf("check %d has no target", cc.ID)
	}
	if len(t) > 2048 || strings.ContainsAny(t, "\x00\r\n\t ") {
		return fmt.Errorf("check %d has a target that is not an address", cc.ID)
	}
	if cc.IntervalSeconds < 10 || cc.IntervalSeconds > 7*24*3600 {
		return fmt.Errorf("check %d has an interval of %d seconds", cc.ID, cc.IntervalSeconds)
	}
	if cc.TimeoutSeconds < 0 || cc.TimeoutSeconds > 300 || cc.Retries < 0 || cc.Retries > 10 {
		return fmt.Errorf("check %d has an out-of-range timeout or retry count", cc.ID)
	}
	if len(cc.Name) > 500 {
		return fmt.Errorf("check %d has an overlong name", cc.ID)
	}
	return nil
}

// Check is the definition as the check runners take it. Only the settings
// ForCollector kept are carried over, so even a definition that arrived with
// a command in it runs without one.
func (cc CollectorCheck) Check() Check {
	cfg := cc.Config.ForCollector()
	cfg.Target = strings.TrimSpace(cc.Target)
	return Check{
		ID:              cc.ID,
		Type:            cc.Type,
		Name:            cc.Name,
		Enabled:         true,
		IntervalSeconds: cc.IntervalSeconds,
		TimeoutSeconds:  cc.TimeoutSeconds,
		Retries:         cc.Retries,
		Config:          cfg,
	}
}

// ForCollector copies the settings a network check reads and nothing else.
// It is an allow-list on purpose: a field added to CheckConfig later stays
// on this server until someone decides here that a collector needs it, and
// TestForCollectorClassifiesEveryField fails until they do. Left behind are
// the command, environment and working directory of a script check, a
// hardware check's machine and metrics token, a Matter server, and the
// check's groups, none of which a network check run elsewhere has any use
// for.
//
// What is kept includes credentials: the SNMP community and v3 passwords, and
// HTTP headers, which commonly carry an API key. They are handed only to the
// collector assigned to the check, over the agent's own TLS connection, and
// docs/COLLECTORS.md says so.
func (c CheckConfig) ForCollector() CheckConfig {
	return CheckConfig{
		Target: c.Target,

		PingCount:  c.PingCount,
		PingMethod: c.PingMethod,

		Method:          c.Method,
		ExpectedStatus:  c.ExpectedStatus,
		FollowRedirects: c.FollowRedirects,
		Headers:         cloneStrings(c.Headers),
		Body:            c.Body,
		IgnoreTLSErrors: c.IgnoreTLSErrors,
		Keyword:         c.Keyword,
		KeywordAbsent:   c.KeywordAbsent,
		JSONPath:        c.JSONPath,
		JSONExpected:    c.JSONExpected,
		CertWarnDays:    c.CertWarnDays,
		CertCheck:       c.CertCheck,
		ContentWatch:    c.ContentWatch,
		ContentHeader:   c.ContentHeader,
		JSONRecord:      c.JSONRecord,
		JSONMetric:      c.JSONMetric,
		JSONUnit:        c.JSONUnit,
		JSONWarnAbove:   c.JSONWarnAbove,
		JSONCritAbove:   c.JSONCritAbove,
		JSONWarnBelow:   c.JSONWarnBelow,
		JSONCritBelow:   c.JSONCritBelow,

		Port: c.Port,

		ExpectedIPs: append([]string(nil), c.ExpectedIPs...),
		DNSServer:   c.DNSServer,
		RecordType:  c.RecordType,

		LatencyWarnMS:     c.LatencyWarnMS,
		PacketLossWarnPct: c.PacketLossWarnPct,

		SNMPVersion:   c.SNMPVersion,
		SNMPPort:      c.SNMPPort,
		SNMPCommunity: c.SNMPCommunity,
		SNMPUser:      c.SNMPUser,
		SNMPAuthProto: c.SNMPAuthProto,
		SNMPAuthPass:  c.SNMPAuthPass,
		SNMPPrivProto: c.SNMPPrivProto,
		SNMPPrivPass:  c.SNMPPrivPass,
		SNMPOIDs:      append([]SNMPOID(nil), c.SNMPOIDs...),
	}
}

func cloneStrings(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// CollectorResult is one result a collector sends back. Held marks a result
// the collector kept through an outage and is delivering late, by its own
// clock; GWatch adds such a result to the check's history at the time it was
// taken and judges nothing on it, exactly as it does a held hardware reading.
type CollectorResult struct {
	Result Result `json:"result"`
	Held   bool   `json:"held,omitempty"`
}

// CollectorBatch is what a collector posts to /ingest/results. An empty batch
// is a heartbeat: the collector is alive and had nothing due.
type CollectorBatch struct {
	Results []CollectorResult `json:"results"`
}

// CollectorBatchReply is GWatch's answer to a batch. Rejected counts results
// that were refused for good — a check no longer assigned to this collector,
// a held result whose time cannot be believed — which the collector drops
// rather than sending again.
type CollectorBatchReply struct {
	Accepted int `json:"accepted"`
	Rejected int `json:"rejected"`
}

// Collector states as the interface shows them on an agent.
const (
	CollectorOff         = ""            // not turned on in GWatch
	CollectorWaiting     = "waiting"     // on in GWatch, not allowed by the machine (or it has not said yet)
	CollectorIdle        = "idle"        // both agree, and no node checks from it
	CollectorRunning     = "running"     // sending results
	CollectorUnreachable = "unreachable" // has checks and has gone quiet
)

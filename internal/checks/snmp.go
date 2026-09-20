package checks

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gosnmp/gosnmp"

	"github.com/jxburros/GWatch/internal/model"
)

const (
	defaultSNMPPort = 161
	maxSNMPOIDs     = 64
	// snmpChunk is how many OIDs go into one GET. Devices are allowed to
	// refuse an over-large request with "tooBig" rather than answering it, and
	// cheap routers refuse sooner than the standard's 60, so a run asks in
	// modest batches instead of gambling on one packet.
	snmpChunk = 20
)

// snmpAuthProtocols and snmpPrivProtocols map what the editor stores to what
// gosnmp expects. The empty string is "none", which is how a v3 user with no
// authentication or no privacy is configured.
var snmpAuthProtocols = map[string]gosnmp.SnmpV3AuthProtocol{
	"":       gosnmp.NoAuth,
	"MD5":    gosnmp.MD5,
	"SHA":    gosnmp.SHA,
	"SHA224": gosnmp.SHA224,
	"SHA256": gosnmp.SHA256,
	"SHA384": gosnmp.SHA384,
	"SHA512": gosnmp.SHA512,
}

var snmpPrivProtocols = map[string]gosnmp.SnmpV3PrivProtocol{
	"":        gosnmp.NoPriv,
	"DES":     gosnmp.DES,
	"AES":     gosnmp.AES,
	"AES192":  gosnmp.AES192,
	"AES256":  gosnmp.AES256,
	"AES192C": gosnmp.AES192C,
	"AES256C": gosnmp.AES256C,
}

// ---- counter state -------------------------------------------------------

// A counter OID reports a total that only ever grows, so what it means is the
// rate it grows at. That needs the previous reading, which lives here rather
// than in the database: it is worth nothing after a restart (the very next run
// produces a fresh pair) and writing it back on every run would double the
// write traffic of an SNMP check for no gain.
type snmpCounter struct {
	raw  uint64 // the value as the device reported it, before Scale
	bits int    // 32 or 64, so a wrap can be unwound at the right width
}

type snmpSample struct {
	at      time.Time
	byOID   map[string]snmpCounter
	version string // config that invalidates the sample when it changes
}

var (
	snmpStateMu sync.Mutex
	snmpState   = map[int64]snmpSample{} // check ID -> its previous counter reading
)

// ForgetCheck drops the per-check state the runners keep in memory. It is
// called when a check is deleted or reconfigured so a later check reusing the
// same id cannot inherit a stale counter reading and report an enormous rate.
func ForgetCheck(checkID int64) {
	snmpStateMu.Lock()
	delete(snmpState, checkID)
	snmpStateMu.Unlock()
}

// previousSNMP returns the last sample for a check, and stores the new one.
// A check id of 0 is an unsaved check being tested from the editor: it has no
// identity to key state by, so it never sees a rate.
func previousSNMP(checkID int64, version string, now time.Time, readings map[string]snmpCounter) (snmpSample, bool) {
	if checkID == 0 {
		return snmpSample{}, false
	}
	snmpStateMu.Lock()
	defer snmpStateMu.Unlock()
	prev, ok := snmpState[checkID]
	snmpState[checkID] = snmpSample{at: now, byOID: readings, version: version}
	if !ok || prev.version != version {
		// The first run after a start, or after the check was pointed at a
		// different device: there is nothing sound to compare against.
		return snmpSample{}, false
	}
	return prev, true
}

// counterDelta returns how much a counter advanced between two readings,
// unwinding a wrap-around at the counter's own width. A 64-bit counter wraps
// naturally in uint64 arithmetic; a 32-bit one is masked back to its width.
func counterDelta(prev, cur snmpCounter) uint64 {
	d := cur.raw - prev.raw
	if cur.bits == 32 {
		d &= 0xFFFFFFFF
	}
	return d
}

// ---- runner ---------------------------------------------------------------

// runSNMPCheck reads every configured OID from the device in one or a few
// GETs and turns the readings into a verdict. LatencyMS is the round trip of
// the exchange, so an SNMP check charts like any other check even before its
// own metrics are plotted.
func runSNMPCheck(ctx context.Context, check model.Check, target string, opts Options) model.Result {
	cfg := check.Config
	oids := cfg.SNMPOIDs
	if len(oids) == 0 {
		return failResult("no OIDs configured — add at least one reading to take")
	}
	host, port, err := hostPort(target, cfg.SNMPPort, defaultSNMPPort)
	if err != nil {
		return failResult(err.Error())
	}
	timeout := attemptTimeout(check)

	client, err := snmpClient(ctx, cfg, host, port, timeout)
	if err != nil {
		return failResult(err.Error())
	}
	if err := client.Connect(); err != nil {
		return failResult(fmt.Sprintf("could not open a socket to %s: %v", host, err))
	}
	defer client.Conn.Close()

	start := time.Now()
	pdus := make(map[string]gosnmp.SnmpPDU, len(oids))
	for _, batch := range snmpBatches(oids, snmpChunk) {
		packet, err := client.Get(batch)
		if err != nil {
			return failResult(snmpFailureMessage(err, cfg, host, port, timeout))
		}
		if packet.Error != gosnmp.NoError {
			return failResult(fmt.Sprintf("%s answered with the SNMP error %q", host, packet.Error))
		}
		for _, pdu := range packet.Variables {
			pdus[normalizeOID(pdu.Name)] = pdu
		}
	}
	elapsed := time.Since(start)

	res := model.Result{Success: true, LatencyMS: msPtr(elapsed)}
	res.Details.SNMP = make([]model.SNMPValue, 0, len(oids))
	res.Metrics = map[string]float64{}

	now := time.Now()
	readings := map[string]snmpCounter{}
	for _, o := range oids {
		if strings.EqualFold(o.Kind, "counter") {
			if raw, bits, ok := snmpCounterValue(pdus[normalizeOID(o.OID)]); ok {
				readings[normalizeOID(o.OID)] = snmpCounter{raw: raw, bits: bits}
			}
		}
	}
	prev, havePrev := previousSNMP(check.ID, snmpStateKey(cfg, host, port), now, readings)

	var criticals []string
	for _, o := range oids {
		key := normalizeOID(o.OID)
		pdu, answered := pdus[key]
		val := model.SNMPValue{OID: o.OID, Name: o.Name, Unit: o.Unit}
		if !answered || isSNMPMissing(pdu) {
			// The device answered, but not about this OID: the wrong index on
			// an interface, or a MIB it does not implement. That is a
			// configuration fault worth naming rather than a silent gap.
			val.Raw = "not available on this device"
			res.Details.SNMP = append(res.Details.SNMP, val)
			criticals = append(criticals, fmt.Sprintf("%s (%s) is not available on this device", o.Name, o.OID))
			continue
		}
		val.Raw = snmpRawText(pdu)

		var measured *float64
		if strings.EqualFold(o.Kind, "counter") {
			raw, bits, ok := snmpCounterValue(pdu)
			if !ok {
				res.Details.SNMP = append(res.Details.SNMP, val)
				continue
			}
			if havePrev {
				if before, seen := prev.byOID[key]; seen {
					secs := now.Sub(prev.at).Seconds()
					if secs > 0 {
						rate := o.Scaled(float64(counterDelta(before, snmpCounter{raw: raw, bits: bits})) / secs)
						val.Rate = fptr(rate)
						measured = val.Rate
					}
				}
			}
		} else if n, ok := snmpNumeric(pdu); ok {
			val.Value = fptr(o.Scaled(n))
			measured = val.Value
		}
		res.Details.SNMP = append(res.Details.SNMP, val)

		if measured == nil {
			// A first counter sample, or a value that is text rather than a
			// number (sysDescr, a MAC address): reported, never thresholded.
			continue
		}
		res.Metrics[o.Name] = *measured
		if crit, warn := judgeSNMP(o, *measured); crit != "" {
			criticals = append(criticals, crit)
		} else if warn != "" {
			res.Warnings = append(res.Warnings, warn)
		}
	}

	if len(criticals) > 0 {
		res.Success = false
		res.Message = strings.Join(criticals, "; ")
		res.Error = res.Message
		return res
	}
	res.Message = snmpSummary(res.Details.SNMP, elapsed)
	return res
}

// snmpStateKey describes the configuration a stored counter reading belongs
// to. When it changes the previous sample is meaningless, so it is dropped
// rather than turned into a rate against a different device.
func snmpStateKey(cfg model.CheckConfig, host string, port int) string {
	return fmt.Sprintf("%s|%d|%s", host, port, snmpVersionName(cfg.SNMPVersion))
}

// judgeSNMP compares one reading against its thresholds and returns the
// critical and the warning wording, either of which may be empty. The
// comparisons are strict, so a threshold of 1 on both sides ("must be exactly
// 1") is how an interface's operational status is expressed.
func judgeSNMP(o model.SNMPOID, v float64) (critical, warning string) {
	label := fmt.Sprintf("%s (%s) is %s", o.Name, o.OID, fmtSNMPValue(v, o.Unit))
	switch {
	case o.CritAbove != nil && v > *o.CritAbove:
		return fmt.Sprintf("%s, above the critical threshold of %s", label, fmtSNMPValue(*o.CritAbove, o.Unit)), ""
	case o.CritBelow != nil && v < *o.CritBelow:
		return fmt.Sprintf("%s, below the critical threshold of %s", label, fmtSNMPValue(*o.CritBelow, o.Unit)), ""
	case o.WarnAbove != nil && v > *o.WarnAbove:
		return "", fmt.Sprintf("%s, above the warning threshold of %s", label, fmtSNMPValue(*o.WarnAbove, o.Unit))
	case o.WarnBelow != nil && v < *o.WarnBelow:
		return "", fmt.Sprintf("%s, below the warning threshold of %s", label, fmtSNMPValue(*o.WarnBelow, o.Unit))
	}
	return "", ""
}

// snmpSummary is the one-line message shown beside the check. It names the
// first few readings, which is what the reader wants to see at a glance, and
// says how many more there are.
func snmpSummary(values []model.SNMPValue, elapsed time.Duration) string {
	parts := make([]string, 0, 3)
	for _, v := range values {
		if len(parts) == 3 {
			break
		}
		switch {
		case v.Value != nil:
			parts = append(parts, fmt.Sprintf("%s %s", v.Name, fmtSNMPValue(*v.Value, v.Unit)))
		case v.Rate != nil:
			parts = append(parts, fmt.Sprintf("%s %s", v.Name, fmtSNMPValue(*v.Rate, v.Unit)))
		case v.Raw != "":
			parts = append(parts, fmt.Sprintf("%s %s", v.Name, v.Raw))
		}
	}
	msg := fmt.Sprintf("%s read in %s ms", plural(len(values), "reading"), fmtMS(msOf(elapsed)))
	if len(parts) > 0 {
		msg += " · " + strings.Join(parts, ", ")
		if len(values) > len(parts) {
			msg += fmt.Sprintf(" and %d more", len(values)-len(parts))
		}
	}
	return msg
}

// fmtSNMPValue prints a reading the way the message should read it: without a
// trailing row of zeros, and with its unit when it has one.
func fmtSNMPValue(v float64, unit string) string {
	s := strconv.FormatFloat(round1(v), 'f', -1, 64)
	if unit == "" {
		return s
	}
	return s + " " + unit
}

// snmpFailureMessage explains why the exchange produced nothing. An SNMP v2c
// device that dislikes the community string does not say so — it says nothing
// at all — so a timeout has to carry the possibility with it.
func snmpFailureMessage(err error, cfg model.CheckConfig, host string, port int, timeout time.Duration) string {
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "timeout") {
		if snmpVersionName(cfg.SNMPVersion) == "3" {
			return fmt.Sprintf("no response from %s:%d after %s — check the device is reachable, that SNMP v3 is enabled and that the user, authentication and privacy settings match", host, port, fmtDur(timeout))
		}
		return fmt.Sprintf("no response from %s:%d after %s — check the device is reachable on UDP %d, that SNMP is enabled and that the community string is right: SNMP v2c answers a wrong community with silence, so a wrong community and an unreachable device look the same", host, port, fmtDur(timeout), port)
	}
	if strings.Contains(strings.ToLower(err.Error()), "authentication") || strings.Contains(strings.ToLower(err.Error()), "unknown user") {
		return fmt.Sprintf("authentication failed at %s:%d: %v", host, port, err)
	}
	if label := describeNetError(err, timeout); label != "" {
		return fmt.Sprintf("%s:%d: %s", host, port, label)
	}
	return err.Error()
}

// snmpClient builds a configured but unconnected client.
func snmpClient(ctx context.Context, cfg model.CheckConfig, host string, port int, timeout time.Duration) (*gosnmp.GoSNMP, error) {
	client := &gosnmp.GoSNMP{
		Target:    host,
		Port:      uint16(port),
		Transport: "udp",
		Timeout:   timeout,
		// Retries belong to the check, which already repeats a failed run
		// according to its own Retries setting; retrying underneath that as
		// well would multiply the two.
		Retries: 0,
		MaxOids: snmpChunk,
		Context: ctx,
	}
	if snmpVersionName(cfg.SNMPVersion) == "3" {
		auth, ok := snmpAuthProtocols[strings.ToUpper(strings.TrimSpace(cfg.SNMPAuthProto))]
		if !ok {
			return nil, fmt.Errorf("unsupported SNMP v3 authentication protocol %q", cfg.SNMPAuthProto)
		}
		priv, ok := snmpPrivProtocols[strings.ToUpper(strings.TrimSpace(cfg.SNMPPrivProto))]
		if !ok {
			return nil, fmt.Errorf("unsupported SNMP v3 privacy protocol %q", cfg.SNMPPrivProto)
		}
		client.Version = gosnmp.Version3
		client.SecurityModel = gosnmp.UserSecurityModel
		client.MsgFlags = gosnmp.NoAuthNoPriv
		if auth != gosnmp.NoAuth {
			client.MsgFlags = gosnmp.AuthNoPriv
		}
		if priv != gosnmp.NoPriv {
			client.MsgFlags = gosnmp.AuthPriv
		}
		client.SecurityParameters = &gosnmp.UsmSecurityParameters{
			UserName:                 strings.TrimSpace(cfg.SNMPUser),
			AuthenticationProtocol:   auth,
			AuthenticationPassphrase: cfg.SNMPAuthPass,
			PrivacyProtocol:          priv,
			PrivacyPassphrase:        cfg.SNMPPrivPass,
		}
		return client, nil
	}
	client.Version = gosnmp.Version2c
	client.Community = snmpCommunity(cfg)
	return client, nil
}

// snmpCommunity is the community string a v2c check reads with. An unset one
// means "public", which is what every device ships with and what the editor
// fills in.
func snmpCommunity(cfg model.CheckConfig) string {
	if c := strings.TrimSpace(cfg.SNMPCommunity); c != "" {
		return c
	}
	return "public"
}

// snmpVersionName normalises the stored version. Anything unrecognised is
// treated as 2c, which is what validation already insisted on.
func snmpVersionName(v string) string {
	switch strings.TrimSpace(v) {
	case "3":
		return "3"
	default:
		return "2c"
	}
}

// snmpBatches splits the OID list into requests of at most size entries.
func snmpBatches(oids []model.SNMPOID, size int) [][]string {
	var out [][]string
	var cur []string
	seen := map[string]bool{}
	for _, o := range oids {
		key := normalizeOID(o.OID)
		if seen[key] {
			// Two rows may read the same OID under different names; the device
			// only needs to be asked once.
			continue
		}
		seen[key] = true
		cur = append(cur, key)
		if len(cur) == size {
			out = append(out, cur)
			cur = nil
		}
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// normalizeOID gives an OID the leading dot gosnmp and most devices print, so
// a configured "1.3.6.1…" and a replied ".1.3.6.1…" are the same key.
func normalizeOID(oid string) string {
	oid = strings.TrimSpace(oid)
	if oid == "" {
		return ""
	}
	if !strings.HasPrefix(oid, ".") {
		return "." + oid
	}
	return oid
}

// isSNMPMissing reports the three "no such thing" answers a device gives for
// an OID it does not implement.
func isSNMPMissing(pdu gosnmp.SnmpPDU) bool {
	switch pdu.Type {
	case gosnmp.NoSuchObject, gosnmp.NoSuchInstance, gosnmp.EndOfMibView, gosnmp.Null:
		return true
	}
	return false
}

// snmpNumeric returns the reading as a number, or false when the device
// answered with something that is not one.
func snmpNumeric(pdu gosnmp.SnmpPDU) (float64, bool) {
	switch v := pdu.Value.(type) {
	case int:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint32:
		return float64(v), true
	case uint64:
		return float64(v), true
	case float32:
		return float64(v), true
	case float64:
		return v, true
	}
	return 0, false
}

// snmpCounterValue returns a counter reading and the width it counts at, so a
// wrap can be unwound. Counter64 is the 64-bit form; everything else a device
// can answer a counter OID with is 32 bits wide.
func snmpCounterValue(pdu gosnmp.SnmpPDU) (uint64, int, bool) {
	n, ok := snmpNumeric(pdu)
	if !ok || n < 0 || math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, 0, false
	}
	bits := 32
	if pdu.Type == gosnmp.Counter64 {
		bits = 64
	}
	if v, isU64 := pdu.Value.(uint64); isU64 {
		return v, bits, true
	}
	return uint64(n), bits, true
}

// snmpRawText renders a reading the way a person reads it: text for an octet
// string, the number for everything else.
func snmpRawText(pdu gosnmp.SnmpPDU) string {
	switch v := pdu.Value.(type) {
	case []byte:
		return printableOctets(v)
	case string:
		return v
	case nil:
		return ""
	}
	if n, ok := snmpNumeric(pdu); ok {
		return strconv.FormatFloat(n, 'f', -1, 64)
	}
	return fmt.Sprintf("%v", pdu.Value)
}

// printableOctets shows an octet string as text when it is text, and as hex
// when it is not — a MAC address or an engine ID would otherwise arrive as a
// row of replacement characters.
func printableOctets(b []byte) string {
	printable := true
	for _, c := range b {
		if c != '\t' && c != '\n' && c != '\r' && (c < 0x20 || c > 0x7e) {
			printable = false
			break
		}
	}
	if printable {
		return strings.TrimSpace(string(b))
	}
	parts := make([]string, 0, len(b))
	for _, c := range b {
		parts = append(parts, fmt.Sprintf("%02x", c))
	}
	return strings.Join(parts, ":")
}

// ---- validation -----------------------------------------------------------

// validateSNMPCheck refuses a configuration the runner could not act on: an
// OID that is not one, a name the results could not be told apart by, or a
// pair of thresholds that contradict each other.
func validateSNMPCheck(cfg model.CheckConfig) error {
	switch strings.TrimSpace(cfg.SNMPVersion) {
	case "", "2c", "3":
	default:
		return fmt.Errorf("unsupported SNMP version %q (use 2c or 3)", cfg.SNMPVersion)
	}
	if cfg.SNMPPort < 0 || cfg.SNMPPort > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	if snmpVersionName(cfg.SNMPVersion) == "3" {
		if strings.TrimSpace(cfg.SNMPUser) == "" {
			return errors.New("SNMP v3 needs a user name")
		}
		auth := strings.ToUpper(strings.TrimSpace(cfg.SNMPAuthProto))
		if _, ok := snmpAuthProtocols[auth]; !ok {
			return fmt.Errorf("unsupported authentication protocol %q (use MD5, SHA, SHA224, SHA256, SHA384 or SHA512)", cfg.SNMPAuthProto)
		}
		priv := strings.ToUpper(strings.TrimSpace(cfg.SNMPPrivProto))
		if _, ok := snmpPrivProtocols[priv]; !ok {
			return fmt.Errorf("unsupported privacy protocol %q (use DES, AES, AES192, AES256, AES192C or AES256C)", cfg.SNMPPrivProto)
		}
		if auth != "" && cfg.SNMPAuthPass == "" {
			return errors.New("an authentication password is required when an authentication protocol is set")
		}
		if priv != "" && auth == "" {
			return errors.New("privacy needs authentication: choose an authentication protocol as well")
		}
		if priv != "" && cfg.SNMPPrivPass == "" {
			return errors.New("a privacy password is required when a privacy protocol is set")
		}
	}
	if len(cfg.SNMPOIDs) == 0 {
		return errors.New("add at least one OID to read")
	}
	if len(cfg.SNMPOIDs) > maxSNMPOIDs {
		return fmt.Errorf("an SNMP check can read at most %d OIDs; split the rest into a second check", maxSNMPOIDs)
	}
	names := map[string]bool{}
	for _, o := range cfg.SNMPOIDs {
		if err := validDottedOID(o.OID); err != nil {
			return err
		}
		name := strings.TrimSpace(o.Name)
		if name == "" {
			return fmt.Errorf("the OID %s needs a name", o.OID)
		}
		if names[strings.ToLower(name)] {
			return fmt.Errorf("two readings are both called %q — names identify the metric in charts, so they must differ", name)
		}
		names[strings.ToLower(name)] = true
		switch strings.ToLower(strings.TrimSpace(o.Kind)) {
		case "", "gauge", "counter":
		default:
			return fmt.Errorf("%s: unsupported kind %q (use gauge or counter)", name, o.Kind)
		}
		if o.Scale < 0 {
			return fmt.Errorf("%s: the scale cannot be negative", name)
		}
		if o.WarnAbove != nil && o.CritAbove != nil && *o.WarnAbove >= *o.CritAbove {
			return fmt.Errorf("%s: the critical threshold must be above the warning one", name)
		}
		if o.WarnBelow != nil && o.CritBelow != nil && *o.WarnBelow <= *o.CritBelow {
			return fmt.Errorf("%s: the critical threshold must be below the warning one", name)
		}
	}
	return nil
}

// validDottedOID accepts the dotted numeric form, with or without the leading
// dot people copy out of a MIB browser. Names ("sysUpTime.0") are refused:
// GWatch ships no MIB files and could not resolve one.
func validDottedOID(oid string) error {
	o := strings.TrimSpace(oid)
	if o == "" {
		return errors.New("an OID is required")
	}
	o = strings.TrimPrefix(o, ".")
	if o == "" {
		return fmt.Errorf("invalid OID %q", oid)
	}
	parts := strings.Split(o, ".")
	if len(parts) < 2 {
		return fmt.Errorf("invalid OID %q — an OID looks like 1.3.6.1.2.1.1.3.0", oid)
	}
	for _, p := range parts {
		if p == "" {
			return fmt.Errorf("invalid OID %q — it has an empty part", oid)
		}
		n, err := strconv.ParseUint(p, 10, 32)
		if err != nil {
			return fmt.Errorf("invalid OID %q — %q is not a number; GWatch reads numeric OIDs, not MIB names", oid, p)
		}
		_ = n
	}
	return nil
}

package checks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gosnmp/gosnmp"

	"github.com/jxburros/GWatch/internal/model"
)

// Walking a device is how the number in an interface OID is found: SNMP
// indexes the ports itself, and the index is very often not the number
// printed on the case. Everything here exists to turn "what can this switch
// tell me" into a list somebody can tick.

const (
	// WalkDefaultRoot is the mib-2 subtree: system, interfaces, IP, and the
	// host-resources table on the devices that have one. Walking from the
	// root of the whole tree would drag in vendor subtrees thousands of rows
	// deep for no benefit.
	WalkDefaultRoot = "1.3.6.1.2.1"
	// WalkMaxRows is the ceiling on what one walk returns, whatever the
	// caller asks for. A walk is an interactive convenience, not an export.
	WalkMaxRows = 500
	// WalkTimeout bounds the whole exchange. Somebody is watching a spinner.
	WalkTimeout = 15 * time.Second
)

// errWalkFull stops a BulkWalk once enough rows have been collected. gosnmp
// has no other way to say "that is enough".
var errWalkFull = errors.New("walk: enough rows")

// WalkRequest describes one walk. The credentials are the same ones an SNMP
// check holds, because the walk is how a check gets configured.
type WalkRequest struct {
	Host   string
	Config model.CheckConfig // version, port and credentials; SNMPOIDs is ignored
	Root   string            // subtree to walk, default WalkDefaultRoot
	Max    int               // rows to return, capped at WalkMaxRows
}

// WalkRow is one value the device reported.
type WalkRow struct {
	OID string `json:"oid"`
	// Type is the SNMP type as the device sent it (Counter32, Gauge32,
	// OctetString…), which is what the guess at Kind is made from.
	Type  string `json:"type"`
	Value string `json:"value"`
	// Name is a suggestion for the reading's name, from the table of
	// well-known OIDs below. It is empty for an OID GWatch does not recognise,
	// which is most of a device's tree.
	Name string `json:"name,omitempty"`
	// Kind is "counter" for the counter types and "gauge" otherwise, so a
	// ticked row arrives in the editor already set up the right way.
	Kind string `json:"kind"`
}

// knownOIDs names the OIDs worth recognising: the standard-MIB readings the
// editor's presets already cover, so a walked row arrives with the same name
// a preset would have given it. The keys are prefixes — an entry in a table
// (ifInOctets.3) matches the prefix of its column (ifInOctets) — and the
// longest match wins.
//
// Vendor MIBs are deliberately absent. A name that is only right on one make
// would be worse than no name, because the reader would trust it.
var knownOIDs = []struct {
	prefix  string
	name    string
	indexed bool // a column of a table, so the trailing index is part of the name
}{
	{"1.3.6.1.2.1.1.1.0", "Description", false},
	{"1.3.6.1.2.1.1.3.0", "Uptime", false},
	{"1.3.6.1.2.1.1.4.0", "Contact", false},
	{"1.3.6.1.2.1.1.5.0", "Device name", false},
	{"1.3.6.1.2.1.1.6.0", "Location", false},
	{"1.3.6.1.2.1.2.2.1.2", "Port {N} name", true},
	{"1.3.6.1.2.1.2.2.1.5", "Port {N} speed", true},
	{"1.3.6.1.2.1.2.2.1.7", "Port {N} admin state", true},
	{"1.3.6.1.2.1.2.2.1.8", "Port {N} link", true},
	{"1.3.6.1.2.1.2.2.1.10", "Port {N} in", true},
	{"1.3.6.1.2.1.2.2.1.14", "Port {N} errors in", true},
	{"1.3.6.1.2.1.2.2.1.16", "Port {N} out", true},
	{"1.3.6.1.2.1.2.2.1.20", "Port {N} errors out", true},
	{"1.3.6.1.2.1.25.1.1.0", "Uptime", false},
	{"1.3.6.1.2.1.25.3.3.1.2", "Processor {N}", true},
	{"1.3.6.1.2.1.31.1.1.1.1", "Port {N} name", true},
	{"1.3.6.1.2.1.31.1.1.1.6", "Port {N} in", true},
	{"1.3.6.1.2.1.31.1.1.1.10", "Port {N} out", true},
	{"1.3.6.1.2.1.31.1.1.1.15", "Port {N} speed", true},
	{"1.3.6.1.2.1.31.1.1.1.18", "Port {N} label", true},
}

// nameForOID suggests a reading name for a walked OID.
func nameForOID(oid string) string {
	trimmed := strings.TrimPrefix(strings.TrimSpace(oid), ".")
	best := ""
	bestLen := 0
	for _, k := range knownOIDs {
		if !strings.HasPrefix(trimmed, k.prefix) || len(k.prefix) <= bestLen {
			continue
		}
		if !k.indexed {
			if trimmed != k.prefix {
				continue
			}
			best, bestLen = k.name, len(k.prefix)
			continue
		}
		index := strings.TrimPrefix(trimmed[len(k.prefix):], ".")
		if index == "" || strings.Contains(index, ".") {
			// A deeper subtree, not a row of this column.
			continue
		}
		best, bestLen = strings.ReplaceAll(k.name, "{N}", index), len(k.prefix)
	}
	return best
}

// kindForType guesses whether a walked OID should be read as a counter. The
// SNMP type says it outright: the counter types climb, everything else does
// not.
func kindForType(t gosnmp.Asn1BER) string {
	switch t {
	case gosnmp.Counter32, gosnmp.Counter64:
		return "counter"
	}
	return "gauge"
}

// Walk reads a subtree of a device and returns what it found. It is used by
// the editor's "Walk this device", never by the scheduler, so it bounds
// itself tightly: one short timeout, and a hard ceiling on rows.
func Walk(ctx context.Context, req WalkRequest) ([]WalkRow, bool, error) {
	host, err := hostOnly(req.Host)
	if err != nil {
		return nil, false, err
	}
	port := req.Config.SNMPPort
	if port <= 0 {
		port = defaultSNMPPort
	}
	if port > 65535 {
		return nil, false, errors.New("port must be between 1 and 65535")
	}
	root := strings.TrimSpace(req.Root)
	if root == "" {
		root = WalkDefaultRoot
	}
	if err := validDottedOID(root); err != nil {
		return nil, false, err
	}
	max := req.Max
	if max <= 0 || max > WalkMaxRows {
		max = WalkMaxRows
	}

	ctx, cancel := context.WithTimeout(ctx, WalkTimeout)
	defer cancel()

	// One second per request, so a device that ignores us gives up long
	// before the whole walk's budget is spent.
	client, err := snmpClient(ctx, req.Config, host, port, time.Second)
	if err != nil {
		return nil, false, err
	}
	client.MaxRepetitions = 25
	if err := client.Connect(); err != nil {
		return nil, false, fmt.Errorf("could not open a socket to %s: %v", host, err)
	}
	defer client.Conn.Close()

	// BulkWalk is a v2c/v3 feature, and those are the only versions GWatch
	// speaks, so there is no GetNext fallback to fall back to.
	rows := make([]WalkRow, 0, 64)
	err = client.BulkWalk(normalizeOID(root), func(pdu gosnmp.SnmpPDU) error {
		if isSNMPMissing(pdu) {
			return nil
		}
		oid := strings.TrimPrefix(normalizeOID(pdu.Name), ".")
		rows = append(rows, WalkRow{
			OID:   oid,
			Type:  pdu.Type.String(),
			Value: truncateWalkValue(snmpRawText(pdu)),
			Name:  nameForOID(oid),
			Kind:  kindForType(pdu.Type),
		})
		if len(rows) >= max {
			return errWalkFull
		}
		return nil
	})
	truncated := errors.Is(err, errWalkFull)
	if err != nil && !truncated {
		return nil, false, errors.New(snmpFailureMessage(err, req.Config, host, port, WalkTimeout))
	}
	return rows, truncated, nil
}

// truncateWalkValue keeps a device's chattier answers (a full sysDescr can run
// to several lines) from filling the list the reader is trying to scan.
func truncateWalkValue(v string) string {
	const limit = 160
	v = strings.Join(strings.Fields(v), " ")
	if len(v) <= limit {
		return v
	}
	return v[:limit] + "…"
}

package checks

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/matter"
	"github.com/jxburros/GWatch/internal/model"
)

// maxMatterReadings bounds one Matter check, as maxSNMPOIDs bounds an SNMP
// one. A bridge with more sensors than this is better split over a check per
// room, which also keeps one unreachable device from hiding the rest.
const maxMatterReadings = 64

// ---- runner ---------------------------------------------------------------

// runMatterCheck asks the Matter server for the configured attributes of one
// node in a single read_attribute and turns the answers into a verdict, the
// way runSNMPCheck turns one GET into one.
//
// Each run opens its own WebSocket connection and closes it when it is done.
// A long-lived connection shared between checks would save a handshake per
// run, but it would also have to notice and recover from the sidecar
// restarting, and a check that runs once a minute on a LAN gains nothing
// measurable from it. Opening fresh also makes the sidecar's health part of
// every verdict: if it is down, every Matter check says so, in the sidecar's
// name rather than the device's.
//
// The read is live — the server asks the device rather than answering from
// its cache — so LatencyMS is the round trip from GWatch to the sidecar to
// the device and back, which is what an unhealthy Thread mesh shows up in
// first.
func runMatterCheck(ctx context.Context, check model.Check, opts Options) model.Result {
	cfg := check.Config
	if len(cfg.MatterReadings) == 0 {
		return failResult("no readings configured — load the devices from the Matter server and pick at least one")
	}
	serverURL := matter.ServerURL(cfg.MatterServer, opts.NodeHost)
	if serverURL == "" {
		return failResult("no Matter server address — set one on the check, or give the node the host python-matter-server runs on")
	}
	timeout := attemptTimeout(check)

	client, err := matter.Dial(ctx, serverURL)
	if err != nil {
		return failResult(matterServerFailure(err, serverURL, timeout))
	}
	defer client.Close()

	paths := make([]string, 0, len(cfg.MatterReadings))
	seen := map[string]bool{}
	for _, r := range cfg.MatterReadings {
		p := matterPath(r)
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}

	start := time.Now()
	values, err := client.ReadAttributes(ctx, cfg.MatterNodeID, paths)
	elapsed := time.Since(start)
	if err != nil {
		// read_attribute says NodeNotReady both for a node that is offline
		// and for one that was never commissioned (device_controller.py
		// checks both in one condition), so a second question tells them
		// apart: get_node knows every node, available or not.
		if matter.IsCode(err, matter.ErrCodeNodeNotReady) {
			if _, gerr := client.GetNode(ctx, cfg.MatterNodeID); matter.IsCode(gerr, matter.ErrCodeNodeNotExists) {
				err = gerr
			}
		}
		res := failResult(matterReadFailure(err, cfg.MatterNodeID, serverURL, timeout))
		res.Details.MatterServer = serverURL
		return res
	}

	res := model.Result{Success: true, LatencyMS: msPtr(elapsed)}
	res.Details.MatterServer = serverURL
	res.Details.Matter = make([]model.MatterValue, 0, len(cfg.MatterReadings))
	res.Metrics = map[string]float64{}

	var criticals []string
	for _, r := range cfg.MatterReadings {
		path := matterPath(r)
		val := model.MatterValue{Name: r.Name, Endpoint: r.Endpoint, Path: path, Unit: r.Unit}
		raw, answered := values[path]
		if !answered {
			// The node answered, but not about this attribute: the wrong
			// endpoint, or a cluster this device does not have. That is a
			// configuration fault worth naming rather than a silent gap.
			val.Raw = "not available on this device"
			res.Details.Matter = append(res.Details.Matter, val)
			criticals = append(criticals, fmt.Sprintf("%s (%s) is not available on node %d", r.Name, path, cfg.MatterNodeID))
			continue
		}
		in := matter.Interpret(matter.Kind(r.Kind), raw, r.Scale)
		if in.Missing {
			// Every one of these attributes is nullable, and null is how a
			// sensor says it has no reading right now — a probe that has
			// come loose, a battery sensor still waking up. The device is
			// there, so this is a warning rather than an outage.
			val.Raw = "no value reported"
			res.Details.Matter = append(res.Details.Matter, val)
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s (%s) has no value right now", r.Name, path))
			continue
		}
		val.Raw = in.Text
		val.Value = in.Value
		res.Details.Matter = append(res.Details.Matter, val)
		if in.Value == nil {
			// Text (a firmware version, a label): reported, never judged.
			continue
		}
		res.Metrics[r.Name] = *in.Value
		if crit, warn := judgeMatter(r, path, *in.Value); crit != "" {
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
	res.Message = matterSummary(res.Details.Matter, cfg.MatterNodeID, elapsed)
	return res
}

// matterPath is the attribute path a reading asks for: the standard kind's
// cluster and attribute on the reading's endpoint, or the ones given for a
// plain attribute.
func matterPath(r model.MatterReading) string {
	if st, ok := matter.StandardFor(matter.Kind(r.Kind)); ok {
		return matter.Path(r.Endpoint, st.Cluster, st.Attribute)
	}
	return matter.Path(r.Endpoint, r.Cluster, r.Attribute)
}

// judgeMatter compares one reading against its thresholds, exactly as
// judgeSNMP does: strictly, critical first.
func judgeMatter(r model.MatterReading, path string, v float64) (critical, warning string) {
	label := fmt.Sprintf("%s (%s) is %s", r.Name, path, fmtMatterValue(v, r.Unit))
	switch {
	case r.CritAbove != nil && v > *r.CritAbove:
		return fmt.Sprintf("%s, above the critical threshold of %s", label, fmtMatterValue(*r.CritAbove, r.Unit)), ""
	case r.CritBelow != nil && v < *r.CritBelow:
		return fmt.Sprintf("%s, below the critical threshold of %s", label, fmtMatterValue(*r.CritBelow, r.Unit)), ""
	case r.WarnAbove != nil && v > *r.WarnAbove:
		return "", fmt.Sprintf("%s, above the warning threshold of %s", label, fmtMatterValue(*r.WarnAbove, r.Unit))
	case r.WarnBelow != nil && v < *r.WarnBelow:
		return "", fmt.Sprintf("%s, below the warning threshold of %s", label, fmtMatterValue(*r.WarnBelow, r.Unit))
	}
	return "", ""
}

// fmtMatterValue prints a reading to two decimals at most — a temperature
// sensor reports hundredths of a degree — with its unit when it has one.
func fmtMatterValue(v float64, unit string) string {
	s := strconv.FormatFloat(math.Round(v*100)/100, 'f', -1, 64)
	if unit == "" {
		return s
	}
	return s + " " + unit
}

// matterSummary is the one-line message shown beside the check, naming the
// first few readings in the words a person would use ("Door open").
func matterSummary(values []model.MatterValue, nodeID uint64, elapsed time.Duration) string {
	parts := make([]string, 0, 3)
	for _, v := range values {
		if len(parts) == 3 {
			break
		}
		if v.Raw == "" {
			continue
		}
		text := v.Raw
		if v.Unit != "" && v.Value != nil {
			text = fmtMatterValue(*v.Value, v.Unit)
		}
		parts = append(parts, v.Name+" "+text)
	}
	msg := fmt.Sprintf("%s read from node %d in %s ms", plural(len(values), "reading"), nodeID, fmtMS(msOf(elapsed)))
	if len(parts) > 0 {
		msg += " · " + strings.Join(parts, ", ")
		if len(values) > len(parts) {
			msg += fmt.Sprintf(" and %d more", len(values)-len(parts))
		}
	}
	return msg
}

// matterServerFailure explains why the sidecar itself could not be reached.
// It names the sidecar, not the device, because every Matter check that uses
// it will say the same and the fix is in one place.
func matterServerFailure(err error, serverURL string, timeout time.Duration) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Sprintf("the Matter server at %s did not answer within %s — is python-matter-server running, and reachable from this computer?", serverURL, fmtDur(timeout))
	}
	if label := describeNetError(err, timeout); label != "" {
		return fmt.Sprintf("could not reach the Matter server at %s: %s — is python-matter-server running there?", serverURL, label)
	}
	return fmt.Sprintf("could not talk to the Matter server at %s: %v", serverURL, err)
}

// matterReadFailure explains a read the sidecar refused. The three that
// matter are the node being unreachable (the device is off, out of range or
// its Thread border router is down), the node not existing on this fabric,
// and the read timing out on the way to the device.
func matterReadFailure(err error, nodeID uint64, serverURL string, timeout time.Duration) string {
	switch {
	case matter.IsCode(err, matter.ErrCodeNodeNotReady), matter.IsCode(err, matter.ErrCodeNodeNotResolving):
		return fmt.Sprintf("Matter node %d is unavailable: the Matter server cannot reach it right now — is the device powered, and for a Thread device, is its border router up?", nodeID)
	case matter.IsCode(err, matter.ErrCodeNodeNotExists):
		return fmt.Sprintf("there is no node %d on the Matter server at %s — it may have been removed; load the devices again to pick it", nodeID, serverURL)
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Sprintf("Matter node %d did not answer within %s", nodeID, fmtDur(timeout))
	}
	return err.Error()
}

// ---- validation -----------------------------------------------------------

// validateMatterCheck refuses a configuration the runner could not act on:
// no address for the sidecar, no node, a reading of an unknown kind, two
// readings with one name, or thresholds that contradict each other.
func validateMatterCheck(cfg model.CheckConfig, nodeHost string) error {
	serverURL := matter.ServerURL(cfg.MatterServer, nodeHost)
	if serverURL == "" {
		return errors.New("a Matter server address (or a node host) is required")
	}
	if err := matter.ValidateServerURL(serverURL); err != nil {
		return err
	}
	if cfg.MatterNodeID == 0 {
		return errors.New("choose the Matter node to read")
	}
	if len(cfg.MatterReadings) == 0 {
		return errors.New("add at least one reading")
	}
	if len(cfg.MatterReadings) > maxMatterReadings {
		return fmt.Errorf("a Matter check can take at most %d readings; split the rest into a second check", maxMatterReadings)
	}
	names := map[string]bool{}
	for _, r := range cfg.MatterReadings {
		name := strings.TrimSpace(r.Name)
		if name == "" {
			return fmt.Errorf("the reading on endpoint %d needs a name", r.Endpoint)
		}
		if names[strings.ToLower(name)] {
			return fmt.Errorf("two readings are both called %q — names identify the metric in charts, so they must differ", name)
		}
		names[strings.ToLower(name)] = true
		kind := matter.Kind(r.Kind)
		if _, ok := matter.StandardFor(kind); !ok {
			if kind != matter.KindAttribute {
				return fmt.Errorf("%s: unsupported reading %q (use temperature, humidity, onoff, contact, occupancy, illuminance, battery or attribute)", name, r.Kind)
			}
			if r.Cluster == 0 {
				return fmt.Errorf("%s: a plain attribute reading needs a cluster id", name)
			}
		}
		if r.Scale < 0 {
			return fmt.Errorf("%s: the scale cannot be negative", name)
		}
		if r.WarnAbove != nil && r.CritAbove != nil && *r.WarnAbove >= *r.CritAbove {
			return fmt.Errorf("%s: the critical threshold must be above the warning one", name)
		}
		if r.WarnBelow != nil && r.CritBelow != nil && *r.WarnBelow <= *r.CritBelow {
			return fmt.Errorf("%s: the critical threshold must be below the warning one", name)
		}
	}
	return nil
}

// Hardware health check: reads processor, memory, filesystem and throughput
// figures for a machine and compares them with the check's thresholds.
//
// The check does not itself go and measure anything remote. Readings come from
// one of three places, and which one is what HostSource selects:
//
//   - local: the computer GWatch runs on, sampled by the engine.
//   - agent: the newest reading a registered machine pushed to GWatch. The
//     connection is always outbound from that machine; GWatch is given
//     readings, not access.
//   - url:   a metrics endpoint the machine exposes, which GWatch reads.
//
// A threshold crossing is a degraded check for the warning level and a down
// check for the critical level, so "the disk is filling" and "the disk is
// full" are different events rather than the same alert twice.
package checks

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// HostReader supplies hardware readings that were taken elsewhere: by the
// engine's sampler for this computer, or by an agent that pushed them in.
type HostReader interface {
	// LocalHost returns the newest reading for the computer GWatch runs on.
	LocalHost(ctx context.Context) (model.HostMetrics, error)
	// AgentHost returns the newest reading pushed by a registered machine.
	// It returns an error when the machine has not reported yet.
	AgentHost(ctx context.Context, agentID int64) (model.HostMetrics, error)
}

// maxMetricsBody caps what a scraped endpoint may return. A hardware reading
// is a few kilobytes; anything larger is a misconfigured URL, and reading it
// into memory is how a monitoring agent becomes the outage.
const maxMetricsBody = 1 << 20 // 1 MiB

// defaultStaleFactor turns the check interval into a staleness deadline when
// the check does not set one: three missed reports before the machine counts
// as down, which tolerates one slow report without crying wolf.
const defaultStaleFactor = 3

// minStaleSeconds floors that deadline so a fast check does not report a
// machine down over ordinary scheduling jitter.
const minStaleSeconds = 60

func runSystemCheck(ctx context.Context, check model.Check, opts Options) model.Result {
	metrics, err := readHostMetrics(ctx, check, opts)
	if err != nil {
		return failResult(err.Error())
	}
	return evaluateHost(check, metrics, time.Now())
}

// readHostMetrics fetches the reading the check is configured to evaluate.
func readHostMetrics(ctx context.Context, check model.Check, opts Options) (model.HostMetrics, error) {
	switch source(check) {
	case model.HostSourceAgent:
		if opts.Hosts == nil {
			return model.HostMetrics{}, errors.New("hardware readings are unavailable in this context")
		}
		if check.Config.AgentID <= 0 {
			return model.HostMetrics{}, errors.New("no machine selected for this hardware check")
		}
		m, err := opts.Hosts.AgentHost(ctx, check.Config.AgentID)
		if err != nil {
			return model.HostMetrics{}, fmt.Errorf("no reading from this machine yet: %w", err)
		}
		return m, nil
	case model.HostSourceURL:
		return scrapeHostMetrics(ctx, check)
	default:
		if opts.Hosts == nil {
			return model.HostMetrics{}, errors.New("hardware readings are unavailable in this context")
		}
		m, err := opts.Hosts.LocalHost(ctx)
		if err != nil {
			return model.HostMetrics{}, fmt.Errorf("cannot read this computer's hardware: %w", err)
		}
		return m, nil
	}
}

// source returns the configured host source, defaulting to this computer.
func source(check model.Check) model.HostSource {
	if s := check.Config.HostSource; s.Valid() {
		return s
	}
	return model.HostSourceLocal
}

// scrapeHostMetrics reads a machine's own metrics endpoint. The reading is
// stamped with the key of the check that fetched it, because a scraped machine
// has no identity of its own in GWatch — only the check pointing at it does.
func scrapeHostMetrics(ctx context.Context, check model.Check) (model.HostMetrics, error) {
	url := strings.TrimSpace(check.Config.MetricsURL)
	if url == "" {
		return model.HostMetrics{}, errors.New("no metrics URL configured")
	}
	if !strings.Contains(url, "://") {
		url = "https://" + url
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return model.HostMetrics{}, fmt.Errorf("metrics URL is not usable: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if token := strings.TrimSpace(check.Config.MetricsToken); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	timeout := attemptTimeout(check)
	transport := &http.Transport{
		Proxy:                 nil, // a local monitor always connects directly
		DialContext:           (&net.Dialer{Timeout: timeout, KeepAlive: -1}).DialContext,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: check.Config.IgnoreTLSErrors, MinVersion: tls.VersionTLS12}, //nolint:gosec // user opt-in
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		DisableKeepAlives:     true,
	}
	defer transport.CloseIdleConnections()

	resp, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		return model.HostMetrics{}, fmt.Errorf("cannot reach the metrics endpoint: %s", describeNetError(err, timeout))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return model.HostMetrics{}, fmt.Errorf("the metrics endpoint refused the token (HTTP %d)", resp.StatusCode)
		}
		return model.HostMetrics{}, fmt.Errorf("the metrics endpoint answered HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxMetricsBody+1))
	if err != nil {
		return model.HostMetrics{}, fmt.Errorf("cannot read the metrics endpoint: %w", err)
	}
	if len(body) > maxMetricsBody {
		return model.HostMetrics{}, errors.New("the metrics endpoint returned more data than a hardware reading should be")
	}
	var m model.HostMetrics
	if err := json.Unmarshal(body, &m); err != nil {
		return model.HostMetrics{}, errors.New("the metrics endpoint did not return a GWatch hardware reading")
	}
	if m.Timestamp.IsZero() {
		m.Timestamp = time.Now()
	}
	m.Key = model.URLHostKey(check.ID)
	return m, nil
}

// evaluateHost turns a reading into a result by applying the check's
// thresholds. It is separate from fetching so the rules can be tested against
// readings this machine will never produce.
func evaluateHost(check model.Check, m model.HostMetrics, now time.Time) model.Result {
	cfg := check.Config
	res := model.Result{Success: true, Timestamp: now}
	age := now.Sub(m.Timestamp)
	if age < 0 {
		// A machine whose clock runs ahead is not stale; treat it as current
		// rather than as a reading from the future.
		age = 0
	}
	ageSec := age.Seconds()
	res.Details.Host = &m
	res.Details.HostAgeSec = &ageSec

	if limit := staleAfter(check); age > limit {
		return failResult(fmt.Sprintf("no hardware reading for %s (the machine has not reported since %s)",
			humanAge(age), m.Timestamp.Format(time.RFC3339)))
	}

	var warnings, critical []string
	consider := func(label string, value *float64, warn, crit float64, format func(float64) string) {
		if value == nil {
			return
		}
		switch {
		case crit > 0 && *value >= crit:
			critical = append(critical, fmt.Sprintf("%s is %s, at or above the %s critical threshold", label, format(*value), format(crit)))
		case warn > 0 && *value >= warn:
			warnings = append(warnings, fmt.Sprintf("%s is %s, at or above the %s warning threshold", label, format(*value), format(warn)))
		}
	}

	consider("Processor use", m.CPU.UsagePct, cfg.CPUWarnPct, cfg.CPUCritPct, pctString)
	// Where processor utilisation is unavailable — macOS has no cgo-free way
	// to read it — load average per core carries the same meaning, so the
	// check falls back to it rather than silently watching nothing.
	consider("Load per core", m.CPU.LoadPerCore, cfg.LoadWarnPerCore, cfg.LoadCritPerCore, func(v float64) string {
		return fmt.Sprintf("%.2f", v)
	})
	if m.Memory.TotalBytes > 0 {
		used := m.Memory.UsedPct
		consider("Memory use", &used, cfg.MemWarnPct, cfg.MemCritPct, pctString)
	}
	consider("Swap use", m.Memory.SwapUsedPct, cfg.SwapWarnPct, 0, pctString)

	for _, fs := range selectedFilesystems(m, cfg.DiskMounts) {
		used := fs.UsedPct
		consider(fmt.Sprintf("Disk %s", fs.Mount), &used, cfg.DiskWarnPct, cfg.DiskCritPct, pctString)
		if fs.InodesUsedPct != nil && cfg.DiskCritPct > 0 {
			// A filesystem can run out of inodes with space to spare, and the
			// failure looks identical to a full disk to whatever was writing.
			consider(fmt.Sprintf("Inodes on %s", fs.Mount), fs.InodesUsedPct, cfg.DiskWarnPct, cfg.DiskCritPct, pctString)
		}
	}

	if len(critical) > 0 {
		sort.Strings(critical)
		res = failResult(strings.Join(critical, "; "))
		res.Timestamp = now
		res.Details.Host = &m
		res.Details.HostAgeSec = &ageSec
		res.Warnings = warnings
		return res
	}
	sort.Strings(warnings)
	res.Warnings = warnings
	res.Message = m.Describe()
	if len(m.Warnings) > 0 {
		// The collector could not read something. That is worth showing, but
		// it is not a hardware problem, so it does not change the status.
		res.Message += " — " + strings.Join(m.Warnings, "; ")
	}
	return res
}

// selectedFilesystems narrows the reading to the mount points the check cares
// about. An empty list means every filesystem, and a named mount that is not
// present is simply absent rather than an error: a removable volume that is
// not plugged in is not a hardware fault.
func selectedFilesystems(m model.HostMetrics, mounts []string) []model.HostFilesystem {
	if len(mounts) == 0 {
		return m.Filesystems
	}
	want := make(map[string]bool, len(mounts))
	for _, mount := range mounts {
		if mount = strings.TrimSpace(mount); mount != "" {
			want[strings.ToLower(mount)] = true
		}
	}
	var out []model.HostFilesystem
	for _, fs := range m.Filesystems {
		if want[strings.ToLower(fs.Mount)] {
			out = append(out, fs)
		}
	}
	return out
}

// staleAfter is how old a reading may be before the machine counts as down.
func staleAfter(check model.Check) time.Duration {
	secs := check.Config.StaleAfterSeconds
	if secs <= 0 {
		secs = check.IntervalSeconds * defaultStaleFactor
	}
	if secs < minStaleSeconds {
		secs = minStaleSeconds
	}
	return time.Duration(secs) * time.Second
}

func pctString(v float64) string { return fmt.Sprintf("%.0f%%", v) }

// humanAge renders a reading's age the way someone reading an alert would say
// it, rather than as a Go duration.
func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d seconds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%.1f hours", d.Hours())
	}
	return fmt.Sprintf("%.0f days", d.Hours()/24)
}

// validateSystemCheck reports a configuration the hardware check cannot run.
func validateSystemCheck(cfg model.CheckConfig) error {
	if cfg.HostSource != "" && !cfg.HostSource.Valid() {
		return fmt.Errorf("unsupported hardware source %q (use local, agent or url)", cfg.HostSource)
	}
	switch cfg.HostSource {
	case model.HostSourceAgent:
		if cfg.AgentID <= 0 {
			return errors.New("choose which registered machine this check reads")
		}
	case model.HostSourceURL:
		url := strings.TrimSpace(cfg.MetricsURL)
		if url == "" {
			return errors.New("a metrics URL is required when reading an exposed endpoint")
		}
		if !strings.Contains(url, "://") {
			url = "https://" + url
		}
		if _, err := normalizeURL(url); err != nil {
			return err
		}
	}
	pcts := map[string]float64{
		"processor warning":  cfg.CPUWarnPct,
		"processor critical": cfg.CPUCritPct,
		"memory warning":     cfg.MemWarnPct,
		"memory critical":    cfg.MemCritPct,
		"swap warning":       cfg.SwapWarnPct,
		"disk warning":       cfg.DiskWarnPct,
		"disk critical":      cfg.DiskCritPct,
	}
	for name, v := range pcts {
		if v < 0 || v > 100 {
			return fmt.Errorf("the %s threshold must be between 0 (off) and 100", name)
		}
	}
	// A critical threshold below its warning threshold would report the
	// machine down before it ever reported it degraded, which is not what
	// anyone means by those two words.
	for _, pair := range []struct {
		name       string
		warn, crit float64
	}{
		{"processor", cfg.CPUWarnPct, cfg.CPUCritPct},
		{"memory", cfg.MemWarnPct, cfg.MemCritPct},
		{"disk", cfg.DiskWarnPct, cfg.DiskCritPct},
		{"load per core", cfg.LoadWarnPerCore, cfg.LoadCritPerCore},
	} {
		if pair.warn > 0 && pair.crit > 0 && pair.crit < pair.warn {
			return fmt.Errorf("the %s critical threshold must be at or above its warning threshold", pair.name)
		}
	}
	if cfg.LoadWarnPerCore < 0 || cfg.LoadCritPerCore < 0 {
		return errors.New("load thresholds cannot be negative")
	}
	if cfg.StaleAfterSeconds < 0 {
		return errors.New("the staleness limit cannot be negative")
	}
	return nil
}

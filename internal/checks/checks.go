// Package checks executes the individual monitor types (ping, http, cert,
// tcp, dns, keyword, json) and turns the outcome into a model.Result.
//
// Run never returns an error: every failure, including a configuration
// problem, is reported as a Result with Success=false so that the engine can
// store and display it like any other observation.
package checks

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// Options carries the global defaults and the per-check history needed to run
// a check outside of its stored configuration.
type Options struct {
	NodeHost          string  // fallback target when check.Config.Target is empty
	PreviousHash      string  // previous content hash for ContentWatch "hash" ("" = none yet)
	PreviousValue     string  // previous watched value for the other ContentWatch modes
	DefaultCertWarn   int     // global cert warn days (used when Config.CertWarnDays == 0; default 14)
	LatencyWarnMS     float64 // global latency warning threshold (used when Config.LatencyWarnMS == 0; 0 = off)
	PacketLossWarnPct float64 // global packet loss warning threshold (used when Config.PacketLossWarnPct == 0; 0 = off)
	PingMethod        string  // global ping method (used when Config.PingMethod == ""; "" = auto)

	// Hosts supplies hardware readings taken elsewhere — by the engine's
	// sampler for this computer, or by an agent that pushed them in. It is nil
	// outside the engine, and a hardware check then reports that it cannot
	// read anything rather than panicking.
	Hosts HostReader
}

const (
	defaultTimeout   = 10 * time.Second
	defaultCertWarn  = 14
	maxRetries       = 10
	retryPause       = 500 * time.Millisecond
	maxBodyBytes     = 2 << 20 // 2 MiB
	maxRedirects     = 10
	minIntervalSecs  = 10
	maxTimeoutSecs   = 300
	defaultPingCount = 4
	maxPingCount     = 20
)

// Run executes the check once including its configured immediate retries.
// Each attempt is bounded by check.TimeoutSeconds (default 10s) and the whole
// run by ctx. The returned result describes the last attempt; Attempts holds
// the number of attempts made.
func Run(ctx context.Context, check model.Check, opts Options) model.Result {
	start := time.Now()
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := attemptTimeout(check)
	attempts := check.Retries + 1
	if attempts < 1 {
		attempts = 1
	}
	if attempts > maxRetries+1 {
		attempts = maxRetries + 1
	}

	var res model.Result
	for i := 0; i < attempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				res.Attempts = i
				return finalize(res, check, opts, start)
			case <-time.After(retryPause):
			}
		}
		actx, cancel := context.WithTimeout(ctx, timeout)
		res = runOnce(actx, check, opts)
		cancel()
		res.Attempts = i + 1
		if res.Success || ctx.Err() != nil {
			break
		}
	}
	return finalize(res, check, opts, start)
}

// runOnce executes a single attempt and recovers from unexpected panics so a
// buggy runner can never take the scheduler down.
func runOnce(ctx context.Context, check model.Check, opts Options) (res model.Result) {
	defer func() {
		if r := recover(); r != nil {
			res = failResult(fmt.Sprintf("internal error: %v", r))
		}
	}()
	// A hardware check reads a machine, not an address: only the "url" source
	// has a target at all, and it carries its own.
	if check.Type == model.CheckSystem {
		return runSystemCheck(ctx, check, opts)
	}
	target := check.Target(opts.NodeHost)
	if target == "" {
		return failResult("no target configured")
	}
	switch check.Type {
	case model.CheckPing:
		return runPingCheck(ctx, check, target, opts)
	case model.CheckHTTP, model.CheckKeyword, model.CheckJSON:
		return runHTTPCheck(ctx, check, target, opts)
	case model.CheckCert:
		return runCertCheck(ctx, check, target, opts)
	case model.CheckTCP:
		return runTCPCheck(ctx, check, target)
	case model.CheckDNS:
		return runDNSCheck(ctx, check, target)
	case model.CheckCustom:
		return runCustomCheck(ctx, check, target)
	case model.CheckSNMP:
		return runSNMPCheck(ctx, check, target, opts)
	}
	return failResult(fmt.Sprintf("unsupported check type %q", check.Type))
}

// finalize applies the cross-cutting rules: timestamps, the global latency
// warning and the derived Status.
func finalize(res model.Result, check model.Check, opts Options, start time.Time) model.Result {
	res.Timestamp = start
	res.CheckID = check.ID
	if res.Attempts == 0 {
		res.Attempts = 1
	}
	if res.Success && res.LatencyMS != nil {
		if warn := pick(check.Config.LatencyWarnMS, opts.LatencyWarnMS); warn > 0 && *res.LatencyMS >= warn {
			res.Warnings = append(res.Warnings, fmt.Sprintf("Latency %s ms is above the %s ms warning threshold", fmtMS(*res.LatencyMS), fmtMS(warn)))
		}
	}
	if !res.Success {
		res.Status = model.StatusDown
		if res.Error == "" {
			res.Error = res.Message
		}
		if res.Message == "" {
			res.Message = res.Error
		}
	} else if len(res.Warnings) > 0 {
		res.Status = model.StatusDegraded
	} else {
		res.Status = model.StatusUp
	}
	return res
}

func attemptTimeout(check model.Check) time.Duration {
	if check.TimeoutSeconds <= 0 {
		return defaultTimeout
	}
	return time.Duration(check.TimeoutSeconds) * time.Second
}

func failResult(msg string) model.Result {
	return model.Result{Success: false, Status: model.StatusDown, Message: msg, Error: msg}
}

// Validate returns a user-facing error when the check configuration cannot be
// run. nodeHost is the owning node's host, used when the check has no target
// of its own.
func Validate(check model.Check, nodeHost string) error {
	if !check.Type.Valid() {
		return fmt.Errorf("unsupported check type %q", check.Type)
	}
	cfg := check.Config
	target := check.Target(nodeHost)
	// A hardware check names a machine rather than an address, so it is the
	// one type with nothing to resolve; validateSystemCheck checks what it
	// does need instead.
	if target == "" && check.Type != model.CheckSystem {
		return errors.New("a target (or a node host) is required")
	}
	if check.IntervalSeconds != 0 && check.IntervalSeconds < minIntervalSecs {
		return fmt.Errorf("interval must be at least %d seconds", minIntervalSecs)
	}
	if check.TimeoutSeconds < 0 || check.TimeoutSeconds > maxTimeoutSecs {
		return fmt.Errorf("timeout must be between 1 and %d seconds", maxTimeoutSecs)
	}
	if check.Retries < 0 || check.Retries > maxRetries {
		return fmt.Errorf("retries must be between 0 and %d", maxRetries)
	}
	if check.FailureThreshold < 0 {
		return errors.New("failure threshold cannot be negative")
	}
	if cfg.LatencyWarnMS < 0 {
		return errors.New("latency warning threshold cannot be negative")
	}
	if cfg.PacketLossWarnPct < 0 || cfg.PacketLossWarnPct > 100 {
		return errors.New("packet loss warning threshold must be between 0 and 100")
	}
	if cfg.CertWarnDays < 0 {
		return errors.New("certificate warning days cannot be negative")
	}

	switch check.Type {
	case model.CheckPing:
		if cfg.PingCount < 0 || cfg.PingCount > maxPingCount {
			return fmt.Errorf("ping count must be between 1 and %d", maxPingCount)
		}
		if cfg.PingMethod != "" && !model.ValidPingMethod(cfg.PingMethod) {
			return errors.New(`ping method must be "auto", "builtin" or "system" (or empty to follow the global setting)`)
		}
		if _, err := hostOnly(target); err != nil {
			return err
		}
	case model.CheckHTTP, model.CheckKeyword, model.CheckJSON:
		if _, err := normalizeURL(target); err != nil {
			return err
		}
		if _, err := ParseStatusExpr(cfg.ExpectedStatus); err != nil {
			return err
		}
		if m := strings.TrimSpace(cfg.Method); m != "" && !isToken(m) {
			return fmt.Errorf("invalid HTTP method %q", cfg.Method)
		}
		for k := range cfg.Headers {
			if strings.TrimSpace(k) == "" || !isToken(strings.TrimSpace(k)) {
				return fmt.Errorf("invalid header name %q", k)
			}
		}
		if check.Type == model.CheckKeyword && strings.TrimSpace(cfg.Keyword) == "" {
			return errors.New("keyword checks need a keyword to look for")
		}
		if check.Type == model.CheckJSON && cfg.JSONRecord {
			if err := validateJSONRecord(cfg); err != nil {
				return err
			}
		}
		switch cfg.ContentWatch {
		case "", "hash", "redirect", "json":
		case "header":
			if strings.TrimSpace(cfg.ContentHeader) == "" {
				return errors.New("content watch by header needs a header name")
			}
		case "keyword":
			if strings.TrimSpace(cfg.Keyword) == "" {
				return errors.New("content watch by keyword needs a keyword")
			}
		default:
			return fmt.Errorf("unsupported content watch mode %q", cfg.ContentWatch)
		}
	case model.CheckCert:
		if cfg.Port < 0 || cfg.Port > 65535 {
			return errors.New("port must be between 1 and 65535")
		}
		if _, _, err := hostPort(target, cfg.Port, 443); err != nil {
			return err
		}
	case model.CheckTCP:
		if cfg.Port < 0 || cfg.Port > 65535 {
			return errors.New("port must be between 1 and 65535")
		}
		if _, _, err := hostPort(target, cfg.Port, 0); err != nil {
			return err
		}
	case model.CheckDNS:
		switch strings.ToUpper(strings.TrimSpace(cfg.RecordType)) {
		case "", "A", "AAAA", "CNAME", "MX", "TXT":
		default:
			return fmt.Errorf("unsupported DNS record type %q (use A, CNAME, MX or TXT)", cfg.RecordType)
		}
		if cfg.DNSServer != "" {
			if _, err := resolverAddr(cfg.DNSServer); err != nil {
				return err
			}
		}
		if _, err := hostOnly(target); err != nil {
			return err
		}
	case model.CheckSystem:
		if err := validateSystemCheck(cfg); err != nil {
			return err
		}
	case model.CheckCustom:
		if err := validateCustomCheck(cfg); err != nil {
			return err
		}
	case model.CheckSNMP:
		if _, _, err := hostPort(target, cfg.SNMPPort, defaultSNMPPort); err != nil {
			return err
		}
		if err := validateSNMPCheck(cfg); err != nil {
			return err
		}
	}
	return nil
}

// maxJSONMetricName bounds the metric name a recording json check may choose.
// The name is a chart title and a query parameter, not a place for prose.
const maxJSONMetricName = 64

// validateJSONRecord checks the recording half of a json check: there has to
// be a path to read, a name to store the value under, and thresholds that
// escalate in the right order — the same rules validateSNMPCheck applies to
// an OID, because the value ends up in the same place.
func validateJSONRecord(cfg model.CheckConfig) error {
	if strings.TrimSpace(cfg.JSONPath) == "" {
		return errors.New("recording a JSON value needs a JSON path to read it from")
	}
	name := cfg.JSONMetricName()
	if len(name) > maxJSONMetricName {
		return fmt.Errorf("the metric name must be at most %d characters", maxJSONMetricName)
	}
	if cfg.JSONWarnAbove != nil && cfg.JSONCritAbove != nil && *cfg.JSONWarnAbove >= *cfg.JSONCritAbove {
		return fmt.Errorf("%s: the critical threshold must be above the warning one", name)
	}
	if cfg.JSONWarnBelow != nil && cfg.JSONCritBelow != nil && *cfg.JSONWarnBelow <= *cfg.JSONCritBelow {
		return fmt.Errorf("%s: the critical threshold must be below the warning one", name)
	}
	return nil
}

// ---- shared helpers -------------------------------------------------------

func pick(local, global float64) float64 {
	if local > 0 {
		return local
	}
	return global
}

func pickInt(local, global, fallback int) int {
	if local > 0 {
		return local
	}
	if global > 0 {
		return global
	}
	return fallback
}

func round1(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Round(v*10) / 10
}

func msOf(d time.Duration) float64 {
	return round1(float64(d) / float64(time.Millisecond))
}

func msPtr(d time.Duration) *float64 {
	v := msOf(d)
	return &v
}

func fptr(v float64) *float64 {
	v = round1(v)
	return &v
}

func bptr(b bool) *bool { return &b }

func fmtMS(v float64) string {
	return strconv.FormatFloat(round1(v), 'f', -1, 64)
}

func fmtDur(d time.Duration) string {
	if d%time.Second == 0 {
		return fmt.Sprintf("%ds", int(d/time.Second))
	}
	return d.String()
}

func isToken(s string) bool {
	for _, r := range s {
		if r <= ' ' || r >= 0x7f || strings.ContainsRune("()<>@,;:\\\"/[]?={}", r) {
			return false
		}
	}
	return s != ""
}

// normalizeURL adds https:// when no scheme is present and validates the URL.
func normalizeURL(target string) (*url.URL, error) {
	t := strings.TrimSpace(target)
	if t == "" {
		return nil, errors.New("a URL is required")
	}
	if !strings.Contains(t, "://") {
		t = "https://" + t
	}
	u, err := url.Parse(t)
	if err != nil {
		return nil, fmt.Errorf("invalid URL %q: %v", target, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported URL scheme %q (use http or https)", u.Scheme)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("URL %q has no host", target)
	}
	return u, nil
}

// hostOnly extracts a bare hostname/IP from a target that may be a hostname,
// host:port or a URL.
func hostOnly(target string) (string, error) {
	t := strings.TrimSpace(target)
	if strings.Contains(t, "://") {
		u, err := url.Parse(t)
		if err != nil {
			return "", fmt.Errorf("invalid target %q: %v", target, err)
		}
		t = u.Host
	}
	if h, _, err := net.SplitHostPort(t); err == nil {
		t = h
	}
	t = strings.Trim(t, "[]")
	if t == "" {
		return "", errors.New("a hostname or IP address is required")
	}
	if strings.ContainsAny(t, " /\\") {
		return "", fmt.Errorf("invalid hostname %q", t)
	}
	return t, nil
}

// hostPort resolves the host and port for the tcp and cert checks. The
// configured port wins, then a port embedded in the target, then the default.
// A default of 0 means the port is required.
func hostPort(target string, cfgPort, defaultPort int) (string, int, error) {
	t := strings.TrimSpace(target)
	port := 0
	if strings.Contains(t, "://") {
		u, err := url.Parse(t)
		if err != nil {
			return "", 0, fmt.Errorf("invalid target %q: %v", target, err)
		}
		if p := u.Port(); p != "" {
			port, _ = strconv.Atoi(p)
		} else if u.Scheme == "http" {
			port = 80
		} else if u.Scheme == "https" {
			port = 443
		}
		t = u.Hostname()
	} else if h, p, err := net.SplitHostPort(t); err == nil {
		t = h
		port, _ = strconv.Atoi(p)
	}
	t = strings.Trim(t, "[]")
	if t == "" {
		return "", 0, errors.New("a hostname or IP address is required")
	}
	if strings.ContainsAny(t, " /\\") {
		return "", 0, fmt.Errorf("invalid hostname %q", t)
	}
	if cfgPort > 0 {
		port = cfgPort
	}
	if port == 0 {
		port = defaultPort
	}
	if port <= 0 || port > 65535 {
		return "", 0, errors.New("a port between 1 and 65535 is required")
	}
	return t, port, nil
}

// Winsock error numbers for the conditions netErrorLabel rewords. Windows
// does not report the POSIX errnos for socket failures, and the text it
// produces is localized, so they are matched numerically. The values never
// occur on other platforms, so they are safe to test for everywhere.
const (
	wsaeNetUnreach  = syscall.Errno(10051)
	wsaeConnReset   = syscall.Errno(10054)
	wsaeConnRefused = syscall.Errno(10061)
	wsaeHostUnreach = syscall.Errno(10065)
)

// netErrorLabel maps a socket error number to the wording shown to the user,
// or "" when the error is not one of the conditions worth rewording. Matching
// the errno rather than the message keeps the wording identical on Windows,
// where the operating system text is both different and translated.
func netErrorLabel(err error) string {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return ""
	}
	switch errno {
	case syscall.ECONNREFUSED, wsaeConnRefused:
		return "connection refused"
	case syscall.EHOSTUNREACH, wsaeHostUnreach:
		return "no route to host"
	case syscall.ENETUNREACH, wsaeNetUnreach:
		return "network is unreachable"
	case syscall.ECONNRESET, wsaeConnReset:
		return "connection reset by peer"
	}
	return ""
}

// describeNetError turns a low-level network error into the wording shown to
// the user.
func describeNetError(err error, timeout time.Duration) string {
	if err == nil {
		return ""
	}
	var uerr *url.Error
	if errors.As(err, &uerr) {
		err = uerr.Err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Sprintf("timed out after %s", fmtDur(timeout))
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		if dnsErr.IsTimeout {
			return fmt.Sprintf("DNS lookup failed: timed out resolving %s", dnsErr.Name)
		}
		if dnsErr.IsNotFound {
			return fmt.Sprintf("DNS lookup failed: %s not found", dnsErr.Name)
		}
		return "DNS lookup failed: " + dnsErr.Error()
	}
	if isTLSError(err) {
		msg := err.Error()
		msg = strings.TrimPrefix(msg, "tls: ")
		return "TLS: " + msg
	}
	if label := netErrorLabel(err); label != "" {
		return label
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "connection refused"):
		return "connection refused"
	case strings.Contains(lower, "no route to host"):
		return "no route to host"
	case strings.Contains(lower, "network is unreachable"):
		return "network is unreachable"
	case strings.Contains(lower, "connection reset"):
		return "connection reset by peer"
	}
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return fmt.Sprintf("timed out after %s", fmtDur(timeout))
	}
	// Trim the noisy "dial tcp 1.2.3.4:443: " prefix Go adds.
	if i := strings.LastIndex(msg, ": "); i >= 0 && strings.HasPrefix(msg, "dial ") {
		msg = msg[i+2:]
	}
	return msg
}

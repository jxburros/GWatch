package checks

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

const userAgent = "GWatch/1.0 (+local network monitor)"

// httpTiming accumulates httptrace timings. Redirect hops add up.
type httpTiming struct {
	mu        sync.Mutex
	start     time.Time
	dnsStart  time.Time
	connStart time.Time
	tlsStart  time.Time
	dns       time.Duration
	connect   time.Duration
	tlsDur    time.Duration
	firstByte time.Duration
	sawDNS    bool
	sawConn   bool
	sawTLS    bool
	sawFirst  bool
}

func (t *httpTiming) trace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) {
			t.mu.Lock()
			t.dnsStart = time.Now()
			t.mu.Unlock()
		},
		DNSDone: func(httptrace.DNSDoneInfo) {
			t.mu.Lock()
			if !t.dnsStart.IsZero() {
				t.dns += time.Since(t.dnsStart)
				t.sawDNS = true
			}
			t.mu.Unlock()
		},
		ConnectStart: func(string, string) {
			t.mu.Lock()
			t.connStart = time.Now()
			t.mu.Unlock()
		},
		ConnectDone: func(_, _ string, err error) {
			t.mu.Lock()
			if !t.connStart.IsZero() {
				t.connect += time.Since(t.connStart)
				t.sawConn = true
			}
			t.mu.Unlock()
		},
		TLSHandshakeStart: func() {
			t.mu.Lock()
			t.tlsStart = time.Now()
			t.mu.Unlock()
		},
		TLSHandshakeDone: func(tls.ConnectionState, error) {
			t.mu.Lock()
			if !t.tlsStart.IsZero() {
				t.tlsDur += time.Since(t.tlsStart)
				t.sawTLS = true
			}
			t.mu.Unlock()
		},
		GotFirstResponseByte: func() {
			t.mu.Lock()
			t.firstByte = time.Since(t.start)
			t.sawFirst = true
			t.mu.Unlock()
		},
	}
}

func (t *httpTiming) fill(d *model.ResultDetails) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sawDNS {
		d.DNSMs = msPtr(t.dns)
	}
	if t.sawConn {
		d.ConnectMs = msPtr(t.connect)
	}
	if t.sawTLS {
		d.TLSMs = msPtr(t.tlsDur)
	}
	if t.sawFirst {
		d.FirstByteMs = msPtr(t.firstByte)
	}
}

// runHTTPCheck implements the http, keyword and json check types.
func runHTTPCheck(ctx context.Context, check model.Check, target string, opts Options) model.Result {
	cfg := check.Config
	timeout := attemptTimeout(check)

	u, err := normalizeURL(target)
	if err != nil {
		return failResult(err.Error())
	}
	matcher, err := ParseStatusExpr(cfg.ExpectedStatus)
	if err != nil {
		return failResult(err.Error())
	}
	if check.Type == model.CheckKeyword && strings.TrimSpace(cfg.Keyword) == "" {
		return failResult("keyword checks need a keyword to look for")
	}

	method := strings.ToUpper(strings.TrimSpace(cfg.Method))
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if cfg.Body != "" {
		body = strings.NewReader(cfg.Body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return failResult(fmt.Sprintf("invalid request: %v", err))
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "*/*")
	for k, v := range cfg.Headers {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if strings.EqualFold(k, "Host") {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	if cfg.Body != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	timing := &httpTiming{start: time.Now()}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), timing.trace()))

	transport := &http.Transport{
		Proxy:                  nil, // a local monitor always connects directly
		DialContext:            (&net.Dialer{Timeout: timeout, KeepAlive: -1}).DialContext,
		TLSClientConfig:        &tls.Config{InsecureSkipVerify: cfg.IgnoreTLSErrors, MinVersion: tls.VersionTLS10}, //nolint:gosec // user opt-in
		TLSHandshakeTimeout:    timeout,
		ResponseHeaderTimeout:  timeout,
		DisableKeepAlives:      true,
		ForceAttemptHTTP2:      true,
		MaxResponseHeaderBytes: 1 << 20,
	}
	defer transport.CloseIdleConnections()

	follow := cfg.FollowRedirects == nil || *cfg.FollowRedirects
	redirects := 0
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if !follow {
				return http.ErrUseLastResponse
			}
			if len(via) >= maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			redirects = len(via)
			return nil
		},
	}

	res := model.Result{}
	resp, err := client.Do(req)
	total := time.Since(timing.start)
	timing.fill(&res.Details)
	if err != nil {
		msg := describeNetError(err, timeout)
		res.Success = false
		res.Error = msg
		res.Message = msg
		res.Details.Redirects = redirects
		// A failed TLS verification still deserves certificate details in
		// the inspector, so probe the leaf certificate separately.
		if u.Scheme == "https" && isTLSError(err) && ctx.Err() == nil {
			if info, _, _, perr := probeCert(ctx, u.Hostname(), urlPort(u), timeout); perr == nil {
				res.Details.Cert = info
			}
		}
		return res
	}
	defer resp.Body.Close()

	bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	total = time.Since(timing.start)
	res.LatencyMS = msPtr(total)
	res.Details.TotalMs = msPtr(total)
	res.Details.StatusCode = resp.StatusCode
	res.Details.Redirects = redirects
	if resp.Request != nil && resp.Request.URL != nil {
		res.Details.FinalURL = resp.Request.URL.String()
	} else {
		res.Details.FinalURL = u.String()
	}
	if resp.ContentLength >= 0 {
		res.Details.ContentLength = resp.ContentLength
	} else {
		res.Details.ContentLength = int64(len(bodyBytes))
	}
	if readErr != nil && len(bodyBytes) == 0 {
		msg := "reading response failed: " + describeNetError(readErr, timeout)
		res.Message = msg
		res.Error = msg
		return res
	}

	// Certificate details.
	warnDays := pickInt(cfg.CertWarnDays, opts.DefaultCertWarn, defaultCertWarn)
	if resp.TLS != nil && (cfg.CertCheck == nil || *cfg.CertCheck) && len(resp.TLS.PeerCertificates) > 0 {
		info := certInfoFromState(resp.TLS, u.Hostname(), cfg.IgnoreTLSErrors)
		res.Details.Cert = info
		res.Warnings = append(res.Warnings, certWarnings(info, warnDays)...)
	}

	// Status expectation.
	res.Success = matcher.Matches(resp.StatusCode)
	if res.Success {
		res.Message = fmt.Sprintf("HTTP %d in %s ms", resp.StatusCode, fmtMS(*res.LatencyMS))
	} else {
		res.Details.UnexpectedCode = true
		res.Message = fmt.Sprintf("Unexpected HTTP %d (expected %s)", resp.StatusCode, matcher.String())
		res.Error = res.Message
	}

	bodyText := string(bodyBytes)

	// Keyword.
	if check.Type == model.CheckKeyword {
		found := strings.Contains(bodyText, cfg.Keyword)
		res.Details.KeywordFound = bptr(found)
		ok := found != cfg.KeywordAbsent
		var msg string
		switch {
		case found && !cfg.KeywordAbsent:
			msg = fmt.Sprintf("Keyword %q found", cfg.Keyword)
		case found && cfg.KeywordAbsent:
			msg = fmt.Sprintf("Keyword %q found (expected absent)", cfg.Keyword)
		case !found && cfg.KeywordAbsent:
			msg = fmt.Sprintf("Keyword %q absent as expected", cfg.Keyword)
		default:
			msg = fmt.Sprintf("Keyword %q not found", cfg.Keyword)
		}
		if res.Success {
			res.Success = ok
			res.Message = msg
			if !ok {
				res.Error = msg
			}
		} else {
			res.Message += "; " + msg
		}
	}

	// JSON.
	var jsonDoc any
	jsonParsed := false
	parseJSON := func() bool {
		if jsonParsed {
			return jsonDoc != nil || len(bodyBytes) > 0
		}
		jsonParsed = true
		dec := json.NewDecoder(strings.NewReader(bodyText))
		dec.UseNumber()
		if err := dec.Decode(&jsonDoc); err != nil {
			jsonDoc = nil
			return false
		}
		return true
	}
	if check.Type == model.CheckJSON {
		path := strings.TrimSpace(cfg.JSONPath)
		var msg string
		ok := false
		if !parseJSON() {
			msg = "response is not valid JSON"
		} else {
			val, exists := resolveJSONPath(jsonDoc, path)
			if exists {
				res.Details.JSONValue = jsonValueString(val)
			}
			label := path
			if label == "" {
				label = "$"
			}
			switch {
			case !exists:
				msg = fmt.Sprintf("json path %q not found", label)
			case cfg.JSONExpected == "":
				ok = true
				msg = fmt.Sprintf("json %q = %s", label, quoteIfNeeded(res.Details.JSONValue))
			default:
				ok = jsonValueEquals(res.Details.JSONValue, cfg.JSONExpected)
				if ok {
					msg = fmt.Sprintf("json %q = %s", label, quoteIfNeeded(res.Details.JSONValue))
				} else {
					msg = fmt.Sprintf("json %q = %s (expected %s)", label, quoteIfNeeded(res.Details.JSONValue), quoteIfNeeded(cfg.JSONExpected))
				}
			}
		}
		res.Details.JSONMatched = bptr(ok)
		if res.Success {
			res.Success = ok
			res.Message = msg
			if !ok {
				res.Error = msg
			}
		} else {
			res.Message += "; " + msg
		}
	}

	// Optional content change watch.
	switch cfg.ContentWatch {
	case "hash":
		res.Details.ContentHash = contentHash(bodyBytes)
		if opts.PreviousHash != "" && opts.PreviousHash != res.Details.ContentHash {
			res.Details.ContentChanged = true
			res.Warnings = append(res.Warnings, "Response content changed")
		}
	case "header", "redirect", "keyword", "json":
		var value, what string
		switch cfg.ContentWatch {
		case "header":
			value = resp.Header.Get(cfg.ContentHeader)
			what = fmt.Sprintf("header %q", cfg.ContentHeader)
		case "redirect":
			value = res.Details.FinalURL
			what = "final URL"
		case "keyword":
			if strings.Contains(bodyText, cfg.Keyword) {
				value = "found"
			} else {
				value = "missing"
			}
			what = fmt.Sprintf("keyword %q", cfg.Keyword)
		case "json":
			if parseJSON() {
				if v, exists := resolveJSONPath(jsonDoc, strings.TrimSpace(cfg.JSONPath)); exists {
					value = jsonValueString(v)
				}
			}
			what = fmt.Sprintf("json %q", cfg.JSONPath)
		}
		res.Details.ContentValue = value
		if opts.PreviousValue != "" && opts.PreviousValue != value {
			res.Details.ContentChanged = true
			res.Warnings = append(res.Warnings, fmt.Sprintf("Response content changed: %s is now %s (was %s)", what, quoteIfNeeded(value), quoteIfNeeded(opts.PreviousValue)))
		}
	}

	return res
}

func urlPort(u interface{ Port() string }) int {
	if p, err := strconv.Atoi(u.Port()); err == nil && p > 0 {
		return p
	}
	return 443
}

func quoteIfNeeded(s string) string {
	if s == "" {
		return `""`
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil || s == "true" || s == "false" || s == "null" {
		return s
	}
	if strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[") {
		return s
	}
	return strconv.Quote(s)
}

// isTLSError reports whether err comes from the TLS layer (handshake, alert
// or certificate verification).
func isTLSError(err error) bool {
	if err == nil {
		return false
	}
	var certErr *tls.CertificateVerificationError
	var recErr tls.RecordHeaderError
	var alertErr tls.AlertError
	var unknownAuth x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var hostErr x509.HostnameError
	if errors.As(err, &certErr) || errors.As(err, &recErr) || errors.As(err, &alertErr) ||
		errors.As(err, &unknownAuth) || errors.As(err, &invalid) || errors.As(err, &hostErr) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "tls:") || strings.Contains(msg, "x509:") || strings.Contains(msg, "certificate")
}

// ---- content normalisation ----------------------------------------------

var (
	reScript   = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>`)
	reComment  = regexp.MustCompile(`(?s)<!--.*?-->`)
	reSpaceRun = regexp.MustCompile(`\s+`)
)

// contentHash returns the sha256 of the body after normalisation: scripts and
// HTML comments removed, lower-cased, whitespace runs collapsed.
func contentHash(body []byte) string {
	s := string(body)
	s = reScript.ReplaceAllString(s, "")
	s = reComment.ReplaceAllString(s, "")
	s = strings.ToLower(s)
	s = reSpaceRun.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// ---- JSON path ------------------------------------------------------------

// resolveJSONPath walks a decoded JSON document along a dotted path with
// optional [n] array indexes (e.g. "data.items[0].name"). An empty path
// returns the whole document. A leading "$" or "$." is accepted.
func resolveJSONPath(doc any, path string) (any, bool) {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "$")
	path = strings.TrimPrefix(path, ".")
	if path == "" {
		return doc, true
	}
	cur := doc
	for _, seg := range splitJSONPath(path) {
		switch seg.kind {
		case segKey:
			m, ok := cur.(map[string]any)
			if !ok {
				return nil, false
			}
			v, ok := m[seg.key]
			if !ok {
				return nil, false
			}
			cur = v
		case segIndex:
			arr, ok := cur.([]any)
			if !ok {
				return nil, false
			}
			idx := seg.index
			if idx < 0 {
				idx = len(arr) + idx
			}
			if idx < 0 || idx >= len(arr) {
				return nil, false
			}
			cur = arr[idx]
		}
	}
	return cur, true
}

type segKind int

const (
	segKey segKind = iota
	segIndex
)

type pathSeg struct {
	kind  segKind
	key   string
	index int
}

func splitJSONPath(path string) []pathSeg {
	var segs []pathSeg
	var key strings.Builder
	flushKey := func() {
		if key.Len() > 0 {
			segs = append(segs, pathSeg{kind: segKey, key: key.String()})
			key.Reset()
		}
	}
	i := 0
	for i < len(path) {
		c := path[i]
		switch c {
		case '.':
			flushKey()
			i++
		case '[':
			flushKey()
			end := strings.IndexByte(path[i:], ']')
			if end < 0 {
				// Unterminated bracket: treat the rest as a literal key.
				key.WriteString(path[i:])
				i = len(path)
				continue
			}
			inner := strings.TrimSpace(path[i+1 : i+end])
			if n, err := strconv.Atoi(inner); err == nil {
				segs = append(segs, pathSeg{kind: segIndex, index: n})
			} else {
				inner = strings.Trim(inner, `"'`)
				segs = append(segs, pathSeg{kind: segKey, key: inner})
			}
			i += end + 1
		default:
			key.WriteByte(c)
			i++
		}
	}
	flushKey()
	return segs
}

// jsonValueString renders a JSON value for display and comparison: scalars
// plain, containers as compact JSON, null as "null".
func jsonValueString(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case json.Number:
		return t.String()
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprint(t)
		}
		return string(b)
	}
}

// jsonValueEquals compares the rendered value with the expectation as strings,
// treating numerically equal values (1 == 1.0) as a match.
func jsonValueEquals(got, expected string) bool {
	if got == expected {
		return true
	}
	if strings.TrimSpace(got) == strings.TrimSpace(expected) {
		return true
	}
	g, err1 := strconv.ParseFloat(strings.TrimSpace(got), 64)
	e, err2 := strconv.ParseFloat(strings.TrimSpace(expected), 64)
	if err1 == nil && err2 == nil {
		return g == e
	}
	// Allow the user to quote the expected string.
	if unq, err := strconv.Unquote(strings.TrimSpace(expected)); err == nil && unq == got {
		return true
	}
	return false
}

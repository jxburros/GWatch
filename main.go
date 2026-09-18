package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type checkType string

const (
	checkTypePing checkType = "ping"
	checkTypeHTTP checkType = "http"
	checkTypeDNS  checkType = "dns"
)

type checkRequest struct {
	Type           checkType `json:"type"`
	Target         string    `json:"target"`
	TimeoutSeconds int       `json:"timeoutSeconds"`
}

type checkResult struct {
	Type       checkType `json:"type"`
	Target     string    `json:"target"`
	Success    bool      `json:"success"`
	Message    string    `json:"message"`
	DurationMS int64     `json:"durationMs"`
}

var pingCommand = exec.CommandContext
var blockedIPPrefixes = mustParsePrefixes(
	"0.0.0.0/8",
	"100.64.0.0/10",
	"192.0.0.0/24",
	"192.0.2.0/24",
	"198.18.0.0/15",
	"198.51.100.0/24",
	"203.0.113.0/24",
	"240.0.0.0/4",
	"::/128",
	"::1/128",
	"::ffff:0:0/96",
	"64:ff9b:1::/48",
	"100::/64",
	"2001:db8::/32",
	"2001:10::/28",
	"fc00::/7",
	"fe80::/10",
)

func main() {
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir("web")))
	mux.HandleFunc("/api/check", checkHandler)
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	server := &http.Server{
		Addr:              ":8080",
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		Handler:           mux,
	}

	fmt.Println("GWatch listening on http://localhost:8080")
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		panic(err)
	}
}

func checkHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var req checkRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	result, err := runCheck(r.Context(), req)
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	respondJSON(w, http.StatusOK, result)
}

func runCheck(parent context.Context, req checkRequest) (checkResult, error) {
	target := strings.TrimSpace(req.Target)
	if target == "" {
		return checkResult{}, fmt.Errorf("target is required")
	}

	timeout := 5
	if req.TimeoutSeconds > 0 {
		timeout = req.TimeoutSeconds
	}

	ctx, cancel := context.WithTimeout(parent, time.Duration(timeout)*time.Second)
	defer cancel()

	start := time.Now()
	var result checkResult
	var err error

	switch req.Type {
	case checkTypePing:
		result, err = runPingCheck(ctx, target, timeout)
	case checkTypeHTTP:
		result, err = runHTTPCheck(ctx, target)
	case checkTypeDNS:
		result, err = runDNSCheck(ctx, target)
	default:
		return checkResult{}, fmt.Errorf("unsupported check type: %s", req.Type)
	}

	result.Type = req.Type
	result.Target = target
	result.DurationMS = time.Since(start).Milliseconds()
	return result, err
}

func runPingCheck(ctx context.Context, target string, timeoutSeconds int) (checkResult, error) {
	args := []string{"-c", "1", "-W", strconv.Itoa(timeoutSeconds), target}
	if runtime.GOOS == "windows" {
		args = []string{"-n", "1", "-w", strconv.Itoa(timeoutSeconds * 1000), target}
	}

	out, err := pingCommand(ctx, "ping", args...).CombinedOutput()
	if err != nil {
		return checkResult{Success: false, Message: strings.TrimSpace(string(out))}, nil
	}

	return checkResult{Success: true, Message: "ping successful"}, nil
}

func runHTTPCheck(ctx context.Context, target string) (checkResult, error) {
	u, err := normalizeHTTPTarget(target)
	if err != nil {
		return checkResult{}, fmt.Errorf("invalid URL target")
	}
	if err := validateHTTPHost(ctx, u.Hostname()); err != nil {
		return checkResult{}, err
	}

	client := buildHTTPClient(ctx)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return checkResult{}, err
	}

	// lgtm[go/request-forgery]
	resp, err := client.Do(req)
	if err != nil {
		return checkResult{Success: false, Message: err.Error()}, nil
	}
	defer resp.Body.Close()

	success := resp.StatusCode < http.StatusBadRequest
	return checkResult{Success: success, Message: fmt.Sprintf("HTTP %d", resp.StatusCode)}, nil
}

func normalizeHTTPTarget(target string) (*url.URL, error) {
	urlText := target
	if !strings.HasPrefix(urlText, "http://") && !strings.HasPrefix(urlText, "https://") {
		urlText = "https://" + urlText
	}
	return url.ParseRequestURI(urlText)
}

func buildHTTPClient(ctx context.Context) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: safeValidatedDialContext,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after too many redirects")
			}
			return validateHTTPHost(ctx, req.URL.Hostname())
		},
	}
}

func validateHTTPHost(ctx context.Context, host string) error {
	if allowPrivateHTTPTargets() {
		return nil
	}

	if strings.EqualFold(host, "localhost") {
		return fmt.Errorf("localhost is not allowed for HTTP checks")
	}

	ip := net.ParseIP(host)
	if ip != nil {
		if isDisallowedIP(ip) {
			return fmt.Errorf("private or local IP targets are not allowed for HTTP checks")
		}
		return nil
	}

	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return err
	}
	for _, ipAddr := range addrs {
		if isDisallowedIP(ipAddr.IP) {
			return fmt.Errorf("private or local IP targets are not allowed for HTTP checks")
		}
	}

	return nil
}

func isDisallowedIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	addr = addr.Unmap()

	if !addr.IsValid() || !addr.IsGlobalUnicast() || addr.IsLoopback() || addr.IsPrivate() || addr.IsMulticast() || addr.IsLinkLocalUnicast() {
		return true
	}

	for _, prefix := range blockedIPPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}

	return false
}

func allowPrivateHTTPTargets() bool {
	return strings.EqualFold(os.Getenv("GWATCH_ALLOW_PRIVATE_HTTP_TARGETS"), "true")
}

func safeValidatedDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}

	if allowPrivateHTTPTargets() {
		return conn, nil
	}

	remoteAddr := conn.RemoteAddr().String()
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	ip := net.ParseIP(host)
	if ip == nil || isDisallowedIP(ip) {
		_ = conn.Close()
		return nil, fmt.Errorf("private or local IP targets are not allowed for HTTP checks")
	}

	return conn, nil
}

func mustParsePrefixes(prefixes ...string) []netip.Prefix {
	parsed := make([]netip.Prefix, 0, len(prefixes))
	for _, prefix := range prefixes {
		p, err := netip.ParsePrefix(prefix)
		if err != nil {
			panic(err)
		}
		parsed = append(parsed, p)
	}
	return parsed
}

func runDNSCheck(ctx context.Context, target string) (checkResult, error) {
	hosts, err := net.DefaultResolver.LookupIPAddr(ctx, target)
	if err != nil {
		return checkResult{Success: false, Message: err.Error()}, nil
	}

	values := make([]string, 0, len(hosts))
	for _, host := range hosts {
		values = append(values, host.IP.String())
	}

	return checkResult{Success: true, Message: strings.Join(values, ", ")}, nil
}

func respondJSON(w http.ResponseWriter, statusCode int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(body)
}

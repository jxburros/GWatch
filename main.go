package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
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
	urlText := target
	if !strings.HasPrefix(urlText, "http://") && !strings.HasPrefix(urlText, "https://") {
		urlText = "https://" + urlText
	}

	u, err := url.ParseRequestURI(urlText)
	if err != nil {
		return checkResult{}, fmt.Errorf("invalid URL target")
	}
	if err := validateHTTPHost(ctx, u.Hostname()); err != nil {
		return checkResult{}, err
	}

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: safeDialContext,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after too many redirects")
			}
			return validateHTTPHost(req.Context(), req.URL.Hostname())
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return checkResult{}, err
	}

	// lgtm [go/request-forgery]
	resp, err := client.Do(req)
	if err != nil {
		return checkResult{Success: false, Message: err.Error()}, nil
	}
	defer resp.Body.Close()

	success := resp.StatusCode < http.StatusBadRequest
	return checkResult{Success: success, Message: fmt.Sprintf("HTTP %d", resp.StatusCode)}, nil
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

func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if allowPrivateHTTPTargets() {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}

	if ip := net.ParseIP(host); ip != nil {
		if isDisallowedIP(ip) {
			return nil, fmt.Errorf("private or local IP targets are not allowed for HTTP checks")
		}
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}

	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}

	dialer := &net.Dialer{}
	for _, ipAddr := range addrs {
		if isDisallowedIP(ipAddr.IP) {
			continue
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ipAddr.IP.String(), port))
	}

	return nil, fmt.Errorf("private or local IP targets are not allowed for HTTP checks")
}

func isDisallowedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalMulticast() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast()
}

func allowPrivateHTTPTargets() bool {
	return strings.EqualFold(os.Getenv("GWATCH_ALLOW_PRIVATE_HTTP_TARGETS"), "true")
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

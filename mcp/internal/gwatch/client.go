// Package gwatch is a small client for GWatch's JSON API.
//
// It is deliberately standalone: the MCP companion never imports the GWatch
// module, it only speaks the documented HTTP API with an API key. That is what
// lets this binary ship and version on its own while the monitoring service
// stays untouched (ROADMAP 3.1).
package gwatch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// APIPrefix is the stable, versioned prefix documented in docs/API.md. The
// unprefixed /api/… alias follows the current version, so an integration that
// wants to keep working across releases asks for a version by name.
const APIPrefix = "/api/v1"

// Client talks to one GWatch instance with one API key.
type Client struct {
	BaseURL   string
	APIKey    string
	UserAgent string
	HTTP      *http.Client
}

// New builds a client for baseURL using key. timeout bounds each request.
func New(baseURL, key, userAgent string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		BaseURL:   strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		APIKey:    strings.TrimSpace(key),
		UserAgent: userAgent,
		HTTP:      &http.Client{Timeout: timeout},
	}
}

// Error is a non-2xx answer from GWatch. It keeps the status and the server's
// own {"error": …} message, which is what the person on the other end of the
// MCP session needs to see — a 403 here means the API key's scope is not
// enough, and no amount of retrying will change that.
type Error struct {
	Status  int
	Message string
	Method  string
	Path    string
}

func (e *Error) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	switch e.Status {
	case http.StatusUnauthorized:
		return fmt.Sprintf("GWatch rejected the API key (HTTP 401 on %s %s): %s", e.Method, e.Path, msg)
	case http.StatusForbidden:
		return fmt.Sprintf("GWatch refused this request (HTTP 403 on %s %s): %s", e.Method, e.Path, msg)
	default:
		return fmt.Sprintf("GWatch returned HTTP %d on %s %s: %s", e.Status, e.Method, e.Path, msg)
	}
}

// Forbidden reports whether the server refused the request on authorization
// grounds. Callers must surface this rather than retry it.
func (e *Error) Forbidden() bool { return e.Status == http.StatusForbidden }

// Do performs one request against path (relative to the versioned prefix) and
// decodes a JSON body into out, which may be nil.
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	u := c.BaseURL + APIPrefix + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding request body: %w", err)
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("X-API-Key", c.APIKey)
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach GWatch at %s: %w", c.BaseURL, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &Error{Status: resp.StatusCode, Message: serverMessage(raw), Method: method, Path: APIPrefix + path}
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decoding %s %s: %w", method, APIPrefix+path, err)
	}
	return nil
}

// serverMessage pulls the {"error": …} field out of an error body, falling
// back to the raw text when it is not JSON.
func serverMessage(raw []byte) string {
	var doc struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &doc) == nil && doc.Error != "" {
		return doc.Error
	}
	s := strings.TrimSpace(string(raw))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

// Get performs a GET and decodes the JSON answer into out.
func (c *Client) Get(ctx context.Context, path string, query url.Values, out any) error {
	return c.Do(ctx, http.MethodGet, path, query, nil, out)
}

// Post performs a POST with a JSON body.
func (c *Client) Post(ctx context.Context, path string, body, out any) error {
	return c.Do(ctx, http.MethodPost, path, nil, body, out)
}

// Put performs a PUT with a JSON body.
func (c *Client) Put(ctx context.Context, path string, body, out any) error {
	return c.Do(ctx, http.MethodPut, path, nil, body, out)
}

// Delete performs a DELETE.
func (c *Client) Delete(ctx context.Context, path string, out any) error {
	return c.Do(ctx, http.MethodDelete, path, nil, nil, out)
}

// Me is the subset of GET /api/v1/me this server cares about.
type Me struct {
	Kind     string `json:"kind"`
	Name     string `json:"name,omitempty"`
	Role     string `json:"role,omitempty"`
	Scope    string `json:"scope,omitempty"`
	IsAdmin  bool   `json:"isAdmin"`
	CanWrite bool   `json:"canWrite"`
	SignedIn bool   `json:"signedIn"`
}

// Describe renders the principal the way a setup check should print it.
func (m Me) Describe() string {
	switch {
	case m.Kind == "":
		return "anonymous (the key was not recognised)"
	case m.Kind == "apikey" && m.Name != "":
		return fmt.Sprintf("API key %q (scope %s)", m.Name, m.Scope)
	case m.Kind == "apikey":
		return fmt.Sprintf("API key (scope %s)", m.Scope)
	default:
		return fmt.Sprintf("%s %q", m.Kind, m.Name)
	}
}

// Me fetches the current principal.
func (c *Client) Me(ctx context.Context) (Me, error) {
	var me Me
	err := c.Get(ctx, "/me", nil, &me)
	return me, err
}

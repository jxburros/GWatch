// Package auth holds the identity primitives shared by the HTTP layer and the
// store: roles, the principal that every request is resolved to, password
// hashing, session/API-key token generation and a small rate limiter.
//
// Nothing in here talks to the database; internal/store owns the tables and
// internal/api owns the policy that decides what a principal may do.
package auth

import (
	"context"
	"fmt"
	"strings"
)

// Role is the two-tier permission level of a user account. GWatch deliberately
// stops at two roles (see ROADMAP "explicit non-goals"): full RBAC is out.
type Role string

const (
	// RoleAdmin may change anything.
	RoleAdmin Role = "admin"
	// RoleViewer may only read.
	RoleViewer Role = "viewer"
)

// ParseRole normalises a role string, defaulting to viewer for anything
// unrecognised (fail closed: an unknown role never gains admin).
func ParseRole(s string) Role {
	if strings.EqualFold(strings.TrimSpace(s), string(RoleAdmin)) {
		return RoleAdmin
	}
	return RoleViewer
}

// Valid reports whether r is one of the two known roles.
func (r Role) Valid() bool { return r == RoleAdmin || r == RoleViewer }

// Scope is the capability of an API key.
type Scope string

const (
	// ScopeRead allows read-only endpoints only.
	ScopeRead Scope = "read"
	// ScopeReadWrite additionally allows monitoring writes (nodes, checks,
	// dashboards, notes…) but never administration.
	ScopeReadWrite Scope = "readwrite"
)

// ParseScope normalises a scope string; anything unrecognised (including the
// empty string) becomes read-only.
func ParseScope(s string) Scope {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "readwrite", "read-write", "rw", "write":
		return ScopeReadWrite
	default:
		return ScopeRead
	}
}

// Kind identifies how a principal proved who it is.
type Kind string

const (
	// KindAnonymous is an unauthenticated caller.
	KindAnonymous Kind = ""
	// KindLocal is a client on this computer while no sign-in is required.
	KindLocal Kind = "local"
	// KindUser is a signed-in user account (session cookie).
	KindUser Kind = "user"
	// KindAPIKey is a caller presenting an API key.
	KindAPIKey Kind = "apikey"
	// KindPassword is the legacy LAN access password over HTTP basic auth.
	KindPassword Kind = "password"
)

// Principal is the identity of one request.
type Principal struct {
	Kind   Kind   `json:"kind"`
	Name   string `json:"name"`
	Role   Role   `json:"role"`
	Scope  Scope  `json:"scope,omitempty"`
	UserID int64  `json:"userId,omitempty"`
	KeyID  int64  `json:"keyId,omitempty"`
}

// Anonymous is the zero principal.
var Anonymous = Principal{}

// Authenticated reports whether the request proved any identity at all.
func (p Principal) Authenticated() bool { return p.Kind != KindAnonymous }

// IsAdmin reports whether the principal holds the admin role. API keys never
// hold it: a key with the readwrite scope can write monitoring data (see
// CanWrite) but is not an administrator.
func (p Principal) IsAdmin() bool {
	if p.Kind == KindAPIKey || p.Kind == KindAnonymous {
		return false
	}
	return p.Role == RoleAdmin
}

// CanWrite reports whether the principal may perform write actions that are
// allowed for its kind. For API keys that means the readwrite scope; for
// everyone else it means the admin role.
func (p Principal) CanWrite() bool {
	if p.Kind == KindAPIKey {
		return p.Scope == ScopeReadWrite
	}
	return p.IsAdmin()
}

// Label is a short human description used in the audit log, e.g.
// "pat (admin)" or "api key Home Assistant (read-write)".
func (p Principal) Label() string {
	switch p.Kind {
	case KindLocal:
		return "local"
	case KindPassword:
		return "password"
	case KindUser:
		return fmt.Sprintf("%s (%s)", p.Name, p.Role)
	case KindAPIKey:
		mode := "read-only"
		if p.Scope == ScopeReadWrite {
			mode = "read-write"
		}
		return fmt.Sprintf("api key %s (%s)", p.Name, mode)
	default:
		return ""
	}
}

// Describe is a longer phrase used inside 403 messages.
func (p Principal) Describe() string {
	switch p.Kind {
	case KindUser:
		return fmt.Sprintf("you are signed in as %s %q", p.Role, p.Name)
	case KindAPIKey:
		return fmt.Sprintf("you are using the API key %q", p.Name)
	case KindPassword:
		return "you are using the legacy access password"
	case KindLocal:
		return "you are on this computer"
	default:
		return "you are not signed in"
	}
}

type ctxKey struct{}

// WithPrincipal returns a context carrying p.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// FromContext returns the principal stored in ctx, or Anonymous.
func FromContext(ctx context.Context) Principal {
	if ctx == nil {
		return Anonymous
	}
	if p, ok := ctx.Value(ctxKey{}).(Principal); ok {
		return p
	}
	return Anonymous
}

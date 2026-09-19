package model

import (
	"encoding/json"
	"strings"
	"time"
)

// A wallboard is what a spare screen shows: a room-away read of the network,
// arranged by whoever put the screen up. It is close kin to a dashboard — a
// named set of configured panels — but it is not the same object and does not
// share its widget catalogue: a dashboard is read at a desk, a wallboard from
// the other side of a room, and the panels worth having differ accordingly.
//
// A wallboard can also be projected: switched on for sharing, it can be opened
// by a browser on the network with nothing but its address and a token, which
// is how a display with no keyboard ends up showing one.
type Wallboard struct {
	ID        int64         `json:"id"`
	Name      string        `json:"name"`
	SortOrder int           `json:"sortOrder"`
	Layout    WallLayout    `json:"layout"`
	Panels    []WallPanel   `json:"panels"`
	Share     WallboardLink `json:"share"`
	CreatedAt time.Time     `json:"createdAt"`
	UpdatedAt time.Time     `json:"updatedAt"`
}

// WallLayout is how the whole board is drawn rather than what is on it.
type WallLayout struct {
	// Columns the panel grid is divided into. A panel's Width is counted in
	// these, so the same board reads sensibly on a 16:9 television and on a
	// portrait display by changing this one number.
	Columns int `json:"columns"`
	// Theme picks the board's skin. See WallThemes.
	Theme string `json:"theme"`
	// Scale multiplies the board's type size, for a screen further away (or
	// closer) than the one it was laid out on. 1 is the size as designed.
	Scale float64 `json:"scale"`
	// RefreshSeconds is how often the board re-reads its data.
	RefreshSeconds int `json:"refreshSeconds"`
	// HideChrome drops the footer strip, for a screen with nothing to click.
	HideChrome bool `json:"hideChrome"`
}

// WallThemes are the skins a wallboard may wear. They are the board's own
// visual identity rather than the application's: the interface is a control
// room read at arm's length, a wallboard is a sign read across a room.
var WallThemes = []string{"signal", "contrast", "midnight", "daylight"}

// WallPanel is one tile on a wallboard. Config is interpreted by the renderer;
// see docs/API.md for what each type reads.
type WallPanel struct {
	ID     string          `json:"id"`
	Type   string          `json:"type"`
	Title  string          `json:"title"`
	Width  int             `json:"width"`  // in layout columns
	Height int             `json:"height"` // in grid rows (1..4)
	Config json.RawMessage `json:"config"`
}

// WallPanelTypes lists the panel types a wallboard understands, in the order
// the editor offers them.
var WallPanelTypes = []string{
	"headline",    // the one sentence that matters, with a lit circle
	"counts",      // up / degraded / down / unknown as large numerals
	"clock",       // time and date
	"attention",   // what is down or degraded right now
	"groups",      // one tile per group, worst status wins
	"nodes",       // a grid of nodes and their status
	"trends",      // latency / availability charts
	"certs",       // certificates expiring soon
	"maintenance", // maintenance windows in force
	"health",      // is GWatch itself running and checking
	"message",     // a fixed line of text for whoever walks past
}

// ValidWallPanelType reports whether a panel type is one of the above.
func ValidWallPanelType(t string) bool {
	for _, k := range WallPanelTypes {
		if k == t {
			return true
		}
	}
	return false
}

// WallboardLink is a wallboard's projection setting: whether a browser that
// has not signed in may open this one board, and the token that lets it.
//
// The token is kept as it is rather than hashed, unlike every other credential
// GWatch holds. That is deliberate and is the whole point of the feature: the
// address has to be readable again later, because it is typed into a display
// that has no keyboard of its own and is set up once and left alone. It is
// worth exactly one read-only wallboard, it is shown only to an administrator,
// and turning sharing off or rotating the token takes it back.
type WallboardLink struct {
	Enabled bool   `json:"enabled"`
	Token   string `json:"token,omitempty"`
	// Redacted is set when the token was withheld because the reader is not an
	// administrator, so the interface can say "shared" without showing how.
	Redacted bool `json:"redacted,omitempty"`
}

// Redact returns the link as a non-administrator may see it.
func (l WallboardLink) Redact() WallboardLink {
	if l.Token == "" {
		return l
	}
	return WallboardLink{Enabled: l.Enabled, Redacted: true}
}

// DefaultWallLayout is what a new wallboard starts as.
func DefaultWallLayout() WallLayout {
	return WallLayout{Columns: 12, Theme: "signal", Scale: 1, RefreshSeconds: 20}
}

// Normalize bounds a layout to what the renderer can actually draw, so a
// hand-edited or imported board cannot produce a screen nobody can read.
func (l *WallLayout) Normalize() {
	if l.Columns < 4 {
		l.Columns = DefaultWallLayout().Columns
	}
	if l.Columns > 24 {
		l.Columns = 24
	}
	if !containsString(WallThemes, l.Theme) {
		l.Theme = DefaultWallLayout().Theme
	}
	switch {
	case l.Scale < 0.6:
		l.Scale = 0.6
	case l.Scale > 2:
		l.Scale = 2
	}
	switch {
	case l.RefreshSeconds < 5:
		l.RefreshSeconds = DefaultWallLayout().RefreshSeconds
	case l.RefreshSeconds > 3600:
		l.RefreshSeconds = 3600
	}
}

// Normalize bounds one panel. A panel wider than the board is clamped to it.
func (p *WallPanel) Normalize(columns int) {
	if p.Width < 1 {
		p.Width = 1
	}
	if p.Width > columns {
		p.Width = columns
	}
	if p.Height < 1 {
		p.Height = 1
	}
	if p.Height > 4 {
		p.Height = 4
	}
	p.Title = strings.TrimSpace(p.Title)
	if len(p.Config) == 0 {
		p.Config = json.RawMessage("{}")
	}
}

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// DefaultWallboard is the board GWatch makes for itself the first time one is
// asked for: the arrangement the old fixed wallboard had, now editable.
func DefaultWallboard() Wallboard {
	cfg := func(v any) json.RawMessage {
		b, err := json.Marshal(v)
		if err != nil {
			return json.RawMessage("{}")
		}
		return b
	}
	return Wallboard{
		Name:   "Wallboard",
		Layout: DefaultWallLayout(),
		Panels: []WallPanel{
			{ID: "headline", Type: "headline", Width: 6, Height: 1, Config: cfg(map[string]any{})},
			{ID: "counts", Type: "counts", Width: 4, Height: 1, Config: cfg(map[string]any{})},
			{ID: "clock", Type: "clock", Width: 2, Height: 1, Config: cfg(map[string]any{"seconds": false})},
			{ID: "attention", Type: "attention", Title: "Needs attention", Width: 5, Height: 2, Config: cfg(map[string]any{"limit": 6})},
			{ID: "trends", Type: "trends", Title: "Trends", Width: 7, Height: 2, Config: cfg(map[string]any{"range": "24h", "limit": 4})},
			{ID: "groups", Type: "groups", Title: "Groups", Width: 5, Height: 1, Config: cfg(map[string]any{})},
			{ID: "certs", Type: "certs", Title: "Certificates", Width: 4, Height: 1, Config: cfg(map[string]any{})},
			{ID: "health", Type: "health", Title: "Service", Width: 3, Height: 1, Config: cfg(map[string]any{})},
		},
	}
}

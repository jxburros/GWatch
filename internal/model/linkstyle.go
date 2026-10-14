package model

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// LinkStyle is how the line from a node to the node it depends on is drawn on
// the network map. It belongs to the child — the node that names its parent —
// because that is where the link itself lives, and every map (the page, a
// dashboard widget, an exported picture) reads it from there. Each field left
// at its zero value defers to the map's own setting, which defers to the
// theme.
type LinkStyle struct {
	Color string  `json:"color,omitempty"` // #rgb, #rrggbb or #rrggbbaa; "" follows the map
	Width float64 `json:"width,omitempty"` // line thickness in drawing units; 0 follows the map
	Dash  string  `json:"dash,omitempty"`  // one of LinkDashes; "" follows the map
}

// The dash styles a link may be drawn in. They are an enumeration, not a
// pattern, so what reaches an SVG attribute is always one of a handful of
// strings the browser maps to a stroke-dasharray itself.
const (
	LinkDashSolid   = "solid"
	LinkDashDashed  = "dashed"
	LinkDashDotted  = "dotted"
	LinkDashDashDot = "dash-dot"
)

// LinkDashes lists the dash styles in display order.
var LinkDashes = []string{LinkDashSolid, LinkDashDashed, LinkDashDotted, LinkDashDashDot}

// The thickness range a link may take, in the map's drawing units. Anything
// outside it is pulled back in rather than refused: a line thicker than this
// swallows the nodes it joins and a thinner one vanishes at any zoom.
const (
	MinLinkWidth = 1.0
	MaxLinkWidth = 8.0
)

// ValidLinkDash reports whether s is one of the dash styles.
func ValidLinkDash(s string) bool {
	for _, d := range LinkDashes {
		if s == d {
			return true
		}
	}
	return false
}

// NormalizeLinkColor returns the colour in lower case and whether it is one a
// link may have: #rgb, #rrggbb or #rrggbbaa, and nothing else. A colour is
// written into an SVG attribute and a stylesheet, so it is matched against
// the exact shape rather than cleaned up — a name, a function or a url() is
// never let through.
func NormalizeLinkColor(s string) (string, bool) {
	if len(s) == 0 || s[0] != '#' {
		return "", false
	}
	switch len(s) - 1 {
	case 3, 6, 8:
	default:
		return "", false
	}
	out := []byte(strings.ToLower(s))
	for _, c := range out[1:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return "", false
		}
	}
	return string(out), true
}

// clampLinkWidth pulls a width into range and keeps one decimal. Zero (and
// anything that is not a number) means "not set".
func clampLinkWidth(w float64) float64 {
	if math.IsNaN(w) || math.IsInf(w, 0) || w <= 0 {
		return 0
	}
	w = math.Min(MaxLinkWidth, math.Max(MinLinkWidth, w))
	return math.Round(w*10) / 10
}

// Clean validates a link style for a write coming in from outside. A colour
// or a dash style that is not one of the allowed forms is an error, which the
// caller reports; a width out of range is clamped. A style that sets nothing
// comes back as nil, so "no style" has one spelling.
func (l *LinkStyle) Clean() (*LinkStyle, error) {
	if l == nil {
		return nil, nil
	}
	out := LinkStyle{Width: clampLinkWidth(l.Width)}
	if c := strings.TrimSpace(l.Color); c != "" {
		norm, ok := NormalizeLinkColor(c)
		if !ok {
			return nil, fmt.Errorf("link colour %q must be a hex colour such as #3b82f6", l.Color)
		}
		out.Color = norm
	}
	if d := strings.ToLower(strings.TrimSpace(l.Dash)); d != "" {
		if !ValidLinkDash(d) {
			return nil, fmt.Errorf("link style %q must be one of %s", l.Dash, strings.Join(LinkDashes, ", "))
		}
		out.Dash = d
	}
	if out == (LinkStyle{}) {
		return nil, nil
	}
	return &out, nil
}

// Sanitized is Clean for data that is already on disk or in a backup: it
// cannot refuse, so what is not valid is dropped and the rest kept. A hand-
// edited backup or database can then neither fail a restore nor smuggle a
// colour string into a drawing.
func (l *LinkStyle) Sanitized() *LinkStyle {
	if l == nil {
		return nil
	}
	out := LinkStyle{Width: clampLinkWidth(l.Width)}
	if norm, ok := NormalizeLinkColor(strings.TrimSpace(l.Color)); ok {
		out.Color = norm
	}
	if d := strings.ToLower(strings.TrimSpace(l.Dash)); ValidLinkDash(d) {
		out.Dash = d
	}
	if out == (LinkStyle{}) {
		return nil
	}
	return &out
}

// Equal reports whether two link styles draw the same line. It is meant for
// cleaned styles, where "sets nothing" has one spelling: nil.
func (l *LinkStyle) Equal(o *LinkStyle) bool {
	if l == nil || o == nil {
		return l == o
	}
	return *l == *o
}

// CleanLinkStyle validates the node's link style on a write. A style is the
// style of a link, so a node with nothing it depends on has none.
func (n *Node) CleanLinkStyle() error {
	if n.DependsOnNode == nil {
		n.LinkStyle = nil
		return nil
	}
	ls, err := n.LinkStyle.Clean()
	if err != nil {
		return err
	}
	n.LinkStyle = ls
	return nil
}

// CleanMapLineConfig checks the line settings a network map widget carries in
// its configuration — lineColor, lineWidth and lineStyle, the look of every
// line that has no style of its own — and returns the configuration with them
// in their canonical form. The rest of a widget's configuration is the web
// interface's business and is passed through untouched. It holds the map to
// the same rules as a link: a colour that is not #rgb, #rrggbb or #rrggbbaa
// or a style that is not one of LinkDashes is an error, a width is pulled
// into range.
func CleanMapLineConfig(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return raw, nil
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal(raw, &cfg); err != nil || cfg == nil {
		// Not an object: nothing in it is a line setting.
		return raw, nil
	}
	set := func(key string, v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		cfg[key] = b
		return nil
	}
	if v, ok := cfg["lineColor"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return nil, fmt.Errorf("lineColor must be a hex colour such as #3b82f6")
		}
		if s = strings.TrimSpace(s); s == "" {
			delete(cfg, "lineColor")
		} else {
			norm, ok := NormalizeLinkColor(s)
			if !ok {
				return nil, fmt.Errorf("lineColor %q must be a hex colour such as #3b82f6", s)
			}
			if err := set("lineColor", norm); err != nil {
				return nil, err
			}
		}
	}
	if v, ok := cfg["lineWidth"]; ok {
		var w float64
		if err := json.Unmarshal(v, &w); err != nil {
			return nil, fmt.Errorf("lineWidth must be a number from %g to %g", MinLinkWidth, MaxLinkWidth)
		}
		if w = clampLinkWidth(w); w == 0 {
			delete(cfg, "lineWidth")
		} else if err := set("lineWidth", w); err != nil {
			return nil, err
		}
	}
	if v, ok := cfg["lineStyle"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return nil, fmt.Errorf("lineStyle must be one of %s", strings.Join(LinkDashes, ", "))
		}
		if s = strings.ToLower(strings.TrimSpace(s)); s == "" {
			delete(cfg, "lineStyle")
		} else if !ValidLinkDash(s) {
			return nil, fmt.Errorf("lineStyle %q must be one of %s", s, strings.Join(LinkDashes, ", "))
		} else if err := set("lineStyle", s); err != nil {
			return nil, err
		}
	}
	return json.Marshal(cfg)
}

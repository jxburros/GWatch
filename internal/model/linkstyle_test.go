package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeLinkColorTakesHexAndNothingElse(t *testing.T) {
	good := map[string]string{
		"#fff":       "#fff",
		"#FFF":       "#fff",
		"#3B82F6":    "#3b82f6",
		"#3b82f680":  "#3b82f680",
		"#AbCdEf":    "#abcdef",
		"#00000000":  "#00000000",
		"#0a0":       "#0a0",
		"#1234567":   "", // seven digits is no colour
		"#12":        "",
		"#12345":     "",
		"#123456789": "",
	}
	for in, want := range good {
		got, ok := NormalizeLinkColor(in)
		if (want != "") != ok || got != want {
			t.Errorf("NormalizeLinkColor(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{
		"", "red", "rgb(0,0,0)", "#ggg", "3b82f6", "# fff", "#fff ", " #fff", "#fff\n",
		"url(#x)", "#fff;fill:red", "#fff\" onload=\"x", "var(--down)", "javascript:alert(1)", "#ｆｆｆ", "#fff;", "#fffffg",
	} {
		if got, ok := NormalizeLinkColor(bad); ok {
			t.Errorf("NormalizeLinkColor(%q) accepted as %q", bad, got)
		}
	}
}

func TestLinkStyleCleanRefusesJunkAndClampsWidth(t *testing.T) {
	got, err := (&LinkStyle{Color: " #3B82F6 ", Width: 3.14159, Dash: " Dashed "}).Clean()
	if err != nil || !got.Equal(&LinkStyle{Color: "#3b82f6", Width: 3.1, Dash: LinkDashDashed}) {
		t.Fatalf("clean = %+v, %v", got, err)
	}
	for in, want := range map[float64]float64{0.2: MinLinkWidth, 1: 1, 8: 8, 99: MaxLinkWidth, 1.25: 1.3} {
		if got, err := (&LinkStyle{Width: in}).Clean(); err != nil || got.Width != want {
			t.Errorf("width %v cleaned to %+v, %v; want %v", in, got, err, want)
		}
	}
	// "Not set" has one spelling: nil.
	for _, empty := range []*LinkStyle{nil, {}, {Width: -2}, {Color: "  ", Dash: " "}} {
		if got, err := empty.Clean(); err != nil || got != nil {
			t.Errorf("clean(%+v) = %+v, %v; want nil", empty, got, err)
		}
	}
	for _, bad := range []LinkStyle{
		{Color: "red"},
		{Color: "#12"},
		{Color: "#fff;stroke-width:99"},
		{Color: "url(#a)"},
		{Dash: "wavy"},
		{Dash: "5 4"},
		{Dash: "solid;fill:red"},
	} {
		if got, err := bad.Clean(); err == nil {
			t.Errorf("clean(%+v) accepted as %+v", bad, got)
		}
	}
	for _, d := range LinkDashes {
		if got, err := (&LinkStyle{Dash: d}).Clean(); err != nil || got.Dash != d {
			t.Errorf("dash %q cleaned to %+v, %v", d, got, err)
		}
	}
}

func TestLinkStyleSanitizedDropsWhatIsNotValid(t *testing.T) {
	got := (&LinkStyle{Color: "javascript:x", Width: 50, Dash: "DOTTED"}).Sanitized()
	if !got.Equal(&LinkStyle{Width: MaxLinkWidth, Dash: LinkDashDotted}) {
		t.Fatalf("sanitized = %+v", got)
	}
	if (&LinkStyle{Color: "nope", Dash: "nope"}).Sanitized() != nil {
		t.Fatal("a style with nothing valid in it is none")
	}
	if (*LinkStyle)(nil).Sanitized() != nil {
		t.Fatal("nil stays nil")
	}
}

func TestNodeCleanLinkStyleTiesTheStyleToTheLink(t *testing.T) {
	parent := int64(4)
	n := Node{DependsOnNode: &parent, LinkStyle: &LinkStyle{Color: "#ABC"}}
	if err := n.CleanLinkStyle(); err != nil || n.LinkStyle.Color != "#abc" {
		t.Fatalf("with a parent: %+v, %v", n.LinkStyle, err)
	}
	n.DependsOnNode = nil
	if err := n.CleanLinkStyle(); err != nil || n.LinkStyle != nil {
		t.Fatalf("with no parent the style goes too: %+v, %v", n.LinkStyle, err)
	}
	// Even a junk style is no error when there is no link to hold it.
	n.LinkStyle = &LinkStyle{Color: "bogus"}
	if err := n.CleanLinkStyle(); err != nil || n.LinkStyle != nil {
		t.Fatalf("junk with no parent: %+v, %v", n.LinkStyle, err)
	}
	n.DependsOnNode = &parent
	n.LinkStyle = &LinkStyle{Color: "bogus"}
	if err := n.CleanLinkStyle(); err == nil {
		t.Fatal("junk with a parent should be refused")
	}
}

func TestNodeLinkStyleWireFormat(t *testing.T) {
	var n Node
	if err := json.Unmarshal([]byte(`{"id":2,"name":"NAS","dependsOnNodeId":1,"linkStyle":{"color":"#3b82f6","width":2.5,"dash":"dotted"}}`), &n); err != nil {
		t.Fatal(err)
	}
	if !n.LinkStyle.Equal(&LinkStyle{Color: "#3b82f6", Width: 2.5, Dash: "dotted"}) {
		t.Fatalf("decoded %+v", n.LinkStyle)
	}
	out, _ := json.Marshal(Node{ID: 3, Name: "x"})
	if !strings.Contains(string(out), `"linkStyle":null`) {
		t.Fatalf("a node with no style should say null, got %s", out)
	}
	out, _ = json.Marshal(LinkStyle{Dash: "dashed"})
	if string(out) != `{"dash":"dashed"}` {
		t.Fatalf("unset fields should be left out, got %s", out)
	}
}

func TestCleanMapLineConfig(t *testing.T) {
	got, err := CleanMapLineConfig(json.RawMessage(`{"orientation":"radial","lineColor":" #E5A50A ","lineWidth":12,"lineStyle":"Dotted","labels":"name"}`))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(got, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["lineColor"] != "#e5a50a" || cfg["lineWidth"] != float64(8) || cfg["lineStyle"] != "dotted" || cfg["orientation"] != "radial" || cfg["labels"] != "name" {
		t.Fatalf("cleaned config = %s", got)
	}
	// Unset settings are dropped rather than stored as empties.
	got, err = CleanMapLineConfig(json.RawMessage(`{"lineColor":"","lineWidth":0,"lineStyle":"","group":"Lab"}`))
	if err != nil || string(got) != `{"group":"Lab"}` {
		t.Fatalf("empties: %s, %v", got, err)
	}
	// Nothing to check is no error, and is left exactly as it came.
	for _, raw := range []string{``, `{}`, `[]`, `null`, `"text"`} {
		out, err := CleanMapLineConfig(json.RawMessage(raw))
		if err != nil || string(out) != raw && raw != `{}` {
			t.Errorf("CleanMapLineConfig(%q) = %q, %v", raw, out, err)
		}
	}
	for _, bad := range []string{
		`{"lineColor":"red"}`,
		`{"lineColor":"url(#x)"}`,
		`{"lineColor":3}`,
		`{"lineWidth":"thick"}`,
		`{"lineStyle":"wavy"}`,
		`{"lineStyle":5}`,
	} {
		if out, err := CleanMapLineConfig(json.RawMessage(bad)); err == nil {
			t.Errorf("CleanMapLineConfig(%s) accepted as %s", bad, out)
		}
	}
}

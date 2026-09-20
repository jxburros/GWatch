package discovery

import (
	"strings"
	"testing"

	"github.com/jxburros/GWatch/internal/checks"
)

func TestSuggestFromOpenPorts(t *testing.T) {
	cases := []struct {
		name  string
		ports []int
		want  string
		says  string // a word the note has to carry, so it still explains itself
	}{
		{"nothing answered but the ping", nil, "ping", ""},
		{"SSH and a web page: a server", []int{22, 80}, "home-server", "server"},
		{"SSH and HTTPS: the same", []int{22, 443}, "home-server", "server"},
		{"SSH on its own", []int{22}, "home-server", "SSH"},
		{"only a web interface: a router", []int{80, 443}, "router", "router"},
		{"only an alternate web port", []int{8080}, "router", "router"},
		{"both alternate web ports", []int{8443, 8080}, "router", "router"},
		{"remote desktop: a Windows machine", []int{3389}, "ping", "Windows"},
		{"remote desktop beside a web page is still Windows", []int{80, 3389}, "ping", "Windows"},
		{"Plex", []int{32400}, "tcp-service", "Plex"},
		{"a printer", []int{9100}, "tcp-service", "printer"},
		{"a printer with an admin page is still a printer", []int{80, 9100}, "tcp-service", "printer"},
		{"an MQTT broker", []int{1883}, "tcp-service", "MQTT"},
		{"file sharing on its own", []int{445}, "ping", "File sharing"},
		{"a media server you can log in to is a server", []int{22, 80, 32400}, "home-server", "server"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Suggest(c.ports)
			if got.Template != c.want {
				t.Errorf("Suggest(%v) = %q, want %q", c.ports, got.Template, c.want)
			}
			if c.says == "" {
				if got.Note != "" {
					t.Errorf("Suggest(%v) explained itself as %q, but there is nothing to explain", c.ports, got.Note)
				}
				return
			}
			if !strings.Contains(got.Note, c.says) {
				t.Errorf("Suggest(%v) note %q does not mention %q", c.ports, got.Note, c.says)
			}
		})
	}
}

// Every template the table can name has to exist, or "add selected" would fail
// on a suggestion the interface made itself.
func TestSuggestedTemplatesExist(t *testing.T) {
	known := map[string]bool{}
	for _, tmpl := range checks.Templates() {
		known[tmpl.ID] = true
	}
	seen := map[string]bool{}
	for _, rule := range suggestRules {
		seen[rule.give.Template] = true
	}
	seen[Suggest(nil).Template] = true // the fallback
	for id := range seen {
		if !known[id] {
			t.Errorf("the table suggests a %q template, which checks.Templates() does not offer", id)
		}
	}
}

func TestNormalizePorts(t *testing.T) {
	if got, err := normalizePorts(nil); err != nil || len(got) != len(DefaultPorts) {
		t.Fatalf("no list at all should mean the defaults: %v %v", got, err)
	}
	// A list that is there but empty is a decision, not an omission.
	if got, err := normalizePorts([]int{}); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("an empty list should mean no ports at all: %v %v", got, err)
	}
	got, err := normalizePorts([]int{443, 22, 443, 80})
	if err != nil {
		t.Fatalf("normalizePorts: %v", err)
	}
	if want := []int{22, 80, 443}; len(got) != len(want) || got[0] != want[0] || got[2] != want[2] {
		t.Fatalf("duplicates should go and the rest should sort: got %v, want %v", got, want)
	}
	for _, bad := range [][]int{{0}, {-1}, {70000}} {
		if _, err := normalizePorts(bad); err == nil {
			t.Errorf("port %v was accepted", bad)
		}
	}
	long := make([]int, 0, MaxPorts+1)
	for p := 1; p <= MaxPorts+1; p++ {
		long = append(long, p)
	}
	if _, err := normalizePorts(long); err == nil {
		t.Errorf("a list of %d ports was accepted", len(long))
	}
}

// 161 is in the comment above DefaultPorts as the port discovery deliberately
// does not probe, because a silent UDP port and a filtered one look the same.
func TestDefaultPortsLeaveSNMPAlone(t *testing.T) {
	for _, p := range DefaultPorts {
		if p == 161 {
			t.Fatal("161 is UDP; a TCP probe of it would report nothing useful")
		}
	}
}

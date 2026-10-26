package model

import (
	"reflect"
	"strings"
	"testing"
)

func TestCollectSelectionNormalizeDropsBlanksAndDuplicates(t *testing.T) {
	got := CollectSelection{Interfaces: []string{" eth1 ", "eth0", "eth1", ""}, Mounts: []string{"  "}}.Normalize()
	if !reflect.DeepEqual(got.Interfaces, []string{"eth0", "eth1"}) {
		t.Errorf("interfaces: %v", got.Interfaces)
	}
	if blank := (CollectSelection{Mounts: []string{" "}}).Normalize(); got.Mounts != nil || !blank.IsZero() {
		t.Errorf("a list of blanks should be no list at all, got %#v", got.Mounts)
	}
}

func TestCollectSelectionValidateRefusesUnknownFamiliesAndExcess(t *testing.T) {
	if err := (CollectSelection{Metrics: []string{"cpu", "net"}}).Validate(); err != nil {
		t.Errorf("known families refused: %v", err)
	}
	if err := (CollectSelection{Metrics: []string{"cpu", "gpu"}}).Validate(); err == nil || !strings.Contains(err.Error(), "gpu") {
		t.Errorf("an unknown family should be named in the refusal, got %v", err)
	}
	many := make([]string, MaxCollectNames+1)
	for i := range many {
		many[i] = strings.Repeat("a", 3) + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	if err := (CollectSelection{Interfaces: many}).Validate(); err == nil {
		t.Error("an absurd number of interfaces should be refused")
	}
	if err := (CollectSelection{Disks: []string{strings.Repeat("x", MaxCollectNameLen+1)}}).Validate(); err == nil {
		t.Error("an absurdly long name should be refused")
	}
}

// The machine's own configuration is a ceiling: GWatch can choose within it
// and can never widen it.
func TestEffectiveSelectionNeverWidensTheCeiling(t *testing.T) {
	cases := []struct {
		name            string
		ceiling, chosen []string
		want            []string
	}{
		{"both automatic", nil, nil, nil},
		{"only GWatch chose", nil, []string{"eth0"}, []string{"eth0"}},
		{"only the machine chose", []string{"eth0"}, nil, []string{"eth0"}},
		{"both chose, overlap", []string{"eth0", "eth1"}, []string{"eth1", "eth2"}, []string{"eth1"}},
		{"both chose, nothing shared", []string{"eth0"}, []string{"eth1"}, []string{""}},
	}
	for _, c := range cases {
		got := CollectSelection{Interfaces: c.ceiling}.Effective(CollectSelection{Interfaces: c.chosen}).Interfaces
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %#v, want %#v", c.name, got, c.want)
		}
	}
}

func TestInventoryNormalizeBoundsWhatTheNetworkSent(t *testing.T) {
	long := strings.Repeat("n", MaxCollectNameLen*2)
	in := Inventory{Interfaces: []InventoryItem{{Name: "  "}, {Name: long, Detail: long}, {Name: "eth0"}}}
	got := in.Normalize()
	if len(got.Interfaces) != 2 || len(got.Interfaces[0].Name) != MaxCollectNameLen || len(got.Interfaces[0].Detail) != MaxCollectNameLen {
		t.Errorf("unexpected %+v", got.Interfaces)
	}
}

// Containers and guests are watch lists like services: empty is none, the
// ceiling rule applies, and a name that could read as an option is refused.
func TestContainerAndVMWatchLists(t *testing.T) {
	if (CollectSelection{Containers: []string{" ", ""}}).Watching() {
		t.Error("blank container names watch nothing")
	}
	sel := CollectSelection{Containers: []string{"web", " web", "db"}, VMs: []string{"dc01"}, Metrics: []string{MetricCPU}}.Normalize()
	if !sel.Watching() || len(sel.Containers) != 2 || sel.IsZero() {
		t.Errorf("normalized: %+v", sel)
	}
	if w := sel.WatchOnly(); w.Metrics != nil || len(w.Containers) != 2 || len(w.VMs) != 1 {
		t.Errorf("watch only: %+v", w)
	}
	ceiling := CollectSelection{Containers: []string{"web"}}
	eff := ceiling.Effective(CollectSelection{Containers: []string{"web", "db"}, VMs: []string{"dc01"}})
	if len(eff.WatchedContainers()) != 1 || eff.WatchedContainers()[0] != "web" || len(eff.WatchedVMs()) != 1 {
		t.Errorf("effective: %+v", eff)
	}
	if eff := ceiling.Effective(CollectSelection{Containers: []string{"db"}}); len(eff.WatchedContainers()) != 0 || eff.Watching() {
		t.Errorf("nothing in common is nothing: %+v", eff)
	}
	for _, bad := range []CollectSelection{{Containers: []string{"-f"}}, {VMs: []string{"dc\x0001"}}} {
		if bad.Validate() == nil {
			t.Errorf("should be refused: %+v", bad)
		}
	}
	inv := Inventory{Containers: []InventoryItem{{Name: " web "}, {Name: ""}}, VMs: []InventoryItem{{Name: "dc01"}}}.Normalize()
	if len(inv.Containers) != 1 || inv.Containers[0].Name != "web" || len(inv.VMs) != 1 {
		t.Errorf("inventory: %+v", inv)
	}
}

func TestDockerAndVMValues(t *testing.T) {
	for _, c := range []struct {
		c    HostDockerContainer
		want float64
	}{
		{HostDockerContainer{State: "running"}, 2},
		{HostDockerContainer{State: "running", Health: "healthy"}, 2},
		{HostDockerContainer{State: "running", Health: "starting"}, 2},
		{HostDockerContainer{State: "running", Health: "unhealthy"}, 1},
		{HostDockerContainer{State: "paused"}, 1},
		{HostDockerContainer{State: "restarting"}, 1},
		{HostDockerContainer{State: "exited"}, 0},
		{HostDockerContainer{State: "created"}, 0},
		{HostDockerContainer{State: "dead"}, 0},
		{HostDockerContainer{State: "not found"}, 0},
	} {
		if got := DockerValue(c.c); got != c.want {
			t.Errorf("%+v: %v, want %v", c.c, got, c.want)
		}
	}
	for state, want := range map[string]float64{"running": 2, "paused": 1, "saved": 1, "starting": 1, "running (critical)": 1,
		"off": 0, "off (critical)": 0, "not found": 0} {
		if got := VMValue(HostVM{State: state}); got != want {
			t.Errorf("%s: %v, want %v", state, got, want)
		}
	}
	for key, unit := range map[string]string{"docker:web": "container", "docker-restarts:web": "count", "docker-cpu:web": "%",
		"docker-memory:web": "%", "vm:dc01": "guest", "vm-cpu:dc01": "%", "vm-memory:dc01": "%"} {
		if got := SystemMetricUnit(key); got != unit {
			t.Errorf("%s: unit %q, want %q", key, got, unit)
		}
	}
	if err := ValidateMetricThresholds(SystemDefaults().MetricThresholds); err != nil {
		t.Errorf("the defaults must be valid: %v", err)
	}
}

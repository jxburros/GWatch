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

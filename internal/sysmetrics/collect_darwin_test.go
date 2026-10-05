//go:build darwin

package sysmetrics

import "testing"

func TestDarwinVMStat(t *testing.T) {
	fixture := []byte("Mach Virtual Memory Statistics: (page size of 16384 bytes)\nPages active: 10.\nPages wired down: 2.\nPages occupied by compressor: 3.\nPages inactive: 4.\n")
	mem := parseDarwinMemory(1024*1024, fixture)
	if mem.UsedBytes != 15*16384 || mem.AvailableBytes != 1024*1024-15*16384 || mem.CachedBytes != 4*16384 {
		t.Fatalf("%+v", mem)
	}
	if mem := parseDarwinMemory(100, fixture); mem.UsedBytes != 100 || mem.AvailableBytes != 0 {
		t.Fatal(mem)
	}
	if mem := parseDarwinMemory(100, []byte("page size of ")); mem.UsedBytes != 0 {
		t.Fatal(mem)
	}
}
func TestDarwinNetstat(t *testing.T) {
	fixture := []byte("Name Mtu Network Address Ipkts Ierrs Ibytes Opkts Oerrs Obytes Coll\nen0 1500 link#1 aa 10 2 1000 20 3 2000 0\nen0 1500 inet 192.0.2.1 10 2 1000 20 3 2000 0\nlo0 16384 link#2 10 0 100 10 0 100 0\nen1* 1500 link#3 0 0 0 0 0 0 0\nmalformed row\n")
	got, err := parseDarwinInterfaces(fixture, map[string]bool{"en0": true}, map[string]bool{"lo0": true}, nil)
	if err != nil || len(got) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	if got[0].name != "en0" || got[0].rxBytes != 1000 || got[0].txBytes != 2000 || got[0].rxErrors != 2 || got[0].txErrors != 3 || got[1].up {
		t.Fatalf("%+v", got)
	}
	if got, err := parseDarwinInterfaces([]byte("invalid header"), nil, nil, nil); err != nil || len(got) != 0 {
		t.Fatalf("%+v %v", got, err)
	}
}

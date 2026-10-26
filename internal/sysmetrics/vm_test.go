package sysmetrics

import (
	"testing"

	"github.com/jxburros/GWatch/internal/model"
)

// Rows as wmiQuery returns them from a Windows Server 2022 Hyper-V host: the
// host's own Msvm_ComputerSystem first (its Name is the computer name), then
// one per guest, keyed by VM id. EnabledState arrives as VT_I4, and every
// uint64 — OnTimeInMilliseconds, MemoryUsage — as a string, which is how WMI
// hands over 64-bit numbers. NULL is nil.
var hyperVSystemsFixture = []wmiRow{
	{"Name": "HV01", "ElementName": "HV01", "EnabledState": int64(2), "OnTimeInMilliseconds": "954000123"},
	{"Name": "6D1F3A1E-2B4C-4F7A-9A8B-1C2D3E4F5A6B", "ElementName": "dc01", "EnabledState": int64(2), "OnTimeInMilliseconds": "86400500"},
	{"Name": "0B5E2C77-9D11-4E2A-8C3B-7F6E5D4C3B2A", "ElementName": "lab", "EnabledState": int64(3), "OnTimeInMilliseconds": "0"},
	{"Name": "A1B2C3D4-E5F6-4711-8899-AABBCCDDEEFF", "ElementName": "build", "EnabledState": int64(32768), "OnTimeInMilliseconds": "3600000"},
	{"Name": "F0E1D2C3-B4A5-4697-8877-665544332211", "ElementName": "media", "EnabledState": int64(6), "OnTimeInMilliseconds": nil},
	{"Name": "11111111-2222-4333-8444-555555555555", "ElementName": "booting", "EnabledState": int64(32770), "OnTimeInMilliseconds": "4000"},
}

var hyperVSummaryFixture = []wmiRow{
	{"Name": "6d1f3a1e-2b4c-4f7a-9a8b-1c2d3e4f5a6b", "ProcessorLoad": int64(7), "MemoryUsage": "4096", "MemoryAvailable": int64(38), "NumberOfProcessors": int64(2)},
	{"Name": "0b5e2c77-9d11-4e2a-8c3b-7f6e5d4c3b2a", "ProcessorLoad": nil, "MemoryUsage": "0", "MemoryAvailable": nil, "NumberOfProcessors": int64(4)},
	{"Name": "a1b2c3d4-e5f6-4711-8899-aabbccddeeff", "ProcessorLoad": int64(0), "MemoryUsage": "2048", "MemoryAvailable": nil, "NumberOfProcessors": int64(1)},
}

func TestHyperVGuestsFromRecordedWMI(t *testing.T) {
	got := hyperVGuests(hyperVSystemsFixture, hyperVSummaryFixture)
	if len(got) != 5 {
		t.Fatalf("five guests, and not the host: %+v", got)
	}
	dc := got[0]
	if dc.Name != "dc01" || dc.State != "running" || dc.Hypervisor != "hyperv" || dc.ID != "6d1f3a1e-2b4c-4f7a-9a8b-1c2d3e4f5a6b" || dc.UptimeSecs != 86401 {
		t.Errorf("dc01: %+v", dc)
	}
	if dc.Processors != 2 || dc.CPUPct == nil || *dc.CPUPct != 7 || dc.MemoryAssignedBytes != 4096<<20 || dc.MemoryPct == nil || *dc.MemoryPct != 62 {
		t.Errorf("dc01 figures: %+v", dc)
	}
	if model.VMValue(dc) != 2 {
		t.Error("running is 2")
	}
	lab := got[1]
	if lab.State != "off" || lab.UptimeSecs != 0 || lab.CPUPct != nil || lab.MemoryPct != nil || lab.Processors != 0 || model.VMValue(lab) != 0 {
		t.Errorf("a guest that is off has no figures: %+v", lab)
	}
	build := got[2]
	if build.State != "paused" || model.VMValue(build) != 1 || build.MemoryPct != nil {
		t.Errorf("paused, without integration services' memory figure: %+v", build)
	}
	if got[3].State != "saved" || model.VMValue(got[3]) != 1 || got[3].UptimeSecs != 0 {
		t.Errorf("saved: %+v", got[3])
	}
	if got[4].State != "starting" || model.VMValue(got[4]) != 1 || got[4].CPUPct != nil {
		t.Errorf("starting, with no summary row: %+v", got[4])
	}

	// Without the second query the guests are still states.
	bare := hyperVGuests(hyperVSystemsFixture, nil)
	if len(bare) != 5 || bare[0].State != "running" || bare[0].CPUPct != nil {
		t.Errorf("no summaries: %+v", bare)
	}
}

func TestHyperVState(t *testing.T) {
	for in, want := range map[int64]string{2: "running", 3: "off", 4: "stopping", 6: "saved", 9: "paused", 10: "starting",
		32768: "paused", 32769: "saved", 32770: "starting", 32771: "running", 32773: "saving", 32774: "stopping",
		32776: "pausing", 32777: "resuming", 0: "unknown", 12: "state 12"} {
		if got := hyperVState(in); got != want {
			t.Errorf("%d: %q, want %q", in, got, want)
		}
	}
}

// Get-VM's output, from the fallback, reads the same as WMI's.
func TestParseGetVM(t *testing.T) {
	const out = "\ufeff" + `[{"Name":"dc01","Id":"6D1F3A1E-2B4C-4F7A-9A8B-1C2D3E4F5A6B","State":"Running","UptimeSeconds":86400,"CPUUsage":7,"MemoryAssigned":4294967296,"MemoryDemand":2147483648,"ProcessorCount":2},` +
		`{"Name":"lab","Id":"0b5e2c77-9d11-4e2a-8c3b-7f6e5d4c3b2a","State":"Off","UptimeSeconds":0,"CPUUsage":0,"MemoryAssigned":0,"MemoryDemand":0,"ProcessorCount":4},` +
		`{"Name":"media","Id":"f0e1d2c3-b4a5-4697-8877-665544332211","State":"Saved","UptimeSeconds":0,"CPUUsage":0,"MemoryAssigned":0,"MemoryDemand":0,"ProcessorCount":1},` +
		`{"Name":"nas","Id":"x","State":"RunningCritical","UptimeSeconds":50,"CPUUsage":3,"MemoryAssigned":1073741824,"MemoryDemand":0,"ProcessorCount":1},` +
		`{"Name":"old","Id":"y","State":"OffCritical","UptimeSeconds":0,"CPUUsage":0,"MemoryAssigned":0,"MemoryDemand":0,"ProcessorCount":1}]` + "\r\n"
	got, err := parseGetVM(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("got %+v", got)
	}
	dc := got[0]
	if dc.State != "running" || dc.ID != "6d1f3a1e-2b4c-4f7a-9a8b-1c2d3e4f5a6b" || dc.CPUPct == nil || *dc.CPUPct != 7 || dc.MemoryPct == nil || *dc.MemoryPct != 50 || dc.UptimeSecs != 86400 {
		t.Errorf("dc01: %+v", dc)
	}
	if got[1].State != "off" || got[1].CPUPct != nil || got[1].Processors != 0 || model.VMValue(got[1]) != 0 {
		t.Errorf("off: %+v", got[1])
	}
	if got[2].State != "saved" || model.VMValue(got[2]) != 1 {
		t.Errorf("saved: %+v", got[2])
	}
	if got[3].State != "running (critical)" || model.VMValue(got[3]) != 1 || got[3].MemoryPct != nil || got[3].CPUPct == nil {
		t.Errorf("running with its storage gone: %+v", got[3])
	}
	if got[4].State != "off (critical)" || model.VMValue(got[4]) != 0 {
		t.Errorf("off with its storage gone: %+v", got[4])
	}

	// An older PowerShell unrolls a one-element array into the object.
	one, err := parseGetVM(`{"Name":"solo","Id":"z","State":"Paused","ProcessorCount":1}`)
	if err != nil || len(one) != 1 || one[0].State != "paused" {
		t.Errorf("one guest: %+v %v", one, err)
	}
	if none, err := parseGetVM("[]"); err != nil || len(none) != 0 {
		t.Errorf("no guests: %+v %v", none, err)
	}
	if _, err := parseGetVM("Get-VM : not recognized"); err == nil {
		t.Error("output that is not JSON should be an error")
	}
}

// Watched names are answered from the hypervisor's list without regard to
// case, as Hyper-V compares them, and the list offered for choosing has each
// name once.
func TestPickVMsAndInventory(t *testing.T) {
	all := hyperVGuests(hyperVSystemsFixture, hyperVSummaryFixture)
	got := pickVMs(all, []string{"DC01", "nosuch"})
	if len(got) != 2 || got[0].Name != "DC01" || got[0].State != "running" || got[1].State != "not found" || model.VMValue(got[1]) != 0 {
		t.Errorf("got %+v", got)
	}
	inv := vmInventory(append(all, model.HostVM{Name: "DC01", State: "off"}))
	if len(inv) != 5 || inv[0].Name != "booting" || inv[2].Name != "dc01" || inv[2].Detail != "running · 2 virtual processors" || inv[1].Detail != "paused · 1 virtual processor" {
		t.Errorf("inventory: %+v", inv)
	}
}

func TestIsGUID(t *testing.T) {
	for s, want := range map[string]bool{
		"6D1F3A1E-2B4C-4F7A-9A8B-1C2D3E4F5A6B": true, "{6d1f3a1e-2b4c-4f7a-9a8b-1c2d3e4f5a6b}": true,
		"HV01": false, "6D1F3A1E-2B4C-4F7A-9A8B-1C2D3E4F5A6": false, "6D1F3A1E+2B4C-4F7A-9A8B-1C2D3E4F5A6B": false,
	} {
		if isGUID(s) != want {
			t.Errorf("%q: want %v", s, want)
		}
	}
}

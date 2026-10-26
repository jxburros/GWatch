package sysmetrics

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/jxburros/GWatch/internal/model"
)

// Watching virtual machines.
//
// A guest is watched like a container: by name, only once someone has named
// it (model.CollectSelection.VMs), on the machine that hosts it. The only
// hypervisor read so far is Hyper-V, on Windows, through WMI's
// root\virtualization\v2 namespace (vm_windows.go), with PowerShell's Get-VM
// as a fallback should WMI answer with something other than "not installed"
// or "not allowed". libvirt and Proxmox are the next candidates; a watched
// guest elsewhere says, in the reading's warnings, that it cannot be read
// there yet.
//
// What is turned into readings — the WMI rows, Get-VM's output — is parsed
// here, as plain functions, so its tests run on every platform.

// errVMUnsupported is the answer wherever no hypervisor is read.
var errVMUnsupported = fmt.Errorf("virtual machines are read from Hyper-V, on Windows; this machine's hypervisor cannot be read yet")

// readVMs reads the named guests. Every watched name is in the answer, "not
// found" when the hypervisor has no guest by it.
func readVMs(ctx context.Context, names []string) ([]model.HostVM, []string, error) {
	all, warnings, err := listGuests(ctx)
	if err != nil {
		return nil, warnings, err
	}
	return pickVMs(all, names), warnings, nil
}

// pickVMs answers for each watched name from the hypervisor's list. Hyper-V
// compares names without regard to case, and so does this; it also lets two
// guests share a name, in which case the first listed is the one reported.
func pickVMs(all []model.HostVM, names []string) []model.HostVM {
	out := make([]model.HostVM, 0, len(names))
	for _, n := range names {
		found := model.HostVM{Name: n, State: "not found"}
		for _, v := range all {
			if strings.EqualFold(v.Name, n) {
				found = v
				found.Name = n
				break
			}
		}
		out = append(out, found)
	}
	return out
}

// ListVMs lists every guest on this machine's hypervisor, for a person
// choosing which to watch. Detail is its state.
func ListVMs(ctx context.Context) ([]model.InventoryItem, error) {
	all, _, err := listGuests(ctx)
	if err != nil {
		return nil, err
	}
	return vmInventory(all), nil
}

func vmInventory(all []model.HostVM) []model.InventoryItem {
	out := make([]model.InventoryItem, 0, len(all))
	seen := map[string]bool{}
	for _, v := range all {
		if v.Name == "" || seen[strings.ToLower(v.Name)] {
			continue
		}
		seen[strings.ToLower(v.Name)] = true
		detail := v.State
		if v.Processors > 0 {
			detail += fmt.Sprintf(" · %d virtual processor%s", v.Processors, map[bool]string{true: "", false: "s"}[v.Processors == 1])
		}
		out = append(out, model.InventoryItem{Name: v.Name, Detail: detail})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

/* ---------------- Hyper-V through WMI ---------------- */

// wmiRow is one WMI object, the properties asked for and nothing else. The
// values are what a VARIANT holds once read: a string (which is how WMI hands
// over every 64-bit number and every date), an int64 for the smaller
// integers, a bool, or nil for a property that is NULL.
type wmiRow map[string]any

func (r wmiRow) str(k string) string {
	switch v := r[k].(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

// num reads a number however WMI sent it. ok is false for NULL, which is how
// Hyper-V says it does not know (a guest that is off has no processor load).
func (r wmiRow) num(k string) (float64, bool) {
	switch v := r[k].(type) {
	case int64:
		return float64(v), true
	case float64:
		return v, true
	case bool:
		if v {
			return 1, true
		}
		return 0, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f, err == nil
	}
	return 0, false
}

// The queries, kept with the parser that reads their answers. Every
// Msvm_ComputerSystem is either the host itself or a guest; the host's Name is
// its computer name and a guest's is its VM id, a GUID, which is how they are
// told apart (Caption says "Virtual Machine" only in English).
const (
	hyperVNamespace    = `root\virtualization\v2`
	hyperVSystemsQuery = "SELECT Name, ElementName, EnabledState, OnTimeInMilliseconds FROM Msvm_ComputerSystem"
	hyperVSummaryQuery = "SELECT Name, ProcessorLoad, MemoryUsage, MemoryAvailable, NumberOfProcessors FROM Msvm_SummaryInformation"
	hyperVGetVMCommand = `$ErrorActionPreference = 'Stop'; ConvertTo-Json -Compress -InputObject @(Get-VM | Select-Object Name, @{n='Id';e={[string]$_.Id}}, @{n='State';e={[string]$_.State}}, @{n='UptimeSeconds';e={[int64]$_.Uptime.TotalSeconds}}, CPUUsage, MemoryAssigned, MemoryDemand, ProcessorCount)`
)

// The properties each query reads from every row it returns.
var (
	hyperVSystemsProps = []string{"Name", "ElementName", "EnabledState", "OnTimeInMilliseconds"}
	hyperVSummaryProps = []string{"Name", "ProcessorLoad", "MemoryUsage", "MemoryAvailable", "NumberOfProcessors"}
)

// hyperVGuests turns the two queries' rows into guests. summaries may be
// empty — the figures are a bonus, and a guest with none is still a state.
func hyperVGuests(systems, summaries []wmiRow) []model.HostVM {
	byID := map[string]wmiRow{}
	for _, s := range summaries {
		byID[strings.ToLower(s.str("Name"))] = s
	}
	var out []model.HostVM
	for _, r := range systems {
		id := r.str("Name")
		if !isGUID(id) {
			continue // the host itself
		}
		v := model.HostVM{Name: r.str("ElementName"), Hypervisor: "hyperv", ID: strings.ToLower(id)}
		state, _ := r.num("EnabledState")
		v.State = hyperVState(int64(state))
		if ms, ok := r.num("OnTimeInMilliseconds"); ok && ms > 0 && v.State != "off" {
			v.UptimeSecs = math.Round(ms / 1000)
		}
		if s, ok := byID[v.ID]; ok && v.State != "off" {
			if n, ok := s.num("NumberOfProcessors"); ok {
				v.Processors = int(n)
			}
			if load, ok := s.num("ProcessorLoad"); ok {
				pct := clampPct(load)
				v.CPUPct = &pct
			}
			if mb, ok := s.num("MemoryUsage"); ok && mb > 0 {
				v.MemoryAssignedBytes = uint64(mb) << 20
			}
			// MemoryAvailable is the share of its memory the guest has free,
			// as its integration services report it. Without them there is
			// no figure, rather than a guest that looks completely full.
			if avail, ok := s.num("MemoryAvailable"); ok && avail >= 0 && avail <= 100 && v.MemoryAssignedBytes > 0 {
				pct := clampPct(100 - avail)
				v.MemoryPct = &pct
			}
		}
		out = append(out, v)
	}
	return out
}

// hyperVState words a guest's EnabledState: the CIM values (2 enabled, 3
// disabled, …) and the Hyper-V ones above 32767. Taking a checkpoint does not
// stop a guest, so it is still running.
func hyperVState(enabled int64) string {
	switch enabled {
	case 2, 32771:
		return "running"
	case 3:
		return "off"
	case 4, 32774:
		return "stopping"
	case 6, 32769:
		return "saved"
	case 9, 32768:
		return "paused"
	case 10, 32770:
		return "starting"
	case 11:
		return "resetting"
	case 32773:
		return "saving"
	case 32776:
		return "pausing"
	case 32777:
		return "resuming"
	case 0:
		return "unknown"
	}
	return fmt.Sprintf("state %d", enabled)
}

// isGUID reports whether s is a GUID written the usual way, with or without
// braces.
func isGUID(s string) bool {
	s = strings.TrimSuffix(strings.TrimPrefix(s, "{"), "}")
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
				return false
			}
		}
	}
	return true
}

/* ---------------- Hyper-V through Get-VM ---------------- */

// getVMEntry is one guest in hyperVGetVMCommand's output.
type getVMEntry struct {
	Name           string  `json:"Name"`
	ID             string  `json:"Id"`
	State          string  `json:"State"`
	UptimeSeconds  float64 `json:"UptimeSeconds"`
	CPUUsage       float64 `json:"CPUUsage"`
	MemoryAssigned uint64  `json:"MemoryAssigned"`
	MemoryDemand   uint64  `json:"MemoryDemand"`
	ProcessorCount int     `json:"ProcessorCount"`
}

// parseGetVM reads hyperVGetVMCommand's JSON: an array of guests (or, from an
// older PowerShell that unrolls a one-element array, a single one).
func parseGetVM(text string) ([]model.HostVM, error) {
	text = strings.TrimSpace(strings.TrimPrefix(text, "\ufeff"))
	if text == "" {
		return nil, nil
	}
	var list []getVMEntry
	if strings.HasPrefix(text, "{") {
		var one getVMEntry
		if err := json.Unmarshal([]byte(text), &one); err != nil {
			return nil, fmt.Errorf("reading Get-VM's output: %w", err)
		}
		list = []getVMEntry{one}
	} else if err := json.Unmarshal([]byte(text), &list); err != nil {
		return nil, fmt.Errorf("reading Get-VM's output: %w", err)
	}
	out := make([]model.HostVM, 0, len(list))
	for _, e := range list {
		v := model.HostVM{Name: e.Name, Hypervisor: "hyperv", ID: strings.ToLower(e.ID), State: getVMState(e.State)}
		if v.State != "off" && v.State != "saved" {
			v.UptimeSecs = math.Round(e.UptimeSeconds)
			v.Processors = e.ProcessorCount
			v.MemoryAssignedBytes = e.MemoryAssigned
			if strings.HasPrefix(v.State, "running") {
				pct := clampPct(e.CPUUsage)
				v.CPUPct = &pct
			}
			// Get-VM reports demand only for a guest with dynamic memory
			// and running integration services; zero is "no figure".
			if e.MemoryDemand > 0 && e.MemoryAssigned > 0 {
				pct := clampPct(float64(e.MemoryDemand) / float64(e.MemoryAssigned) * 100)
				v.MemoryPct = &pct
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// getVMState words Get-VM's VMState the way hyperVState words WMI's, so a
// guest reads the same whichever answered. The Critical states are a guest
// whose storage has gone away underneath it.
func getVMState(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "running":
		return "running"
	case "off":
		return "off"
	case "saved", "fastsaved", "hibernated":
		return "saved"
	case "paused":
		return "paused"
	case "starting":
		return "starting"
	case "stopping", "forceshutdown":
		return "stopping"
	case "saving", "fastsaving":
		return "saving"
	case "pausing":
		return "pausing"
	case "resuming":
		return "resuming"
	case "reset", "forcereboot":
		return "resetting"
	case "":
		return "unknown"
	}
	lower := strings.ToLower(s)
	if base, ok := strings.CutSuffix(lower, "critical"); ok && base != "" {
		return getVMState(base) + " (critical)"
	}
	return lower
}

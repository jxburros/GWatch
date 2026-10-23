package sysmetrics

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jxburros/GWatch/internal/model"
)

// Choosing what is reported.
//
// A machine has far more to report than anyone wants to read: every virtual
// bridge, tunnel and container interface appears next to the one network card
// that matters. A model.CollectSelection says which readings are kept, and
// these functions apply it to a reading after it has been taken. Filtering a
// finished reading rather than steering the platform collectors is deliberate:
// rates are derived from consecutive readings, so a collector that skipped an
// interface for a while would hand back a nonsense rate when it was switched
// back on.

// virtualInterfaceMarkers are fragments of interface names that mark one as
// scaffolding rather than a network connection. The list errs on the side of
// hiding: what is hidden is one tick away in the inventory, whereas a hundred
// container interfaces on a Docker host are noise nobody asked for.
var virtualInterfaceMarkers = []string{
	// Windows pseudo and tunnel adapters.
	"loopback", "pseudo-interface", "tunnel", "teredo", "isatap", "6to4", "miniport",
	"wi-fi direct", "virtual wifi", "local area connection*", "bluetooth", "kernel debug",
	// Hypervisor and VPN adapters that are not the machine's own connection.
	"vethernet", "vmware", "virtualbox", "host-only", "hyper-v", "tap-windows", "npcap",
}

// virtualInterfacePrefixes are the name prefixes Linux and macOS give to
// interfaces that exist for containers, bridges, tunnels and the like. Any name
// that starts with one is virtual.
var virtualInterfacePrefixes = []string{
	"veth", "docker", "br-", "virbr", "vmnet", "vboxnet", "utun", "awdl", "llw", "anpi",
	"cni", "flannel", "cali", "kube", "cilium", "lxcbr", "lxdbr", "tailscale", "ipsec",
	"ip6tnl", "ip_vti", "teql", "dummy", "bridge",
}

// virtualInterfaceStems are short prefixes that only mean something when a
// number (or nothing) follows: "tun0" and "wg0" are tunnels, "tunnelbear" and
// "apple" are not, and "ap" must not swallow "apc-lan".
var virtualInterfaceStems = []string{
	"lo", "tun", "tap", "ap", "gif", "stf", "wg", "sit", "gre", "ppp", "ifb",
}

// InterfaceIsAutomatic reports whether the automatic selection includes an
// interface: one that is not obviously virtual, and that is up or has carried
// traffic. A person who wants a tunnel or a bridge ticks it by name.
func InterfaceIsAutomatic(n model.HostInterface) bool {
	if looksVirtual(n.Name) {
		return false
	}
	return n.Up || n.RxBytes > 0 || n.TxBytes > 0
}

func looksVirtual(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	for _, m := range virtualInterfaceMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	for _, p := range virtualInterfacePrefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	for _, p := range virtualInterfaceStems {
		if rest, ok := strings.CutPrefix(lower, p); ok && (rest == "" || isDigits(rest)) {
			return true
		}
	}
	return false
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// InventoryOf lists what a reading contains that a person could choose from.
func InventoryOf(m model.HostMetrics) model.Inventory {
	var inv model.Inventory
	for _, n := range m.Interfaces {
		detail := strings.Join(n.Addresses, ", ")
		if n.SpeedMbit > 0 {
			if detail != "" {
				detail += " · "
			}
			detail += fmt.Sprintf("%d Mbit/s", n.SpeedMbit)
		}
		if !n.Up {
			if detail != "" {
				detail += " · "
			}
			detail += "down"
		}
		inv.Interfaces = append(inv.Interfaces, model.InventoryItem{Name: n.Name, Detail: detail, Auto: InterfaceIsAutomatic(n)})
	}
	for _, d := range m.Disks {
		inv.Disks = append(inv.Disks, model.InventoryItem{Name: d.Name, Auto: true})
	}
	for _, f := range m.Filesystems {
		detail := f.Device
		if f.FSType != "" {
			if detail != "" {
				detail += " · "
			}
			detail += f.FSType
		}
		inv.Mounts = append(inv.Mounts, model.InventoryItem{Name: f.Mount, Detail: detail, Auto: true})
	}
	sortItems := func(items []model.InventoryItem) {
		sort.SliceStable(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	}
	sortItems(inv.Interfaces)
	sortItems(inv.Disks)
	sortItems(inv.Mounts)
	return inv
}

// Apply keeps only what sel chooses. A list that is empty chooses nothing in
// particular, which means "automatic": every metric family, every disk and
// filesystem, and — when automatic is true — only the interfaces
// InterfaceIsAutomatic allows. automatic is false when a reading is being
// narrowed to what a machine's own configuration permits, so the inventory
// built from it still lists the interfaces that would be hidden by default.
//
// Filesystem space and inodes travel together in a reading, so choosing
// "inodes" keeps the whole filesystem entry and choosing only "disk" clears the
// inode figures.
func Apply(m model.HostMetrics, sel model.CollectSelection, automatic bool) model.HostMetrics {
	out := m
	has := func(family string) bool {
		if len(sel.Metrics) == 0 {
			return true
		}
		for _, f := range sel.Metrics {
			if f == family {
				return true
			}
		}
		return false
	}

	if !has(model.MetricCPU) {
		out.CPU.UsagePct, out.CPU.UserPct, out.CPU.SystemPct, out.CPU.IOWaitPct, out.CPU.StealPct = nil, nil, nil, nil, nil
	}
	if !has(model.MetricLoad) {
		out.CPU.Load1, out.CPU.Load5, out.CPU.Load15, out.CPU.LoadPerCore = nil, nil, nil, nil
	}
	if !has(model.MetricMemory) {
		out.Memory.TotalBytes, out.Memory.UsedBytes, out.Memory.AvailableBytes, out.Memory.CachedBytes = 0, 0, 0, 0
		out.Memory.UsedPct = 0
	}
	if !has(model.MetricSwap) {
		out.Memory.SwapTotalBytes, out.Memory.SwapUsedBytes, out.Memory.SwapUsedPct = 0, 0, nil
	}

	switch {
	case !has(model.MetricDisk) && !has(model.MetricInodes):
		out.Filesystems = nil
	default:
		fss := make([]model.HostFilesystem, 0, len(m.Filesystems))
		for _, f := range m.Filesystems {
			if !listed(sel.Mounts, f.Mount) {
				continue
			}
			if !has(model.MetricInodes) {
				f.InodesTotal, f.InodesUsed, f.InodesUsedPct = 0, 0, nil
			}
			fss = append(fss, f)
		}
		out.Filesystems = fss
	}

	if !has(model.MetricNet) {
		out.Interfaces = nil
	} else {
		nics := make([]model.HostInterface, 0, len(m.Interfaces))
		for _, n := range m.Interfaces {
			switch {
			case len(sel.Interfaces) > 0:
				if !listed(sel.Interfaces, n.Name) {
					continue
				}
			case automatic && !InterfaceIsAutomatic(n):
				continue
			}
			nics = append(nics, n)
		}
		out.Interfaces = nics
	}

	if !has(model.MetricDiskIO) {
		out.Disks = nil
	} else {
		ds := make([]model.HostDiskIO, 0, len(m.Disks))
		for _, d := range m.Disks {
			if listed(sel.Disks, d.Name) {
				ds = append(ds, d)
			}
		}
		out.Disks = ds
	}

	// Temperatures, pressure and pool health are whole families with no
	// instance list of their own: a machine with one noisy sensor turns the
	// family off, or leaves the sensor charted without a threshold.
	if !has(model.MetricTemp) {
		out.Temperatures = nil
	}
	if !has(model.MetricPSI) {
		out.Pressure = nil
	}
	if !has(model.MetricPool) {
		out.Pools = nil
	}
	return out
}

// listed reports whether name is chosen by an instance list. An empty list
// chooses everything.
func listed(list []string, name string) bool {
	if len(list) == 0 {
		return true
	}
	for _, s := range list {
		if s == name {
			return true
		}
	}
	return false
}

package model

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// What an agent may ask GWatch for, and what GWatch may answer.
//
// An agent connects outwards, so GWatch can never tell it to do anything. What
// it can do is answer, when the agent asks, a handful of questions whose
// answers are plain data: which readings to collect, how long to wait before
// asking again, which agent release is the newest, and — only while an
// administrator is rotating this machine's token — the replacement token;
// and, for an agent both its owner and an administrator have made a remote
// collector, the built-in network checks to run (entry 16). That is the whole
// list. There is no field here that names a URL to fetch code from, a command
// to run, a file to write or a version to install, and adding one would change
// what an agent is, so it is written as a closed set of types rather than a map
// (docs/AGENT-DECISIONS.md, entry 12).

// Collection limits. They bound what an agent will accept from a server and
// what a server will accept from a person, so a confused client cannot make
// either side carry something unreasonable.
const (
	MaxCollectNames   = 256 // entries per list
	MaxCollectNameLen = 200 // characters per entry

	// CheckInDefault is how often an agent asks GWatch for its settings when
	// nothing says otherwise. Rare on purpose: nothing it learns is urgent.
	CheckInDefault = 15 * time.Minute
	CheckInMin     = time.Minute
	CheckInMax     = 24 * time.Hour
)

// CollectSelection says which readings an agent takes and sends. An empty list
// means "automatic" — everything the agent would collect had nobody chosen —
// so the zero value is the agent's behaviour before this existed.
//
// Two of these meet on every agent: the one in the agent's own configuration
// file, which is a ceiling the machine's owner sets, and the one an
// administrator sets in GWatch, which chooses within it. Effective applies the
// rule.
type CollectSelection struct {
	// Metrics are the families to collect (cpu, memory, swap, load, disk,
	// inodes, net, diskio — the MetricFamilies). Empty means every family.
	Metrics []string `json:"metrics,omitempty"`
	// Interfaces are the network interface names to report. Empty means the
	// automatic set, which leaves out virtual and tunnel interfaces.
	Interfaces []string `json:"interfaces,omitempty"`
	// Disks are the block devices whose throughput is reported. Empty means
	// the automatic set.
	Disks []string `json:"disks,omitempty"`
	// Mounts are the filesystems reported, by mount point. Empty means the
	// automatic set.
	Mounts []string `json:"mounts,omitempty"`

	// Services and Processes are the services and programs to watch, by
	// name. Unlike the lists above, empty here means none rather than
	// automatic: there is no sensible "every service" to fall back on, and
	// looking for nothing is what keeps a machine nobody asked about from
	// running systemctl or walking the process table on every reading. A
	// service is a systemd unit ("nginx", or "nginx.service"), a Windows
	// service's name or display name, or a launchd label; a process is an
	// executable name ("plex", "Plex Media Server.exe"). The ceiling rule is
	// the same as everywhere else: names in the agent's own file are both
	// what it watches when GWatch has not chosen and the most GWatch can
	// choose from.
	Services  []string `json:"services,omitempty"`
	Processes []string `json:"processes,omitempty"`

	// Containers and VMs are watched the same way: by name, empty meaning
	// none. A container is a Docker or Podman container's name, as docker ps
	// shows it ("web", "nextcloud-db-1"); a VM is a guest's name as its
	// hypervisor shows it (Hyper-V Manager's Name column). Naming one is the
	// only thing that makes a machine ask its Docker daemon or its
	// hypervisor anything.
	Containers []string `json:"containers,omitempty"`
	VMs        []string `json:"vms,omitempty"`
}

// Watching reports whether the selection names any service, process,
// container or virtual machine to watch, which is the only time a collector
// looks for them.
func (c CollectSelection) Watching() bool {
	return len(watchNames(c.Services)) > 0 || len(watchNames(c.Processes)) > 0 ||
		len(watchNames(c.Containers)) > 0 || len(watchNames(c.VMs)) > 0
}

// WatchedServices, WatchedProcesses, WatchedContainers and WatchedVMs are
// the names to look for, without the empty entry that stands for "both sides
// chose and nothing is in common" (see combineNames).
func (c CollectSelection) WatchedServices() []string   { return watchNames(c.Services) }
func (c CollectSelection) WatchedProcesses() []string  { return watchNames(c.Processes) }
func (c CollectSelection) WatchedContainers() []string { return watchNames(c.Containers) }
func (c CollectSelection) WatchedVMs() []string        { return watchNames(c.VMs) }

// WatchOnly is the part of a selection that names things to watch, with the
// rest left automatic.
func (c CollectSelection) WatchOnly() CollectSelection {
	return CollectSelection{Services: c.Services, Processes: c.Processes, Containers: c.Containers, VMs: c.VMs}
}

func watchNames(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// IsZero reports whether the selection chooses nothing, which is "automatic"
// everywhere.
func (c CollectSelection) IsZero() bool {
	return len(c.Metrics) == 0 && len(c.Interfaces) == 0 && len(c.Disks) == 0 && len(c.Mounts) == 0 &&
		len(c.Services) == 0 && len(c.Processes) == 0 && len(c.Containers) == 0 && len(c.VMs) == 0
}

// Normalize trims, de-duplicates and sorts every list, so two selections that
// mean the same thing compare equal and are stored the same way.
func (c CollectSelection) Normalize() CollectSelection {
	return CollectSelection{
		Metrics:    normalizeNames(c.Metrics),
		Interfaces: normalizeNames(c.Interfaces),
		Disks:      normalizeNames(c.Disks),
		Mounts:     normalizeNames(c.Mounts),
		Services:   normalizeNames(c.Services),
		Processes:  normalizeNames(c.Processes),
		Containers: normalizeNames(c.Containers),
		VMs:        normalizeNames(c.VMs),
	}
}

func normalizeNames(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

// Validate reports a selection that should be refused rather than stored: an
// unknown metric family (which would silently collect nothing), or more or
// longer entries than any machine has.
func (c CollectSelection) Validate() error {
	for _, m := range c.Metrics {
		if !isMetricFamily(m) {
			return fmt.Errorf("%q is not a metric family; use %s", m, strings.Join(MetricFamilies, ", "))
		}
	}
	for label, list := range map[string][]string{"metrics": c.Metrics, "interfaces": c.Interfaces, "disks": c.Disks, "mounts": c.Mounts, "services": c.Services, "processes": c.Processes, "containers": c.Containers, "virtual machines": c.VMs} {
		if len(list) > MaxCollectNames {
			return fmt.Errorf("too many %s (at most %d)", label, MaxCollectNames)
		}
		for _, s := range list {
			if len(s) > MaxCollectNameLen {
				return fmt.Errorf("a name in %s is too long", label)
			}
		}
	}
	// A service or program name is handed to systemctl, launchctl or the
	// Service Control Manager as one argument. Nothing is run through a
	// shell, but a name starting with a dash would read as an option, and a
	// control character is never part of a real name. Container and guest
	// names are only ever compared with what the daemon or the hypervisor
	// lists, never passed to either, but the same rule keeps them sane.
	for kind, list := range map[string][]string{"service": c.Services, "program": c.Processes, "container": c.Containers, "virtual machine": c.VMs} {
		for _, s := range list {
			if strings.HasPrefix(s, "-") || strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
				return fmt.Errorf("%q is not a %s name", s, kind)
			}
		}
	}
	return nil
}

func isMetricFamily(s string) bool {
	for _, f := range MetricFamilies {
		if f == s {
			return true
		}
	}
	return false
}

// Effective combines a ceiling (the agent's own configuration) with a
// selection (chosen in GWatch). For each list: nothing chosen by either side
// is automatic; one side chosen uses that side; both chosen keeps only what
// both allow. The machine's owner therefore always has the last word on what
// leaves the machine, and GWatch can never widen it.
func (c CollectSelection) Effective(selection CollectSelection) CollectSelection {
	return CollectSelection{
		Metrics:    combineNames(c.Metrics, selection.Metrics),
		Interfaces: combineNames(c.Interfaces, selection.Interfaces),
		Disks:      combineNames(c.Disks, selection.Disks),
		Mounts:     combineNames(c.Mounts, selection.Mounts),
		Services:   combineNames(c.Services, selection.Services),
		Processes:  combineNames(c.Processes, selection.Processes),
		Containers: combineNames(c.Containers, selection.Containers),
		VMs:        combineNames(c.VMs, selection.VMs),
	}
}

func combineNames(ceiling, selection []string) []string {
	switch {
	case len(ceiling) == 0:
		return selection
	case len(selection) == 0:
		return ceiling
	}
	allowed := make(map[string]bool, len(ceiling))
	for _, s := range ceiling {
		allowed[s] = true
	}
	out := make([]string, 0, len(selection))
	for _, s := range selection {
		if allowed[s] {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		// Both sides chose and nothing is in common. An empty list would mean
		// "automatic", the opposite of what was asked, so the only honest
		// answer is a list nobody can match.
		return []string{""}
	}
	return out
}

// InventoryItem is one thing an agent could report: a network interface, a
// block device or a mounted filesystem.
type InventoryItem struct {
	Name string `json:"name"`
	// Detail is a short description for a person choosing: an address, a size,
	// a filesystem type.
	Detail string `json:"detail,omitempty"`
	// Auto is whether the automatic selection includes it. Virtual and tunnel
	// interfaces are not Auto, so the list a person ticks from starts out as
	// the sensible one.
	Auto bool `json:"auto"`
}

// Inventory is what an agent could collect, within what its own configuration
// allows. It is what lets GWatch offer checkboxes for a drive it has not been
// told about, and it is the only thing an agent says about itself beyond its
// readings.
type Inventory struct {
	Interfaces []InventoryItem `json:"interfaces,omitempty"`
	Disks      []InventoryItem `json:"disks,omitempty"`
	Mounts     []InventoryItem `json:"mounts,omitempty"`

	// Services and Processes are what could be watched: every service the
	// system knows, with its description and whether it is running now, and
	// every program running at the moment the list was made. They are made
	// when asked for — at an agent's check-in, or when someone opens the
	// list for this computer — never with a reading, and Auto is always
	// false: nothing is watched until someone picks it.
	Services  []InventoryItem `json:"services,omitempty"`
	Processes []InventoryItem `json:"processes,omitempty"`

	// Containers and VMs are every container the machine's Docker or Podman
	// daemon has, running or not, and every guest its hypervisor has, made
	// at the same moments and on the same terms.
	Containers []InventoryItem `json:"containers,omitempty"`
	VMs        []InventoryItem `json:"vms,omitempty"`
}

// MaxInventoryWatchNames bounds the service and process lists of an
// inventory. A Windows machine has three or four hundred services, more than
// the other lists ever hold, and the whole check-in still has to fit
// comfortably in what GWatch accepts.
const MaxInventoryWatchNames = 1024

// Normalize bounds an inventory received from the network.
func (i Inventory) Normalize() Inventory {
	clipTo := func(in []InventoryItem, limit int) []InventoryItem {
		if len(in) > limit {
			in = in[:limit]
		}
		out := make([]InventoryItem, 0, len(in))
		for _, it := range in {
			it.Name = strings.TrimSpace(it.Name)
			if it.Name == "" {
				continue
			}
			if len(it.Name) > MaxCollectNameLen {
				it.Name = it.Name[:MaxCollectNameLen]
			}
			if len(it.Detail) > MaxCollectNameLen {
				it.Detail = it.Detail[:MaxCollectNameLen]
			}
			out = append(out, it)
		}
		return out
	}
	clip := func(in []InventoryItem) []InventoryItem { return clipTo(in, MaxCollectNames) }
	return Inventory{
		Interfaces: clip(i.Interfaces), Disks: clip(i.Disks), Mounts: clip(i.Mounts),
		Services:   clipTo(i.Services, MaxInventoryWatchNames),
		Processes:  clipTo(i.Processes, MaxInventoryWatchNames),
		Containers: clipTo(i.Containers, MaxInventoryWatchNames),
		VMs:        clipTo(i.VMs, MaxInventoryWatchNames),
	}
}

// AgentCheckIn is what an agent says when it asks GWatch for its settings.
type AgentCheckIn struct {
	Version   string     `json:"version,omitempty"`
	Inventory *Inventory `json:"inventory,omitempty"`
	// Collector is whether this machine's own configuration allows it to run
	// checks for GWatch (`"collector": true` in agent.json).
	Collector bool `json:"collector,omitempty"`
}

// AgentSettings is GWatch's whole answer to a check-in. See the note at the
// top of this file: this struct is the complete list of what an agent can be
// told, and every field is data.
type AgentSettings struct {
	// CheckInSeconds is when to ask again.
	CheckInSeconds int `json:"checkInSeconds"`
	// Collect is the selection an administrator made for this machine.
	Collect CollectSelection `json:"collect"`
	// LatestAgent is the newest agent release GWatch knows of, or empty. It is
	// a hint that a release exists, nothing more: an agent still fetches and
	// verifies releases itself, from the release feed, and never from here.
	LatestAgent string `json:"latestAgent,omitempty"`
	// Token is the replacement token, present only while a rotation is waiting
	// to be picked up, and only when the caller authenticated with the token
	// being replaced.
	Token string `json:"token,omitempty"`
	// Collector is the list of network checks this agent is to run as a
	// remote collector. It is present only when an administrator has turned
	// collecting on for this agent and the agent said in this check-in that
	// its own configuration allows it, so neither side alone can start it and
	// no check's credentials go to a machine that has not opted in. See
	// model/collector.go and docs/AGENT-DECISIONS.md, entry 16.
	Collector *CollectorAssignment `json:"collector,omitempty"`
}

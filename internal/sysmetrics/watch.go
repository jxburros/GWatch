package sysmetrics

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// Watching services and programs.
//
// Everything else a reading holds is the whole machine: every disk, every
// interface. Services and processes are the opposite. A machine runs hundreds
// of each, almost none of which anyone cares about, and finding out about
// them costs a command (systemctl, launchctl) or a walk of the process table.
// So nothing here runs unless someone has named the services and programs
// they want watched (model.CollectSelection.Services and .Processes), and then
// only those are reported: a Collector with nothing to watch is exactly as
// cheap as one from before this existed.
//
// The other half is the list a person picks names from. That is the whole
// machine, and it is made only when asked for (ListServices, ListProcesses):
// when an agent checks in with GWatch, which is every quarter of an hour or
// so, or when someone opens the list for the computer GWatch runs on. It is
// never part of a reading.
//
// Each platform supplies three things: the state of the named services, the
// full list of services, and the executable name of every running process.
// Linux asks systemctl, Windows the Service Control Manager, macOS launchctl
// and ps. None needs privilege beyond what the reading already has, although
// macOS only lists the system's own launchd jobs to an agent running as root
// (which an installed agent is).

// watchTimeout bounds the commands run to answer for services. systemctl
// answers from memory in milliseconds; one stuck behind a busy D-Bus can take
// far longer, and the reading must not wait for it.
const watchTimeout = 5 * time.Second

// SetWatch names the services, programs, containers and virtual machines
// every later Collect reports: the selection's watch lists, and nothing else
// from it. Empty lists watch nothing, which is the default and costs nothing.
// The names are copied, so the caller may reuse its slices.
func (c *Collector) SetWatch(sel model.CollectSelection) {
	c.mu.Lock()
	c.services = cleanNames(sel.WatchedServices())
	c.processes = cleanNames(sel.WatchedProcesses())
	c.containers = cleanNames(sel.WatchedContainers())
	c.vms = cleanNames(sel.WatchedVMs())
	c.mu.Unlock()
}

// watchLists is one reading's copy of what to watch.
type watchLists struct {
	services, processes, containers, vms []string
}

func (w watchLists) empty() bool {
	return len(w.services) == 0 && len(w.processes) == 0 && len(w.containers) == 0 && len(w.vms) == 0
}

func cleanNames(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// readWatched reads what is watched into m and returns any warnings. A
// source that cannot answer adds a warning and leaves its list out, rather
// than reporting everything it would have answered for as stopped: "could
// not tell" is not "down".
func (c *Collector) readWatched(ctx context.Context, m *model.HostMetrics, w watchLists) []string {
	var warnings []string
	if len(w.services) > 0 {
		got, err := serviceStates(ctx, w.services)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("services: %v", err))
		} else {
			m.Services = got
		}
	}
	if len(w.processes) > 0 {
		names, err := processNames(ctx)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("processes: %v", err))
		} else {
			m.Processes = countProcesses(names, w.processes, runtime.GOOS == "windows")
		}
	}
	if len(w.containers) > 0 {
		got, err := c.readDocker(ctx, w.containers)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("containers: %v", err))
		} else {
			m.Docker = got
		}
	}
	if len(w.vms) > 0 {
		got, more, err := readVMs(ctx, w.vms)
		warnings = append(warnings, more...)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("virtual machines: %v", err))
		} else {
			m.VMs = got
		}
	}
	return warnings
}

// countProcesses counts, for each watched name, the running processes with
// that executable name. Every watched name is in the answer, with a count of
// zero when nothing matches, because zero is the reading that matters.
//
// windowsNames matches the way Windows names programs: without regard to
// case, and with or without the ".exe" every executable carries, so "plex
// media server" and "Plex Media Server.exe" are the same program. Elsewhere a
// name is matched exactly, as the kernel reports it.
func countProcesses(running, watched []string, windowsNames bool) []model.HostProcess {
	key := func(s string) string {
		if !windowsNames {
			return s
		}
		s = strings.ToLower(s)
		return strings.TrimSuffix(s, ".exe")
	}
	counts := map[string]int{}
	for _, n := range running {
		counts[key(n)]++
	}
	out := make([]model.HostProcess, 0, len(watched))
	for _, w := range watched {
		out = append(out, model.HostProcess{Name: w, Count: counts[key(w)]})
	}
	return out
}

// pickServices answers for each watched name from a full list of services,
// with "not found" for a name the system does not have. match says whether a
// listed service is the one a watched name means; the first match wins.
func pickServices(all []model.HostService, watched []string, match func(svc model.HostService, name string) bool) []model.HostService {
	out := make([]model.HostService, 0, len(watched))
	for _, w := range watched {
		found := model.HostService{Name: w, State: "not found"}
		for _, s := range all {
			if match(s, w) {
				found = s
				found.Name = w
				break
			}
		}
		out = append(out, found)
	}
	return out
}

// ListServices lists every service this machine has, for a person choosing
// which to watch. Detail says whether it is running now and what it is.
func ListServices(ctx context.Context) ([]model.InventoryItem, error) {
	all, err := listServices(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.InventoryItem, 0, len(all))
	for _, s := range all {
		state := "stopped"
		if s.Running {
			state = "running"
		}
		detail := state
		if s.Description != "" && s.Description != s.Name {
			detail += " · " + s.Description
		}
		out = append(out, model.InventoryItem{Name: s.Name, Detail: detail})
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

// Watchable is everything a machine could be told to watch, for a person
// choosing, with what could not be listed and why.
type Watchable struct {
	Services   []model.InventoryItem `json:"services"`
	Processes  []model.InventoryItem `json:"processes"`
	Containers []model.InventoryItem `json:"containers"`
	VMs        []model.InventoryItem `json:"vms"`
	Problems   []string              `json:"problems"`
}

// ListWatchable makes every list at once. A list that cannot be made — no
// systemd, no Docker, no Hyper-V — is left empty with a problem saying so;
// the others are still worth having.
func ListWatchable(ctx context.Context) Watchable {
	var w Watchable
	var err error
	note := func(what string, err error) {
		if err != nil {
			w.Problems = append(w.Problems, what+": "+err.Error())
		}
	}
	w.Services, err = ListServices(ctx)
	note("services", err)
	w.Processes, err = ListProcesses(ctx)
	note("programs", err)
	w.Containers, err = ListContainers(ctx)
	if !errors.Is(err, errDockerNoSocket) {
		// A machine with no Docker at all is the usual case, not a problem.
		note("containers", err)
	}
	w.VMs, err = ListVMs(ctx)
	if !errors.Is(err, errVMUnsupported) {
		note("virtual machines", err)
	}
	return w
}

// ListProcesses lists the programs running now, once each, with how many
// copies are. It is a snapshot: a program that runs now and then (a backup
// job) may not be in it, and its name can be typed instead.
func ListProcesses(ctx context.Context) ([]model.InventoryItem, error) {
	names, err := processNames(ctx)
	if err != nil {
		return nil, err
	}
	return processInventory(names), nil
}

// processInventory folds a list of process names into one entry per name.
func processInventory(names []string) []model.InventoryItem {
	counts := map[string]int{}
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			counts[n]++
		}
	}
	out := make([]model.InventoryItem, 0, len(counts))
	for n, c := range counts {
		detail := "1 running"
		if c != 1 {
			detail = fmt.Sprintf("%d running", c)
		}
		out = append(out, model.InventoryItem{Name: n, Detail: detail})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// windowsServiceState words a Service Control Manager state (the
// SERVICE_STOPPED … SERVICE_PAUSED constants, 1 to 7) the way the Services
// console does. It lives here rather than with the Windows code so it is
// tested everywhere.
func windowsServiceState(state uint32) string {
	switch state {
	case 1:
		return "stopped"
	case 2:
		return "start pending"
	case 3:
		return "stop pending"
	case 4:
		return "running"
	case 5:
		return "continue pending"
	case 6:
		return "pause pending"
	case 7:
		return "paused"
	}
	return fmt.Sprintf("state %d", state)
}

/* ---------------- Linux: systemctl ---------------- */

// These parsers are for systemctl's output and are only called on Linux, but
// they are plain text functions and are kept here so their tests run on every
// platform.

// systemdUnitSuffixes are the unit types a watched name may already end in.
// Anything else is a service, so "nginx" means "nginx.service" — and so does
// "nginx.service", and "php8.2-fpm" means "php8.2-fpm.service" despite its dot.
var systemdUnitSuffixes = []string{".service", ".socket", ".timer", ".mount", ".target", ".path", ".scope", ".slice", ".swap", ".automount", ".device"}

// systemdUnitName turns a watched name into the unit systemctl knows it by.
func systemdUnitName(name string) string {
	for _, s := range systemdUnitSuffixes {
		if strings.HasSuffix(name, s) {
			return name
		}
	}
	return name + ".service"
}

// systemdRunning reports whether an ActiveState counts as running: active,
// or in the middle of reloading, which is a running service picking up new
// settings. "activating" is not yet running, and a service stuck there is
// one worth hearing about.
func systemdRunning(activeState string) bool {
	switch activeState {
	case "active", "reloading", "refreshing":
		return true
	}
	return false
}

// parseSystemctlShow reads `systemctl show -p … unit…`: one block of
// Key=value lines per unit, in the order the units were named, separated by
// blank lines.
func parseSystemctlShow(text string) []map[string]string {
	var out []map[string]string
	cur := map[string]string{}
	flush := func() {
		if len(cur) > 0 {
			out = append(out, cur)
			cur = map[string]string{}
		}
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			cur[k] = v
		}
	}
	flush()
	return out
}

// systemdServices turns the blocks of `systemctl show` into watched services,
// named as they were asked for. A unit systemd has never heard of still gets a
// block, with LoadState=not-found, which is reported as "not found".
func systemdServices(names []string, blocks []map[string]string) ([]model.HostService, error) {
	if len(blocks) != len(names) {
		return nil, fmt.Errorf("systemctl answered for %d units, not the %d asked about", len(blocks), len(names))
	}
	out := make([]model.HostService, 0, len(names))
	for i, b := range blocks {
		s := model.HostService{Name: names[i], Description: b["Description"]}
		switch {
		case b["LoadState"] == "not-found":
			s.State = "not found"
			s.Description = ""
		default:
			s.Running = systemdRunning(b["ActiveState"])
			s.State = b["ActiveState"]
			if sub := b["SubState"]; sub != "" && sub != s.State {
				s.State += " (" + sub + ")"
			}
			if b["LoadState"] == "masked" {
				s.State += ", masked"
			}
		}
		out = append(out, s)
	}
	return out, nil
}

// parseSystemctlListUnits reads `systemctl list-units --type=service --all
// --no-legend --plain`: unit, load state, active state, sub state, then the
// description, which may contain spaces. Units systemd only knows as a
// dangling reference (load state not-found) are left out; they are not
// services anyone could watch. The ".service" is dropped from each name,
// since that is how people write them.
func parseSystemctlListUnits(text string) []model.HostService {
	var out []model.HostService
	for _, line := range strings.Split(text, "\n") {
		// Some versions mark a failed unit with a bullet even with --plain.
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "●*"))
		fields := strings.Fields(line)
		if len(fields) < 4 || !strings.HasSuffix(fields[0], ".service") || fields[1] == "not-found" {
			continue
		}
		rest := line
		for i := 0; i < 4; i++ {
			rest = strings.TrimLeft(rest, " \t")
			if j := strings.IndexAny(rest, " \t"); j >= 0 {
				rest = rest[j:]
			} else {
				rest = ""
			}
		}
		desc := strings.TrimSpace(rest)
		state := fields[2]
		if fields[3] != state {
			state += " (" + fields[3] + ")"
		}
		out = append(out, model.HostService{
			Name:        strings.TrimSuffix(fields[0], ".service"),
			Running:     systemdRunning(fields[2]),
			State:       state,
			Description: desc,
		})
	}
	return out
}

// procName is a Linux process's executable name from its /proc/<pid>/comm
// and /proc/<pid>/cmdline. comm is the name the kernel keeps, cut to 15
// characters; when it has been cut, the program's own first argument says
// what the rest was ("Plex Media Serv" is "Plex Media Server"). A process
// with an empty command line is a kernel thread (or a zombie), which is not
// a program anyone means, and gets "".
func procName(comm, cmdline string) string {
	comm = strings.TrimRight(comm, "\n")
	if cmdline == "" || comm == "" {
		return ""
	}
	if len(comm) < 15 {
		return comm
	}
	argv0, _, _ := strings.Cut(cmdline, "\x00")
	if i := strings.LastIndexByte(argv0, '/'); i >= 0 {
		argv0 = argv0[i+1:]
	}
	if strings.HasPrefix(argv0, comm) {
		return argv0
	}
	return comm
}

/* ---------------- macOS: launchctl and ps ---------------- */

// parseLaunchctlList reads `launchctl list`: a header, then one line per job
// with its PID ("-" when it is not running), the status it last exited with,
// and its label. A running job has a PID; one that is loaded and waiting (to
// be started on demand, or on a schedule) has none and is reported as not
// running, with its last exit status when that was a failure.
func parseLaunchctlList(text string) []model.HostService {
	var out []model.HostService
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] == "PID" {
			continue
		}
		label := strings.Join(fields[2:], " ")
		s := model.HostService{Name: label}
		if fields[0] != "-" {
			s.Running, s.State = true, "running"
		} else {
			s.State = "not running"
			if fields[1] != "0" && fields[1] != "-" {
				s.State += " (last exit status " + fields[1] + ")"
			}
		}
		out = append(out, s)
	}
	return out
}

// launchdMatch says whether a launchd label is the job a watched name means:
// the label itself ("com.openssh.sshd"), or its last part ("sshd"), which is
// how most people would name it.
func launchdMatch(s model.HostService, name string) bool {
	return s.Name == name || strings.HasSuffix(s.Name, "."+name)
}

// windowsServiceMatch says whether a service is the one a watched name means:
// its service name ("Spooler") or its display name ("Print Spooler"), either
// without regard to case, as Windows itself treats them.
func windowsServiceMatch(s model.HostService, name string) bool {
	return strings.EqualFold(s.Name, name) || strings.EqualFold(s.Description, name)
}

// parsePsComm reads `ps -axco comm=`: one executable name per line.
func parsePsComm(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

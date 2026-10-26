package sysmetrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// Watching Docker containers.
//
// A container is watched the way a service is: by name, only when someone has
// named it (model.CollectSelection.Containers), and then only the named ones
// are asked about. The daemon is read over its HTTP API on the Unix socket it
// listens on, with GET requests only — the list of containers, then for each
// watched one its inspect record and one set of statistics. Nothing is ever
// started, stopped or changed, and the client has no code that could.
//
// Read-only use of the socket is still a lot of access: whoever can connect to
// /var/run/docker.sock can read every container's configuration, environment
// included, and the daemon itself does not distinguish a reader from a writer.
// That is why nothing here runs until a container is named, and why the docs
// ask for the agent's account to be chosen with it in mind (docs/HARDWARE.md).
//
// Podman serves the same API on its own socket, and is found the same way.

// dockerTimeout bounds one request to the daemon. A daemon busy pulling an
// image still answers these in milliseconds; one that does not answer in
// seconds is stuck, and the reading must not wait for it.
const dockerTimeout = 5 * time.Second

// dockerRestartWindow is how far back RecentRestarts counts.
const dockerRestartWindow = time.Hour

// errDockerNoSocket is the answer on a machine with no daemon to ask.
var errDockerNoSocket = errors.New("no Docker or Podman socket was found (set DOCKER_HOST=unix:///path/to/the.sock if it is somewhere else)")

// dockerSocket finds the socket to talk to: DOCKER_HOST when it names a Unix
// socket, otherwise the first of the usual places that holds one. It is a
// variable so a test can point it at a daemon of its own.
var dockerSocket = func() (string, error) {
	if h := strings.TrimSpace(os.Getenv("DOCKER_HOST")); h != "" {
		p, ok := strings.CutPrefix(h, "unix://")
		if !ok {
			return "", fmt.Errorf("DOCKER_HOST is %q; only a local daemon's Unix socket (unix://…) is read", h)
		}
		return p, nil
	}
	for _, p := range dockerSocketCandidates() {
		if fi, err := os.Stat(p); err == nil && fi.Mode()&fs.ModeSocket != 0 {
			return p, nil
		}
	}
	return "", errDockerNoSocket
}

// dockerSocketCandidates are where Docker and Podman put their sockets: the
// system daemon's, rootful and rootless Podman's, and Docker Desktop's on a
// Mac. Under a host root (an agent in a container reading the machine around
// it) the host's own are tried too, through /run since /var/run is a link the
// container would resolve to its own.
func dockerSocketCandidates() []string {
	list := []string{"/var/run/docker.sock", "/run/docker.sock", "/run/podman/podman.sock"}
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		list = append(list, filepath.Join(d, "podman", "podman.sock"), filepath.Join(d, "docker.sock"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		list = append(list, filepath.Join(home, ".docker", "run", "docker.sock"))
	}
	if hostRoot != "" {
		list = append(list, filepath.Join(hostRoot, "run", "docker.sock"), filepath.Join(hostRoot, "run", "podman", "podman.sock"))
	}
	return list
}

// dockerClient is an HTTP client whose every connection is to the socket.
type dockerClient struct {
	socket string
	http   *http.Client
}

func newDockerClient() (*dockerClient, error) {
	socket, err := dockerSocket()
	if err != nil {
		return nil, err
	}
	d := &net.Dialer{Timeout: dockerTimeout}
	return &dockerClient{socket: socket, http: &http.Client{
		Timeout: dockerTimeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return d.DialContext(ctx, "unix", socket)
			},
			// No Proxy: nothing but the socket is ever reached through this.
			MaxIdleConns:    2,
			IdleConnTimeout: 10 * time.Second,
		},
	}}, nil
}

// get reads one API path into out. The host name in the URL is a placeholder;
// the transport only ever dials the socket.
func (c *dockerClient) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker"+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		var op *net.OpError
		if errors.As(err, &op) && errors.Is(op.Err, os.ErrPermission) {
			return fmt.Errorf("this account may not use %s (add it to the docker group, or run the agent as an account that may)", c.socket)
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var msg struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&msg)
		if msg.Message == "" {
			msg.Message = resp.Status
		}
		return fmt.Errorf("%s: %s", path, msg.Message)
	}
	// A daemon with thousands of containers still lists them in a few
	// megabytes; anything bigger is not an answer to read.
	return json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(out)
}

func (c *dockerClient) close() { c.http.CloseIdleConnections() }

/* ---------------- what the API returns ---------------- */

// These are the few fields read from each answer. Docker and Podman both
// return far more; anything missing is left at its zero value.

// dockerListEntry is one container in GET /containers/json?all=1.
type dockerListEntry struct {
	ID     string   `json:"Id"`
	Names  []string `json:"Names"`
	Image  string   `json:"Image"`
	State  string   `json:"State"`
	Status string   `json:"Status"`
}

// name is the container's name without the slash Docker puts before it.
func (e dockerListEntry) name() string {
	for _, n := range e.Names {
		if n = strings.TrimPrefix(n, "/"); n != "" && !strings.Contains(n, "/") {
			return n
		}
	}
	return ""
}

// dockerInspect is GET /containers/{id}/json.
type dockerInspect struct {
	ID           string `json:"Id"`
	RestartCount int    `json:"RestartCount"`
	State        struct {
		Status     string `json:"Status"`
		Running    bool   `json:"Running"`
		Paused     bool   `json:"Paused"`
		Restarting bool   `json:"Restarting"`
		ExitCode   int    `json:"ExitCode"`
		StartedAt  string `json:"StartedAt"`
		Health     *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
	HostConfig struct {
		NanoCPUs  int64 `json:"NanoCpus"`
		CPUQuota  int64 `json:"CpuQuota"`
		CPUPeriod int64 `json:"CpuPeriod"`
		Memory    int64 `json:"Memory"`
	} `json:"HostConfig"`
}

// dockerStats is GET /containers/{id}/stats?stream=false.
type dockerStats struct {
	CPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemUsage uint64 `json:"system_cpu_usage"`
		OnlineCPUs  int    `json:"online_cpus"`
	} `json:"cpu_stats"`
	MemoryStats struct {
		Usage uint64            `json:"usage"`
		Limit uint64            `json:"limit"`
		Stats map[string]uint64 `json:"stats"`
	} `json:"memory_stats"`
}

/* ---------------- turning answers into readings ---------------- */

// dockerCPUMark is one container's processor counters at one reading: the
// container's cumulative processor time and the whole machine's, both in
// nanoseconds, as the daemon reports them.
type dockerCPUMark struct {
	id            string
	total, system uint64
}

// dockerRestartMark is a container's restart count at one reading.
type dockerRestartMark struct {
	at    time.Time
	id    string
	count int
}

// dockerState is what a Collector remembers between readings: the processor
// counters each rate is measured from, and the restart counts each recent
// figure is counted from. Both are keyed by the watched name.
type dockerState struct {
	cpu      map[string]dockerCPUMark
	restarts map[string][]dockerRestartMark
}

// dockerContainer builds a watched container's reading from its list entry,
// inspect record and statistics (nil for a container that is not running).
// prev is its processor counters at the last reading, if there was one.
func dockerContainer(name string, e dockerListEntry, in dockerInspect, st *dockerStats, prev *dockerCPUMark) (model.HostDockerContainer, *dockerCPUMark) {
	c := model.HostDockerContainer{
		Name:         name,
		ID:           shortID(e.ID),
		Image:        e.Image,
		State:        in.State.Status,
		Status:       e.Status,
		ExitCode:     in.State.ExitCode,
		RestartCount: in.RestartCount,
	}
	if c.State == "" {
		c.State = e.State
	}
	if in.State.Health != nil {
		c.Health = in.State.Health.Status
	}
	if t, err := time.Parse(time.RFC3339Nano, in.State.StartedAt); err == nil && t.Year() > 1 {
		c.StartedAt = &t
	}
	if c.State == "running" {
		// The code a running container last exited with, before its
		// restart policy brought it back, is history rather than state.
		c.ExitCode = 0
	}

	var limitCores float64
	switch {
	case in.HostConfig.NanoCPUs > 0:
		limitCores = float64(in.HostConfig.NanoCPUs) / 1e9
	case in.HostConfig.CPUQuota > 0 && in.HostConfig.CPUPeriod > 0:
		limitCores = float64(in.HostConfig.CPUQuota) / float64(in.HostConfig.CPUPeriod)
	}
	if limitCores > 0 {
		c.CPULimitCores = &limitCores
	}
	if st == nil || c.State != "running" {
		return c, nil
	}

	// Memory as docker stats counts it: what the cgroup is charged, less the
	// file cache the kernel would drop before it ran out (inactive_file on
	// cgroup v2, total_inactive_file on v1).
	used := st.MemoryStats.Usage
	cache, ok := st.MemoryStats.Stats["inactive_file"]
	if !ok {
		cache = st.MemoryStats.Stats["total_inactive_file"]
	}
	if cache < used {
		used -= cache
	}
	limit := st.MemoryStats.Limit
	if in.HostConfig.Memory > 0 {
		limit = uint64(in.HostConfig.Memory)
	}
	if used > 0 {
		c.MemoryUsedBytes = used
	}
	if limit > 0 && used > 0 {
		c.MemoryLimitBytes = limit
		pct := clampPct(float64(used) / float64(limit) * 100)
		c.MemoryPct = &pct
	}

	// Processor use is a rate, measured between this reading and the last
	// one of the same container: the container's processor time over the
	// machine's, scaled by its processors, is how many cores it kept busy.
	mark := &dockerCPUMark{id: e.ID, total: st.CPUStats.CPUUsage.TotalUsage, system: st.CPUStats.SystemUsage}
	if prev != nil && prev.id == e.ID && mark.system > prev.system && mark.total >= prev.total {
		cpus := st.CPUStats.OnlineCPUs
		if cpus <= 0 {
			cpus = 1
		}
		cores := float64(mark.total-prev.total) / float64(mark.system-prev.system) * float64(cpus)
		against := limitCores
		if against <= 0 || against > float64(cpus) {
			against = float64(cpus)
		}
		pct := clampPct(cores / against * 100)
		c.CPUPct = &pct
	}
	return c, mark
}

// recentRestarts counts how many restarts a container has had in the window
// before now, from the counts this Collector has seen, and returns the marks
// to keep. Docker counts restarts since a container was last started by hand
// and starts again from zero when it is (or when it is created anew, as
// Compose does), so a count that goes down, or a different container under
// the name, starts the history again. The first reading of a container has
// nothing to compare with and counts nothing.
func recentRestarts(marks []dockerRestartMark, now time.Time, id string, count int) (int, []dockerRestartMark) {
	if n := len(marks); n > 0 && (marks[n-1].id != id || marks[n-1].count > count) {
		marks = nil
	}
	marks = append(marks, dockerRestartMark{at: now, id: id, count: count})
	// The baseline is the newest mark at or before the start of the window,
	// so a restart just inside the hour still counts; anything older than
	// that is no longer needed.
	cutoff := now.Add(-dockerRestartWindow)
	first := 0
	for i, m := range marks {
		if !m.at.After(cutoff) {
			first = i
		}
	}
	marks = marks[first:]
	return max(0, count-marks[0].count), marks
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

/* ---------------- reading and listing ---------------- */

// readDocker reads the named containers. Every watched name is in the answer,
// "not found" when the daemon has no container by it. A daemon that cannot be
// reached at all is an error, which the caller reports as a warning rather
// than calling every container stopped: "could not tell" is not "down".
func (c *Collector) readDocker(ctx context.Context, names []string) ([]model.HostDockerContainer, error) {
	client, err := newDockerClient()
	if err != nil {
		return nil, err
	}
	defer client.close()
	var list []dockerListEntry
	if err := client.get(ctx, "/containers/json?all=1", &list); err != nil {
		return nil, err
	}
	byName := make(map[string]dockerListEntry, len(list))
	for _, e := range list {
		if n := e.name(); n != "" {
			byName[n] = e
		}
	}

	c.mu.Lock()
	if c.docker.cpu == nil {
		c.docker = dockerState{cpu: map[string]dockerCPUMark{}, restarts: map[string][]dockerRestartMark{}}
	}
	c.mu.Unlock()

	now := time.Now()
	out := make([]model.HostDockerContainer, 0, len(names))
	for _, name := range names {
		e, ok := byName[name]
		if !ok {
			out = append(out, model.HostDockerContainer{Name: name, State: "not found"})
			c.mu.Lock()
			delete(c.docker.cpu, name)
			delete(c.docker.restarts, name)
			c.mu.Unlock()
			continue
		}
		var in dockerInspect
		if err := client.get(ctx, "/containers/"+url.PathEscape(e.ID)+"/json", &in); err != nil {
			// Gone between the list and now, or the daemon is struggling:
			// the list's own word for it is still worth reporting.
			out = append(out, model.HostDockerContainer{Name: name, ID: shortID(e.ID), Image: e.Image, State: e.State, Status: e.Status})
			continue
		}
		var st *dockerStats
		if in.State.Status == "running" {
			var s dockerStats
			// one-shot answers at once instead of waiting a second to
			// work out a rate of its own; the rate is worked out here.
			if err := client.get(ctx, "/containers/"+url.PathEscape(e.ID)+"/stats?stream=false&one-shot=true", &s); err == nil {
				st = &s
			}
		}
		c.mu.Lock()
		var prev *dockerCPUMark
		if p, ok := c.docker.cpu[name]; ok {
			prev = &p
		}
		got, mark := dockerContainer(name, e, in, st, prev)
		if mark != nil {
			c.docker.cpu[name] = *mark
		} else {
			delete(c.docker.cpu, name)
		}
		got.RecentRestarts, c.docker.restarts[name] = recentRestarts(c.docker.restarts[name], now, e.ID, got.RestartCount)
		c.mu.Unlock()
		out = append(out, got)
	}

	// Forget containers that are no longer watched.
	c.mu.Lock()
	watched := make(map[string]bool, len(names))
	for _, n := range names {
		watched[n] = true
	}
	for n := range c.docker.restarts {
		if !watched[n] {
			delete(c.docker.restarts, n)
			delete(c.docker.cpu, n)
		}
	}
	c.mu.Unlock()
	return out, nil
}

// ListContainers lists every container the machine's Docker or Podman daemon
// has, running or not, for a person choosing which to watch. Detail is its
// state and image.
func ListContainers(ctx context.Context) ([]model.InventoryItem, error) {
	client, err := newDockerClient()
	if err != nil {
		return nil, err
	}
	defer client.close()
	var list []dockerListEntry
	if err := client.get(ctx, "/containers/json?all=1", &list); err != nil {
		return nil, err
	}
	return containerInventory(list), nil
}

// containerInventory turns the daemon's list into one entry per container.
func containerInventory(list []dockerListEntry) []model.InventoryItem {
	out := make([]model.InventoryItem, 0, len(list))
	for _, e := range list {
		name := e.name()
		if name == "" {
			continue
		}
		detail := e.State
		if detail == "" {
			detail = e.Status
		}
		if e.Image != "" {
			detail += " · " + e.Image
		}
		out = append(out, model.InventoryItem{Name: name, Detail: detail})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

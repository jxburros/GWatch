package sysmetrics

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// Answers recorded from Docker Engine 27 (cgroup v2), cut down to a few
// containers and to the fields that matter, plus a little of what is ignored
// so the decoder is shown to skip it.

const dockerListFixture = `[
 {"Id":"8dfafdbc3a40a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a","Names":["/web"],"Image":"nginx:1.27","State":"running","Status":"Up 3 hours (healthy)","Labels":{"com.docker.compose.project":"site"}},
 {"Id":"1111111111112222222222223333333333334444444444445555555555556666","Names":["/db"],"Image":"postgres:16","State":"exited","Status":"Exited (137) 5 minutes ago"},
 {"Id":"aaaaaaaaaaaabbbbbbbbbbbbccccccccccccddddddddddddeeeeeeeeeeeeffff","Names":["/worker"],"Image":"site/worker:latest","State":"restarting","Status":"Restarting (1) 4 seconds ago"},
 {"Id":"9999999999998888888888887777777777776666666666665555555555554444","Names":["/site-api-1","/web/api"],"Image":"site/api","State":"running","Status":"Up 2 minutes (unhealthy)"}
]`

const dockerInspectWeb = `{
 "Id":"8dfafdbc3a40a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a",
 "Name":"/web","RestartCount":0,
 "State":{"Status":"running","Running":true,"Paused":false,"Restarting":false,"OOMKilled":false,"Dead":false,"Pid":4242,"ExitCode":0,"StartedAt":"2026-10-26T07:12:03.123456789Z","FinishedAt":"0001-01-01T00:00:00Z",
  "Health":{"Status":"healthy","FailingStreak":0,"Log":[]}},
 "HostConfig":{"NanoCpus":2000000000,"CpuQuota":0,"CpuPeriod":0,"Memory":536870912,"RestartPolicy":{"Name":"unless-stopped"}}
}`

const dockerInspectDB = `{
 "Id":"1111111111112222222222223333333333334444444444445555555555556666",
 "Name":"/db","RestartCount":0,
 "State":{"Status":"exited","Running":false,"ExitCode":137,"StartedAt":"2026-10-25T21:00:00Z","FinishedAt":"2026-10-26T10:07:00Z"},
 "HostConfig":{"NanoCpus":0,"Memory":0}
}`

const dockerInspectWorkerTemplate = `{
 "Id":"aaaaaaaaaaaabbbbbbbbbbbbccccccccccccddddddddddddeeeeeeeeeeeeffff",
 "Name":"/worker","RestartCount":RESTARTS,
 "State":{"Status":"restarting","Running":true,"Restarting":true,"ExitCode":1,"StartedAt":"2026-10-26T10:11:58Z"},
 "HostConfig":{"CpuQuota":50000,"CpuPeriod":100000}
}`

const dockerInspectAPI = `{
 "Id":"9999999999998888888888887777777777776666666666665555555555554444",
 "Name":"/site-api-1","RestartCount":0,
 "State":{"Status":"running","Running":true,"ExitCode":0,"StartedAt":"2026-10-26T10:10:00Z","Health":{"Status":"unhealthy","FailingStreak":4}},
 "HostConfig":{}
}`

// dockerStatsWeb is GET /stats?stream=false&one-shot=true for web: processor
// counters (one-shot leaves precpu_stats empty) and cgroup v2 memory.
func dockerStatsWeb(total, system uint64) string {
	b, _ := json.Marshal(map[string]any{
		"read":         "2026-10-26T10:12:00Z",
		"precpu_stats": map[string]any{},
		"cpu_stats": map[string]any{
			"cpu_usage":        map[string]any{"total_usage": total, "usage_in_kernelmode": 1, "usage_in_usermode": 2},
			"system_cpu_usage": system, "online_cpus": 8,
		},
		"memory_stats": map[string]any{
			"usage": 300 << 20, "limit": 512 << 20,
			"stats": map[string]any{"inactive_file": 44 << 20, "active_file": 10 << 20, "anon": 200 << 20},
		},
	})
	return string(b)
}

// fakeDocker serves the recorded answers on a Unix socket and points
// dockerSocket at it. restarts is the worker's restart count, which a test
// moves on between readings.
type fakeDocker struct {
	mu       sync.Mutex
	restarts int
	webCPU   [2]uint64
	paths    []string
}

func startFakeDocker(t *testing.T) *fakeDocker {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake daemon listens on a Unix socket")
	}
	// Socket paths are limited to about a hundred bytes, which a test's
	// temporary directory can exceed.
	dir, err := os.MkdirTemp("", "gwd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("cannot listen on a Unix socket here: %v", err)
	}
	f := &fakeDocker{webCPU: [2]uint64{10_000_000_000, 1_000_000_000_000}}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.paths = append(f.paths, r.Method+" "+r.URL.RequestURI())
		if r.Method != http.MethodGet {
			http.Error(w, `{"message":"read only"}`, http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/containers/json":
			w.Write([]byte(dockerListFixture))
		case "/containers/8dfafdbc3a40a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a/json":
			w.Write([]byte(dockerInspectWeb))
		case "/containers/8dfafdbc3a40a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a/stats":
			w.Write([]byte(dockerStatsWeb(f.webCPU[0], f.webCPU[1])))
		case "/containers/1111111111112222222222223333333333334444444444445555555555556666/json":
			w.Write([]byte(dockerInspectDB))
		case "/containers/aaaaaaaaaaaabbbbbbbbbbbbccccccccccccddddddddddddeeeeeeeeeeeeffff/json":
			w.Write([]byte(strings.Replace(dockerInspectWorkerTemplate, "RESTARTS", itoa(f.restarts), 1)))
		case "/containers/9999999999998888888888887777777777776666666666665555555555554444/json":
			w.Write([]byte(dockerInspectAPI))
		default:
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"message":"No such container"}`))
		}
	}))
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)

	old := dockerSocket
	dockerSocket = func() (string, error) { return sock, nil }
	t.Cleanup(func() { dockerSocket = old })
	return f
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// The collector asks the daemon about the watched containers only, reads
// each one's state, health and limits from its inspect record, and works out
// its processor use itself between two readings.
func TestReadDockerFromRecordedAnswers(t *testing.T) {
	f := startFakeDocker(t)
	c := NewCollector("")
	ctx := context.Background()
	names := []string{"web", "db", "worker", "site-api-1", "nosuch"}

	got, err := c.readDocker(ctx, names)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(names) {
		t.Fatalf("one entry per watched name: %+v", got)
	}
	web := got[0]
	if web.Name != "web" || web.ID != "8dfafdbc3a40" || web.Image != "nginx:1.27" || web.State != "running" || web.Health != "healthy" || web.Status != "Up 3 hours (healthy)" {
		t.Errorf("web: %+v", web)
	}
	if web.StartedAt == nil || web.StartedAt.Hour() != 7 {
		t.Errorf("started: %v", web.StartedAt)
	}
	if web.CPULimitCores == nil || *web.CPULimitCores != 2 {
		t.Errorf("a two-core limit: %v", web.CPULimitCores)
	}
	if web.CPUPct != nil {
		t.Errorf("the first reading has nothing to measure a rate against: %v", *web.CPUPct)
	}
	// 300 MiB charged, 44 MiB of it cache the kernel would drop, against the
	// container's own 512 MiB limit.
	if web.MemoryUsedBytes != 256<<20 || web.MemoryLimitBytes != 512<<20 || web.MemoryPct == nil || *web.MemoryPct != 50 {
		t.Errorf("memory: %d of %d, %v", web.MemoryUsedBytes, web.MemoryLimitBytes, web.MemoryPct)
	}
	if model.DockerValue(web) != 2 {
		t.Errorf("a healthy running container is 2")
	}

	db := got[1]
	if db.State != "exited" || db.ExitCode != 137 || db.MemoryPct != nil || db.CPUPct != nil || model.DockerValue(db) != 0 {
		t.Errorf("db: %+v", db)
	}
	worker := got[2]
	if worker.State != "restarting" || worker.CPULimitCores == nil || *worker.CPULimitCores != 0.5 || model.DockerValue(worker) != 1 {
		t.Errorf("worker: %+v", worker)
	}
	api := got[3]
	if api.Name != "site-api-1" || api.Health != "unhealthy" || model.DockerValue(api) != 1 {
		t.Errorf("a container known by its first name, unhealthy: %+v", api)
	}
	if got[4].State != "not found" || model.DockerValue(got[4]) != 0 {
		t.Errorf("missing: %+v", got[4])
	}

	// A second reading: web used 1.2 core-seconds while the machine's eight
	// processors ran for 8 core-seconds in all — 1.2 of its 2 cores is 60%.
	f.mu.Lock()
	f.webCPU = [2]uint64{f.webCPU[0] + 1_200_000_000, f.webCPU[1] + 8_000_000_000}
	f.restarts = 4
	f.mu.Unlock()
	got, err = c.readDocker(ctx, names)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].CPUPct == nil || *got[0].CPUPct < 59.9 || *got[0].CPUPct > 60.1 {
		t.Errorf("web processor: %v", got[0].CPUPct)
	}
	if got[2].RestartCount != 4 || got[2].RecentRestarts != 4 {
		t.Errorf("four restarts since the last reading: %+v", got[2])
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.paths {
		if !strings.HasPrefix(p, "GET ") {
			t.Errorf("only GET requests: %s", p)
		}
		if strings.Contains(p, "1111111111112222") && strings.Contains(p, "/stats") {
			t.Errorf("a stopped container has no statistics to ask for: %s", p)
		}
	}
}

// No daemon is a warning on the reading, not a list of stopped containers.
func TestReadDockerWithoutADaemon(t *testing.T) {
	old := dockerSocket
	dockerSocket = func() (string, error) { return "", errDockerNoSocket }
	defer func() { dockerSocket = old }()
	var m model.HostMetrics
	warnings := NewCollector("").readWatched(context.Background(), &m, watchLists{containers: []string{"web"}})
	if m.Docker != nil || len(warnings) != 1 || !strings.Contains(warnings[0], "containers: no Docker or Podman socket") {
		t.Errorf("no daemon: %+v %q", m.Docker, warnings)
	}
}

func TestDockerSocketFromDockerHost(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix:///srv/podman.sock")
	if got, err := dockerSocket(); err != nil || got != "/srv/podman.sock" {
		t.Errorf("got %q %v", got, err)
	}
	t.Setenv("DOCKER_HOST", "tcp://10.0.0.5:2375")
	if _, err := dockerSocket(); err == nil || !strings.Contains(err.Error(), "only a local daemon") {
		t.Errorf("a remote daemon is not read: %v", err)
	}
}

func TestListContainersFromRecordedAnswer(t *testing.T) {
	startFakeDocker(t)
	got, err := ListContainers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"db · exited · postgres:16", "site-api-1 · running · site/api", "web · running · nginx:1.27", "worker · restarting · site/worker:latest"}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i, it := range got {
		if s := it.Name + " · " + it.Detail; s != want[i] || it.Auto {
			t.Errorf("%d: %q", i, s)
		}
	}
}

// Restarts are counted over the last hour from what this collector has
// seen, and start again when the container is started by hand (the count
// goes down) or replaced (a new id under the same name).
func TestRecentRestarts(t *testing.T) {
	t0 := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	var marks []dockerRestartMark
	var n int
	step := func(at time.Duration, id string, count int) int {
		n, marks = recentRestarts(marks, t0.Add(at), id, count)
		return n
	}
	if got := step(0, "a", 7); got != 0 {
		t.Errorf("the first reading counts nothing: %d", got)
	}
	if got := step(10*time.Minute, "a", 9); got != 2 {
		t.Errorf("two since: %d", got)
	}
	if got := step(50*time.Minute, "a", 10); got != 3 {
		t.Errorf("three within the hour: %d", got)
	}
	if got := step(70*time.Minute, "a", 10); got != 1 {
		t.Errorf("the first two are more than an hour old: %d", got)
	}
	if got := step(200*time.Minute, "a", 10); got != 0 {
		t.Errorf("nothing for hours: %d", got)
	}
	if len(marks) > 3 {
		t.Errorf("old marks are dropped: %d kept", len(marks))
	}
	if got := step(201*time.Minute, "a", 0); got != 0 {
		t.Errorf("started by hand: %d", got)
	}
	if got := step(202*time.Minute, "b", 1); got != 0 {
		t.Errorf("a new container under the name: %d", got)
	}
}

// A selection keeps only the containers and guests it names.
func TestApplyKeepsWatchedContainersAndVMs(t *testing.T) {
	m := model.HostMetrics{
		Docker: []model.HostDockerContainer{{Name: "web", State: "running"}, {Name: "db", State: "exited"}},
		VMs:    []model.HostVM{{Name: "dc01", State: "running"}},
	}
	all := Apply(m, model.CollectSelection{Metrics: []string{model.MetricCPU}}, true)
	if len(all.Docker) != 2 || len(all.VMs) != 1 {
		t.Errorf("a family selection must not drop watched names: %+v", all)
	}
	some := Apply(m, model.CollectSelection{Containers: []string{"db"}, VMs: []string{"other"}}, true)
	if len(some.Docker) != 1 || some.Docker[0].Name != "db" || len(some.VMs) != 0 {
		t.Errorf("got %+v", some)
	}
}

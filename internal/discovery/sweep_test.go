package discovery

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeNetwork stands in for a house full of devices: which addresses answer a
// ping, what they are called, and which of their ports accept a connection.
// Installing one replaces the package's three points of contact with the
// network for the length of one test, so nothing here sends a packet.
type fakeNetwork struct {
	mu sync.Mutex

	up    map[string]time.Duration // address -> round trip
	names map[string]string        // address -> reverse DNS name
	open  map[string][]int         // address -> ports that accept

	// dropFirst names the addresses whose first ping goes unanswered, so the
	// retry can be tested; pinged counts every attempt.
	dropFirst map[string]bool
	pinged    map[string]int

	// block holds every ping until it is closed, so a test can watch a run
	// that is genuinely still going. holdIf does the same for the addresses it
	// picks out, and lets go only when the run is cancelled — which is how a
	// test arranges for exactly the devices it named to have answered by the
	// time it pulls the plug.
	block  chan struct{}
	holdIf func(ip string) bool
}

func newFakeNetwork() *fakeNetwork {
	return &fakeNetwork{
		up:        map[string]time.Duration{},
		names:     map[string]string{},
		open:      map[string][]int{},
		dropFirst: map[string]bool{},
		pinged:    map[string]int{},
	}
}

func (f *fakeNetwork) device(ip, name string, rtt time.Duration, ports ...int) *fakeNetwork {
	f.up[ip] = rtt
	if name != "" {
		f.names[ip] = name
	}
	f.open[ip] = ports
	return f
}

// install puts the fake in place for the rest of the test.
func (f *fakeNetwork) install(t *testing.T) {
	t.Helper()
	realPing, realLookup, realDial := pingHost, lookupAddr, dialPort
	t.Cleanup(func() { pingHost, lookupAddr, dialPort = realPing, realLookup, realDial })

	pingHost = func(ctx context.Context, host string, count int, timeout time.Duration) (int, int, []time.Duration, error) {
		if f.block != nil {
			select {
			case <-f.block:
			case <-ctx.Done():
				return count, 0, nil, ctx.Err()
			}
		}
		if f.holdIf != nil && f.holdIf(host) {
			<-ctx.Done()
			return count, 0, nil, ctx.Err()
		}
		f.mu.Lock()
		f.pinged[host]++
		attempt := f.pinged[host]
		rtt, up := f.up[host]
		drop := f.dropFirst[host] && attempt == 1
		f.mu.Unlock()
		if !up || drop {
			return count, 0, nil, nil
		}
		return count, count, []time.Duration{rtt}, nil
	}
	lookupAddr = func(ctx context.Context, addr string) ([]string, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if name, ok := f.names[addr]; ok {
			return []string{name + "."}, nil
		}
		return nil, fmt.Errorf("no such host")
	}
	dialPort = func(ctx context.Context, addr string) error {
		host, port, _ := net.SplitHostPort(addr)
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, p := range f.open[host] {
			if fmt.Sprint(p) == port {
				return nil
			}
		}
		return fmt.Errorf("connection refused")
	}
}

func (f *fakeNetwork) attempts(ip string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pinged[ip]
}

// waitFor polls until the condition holds, so a test never depends on how long
// a goroutine takes to get going.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestSweepFindsAndDescribesResponders(t *testing.T) {
	lan := newFakeNetwork()
	lan.device("192.168.1.1", "gateway.lan", 2*time.Millisecond, 80, 443)
	lan.device("192.168.1.20", "nas.lan", 1500*time.Microsecond, 22, 80)
	lan.device("192.168.1.50", "", 4*time.Millisecond, 9100)
	lan.install(t)

	reg := &Registry{}
	job, err := reg.Start(Request{Ranges: []string{"192.168.1.1-60"}, Workers: 8}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != StateRunning || job.Total != 60 {
		t.Fatalf("job at the start: %+v", job)
	}
	waitFor(t, "the sweep to finish", func() bool { j, _ := reg.Get(job.ID); return j.Done() })

	done, _ := reg.Get(job.ID)
	if done.State != StateDone {
		t.Fatalf("state %q, want %q (%s)", done.State, StateDone, done.Error)
	}
	if done.Scanned != done.Total || done.Responders != 3 || len(done.Results) != 3 {
		t.Fatalf("scanned %d/%d, %d responders: %+v", done.Scanned, done.Total, done.Responders, done.Results)
	}
	if done.FinishedAt == nil {
		t.Error("a finished run should carry a finish time")
	}

	// The results are in address order, named, timed, and each carries the
	// template its open ports imply.
	want := []struct {
		ip, host, template string
		ports              []int
	}{
		{"192.168.1.1", "gateway.lan", "router", []int{80, 443}},
		{"192.168.1.20", "nas.lan", "home-server", []int{22, 80}},
		{"192.168.1.50", "", "tcp-service", []int{9100}},
	}
	for i, w := range want {
		got := done.Results[i]
		if got.IP != w.ip || got.Hostname != w.host || got.Template != w.template {
			t.Errorf("result %d: %+v, want %s/%s/%s", i, got, w.ip, w.host, w.template)
		}
		if fmt.Sprint(got.OpenPorts) != fmt.Sprint(w.ports) {
			t.Errorf("result %d: open ports %v, want %v", i, got.OpenPorts, w.ports)
		}
		if got.RTTMS <= 0 {
			t.Errorf("result %d: round trip %v, want something above zero", i, got.RTTMS)
		}
	}
	// A responder with no name is called by its address.
	if n := done.Results[2].Name(); n != "192.168.1.50" {
		t.Errorf("an unnamed device should be called %q, got %q", "192.168.1.50", n)
	}
	if got := reg.Running(); got {
		t.Error("the registry still thinks a run is in flight")
	}
}

// One lost packet on a busy wireless network is ordinary, so a silent first
// ping earns a second one before the device is written off.
func TestSweepRetriesOnceBeforeGivingUp(t *testing.T) {
	lan := newFakeNetwork()
	lan.device("10.0.0.2", "flaky.lan", 3*time.Millisecond)
	lan.dropFirst["10.0.0.2"] = true
	lan.install(t)

	reg := &Registry{}
	job, err := reg.Start(Request{Ranges: []string{"10.0.0.1-3"}, Workers: 3}, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the sweep to finish", func() bool { j, _ := reg.Get(job.ID); return j.Done() })

	done, _ := reg.Get(job.ID)
	if done.Responders != 1 || done.Results[0].IP != "10.0.0.2" {
		t.Fatalf("the retry did not find the flaky device: %+v", done.Results)
	}
	if got := lan.attempts("10.0.0.2"); got != 2 {
		t.Errorf("the flaky address was pinged %d times, want 2", got)
	}
	if got := lan.attempts("10.0.0.1"); got != 2 {
		t.Errorf("a silent address should be tried twice, got %d", got)
	}
}

func TestProgressIsReportedAndEndsExact(t *testing.T) {
	lan := newFakeNetwork()
	lan.device("10.0.0.5", "one.lan", time.Millisecond, 22)
	lan.install(t)

	var mu sync.Mutex
	var seen []Job
	reg := &Registry{}
	job, err := reg.Start(Request{Ranges: []string{"10.0.0.1-10"}, Workers: 2}, func(j Job) {
		mu.Lock()
		seen = append(seen, j)
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the sweep to finish", func() bool { j, _ := reg.Get(job.ID); return j.Done() })
	waitFor(t, "the final notification", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(seen) > 0 && seen[len(seen)-1].Done()
	})

	mu.Lock()
	defer mu.Unlock()
	last := seen[len(seen)-1]
	if last.State != StateDone || last.Scanned != 10 || last.Total != 10 || last.Responders != 1 {
		t.Fatalf("the last notification was %+v", last)
	}
	for i, j := range seen {
		if j.ID != job.ID {
			t.Fatalf("notification %d was for another run", i)
		}
		if j.Scanned > j.Total {
			t.Fatalf("notification %d scanned %d of %d", i, j.Scanned, j.Total)
		}
	}
	if want := "10 addresses in 10.0.0.1-10: 1 responded"; last.Summary() != want {
		t.Errorf("summary %q, want %q", last.Summary(), want)
	}
}

func TestOnlyOneRunAtATime(t *testing.T) {
	lan := newFakeNetwork()
	lan.block = make(chan struct{})
	lan.device("10.0.0.1", "held.lan", time.Millisecond)
	lan.install(t)

	reg := &Registry{}
	first, err := reg.Start(Request{Ranges: []string{"10.0.0.1-8"}, Workers: 2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the first run to get going", reg.Running)

	if _, err := reg.Start(Request{Ranges: []string{"10.0.1.0/30"}}, nil); err != ErrBusy {
		t.Fatalf("a second run was allowed to start: %v", err)
	}
	// The run that is going is still the one a reopened modal reattaches to.
	latest, err := reg.Latest()
	if err != nil || latest.ID != first.ID {
		t.Fatalf("Latest() = %+v, %v; want the run in flight", latest, err)
	}

	close(lan.block)
	waitFor(t, "the first run to finish", func() bool { j, _ := reg.Get(first.ID); return j.Done() })

	// And once it has, another one may start.
	second, err := reg.Start(Request{Ranges: []string{"10.0.1.0/30"}}, nil)
	if err != nil {
		t.Fatalf("a second run should be allowed once the first finished: %v", err)
	}
	waitFor(t, "the second run to finish", func() bool { j, _ := reg.Get(second.ID); return j.Done() })
	// The registry keeps one run, so the first one's id is gone.
	if _, err := reg.Get(first.ID); err != ErrNotFound {
		t.Errorf("the superseded run should no longer be found, got %v", err)
	}
}

func TestCancelStopsTheSweepAndKeepsWhatItFound(t *testing.T) {
	lan := newFakeNetwork()
	lan.device("10.0.0.1", "one.lan", time.Millisecond, 22)
	lan.device("10.0.0.2", "two.lan", time.Millisecond, 80)
	// Only the two devices answer at all; every other address in the /24 hangs
	// until the run is stopped, which is what a sweep of a quiet network looks
	// like from the inside.
	lan.holdIf = func(ip string) bool { return ip != "10.0.0.1" && ip != "10.0.0.2" }
	lan.install(t)

	reg := &Registry{}
	job, err := reg.Start(Request{Ranges: []string{"10.0.0.0/24"}, Workers: 2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the two devices to answer", func() bool {
		return lan.attempts("10.0.0.1") > 0 && lan.attempts("10.0.0.2") > 0
	})

	cancelled, err := reg.Cancel(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.State != StateCancelled {
		t.Fatalf("state after cancelling: %q", cancelled.State)
	}
	if cancelled.FinishedAt == nil {
		t.Error("a cancelled run should carry a finish time too")
	}
	if cancelled.Scanned >= cancelled.Total {
		t.Errorf("a cancelled run should not claim to have scanned all %d addresses (it says %d)", cancelled.Total, cancelled.Scanned)
	}
	if len(cancelled.Results) != cancelled.Responders {
		t.Errorf("%d results but %d responders", len(cancelled.Results), cancelled.Responders)
	}
	// Whatever answered before the stop is kept — those devices are there.
	for _, r := range cancelled.Results {
		if r.IP != "10.0.0.1" && r.IP != "10.0.0.2" {
			t.Errorf("unexpected responder %s", r.IP)
		}
		if r.Template == "" {
			t.Errorf("responder %s has no template to start from", r.IP)
		}
	}
	if reg.Running() {
		t.Error("the registry still thinks the cancelled run is in flight")
	}
	// Cancelling again is not an error: the caller asked for it stopped, and
	// it is stopped.
	if _, err := reg.Cancel(job.ID); err != nil {
		t.Errorf("cancelling a finished run: %v", err)
	}
	if _, err := reg.Cancel("not-an-id"); err != ErrNotFound {
		t.Errorf("cancelling an unknown run: %v", err)
	}
}

func TestStartRefusesBadInputBeforeAnythingRuns(t *testing.T) {
	newFakeNetwork().install(t)
	reg := &Registry{}
	for _, c := range []struct {
		name string
		req  Request
		says string
	}{
		{"no ranges", Request{}, "at least one range"},
		{"IPv6", Request{Ranges: []string{"2001:db8::/64"}}, "IPv6"},
		{"too many addresses", Request{Ranges: []string{"10.0.0.0/8"}}, "limit for one run"},
		{"a port that is not one", Request{Ranges: []string{"10.0.0.1"}, Ports: []int{0}}, "not a port number"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := reg.Start(c.req, nil); err == nil || !strings.Contains(err.Error(), c.says) {
				t.Fatalf("Start(%+v) = %v, want a refusal mentioning %q", c.req, err, c.says)
			}
			if reg.Running() {
				t.Fatal("a refused request started a run anyway")
			}
		})
	}
	if _, err := reg.Latest(); err != ErrNotFound {
		t.Errorf("nothing has run, so Latest() should say so: %v", err)
	}
}

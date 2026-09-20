package api

import (
	"context"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/discovery"
	"github.com/jxburros/GWatch/internal/model"
)

// jobDoc mirrors what the discovery routes return. It is spelled out here
// rather than reusing discovery.Job so the JSON contract the web interface
// reads is actually asserted, field name by field name.
type jobDoc struct {
	ID         string `json:"id"`
	Ranges     []string
	Ports      []int
	State      string
	Total      int
	Scanned    int
	Responders int
	Results    []struct {
		IP        string  `json:"ip"`
		Hostname  string  `json:"hostname"`
		RTTMS     float64 `json:"rttMs"`
		OpenPorts []int   `json:"openPorts"`
		Template  string  `json:"template"`
		Note      string  `json:"note"`
	}
	Error string
}

type addDoc struct {
	Created []nodeDoc `json:"created"`
	Skipped []struct {
		IP     string `json:"ip"`
		Reason string `json:"reason"`
	} `json:"skipped"`
}

// fakeSweep answers with a fixed set of devices, reporting progress on the way
// so the stream side is exercised too. Nothing here touches the network.
func fakeSweep(found ...discovery.Responder) discovery.SweepFunc {
	return func(ctx context.Context, addrs []netip.Addr, ports []int, workers int, report func(discovery.Progress)) []discovery.Responder {
		for i := range addrs {
			if ctx.Err() != nil {
				break
			}
			if report != nil {
				report(discovery.Progress{Scanned: i + 1, Total: len(addrs), Responders: len(found)})
			}
		}
		return found
	}
}

// heldSweep never finishes until the run is cancelled, so a test can look at a
// sweep that is genuinely still going.
func heldSweep() discovery.SweepFunc {
	return func(ctx context.Context, addrs []netip.Addr, ports []int, workers int, report func(discovery.Progress)) []discovery.Responder {
		if report != nil {
			report(discovery.Progress{Scanned: 1, Total: len(addrs)})
		}
		<-ctx.Done()
		return nil
	}
}

func waitForJob(t *testing.T, srv *Server, id string, want string) jobDoc {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, err := srv.discovery().Get(id)
		if err == nil && job.State == want {
			return jobDoc{ID: job.ID, State: job.State, Scanned: job.Scanned, Total: job.Total, Responders: job.Responders}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("discovery run %s never reached %q", id, want)
	return jobDoc{}
}

func TestDiscoveryRunAndAddNodes(t *testing.T) {
	ts, srv := newTestServer(t)
	srv.discovery().Sweep = fakeSweep(
		discovery.Responder{IP: "192.168.1.20", Hostname: "nas.lan", RTTMS: 1.4, OpenPorts: []int{22, 80}, Template: "home-server", Note: "SSH and a web interface"},
		discovery.Responder{IP: "192.168.1.50", RTTMS: 4.2, OpenPorts: []int{9100}, Template: "tcp-service", Note: "printer"},
		discovery.Responder{IP: "192.168.1.1", Hostname: "gateway.lan", RTTMS: 2, OpenPorts: []int{80, 443}, Template: "router"},
	)

	// Nothing has been swept yet, so there is no latest run to reattach to.
	if code, _, _ := as(t, ts, creds{}, "GET", "/api/discovery", nil, nil); code != 404 {
		t.Fatalf("latest run before any run: %d", code)
	}

	var started jobDoc
	code, body, _ := as(t, ts, creds{}, "POST", "/api/discovery", map[string]any{"ranges": []string{"192.168.1.0/24"}}, &started)
	if code != 202 {
		t.Fatalf("start: %d %s", code, body)
	}
	if started.ID == "" || started.Total != 254 || len(started.Ports) == 0 {
		t.Fatalf("started: %+v", started)
	}
	if len(started.Ports) != len(discovery.DefaultPorts) {
		t.Fatalf("no ports field should mean the default set, got %v", started.Ports)
	}
	if started.Ranges[0] != "192.168.1.0/24" {
		t.Fatalf("the run should remember what it was asked for: %+v", started.Ranges)
	}
	waitForJob(t, srv, started.ID, "done")

	var done jobDoc
	if code, body, _ := as(t, ts, creds{}, "GET", "/api/discovery/"+started.ID, nil, &done); code != 200 {
		t.Fatalf("get: %d %s", code, body)
	}
	if done.State != "done" || done.Scanned != 254 || done.Responders != 3 || len(done.Results) != 3 {
		t.Fatalf("finished run: %+v", done)
	}
	if done.Results[0].IP != "192.168.1.20" || done.Results[0].Hostname != "nas.lan" || done.Results[0].Template != "home-server" {
		t.Fatalf("first result: %+v", done.Results[0])
	}

	// The latest run is the one just finished, so a reopened modal finds it.
	var latest jobDoc
	if code, _, _ := as(t, ts, creds{}, "GET", "/api/discovery", nil, &latest); code != 200 || latest.ID != started.ID {
		t.Fatalf("latest: %d %+v", code, latest)
	}

	// Something already watched, to prove a second copy is not created.
	existing := map[string]any{"name": "Gateway", "host": "192.168.1.1", "enabled": true,
		"checks": []map[string]any{{"type": "ping", "name": "Ping", "enabled": true, "intervalSeconds": 60, "timeoutSeconds": 5}}}
	if code, body, _ := as(t, ts, creds{}, "POST", "/api/nodes", existing, nil); code != 201 {
		t.Fatalf("seed node: %d %s", code, body)
	}

	var added addDoc
	add := map[string]any{
		"items": []map[string]any{
			{"ip": "192.168.1.20"},                           // name and template from the sweep
			{"ip": "192.168.1.50", "name": "Office printer"}, // named by hand
			{"ip": "192.168.1.1"},                            // already monitored
			{"ip": "192.168.1.99", "template": "ping"},       // never answered
		},
		"group":  "Home",
		"groups": []string{"Home"},
	}
	if code, body, _ := as(t, ts, creds{}, "POST", "/api/discovery/"+started.ID+"/add", add, &added); code != 201 {
		t.Fatalf("add: %d %s", code, body)
	}
	if len(added.Created) != 2 || len(added.Skipped) != 2 {
		t.Fatalf("created %d, skipped %d: %+v", len(added.Created), len(added.Skipped), added)
	}

	byHost := map[string]nodeDoc{}
	for _, n := range added.Created {
		byHost[n.Host] = n
	}
	nas, ok := byHost["192.168.1.20"]
	if !ok {
		t.Fatalf("the NAS was not created: %+v", added.Created)
	}
	if nas.Name != "nas.lan" {
		t.Errorf("a responder with a name should be called by it, got %q", nas.Name)
	}
	if nas.Template != "home-server" || len(nas.Checks) != 3 {
		t.Errorf("the suggested template should bring its checks: %q, %d checks", nas.Template, len(nas.Checks))
	}
	if nas.Group != "Home" {
		t.Errorf("group %q, want %q", nas.Group, "Home")
	}
	for _, c := range nas.Checks {
		if c.NodeID != nas.ID || c.ID == 0 {
			t.Errorf("check %q was not attached to the node: %+v", c.Name, c)
		}
	}
	printer, ok := byHost["192.168.1.50"]
	if !ok {
		t.Fatalf("the printer was not created: %+v", added.Created)
	}
	if printer.Name != "Office printer" || printer.Template != "tcp-service" {
		t.Errorf("printer: %+v", printer)
	}

	reasons := map[string]string{}
	for _, s := range added.Skipped {
		reasons[s.IP] = s.Reason
	}
	if !strings.Contains(reasons["192.168.1.1"], "already monitored") {
		t.Errorf("the gateway should be skipped as already monitored, got %q", reasons["192.168.1.1"])
	}
	if !strings.Contains(reasons["192.168.1.99"], "not one of the run's responders") {
		t.Errorf("an address the run never saw should be refused, got %q", reasons["192.168.1.99"])
	}

	// The timeline records the sweep and the nodes that came out of it.
	var events []model.Event
	as(t, ts, creds{}, "GET", "/api/events?type=discovery", nil, &events)
	var sawScan, sawAdd bool
	for _, e := range events {
		if strings.HasPrefix(e.Title, "Discovery scanned 254 addresses in 192.168.1.0/24: 3 responded") {
			sawScan = true
		}
		if e.Title == "Added 2 nodes from discovery" {
			sawAdd = true
		}
	}
	if !sawScan || !sawAdd {
		t.Fatalf("timeline entries: scan=%v add=%v %+v", sawScan, sawAdd, events)
	}
}

func TestDiscoveryRefusesASecondRunAndCanBeCancelled(t *testing.T) {
	ts, srv := newTestServer(t)
	srv.discovery().Sweep = heldSweep()

	var first jobDoc
	if code, body, _ := as(t, ts, creds{}, "POST", "/api/discovery", map[string]any{"ranges": []string{"10.0.0.0/24"}}, &first); code != 202 {
		t.Fatalf("start: %d %s", code, body)
	}
	code, body, _ := as(t, ts, creds{}, "POST", "/api/discovery", map[string]any{"ranges": []string{"10.0.1.0/24"}}, nil)
	if code != 409 {
		t.Fatalf("a second run should be refused with 409, got %d %s", code, body)
	}
	if !strings.Contains(body, "already going") {
		t.Errorf("the refusal should say why: %s", body)
	}

	var cancelled jobDoc
	if code, body, _ := as(t, ts, creds{}, "POST", "/api/discovery/"+first.ID+"/cancel", nil, &cancelled); code != 200 {
		t.Fatalf("cancel: %d %s", code, body)
	}
	if cancelled.State != "cancelled" {
		t.Fatalf("state after cancelling: %q", cancelled.State)
	}
	// And once it is stopped, another run may start and finish normally. This
	// one clears the ports field, which means "probe nothing" rather than
	// "use the defaults".
	srv.discovery().Sweep = fakeSweep()
	var second jobDoc
	if code, body, _ := as(t, ts, creds{}, "POST", "/api/discovery", map[string]any{"ranges": []string{"10.0.1.0/30"}, "ports": []int{}}, &second); code != 202 {
		t.Fatalf("a run after a cancelled one: %d %s", code, body)
	}
	if len(second.Ports) != 0 {
		t.Errorf("an empty ports list should probe nothing, got %v", second.Ports)
	}
	waitForJob(t, srv, second.ID, "done")
	// The cancelled run has been superseded: the registry keeps one.
	if code, _, _ := as(t, ts, creds{}, "GET", "/api/discovery/"+first.ID, nil, nil); code != 404 {
		t.Errorf("the superseded run is still readable: %d", code)
	}
}

func TestDiscoveryRefusesBadRanges(t *testing.T) {
	ts, srv := newTestServer(t)
	srv.discovery().Sweep = fakeSweep()

	for _, c := range []struct {
		name string
		body map[string]any
		says string
	}{
		{"no ranges at all", map[string]any{"ranges": []string{}}, "at least one range"},
		{"IPv6", map[string]any{"ranges": []string{"2001:db8::/64"}}, "IPv6"},
		{"far too many addresses", map[string]any{"ranges": []string{"10.0.0.0/8"}}, "limit for one run"},
		{"nonsense", map[string]any{"ranges": []string{"the printer"}}, "not a range GWatch understands"},
		{"a port that is not one", map[string]any{"ranges": []string{"10.0.0.1"}, "ports": []int{99999}}, "not a port number"},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, body, _ := as(t, ts, creds{}, "POST", "/api/discovery", c.body, nil)
			if code != 400 {
				t.Fatalf("got %d %s, want 400", code, body)
			}
			if !strings.Contains(body, c.says) {
				t.Errorf("message %s does not mention %q", body, c.says)
			}
		})
	}
	// None of that started anything.
	if code, _, _ := as(t, ts, creds{}, "GET", "/api/discovery", nil, nil); code != 404 {
		t.Errorf("a refused request left a run behind: %d", code)
	}
	// An id nobody handed out is a 404 on every route that takes one.
	for _, p := range []string{"GET /api/discovery/nope", "POST /api/discovery/nope/cancel", "POST /api/discovery/nope/add"} {
		method, path, _ := strings.Cut(p, " ")
		if code, _, _ := as(t, ts, creds{}, method, path, map[string]any{"items": []any{}}, nil); code != 404 {
			t.Errorf("%s: %d, want 404", p, code)
		}
	}
}

// Discovery is administrator-only and closed to API keys, however wide their
// scope: it pings a few thousand addresses and then creates monitors.
func TestDiscoveryIsAdminOnly(t *testing.T) {
	ts, srv := newTestServer(t)
	allowTestRemote(srv)
	srv.discovery().Sweep = fakeSweep()
	admin := bootstrapAdmin(t, ts, "pat", "correct horse battery")
	viewerPw := "a much longer passphrase"
	if code, body, _ := as(t, ts, creds{Cookie: admin}, "POST", "/api/users", map[string]string{"username": "sam", "password": viewerPw, "role": "viewer"}, nil); code != 201 {
		t.Fatalf("create viewer: %d %s", code, body)
	}
	viewer := login(t, ts, "sam", viewerPw)
	readKey := mintKey(t, ts, admin, "Reader", "read")
	writeKey := mintKey(t, ts, admin, "Writer", "readwrite")

	routes := []struct{ method, path string }{
		{"GET", "/api/discovery"},
		{"POST", "/api/discovery"},
		{"GET", "/api/discovery/x"},
		{"POST", "/api/discovery/x/cancel"},
		{"POST", "/api/discovery/x/add"},
	}
	for _, r := range routes {
		for _, c := range []struct {
			who   string
			creds creds
		}{
			{"a viewer", creds{Cookie: viewer}},
			{"a read-only key", creds{APIKey: readKey, Remote: "192.168.1.30:1"}},
			{"a read-write key", creds{APIKey: writeKey, Remote: "192.168.1.30:1"}},
		} {
			body := map[string]any{"ranges": []string{"10.0.0.1"}}
			code, msg, _ := as(t, ts, c.creds, r.method, r.path, body, nil)
			if code != 403 {
				t.Errorf("%s %s as %s: %d %s, want 403", r.method, r.path, c.who, code, msg)
			}
		}
	}
	// The administrator is not locked out by any of that.
	if code, body, _ := as(t, ts, creds{Cookie: admin}, "POST", "/api/discovery", map[string]any{"ranges": []string{"10.0.0.1"}}, nil); code != 202 {
		t.Fatalf("an administrator should be able to start a run: %d %s", code, body)
	}
}

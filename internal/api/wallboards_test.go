package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/jxburros/GWatch/internal/model"
)

// A display on the network is exactly this: a client from off this machine,
// holding nothing. Everything it may read, it reads because of the address it
// was given.
func onTheNetwork(t *testing.T, ts *httptest.Server, path string) (int, string) {
	t.Helper()
	code, body, _ := as(t, ts, creds{Remote: "192.168.1.60:5000"}, "GET", path, nil, nil)
	return code, body
}

func itoa(id int64) string { return strconv.FormatInt(id, 10) }

// A board made with nothing on it is still worth looking at: the service fills
// in the default arrangement rather than handing back a blank screen.
func TestNewWallboardStartsAsTheDefaultArrangement(t *testing.T) {
	ts, _ := newTestServer(t)

	var created model.Wallboard
	if code := call(t, ts, "POST", "/api/wallboards", map[string]any{"name": "Office screen"}, &created); code != 200 {
		t.Fatalf("create: %d", code)
	}
	if created.Name != "Office screen" || created.ID == 0 {
		t.Fatalf("unexpected board: %+v", created)
	}
	if len(created.Panels) != len(model.DefaultWallboard().Panels) {
		t.Fatalf("a new board should arrive laid out, got %d panels", len(created.Panels))
	}
	if created.Layout.Columns == 0 || created.Layout.RefreshSeconds == 0 {
		t.Fatalf("the layout should be filled in: %+v", created.Layout)
	}
	if created.Share.Enabled || created.Share.Token != "" {
		t.Fatalf("a new board must not be projected: %+v", created.Share)
	}
}

// A layout nobody could read is not saved as given: columns, refresh and scale
// are bounded whatever the caller sends.
func TestWallboardLayoutIsBounded(t *testing.T) {
	ts, _ := newTestServer(t)
	var b model.Wallboard
	call(t, ts, "POST", "/api/wallboards", map[string]any{"name": "b"}, &b)

	var saved model.Wallboard
	call(t, ts, "PUT", "/api/wallboards/"+itoa(b.ID), map[string]any{
		"name":   "b",
		"layout": map[string]any{"columns": 900, "theme": "neon", "scale": 12, "refreshSeconds": 1},
		"panels": []map[string]any{{"type": "headline", "width": 900, "height": 40}},
	}, &saved)

	if saved.Layout.Columns != 24 {
		t.Errorf("columns should be capped, got %d", saved.Layout.Columns)
	}
	if saved.Layout.Theme != "signal" {
		t.Errorf("an unknown theme should fall back, got %q", saved.Layout.Theme)
	}
	if saved.Layout.Scale != 2 {
		t.Errorf("scale should be capped, got %v", saved.Layout.Scale)
	}
	if saved.Layout.RefreshSeconds != model.DefaultWallLayout().RefreshSeconds {
		t.Errorf("a refresh that fast should fall back, got %d", saved.Layout.RefreshSeconds)
	}
	if len(saved.Panels) != 1 || saved.Panels[0].Width != 24 || saved.Panels[0].Height != 4 {
		t.Errorf("a panel should be clamped to the board: %+v", saved.Panels)
	}
	if saved.Panels[0].ID == "" {
		t.Error("a saved panel should be given an id")
	}
}

func TestWallboardRejectsAnUnknownPanel(t *testing.T) {
	ts, _ := newTestServer(t)
	code := call(t, ts, "POST", "/api/wallboards", map[string]any{
		"name":   "b",
		"panels": []map[string]any{{"type": "stock-ticker", "width": 2, "height": 1}},
	}, nil)
	if code != http.StatusBadRequest {
		t.Fatalf("an unknown panel type should be refused, got %d", code)
	}
}

// Projection is the one thing in GWatch that can be read without signing in,
// so the whole of its lifecycle is worth pinning down: off by default, on only
// when asked, reachable only with its own token, and gone when switched off.
func TestWallboardProjection(t *testing.T) {
	ts, srv := newTestServer(t)
	allowTestRemote(srv)
	var b model.Wallboard
	call(t, ts, "POST", "/api/wallboards", map[string]any{"name": "Hall screen"}, &b)
	view := "/api/wallboards/" + itoa(b.ID) + "/view"

	// Not shared: no token is the right token.
	if status, _ := onTheNetwork(t, ts, view); status != http.StatusUnauthorized {
		t.Fatalf("an unshared board must not be readable anonymously, got %d", status)
	}
	// A board that does not exist looks the same from out here as one that is
	// not shared, so ids cannot be probed for.
	unshared, _ := onTheNetwork(t, ts, view)
	missing, _ := onTheNetwork(t, ts, "/api/wallboards/"+itoa(b.ID+1000)+"/view?token=x")
	if missing != unshared {
		t.Fatalf("a missing board answered %d, an unshared one %d", missing, unshared)
	}

	var shared model.Wallboard
	if code := call(t, ts, "POST", "/api/wallboards/"+itoa(b.ID)+"/share", map[string]any{"enabled": true}, &shared); code != 200 {
		t.Fatalf("share: %d", code)
	}
	if !shared.Share.Enabled || shared.Share.Token == "" {
		t.Fatalf("sharing should mint an address: %+v", shared.Share)
	}
	token := shared.Share.Token

	if status, _ := onTheNetwork(t, ts, view+"?token=wrong"); status != http.StatusUnauthorized {
		t.Error("a wrong token must not open the board")
	}
	status, body := onTheNetwork(t, ts, view+"?token="+token)
	if status != 200 {
		t.Fatalf("the board should open at its own address: %d %s", status, body)
	}
	var doc struct {
		Wallboard model.Wallboard `json:"wallboard"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Wallboard.ID != b.ID {
		t.Fatalf("the wrong board came back: %+v", doc.Wallboard)
	}
	// The display was given the token in its address; it is never handed back
	// in the document, where it would end up in a cache or a screenshot.
	if doc.Wallboard.Share.Token != "" {
		t.Error("the view document leaked the share token")
	}
	if strings.Contains(body, token) {
		t.Error("the view document contains the token somewhere it should not")
	}

	// Another board's token opens nothing.
	var other model.Wallboard
	call(t, ts, "POST", "/api/wallboards", map[string]any{"name": "Other"}, &other)
	if status, _ := onTheNetwork(t, ts, "/api/wallboards/"+itoa(other.ID)+"/view?token="+token); status != http.StatusUnauthorized {
		t.Error("a token must only open the board it belongs to")
	}

	// Switching projection off is a revocation, not a pause.
	var off model.Wallboard
	call(t, ts, "POST", "/api/wallboards/"+itoa(b.ID)+"/share", map[string]any{"enabled": false}, &off)
	if off.Share.Enabled || off.Share.Token != "" {
		t.Fatalf("switching off should take the address back: %+v", off.Share)
	}
	if status, _ := onTheNetwork(t, ts, view+"?token="+token); status != http.StatusUnauthorized {
		t.Error("the old address must stop working")
	}
}

// Anything else the same caller tries is still refused: the view route is an
// exception for one board, not a way in.
func TestProjectedBoardGrantsNothingElse(t *testing.T) {
	ts, srv := newTestServer(t)
	allowTestRemote(srv)

	var b model.Wallboard
	call(t, ts, "POST", "/api/wallboards", map[string]any{"name": "Hall"}, &b)
	var shared model.Wallboard
	call(t, ts, "POST", "/api/wallboards/"+itoa(b.ID)+"/share", map[string]any{"enabled": true}, &shared)
	token := shared.Share.Token

	if status, _ := onTheNetwork(t, ts, "/api/wallboards/"+itoa(b.ID)+"/view?token="+token); status != 200 {
		t.Fatalf("the projected board should open from the network, got %d", status)
	}
	for _, path := range []string{"/api/nodes", "/api/overview", "/api/settings", "/api/wallboards", "/api/events"} {
		if status, _ := onTheNetwork(t, ts, path+"?token="+token); status != http.StatusUnauthorized {
			t.Errorf("%s should still need a credential, got %d", path, status)
		}
	}
}

// shareBoard saves a board with the given panels, shares it and returns the
// view document a display on the network receives.
func shareBoard(t *testing.T, ts *httptest.Server, layout map[string]any, panels []map[string]any) string {
	t.Helper()
	var b model.Wallboard
	call(t, ts, "POST", "/api/wallboards", map[string]any{"name": "Board", "layout": layout, "panels": panels}, &b)
	var shared model.Wallboard
	if code := call(t, ts, "POST", "/api/wallboards/"+itoa(b.ID)+"/share", map[string]any{"enabled": true}, &shared); code != 200 {
		t.Fatalf("share: %d", code)
	}
	status, body := onTheNetwork(t, ts, "/api/wallboards/"+itoa(b.ID)+"/view?token="+shared.Share.Token)
	if status != 200 {
		t.Fatalf("view: %d %s", status, body)
	}
	return body
}

// seedTwoNodes adds a node in group "lobby" and an unrelated one whose
// hardware check reads a metrics address carrying a credential.
func seedTwoNodes(t *testing.T, ts *httptest.Server) {
	t.Helper()
	for _, n := range []map[string]any{
		{"name": "Lobby AP", "host": "10.1.1.1", "groups": []string{"lobby"}, "enabled": true, "importance": "normal",
			"checks": []map[string]any{{"type": "ping", "name": "Ping", "enabled": true, "intervalSeconds": 60, "timeoutSeconds": 5}}},
		{"name": "Finance DB", "host": "finance-db.internal", "groups": []string{"finance"}, "enabled": true, "importance": "normal",
			"checks": []map[string]any{{"type": "system", "name": "Hardware", "enabled": true, "intervalSeconds": 60, "timeoutSeconds": 5,
				"config": map[string]any{"hostSource": "url", "metricsUrl": "http://finance-db.internal:9100/metrics?key=MURLSECRET"}}}},
	} {
		if code := call(t, ts, "POST", "/api/nodes", n, nil); code != 201 {
			t.Fatalf("create node %v: %d", n["name"], code)
		}
	}
}

// A board with only a clock on it hands out nothing but the board: no nodes,
// no incidents, no host-health paths or errors.
func TestSharedClockBoardCarriesNoMonitoringData(t *testing.T) {
	ts, srv := newTestServer(t)
	allowTestRemote(srv)
	seedTwoNodes(t, ts)
	body := shareBoard(t, ts, map[string]any{"hideChrome": true}, []map[string]any{{"type": "clock", "width": 2, "height": 1}})
	for _, leak := range []string{"Lobby AP", "Finance DB", "finance-db", "MURLSECRET", "incidents", "databasePath", "dataDir", "keyPath", "recentErrors", "summary", `"health"`} {
		if strings.Contains(body, leak) {
			t.Errorf("a clock-only board carries %q:\n%s", leak, body)
		}
	}
	var doc struct {
		Nodes     []any `json:"nodes"`
		Attention []any `json:"attention"`
		Groups    []any `json:"groups"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Nodes)+len(doc.Attention)+len(doc.Groups) != 0 {
		t.Fatalf("a clock-only board carries monitoring data: %s", body)
	}
}

// A nodes panel for one group receives that group's nodes, by name and
// status only; an unrelated node and its metrics address never leave the
// server. The footer gets the service's running state and nothing else.
func TestSharedNodesBoardCarriesOnlyItsNodes(t *testing.T) {
	ts, srv := newTestServer(t)
	allowTestRemote(srv)
	seedTwoNodes(t, ts)
	body := shareBoard(t, ts, map[string]any{}, []map[string]any{{"type": "nodes", "width": 6, "height": 2, "config": map[string]any{"group": "Lobby"}}})
	for _, leak := range []string{"Finance DB", "finance-db", "MURLSECRET", "metricsUrl", "10.1.1.1", `"checks"`, "databasePath", "dataDir", "recentErrors", "version"} {
		if strings.Contains(body, leak) {
			t.Errorf("a one-group board carries %q:\n%s", leak, body)
		}
	}
	var doc struct {
		Nodes []struct {
			Node struct {
				Name string `json:"name"`
			} `json:"node"`
			Status string `json:"status"`
		} `json:"nodes"`
		Health *struct {
			ServiceRunning bool `json:"serviceRunning"`
		} `json:"health"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Nodes) != 1 || doc.Nodes[0].Node.Name != "Lobby AP" || doc.Nodes[0].Status == "" {
		t.Fatalf("the panel's node should come back with its status: %s", body)
	}
	if doc.Health == nil || !doc.Health.ServiceRunning {
		t.Fatalf("the footer needs the service state: %s", body)
	}
}

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

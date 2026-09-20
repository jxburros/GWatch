package api

import (
	"strings"
	"testing"

	"github.com/jxburros/GWatch/internal/model"
)

// A fresh install has the indicator rules already, so the header says
// something useful before anybody has opened Settings.
func TestIndicatorDefaultsOnFreshSettings(t *testing.T) {
	ts, _ := newTestServer(t)
	var st model.Settings
	if code := call(t, ts, "GET", "/api/settings", nil, &st); code != 200 {
		t.Fatalf("get settings: %d", code)
	}
	if len(st.Indicators) != len(model.DefaultIndicators()) {
		t.Fatalf("expected the default indicators, got %+v", st.Indicators)
	}
	byColour := map[string]int{}
	for _, r := range st.Indicators {
		if r.ID == "" || r.Name == "" || r.Condition.Kind == "" {
			t.Fatalf("incomplete default rule: %+v", r)
		}
		byColour[r.Colour]++
	}
	for _, c := range []string{model.IndicatorRed, model.IndicatorOrange, model.IndicatorYellow} {
		if byColour[c] == 0 {
			t.Errorf("no %s indicator among the defaults", c)
		}
	}
}

// Saving a rule GWatch could never evaluate is refused with the reason,
// rather than stored and silently never lit.
func TestIndicatorValidationOnSave(t *testing.T) {
	ts, srv := newTestServer(t)
	var st model.Settings
	call(t, ts, "GET", "/api/settings", nil, &st)

	bad := st
	bad.Indicators = []model.IndicatorRule{{Name: "Whatever", Enabled: true, Colour: "red", Condition: model.IndicatorCondition{Kind: "phaseOfTheMoon"}}}
	code, body := callBody(t, ts, "PUT", "/api/settings", bad)
	if code != 400 || !strings.Contains(body, "phaseOfTheMoon") {
		t.Fatalf("unknown condition should be refused with a reason, got %d %s", code, body)
	}

	bad.Indicators = []model.IndicatorRule{{Name: "Puce alert", Enabled: true, Colour: "puce", Condition: model.IndicatorCondition{Kind: model.IndicatorServiceHealth}}}
	if code, body := callBody(t, ts, "PUT", "/api/settings", bad); code != 400 || !strings.Contains(body, "yellow") {
		t.Fatalf("unknown colour should be refused, got %d %s", code, body)
	}

	bad.Indicators = []model.IndicatorRule{{Name: "Up is fine", Enabled: true, Colour: "yellow", Condition: model.IndicatorCondition{Kind: model.IndicatorNodesInStatus, Status: "up"}}}
	if code, _ := callBody(t, ts, "PUT", "/api/settings", bad); code != 400 {
		t.Fatalf("an indicator on \"up\" should be refused, got %d", code)
	}

	// A rule that leaves out its id and count is saved with both filled in.
	good := st
	good.Indicators = []model.IndicatorRule{{Name: "Three down", Enabled: true, Colour: "red", Condition: model.IndicatorCondition{Kind: model.IndicatorNodesInStatus, Status: "down"}}}
	var saved model.Settings
	if code := call(t, ts, "PUT", "/api/settings", good, &saved); code != 200 {
		t.Fatalf("save indicators: %d", code)
	}
	if len(saved.Indicators) != 1 || saved.Indicators[0].ID == "" || saved.Indicators[0].Condition.MinCount != 1 {
		t.Fatalf("id and count should be filled in: %+v", saved.Indicators)
	}
	if got := srv.Engine.Settings().Indicators; len(got) != 1 || got[0].Name != "Three down" {
		t.Fatalf("not stored: %+v", got)
	}
}

// A viewer may not read settings but has the same header as everybody else,
// so the effective rules travel with the identity.
func TestIndicatorsReachAViewerThroughMe(t *testing.T) {
	ts, srv := newTestServer(t)
	allowTestRemote(srv)
	admin := bootstrapAdmin(t, ts, "pat", "correct horse battery")
	if code, body, _ := as(t, ts, creds{Cookie: admin}, "POST", "/api/users", map[string]string{"username": "sam", "password": "viewer password", "role": "viewer"}, nil); code != 201 {
		t.Fatalf("create viewer: %d %s", code, body)
	}
	viewer := login(t, ts, "sam", "viewer password")

	// Settings themselves stay closed to them.
	if code, _, _ := as(t, ts, creds{Cookie: viewer}, "GET", "/api/settings", nil, nil); code != 403 {
		t.Fatalf("a viewer should not read settings, got %d", code)
	}

	var me model.Principal
	if code, body, _ := as(t, ts, creds{Cookie: viewer}, "GET", "/api/me", nil, &me); code != 200 {
		t.Fatalf("me: %d %s", code, body)
	}
	if len(me.Indicators) != len(model.DefaultIndicators()) {
		t.Fatalf("a viewer should get the effective rules: %+v", me.Indicators)
	}

	// What the administrator changes is what the viewer then evaluates.
	var st model.Settings
	as(t, ts, creds{Cookie: admin}, "GET", "/api/settings", nil, &st)
	st.Indicators = []model.IndicatorRule{{ID: "only-one", Name: "Anything at all", Enabled: true, Colour: model.IndicatorOrange, Condition: model.IndicatorCondition{Kind: model.IndicatorAttention, MinCount: 2}}}
	if code, body, _ := as(t, ts, creds{Cookie: admin}, "PUT", "/api/settings", st, nil); code != 200 {
		t.Fatalf("save: %d %s", code, body)
	}
	as(t, ts, creds{Cookie: viewer}, "GET", "/api/me", nil, &me)
	if len(me.Indicators) != 1 || me.Indicators[0].ID != "only-one" || me.Indicators[0].Condition.MinCount != 2 {
		t.Fatalf("the viewer did not see the change: %+v", me.Indicators)
	}
}

package api

import (
	"net/http"
	"testing"
)

func TestSessionWriteRejectsSiblingOriginWithoutFetchMetadata(t *testing.T) {
	ts, _ := newTestServer(t)
	cookie := bootstrapAdmin(t, ts, "owner", "long-password")
	for _, origin := range []string{"http://127.0.0.1:1", "https://evil.example"} {
		code, _, _ := as(t, ts, creds{Cookie: cookie, Origin: origin}, "POST", "/api/nodes/999/run", nil, nil)
		if code != http.StatusForbidden {
			t.Fatalf("origin %q: status %d", origin, code)
		}
	}
	code, _, _ := as(t, ts, creds{Cookie: cookie, Origin: ts.URL}, "POST", "/api/nodes/999/run", nil, nil)
	if code == http.StatusForbidden {
		t.Fatal("same-origin request refused")
	}
}

func TestLoginConcurrencyBudget(t *testing.T) {
	ts, s := newTestServer(t)
	for i := 0; i < cap(s.loginSlots); i++ {
		s.loginSlots <- struct{}{}
	}
	for _, name := range []string{"owner", "unknown"} {
		code, _, h := as(t, ts, creds{}, "POST", "/api/auth/login", map[string]string{"username": name, "password": "wrong"}, nil)
		if code != http.StatusTooManyRequests || h.Get("Retry-After") == "" {
			t.Fatalf("busy login: %d %v", code, h)
		}
	}
	for i := 0; i < cap(s.loginSlots); i++ {
		<-s.loginSlots
	}
	code, _, _ := as(t, ts, creds{}, "POST", "/api/auth/login", map[string]string{"username": "unknown", "password": "wrong"}, nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("login after capacity returns: %d", code)
	}
	if len(s.loginSlots) != 0 {
		t.Fatal("verification slot leaked")
	}
}

func TestWeeklyMaintenanceZoneRoundTrip(t *testing.T) {
	ts, _ := newTestServer(t)
	body := map[string]any{"name": "Sunday updates", "enabled": true, "weekdays": []int{0}, "startTime": "22:00", "timeZone": "America/New_York", "durationMinutes": 60}
	var saved map[string]any
	if code := call(t, ts, "POST", "/api/maintenance", body, &saved); code != http.StatusOK {
		t.Fatalf("save: %d", code)
	}
	var list []map[string]any
	if code := call(t, ts, "GET", "/api/maintenance", nil, &list); code != http.StatusOK || len(list) != 1 || list[0]["timeZone"] != body["timeZone"] || list[0]["startTime"] != "22:00" {
		t.Fatalf("round trip: %d %+v", code, list)
	}
	for _, zone := range []string{"", "Local", "Not/AZone"} {
		body["timeZone"] = zone
		if code := call(t, ts, "POST", "/api/maintenance", body, nil); code != http.StatusBadRequest {
			t.Fatalf("accepted zone %q: %d", zone, code)
		}
	}
	body["timeZone"] = "UTC"
	body["startTime"] = "25:00"
	if code := call(t, ts, "POST", "/api/maintenance", body, nil); code != http.StatusBadRequest {
		t.Fatalf("accepted invalid time: %d", code)
	}
}

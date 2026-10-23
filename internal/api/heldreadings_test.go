package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// postHeld submits a reading the way an agent delivers one it held through an
// outage, and returns the status and the time GWatch filed it at.
func postHeld(t *testing.T, url, token string, m model.HostMetrics, held bool) (int, time.Time) {
	t.Helper()
	b, _ := json.Marshal(m)
	req, err := http.NewRequest(http.MethodPost, url+"/ingest/metrics", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	if held {
		req.Header.Set("X-GWatch-Held", "1")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		TS time.Time `json:"ts"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out.TS
}

// Backfilling only means something if a late reading is filed when it was
// taken. A held reading whose time cannot be believed is refused — with a 4xx
// the agent drops rather than retries — instead of being filed as current; an
// unmarked (live) reading with a wrong clock is still filed, at arrival.
func TestHeldReadingsKeepTheirTimeOrAreRefused(t *testing.T) {
	ts, _ := newTestServer(t)
	a := registerAgent(t, ts, "nas")
	now := time.Now()

	twoHours := reading("nas", 10)
	twoHours.Timestamp = now.Add(-2 * time.Hour).Truncate(time.Millisecond)
	code, filed := postHeld(t, ts.URL, a.Token, twoHours, true)
	if code != http.StatusAccepted || !filed.Equal(twoHours.Timestamp) {
		t.Fatalf("a reading held for two hours should be filed at its own time: %d %s (want %s)", code, filed, twoHours.Timestamp)
	}

	for name, at := range map[string]time.Time{
		"older than an agent may hold": now.Add(-72 * time.Hour),
		"from GWatch's future":         now.Add(time.Hour),
		"with no time at all":          {},
	} {
		m := reading("nas", 10)
		m.Timestamp = at
		if code, _ := postHeld(t, ts.URL, a.Token, m, true); code != http.StatusUnprocessableEntity {
			t.Errorf("a held reading %s: want 422, got %d", name, code)
		}
	}

	// The same implausible time on a live reading is a wrong clock on a machine
	// that is reporting now: filed, at arrival.
	live := reading("nas", 10)
	live.Timestamp = now.Add(-72 * time.Hour)
	code, filed = postHeld(t, ts.URL, a.Token, live, false)
	if code != http.StatusAccepted || time.Since(filed) > time.Minute {
		t.Errorf("a live reading with a slow clock should be filed at arrival: %d %s", code, filed)
	}
}

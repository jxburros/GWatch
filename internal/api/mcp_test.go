package api

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/auth"
	"github.com/jxburros/GWatch/internal/model"
)

// fetchRaw performs a GET as the given credentials and returns the status,
// headers and the body as bytes: the skill download is a zip, not JSON.
func fetchRaw(t *testing.T, ts, path string, c creds) (int, http.Header, []byte) {
	t.Helper()
	req, _ := http.NewRequest("GET", ts+path, nil)
	if c.Cookie != "" {
		req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: c.Cookie})
	}
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	if c.Remote != "" {
		req.Header.Set("X-Test-Remote", c.Remote)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, body
}

func TestMCPSkillStatusAndDownload(t *testing.T) {
	ts, srv := newTestServer(t)
	allowTestRemote(srv)
	admin := bootstrapAdmin(t, ts, "pat", "correct horse battery")
	me := creds{Cookie: admin}

	// 1. Before anything is downloaded: the version is known, nothing else is,
	//    and in particular no update is announced — there is nothing to update.
	var st mcpStatus
	if code, body, _ := as(t, ts, me, "GET", "/api/mcp/status", nil, &st); code != 200 {
		t.Fatalf("status: %d %s", code, body)
	}
	if st.SkillVersion != "2.3.4" || st.LastDownloadedVersion != "" || st.LastDownloadedAt != nil || st.LastDownloadedBy != "" || st.UpdateAvailable {
		t.Fatalf("fresh status: %+v", st)
	}

	// 2. The zip unpacks to a gwatch/ folder holding every file of skill/.
	code, hdr, body := fetchRaw(t, ts.URL, "/api/mcp/skill", me)
	if code != 200 {
		t.Fatalf("download: %d %s", code, body)
	}
	if ct := hdr.Get("Content-Type"); ct != "application/zip" {
		t.Errorf("content type %q", ct)
	}
	if cd := hdr.Get("Content-Disposition"); !strings.Contains(cd, `attachment; filename="gwatch-skill-2.3.4.zip"`) {
		t.Errorf("content disposition %q", cd)
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	got := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		got[f.Name] = string(b)
	}
	for _, name := range []string{"gwatch/SKILL.md", "gwatch/README.md", "gwatch/VERSION"} {
		if _, ok := got[name]; !ok {
			t.Errorf("zip is missing %s; has %v", name, got)
		}
	}
	if len(got) != 3 || strings.TrimSpace(got["gwatch/VERSION"]) != "2.3.4" {
		t.Errorf("zip contents: %v", got)
	}

	// 3. The download is remembered, attributed, and audited.
	if code, body, _ := as(t, ts, me, "GET", "/api/mcp/status", nil, &st); code != 200 {
		t.Fatalf("status after download: %d %s", code, body)
	}
	if st.LastDownloadedVersion != "2.3.4" || st.LastDownloadedBy != "pat (admin)" || st.UpdateAvailable {
		t.Fatalf("status after download: %+v", st)
	}
	if st.LastDownloadedAt == nil || time.Since(*st.LastDownloadedAt) > time.Minute {
		t.Fatalf("lastDownloadedAt: %v", st.LastDownloadedAt)
	}
	var stored skillDownload
	if err := srv.Store.GetSetting(t.Context(), skillDownloadKey, &stored); err != nil || stored.Version != "2.3.4" || stored.By != "pat (admin)" {
		t.Fatalf("kv row: %+v %v", stored, err)
	}
	var events []model.Event
	as(t, ts, me, "GET", "/api/events?type=update", nil, &events)
	found := false
	for _, e := range events {
		if e.Title == "Agent skill downloaded: 2.3.4" && e.Actor == "pat (admin)" {
			found = true
		}
	}
	if !found {
		t.Errorf("no audit event for the download; got %+v", events)
	}

	// 4. ?format=md hands out SKILL.md alone, as markdown.
	code, hdr, body = fetchRaw(t, ts.URL, "/api/mcp/skill?format=md", me)
	if code != 200 || !strings.HasPrefix(hdr.Get("Content-Type"), "text/markdown") || !strings.Contains(string(body), "# Working with GWatch") {
		t.Errorf("markdown download: %d %q %q", code, hdr.Get("Content-Type"), body)
	}
	if code, _, body := fetchRaw(t, ts.URL, "/api/mcp/skill?format=pdf", me); code != 400 {
		t.Errorf("unknown format: %d %s", code, body)
	}

	// 5. A newer embedded skill than the one downloaded is an update; the
	//    version is compared against the stored row, not the other way round.
	if err := srv.Store.PutSetting(t.Context(), skillDownloadKey, skillDownload{Version: "2.3.3", At: time.Now(), By: "pat (admin)"}); err != nil {
		t.Fatal(err)
	}
	as(t, ts, me, "GET", "/api/mcp/status", nil, &st)
	if !st.UpdateAvailable || st.LastDownloadedVersion != "2.3.3" {
		t.Fatalf("expected an update to be available: %+v", st)
	}
	// Downloading again clears it.
	fetchRaw(t, ts.URL, "/api/mcp/skill", me)
	as(t, ts, me, "GET", "/api/mcp/status", nil, &st)
	if st.UpdateAvailable || st.LastDownloadedVersion != "2.3.4" {
		t.Fatalf("expected the download to clear the update: %+v", st)
	}
}

// The skill is administrator material: an assistant's own key — even a
// read-write one — cannot fetch it, and neither can a viewer account.
func TestMCPSkillIsAdminOnly(t *testing.T) {
	ts, srv := newTestServer(t)
	allowTestRemote(srv)
	admin := bootstrapAdmin(t, ts, "pat", "correct horse battery")
	for _, scope := range []string{"read", "readwrite"} {
		key := mintKey(t, ts, admin, "assistant "+scope, scope)
		for _, path := range []string{"/api/mcp/status", "/api/mcp/skill"} {
			code, _, body := fetchRaw(t, ts.URL, path, creds{APIKey: key, Remote: "203.0.113.7:9000"})
			if code != 403 || !strings.Contains(string(body), "API keys cannot") {
				t.Errorf("%s key GET %s: %d %s", scope, path, code, body)
			}
		}
	}
	as(t, ts, creds{Cookie: admin}, "POST", "/api/users", map[string]string{"username": "vic", "password": "viewer password", "role": "viewer"}, nil)
	viewer := login(t, ts, "vic", "viewer password")
	if code, _, body := fetchRaw(t, ts.URL, "/api/mcp/skill", creds{Cookie: viewer}); code != 403 {
		t.Errorf("viewer download: %d %s", code, body)
	}
	// And nothing was recorded by the refused attempts.
	if _, ok := srv.lastSkillDownload(t.Context()); ok {
		t.Error("a refused download must not be recorded")
	}
}

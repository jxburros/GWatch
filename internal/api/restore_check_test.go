package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/backup"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
	"github.com/jxburros/GWatch/internal/store/storetest"
)

// #148: a restore no longer trusts an archive completely. Its configuration
// goes through the API's own validation, a preview says what it would change,
// the engine is paused while it is applied, and a failure part-way changes
// nothing.

// craftedArchive writes an encrypted archive holding cfg, the way someone who
// knows the password could, without going through any validation.
func craftedArchive(t *testing.T, dir string, cfg backup.Config) string {
	t.Helper()
	path := filepath.Join(dir, "crafted"+backup.Extension)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc, err := backup.NewEncryptWriter(f, "pw")
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(enc)
	m := backup.Manifest{Format: backup.Format, AppVersion: "test", CreatedAt: time.Now(), Nodes: len(cfg.Nodes), Users: len(cfg.Users), Wallboards: len(cfg.Wallboards)}
	for name, v := range map[string]any{"config.json": cfg, "manifest.json": m} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.NewEncoder(w).Encode(v); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// validConfig is the configuration of a small installation, exported from a
// store of its own so that it is what a real backup would hold.
func validConfig(t *testing.T) backup.Config {
	t.Helper()
	ctx := context.Background()
	src := storetest.Open(t)
	n, err := src.CreateNode(ctx, model.Node{Name: "Restored router", Host: "192.168.50.1", Enabled: true, Importance: model.ImportanceNormal,
		Checks: []model.Check{{Type: model.CheckPing, Name: "Ping", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.SaveTrigger(ctx, model.Trigger{NodeID: n.ID, Name: "Notify", Enabled: true, On: []string{"down"},
		Action: model.Action{Type: model.ActionHTTP, Method: "POST", URL: "https://example.com/hook"}}); err != nil {
		t.Fatal(err)
	}
	cfg, err := backup.ExportConfig(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func postArchive(t *testing.T, url, path string, fields map[string]string) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	mp := multipart.NewWriter(&buf)
	for k, v := range fields {
		mp.WriteField(k, v)
	}
	fw, _ := mp.CreateFormFile("file", filepath.Base(path))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(data)
	mp.Close()
	req, _ := http.NewRequest("POST", url, &buf)
	req.Header.Set("Content-Type", mp.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	return resp.StatusCode, out
}

func nodeNames(t *testing.T, srv *Server) []string {
	t.Helper()
	nodes, err := srv.Store.ListNodes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, n := range nodes {
		out = append(out, n.Name)
	}
	return out
}

func backupFiles(t *testing.T, srv *Server) int {
	t.Helper()
	list, err := backup.List(srv.BackupDir)
	if err != nil {
		t.Fatal(err)
	}
	return len(list)
}

func TestRestoreRefusesAnArchiveThatFailsValidation(t *testing.T) {
	ts, srv := newTestServer(t)
	ctx := context.Background()
	if _, err := srv.Store.CreateNode(ctx, model.Node{Name: "Here before", Host: "10.1.1.1", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	cfg := validConfig(t)
	// A trigger that runs a script the API would refuse (placeholders spliced
	// into a custom interpreter's code without the acknowledgement), an
	// endpoint that is neither protected by a token nor marked as open, and
	// an account with a made-up role.
	cfg.Triggers[0].Action = model.Action{Type: model.ActionScript, Interpreter: "custom", Command: "sh {{file}}", Code: "rm -rf {{node}}"}
	cfg.Endpoints = append(cfg.Endpoints, model.Endpoint{Name: "Backdoor", Slug: "backdoor", Enabled: true, Method: "POST",
		Action: model.Action{Type: model.ActionScript, Interpreter: "sh", Code: "id"}})
	cfg.Users = append(cfg.Users, storeUser("intruder", "superuser"))
	path := craftedArchive(t, t.TempDir(), cfg)

	status, out := postArchive(t, ts.URL+"/api/backups/restore", path, map[string]string{"password": "pw"})
	if status != http.StatusBadRequest {
		t.Fatalf("restore of an invalid archive: %d %v", status, out)
	}
	problems, _ := out["problems"].([]any)
	joined := ""
	for _, p := range problems {
		joined += p.(string) + "\n"
	}
	for _, want := range []string{`trigger "Notify"`, "custom interpreter", `endpoint "Backdoor"`, "a token is required", `account "intruder": unknown role`} {
		if !strings.Contains(joined, want) {
			t.Errorf("problems lack %q:\n%s", want, joined)
		}
	}
	if names := nodeNames(t, srv); len(names) != 1 || names[0] != "Here before" {
		t.Fatalf("a refused restore changed the nodes: %v", names)
	}
	if n := backupFiles(t, srv); n != 0 {
		t.Fatalf("a refused restore took a safety backup (%d files); it is refused before that", n)
	}
	if srv.Engine.Paused() {
		t.Fatal("the engine was left paused")
	}

	// The preview lists the same problems rather than refusing.
	status, out = postArchive(t, ts.URL+"/api/backups/inspect", path, map[string]string{"password": "pw"})
	if status != http.StatusOK {
		t.Fatalf("inspect: %d %v", status, out)
	}
	if p, _ := out["problems"].([]any); len(p) != len(problems) {
		t.Fatalf("the preview's problems %v differ from the restore's %v", p, problems)
	}
}

func storeUser(name, role string) store.UserRecord {
	return store.UserRecord{User: model.User{Username: name, Role: role}, PasswordHash: "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA"}
}

func TestInspectPreviewsWithoutChangingAnything(t *testing.T) {
	ts, srv := newTestServer(t)
	cfg := validConfig(t)
	cfg.Settings.General.RemoteAccess = true
	cfg.Users = append(cfg.Users, storeUser("ops", "admin"))
	cfg.Endpoints = append(cfg.Endpoints, model.Endpoint{Name: "Deploy", Slug: "deploy", Enabled: true, Method: "POST", AllowNoToken: true,
		Action: model.Action{Type: model.ActionScript, Interpreter: "sh", Code: "./deploy.sh"}})
	dir := t.TempDir()
	path := craftedArchive(t, dir, cfg)

	status, out := postArchive(t, ts.URL+"/api/backups/inspect", path, map[string]string{"password": "pw"})
	if status != http.StatusOK {
		t.Fatalf("inspect: %d %v", status, out)
	}
	b, _ := json.Marshal(out)
	var p backup.Preview
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	if p.Counts.Nodes != 1 || p.Counts.Triggers != 1 || p.Counts.Endpoints != 1 || len(p.Problems) != 0 {
		t.Fatalf("preview: %+v", p)
	}
	if len(p.Accounts) != 1 || p.Accounts[0].Username != "ops" || !p.Accounts[0].Admin || !p.AccountsRecreatedAnyway {
		t.Fatalf("accounts: %+v anyway=%v", p.Accounts, p.AccountsRecreatedAnyway)
	}
	if len(p.Commands) != 1 || p.Commands[0].Kind != "endpoint" || p.Commands[0].Detail != "script (sh)" {
		t.Fatalf("commands: %+v", p.Commands)
	}
	if len(p.TokenlessEndpoints) != 1 || p.TokenlessEndpoints[0] != "deploy" {
		t.Fatalf("tokenless endpoints: %v", p.TokenlessEndpoints)
	}
	if len(p.AccessChanges) == 0 || !strings.Contains(p.AccessChanges[0], "LAN access is turned on") {
		t.Fatalf("access changes: %v", p.AccessChanges)
	}
	if len(nodeNames(t, srv)) != 0 || backupFiles(t, srv) != 0 {
		t.Fatal("a preview changed something")
	}

	// The same for an archive in the backup folder; a wrong password is an
	// error, not a preview.
	if err := os.MkdirAll(srv.BackupDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if err := os.WriteFile(filepath.Join(srv.BackupDir, "gwatch-backup-crafted"+backup.Extension), data, 0o600); err != nil {
		t.Fatal(err)
	}
	var again backup.Preview
	if code := call(t, ts, "POST", "/api/backups/inspect-existing", map[string]any{"fileName": "gwatch-backup-crafted" + backup.Extension, "password": "pw"}, &again); code != http.StatusOK || again.Counts.Nodes != 1 {
		t.Fatalf("inspect-existing: %d %+v", code, again)
	}
	if code := call(t, ts, "POST", "/api/backups/inspect-existing", map[string]any{"fileName": "gwatch-backup-crafted" + backup.Extension, "password": "nope"}, nil); code != http.StatusBadRequest {
		t.Fatalf("inspect-existing with the wrong password: %d", code)
	}
}

func TestRestoreFailingPartWayChangesNothingAndResumesTheEngine(t *testing.T) {
	ts, srv := newTestServer(t)
	ctx := context.Background()
	if _, err := srv.Store.CreateNode(ctx, model.Node{Name: "Here before", Host: "10.1.1.1", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	cfg := validConfig(t)
	// Two wallboards with one id: nothing in the validation looks at
	// wallboard ids, so this gets as far as the database, which refuses the
	// second one after the nodes, settings and triggers have been written
	// inside the transaction.
	cfg.Wallboards = []model.Wallboard{{ID: 7, Name: "One", Panels: []model.WallPanel{}}, {ID: 7, Name: "Two", Panels: []model.WallPanel{}}}
	path := craftedArchive(t, t.TempDir(), cfg)

	status, out := postArchive(t, ts.URL+"/api/backups/restore", path, map[string]string{"password": "pw"})
	if status != http.StatusInternalServerError {
		t.Fatalf("restore: %d %v", status, out)
	}
	if msg, _ := out["error"].(string); !strings.Contains(msg, "Nothing was changed") {
		t.Fatalf("the error should say nothing was changed: %q", msg)
	}
	if names := nodeNames(t, srv); len(names) != 1 || names[0] != "Here before" {
		t.Fatalf("a restore that failed part-way changed the nodes: %v", names)
	}
	if list, _ := srv.Store.ListTriggers(ctx, nil); len(list) != 0 {
		t.Fatalf("a restore that failed part-way left triggers: %+v", list)
	}
	if srv.Engine.Paused() {
		t.Fatal("the engine was left paused after a failed restore")
	}
	if backupFiles(t, srv) != 1 {
		t.Fatal("the safety backup should have been taken")
	}
}

func TestRestorePausesAndResumesTheEngine(t *testing.T) {
	ts, srv := newTestServer(t)
	path := craftedArchive(t, t.TempDir(), validConfig(t))

	// While the engine is paused (another restore is running) a restore is
	// refused, before anything is taken or written.
	if err := srv.Engine.Pause(context.Background()); err != nil {
		t.Fatal(err)
	}
	status, out := postArchive(t, ts.URL+"/api/backups/restore", path, map[string]string{"password": "pw"})
	if status != http.StatusConflict {
		t.Fatalf("restore during another: %d %v", status, out)
	}
	if backupFiles(t, srv) != 0 || len(nodeNames(t, srv)) != 0 {
		t.Fatal("a refused restore changed something")
	}
	srv.Engine.Resume()

	status, out = postArchive(t, ts.URL+"/api/backups/restore", path, map[string]string{"password": "pw"})
	if status != http.StatusOK {
		t.Fatalf("restore: %d %v", status, out)
	}
	if srv.Engine.Paused() {
		t.Fatal("the engine was left paused after a restore")
	}
	// Resumed with the restored configuration loaded.
	nodes, _ := srv.Store.ListNodes(context.Background())
	if len(nodes) != 1 {
		t.Fatalf("nodes: %+v", nodes)
	}
	if _, ok := srv.Engine.States()[nodes[0].Checks[0].ID]; !ok {
		t.Fatal("the engine did not reload the restored configuration")
	}
	if _, err := srv.Engine.RunNow(context.Background(), nodes[0].Checks[0].ID); err != nil {
		t.Fatalf("a check run after the restore: %v", err)
	}
}

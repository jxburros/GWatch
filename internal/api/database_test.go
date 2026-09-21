package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/jxburros/GWatch/internal/dbconfig"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
	"github.com/jxburros/GWatch/internal/store/storetest"
)

// Settings › Database talks to database.json in the data directory, never
// to the store that is running. These tests use the SQLite default as the
// "server" to save, since that is the only backend every test run has;
// the connection test and the save path are the same code for a server.
func TestDatabaseStatusSaveAndTest(t *testing.T) {
	ts, srv := newTestServer(t)

	var st databaseStatus
	if code := call(t, ts, "GET", "/api/database", nil, &st); code != http.StatusOK {
		t.Fatalf("GET /api/database: %d", code)
	}
	if st.Active.Driver != storetest.Backend() && !(st.Active.Driver == "sqlite" && storetest.Backend() == "") {
		t.Fatalf("active driver = %q, want the test backend %q", st.Active.Driver, storetest.Backend())
	}
	if st.Active.SchemaVersion == 0 || st.Active.Label == "" || st.Active.Description == "" {
		t.Fatalf("active database not described: %+v", st.Active)
	}
	// The test store lives in a temp dir of its own rather than at
	// <data dir>/gwatch.db, so RestartRequired is not meaningful here; what
	// is, is that nothing has been saved yet.
	if st.Source != "default" || st.Saved.Driver != "sqlite" {
		t.Fatalf("fresh install should report the default: %+v", st)
	}

	// A connection test against SQLite answers with the library version and
	// writes nothing down.
	var test map[string]any
	if code := call(t, ts, "POST", "/api/database/test", store.DBConfig{Driver: "sqlite"}, &test); code != http.StatusOK {
		t.Fatalf("test: %d %v", code, test)
	}
	if test["ok"] != true || test["serverVersion"] == "" {
		t.Fatalf("test result: %v", test)
	}
	if _, err := os.Stat(dbconfig.Path(srv.DataDir)); !os.IsNotExist(err) {
		t.Fatalf("a connection test must not write database.json: %v", err)
	}

	// Saving a server that does not exist is refused, and nothing is saved.
	var errBody map[string]string
	if code := call(t, ts, "PUT", "/api/database", store.DBConfig{Driver: "postgres", Host: "127.0.0.1", Port: 1, User: "u", Password: "p", Database: "d", SSLMode: "disable"}, &errBody); code != http.StatusBadGateway {
		t.Fatalf("PUT unreachable server: %d %v", code, errBody)
	}
	if _, err := os.Stat(dbconfig.Path(srv.DataDir)); !os.IsNotExist(err) {
		t.Fatalf("a failed save must not write database.json: %v", err)
	}
	// So is nonsense.
	if code := call(t, ts, "PUT", "/api/database", store.DBConfig{Driver: "oracle"}, &errBody); code != http.StatusBadRequest {
		t.Fatalf("PUT unknown driver: %d %v", code, errBody)
	}
	if code := call(t, ts, "PUT", "/api/database", store.DBConfig{Driver: "mysql", Host: "h"}, &errBody); code != http.StatusBadRequest {
		t.Fatalf("PUT incomplete config: %d %v", code, errBody)
	}

	// Choosing SQLite is always possible and leaves no file behind, and it
	// is recorded in the audit trail like any other change.
	if code := call(t, ts, "PUT", "/api/database", store.DBConfig{Driver: "sqlite"}, &st); code != http.StatusOK {
		t.Fatalf("PUT sqlite: %d", code)
	}
	if st.Saved.Driver != "sqlite" || st.Source != "default" {
		t.Fatalf("after choosing sqlite: %+v", st)
	}
	var events []model.Event
	call(t, ts, "GET", "/api/events", nil, &events)
	found := false
	for _, e := range events {
		if e.Title == "Database connection changed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("saving the database settings should leave an audit event, got %+v", events)
	}
}

// A saved password comes back masked and a masked password on the way in
// keeps the saved one, the same way the SMTP password behaves.
func TestDatabasePasswordMasked(t *testing.T) {
	ts, srv := newTestServer(t)
	// Write a server config by hand, as an install with a database.json
	// would have; the server is never contacted by GET.
	// Port 1 on the loopback address: refused at once, never a DNS lookup.
	saved := store.DBConfig{Driver: "postgres", Host: "127.0.0.1", Port: 1, User: "gwatch", Password: "s3cret", Database: "gwatch", SSLMode: "disable"}
	if err := dbconfig.Save(srv.DataDir, saved); err != nil {
		t.Fatal(err)
	}
	var st databaseStatus
	if code := call(t, ts, "GET", "/api/database", nil, &st); code != http.StatusOK {
		t.Fatalf("GET: %d", code)
	}
	if st.Saved.Password != passwordMask || st.Saved.Host != "127.0.0.1" || st.Source != "file" || !st.RestartRequired {
		t.Fatalf("saved config: %+v", st)
	}
	raw, _ := json.Marshal(st)
	if string(raw) == "" || strings.Contains(string(raw), "s3cret") {
		t.Fatalf("the password leaked: %s", raw)
	}
	// The masked password is put back for the connection test, so the
	// server is actually asked with the real one (and refuses, because it
	// does not exist — which is the point: the request got that far).
	var body map[string]string
	code := call(t, ts, "POST", "/api/database/test", st.Saved, &body)
	if code != http.StatusBadGateway || strings.Contains(body["error"], passwordMask) {
		t.Fatalf("test with masked password: %d %v", code, body)
	}
	got, _, err := dbconfig.Load(srv.DataDir)
	if err != nil || got.Password != "s3cret" {
		t.Fatalf("saved password after a test: %q %v", got.Password, err)
	}
}

package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jxburros/GWatch/internal/dbconfig"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
)

// Settings › Database. The connection GWatch opens is decided before the
// database is open, so it cannot live in the settings table: these routes
// read and write database.json in the data directory instead
// (internal/dbconfig), and a change takes effect at the next start. Nothing
// here touches the store that is running.

// databaseStatus is the GET /api/database document.
type databaseStatus struct {
	// Active describes the database this process is using right now.
	Active struct {
		Driver        string `json:"driver"`
		Label         string `json:"label"`
		Description   string `json:"description"`
		SchemaVersion int    `json:"schemaVersion"`
		SizeBytes     int64  `json:"sizeBytes"`
	} `json:"active"`
	// Saved is what database.json (or, without one, the default) says the
	// next start should use, with the password masked.
	Saved store.DBConfig `json:"saved"`
	// Source says where Saved came from: "file" or "default".
	Source string `json:"source"`
	File   string `json:"file"`
	// RestartRequired is true when Saved and Active differ.
	RestartRequired bool `json:"restartRequired"`
}

func (s *Server) databaseStatus(r *http.Request) (databaseStatus, error) {
	var st databaseStatus
	if s.DataDir == "" {
		return st, errors.New("the data directory is not known to this server")
	}
	saved, fromFile, err := dbconfig.Load(s.DataDir)
	if err != nil {
		return st, err
	}
	saved = dbconfig.Complete(s.DataDir, saved)
	active := s.Store.Config()
	st.Active.Driver = s.Store.Backend()
	st.Active.Label = s.Store.Driver()
	st.Active.Description = s.Store.Path()
	st.Active.SizeBytes = s.Store.SizeBytes()
	if v, err := s.Store.SchemaVersion(r.Context()); err == nil {
		st.Active.SchemaVersion = v
	}
	st.Source = "default"
	if fromFile {
		st.Source = "file"
	}
	st.File = dbconfig.Path(s.DataDir)
	st.RestartRequired = !sameConnection(saved, active)
	st.Saved = maskDBConfig(saved)
	return st, nil
}

// sameConnection compares two configs without regard to the password,
// which one of them has redacted.
func sameConnection(a, b store.DBConfig) bool {
	a, b = a.Normalized(), b.Normalized()
	a.Password, b.Password = "", ""
	a.DSN, b.DSN = "", ""
	a.KeyFile, b.KeyFile = "", ""
	return a == b
}

// maskDBConfig hides the password the way maskSettings hides the SMTP
// password: a set value comes back as passwordMask, an empty one stays
// empty so the form can tell the two apart.
func maskDBConfig(c store.DBConfig) store.DBConfig {
	if c.Password != "" {
		c.Password = passwordMask
	}
	if c.DSN != "" {
		c.DSN = passwordMask
	}
	c.KeyFile = ""
	return c
}

// unmaskDBConfig puts the saved password (and connection string) back where
// the request carried the mask.
func unmaskDBConfig(c, saved store.DBConfig) store.DBConfig {
	if c.Password == passwordMask {
		c.Password = saved.Password
	}
	if c.DSN == passwordMask {
		c.DSN = saved.DSN
	}
	return c
}

func (s *Server) handleGetDatabase(w http.ResponseWriter, r *http.Request) {
	st, err := s.databaseStatus(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// decodeDBConfig reads a DBConfig from the request and completes it for the
// data directory, restoring a masked password from the saved file.
func (s *Server) decodeDBConfig(r *http.Request) (store.DBConfig, error) {
	if s.DataDir == "" {
		return store.DBConfig{}, errors.New("the data directory is not known to this server")
	}
	var c store.DBConfig
	if err := decodeJSON(r, &c); err != nil {
		return c, err
	}
	saved, _, err := dbconfig.Load(s.DataDir)
	if err != nil {
		// A file that cannot be read should not stop the administrator
		// from replacing it; only the saved password is lost, and a mask
		// then means "no password".
		saved = store.DBConfig{}
	}
	c = dbconfig.Complete(s.DataDir, unmaskDBConfig(c, saved))
	if c.Driver == "sqlite" {
		// The file lives in the data directory, full stop; the form has no
		// path field and the request must not choose one.
		c.Path = dbconfig.Default(s.DataDir).Path
	}
	return c, c.Validate()
}

// handleTestDatabase connects with the settings in the body and reports the
// server's version. Nothing is saved.
func (s *Server) handleTestDatabase(w http.ResponseWriter, r *http.Request) {
	c, err := s.decodeDBConfig(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	info, err := store.TestConnection(r.Context(), c)
	if err != nil {
		writeError(w, http.StatusBadGateway, "Connection failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "driver": info.Driver, "serverVersion": info.ServerVersion, "database": info.Database,
		"message": fmt.Sprintf("Connected to %s (%s).", info.Database, info.ServerVersion)})
}

// handlePutDatabase validates and tests the connection, then writes
// database.json. Choosing SQLite removes the file, which is the default
// anyway. The running process keeps its current database: the response says
// a restart is needed, and so does the audit event.
func (s *Server) handlePutDatabase(w http.ResponseWriter, r *http.Request) {
	c, err := s.decodeDBConfig(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if c.IsServer() {
		if _, err := store.TestConnection(r.Context(), c); err != nil {
			writeError(w, http.StatusBadGateway, "Not saved — the connection failed: "+err.Error())
			return
		}
		if err := dbconfig.Save(s.DataDir, c); err != nil {
			s.fail(w, err)
			return
		}
	} else if err := dbconfig.Remove(s.DataDir); err != nil {
		s.fail(w, err)
		return
	}
	st, err := s.databaseStatus(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	detail := fmt.Sprintf("Next start uses %s (was %s).", c.Describe(), s.Store.Path())
	if !st.RestartRequired {
		detail = fmt.Sprintf("Saved %s, which is the database already in use.", c.Describe())
	}
	s.recordEvent(r.Context(), model.Event{Type: model.EventConfigChanged, Title: "Database connection changed", Detail: detail})
	s.Log.Printf("database settings saved: %s", strings.TrimSuffix(detail, "."))
	writeJSON(w, http.StatusOK, st)
}

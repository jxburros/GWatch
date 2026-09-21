package api

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/auth"
	"github.com/jxburros/GWatch/internal/model"
)

// Settings › AI & MCP. The MCP companion itself is a separate program
// (mcp/); what the core service contributes is the setup page and the agent
// skill — a SKILL.md that teaches an assistant how to use the tools well. The
// skill is embedded from skill/ at the repository root, carries its own
// version in skill/VERSION, and GWatch remembers who last downloaded which
// version so the page can mention, quietly, when a newer one is available.

// skillDownloadKey is the kv row that records the last download.
const skillDownloadKey = "mcpSkillDownload"

// skillFolder is the directory the zip unpacks to. It matches the skill's
// name so that unzipping inside a skills/ folder is the whole installation.
const skillFolder = "gwatch"

// skillDownload is what is stored under skillDownloadKey.
type skillDownload struct {
	Version string    `json:"version"`
	At      time.Time `json:"at"`
	By      string    `json:"by"`
}

// mcpStatus is the answer to GET /api/mcp/status.
type mcpStatus struct {
	SkillVersion          string     `json:"skillVersion"`
	LastDownloadedVersion string     `json:"lastDownloadedVersion,omitempty"`
	LastDownloadedAt      *time.Time `json:"lastDownloadedAt"`
	LastDownloadedBy      string     `json:"lastDownloadedBy,omitempty"`
	// UpdateAvailable is true only when a download has happened and the
	// embedded skill has moved on since. Never having downloaded it is not an
	// update.
	UpdateAvailable bool `json:"updateAvailable"`
}

// skillVersion reads skill/VERSION from the embedded files. It is read on
// each request rather than at start-up because the whole thing is a few bytes
// and a stale copy would defeat the point.
func (s *Server) skillVersion() (string, error) {
	if s.Skill == nil {
		return "", errors.New("the agent skill is not built into this binary")
	}
	b, err := fs.ReadFile(s.Skill, "VERSION")
	if err != nil {
		return "", fmt.Errorf("read skill version: %w", err)
	}
	v := strings.TrimSpace(string(b))
	if v == "" {
		return "", errors.New("skill/VERSION is empty")
	}
	return v, nil
}

func (s *Server) lastSkillDownload(ctx context.Context) (skillDownload, bool) {
	var d skillDownload
	if err := s.Store.GetSetting(ctx, skillDownloadKey, &d); err != nil || d.Version == "" {
		return skillDownload{}, false
	}
	return d, true
}

func (s *Server) handleMCPStatus(w http.ResponseWriter, r *http.Request) {
	v, err := s.skillVersion()
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	out := mcpStatus{SkillVersion: v}
	if d, ok := s.lastSkillDownload(r.Context()); ok {
		at := d.At
		out.LastDownloadedVersion = d.Version
		out.LastDownloadedAt = &at
		out.LastDownloadedBy = d.By
		out.UpdateAvailable = d.Version != v
	}
	writeJSON(w, http.StatusOK, out)
}

// handleMCPSkill sends the skill: a zip of skill/ under a gwatch/ folder by
// default, or SKILL.md alone with ?format=md for assistants that take pasted
// instructions rather than a skills folder. Either way the download is
// recorded, so the settings page can say when it last happened.
func (s *Server) handleMCPSkill(w http.ResponseWriter, r *http.Request) {
	v, err := s.skillVersion()
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	var body []byte
	var name, ctype string
	switch format := r.URL.Query().Get("format"); format {
	case "", "zip":
		body, err = s.skillZip()
		name, ctype = fmt.Sprintf("gwatch-skill-%s.zip", v), "application/zip"
	case "md":
		body, err = fs.ReadFile(s.Skill, "SKILL.md")
		name, ctype = "SKILL.md", "text/markdown; charset=utf-8"
	default:
		writeError(w, http.StatusBadRequest, "format must be zip or md")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	s.recordSkillDownload(r.Context(), v, name)
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	w.Header().Set("Content-Length", fmt.Sprint(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// skillZip packs every file of skill/ under skillFolder/ in memory. The skill
// is a handful of small text files, so there is nothing to stream.
func (s *Server) skillZip() ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	now := time.Now()
	err := fs.WalkDir(s.Skill, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(s.Skill, p)
		if err != nil {
			return err
		}
		hdr := &zip.FileHeader{Name: skillFolder + "/" + p, Method: zip.Deflate, Modified: now}
		f, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("pack skill: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// recordSkillDownload remembers the download and audits it. A failure to
// record is logged rather than returned: the person already has the file.
func (s *Server) recordSkillDownload(ctx context.Context, version, name string) {
	by := auth.FromContext(ctx).Label()
	d := skillDownload{Version: version, At: time.Now().UTC(), By: by}
	if err := s.Store.PutSetting(ctx, skillDownloadKey, d); err != nil {
		s.Log.Errorf("record skill download: %v", err)
	}
	// Filed under "update" because, like a release, the skill is a versioned
	// artefact GWatch hands out and later reports as out of date.
	s.recordEvent(ctx, model.Event{Type: model.EventUpdate, Title: "Agent skill downloaded: " + version, Detail: fmt.Sprintf("%s was downloaded from Settings › AI & MCP.", name)})
}

package api

import (
	"errors"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/reports"
	"github.com/jxburros/GWatch/internal/store"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (s *Server) reportRoutes(mux *http.ServeMux) {
	s.route(mux, "GET /api/reports", s.handleReports)
	s.route(mux, "PUT /api/reports", s.handleSaveReports)
	s.route(mux, "GET /api/reports/generate", s.handleGenerateReport)
}
func (s *Server) handleReports(w http.ResponseWriter, r *http.Request) {
	defs := []model.ReportDefinition{}
	err := s.Store.GetSetting(r.Context(), "reports", &defs)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, defs)
}
func (s *Server) handleSaveReports(w http.ResponseWriter, r *http.Request) {
	var defs []model.ReportDefinition
	if err := decodeJSON(r, &defs); err != nil {
		writeDecodeError(w, err)
		return
	}
	if err := reports.Validate(defs); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	var previous []model.ReportDefinition
	if err := s.Store.GetSetting(r.Context(), "reports", &previous); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.fail(w, err)
		return
	}
	for i := range defs {
		defs[i].CreatedAt = time.Now()
		for _, old := range previous {
			if old.ID == defs[i].ID {
				defs[i].CreatedAt = old.CreatedAt
				break
			}
		}
	}
	if err := s.Store.PutSetting(r.Context(), "reports", defs); err != nil {
		s.fail(w, err)
		return
	}
	s.configChanged(r.Context(), nil, "Report schedules updated", "")
	writeJSON(w, 200, defs)
}
func (s *Server) handleGenerateReport(w http.ResponseWriter, r *http.Request) {
	from, err := time.Parse(time.RFC3339, r.URL.Query().Get("from"))
	if err != nil {
		writeError(w, 400, "from must be RFC3339")
		return
	}
	to, err := time.Parse(time.RFC3339, r.URL.Query().Get("to"))
	if err != nil {
		writeError(w, 400, "to must be RFC3339")
		return
	}
	if !to.After(from) || to.Sub(from) > 366*24*time.Hour {
		writeError(w, 400, "choose a range of up to 366 days")
		return
	}
	d := model.ReportDefinition{Name: "GWatch availability report", IncludeLatencyCharts: r.URL.Query().Get("charts") == "1"}
	d.Groups = splitList(r.URL.Query().Get("groups"))
	d.Tags = splitList(r.URL.Query().Get("tags"))
	if v := r.URL.Query().Get("target"); v != "" {
		d.TargetAvailability, err = strconv.ParseFloat(v, 64)
		if err != nil || d.TargetAvailability < 0 || d.TargetAvailability > 100 {
			writeError(w, 400, "invalid target")
			return
		}
	}
	html, err := reports.Generate(r.Context(), s.Store, d, from, to)
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="gwatch-report.html"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(html))
}

// splitList reads a comma-separated query value. Entries are trimmed and
// blanks dropped: "Office, Lab" names the group "Lab", not " Lab" (which no
// tag and no chart grouping would match), and "Office," does not add an empty
// name that matches nothing.
func splitList(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

package api

import (
	"github.com/jxburros/GWatch/internal/auth"
	"github.com/jxburros/GWatch/internal/store"
	"net/http"
	"strings"
)

func (s *Server) incidentRoutes(mux *http.ServeMux) {
	s.route(mux, "GET /api/incidents", s.handleIncidents)
	s.route(mux, "GET /api/incidents/{id}", s.handleIncident)
	s.route(mux, "POST /api/incidents/{id}/{action}", s.handleIncidentAction)
}

func (s *Server) handleIncidents(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if state != "" && state != "active" && state != "all" && state != "open" && state != "acknowledged" && state != "resolved" {
		writeError(w, 400, "invalid incident state")
		return
	}
	v, err := s.Store.ListIncidents(r.Context(), state)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) handleIncident(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, 400, "invalid incident id")
		return
	}
	v, err := s.Store.GetIncident(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	events, err := s.Store.ListEvents(r.Context(), store.EventFilter{NodeID: &v.NodeID, Since: &v.OpenedAt, Until: v.ResolvedAt, Limit: 5000})
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"incident": v, "events": events})
}
func (s *Server) handleIncidentAction(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, 400, "invalid incident id")
		return
	}
	action := r.PathValue("action")
	if action != "acknowledge" && action != "resolve" && action != "note" {
		writeError(w, 400, "invalid incident action")
		return
	}
	var body struct {
		Note string `json:"note"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeDecodeError(w, err)
		return
	}
	body.Note = strings.TrimSpace(body.Note)
	if len(body.Note) > 8000 || action == "note" && body.Note == "" {
		writeError(w, 400, "a note must contain 1–8000 bytes")
		return
	}
	v, err := s.Store.UpdateIncident(r.Context(), id, action, auth.FromContext(r.Context()).Label(), body.Note)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.configChanged(r.Context(), nil, "Incident "+action, v.NodeName)
	writeJSON(w, 200, v)
}

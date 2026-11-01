package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
)

// mapLayoutKey is the settings key the network map's arrangement is kept under.
const mapLayoutKey = model.MapLayoutSettingKey

func (s *Server) mapRoutes(mux *http.ServeMux) {
	s.route(mux, "GET /api/map/layout", s.handleGetMapLayout)
	s.route(mux, "PUT /api/map/layout", s.handlePutMapLayout)
}

// knownNodeIDs is every node id there is, so a layout never keeps a place for
// a node that has been deleted.
func (s *Server) knownNodeIDs(r *http.Request) (map[int64]bool, error) {
	nodes, err := s.Store.ListNodes(r.Context())
	if err != nil {
		return nil, err
	}
	known := make(map[int64]bool, len(nodes))
	for _, n := range nodes {
		known[n.ID] = true
	}
	return known, nil
}

func (s *Server) loadMapLayout(r *http.Request) (model.MapLayout, error) {
	var l model.MapLayout
	if err := s.Store.GetSetting(r.Context(), mapLayoutKey, &l); err != nil && !errors.Is(err, store.ErrNotFound) {
		return l, err
	}
	return l, nil
}

func (s *Server) handleGetMapLayout(w http.ResponseWriter, r *http.Request) {
	l, err := s.loadMapLayout(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	known, err := s.knownNodeIDs(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	// What was stored was checked on the way in; this only drops the nodes
	// deleted since.
	if err := l.Clean(known); err != nil {
		l = model.MapLayout{}
		_ = l.Clean(nil)
	}
	writeJSON(w, http.StatusOK, l)
}

func (s *Server) handlePutMapLayout(w http.ResponseWriter, r *http.Request) {
	var l model.MapLayout
	if err := decodeJSON(r, &l); err != nil {
		writeDecodeError(w, err)
		return
	}
	known, err := s.knownNodeIDs(r)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := l.Clean(known); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	l.UpdatedAt = time.Now().UTC()
	if err := s.Store.PutSetting(r.Context(), mapLayoutKey, l); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

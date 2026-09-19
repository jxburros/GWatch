package api

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/auth"
	"github.com/jxburros/GWatch/internal/engine"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
)

// maxWallboardTrends bounds how many charts one board asks the database for,
// whatever its panels claim to want: a wallboard reloads itself every few
// seconds for ever, so a board nobody is looking at must not be able to make
// that expensive.
const maxWallboardTrends = 8

// ---- the boards themselves ----

func (s *Server) handleListWallboards(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListWallboards(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	admin := auth.FromContext(r.Context()).IsAdmin()
	for i := range list {
		if !admin {
			list[i].Share = list[i].Share.Redact()
		}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleGetWallboard(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	b, err := s.Store.GetWallboard(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !auth.FromContext(r.Context()).IsAdmin() {
		b.Share = b.Share.Redact()
	}
	writeJSON(w, http.StatusOK, b)
}

func (s *Server) handleSaveWallboard(w http.ResponseWriter, r *http.Request) {
	var b model.Wallboard
	if err := decodeJSON(r, &b); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if r.Method == http.MethodPut {
		id, err := pathID(r, "id")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		b.ID = id
	} else {
		b.ID = 0
	}
	b.Name = strings.TrimSpace(b.Name)
	if b.Name == "" {
		writeError(w, http.StatusBadRequest, "give the wallboard a name")
		return
	}
	if len(b.Name) > 100 {
		writeError(w, http.StatusBadRequest, "the name is too long")
		return
	}
	// A board created with nothing on it starts as the default arrangement
	// rather than as a blank screen: a wallboard is put up to be looked at,
	// and there is one obvious thing for it to show.
	if b.ID == 0 && len(b.Panels) == 0 {
		def := model.DefaultWallboard()
		b.Layout, b.Panels = def.Layout, def.Panels
	}
	for _, p := range b.Panels {
		if !model.ValidWallPanelType(p.Type) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("%q is not a panel a wallboard can show", p.Type))
			return
		}
	}
	saved, err := s.Store.SaveWallboard(r.Context(), b)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) handleDeleteWallboard(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	b, err := s.Store.GetWallboard(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Store.DeleteWallboard(r.Context(), id); err != nil {
		s.fail(w, err)
		return
	}
	if b.Share.Enabled {
		s.auditAuth(r.Context(), "Wallboard deleted: "+b.Name, "Its projected address no longer resolves to anything.")
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleShareWallboard switches a board's projected address on or off and can
// replace the token behind it.
//
// This is the one place in GWatch that hands out something usable without a
// sign-in, so it is written to make that plain: it is off by default, it is
// per board, what it grants is one board's worth of read-only data and nothing
// else, and every change to it is written to the audit trail.
func (s *Server) handleShareWallboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
		Rotate  bool `json:"rotate"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	current, err := s.Store.GetWallboard(ctx, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	token := ""
	if body.Enabled && (body.Rotate || current.Share.Token == "") {
		if token, err = auth.NewWallboardToken(); err != nil {
			s.fail(w, err)
			return
		}
	}
	saved, err := s.Store.SetWallboardShare(ctx, id, body.Enabled, token)
	if err != nil {
		s.fail(w, err)
		return
	}
	switch {
	case !body.Enabled:
		s.auditAuth(ctx, "Wallboard no longer projected: "+saved.Name,
			"Its address stops working. Anything already showing it stops updating at its next refresh.")
	case body.Rotate:
		s.auditAuth(ctx, "Wallboard address changed: "+saved.Name,
			"The previous address stops working; every display showing this board needs the new one.")
	default:
		s.auditAuth(ctx, "Wallboard projected: "+saved.Name,
			"A browser on the network that has the address can now read this one board without signing in. It cannot read anything else.")
	}
	writeJSON(w, http.StatusOK, saved)
}

// ---- what a board actually shows ----

type wallboardView struct {
	engine.Overview
	Wallboard model.Wallboard       `json:"wallboard"`
	Health    model.Health          `json:"health"`
	Trends    []model.HistorySeries `json:"trends"`
	ServerNow time.Time             `json:"serverNow"`
}

// handleWallboardView returns a board and the data it draws in one document,
// so a display refreshing every few seconds makes one request rather than six.
//
// It is the only route reachable without a credential that returns monitoring
// data, and it is reachable only with the token of a board whose sharing is
// switched on. A signed-in reader needs no token; anyone else needs the one
// token that names this board.
func (s *Server) handleWallboardView(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	board, err := s.Store.GetWallboard(ctx, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !auth.FromContext(ctx).Authenticated() {
		token := r.URL.Query().Get("token")
		if !board.Share.Enabled || board.Share.Token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(board.Share.Token)) != 1 {
			// Whether the board exists, is not shared, or was asked for with
			// the wrong token are all the same answer out here.
			writeError(w, http.StatusUnauthorized, "this wallboard is not available at this address")
			return
		}
	}
	// The token names the board; it is never echoed back to the display, which
	// has no use for it beyond the address it was opened with.
	board.Share = board.Share.Redact()

	ov, err := s.Engine.Overview(ctx)
	if err != nil {
		s.fail(w, err)
		return
	}
	doc := wallboardView{
		Overview:  ov,
		Wallboard: board,
		Health:    s.Engine.Health(ctx),
		Trends:    s.wallboardTrends(r, board, ov),
		ServerNow: time.Now(),
	}
	writeJSON(w, http.StatusOK, doc)
}

// wallboardTrends collects the history the board's trend panels ask for. A
// panel that names no checks gets the same automatic pick the dashboard makes,
// so a board laid out in ten seconds still has something worth watching on it.
func (s *Server) wallboardTrends(r *http.Request, board model.Wallboard, ov engine.Overview) []model.HistorySeries {
	type want struct {
		rng   string
		limit int
		ids   []int64
	}
	wants := []want{}
	for _, p := range board.Panels {
		if p.Type != "trends" {
			continue
		}
		var cfg struct {
			Range    string  `json:"range"`
			Limit    int     `json:"limit"`
			CheckIDs []int64 `json:"checkIds"`
		}
		_ = json.Unmarshal(p.Config, &cfg)
		if cfg.Range == "" {
			cfg.Range = "24h"
		}
		if cfg.Limit <= 0 {
			cfg.Limit = 4
		}
		wants = append(wants, want{rng: cfg.Range, limit: cfg.Limit, ids: cfg.CheckIDs})
	}
	if len(wants) == 0 {
		return []model.HistorySeries{}
	}

	byCheck := map[int64]struct {
		check model.Check
		node  model.Node
	}{}
	for _, nv := range ov.Nodes {
		for _, cv := range nv.Checks {
			byCheck[cv.Check.ID] = struct {
				check model.Check
				node  model.Node
			}{cv.Check, nv.Node}
		}
	}

	out := []model.HistorySeries{}
	seen := map[string]bool{}
	now := time.Now()
	for _, wnt := range wants {
		rng, err := store.ParseRange(wnt.rng)
		if err != nil {
			rng, _ = store.ParseRange("24h")
		}
		picked := []struct {
			check model.Check
			node  model.Node
		}{}
		if len(wnt.ids) > 0 {
			for _, id := range wnt.ids {
				if c, ok := byCheck[id]; ok {
					picked = append(picked, c)
				}
			}
		} else {
			for _, c := range autoChecks(ov, wnt.limit) {
				picked = append(picked, struct {
					check model.Check
					node  model.Node
				}{c.check, c.node})
			}
		}
		if len(picked) > wnt.limit {
			picked = picked[:wnt.limit]
		}
		for _, p := range picked {
			key := fmt.Sprintf("%d@%s", p.check.ID, wnt.rng)
			if seen[key] || len(out) >= maxWallboardTrends {
				continue
			}
			seen[key] = true
			series, err := s.Store.History(r.Context(), p.check, p.node.Name, rng, now)
			if err != nil {
				continue
			}
			out = append(out, series)
		}
	}
	return out
}

package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
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
		writeDecodeError(w, err)
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
		writeDecodeError(w, err)
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

// wallboardView is the document a board is drawn from. It is built on the
// server from what the board's panels draw and nothing else: a shared board's
// address is handed to a television, and whoever reads the address can read
// this document, so the browser is never sent data for it to filter out. A
// field is filled only when a panel (or the footer) on this board shows it.
type wallboardView struct {
	Wallboard    model.Wallboard       `json:"wallboard"`
	Summary      *engine.Summary       `json:"summary,omitempty"`
	Nodes        []wallNodeView        `json:"nodes"`
	Groups       []engine.GroupView    `json:"groups"`
	Attention    []engine.Attention    `json:"attention"`
	CertWarnings []wallCert            `json:"certWarnings"`
	Maintenance  []wallMaintenance     `json:"maintenance"`
	Health       *wallHealth           `json:"health,omitempty"`
	Trends       []model.HistorySeries `json:"trends"`
	GeneratedAt  time.Time             `json:"generatedAt"`
	ServerNow    time.Time             `json:"serverNow"`
}

// wallNodeView is a node as a nodes panel draws it: a name, a status and
// what the panel's group and tag filters match on. No host, no checks, no
// metrics address.
type wallNodeView struct {
	Node   wallNode     `json:"node"`
	Status model.Status `json:"status"`
}

type wallNode struct {
	ID     int64    `json:"id"`
	Name   string   `json:"name"`
	Group  string   `json:"group"`
	Groups []string `json:"groups"`
	Tags   []string `json:"tags"`
}

// wallCert is a certificate warning without the subject or the error text.
type wallCert struct {
	NodeName      string    `json:"nodeName"`
	CheckName     string    `json:"checkName"`
	DaysRemaining int       `json:"daysRemaining"`
	NotAfter      time.Time `json:"notAfter"`
}

// wallMaintenance is a window in force, as the maintenance panel names it.
type wallMaintenance struct {
	Name   string `json:"name"`
	Group  string `json:"group"`
	NodeID *int64 `json:"nodeId"`
}

// wallHealth is the service panel's and the footer's view of GWatch itself:
// whether it is running and checking. Paths, database details, versions and
// recent errors are for the Health page, behind a sign-in.
type wallHealth struct {
	ServiceRunning   bool       `json:"serviceRunning"`
	SchedulerRunning bool       `json:"schedulerRunning"`
	LastCheckAt      *time.Time `json:"lastCheckAt"`
	ChecksEnabled    int        `json:"checksEnabled"`
}

// handleWallboardView returns a board and the data it draws in one document,
// so a display refreshing every few seconds makes one request rather than six.
//
// It is the only route reachable without a credential that returns monitoring
// data, and it is reachable only with the token of a board whose sharing is
// switched on. A signed-in reader needs no token; anyone else needs the one
// token that names this board. Either way the document holds only what this
// board draws (projectWallboard).
func (s *Server) handleWallboardView(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	anonymous := !auth.FromContext(ctx).Authenticated()
	board, err := s.Store.GetWallboard(ctx, id)
	if err != nil {
		if anonymous && errors.Is(err, store.ErrNotFound) {
			// Out here a board that does not exist gets the same answer as
			// one that is not shared, or else ids could be probed for.
			writeError(w, http.StatusUnauthorized, "this wallboard is not available at this address")
			return
		}
		s.fail(w, err)
		return
	}
	if anonymous {
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
	doc := projectWallboard(board, ov, func() model.Health { return s.Engine.Health(ctx) })
	doc.Trends = s.wallboardTrends(r, board, ov)
	doc.ServerNow = time.Now()
	writeJSON(w, http.StatusOK, doc)
}

// projectWallboard keeps from ov what board's panels draw (web/wall-render.js
// reads exactly these fields). No check configuration is carried at all, so
// there are no credentials to mask. health is called only when something on
// the board shows the service's state.
func projectWallboard(board model.Wallboard, ov engine.Overview, health func() model.Health) wallboardView {
	doc := wallboardView{
		Wallboard:    board,
		Nodes:        []wallNodeView{},
		Groups:       []engine.GroupView{},
		Attention:    []engine.Attention{},
		CertWarnings: []wallCert{},
		Maintenance:  []wallMaintenance{},
		Trends:       []model.HistorySeries{},
		GeneratedAt:  ov.GeneratedAt,
	}
	shows := map[string]bool{}
	var nodePanels []wallNodeFilter
	for _, p := range board.Panels {
		shows[p.Type] = true
		if p.Type == "nodes" {
			var f wallNodeFilter
			_ = json.Unmarshal(p.Config, &f)
			nodePanels = append(nodePanels, f)
		}
	}
	footer := !board.Layout.HideChrome
	if footer || shows["headline"] || shows["counts"] {
		sum := ov.Summary
		doc.Summary = &sum
	}
	if footer || shows["health"] {
		h := health()
		doc.Health = &wallHealth{ServiceRunning: h.ServiceRunning, SchedulerRunning: h.SchedulerRunning, LastCheckAt: h.LastCheckAt, ChecksEnabled: h.ChecksEnabled}
	}
	if shows["attention"] && ov.Attention != nil {
		doc.Attention = ov.Attention
	}
	if shows["groups"] && ov.Groups != nil {
		doc.Groups = ov.Groups
	}
	if shows["certs"] {
		for _, c := range ov.CertWarnings {
			doc.CertWarnings = append(doc.CertWarnings, wallCert{NodeName: c.NodeName, CheckName: c.CheckName, DaysRemaining: c.DaysRemaining, NotAfter: c.NotAfter})
		}
	}
	if shows["maintenance"] {
		for _, m := range ov.Maintenance {
			doc.Maintenance = append(doc.Maintenance, wallMaintenance{Name: m.Name, Group: m.Group, NodeID: m.NodeID})
		}
	}
	for _, nv := range ov.Nodes {
		for _, f := range nodePanels {
			if f.matches(nv.Node) {
				doc.Nodes = append(doc.Nodes, wallNodeView{
					Node:   wallNode{ID: nv.Node.ID, Name: nv.Node.Name, Group: nv.Node.Group, Groups: nv.Node.Groups, Tags: nv.Node.Tags},
					Status: nv.Status,
				})
				break
			}
		}
	}
	return doc
}

// wallNodeFilter is a nodes panel's choice of nodes, matched the way the
// renderer matches it: by group (case-insensitively), by tag and by id, each
// one that is set having to hold.
type wallNodeFilter struct {
	Group   string  `json:"group"`
	Tag     string  `json:"tag"`
	NodeIDs []int64 `json:"nodeIds"`
}

func (f wallNodeFilter) matches(n model.Node) bool {
	if want := strings.ToLower(strings.TrimSpace(f.Group)); want != "" {
		groups := n.Groups
		if len(groups) == 0 && n.Group != "" {
			groups = []string{n.Group}
		}
		if !slices.ContainsFunc(groups, func(g string) bool { return strings.ToLower(strings.TrimSpace(g)) == want }) {
			return false
		}
	}
	if f.Tag != "" && !slices.Contains(n.Tags, f.Tag) {
		return false
	}
	if len(f.NodeIDs) > 0 && !slices.Contains(f.NodeIDs, n.ID) {
		return false
	}
	return true
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

package api

import (
	"net/http"
	"strings"

	"github.com/jxburros/GWatch/internal/checks"
	"github.com/jxburros/GWatch/internal/model"
)

// handleSNMPWalk reads a subtree of a device and hands back what it found, so
// the editor can offer a list to tick rather than asking somebody to know an
// interface's SNMP index by heart.
//
// It is administrator-only and refused to API keys of every scope: it takes a
// credential and an arbitrary address, makes GWatch talk to it, and reports
// what came back. That is a probe, and a probe belongs to the person sitting
// in front of the machine, not to an integration.
func (s *Server) handleSNMPWalk(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Host        string `json:"host"`
		Version     string `json:"version"`
		Port        int    `json:"port"`
		Community   string `json:"community"`
		User        string `json:"user"`
		AuthProto   string `json:"authProto"`
		AuthPass    string `json:"authPass"`
		PrivProto   string `json:"privProto"`
		PrivPass    string `json:"privPass"`
		OID         string `json:"oid"`
		Max         int    `json:"max"`
		FromCheckID int64  `json:"checkId"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeDecodeError(w, err)
		return
	}
	if strings.TrimSpace(body.Host) == "" {
		writeError(w, http.StatusBadRequest, "a host is required")
		return
	}
	cfg := model.CheckConfig{
		SNMPVersion:   body.Version,
		SNMPPort:      body.Port,
		SNMPCommunity: body.Community,
		SNMPUser:      body.User,
		SNMPAuthProto: body.AuthProto,
		SNMPAuthPass:  body.AuthPass,
		SNMPPrivProto: body.PrivProto,
		SNMPPrivPass:  body.PrivPass,
	}
	// The editor never holds a saved check's credentials, so it names the
	// check instead and the stored ones are used — the same arrangement as
	// POST /api/checks/test.
	if body.FromCheckID != 0 {
		if stored, err := s.Store.GetCheck(r.Context(), body.FromCheckID); err == nil {
			before := checkSecretFields(&stored.Config)
			now := checkSecretFields(&cfg)
			for i := range now {
				if *now[i] == "" || *now[i] == passwordMask {
					*now[i] = *before[i]
				}
			}
			if strings.TrimSpace(cfg.SNMPUser) == "" {
				cfg.SNMPUser = stored.Config.SNMPUser
			}
		}
	}

	rows, truncated, err := checks.Walk(r.Context(), checks.WalkRequest{
		Host:   body.Host,
		Config: cfg,
		Root:   body.OID,
		Max:    body.Max,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"rows":      rows,
		"truncated": truncated,
		"max":       checks.WalkMaxRows,
	})
}

package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/jxburros/GWatch/internal/model"
)

// ---- notification rules (#31) ----

// ruleDoc is a rule as the API answers it: the rule plus where it stands.
type ruleDoc struct {
	model.Rule
	State model.RuleState `json:"state"`
}

func (s *Server) ruleDocs(list []model.Rule) []ruleDoc {
	states := s.Engine.RuleStates()
	out := make([]ruleDoc, 0, len(list))
	for _, r := range list {
		st, ok := states[r.ID]
		if !ok {
			st = model.RuleState{RuleID: r.ID}
		}
		out = append(out, ruleDoc{Rule: r, State: st})
	}
	return out
}

// normalizeRule tidies a rule and checks it: the pure parts through
// model.Rule.Validate, then that every node and check it names exists (and
// a check condition's node is filled in from the check, so the interface
// can show it), then each action the way triggers and endpoints are.
func (s *Server) normalizeRule(ctx context.Context, r *model.Rule) error {
	r.Name = strings.TrimSpace(r.Name)
	r.Join = strings.ToLower(strings.TrimSpace(r.Join))
	if r.Join == "" {
		r.Join = model.RuleJoinAll
	}
	if r.Join != model.RuleJoinAtLeast {
		r.AtLeast = 0
	}
	if r.CooldownMinutes < 0 {
		r.CooldownMinutes = 0
	}
	for i := range r.Conditions {
		c := &r.Conditions[i]
		if c.Kind == "" {
			c.Kind = model.RuleConditionStatus
		}
		c.Status = model.Status(strings.ToLower(strings.TrimSpace(string(c.Status))))
		// A check condition is stored by check alone; the node is implied.
		if c.CheckID != nil {
			c.NodeID = nil
		}
	}
	if err := r.Validate(); err != nil {
		return err
	}
	nodes, err := s.Store.ListNodes(ctx)
	if err != nil {
		return err
	}
	nodeByID := map[int64]model.Node{}
	checkNode := map[int64]int64{}
	for _, n := range nodes {
		nodeByID[n.ID] = n
		for _, c := range n.Checks {
			checkNode[c.ID] = n.ID
		}
	}
	for i, c := range r.Conditions {
		switch {
		case c.CheckID != nil:
			if _, ok := checkNode[*c.CheckID]; !ok {
				return fmt.Errorf("condition %d: the check does not exist", i+1)
			}
		case c.NodeID != nil:
			if _, ok := nodeByID[*c.NodeID]; !ok {
				return fmt.Errorf("condition %d: the node does not exist", i+1)
			}
		}
	}
	for i := range r.Actions {
		if err := s.normalizeAction(&r.Actions[i]); err != nil {
			return fmt.Errorf("action %d: %w", i+1, err)
		}
	}
	return nil
}

func (s *Server) handleListRules(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListRules(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.ruleDocs(list))
}

func (s *Server) handleGetRule(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rule, err := s.Store.GetRule(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.ruleDocs([]model.Rule{rule})[0])
}

func (s *Server) handleSaveRule(w http.ResponseWriter, r *http.Request) {
	var rule model.Rule
	if err := decodeJSON(r, &rule); err != nil {
		writeDecodeError(w, err)
		return
	}
	if r.Method == http.MethodPut {
		id, err := pathID(r, "id")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		rule.ID = id
	} else {
		rule.ID = 0
	}
	if err := s.normalizeRule(r.Context(), &rule); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := s.Store.SaveRule(r.Context(), rule)
	if err != nil {
		s.fail(w, err)
		return
	}
	kinds := make([]string, 0, len(saved.Actions))
	for _, a := range saved.Actions {
		kinds = append(kinds, string(a.Type))
	}
	s.configChanged(r.Context(), nil, "Rule saved: "+saved.Name, fmt.Sprintf("%s of %d conditions → %s.", ruleJoinLabel(saved), len(saved.Conditions), strings.Join(kinds, ", ")))
	writeJSON(w, http.StatusOK, s.ruleDocs([]model.Rule{saved})[0])
}

// ruleJoinLabel is the join as the audit line says it: "All", "Any", "At
// least 2".
func ruleJoinLabel(r model.Rule) string {
	switch r.Join {
	case model.RuleJoinAny:
		return "Any"
	case model.RuleJoinAtLeast:
		return fmt.Sprintf("At least %d", r.AtLeast)
	}
	return "All"
}

func (s *Server) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rule, err := s.Store.GetRule(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Store.DeleteRule(r.Context(), id); err != nil {
		s.fail(w, err)
		return
	}
	s.configChanged(r.Context(), nil, "Rule deleted: "+rule.Name, "")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleTestRule runs a rule's actions once with sample placeholder values
// and answers one ActionResult per action. Nothing is recorded.
func (s *Server) handleTestRule(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	results, err := s.Engine.TestRule(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, results)
}

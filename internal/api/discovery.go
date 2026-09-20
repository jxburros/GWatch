package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jxburros/GWatch/internal/auth"
	"github.com/jxburros/GWatch/internal/checks"
	"github.com/jxburros/GWatch/internal/discovery"
	"github.com/jxburros/GWatch/internal/engine"
	"github.com/jxburros/GWatch/internal/model"
)

// Discovery: sweep a range of addresses, show what answered, and turn the
// chosen ones into nodes. Every route here is administrator-only and closed to
// API keys — pinging a few thousand addresses and then creating monitors from
// the result is not something an integration should be able to set off.

// discoveryRequest is the body of POST /api/discovery.
type discoveryRequest struct {
	Ranges []string `json:"ranges"`
	Ports  []int    `json:"ports"`
}

func (s *Server) handleStartDiscovery(w http.ResponseWriter, r *http.Request) {
	var req discoveryRequest
	if err := decodeJSON(r, &req); err != nil {
		writeDecodeError(w, err)
		return
	}
	// Who asked is settled here, while the request still exists: the run
	// outlives it, and the timeline entry it writes at the end should still
	// name the administrator who set it off.
	actor := auth.FromContext(r.Context()).Label()
	job, err := s.discovery().Start(
		discovery.Request{Ranges: req.Ranges, Ports: req.Ports},
		func(job discovery.Job) { s.pushDiscovery(actor, job) },
	)
	switch {
	case errors.Is(err, discovery.ErrBusy):
		// 409 rather than 429: the request is not too frequent, it is asking
		// for something the install can only do one of at a time, and the
		// caller's own next move is to look at the run that is already going.
		writeError(w, http.StatusConflict, "a discovery run is already going; wait for it to finish or cancel it first")
		return
	case err != nil:
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) handleGetDiscovery(w http.ResponseWriter, r *http.Request) {
	job, err := s.discovery().Get(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "no discovery run with that id")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// handleLatestDiscovery answers with the most recent run so a modal that was
// closed mid-sweep can pick it back up. A 404 here means "nothing has been
// swept yet", which the interface reads as an empty form rather than an error.
func (s *Server) handleLatestDiscovery(w http.ResponseWriter, r *http.Request) {
	job, err := s.discovery().Latest()
	if err != nil {
		writeError(w, http.StatusNotFound, "no discovery has been run yet")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleCancelDiscovery(w http.ResponseWriter, r *http.Request) {
	job, err := s.discovery().Cancel(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "no discovery run with that id")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// discoveryAddRequest is the body of POST /api/discovery/{id}/add.
type discoveryAddRequest struct {
	Items []discoveryAddItem `json:"items"`
	// Group is the group every created node lands in. Groups is the same
	// thing written as a list, which is where the field is heading; the first
	// entry wins when both are sent.
	Group  string   `json:"group"`
	Groups []string `json:"groups"`
}

type discoveryAddItem struct {
	IP       string `json:"ip"`
	Name     string `json:"name"`
	Template string `json:"template"`
}

// discoverySkipped is one address that was not turned into a node, and why.
type discoverySkipped struct {
	IP     string `json:"ip"`
	Reason string `json:"reason"`
}

func (s *Server) handleAddFromDiscovery(w http.ResponseWriter, r *http.Request) {
	job, err := s.discovery().Get(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "no discovery run with that id")
		return
	}
	var req discoveryAddRequest
	if err := decodeJSON(r, &req); err != nil {
		writeDecodeError(w, err)
		return
	}
	if len(req.Items) == 0 {
		writeError(w, http.StatusBadRequest, "choose at least one device to add")
		return
	}

	// What the sweep found, so a request cannot name an address the run never
	// saw: the hostname and the suggested template come from here, and an
	// address nobody pinged has neither.
	found := map[string]discovery.Responder{}
	for _, res := range job.Results {
		found[res.IP] = res
	}

	// Every host already being watched, so re-running a sweep over a network
	// that is half set up adds the other half rather than a second copy of
	// everything.
	existing, err := s.Store.ListNodes(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	taken := map[string]string{}
	for _, n := range existing {
		if h := strings.ToLower(strings.TrimSpace(n.Host)); h != "" {
			taken[h] = n.Name
		}
	}

	templates := map[string]model.NodeTemplate{}
	for _, t := range checks.Templates() {
		templates[t.ID] = t
	}

	group := strings.TrimSpace(req.Group)
	if group == "" && len(req.Groups) > 0 {
		group = strings.TrimSpace(req.Groups[0])
	}

	created := []nodeDoc{}
	skipped := []discoverySkipped{}
	for _, item := range req.Items {
		ip := strings.TrimSpace(item.IP)
		res, ok := found[ip]
		if !ok {
			skipped = append(skipped, discoverySkipped{IP: ip, Reason: "this address was not one of the run's responders"})
			continue
		}
		if name, dup := taken[strings.ToLower(ip)]; dup {
			skipped = append(skipped, discoverySkipped{IP: ip, Reason: fmt.Sprintf("already monitored as %q", name)})
			continue
		}
		templateID := strings.TrimSpace(item.Template)
		if templateID == "" {
			templateID = res.Template
		}
		if templateID == "" {
			templateID = "ping"
		}
		tmpl, ok := templates[templateID]
		if !ok {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("there is no %q template", templateID))
			return
		}
		name := strings.TrimSpace(item.Name)
		if name == "" {
			name = res.Name()
		}

		n := nodeFromTemplate(tmpl, ip, name, group)
		if err := s.normalizeNode(&n); err != nil {
			skipped = append(skipped, discoverySkipped{IP: ip, Reason: err.Error()})
			continue
		}
		saved, err := s.Store.CreateNode(r.Context(), n)
		if err != nil {
			s.fail(w, err)
			return
		}
		created = append(created, s.decorateNode(saved, s.Engine.States(), nil))
		// The same map the duplicate test reads, so two items naming one
		// address in a single request cannot both get through.
		taken[strings.ToLower(ip)] = saved.Name
	}

	if len(created) > 0 {
		if err := s.Engine.ReloadConfig(r.Context()); err != nil {
			s.Log.Errorf("reload config: %v", err)
		}
		names := make([]string, 0, len(created))
		for _, c := range created {
			names = append(names, fmt.Sprintf("%s (%s)", c.Name, c.Host))
		}
		s.recordEvent(r.Context(), model.Event{
			Type:   model.EventDiscovery,
			Title:  fmt.Sprintf("Added %s from discovery", countNodes(len(created))),
			Detail: strings.Join(names, ", "),
		})
	}
	writeJSON(w, http.StatusCreated, map[string]any{"created": created, "skipped": skipped})
}

func countNodes(n int) string {
	if n == 1 {
		return "1 node"
	}
	return fmt.Sprintf("%d nodes", n)
}

// nodeFromTemplate builds the node a template describes, pointed at one
// discovered address. It is the same shape the template picker hands the node
// editor — the checks, the tags, the importance — with the address and the
// name filled in, so a node added from a sweep is indistinguishable from one
// typed in by hand.
func nodeFromTemplate(t model.NodeTemplate, ip, name, group string) model.Node {
	n := t.Node
	n.ID = 0
	n.Host = ip
	n.Name = name
	n.Template = t.ID
	n.Enabled = true
	n.Tags = append([]string(nil), t.Node.Tags...)
	if group != "" {
		setNodeGroup(&n, group)
	}
	n.Checks = make([]model.Check, 0, len(t.Checks))
	for _, c := range t.Checks {
		c.ID, c.NodeID = 0, 0
		n.Checks = append(n.Checks, c)
	}
	return n
}

// setNodeGroup puts a node in a group. It is a function of its own because the
// group field is on its way to becoming a list of them; when it gets there,
// this is the one place in the discovery code that has to learn the new shape.
func setNodeGroup(n *model.Node, group string) {
	n.Group = group
}

// discovery returns the registry, creating it on first use. Handler() makes it
// eagerly too, so the only callers that ever construct one here are tests
// driving a handler without building the router.
func (s *Server) discovery() *discovery.Registry {
	s.discoveryOnce.Do(func() {
		if s.discoveryJobs == nil {
			s.discoveryJobs = &discovery.Registry{}
		}
	})
	return s.discoveryJobs
}

// pushDiscovery puts a run's progress on the update stream, and writes the one
// timeline entry a finished run leaves behind. It is called from the sweep's
// own goroutine, long after the request that started the run was answered,
// which is why the actor is passed in rather than read from a context.
func (s *Server) pushDiscovery(actor string, job discovery.Job) {
	s.Engine.Broadcast(engine.Update{Kind: "discovery", Discovery: &engine.DiscoveryProgress{
		ID:         job.ID,
		State:      job.State,
		Scanned:    job.Scanned,
		Total:      job.Total,
		Responders: job.Responders,
	}})
	if !job.Done() {
		return
	}
	title := "Discovery scanned " + job.Summary()
	detail := ""
	switch job.State {
	case discovery.StateCancelled:
		title = "Discovery cancelled after " + job.Summary()
	case discovery.StateFailed:
		title = "Discovery failed after " + job.Summary()
		detail = job.Error
	}
	s.Engine.RecordEvent(model.Event{Type: model.EventDiscovery, Title: title, Detail: detail, Actor: actor})
}

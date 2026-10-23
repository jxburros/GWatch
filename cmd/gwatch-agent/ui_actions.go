// The control panel's actions beyond settings: the recent log, "copy
// diagnostics", "update now" and pairing again (issue #64).
//
// Each of these is something the agent can already do from a terminal, and
// each is done here by the same code the terminal command uses, not by a
// second implementation of it: the log is the one the service writes
// (agentlog.go), "update now" is applyUpdate, the function the automatic
// updater and `gwatch-agent update` both go through (so a packaged agent
// refuses it in the same words and for the same reason, entry 10 of
// docs/AGENT-DECISIONS.md), and pairing again is pair(), exactly as
// `gwatch-agent pair` runs it.
//
// The guard in ui.go applies to all of them unchanged: the loopback Host
// check and the session cookie for everything, and for the two that change
// something (update-now and pair) the panel header and a matching Origin as
// well. The two that only read (the log and the diagnostics) never carry the
// token: both are passed through redactSecrets, by value and by shape, before
// they leave the agent, because both are exactly the text people paste into
// an issue.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/update"
)

// addPanelState adds what the newer parts of the panel need to the state
// document: who keeps the agent up to date, and where its files are.
func (s *uiServer) addPanelState(st map[string]any, c config) {
	upd := map[string]any{"auto": c.autoUpdate, "canUpdate": true}
	if p, ok := currentPackaging(); ok {
		upd["auto"] = false
		upd["canUpdate"] = false
		upd["manager"] = p.manager
		upd["source"] = p.source
		upd["command"] = p.updateCommand("")
	}
	st["updates"] = upd
	st["paths"] = map[string]string{
		"config": c.configPathOrDefault(),
		"status": statusFile(),
		"log":    logFile(),
	}
}

// ---- the log ----

func (s *uiServer) handleLog(w http.ResponseWriter, r *http.Request) {
	c, _, _ := s.loadConfig()
	writeUIJSON(w, http.StatusOK, s.recentLog(c.token))
}

// ---- diagnostics ----

// diagnostics is everything someone helping with this machine would ask for
// first, as plain text: what the agent is, where it lives, its settings with
// the token taken out, what the service and the reporting loop last said, and
// the end of the log.
func (s *uiServer) diagnostics() string {
	c, problems, warnings := s.loadConfig()
	var b strings.Builder
	line := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }

	line("GWatch agent diagnostics, %s", time.Now().UTC().Format(time.RFC3339))
	line("")
	line("Agent:        %s", versionLine())
	line("Platform:     %s/%s", runtime.GOOS, runtime.GOARCH)
	if exe, err := os.Executable(); err == nil {
		line("Executable:   %s", exe)
	}
	if p, ok := currentPackaging(); ok {
		line("Installed by: %s (updates: %s)", p.source, p.updateCommand(""))
	} else {
		line("Updates:      %s", map[bool]string{true: "automatic", false: "off (autoUpdate is false)"}[c.autoUpdate])
	}
	svc := s.state()
	line("Service:      %s%s", svc.State, map[bool]string{true: " (" + svc.Error + ")", false: ""}[svc.Error != ""])
	line("Server:       %s", c.server)
	line("Name:         %s", map[bool]string{true: c.name, false: "(this computer's own hostname)"}[c.name != ""])
	line("Token:        %s", map[bool]string{true: "set, from the " + c.sources["token"], false: "not set"}[c.token != ""])
	line("Settings:     %s", c.configPathOrDefault())
	for _, p := range problems {
		line("  problem: %s", p)
	}
	for _, w := range warnings {
		line("  warning: %s", w)
	}
	for _, o := range overrides(c) {
		line("  note: %s", o)
	}

	line("")
	line("--- status (%s) ---", statusFile())
	if st, err := readAgentStatus(); err != nil {
		line("(no status: %v)", err)
	} else {
		line("updated %s ago, agent %s, process %d, started %s", time.Since(st.UpdatedAt).Round(time.Second), st.Version, st.PID, st.StartedAt.UTC().Format(time.RFC3339))
		if st.LastReport != nil {
			line("last reading: %s, %s", st.LastReport.UTC().Format(time.RFC3339), map[bool]string{true: "accepted", false: "failed"}[st.ReportOK])
		} else {
			line("last reading: none yet")
		}
		if st.ReportError != "" {
			line("last reading error: %s", st.ReportError)
		}
		if st.Failures > 0 {
			line("failed attempts in a row: %d", st.Failures)
		}
		line("readings held: %d", st.Held)
		if st.LastCheckIn != nil {
			line("last check-in: %s", st.LastCheckIn.UTC().Format(time.RFC3339))
		}
		if st.CheckInErr != "" {
			line("last check-in error: %s", st.CheckInErr)
		}
		if st.LatestAgent != "" {
			line("newest agent GWatch knows of: %s", st.LatestAgent)
		}
		if sel, err := json.Marshal(st.Collecting); err == nil {
			line("collecting: %s", sel)
		}
	}

	line("")
	line("--- settings file, token removed ---")
	if text := strings.TrimSpace(s.fileText()); text != "" {
		line("%s", text)
	} else {
		line("(no file)")
	}

	lg := s.recentLog(c.token)
	line("")
	line("--- last %d lines of the log (%s) ---", logTailLines, lg.Source)
	if len(lg.Lines) == 0 {
		line("(%s)", strings.TrimSuffix(lg.Note, "."))
	}
	for _, l := range lg.Lines {
		line("%s", l)
	}
	// The settings file is shown with its token masked already, and the log
	// was redacted as it was read; this pass is for anything else that
	// happened to quote it (a server error echoing a header, say).
	return redactSecrets(b.String(), c.token)
}

func (s *uiServer) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	writeUIJSON(w, http.StatusOK, map[string]string{"text": s.diagnostics()})
}

// ---- update now ----

// installUpdateFromPanel is applyUpdate with one thing added: a location this
// account cannot write is reported as the permission problem it is, so the
// panel can say how to get permission rather than quoting the message meant
// for a terminal.
func installUpdateFromPanel(ctx context.Context, c config, info model.UpdateInfo) error {
	if !isPackaged() {
		if exe, err := update.Executable(); err == nil && !update.DirWritable(exe) {
			return fmt.Errorf("cannot replace %s: %w", exe, os.ErrPermission)
		}
	}
	return applyUpdate(ctx, c, info)
}

func (s *uiServer) handleUpdateNow(w http.ResponseWriter, r *http.Request) {
	c, problems, _ := s.loadConfig()
	if len(problems) > 0 {
		writeUIError(w, http.StatusConflict, "the settings file has problems: "+strings.Join(problems, "; "))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	info, err := s.checkUpdate(ctx, c)
	if err != nil {
		writeUIJSON(w, http.StatusOK, map[string]any{"ok": false, "message": "could not look for a newer agent: " + err.Error()})
		return
	}
	if !info.UpdateAvailable {
		writeUIJSON(w, http.StatusOK, map[string]any{"ok": true, "message": fmt.Sprintf("This agent (%s) is already the newest release.", version)})
		return
	}
	// A packaged agent belongs to its package manager. applyUpdate refuses
	// as well; saying so here keeps the answer out of the "failed" column.
	if p, ok := currentPackaging(); ok {
		writeUIError(w, http.StatusConflict, p.refuseUpdate(info.LatestVersion, info.ReleaseURL).Error())
		return
	}
	if err := s.installUpdate(ctx, c, info); err != nil {
		if isPermissionError(err) {
			writeUIError(w, http.StatusForbidden, elevateHint)
			return
		}
		writeUIJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	msg := fmt.Sprintf("Installed agent %s. The version it replaced is kept beside it (`gwatch-agent rollback` puts it back).", info.LatestVersion)
	if svc := s.state(); svc.Managed && (svc.State == "running" || svc.State == "stopped") {
		if err := s.restartService(); err != nil {
			msg += " The service could not be restarted onto it (" + err.Error() + "); restart it to run the new version."
		} else {
			msg += " The service was restarted and is running it now."
		}
	}
	st := s.buildState()
	st["ok"] = true
	st["message"] = msg
	writeUIJSON(w, http.StatusOK, st)
}

// restartService restarts the service the way the Restart button does,
// including asking for an administrator's rights if this account has none.
func (s *uiServer) restartService() error {
	err := s.control("restart")
	if err != nil && isPermissionError(err) && s.elevate != nil {
		err = s.elevate("restart")
	}
	return err
}

// ---- pairing again ----

type uiPairRequest struct {
	Server   string `json:"server"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	Insecure bool   `json:"insecure"`
}

// handlePair pairs this machine again: a new code from GWatch for a new token,
// kept in the configuration file, with a first reading to prove it, and the
// service restarted onto it. It is how a machine whose token was revoked, or
// that is moving to a different GWatch, is put right without a terminal.
func (s *uiServer) handlePair(w http.ResponseWriter, r *http.Request) {
	var req uiPairRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeUIError(w, http.StatusBadRequest, "that is not a pairing request")
		return
	}
	req.Server, req.Code, req.Name = strings.TrimSpace(req.Server), strings.TrimSpace(req.Code), strings.TrimSpace(req.Name)
	if req.Server == "" || tidyCode(req.Code) == "" {
		writeUIError(w, http.StatusBadRequest, "give the GWatch server's address and the pairing code it shows (Add node › GWatch agent)")
		return
	}
	c, problems, _ := s.loadConfig()
	if len(problems) > 0 {
		writeUIError(w, http.StatusConflict, "the settings file has problems: "+strings.Join(problems, "; ")+" — fix it in the file editor below first")
		return
	}
	if !configWritable(c.configPathOrDefault()) {
		// Better said before the code is spent than after.
		writeUIError(w, http.StatusForbidden, elevateHint)
		return
	}
	if c.sources == nil {
		c.sources = map[string]string{}
	}
	// Trust in a certificate is about one server (entry 13): a different
	// server starts with none, and whatever is ticked now is the decision.
	if strings.TrimRight(ensureScheme(req.Server), "/") != strings.TrimRight(ensureScheme(c.server), "/") {
		c.certPin = ""
	}
	c.server, c.code, c.insecure = req.Server, req.Code, req.Insecure
	c.token, c.tokenSource = "", srcDefault
	c.sources["server"], c.sources["insecure"] = srcFlag, srcFlag
	if req.Name != "" {
		c.name = req.Name
		c.sources["name"] = srcFlag
	}
	if err := s.pair(&c); err != nil {
		if isPermissionError(err) {
			writeUIError(w, http.StatusForbidden, elevateHint)
			return
		}
		st := s.buildState()
		st["ok"] = false
		st["message"] = err.Error()
		writeUIJSON(w, http.StatusOK, st)
		return
	}
	name := c.pairedAs
	if name == "" {
		name = c.name
	}
	msg := fmt.Sprintf("Paired with GWatch as %q, and GWatch accepted a first reading.", name)
	if svc := s.state(); svc.Managed && (svc.State == "running" || svc.State == "stopped") {
		if err := s.restartService(); err != nil {
			msg += " The service could not be restarted (" + err.Error() + "); restart it so it uses the new token."
		} else {
			msg += " The service was restarted with the new token."
		}
	}
	st := s.buildState()
	st["ok"] = true
	st["message"] = msg
	st["link"] = machineLink(c)
	writeUIJSON(w, http.StatusOK, st)
}

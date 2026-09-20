package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
)

// ---- maintenance windows ----

func (e *Engine) maintenanceLoop() {
	defer e.wg.Done()
	e.trackMaintenance(time.Now())
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-e.ctx.Done():
			return
		case now := <-ticker.C:
			e.trackMaintenance(now)
		}
	}
}

// trackMaintenance records began/ended events when windows change state.
func (e *Engine) trackMaintenance(now time.Time) {
	e.mu.Lock()
	var began, ended []model.MaintenanceWindow
	seen := map[int64]bool{}
	for _, w := range e.windows {
		seen[w.ID] = true
		active := w.Active(now)
		if active && !e.activeMW[w.ID] {
			e.activeMW[w.ID] = true
			began = append(began, w)
		} else if !active && e.activeMW[w.ID] {
			delete(e.activeMW, w.ID)
			ended = append(ended, w)
		}
	}
	for id := range e.activeMW {
		if !seen[id] {
			delete(e.activeMW, id)
		}
	}
	e.mu.Unlock()
	for _, w := range began {
		e.recordEvent(model.Event{Type: model.EventMaintenanceBegan, NodeID: w.NodeID, Title: "Maintenance began: " + w.Name, Detail: scopeDescription(w, e) + " Alerts are suppressed until the window ends."})
	}
	for _, w := range ended {
		e.recordEvent(model.Event{Type: model.EventMaintenanceEnded, NodeID: w.NodeID, Title: "Maintenance ended: " + w.Name, Detail: scopeDescription(w, e) + " Alerts are active again."})
	}
	if len(began)+len(ended) > 0 {
		e.broadcast(Update{Kind: "maintenance"})
	}
}

func scopeDescription(w model.MaintenanceWindow, e *Engine) string {
	if w.NodeID != nil {
		e.mu.Lock()
		n, ok := e.nodes[*w.NodeID]
		e.mu.Unlock()
		if ok {
			return "Applies to " + n.Name + "."
		}
		return "Applies to one node."
	}
	if strings.TrimSpace(w.Group) != "" {
		return "Applies to the group " + w.Group + "."
	}
	return "Applies to all nodes."
}

// ActiveMaintenance returns the windows active now.
func (e *Engine) ActiveMaintenance(now time.Time) []model.MaintenanceWindow {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := []model.MaintenanceWindow{}
	for _, w := range e.windows {
		if w.Active(now) {
			out = append(out, w)
		}
	}
	return out
}

// NodeInMaintenance reports whether the node is covered by an active window.
func (e *Engine) NodeInMaintenance(n model.Node, now time.Time) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.inMaintenanceLocked(n, now)
}

// ---- retention / rollups ----

func (e *Engine) retentionLoop() {
	defer e.wg.Done()
	// Full backfill shortly after start, then incremental rollups every 5
	// minutes and cleanup once an hour.
	select {
	case <-e.ctx.Done():
		return
	case <-time.After(20 * time.Second):
	}
	if err := e.RunRetention(e.ctx, true); err != nil {
		e.RecordError("retention backfill", err)
	}
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	lastCleanup := time.Now()
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			cleanup := time.Since(lastCleanup) >= time.Hour
			if err := e.runRetention(e.ctx, false, cleanup); err != nil {
				e.RecordError("retention", err)
			}
			if cleanup {
				lastCleanup = time.Now()
			}
		}
	}
}

// RunRetention computes rollups (full backfill when requested) and applies
// the retention policy.
func (e *Engine) RunRetention(ctx context.Context, backfill bool) error {
	return e.runRetention(ctx, backfill, true)
}

func (e *Engine) runRetention(ctx context.Context, backfill, cleanup bool) error {
	start := time.Now()
	now := start
	s := e.Settings().Retention
	st := e.store

	from5 := now.Add(-2 * time.Hour)
	from1h := now.Add(-3 * 24 * time.Hour)
	from1d := now.Add(-3 * 24 * time.Hour)
	if backfill {
		oldest, err := st.OldestResult(ctx)
		if err != nil {
			return err
		}
		if oldest != nil {
			from5 = *oldest
		}
		from1h = time.Unix(0, 0)
		from1d = time.Unix(0, 0)
		if oldest != nil {
			// hourly/daily are built from the 5-minute tier which may reach
			// further back than raw data; rebuild from the very beginning.
			from1h = time.Unix(0, 0)
		}
	}
	var deleted int64
	var runErr error
	if _, err := st.RollupFromRaw(ctx, from5, now); err != nil {
		runErr = fmt.Errorf("5-minute rollup: %w", err)
	}
	if runErr == nil {
		if _, err := st.RollupUp(ctx, store.Bucket5m, store.Bucket1h, from1h, now); err != nil {
			runErr = fmt.Errorf("hourly rollup: %w", err)
		}
	}
	if runErr == nil {
		if _, err := st.RollupUp(ctx, store.Bucket1h, store.Bucket1d, from1d, now); err != nil {
			runErr = fmt.Errorf("daily rollup: %w", err)
		}
	}
	if runErr == nil && cleanup {
		del := func(n int64, err error) {
			if err != nil && runErr == nil {
				runErr = err
			}
			deleted += n
		}
		if s.RawDays > 0 {
			del(st.DeleteResultsBefore(ctx, now.AddDate(0, 0, -s.RawDays)))
		}
		if s.FiveMinDays > 0 {
			del(st.DeleteRollupsBefore(ctx, store.Bucket5m, now.AddDate(0, 0, -s.FiveMinDays)))
		}
		if s.HourlyDays > 0 {
			del(st.DeleteRollupsBefore(ctx, store.Bucket1h, now.AddDate(0, 0, -s.HourlyDays)))
		}
		if s.DailyDays > 0 {
			del(st.DeleteRollupsBefore(ctx, store.Bucket1d, now.AddDate(0, 0, -s.DailyDays)))
		}
		if s.EventDays > 0 {
			del(st.DeleteEventsBefore(ctx, now.AddDate(0, 0, -s.EventDays)))
		}
		if s.HostDays > 0 {
			del(st.PruneHostSamples(ctx, now.AddDate(0, 0, -s.HostDays)))
		}
		if deleted > 0 {
			_ = st.Checkpoint(ctx)
		}
	}
	status := e.retentionStatus(ctx, s)
	status.LastRunAt = ptrTime(start)
	status.LastDurationMS = time.Since(start).Milliseconds()
	status.DeletedLastRun = deleted
	if runErr != nil {
		status.LastError = runErr.Error()
	}
	e.mu.Lock()
	e.retention = status
	e.mu.Unlock()
	_ = st.PutSetting(ctx, "retentionStatus", status)
	if deleted > 0 {
		e.recordEvent(model.Event{Type: model.EventRetention, Title: "Retention job trimmed old data", Detail: fmt.Sprintf("%d rows older than the configured retention were rolled up and deleted.", deleted)})
	}
	e.broadcast(Update{Kind: "health"})
	return runErr
}

func (e *Engine) retentionStatus(ctx context.Context, s model.RetentionSettings) model.RetentionStatus {
	e.mu.Lock()
	status := e.retention
	e.mu.Unlock()
	raw, r5, r1h, r1d, events, err := e.store.Counts(ctx)
	if err == nil {
		status.RawRows, status.RollupRows5m, status.RollupRows1h, status.RollupRows1d, status.EventRows = raw, r5, r1h, r1d, events
	}
	status.OldestRaw, _ = e.store.OldestResult(ctx)
	status.Plan = RetentionPlan(s)
	return status
}

// RetentionPlan describes the policy in plain language.
func RetentionPlan(s model.RetentionSettings) []string {
	days := func(n int) string {
		switch {
		case n <= 0:
			return "forever"
		case n%365 == 0:
			return fmt.Sprintf("%d year(s)", n/365)
		case n%30 == 0 && n >= 60:
			return fmt.Sprintf("%d months", n/30)
		}
		return fmt.Sprintf("%d days", n)
	}
	plan := []string{
		fmt.Sprintf("Every individual check result is kept for %s.", days(s.RawDays)),
		fmt.Sprintf("Results are summarised into 5-minute buckets, kept for %s.", days(s.FiveMinDays)),
		fmt.Sprintf("Hourly summaries are kept for %s.", days(s.HourlyDays)),
		fmt.Sprintf("Daily summaries are kept %s.", map[bool]string{true: "forever", false: "for " + days(s.DailyDays)}[s.DailyDays <= 0]),
		fmt.Sprintf("Incident and event history is kept for %s.", days(s.EventDays)),
		fmt.Sprintf("Hardware readings from this computer and any agents are kept for %s.", days(s.HostDays)),
	}
	return plan
}

// RetentionStatus returns the latest status document.
func (e *Engine) RetentionStatus(ctx context.Context) model.RetentionStatus {
	return e.retentionStatus(ctx, e.Settings().Retention)
}

// ---- backup status ----

// SetBackupStatus stores the last backup outcome.
func (e *Engine) SetBackupStatus(ctx context.Context, b model.BackupStatus) {
	e.mu.Lock()
	e.backup = b
	e.mu.Unlock()
	_ = e.store.PutSetting(ctx, "backupStatus", b)
	e.broadcast(Update{Kind: "health"})
}

// BackupStatus returns the last backup outcome.
func (e *Engine) BackupStatus() model.BackupStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.backup
}

// ---- health ----

// Health builds the self-observability document.
func (e *Engine) Health(ctx context.Context) model.Health {
	retention := e.RetentionStatus(ctx)
	errs, _ := e.store.ListEvents(ctx, store.EventFilter{Limit: 10, Types: []model.EventType{model.EventInternalError, model.EventAlertFailed}})
	if errs == nil {
		errs = []model.Event{}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	h := model.Health{
		Version:          e.opts.Version,
		ServiceMode:      e.opts.ServiceMode,
		ServiceRunning:   e.started && !e.stopping,
		StartedAt:        e.startedAt,
		UptimeSeconds:    int64(now.Sub(e.startedAt).Seconds()),
		Now:              now,
		SchedulerRunning: e.started && !e.stopping && now.Sub(e.lastTick) < 10*time.Second,
		LastCheckAt:      e.lastCheckAt,
		LastSuccessAt:    e.lastOKAt,
		LastGap:          e.lastGap,
		DatabasePath:     e.store.Path(),
		DatabaseBytes:    e.store.SizeBytes(),
		DataDir:          e.opts.DataDir,
		KeyPath:          filepath.Join(e.opts.DataDir, store.KeyFileName),
		BackupDir:        filepath.Join(e.opts.DataDir, "backups"),
		Retention:        retention,
		Backup:           e.backup,
		RecentErrors:     errs,
		AlertsEnabled:    e.settings.Alerts.Enabled,
		SMTPConfigured:   e.settings.Alerts.SMTP.Host != "" && e.settings.Alerts.SMTP.From != "",
		LastAlertAt:      e.lastAlertAt,
		LastAlertError:   e.lastAlertErr,
		ListenAddress:    e.opts.ListenAddr,
		Platform:         runtime.GOOS + "/" + runtime.GOARCH,
	}
	for id, c := range e.checks {
		h.ChecksTotal++
		n := e.nodes[c.NodeID]
		if c.Enabled && n.Enabled {
			h.ChecksEnabled++
		}
		if e.running[id] {
			h.ChecksRunning++
		}
		if st := e.states[id]; st != nil && st.NextRunAt != nil && c.Enabled && n.Enabled {
			if h.NextCheckAt == nil || st.NextRunAt.Before(*h.NextCheckAt) {
				h.NextCheckAt = st.NextRunAt
			}
		}
	}
	return h
}

// ---- overview ----

// CheckView pairs a check with its live state and last result.
type CheckView struct {
	Check      model.Check      `json:"check"`
	State      model.CheckState `json:"state"`
	LastResult *model.Result    `json:"lastResult"`
}

// NodeView is a node with derived status information.
type NodeView struct {
	Node          model.Node   `json:"node"`
	Status        model.Status `json:"status"`
	Checks        []CheckView  `json:"checks"`
	InMaintenance bool         `json:"inMaintenance"`
	AffectedBy    string       `json:"affectedBy"`
}

// GroupView summarises a group.
type GroupView struct {
	Name        string       `json:"name"`
	Status      model.Status `json:"status"`
	Up          int          `json:"up"`
	Degraded    int          `json:"degraded"`
	Down        int          `json:"down"`
	Unknown     int          `json:"unknown"`
	Paused      int          `json:"paused"`
	Maintenance int          `json:"maintenance"`
	Total       int          `json:"total"`
}

// Summary counts nodes by status.
type Summary struct {
	Up          int `json:"up"`
	Degraded    int `json:"degraded"`
	Down        int `json:"down"`
	Unknown     int `json:"unknown"`
	Paused      int `json:"paused"`
	Maintenance int `json:"maintenance"`
	Total       int `json:"total"`
}

// CertWarning is a certificate close to expiry or invalid.
type CertWarning struct {
	NodeID        int64     `json:"nodeId"`
	NodeName      string    `json:"nodeName"`
	CheckID       int64     `json:"checkId"`
	CheckName     string    `json:"checkName"`
	DaysRemaining int       `json:"daysRemaining"`
	NotAfter      time.Time `json:"notAfter"`
	Subject       string    `json:"subject"`
	Valid         bool      `json:"valid"`
	Error         string    `json:"error,omitempty"`
}

// Attention is an item on the needs-attention list.
type Attention struct {
	NodeID     int64        `json:"nodeId"`
	NodeName   string       `json:"nodeName"`
	CheckID    int64        `json:"checkId"`
	CheckName  string       `json:"checkName"`
	Status     model.Status `json:"status"`
	Message    string       `json:"message"`
	Since      *time.Time   `json:"since"`
	AffectedBy string       `json:"affectedBy"`
}

// Overview is the dashboard document.
type Overview struct {
	Summary      Summary                   `json:"summary"`
	Nodes        []NodeView                `json:"nodes"`
	Groups       []GroupView               `json:"groups"`
	Incidents    []model.Event             `json:"incidents"`
	CertWarnings []CertWarning             `json:"certWarnings"`
	Attention    []Attention               `json:"attention"`
	Maintenance  []model.MaintenanceWindow `json:"maintenance"`
	GeneratedAt  time.Time                 `json:"generatedAt"`
}

// NodeStatus derives a node's status from its checks (maintenance wins over
// everything except being paused).
func (e *Engine) nodeStatusLocked(n model.Node, now time.Time) (model.Status, bool) {
	inMW := e.inMaintenanceLocked(n, now)
	if !n.Enabled {
		return model.StatusPaused, inMW
	}
	status := model.StatusPaused
	any := false
	for _, c := range n.Checks {
		if !c.Enabled {
			continue
		}
		any = true
		if st := e.states[c.ID]; st != nil {
			status = model.Worst(status, st.Status)
		} else {
			status = model.Worst(status, model.StatusUnknown)
		}
	}
	if !any {
		return model.StatusPaused, inMW
	}
	if inMW {
		return model.StatusMaintenance, inMW
	}
	return status, inMW
}

// Overview builds the dashboard document.
func (e *Engine) Overview(ctx context.Context) (Overview, error) {
	last, err := e.store.LastResults(ctx)
	if err != nil {
		return Overview{}, err
	}
	incidents, err := e.store.ListEvents(ctx, store.EventFilter{Limit: 20, Types: []model.EventType{model.EventDown, model.EventRecovered, model.EventWarning, model.EventWarningCleared, model.EventCertWarning, model.EventContentChanged, model.EventAffectedByParent, model.EventMaintenanceBegan, model.EventMaintenanceEnded, model.EventMonitorGap}})
	if err != nil {
		return Overview{}, err
	}
	now := time.Now()
	e.mu.Lock()
	defer e.mu.Unlock()
	ov := Overview{Nodes: []NodeView{}, Groups: []GroupView{}, Incidents: incidents, CertWarnings: []CertWarning{}, Attention: []Attention{}, Maintenance: []model.MaintenanceWindow{}, GeneratedAt: now}
	groups := map[string]*GroupView{}
	for _, w := range e.windows {
		if w.Active(now) {
			ov.Maintenance = append(ov.Maintenance, w)
		}
	}
	for _, n := range e.nodes {
		status, inMW := e.nodeStatusLocked(n, now)
		nv := NodeView{Node: n, Status: status, Checks: []CheckView{}, InMaintenance: inMW}
		for _, c := range n.Checks {
			st := e.states[c.ID]
			cv := CheckView{Check: c}
			if st != nil {
				cv.State = *st
				if st.AffectedByNodeName != "" && nv.AffectedBy == "" {
					nv.AffectedBy = st.AffectedByNodeName
				}
			} else {
				cv.State = model.CheckState{CheckID: c.ID, Status: model.StatusUnknown}
			}
			if r, ok := last[c.ID]; ok {
				rc := r
				cv.LastResult = &rc
				if rc.Details.Cert != nil && c.Enabled && n.Enabled {
					warnDays := c.Config.CertWarnDays
					if warnDays <= 0 {
						warnDays = e.settings.Alerts.CertWarnDays
					}
					if rc.Details.Cert.DaysRemaining <= warnDays || !rc.Details.Cert.Valid {
						ov.CertWarnings = append(ov.CertWarnings, CertWarning{NodeID: n.ID, NodeName: n.Name, CheckID: c.ID, CheckName: c.Name, DaysRemaining: rc.Details.Cert.DaysRemaining, NotAfter: rc.Details.Cert.NotAfter, Subject: rc.Details.Cert.Subject, Valid: rc.Details.Cert.Valid, Error: rc.Details.Cert.Error})
					}
				}
			}
			if c.Enabled && n.Enabled && st != nil && (st.Status == model.StatusDown || st.Status == model.StatusDegraded) {
				ov.Attention = append(ov.Attention, Attention{NodeID: n.ID, NodeName: n.Name, CheckID: c.ID, CheckName: c.Name, Status: st.Status, Message: st.LastMessage, Since: st.LastChangeAt, AffectedBy: st.AffectedByNodeName})
			}
			nv.Checks = append(nv.Checks, cv)
		}
		ov.Nodes = append(ov.Nodes, nv)
		ov.Summary.Total++
		switch status {
		case model.StatusUp:
			ov.Summary.Up++
		case model.StatusDegraded:
			ov.Summary.Degraded++
		case model.StatusDown:
			ov.Summary.Down++
		case model.StatusPaused:
			ov.Summary.Paused++
		case model.StatusMaintenance:
			ov.Summary.Maintenance++
		default:
			ov.Summary.Unknown++
		}
		// A node counts in every group it belongs to, so the group tallies
		// added together can come to more than the number of nodes. Each
		// group answers "how is this group doing", which is the question the
		// group cards on the dashboard and the wallboard ask.
		gnames := n.GroupList()
		if len(gnames) == 0 {
			gnames = []string{"Ungrouped"}
		}
		for _, gname := range gnames {
			gname = strings.TrimSpace(gname)
			if gname == "" {
				continue
			}
			g, ok := groups[gname]
			if !ok {
				g = &GroupView{Name: gname, Status: model.StatusPaused}
				groups[gname] = g
			}
			g.Total++
			g.Status = model.Worst(g.Status, status)
			switch status {
			case model.StatusUp:
				g.Up++
			case model.StatusDegraded:
				g.Degraded++
			case model.StatusDown:
				g.Down++
			case model.StatusPaused:
				g.Paused++
			case model.StatusMaintenance:
				g.Maintenance++
			default:
				g.Unknown++
			}
		}
	}
	sort.Slice(ov.Nodes, func(i, j int) bool {
		a, b := ov.Nodes[i], ov.Nodes[j]
		if a.Status.Severity() != b.Status.Severity() {
			return a.Status.Severity() > b.Status.Severity()
		}
		return strings.ToLower(a.Node.Name) < strings.ToLower(b.Node.Name)
	})
	for _, g := range groups {
		ov.Groups = append(ov.Groups, *g)
	}
	sort.Slice(ov.Groups, func(i, j int) bool { return strings.ToLower(ov.Groups[i].Name) < strings.ToLower(ov.Groups[j].Name) })
	sort.Slice(ov.Attention, func(i, j int) bool {
		if ov.Attention[i].Status.Severity() != ov.Attention[j].Status.Severity() {
			return ov.Attention[i].Status.Severity() > ov.Attention[j].Status.Severity()
		}
		return ov.Attention[i].NodeName < ov.Attention[j].NodeName
	})
	sort.Slice(ov.CertWarnings, func(i, j int) bool { return ov.CertWarnings[i].DaysRemaining < ov.CertWarnings[j].DaysRemaining })
	return ov, nil
}

// States returns a copy of all live states.
func (e *Engine) States() map[int64]model.CheckState {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(map[int64]model.CheckState, len(e.states))
	for id, st := range e.states {
		out[id] = *st
	}
	return out
}

// State returns the live state of one check.
func (e *Engine) State(checkID int64) (model.CheckState, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	st, ok := e.states[checkID]
	if !ok {
		return model.CheckState{}, false
	}
	return *st, true
}

// NodeStatus returns the derived status of a node.
func (e *Engine) NodeStatus(n model.Node) (model.Status, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.nodeStatusLocked(n, time.Now())
}

// ---- hardware readings ----

// hostSampleLoop reads this computer's hardware on a schedule. It runs whether
// or not a check watches this machine, so the hardware page has a history to
// draw from the moment someone opens it.
func (e *Engine) hostSampleLoop() {
	defer e.wg.Done()
	e.hosts.Run(e.ctx)
}

// recordScrapedHost stores a reading a hardware check fetched itself. Readings
// this computer produced are already stored by the sampler, and an agent's are
// stored when it pushes them, so only a scraped endpoint's reading arrives
// this way — which is why the key decides rather than the check type.
func (e *Engine) recordScrapedHost(ctx context.Context, result model.Result) {
	host := result.Details.Host
	if host == nil || !strings.HasPrefix(host.Key, "url:") {
		return
	}
	if err := e.hosts.Record(ctx, *host); err != nil {
		e.RecordError("store scraped hardware reading", err)
	}
}

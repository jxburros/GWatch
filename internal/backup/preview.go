package backup

import (
	"context"
	"errors"
	"fmt"

	"github.com/jxburros/GWatch/internal/auth"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
)

// A restore replaces the whole configuration, and an archive is only as
// trustworthy as whoever had it and its password. Before anything is applied
// the administrator is therefore shown what the archive would change in the
// places where a change matters for security (#148): accounts that would come
// back or be added, and which of them are administrators; API keys; anything
// that runs a command on this computer; endpoints anybody can call without a
// token; and changes to who can reach the web interface and how they sign in.
// Plan works that out by comparing the archive with this server's database
// without writing anything; the Settings › Backups restore dialog shows it
// and asks for confirmation.

// Preview is what restoring an archive would do, as POST /api/backups/inspect
// returns it.
type Preview struct {
	Manifest Manifest      `json:"manifest"`
	Counts   PreviewCounts `json:"counts"`
	// Accounts are the user accounts in the archive that are not on this
	// server. They are only re-created when the restore is asked to
	// (restoreAccounts), or without asking when this server has no accounts
	// at all (AccountsRecreatedAnyway).
	Accounts                []PreviewAccount `json:"accounts"`
	AccountsRecreatedAnyway bool             `json:"accountsRecreatedAnyway"`
	// Agents are the registered machines in the archive that are not on
	// this server, under the same rule as Accounts.
	Agents []string `json:"agents"`
	// APIKeys are the keys in the archive that this server does not have.
	// They are always added (a key revoked here is still known here, so it
	// is not among them).
	APIKeys []PreviewKey `json:"apiKeys"`
	// Commands are the things in the archive that run a program on this
	// computer: script and git actions of triggers, endpoints, notification
	// rules, and checks that run code.
	Commands []PreviewCommand `json:"commands"`
	// TokenlessEndpoints are the archive's custom endpoints that anybody who
	// can reach the server may call without a token, by URL name.
	TokenlessEndpoints []string `json:"tokenlessEndpoints"`
	// AccessChanges describe, one sentence each, how the archive's settings
	// change who can reach the web interface and how they sign in, compared
	// with the settings in force now.
	AccessChanges []string `json:"accessChanges"`
	// Problems are the reasons the archive's configuration fails validation;
	// a restore of an archive with any is refused. Plan leaves it empty; the
	// API fills it in with its own validation.
	Problems []string `json:"problems"`
}

// PreviewCounts counts what an archive holds.
type PreviewCounts struct {
	Nodes       int  `json:"nodes"`
	Checks      int  `json:"checks"`
	Dashboards  int  `json:"dashboards"`
	Maintenance int  `json:"maintenance"`
	Triggers    int  `json:"triggers"`
	Endpoints   int  `json:"endpoints"`
	Rules       int  `json:"rules"`
	Charts      int  `json:"charts"`
	Wallboards  int  `json:"wallboards"`
	Reports     int  `json:"reports"`
	Users       int  `json:"users"`
	APIKeys     int  `json:"apiKeys"`
	Agents      int  `json:"agents"`
	Incidents   int  `json:"incidents"`
	History     bool `json:"history"`
	Results     int  `json:"results"`
	Rollups     int  `json:"rollups"`
	Events      int  `json:"events"`
}

// PreviewAccount is an account in an archive that this server does not have.
type PreviewAccount struct {
	Username string `json:"username"`
	Role     string `json:"role"`
	Admin    bool   `json:"admin"`
}

// PreviewKey is an API key in an archive that this server does not have.
type PreviewKey struct {
	Name    string `json:"name"`
	Prefix  string `json:"prefix"`
	Scope   string `json:"scope"`
	Revoked bool   `json:"revoked"`
}

// PreviewCommand is one thing in an archive that runs a program here.
type PreviewCommand struct {
	// Kind is "trigger", "endpoint", "rule" or "check".
	Kind string `json:"kind"`
	// Name names it the way the interface does: the trigger, endpoint or
	// rule name, or "node › check" for a check.
	Name string `json:"name"`
	// Detail says what it runs: "script (bash)", "git pull --ff-only",
	// "custom check".
	Detail string `json:"detail"`
}

// Plan works out what restoring the archive (m, cfg) onto st would do,
// without writing anything. checkRunsCode, when not nil, says which checks
// run code; it is the API's rule (the one that keeps such checks away from
// API keys), passed in because this package cannot import the API.
func Plan(ctx context.Context, st *store.Store, m Manifest, cfg Config, checkRunsCode func(model.Check) bool) (Preview, error) {
	p := Preview{Manifest: m, Accounts: []PreviewAccount{}, Agents: []string{}, APIKeys: []PreviewKey{},
		Commands: []PreviewCommand{}, TokenlessEndpoints: []string{}, AccessChanges: []string{}, Problems: []string{}}
	c := &p.Counts
	c.Nodes, c.Dashboards, c.Maintenance = len(cfg.Nodes), len(cfg.Dashboards), len(cfg.Maintenance)
	c.Triggers, c.Endpoints, c.Rules, c.Charts = len(cfg.Triggers), len(cfg.Endpoints), len(cfg.Rules), len(cfg.Charts)
	c.Wallboards, c.Reports, c.Incidents = len(cfg.Wallboards), len(cfg.Reports), len(cfg.Incidents)
	c.Users, c.APIKeys, c.Agents = len(cfg.Users), len(cfg.APIKeys), len(cfg.Agents)
	c.History, c.Results, c.Rollups, c.Events = m.IncludeHistory, m.Results, m.Rollups, m.Events

	nodeName := map[int64]string{}
	for _, n := range cfg.Nodes {
		c.Checks += len(n.Checks)
		nodeName[n.ID] = n.Name
		if checkRunsCode == nil {
			continue
		}
		for _, ch := range n.Checks {
			if checkRunsCode(ch) {
				detail := string(ch.Type) + " check"
				if ch.Type == model.CheckCustom {
					detail = "custom check"
				} else if ch.Type == model.CheckSystem {
					detail = "hardware check reading a URL"
				}
				p.Commands = append(p.Commands, PreviewCommand{Kind: "check", Name: n.Name + " › " + ch.Name, Detail: detail})
			}
		}
	}

	// Accounts and agents not on this server.
	users, err := st.CountUsers(ctx)
	if err != nil {
		return p, err
	}
	p.AccountsRecreatedAnyway = users == 0
	for _, u := range cfg.Users {
		if _, _, err := st.GetUserByName(ctx, u.Username); errors.Is(err, store.ErrNotFound) {
			p.Accounts = append(p.Accounts, PreviewAccount{Username: u.Username, Role: string(u.Role), Admin: auth.Role(u.Role) == auth.RoleAdmin})
		} else if err != nil {
			return p, err
		}
	}
	for _, a := range cfg.Agents {
		known, err := st.AgentTokenKnown(ctx, a.TokenHash)
		if err != nil {
			return p, err
		}
		if !known {
			p.Agents = append(p.Agents, a.Name)
		}
	}
	keys, err := st.ExportAPIKeys(ctx)
	if err != nil {
		return p, err
	}
	have := map[string]bool{}
	for _, k := range keys {
		have[k.KeyHash] = true
	}
	for _, k := range cfg.APIKeys {
		if !have[k.KeyHash] {
			p.APIKeys = append(p.APIKeys, PreviewKey{Name: k.Name, Prefix: k.Prefix, Scope: string(k.Scope), Revoked: k.RevokedAt != nil})
		}
	}

	// What runs a program here.
	for _, t := range cfg.Triggers {
		if d, ok := commandDetail(t.Action); ok {
			name := t.Name
			if n, ok := nodeName[t.NodeID]; ok {
				name = n + " › " + t.Name
			}
			p.Commands = append(p.Commands, PreviewCommand{Kind: "trigger", Name: name, Detail: d})
		}
	}
	for _, e := range cfg.Endpoints {
		if d, ok := commandDetail(e.Action); ok {
			p.Commands = append(p.Commands, PreviewCommand{Kind: "endpoint", Name: e.Name + " (/hook/" + e.Slug + ")", Detail: d})
		}
		if e.AllowNoToken && e.Token == "" {
			p.TokenlessEndpoints = append(p.TokenlessEndpoints, e.Slug)
		}
	}
	for _, r := range cfg.Rules {
		for i, a := range r.Actions {
			if d, ok := commandDetail(a); ok {
				p.Commands = append(p.Commands, PreviewCommand{Kind: "rule", Name: fmt.Sprintf("%s (action %d)", r.Name, i+1), Detail: d})
			}
		}
	}

	current, err := st.LoadSettings(ctx)
	if err != nil {
		return p, err
	}
	p.AccessChanges = accessChanges(current.General, cfg.Settings.General)
	return p, nil
}

// commandDetail says what an action runs, for the actions that run a program
// on this computer (a script or a git command).
func commandDetail(a model.Action) (string, bool) {
	switch a.Type {
	case model.ActionScript:
		if a.Interpreter == "" {
			return "script", true
		}
		return "script (" + a.Interpreter + ")", true
	case model.ActionGit:
		return "git " + a.GitArgs, true
	}
	return "", false
}

// accessChanges compares the network-access and sign-in settings in force
// with an archive's, one sentence per difference. The trusted reverse
// proxies are not among them: they are a command-line setting
// (--trusted-proxies), never stored in the database, so no archive carries
// them.
func accessChanges(before, after model.GeneralSettings) []string {
	out := []string{}
	onOff := func(v bool) string {
		if v {
			return "on"
		}
		return "off"
	}
	if before.RemoteAccess != after.RemoteAccess {
		if after.RemoteAccess {
			out = append(out, "LAN access is turned on: other devices on the network will be able to open GWatch.")
		} else {
			out = append(out, "LAN access is turned off: only this computer will be able to open GWatch.")
		}
	}
	switch {
	case before.AccessPassword == "" && after.AccessPassword != "":
		out = append(out, "An access password for other devices is set.")
	case before.AccessPassword != "" && after.AccessPassword == "":
		out = append(out, "The access password for other devices is removed.")
	case before.AccessPassword != after.AccessPassword:
		out = append(out, "The access password for other devices is replaced by the one in the backup.")
	}
	if before.RequireLoginLocally != after.RequireLoginLocally {
		out = append(out, "Signing in on this computer is turned "+onOff(after.RequireLoginLocally)+".")
	}
	if before.BehindHTTPSProxy != after.BehindHTTPSProxy {
		out = append(out, "\"Behind an HTTPS proxy\" is turned "+onOff(after.BehindHTTPSProxy)+".")
	}
	if before.ListenPort != after.ListenPort {
		port := "the start-up default"
		if after.ListenPort != 0 {
			port = fmt.Sprintf("%d", after.ListenPort)
		}
		out = append(out, "The listening port changes to "+port+".")
	}
	if normHashing(before.PasswordHashing) != normHashing(after.PasswordHashing) {
		out = append(out, "Password hashing changes to "+normHashing(after.PasswordHashing)+".")
	}
	return out
}

func normHashing(v string) string {
	if v == "" {
		return string(auth.Argon2id)
	}
	return v
}

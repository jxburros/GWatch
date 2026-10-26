package main

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/sysmetrics"
)

// Watching services and programs (#62) from the agent's side: the machine's
// own file is the ceiling, GWatch chooses within it, and what GWatch is
// offered to choose from stays within it too.

func TestWatchedNamesFollowTheCeilingRule(t *testing.T) {
	rt := &agentRuntime{
		ceiling:   model.CollectSelection{Services: []string{"nginx", "smbd"}},
		selection: model.CollectSelection{Services: []string{"smbd", "docker"}, Processes: []string{"plex"}},
	}
	eff := rt.effective()
	if !reflect.DeepEqual(eff.WatchedServices(), []string{"smbd"}) {
		t.Errorf("GWatch can only choose within the machine's list: %v", eff.Services)
	}
	if !reflect.DeepEqual(eff.WatchedProcesses(), []string{"plex"}) {
		t.Errorf("no ceiling on programs, so GWatch's choice stands: %v", eff.Processes)
	}

	// Containers and guests follow the same rule.
	rt.ceiling.Containers = []string{"web"}
	rt.selection.Containers, rt.selection.VMs = []string{"web", "db"}, []string{"dc01"}
	if eff := rt.effective(); !reflect.DeepEqual(eff.WatchedContainers(), []string{"web"}) || !reflect.DeepEqual(eff.WatchedVMs(), []string{"dc01"}) {
		t.Errorf("containers and guests: %+v", eff)
	}
	rt.ceiling.Containers = nil

	// Nothing in common is nothing watched, not everything.
	rt.selection = model.CollectSelection{Services: []string{"docker"}}
	if got := rt.effective(); got.Watching() || len(got.WatchedServices()) != 0 {
		t.Errorf("nothing in common should watch nothing: %+v", got)
	}
}

func TestWatchInventoryStaysWithinTheMachinesList(t *testing.T) {
	items := []model.InventoryItem{{Name: "nginx", Detail: "running"}, {Name: "smbd", Detail: "stopped"}, {Name: "cron", Detail: "running"}}
	if got := withinCeiling(items, nil); len(got) != 3 {
		t.Errorf("no ceiling offers everything: %+v", got)
	}
	got := withinCeiling(items, []string{"NGINX", "absent"})
	want := []model.InventoryItem{{Name: "NGINX", Detail: "running"}, {Name: "absent"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestThePanelListsWatchableNamesOnRequest(t *testing.T) {
	p := newPanel(t)
	old := listWatchable
	calls := 0
	listWatchable = func(context.Context) sysmetrics.Watchable {
		calls++
		return sysmetrics.Watchable{
			Services:   []model.InventoryItem{{Name: "nginx", Detail: "running"}},
			Processes:  []model.InventoryItem{{Name: "plex", Detail: "1 running"}},
			Containers: []model.InventoryItem{{Name: "web", Detail: "running · nginx:1.27"}},
		}
	}
	defer func() { listWatchable = old }()

	// Opening the panel does not list anything; asking does.
	p.ok("GET", "/api/state", nil)
	if calls != 0 {
		t.Fatal("the state must not list services")
	}
	var got struct {
		Services   []model.InventoryItem `json:"services"`
		Processes  []model.InventoryItem `json:"processes"`
		Containers []model.InventoryItem `json:"containers"`
	}
	json.Unmarshal(p.ok("GET", "/api/watchable", nil), &got)
	if calls != 1 || len(got.Services) != 1 || got.Processes[0].Name != "plex" || got.Containers[0].Name != "web" {
		t.Errorf("got %+v after %d calls", got, calls)
	}
	if resp, _ := p.do("GET", "/api/watchable", nil, opts{}); resp.StatusCode != 403 {
		t.Errorf("without the cookie: %d", resp.StatusCode)
	}

	// The names chosen are saved with the rest of the collection settings.
	p.ok("PUT", "/api/settings", map[string]any{"collect": map[string]any{"services": []string{"nginx"}, "processes": []string{"plex"}, "containers": []string{"web"}, "vms": []string{"dc01"}}})
	c, problems, _ := baseConfig()
	if len(problems) > 0 || !reflect.DeepEqual(c.collect.Services, []string{"nginx"}) || !reflect.DeepEqual(c.collect.Processes, []string{"plex"}) ||
		!reflect.DeepEqual(c.collect.Containers, []string{"web"}) || !reflect.DeepEqual(c.collect.VMs, []string{"dc01"}) {
		t.Errorf("saved: %+v %v", c.collect, problems)
	}
	resp, _ := p.do("PUT", "/api/settings", map[string]any{"collect": map[string]any{"services": []string{"--help"}}}, opts{cookie: true, header: true})
	if resp.StatusCode != 400 {
		t.Errorf("a name that reads as an option should be refused: %d", resp.StatusCode)
	}
}

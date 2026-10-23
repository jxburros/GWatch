package api

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
)

// A machine being paired gets its node at once, with a switched-off hardware
// check that has no machine yet. The server's own backup of that state, or of
// the cancelled or redeemed code, must restore, both onto the same server and
// onto a clean one (QA-01).
func TestRestoreBackupWithPairedMachineNode(t *testing.T) {
	for _, ending := range []string{"pending", "cancelled", "redeemed"} {
		t.Run(ending, func(t *testing.T) {
			ts, srv := newTestServer(t)
			var created struct {
				Code    string            `json:"code"`
				Pairing model.PairingCode `json:"pairing"`
			}
			if code := call(t, ts, "POST", "/api/agents/pairings", map[string]any{"name": "Waiting for agent"}, &created); code != http.StatusCreated {
				t.Fatalf("create pairing: %d", code)
			}
			switch ending {
			case "cancelled":
				if code := call(t, ts, "DELETE", fmt.Sprintf("/api/agents/pairings/%d", created.Pairing.ID), nil, nil); code != http.StatusNoContent {
					t.Fatalf("cancel pairing: %d", code)
				}
			case "redeemed":
				if code := call(t, ts, "POST", "/api/agents/pair", map[string]any{"code": created.Code, "hostname": "pc", "os": "windows", "arch": "amd64"}, nil); code != http.StatusCreated {
					t.Fatalf("pair: %d", code)
				}
			}

			var info model.BackupInfo
			if code := call(t, ts, "POST", "/api/backups", map[string]any{"password": "pw", "includeHistory": true}, &info); code != http.StatusCreated {
				t.Fatalf("backup: %d", code)
			}
			var out map[string]any
			if code := call(t, ts, "POST", "/api/backups/restore-existing", map[string]any{"fileName": info.FileName, "password": "pw", "includeHistory": true}, &out); code != http.StatusOK {
				t.Fatalf("restore onto the same server: %d %v", code, out)
			}
			wantMachineNode(t, srv, ending == "redeemed")
			if n := eventsTitled(t, srv, "Pre-restore backup created"); n != 1 {
				t.Errorf("%d \"Pre-restore backup created\" events after the restore, want 1", n)
			}

			clean, cleanSrv := newTestServer(t)
			status, res := postArchive(t, clean.URL+"/api/backups/restore", filepath.Join(srv.BackupDir, info.FileName), map[string]string{"password": "pw", "includeHistory": "true"})
			if status != http.StatusOK {
				t.Fatalf("restore onto a clean server: %d %v", status, res)
			}
			wantMachineNode(t, cleanSrv, ending == "redeemed")
		})
	}
}

func wantMachineNode(t *testing.T, srv *Server, bound bool) {
	t.Helper()
	nodes, err := srv.Store.ListNodes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Name != "Waiting for agent" || len(nodes[0].Checks) != 1 {
		t.Fatalf("restored nodes: %+v", nodes)
	}
	c := nodes[0].Checks[0]
	if bound != (c.Config.AgentID > 0) || bound != c.Enabled {
		t.Errorf("hardware check after restore: enabled %v, agent %d (want bound %v)", c.Enabled, c.Config.AgentID, bound)
	}
}

func eventsTitled(t *testing.T, srv *Server, title string) int {
	t.Helper()
	events, err := srv.Store.ListEvents(context.Background(), store.EventFilter{Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range events {
		if e.Title == title {
			n++
		}
	}
	return n
}

// The relaxation is only for a check that is switched off: one that is on
// must still name its machine.
func TestEnabledAgentCheckStillNeedsAMachine(t *testing.T) {
	ts, _ := newTestServer(t)
	check := machineCheck(true)
	node := map[string]any{"name": "PC", "enabled": true, "checks": []model.Check{check}}
	if code := call(t, ts, "POST", "/api/nodes", node, nil); code != http.StatusBadRequest {
		t.Fatalf("an enabled hardware check with no machine was accepted: %d", code)
	}
	check.Enabled = false
	node["checks"] = []model.Check{check}
	if code := call(t, ts, "POST", "/api/nodes", node, nil); code != http.StatusCreated {
		t.Fatalf("a switched-off check waiting for its machine was refused: %d", code)
	}
}

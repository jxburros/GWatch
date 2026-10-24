package backup

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jxburros/GWatch/internal/auth"
	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/store"
	"github.com/jxburros/GWatch/internal/store/storetest"
)

// #148: restoring an older backup must not bring back an account or an agent
// deleted since, unless that is asked for; and an agent that is still here
// gets its node link back after the nodes are restored.
func TestRestoreLeavesDeletedAccountsOutAndRelinksAgents(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st := storetest.Open(t)
	n, err := st.CreateNode(ctx, model.Node{Name: "NAS", Host: "10.0.0.5", Enabled: true,
		Checks: []model.Check{{Type: model.CheckPing, Name: "Ping", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUser(ctx, "admin", "$argon2id$x", auth.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	gone, err := st.CreateUser(ctx, "ex-admin", "$argon2id$y", auth.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	kept, err := st.CreateAgent(ctx, "nas", &n.ID, "gwa_aaaa", "hash-kept", "admin")
	if err != nil {
		t.Fatal(err)
	}
	purged, err := st.CreateAgent(ctx, "stolen laptop", nil, "gwa_bbbb", "hash-purged", "admin")
	if err != nil {
		t.Fatal(err)
	}
	info, err := Create(ctx, st, dir, "pw", false, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteUser(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteAgent(ctx, purged.ID); err != nil {
		t.Fatal(err)
	}

	sum, err := RestoreWith(ctx, st, filepath.Join(dir, info.FileName), "pw", RestoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sum.SkippedUsers, []string{"ex-admin"}) || !slices.Equal(sum.SkippedAgents, []string{"stolen laptop"}) {
		t.Fatalf("skipped users %v, agents %v", sum.SkippedUsers, sum.SkippedAgents)
	}
	if _, _, err := st.GetUserByName(ctx, "ex-admin"); err != store.ErrNotFound {
		t.Fatalf("a deleted account came back: %v", err)
	}
	if known, _ := st.AgentTokenKnown(ctx, "hash-purged"); known {
		t.Fatal("a purged agent came back")
	}
	a, err := st.GetAgent(ctx, kept.ID)
	if err != nil {
		t.Fatal(err)
	}
	if a.NodeID == nil || *a.NodeID != n.ID {
		t.Fatalf("the agent lost its node: %+v", a.NodeID)
	}

	// Asked for, they come back.
	sum, err = RestoreWith(ctx, st, filepath.Join(dir, info.FileName), "pw", RestoreOptions{RecreateAccounts: true})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Users != 1 || sum.Agents != 1 || len(sum.SkippedUsers)+len(sum.SkippedAgents) != 0 {
		t.Fatalf("recreate: %+v", sum)
	}
}

// A node's "check from" names an agent by id, and agent ids are not kept
// across a restore onto a server that already has agents. The node has to
// stay with the machine it named in the archive — never be handed (with its
// credentials) to whichever local agent holds the old number.
func TestRestoreKeepsNodeCollectorOnTheSameMachine(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	src := storetest.Open(t)
	if _, err := src.CreateAgent(ctx, "office pc", nil, "gwa_1111", "hash-office", "admin"); err != nil {
		t.Fatal(err)
	}
	coll, err := src.CreateAgent(ctx, "branch collector", nil, "gwa_2222", "hash-branch", "admin")
	if err != nil {
		t.Fatal(err)
	}
	left, err := src.CreateAgent(ctx, "lab collector", nil, "gwa_3333", "hash-lab", "admin")
	if err != nil {
		t.Fatal(err)
	}
	mk := func(name string, collector int64) model.Node {
		n, err := src.CreateNode(ctx, model.Node{Name: name, Host: "10.1.0.1", Enabled: true, CollectorID: &collector,
			Checks: []model.Check{{Type: model.CheckPing, Name: "Ping", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5}}})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	branch := mk("branch router", coll.ID)
	lab := mk("lab switch", left.ID)
	// A hardware check names the machine it reads the same way.
	hw, err := src.CreateNode(ctx, model.Node{Name: "branch box", Enabled: true,
		Checks: []model.Check{{Type: model.CheckSystem, Name: "Hardware", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5,
			Config: model.CheckConfig{HostSource: model.HostSourceAgent, AgentID: coll.ID}}}})
	if err != nil {
		t.Fatal(err)
	}
	var dst *store.Store
	hwAgent := func() int64 {
		t.Helper()
		c, err := dst.GetCheck(ctx, hw.Checks[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		return c.Config.AgentID
	}
	info, err := Create(ctx, src, dir, "pw", false, "test")
	if err != nil {
		t.Fatal(err)
	}

	// The target already has three different machines, holding ids 1–3.
	dst = storetest.Open(t)
	var others []model.Agent
	for i, h := range []string{"hash-x", "hash-y", "hash-z"} {
		a, err := dst.CreateAgent(ctx, fmt.Sprintf("other %d", i), nil, "gwa_x", h, "admin")
		if err != nil {
			t.Fatal(err)
		}
		others = append(others, a)
	}
	if others[1].ID != coll.ID || others[2].ID != left.ID {
		t.Fatalf("test setup: ids %d,%d vs %d,%d", others[1].ID, others[2].ID, coll.ID, left.ID)
	}

	// Re-created, the archive's machines get new ids (theirs are taken), and
	// the node follows its collector to the new one.
	if _, err := RestoreWith(ctx, dst, filepath.Join(dir, info.FileName), "pw", RestoreOptions{RecreateAccounts: true}); err != nil {
		t.Fatal(err)
	}
	local, err := dst.AgentByTokenHash(ctx, "hash-branch")
	if err != nil {
		t.Fatal(err)
	}
	n, err := dst.GetNode(ctx, branch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n.CollectorID == nil {
		t.Fatalf("branch router checks from this server, want the branch collector (agent %d)", local.ID)
	}
	if *n.CollectorID != local.ID {
		t.Fatalf("branch router checks from agent %d, want the branch collector (agent %d)", *n.CollectorID, local.ID)
	}
	if got := hwAgent(); got != local.ID {
		t.Fatalf("hardware check reads agent %d, want %d", got, local.ID)
	}

	// Restored without re-creating machines, the archive's collectors stay
	// out; their nodes must not fall to the local agents holding their old
	// ids.
	for _, h := range []string{"hash-office", "hash-branch", "hash-lab"} {
		a, err := dst.AgentByTokenHash(ctx, h)
		if err != nil {
			t.Fatal(err)
		}
		if err := dst.DeleteAgent(ctx, a.ID); err != nil {
			t.Fatal(err)
		}
	}
	sum, err := RestoreWith(ctx, dst, filepath.Join(dir, info.FileName), "pw", RestoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.SkippedAgents) != 3 {
		t.Fatalf("skipped %v", sum.SkippedAgents)
	}
	for _, id := range []int64{branch.ID, lab.ID} {
		n, err := dst.GetNode(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if n.CollectorID != nil {
			t.Fatalf("%s checks from agent %d, a different machine than the archive named", n.Name, *n.CollectorID)
		}
	}
	if got := hwAgent(); got != 0 {
		t.Fatalf("hardware check reads agent %d, a different machine than the archive named", got)
	}
}

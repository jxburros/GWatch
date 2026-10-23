package backup

import (
	"context"
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

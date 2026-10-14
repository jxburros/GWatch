package store

import (
	"context"
	"testing"

	"github.com/jxburros/GWatch/internal/model"
)

func TestLinkStylePersistsThroughEveryWritePath(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	parent, err := s.CreateNode(ctx, model.Node{Name: "Gateway", Host: "10.0.0.1", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	want := &model.LinkStyle{Color: "#3b82f6", Width: 3, Dash: model.LinkDashDashed}
	child, err := s.CreateNode(ctx, model.Node{Name: "NAS", Host: "10.0.0.5", Enabled: true, DependsOnNode: &parent.ID, LinkStyle: want})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetNode(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.LinkStyle.Equal(want) {
		t.Fatalf("created link style = %+v, want %+v", got.LinkStyle, want)
	}
	if p, _ := s.GetNode(ctx, parent.ID); p.LinkStyle != nil {
		t.Fatalf("a node that never set one has %+v", p.LinkStyle)
	}

	// UpdateNode replaces it, and a nil one clears it.
	got.LinkStyle = &model.LinkStyle{Dash: model.LinkDashDotted}
	if _, _, err := s.UpdateNode(ctx, got); err != nil {
		t.Fatal(err)
	}
	again, _ := s.GetNode(ctx, child.ID)
	if !again.LinkStyle.Equal(&model.LinkStyle{Dash: model.LinkDashDotted}) {
		t.Fatalf("updated link style = %+v", again.LinkStyle)
	}
	again.LinkStyle = nil
	if _, _, err := s.UpdateNode(ctx, again); err != nil {
		t.Fatal(err)
	}
	if cleared, _ := s.GetNode(ctx, child.ID); cleared.LinkStyle != nil {
		t.Fatalf("a cleared link style came back as %+v", cleared.LinkStyle)
	}

	// BulkUpdate writes node rows too, and is what the network map's panel uses.
	again.LinkStyle = &model.LinkStyle{Color: "#abc", Width: 2}
	if _, err := s.BulkUpdate(ctx, []model.Node{again}, nil); err != nil {
		t.Fatal(err)
	}
	if bulk, _ := s.GetNode(ctx, child.ID); !bulk.LinkStyle.Equal(&model.LinkStyle{Color: "#abc", Width: 2}) {
		t.Fatalf("bulk-updated link style = %+v", bulk.LinkStyle)
	}

	// And it is in the list the overview, the export and a backup read.
	all, err := s.ListNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range all {
		if n.ID == child.ID && !n.LinkStyle.Equal(&model.LinkStyle{Color: "#abc", Width: 2}) {
			t.Fatalf("listed link style = %+v", n.LinkStyle)
		}
	}
}

func TestLinkStyleIsSanitisedOnTheWayInAndOut(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	parent, _ := s.CreateNode(ctx, model.Node{Name: "Router", Enabled: true})

	// A style the API would have refused never gets into the row...
	n, err := s.CreateNode(ctx, model.Node{Name: "Cam", Enabled: true, DependsOnNode: &parent.ID, LinkStyle: &model.LinkStyle{Color: "red;fill:url(#x)", Width: 40, Dash: "wavy"}})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetNode(ctx, n.ID)
	if !got.LinkStyle.Equal(&model.LinkStyle{Width: model.MaxLinkWidth}) {
		t.Fatalf("link style = %+v, want only the clamped width", got.LinkStyle)
	}

	// ... and one that got there by another road (a hand-edited database)
	// is cleaned when it is read.
	if _, err := s.exec(ctx, "UPDATE nodes SET link_style = ? WHERE id = ?", `{"color":"javascript:alert(1)","width":0.2,"dash":"DOTTED"}`, n.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetNode(ctx, n.ID)
	if !got.LinkStyle.Equal(&model.LinkStyle{Width: model.MinLinkWidth, Dash: model.LinkDashDotted}) {
		t.Fatalf("link style read back = %+v", got.LinkStyle)
	}
	if _, err := s.exec(ctx, "UPDATE nodes SET link_style = ? WHERE id = ?", `not json`, n.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.GetNode(ctx, n.ID); got.LinkStyle != nil {
		t.Fatalf("an unreadable link style came back as %+v", got.LinkStyle)
	}
}

func TestDeletingAParentTakesTheLinkStyleOfItsChildrenWithIt(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	parent, _ := s.CreateNode(ctx, model.Node{Name: "Switch", Enabled: true})
	other, _ := s.CreateNode(ctx, model.Node{Name: "Router", Enabled: true})
	style := &model.LinkStyle{Color: "#ff0000", Width: 4}
	child, _ := s.CreateNode(ctx, model.Node{Name: "Printer", Enabled: true, DependsOnNode: &parent.ID, LinkStyle: style})
	sibling, _ := s.CreateNode(ctx, model.Node{Name: "Camera", Enabled: true, DependsOnNode: &other.ID, LinkStyle: style})

	if err := s.DeleteNode(ctx, parent.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetNode(ctx, child.ID)
	if got.DependsOnNode != nil || got.LinkStyle != nil {
		t.Fatalf("after its parent was deleted the child has dependency %v and style %+v", got.DependsOnNode, got.LinkStyle)
	}
	if kept, _ := s.GetNode(ctx, sibling.ID); !kept.LinkStyle.Equal(style) {
		t.Fatalf("a node under another parent lost its style: %+v", kept.LinkStyle)
	}
	if err := s.DeleteNode(ctx, parent.ID); err != ErrNotFound {
		t.Fatalf("deleting it again = %v, want ErrNotFound", err)
	}
}

// An install from before link styles has a nodes table without the column.
func TestLinkStyleColumnIsAddedToAnOlderDatabase(t *testing.T) {
	cfg := testConfig(t)
	rawOldShapeDB(t, cfg)
	db, _ := rawDB(t, cfg)
	if _, err := db.Exec("ALTER TABLE nodes DROP COLUMN link_style"); err != nil {
		t.Fatalf("make the table the way it was: %v", err)
	}
	db.Close()

	ctx := context.Background()
	s := openCfg(t, cfg)
	n, err := s.GetNode(ctx, 1)
	if err != nil {
		t.Fatalf("read a node from the older database: %v", err)
	}
	if n.LinkStyle != nil {
		t.Fatalf("a node from before link styles has %+v", n.LinkStyle)
	}
	n.LinkStyle = &model.LinkStyle{Dash: model.LinkDashDashDot}
	if _, _, err := s.UpdateNode(ctx, n); err != nil {
		t.Fatalf("save a link style on the older database: %v", err)
	}
	if got, _ := s.GetNode(ctx, 1); !got.LinkStyle.Equal(&model.LinkStyle{Dash: model.LinkDashDashDot}) {
		t.Fatalf("link style = %+v", got.LinkStyle)
	}
}

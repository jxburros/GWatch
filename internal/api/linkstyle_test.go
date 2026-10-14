package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/jxburros/GWatch/internal/backup"
	"github.com/jxburros/GWatch/internal/engine"
	"github.com/jxburros/GWatch/internal/model"
)

// newLinkedNodes makes a parent and a child that depends on it.
func newLinkedNodes(t *testing.T, ts *httptest.Server) (parent, child nodeDoc) {
	t.Helper()
	if code := call(t, ts, "POST", "/api/nodes", map[string]any{"name": "Router", "host": "10.0.0.1", "enabled": true, "checks": []map[string]any{}}, &parent); code != 201 {
		t.Fatalf("create parent: %d", code)
	}
	if code := call(t, ts, "POST", "/api/nodes", map[string]any{"name": "NAS", "host": "10.0.0.5", "enabled": true, "dependsOnNodeId": parent.ID, "checks": []map[string]any{}}, &child); code != 201 {
		t.Fatalf("create child: %d", code)
	}
	return parent, child
}

func TestLinkStyleRoundTripsThroughTheNodeAPI(t *testing.T) {
	ts, _ := newTestServer(t)
	parent, _ := newLinkedNodes(t, ts)

	var created nodeDoc
	body := map[string]any{"name": "Camera", "host": "10.0.0.9", "enabled": true, "dependsOnNodeId": parent.ID,
		"linkStyle": map[string]any{"color": "#E5A50A", "width": 3, "dash": "Dotted"}, "checks": []map[string]any{}}
	if code := call(t, ts, "POST", "/api/nodes", body, &created); code != 201 {
		t.Fatalf("create: %d", code)
	}
	want := &model.LinkStyle{Color: "#e5a50a", Width: 3, Dash: "dotted"}
	if !created.LinkStyle.Equal(want) {
		t.Fatalf("created link style = %+v, want %+v (lower-cased)", created.LinkStyle, want)
	}
	var got nodeDoc
	call(t, ts, "GET", fmt.Sprintf("/api/nodes/%d", created.ID), nil, &got)
	if !got.LinkStyle.Equal(want) {
		t.Fatalf("read back %+v", got.LinkStyle)
	}

	// The overview the network map draws from carries it too.
	var ov engine.Overview
	call(t, ts, "GET", "/api/overview", nil, &ov)
	found := false
	for _, nv := range ov.Nodes {
		if nv.Node.ID == created.ID {
			found = true
			if !nv.Node.LinkStyle.Equal(want) {
				t.Fatalf("overview link style = %+v", nv.Node.LinkStyle)
			}
		}
	}
	if !found {
		t.Fatal("node missing from the overview")
	}

	// Saving the node whole replaces it; a width out of range is pulled in.
	got.LinkStyle = &model.LinkStyle{Width: 50}
	var updated nodeDoc
	if code := call(t, ts, "PUT", fmt.Sprintf("/api/nodes/%d", created.ID), got.Node, &updated); code != 200 {
		t.Fatalf("update: %d", code)
	}
	if !updated.LinkStyle.Equal(&model.LinkStyle{Width: model.MaxLinkWidth}) {
		t.Fatalf("clamped link style = %+v", updated.LinkStyle)
	}

	// Taking the dependency away takes the style of the link with it.
	updated.DependsOnNode = nil
	var unlinked nodeDoc
	if code := call(t, ts, "PUT", fmt.Sprintf("/api/nodes/%d", created.ID), updated.Node, &unlinked); code != 200 {
		t.Fatalf("unlink: %d", code)
	}
	if unlinked.LinkStyle != nil {
		t.Fatalf("a node with no parent still has style %+v", unlinked.LinkStyle)
	}

	// A copy keeps the dependency and so the style.
	var dup nodeDoc
	if code := call(t, ts, "POST", fmt.Sprintf("/api/nodes/%d/duplicate", created.ID), nil, &dup); code != 201 {
		t.Fatalf("duplicate: %d", code)
	}
	var child nodeDoc
	call(t, ts, "POST", "/api/nodes", map[string]any{"name": "Cam 2", "host": "x", "enabled": true, "dependsOnNodeId": parent.ID, "linkStyle": map[string]any{"dash": "dashed"}, "checks": []map[string]any{}}, &child)
	var copyOfChild nodeDoc
	call(t, ts, "POST", fmt.Sprintf("/api/nodes/%d/duplicate", child.ID), nil, &copyOfChild)
	if !copyOfChild.LinkStyle.Equal(&model.LinkStyle{Dash: "dashed"}) {
		t.Fatalf("duplicate link style = %+v", copyOfChild.LinkStyle)
	}
}

func TestLinkStyleIsRefusedWhenItIsNotAColourOrADash(t *testing.T) {
	ts, _ := newTestServer(t)
	parent, child := newLinkedNodes(t, ts)
	for name, style := range map[string]map[string]any{
		"a colour name":      {"color": "red"},
		"a css function":     {"color": "rgb(0,0,0)"},
		"an injected style":  {"color": "#fff;stroke-width:99"},
		"a short hex":        {"color": "#12"},
		"an unknown dash":    {"dash": "wavy"},
		"a free-form dash":   {"dash": "5 4"},
		"an injected dash":   {"dash": "solid;fill:red"},
		"a width that isn't": {"width": "thick"},
	} {
		body := map[string]any{"name": "Bad", "host": "x", "enabled": true, "dependsOnNodeId": parent.ID, "linkStyle": style, "checks": []map[string]any{}}
		if code := call(t, ts, "POST", "/api/nodes", body, nil); code != 400 {
			t.Errorf("create with %s = %d, want 400", name, code)
		}
		var got nodeDoc
		call(t, ts, "GET", fmt.Sprintf("/api/nodes/%d", child.ID), nil, &got)
		put := map[string]any{"name": got.Name, "host": got.Host, "enabled": true, "dependsOnNodeId": parent.ID, "linkStyle": style, "checks": []map[string]any{}}
		if code := call(t, ts, "PUT", fmt.Sprintf("/api/nodes/%d", child.ID), put, nil); code != 400 {
			t.Errorf("update with %s = %d, want 400", name, code)
		}
		if code := call(t, ts, "PATCH", "/api/nodes/bulk", map[string]any{"nodeIds": []int64{child.ID}, "node": map[string]any{"linkStyle": style}}, nil); code != 400 {
			t.Errorf("bulk patch with %s = %d, want 400", name, code)
		}
	}
	var got nodeDoc
	call(t, ts, "GET", fmt.Sprintf("/api/nodes/%d", child.ID), nil, &got)
	if got.LinkStyle != nil {
		t.Fatalf("a refused style was kept: %+v", got.LinkStyle)
	}
}

func TestBulkPatchSetsAndClearsLinkStyle(t *testing.T) {
	ts, _ := newTestServer(t)
	parent, child := newLinkedNodes(t, ts)
	var other nodeDoc
	call(t, ts, "POST", "/api/nodes", map[string]any{"name": "Printer", "host": "10.0.0.6", "enabled": true, "dependsOnNodeId": parent.ID, "checks": []map[string]any{}}, &other)

	var res bulkResponse
	set := map[string]any{"nodeIds": []int64{child.ID}, "node": map[string]any{"linkStyle": map[string]any{"color": "#3b82f6", "width": 4}}}
	if code := call(t, ts, "PATCH", "/api/nodes/bulk", set, &res); code != 200 || len(res.Changes) != 1 || res.Changes[0] != "link style changed" {
		t.Fatalf("set: %d %+v", code, res)
	}
	var got, untouched nodeDoc
	call(t, ts, "GET", fmt.Sprintf("/api/nodes/%d", child.ID), nil, &got)
	call(t, ts, "GET", fmt.Sprintf("/api/nodes/%d", other.ID), nil, &untouched)
	if !got.LinkStyle.Equal(&model.LinkStyle{Color: "#3b82f6", Width: 4}) || untouched.LinkStyle != nil {
		t.Fatalf("after set: %+v / %+v", got.LinkStyle, untouched.LinkStyle)
	}

	// A patch that does not mention it leaves it alone; one that sets it to
	// null (what the Reset button sends) clears it.
	if code := call(t, ts, "PATCH", "/api/nodes/bulk", map[string]any{"nodeIds": []int64{child.ID}, "node": map[string]any{"tags": []string{"a"}}}, nil); code != 200 {
		t.Fatalf("tags: %d", code)
	}
	call(t, ts, "GET", fmt.Sprintf("/api/nodes/%d", child.ID), nil, &got)
	if got.LinkStyle == nil {
		t.Fatal("an unrelated patch cleared the link style")
	}
	if code := call(t, ts, "PATCH", "/api/nodes/bulk", map[string]any{"nodeIds": []int64{child.ID}, "node": map[string]any{"linkStyle": nil}}, &res); code != 200 || res.Changes[0] != "link style reset" {
		t.Fatalf("clear: %d %+v", code, res)
	}
	call(t, ts, "GET", fmt.Sprintf("/api/nodes/%d", child.ID), nil, &got)
	if got.LinkStyle != nil {
		t.Fatalf("not cleared: %+v", got.LinkStyle)
	}

	// Clearing the dependency in the same patch as a style leaves no style.
	set["node"] = map[string]any{"linkStyle": map[string]any{"dash": "dashed"}, "dependsOnNodeId": nil}
	if code := call(t, ts, "PATCH", "/api/nodes/bulk", set, nil); code != 200 {
		t.Fatalf("both: %d", code)
	}
	call(t, ts, "GET", fmt.Sprintf("/api/nodes/%d", child.ID), nil, &got)
	if got.DependsOnNode != nil || got.LinkStyle != nil {
		t.Fatalf("a link that is gone kept its style: dep %v style %+v", got.DependsOnNode, got.LinkStyle)
	}
	// Pointing the node at another parent keeps what was chosen.
	set["node"] = map[string]any{"dependsOnNodeId": parent.ID, "linkStyle": map[string]any{"dash": "dashed"}}
	call(t, ts, "PATCH", "/api/nodes/bulk", set, nil)
	call(t, ts, "PATCH", "/api/nodes/bulk", map[string]any{"nodeIds": []int64{child.ID}, "node": map[string]any{"dependsOnNodeId": other.ID}}, nil)
	call(t, ts, "GET", fmt.Sprintf("/api/nodes/%d", child.ID), nil, &got)
	if got.DependsOnNode == nil || *got.DependsOnNode != other.ID || !got.LinkStyle.Equal(&model.LinkStyle{Dash: "dashed"}) {
		t.Fatalf("after re-parenting: dep %v style %+v", got.DependsOnNode, got.LinkStyle)
	}
}

func TestLinkStyleIsInTheConfigurationExport(t *testing.T) {
	ts, srv := newTestServer(t)
	parent, _ := newLinkedNodes(t, ts)
	var styled nodeDoc
	call(t, ts, "POST", "/api/nodes", map[string]any{"name": "Camera", "host": "x", "enabled": true, "dependsOnNodeId": parent.ID, "linkStyle": map[string]any{"color": "#0a0", "dash": "dash-dot"}, "checks": []map[string]any{}}, &styled)

	var doc struct {
		Nodes []model.Node `json:"nodes"`
	}
	if code := call(t, ts, "GET", "/api/export/config.json", nil, &doc); code != 200 {
		t.Fatalf("export: %d", code)
	}
	var found *model.Node
	for i := range doc.Nodes {
		if doc.Nodes[i].ID == styled.ID {
			found = &doc.Nodes[i]
		}
	}
	if found == nil || !found.LinkStyle.Equal(&model.LinkStyle{Color: "#0a0", Dash: "dash-dot"}) {
		t.Fatalf("exported node = %+v", found)
	}
	// And what the export holds is what a restore reads.
	cfg, err := backup.ExportConfig(context.Background(), srv.Store)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(cfg)
	var back backup.Config
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	for _, n := range back.Nodes {
		if n.ID == styled.ID && !n.LinkStyle.Equal(&model.LinkStyle{Color: "#0a0", Dash: "dash-dot"}) {
			t.Fatalf("round-tripped through JSON: %+v", n.LinkStyle)
		}
	}
}

func TestNetworkMapWidgetLineSettingsAreChecked(t *testing.T) {
	ts, _ := newTestServer(t)
	widget := func(cfg string) map[string]any {
		return map[string]any{"name": "Board", "widgets": []map[string]any{{"id": "w1", "type": "network_map", "title": "Map", "width": 4, "height": 3, "config": json.RawMessage(cfg)}}}
	}
	var saved model.Dashboard
	if code := call(t, ts, "POST", "/api/dashboards", widget(`{"orientation":"radial","lineColor":"#3B82F6","lineWidth":30,"lineStyle":"dashed"}`), &saved); code != 200 {
		t.Fatalf("save: %d", code)
	}
	var cfg map[string]any
	if err := json.Unmarshal(saved.Widgets[0].Config, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["lineColor"] != "#3b82f6" || cfg["lineWidth"] != float64(8) || cfg["lineStyle"] != "dashed" || cfg["orientation"] != "radial" {
		t.Fatalf("stored config = %s", saved.Widgets[0].Config)
	}
	for _, bad := range []string{`{"lineColor":"red"}`, `{"lineColor":"#fff;x"}`, `{"lineStyle":"wavy"}`, `{"lineWidth":"x"}`} {
		if code := call(t, ts, "POST", "/api/dashboards", widget(bad), nil); code != 400 {
			t.Errorf("widget config %s = %d, want 400", bad, code)
		}
	}
	// Another widget's configuration is none of this check's business.
	other := map[string]any{"name": "Other", "widgets": []map[string]any{{"id": "w1", "type": "status_summary", "title": "S", "width": 2, "height": 1, "config": json.RawMessage(`{"lineColor":"red"}`)}}}
	if code := call(t, ts, "POST", "/api/dashboards", other, nil); code != 200 {
		t.Fatalf("other widget: %d", code)
	}
}

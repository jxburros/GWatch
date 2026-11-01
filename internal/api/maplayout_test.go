package api

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/jxburros/GWatch/internal/model"
)

func TestMapLayoutRoundTripsAndForgetsDeletedNodes(t *testing.T) {
	ts, _ := newTestServer(t)
	parent, child := newLinkedNodes(t, ts)

	// Nothing arranged yet: an empty layout, not a 404.
	var empty model.MapLayout
	if code := call(t, ts, "GET", "/api/map/layout", nil, &empty); code != 200 {
		t.Fatalf("get empty: %d", code)
	}
	if len(empty.Positions) != 0 || len(empty.Collapsed) != 0 {
		t.Fatalf("empty layout = %+v", empty)
	}

	pk, ck := strconv.FormatInt(parent.ID, 10), strconv.FormatInt(child.ID, 10)
	body := map[string]any{
		"positions": map[string]any{
			pk:     map[string]any{"x": 12.345, "y": -40, "pinned": true},
			ck:     map[string]any{"x": 200, "y": 80},
			"9999": map[string]any{"x": 1, "y": 1, "pinned": true}, // no such node: dropped
		},
		"collapsed": []int64{parent.ID, parent.ID, 9999},
	}
	var saved model.MapLayout
	if code := call(t, ts, "PUT", "/api/map/layout", body, &saved); code != 200 {
		t.Fatalf("put: %d", code)
	}
	if len(saved.Positions) != 2 || saved.Positions[pk] != (model.MapPosition{X: 12.3, Y: -40, Pinned: true}) || saved.Positions[ck].Pinned {
		t.Fatalf("saved positions = %+v", saved.Positions)
	}
	if len(saved.Collapsed) != 1 || saved.Collapsed[0] != parent.ID {
		t.Fatalf("saved collapsed = %v", saved.Collapsed)
	}

	// A deleted node's place is forgotten when the layout is read.
	if code := call(t, ts, "DELETE", fmt.Sprintf("/api/nodes/%d", child.ID), nil, nil); code != 200 && code != 204 {
		t.Fatalf("delete: %d", code)
	}
	var got model.MapLayout
	call(t, ts, "GET", "/api/map/layout", nil, &got)
	if _, ok := got.Positions[ck]; ok || len(got.Positions) != 1 {
		t.Fatalf("after delete = %+v", got.Positions)
	}
}

func TestMapLayoutRefusesNonsense(t *testing.T) {
	ts, _ := newTestServer(t)
	parent, _ := newLinkedNodes(t, ts)
	pk := strconv.FormatInt(parent.ID, 10)
	for name, body := range map[string]any{
		"not an id":    map[string]any{"positions": map[string]any{"router": map[string]any{"x": 1, "y": 1}}},
		"out of range": map[string]any{"positions": map[string]any{pk: map[string]any{"x": 1e9, "y": 1}}},
		"bad collapse": map[string]any{"collapsed": []int64{-3}},
	} {
		if code := call(t, ts, "PUT", "/api/map/layout", body, nil); code != 400 {
			t.Errorf("%s: %d, want 400", name, code)
		}
	}
}

func TestMapLayoutClean(t *testing.T) {
	l := model.MapLayout{Positions: map[string]model.MapPosition{"5": {X: 1.26, Y: 2}}, Collapsed: []int64{9, 3, 9}}
	if err := l.Clean(nil); err != nil {
		t.Fatal(err)
	}
	if l.Positions["5"].X != 1.3 || len(l.Collapsed) != 2 || l.Collapsed[0] != 3 {
		t.Fatalf("cleaned = %+v", l)
	}
}

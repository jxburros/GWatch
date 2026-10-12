package store

import (
	"context"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

func TestLastResultsLatestInsertAndEmptyCheck(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	n, err := s.CreateNode(ctx, model.Node{Name: "latest", Checks: []model.Check{{Name: "populated", Type: model.CheckPing}, {Name: "empty", Type: model.CheckPing}}})
	if err != nil {
		t.Fatal(err)
	}
	id := n.Checks[0].ID
	var last model.Result
	for i := 0; i < 5; i++ {
		// Out-of-order timestamps must not change the latest-inserted contract.
		last, err = s.RecordResult(ctx, model.Result{CheckID: id, Timestamp: time.Now().Add(-time.Duration(i) * time.Hour), Success: true, Status: model.StatusUp, Message: "sample"}, model.CheckState{CheckID: id})
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.LastResults(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[id].ID != last.ID {
		t.Fatalf("last results: %+v", got)
	}
	if err = s.DeleteNode(ctx, n.ID); err != nil {
		t.Fatal(err)
	}
	got, err = s.LastResults(ctx)
	if err != nil || len(got) != 0 {
		t.Fatalf("deleted node results: %+v %v", got, err)
	}
}

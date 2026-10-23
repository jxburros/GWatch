package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// The restore of a backup (#148) writes a whole configuration through InTx so
// that a failure part-way leaves nothing behind. These tests hold InTx to
// that on whichever backend GWATCH_TEST_DB names: the ordinary methods called
// on the bound store must all land in the one transaction, see what it has
// written so far, and vanish together when it is rolled back.

func TestInTxRollsBackEveryMethodTogether(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	before, err := s.CreateNode(ctx, model.Node{Name: "Before", Host: "10.0.0.1", Enabled: true,
		Checks: []model.Check{{Type: model.CheckPing, Name: "Ping", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5}}})
	if err != nil {
		t.Fatal(err)
	}
	settings := model.DefaultSettings()
	settings.General.InstanceName = "Before"
	if err := s.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}

	boom := errors.New("injected failure")
	err = s.InTx(ctx, func(tx *Store) error {
		// RestoreNodes opens a transaction of its own when called on s; on tx
		// it must join this one.
		replaced := model.Node{ID: 77, Name: "Replaced", Host: "10.0.0.2", Enabled: true, CreatedAt: time.Now(), UpdatedAt: time.Now(),
			Checks: []model.Check{{ID: 770, NodeID: 77, Type: model.CheckPing, Name: "Ping", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5}}}
		if err := tx.RestoreNodes(ctx, []model.Node{replaced}, false); err != nil {
			return err
		}
		changed := settings
		changed.General.InstanceName = "During"
		if err := tx.SaveSettings(ctx, changed); err != nil {
			return err
		}
		if _, err := tx.SaveTrigger(ctx, model.Trigger{NodeID: 77, Name: "T", Enabled: true, On: []string{"down"}, Action: model.Action{Type: model.ActionHTTP, Method: "POST", URL: "http://example.invalid/"}}); err != nil {
			return err
		}
		if _, err := tx.ImportUser(ctx, UserRecord{User: model.User{ID: 5, Username: "ghost", Role: "admin"}, PasswordHash: "x"}); err != nil {
			return err
		}
		// Reads on the bound store see the transaction's own writes.
		if n, err := tx.ListNodes(ctx); err != nil || len(n) != 1 || n[0].Name != "Replaced" {
			t.Errorf("inside the transaction: nodes %+v, %v", n, err)
		}
		if _, _, err := tx.GetUserByName(ctx, "ghost"); err != nil {
			t.Errorf("inside the transaction the imported user is missing: %v", err)
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("InTx returned %v, want the injected error", err)
	}

	nodes, err := s.ListNodes(ctx)
	if err != nil || len(nodes) != 1 || nodes[0].ID != before.ID || nodes[0].Name != "Before" || len(nodes[0].Checks) != 1 {
		t.Fatalf("after the rollback the nodes should be as they were: %+v, %v", nodes, err)
	}
	got, err := s.LoadSettings(ctx)
	if err != nil || got.General.InstanceName != "Before" {
		t.Fatalf("after the rollback the settings should be as they were: %q, %v", got.General.InstanceName, err)
	}
	if list, _ := s.ListTriggers(ctx, nil); len(list) != 0 {
		t.Fatalf("the trigger written in the transaction survived: %+v", list)
	}
	if _, _, err := s.GetUserByName(ctx, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the user written in the transaction survived: %v", err)
	}
	// And the writer is free again: an ordinary write goes through.
	if _, err := s.SaveTrigger(ctx, model.Trigger{NodeID: before.ID, Name: "After", Enabled: true, On: []string{"down"}, Action: model.Action{Type: model.ActionHTTP, Method: "POST", URL: "http://example.invalid/"}}); err != nil {
		t.Fatalf("write after the rollback: %v", err)
	}
}

func TestInTxCommitsOnSuccess(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	err := s.InTx(ctx, func(tx *Store) error {
		n := model.Node{ID: 3, Name: "Committed", Host: "10.0.0.3", Enabled: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}
		if err := tx.RestoreNodes(ctx, []model.Node{n}, false); err != nil {
			return err
		}
		// A nested InTx on the bound store joins the same transaction.
		return tx.InTx(ctx, func(inner *Store) error {
			_, err := inner.SaveDashboard(ctx, model.Dashboard{Name: "Board"})
			return err
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	nodes, _ := s.ListNodes(ctx)
	boards, _ := s.ListDashboards(ctx)
	if len(nodes) != 1 || nodes[0].Name != "Committed" || len(boards) != 1 {
		t.Fatalf("committed writes missing: nodes %+v boards %+v", nodes, boards)
	}
}

func TestInTxRollsBackOnPanic(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	func() {
		defer func() { _ = recover() }()
		_ = s.InTx(ctx, func(tx *Store) error {
			if _, err := tx.SaveDashboard(ctx, model.Dashboard{Name: "Lost"}); err != nil {
				return err
			}
			panic("boom")
		})
	}()
	if boards, _ := s.ListDashboards(ctx); len(boards) != 0 {
		t.Fatalf("a panic inside InTx kept its writes: %+v", boards)
	}
	// The writer must not be left holding the aborted transaction.
	done := make(chan error, 1)
	go func() { _, err := s.SaveDashboard(ctx, model.Dashboard{Name: "After"}); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a write after a panicking InTx hung")
	}
}

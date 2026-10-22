package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/jxburros/GWatch/internal/auth"
)

// The store, not the handler, keeps the last administrator: demoting or
// deleting the only one is refused, and the check commits with the change.
func TestLastAdminIsKept(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	pat, err := s.CreateUser(ctx, "pat", "hash", auth.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	sam, err := s.CreateUser(ctx, "sam", "hash", auth.RoleViewer)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateUserRole(ctx, pat.ID, auth.RoleViewer); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("demote the only admin: %v", err)
	}
	if err := s.DeleteUser(ctx, pat.ID); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("delete the only admin: %v", err)
	}
	// Saving the admin as an admin again, and removing a viewer, are fine.
	if err := s.UpdateUserRole(ctx, pat.ID, auth.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(ctx, sam.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(ctx, 9999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing user: %v", err)
	}
	if err := s.UpdateUserRole(ctx, 9999, auth.RoleViewer); !errors.Is(err, ErrNotFound) {
		t.Fatalf("demote missing user: %v", err)
	}
	if u, err := s.GetUser(ctx, pat.ID); err != nil || u.Role != "admin" {
		t.Fatalf("pat after refusals: %+v %v", u, err)
	}
}

// Two administrators removed at the same moment by two requests must not both
// go: one removal succeeds and the other is refused with ErrLastAdmin. Each
// round races the previous round's survivor against a newly created admin, so
// exactly two administrators exist when the race starts.
func TestLastAdminRace(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	first, err := s.CreateUser(ctx, "admin-start", "hash", auth.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	survivor := first.ID
	for round := 0; round < 20; round++ {
		u, err := s.CreateUser(ctx, fmt.Sprintf("admin-%d", round), "hash", auth.RoleAdmin)
		if err != nil {
			t.Fatal(err)
		}
		ids := [2]int64{survivor, u.ID}
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i := range ids {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				if round%2 == 0 {
					errs[i] = s.DeleteUser(ctx, ids[i])
				} else {
					errs[i] = s.UpdateUserRole(ctx, ids[i], auth.RoleViewer)
				}
			}(i)
		}
		wg.Wait()
		refused := 0
		for i, err := range errs {
			switch {
			case errors.Is(err, ErrLastAdmin):
				refused++
				survivor = ids[i]
			case err != nil:
				t.Fatalf("round %d: %v", round, err)
			}
		}
		if refused != 1 {
			t.Fatalf("round %d: %d of 2 removals refused, want exactly 1", round, refused)
		}
		if n, err := s.CountAdmins(ctx); err != nil || n != 1 {
			t.Fatalf("round %d: %d administrators left (%v)", round, n, err)
		}
	}
}

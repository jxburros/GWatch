package permissions

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsOwnerTrustFollowsProcessIdentity(t *testing.T) {
	const user = "S-1-5-21-100-200-300-1001"
	const otherUser = "S-1-5-21-100-200-300-1002"
	for _, tc := range []struct {
		name, owner, process string
		want                 bool
	}{
		{"interactive own state including elevated account", user, user, true},
		{"interactive other user state", otherUser, user, false},
		{"interactive installed state", "S-1-5-32-544", user, true},
		{"SYSTEM own state", "S-1-5-18", "S-1-5-18", true},
		{"SYSTEM installed state", "S-1-5-32-544", "S-1-5-18", true},
		{"SYSTEM rejects interactive account state", user, "S-1-5-18", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := trustedOwnerSID(tc.owner, tc.process); got != tc.want {
				t.Fatalf("trustedOwnerSID(%q, %q) = %v", tc.owner, tc.process, got)
			}
		})
	}
}

func TestWindowsProtectedStateOwnerAndDACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	if err := os.WriteFile(path, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateFile(path); err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfigOwner(path); err != nil {
		t.Fatal(err)
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		t.Fatal(err)
	}
	token := windows.GetCurrentProcessToken()
	if token.IsElevated() && owner.String() != "S-1-5-32-544" {
		t.Fatalf("elevated installation must leave Administrators-owned state, got %s", owner)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("state inherited an unprotected DACL")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "private" {
		t.Fatalf("protected state not readable: %q, %v", got, err)
	}
}

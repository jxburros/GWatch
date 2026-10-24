package actions

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// A git action that times out ends when it times out, even when what git
// started (ssh, a credential helper) is still holding its output open.
func TestGitActionTimeoutIsNotHeldByAChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell stand-in for git")
	}
	bin := t.TempDir()
	fake := "#!/bin/sh\nsleep 30 &\nwait\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	start := time.Now()
	res := (&Runner{}).Run(context.Background(), model.Action{Type: model.ActionGit, Repo: t.TempDir(), GitArgs: "pull", TimeoutSeconds: 1}, nil)
	if res.OK {
		t.Fatalf("expected a timeout: %+v", res)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("the action took %s; a child of git held it past its 1s timeout", d)
	}
}

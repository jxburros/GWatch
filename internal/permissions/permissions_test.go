package permissions

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPrivateStateRemainsReadableAndRejectsFileDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := EnsurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "secret")
	if err := os.WriteFile(file, []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateFile(file); err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfigOwner(file); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(file); err != nil || string(got) != "secret" {
		t.Fatalf("%q %v", got, err)
	}
	if err := EnsurePrivateDir(file); err == nil {
		t.Fatal("file accepted as directory")
	}
	if err := EnsurePrivateFile(dir); err == nil {
		t.Fatal("directory accepted as file")
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(file)
		if info.Mode().Perm() != 0600 {
			t.Fatal(info.Mode())
		}
		info, _ = os.Stat(dir)
		if info.Mode().Perm() != 0700 {
			t.Fatal(info.Mode())
		}
	}
}
func TestRejectSymlinkState(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlink privilege unavailable")
	}
	if err := EnsurePrivateFile(link); err == nil {
		t.Fatal("symlink accepted")
	}
	if err := ValidateConfigOwner(link); err == nil {
		t.Fatal("symlink configuration accepted")
	}
}

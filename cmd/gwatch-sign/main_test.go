package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jxburros/GWatch/internal/update"
)

func TestSignVerifyCLIAndRejectTampering(t *testing.T) {
	public, private, err := update.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(envKey, update.EncodeSeed(private))
	path := filepath.Join(t.TempDir(), "asset")
	if err := os.WriteFile(path, []byte("release"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"sign", path}); err == nil {
		t.Fatal("signed without a release version")
	}
	if err := run([]string{"sign", "-version", "1.2.3", path, path + ".sha256"}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"verify", "-version", "1.2.3", "-pub", update.FormatPublicKey(public), path, path + ".sig"}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"verify", "-version", "1.2.4", "-pub", update.FormatPublicKey(public), path}); err == nil {
		t.Fatal("a signature for 1.2.3 verified as 1.2.4")
	}
	if err := os.WriteFile(path, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"verify", "-version", "1.2.3", "-pub", update.FormatPublicKey(public), path}); err == nil {
		t.Fatal("tampered file verified")
	}
}
func TestSigningFailsClosedAndPreservesKey(t *testing.T) {
	t.Setenv(envKey, "")
	for _, args := range [][]string{{}, {"unknown"}, {"sign"}, {"verify"}, {"sign", "missing"}, {"verify", "-pub", "bad", "missing"}} {
		if err := run(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	path := filepath.Join(t.TempDir(), "release.key")
	if err := os.WriteFile(path, []byte("existing-key"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := keygen([]string{"-out", path}); err == nil {
		t.Fatal("overwrote existing signing key")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "existing-key" {
		t.Fatal("key changed")
	}
}

func TestForcedKeygenTightensPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "release.key")
	if err := os.WriteFile(path, []byte("old key"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := keygen([]string{"-out", path, "-force"}); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("forced key file has mode %v, want 0600", st.Mode().Perm())
	}
	if b, _ := os.ReadFile(path); string(b) == "old key" {
		t.Fatal("key was not replaced")
	}

	// A link is refused, so the key cannot be written wherever it points.
	target := filepath.Join(dir, "elsewhere")
	if err := os.WriteFile(target, []byte("not a key"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.key")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := keygen([]string{"-out", link, "-force"}); err == nil {
		t.Fatal("wrote a signing key through a symlink")
	}
	if b, _ := os.ReadFile(target); string(b) != "not a key" {
		t.Fatal("symlink target changed")
	}
}

package main

import (
	"github.com/jxburros/GWatch/internal/update"
	"os"
	"path/filepath"
	"testing"
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
	if err := run([]string{"sign", path, path + ".sha256"}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"verify", "-pub", update.FormatPublicKey(public), path, path + ".sig"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"verify", "-pub", update.FormatPublicKey(public), path}); err == nil {
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

package secrets

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadCreatesKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "gwatch.key")
	b, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file perms = %v, want 0600", fi.Mode().Perm())
	}
	// Reloading yields the same key, so values round-trip across restarts.
	sealed, err := b.Seal("hunter2")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	b2, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, err := b2.Open(sealed)
	if err != nil || got != "hunter2" {
		t.Fatalf("open after reload = %q, %v", got, err)
	}
}

func TestSealEmptyString(t *testing.T) {
	b := testBox(t)
	sealed, err := b.Seal("")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if sealed != "" {
		t.Fatalf("seal(\"\") = %q, want empty", sealed)
	}
	got, err := b.Open("")
	if err != nil || got != "" {
		t.Fatalf("open(\"\") = %q, %v", got, err)
	}
}

func TestSealOpenRoundTrip(t *testing.T) {
	b := testBox(t)
	for _, plain := range []string{"p@ss", "unicode ✓ sécret", strings.Repeat("x", 4096)} {
		sealed, err := b.Seal(plain)
		if err != nil {
			t.Fatalf("seal: %v", err)
		}
		if !IsSealed(sealed) {
			t.Fatalf("sealed value missing prefix: %q", sealed)
		}
		if strings.Contains(sealed, plain) {
			t.Fatalf("plaintext visible in sealed value")
		}
		got, err := b.Open(sealed)
		if err != nil || got != plain {
			t.Fatalf("round trip = %q, %v", got, err)
		}
	}
}

func TestSealUsesFreshNonce(t *testing.T) {
	b := testBox(t)
	a, _ := b.Seal("same")
	c, _ := b.Seal("same")
	if a == c {
		t.Fatalf("two seals of the same value are identical: %q", a)
	}
}

func TestLegacyPlaintextPassesThrough(t *testing.T) {
	b := testBox(t)
	got, err := b.Open("plain-password")
	if err != nil || got != "plain-password" {
		t.Fatalf("legacy passthrough = %q, %v", got, err)
	}
	if IsSealed("plain-password") {
		t.Fatalf("plaintext reported as sealed")
	}
}

func TestOpenDetectsTampering(t *testing.T) {
	b := testBox(t)
	sealed, err := b.Seal("secret")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(sealed, Prefix))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	raw[len(raw)-1] ^= 0xff
	tampered := Prefix + base64.RawURLEncoding.EncodeToString(raw)
	if _, err := b.Open(tampered); !errors.Is(err, ErrCannotDecrypt) {
		t.Fatalf("tampered open err = %v, want ErrCannotDecrypt", err)
	}
	if _, err := b.Open(Prefix + "!!!not base64!!!"); !errors.Is(err, ErrCannotDecrypt) {
		t.Fatalf("garbage open err = %v, want ErrCannotDecrypt", err)
	}
	if _, err := b.Open(Prefix + base64.RawURLEncoding.EncodeToString([]byte("short"))); !errors.Is(err, ErrCannotDecrypt) {
		t.Fatalf("short open err = %v, want ErrCannotDecrypt", err)
	}
}

func TestOpenWithDifferentKeyFails(t *testing.T) {
	a := testBox(t)
	b := testBox(t)
	sealed, err := a.Seal("secret")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, err := b.Open(sealed); !errors.Is(err, ErrCannotDecrypt) {
		t.Fatalf("wrong-key open err = %v, want ErrCannotDecrypt", err)
	}
}

func TestLoadRejectsBadKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gwatch.key")
	if err := os.WriteFile(path, []byte("too short"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatalf("expected error for malformed key file")
	}
}

func TestLoadAcceptsRawKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gwatch.key")
	key := make([]byte, KeySize)
	for i := range key {
		key[i] = byte(i)
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	b, err := Load(path)
	if err != nil {
		t.Fatalf("load raw key: %v", err)
	}
	sealed, err := b.Seal("v")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if got, err := b.Open(sealed); err != nil || got != "v" {
		t.Fatalf("round trip = %q, %v", got, err)
	}
}

func testBox(t *testing.T) *Box {
	t.Helper()
	b, err := Load(filepath.Join(t.TempDir(), "gwatch.key"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return b
}

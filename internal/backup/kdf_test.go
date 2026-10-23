package backup

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/jxburros/GWatch/internal/store/storetest"
)

// TestFormat1FixtureStillDecrypts reads an archive written by the code as it
// was before the key-derivation identifier existed (#49). The fixture in
// testdata was produced by that old NewEncryptWriter, unchanged, with the
// password "fixture-password"; if it ever stops decrypting, every backup an
// owner made with an older release has stopped being restorable.
func TestFormat1FixtureStillDecrypts(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "format1-argon2id.gwbackup"))
	if err != nil {
		t.Fatal(err)
	}
	if data[len(magic)] != formatV1 {
		t.Fatalf("fixture is not format 1 (version byte %d)", data[len(magic)])
	}
	r, err := NewDecryptReader(bytes.NewReader(data), "fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("old archive no longer decrypts: %v", err)
	}
	if want := "GWatch format-1 archive written before the KDF identifier existed.\n"; string(got) != want {
		t.Fatalf("plaintext = %q", got)
	}
	r, err = NewDecryptReader(bytes.NewReader(data), "not the password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(r); err != ErrBadPassword {
		t.Fatalf("wrong password on the fixture: %v", err)
	}
}

// TestKDFRoundTrip writes and reads an archive with each derivation, checks
// the header each one gets, and that the reader takes the derivation from the
// header rather than from anything the caller says.
func TestKDFRoundTrip(t *testing.T) {
	payload := bytes.Repeat([]byte("gwatch "), 50_000)
	for _, tc := range []struct {
		kdf    KDF
		header []byte
	}{
		{KDFArgon2id, []byte{'G', 'W', 'B', 'K', formatV1}},
		{KDFPBKDF2, []byte{'G', 'W', 'B', 'K', formatV2, byte(KDFPBKDF2)}},
	} {
		t.Run(tc.kdf.String(), func(t *testing.T) {
			var buf bytes.Buffer
			w, err := NewEncryptWriterKDF(&buf, "secret", tc.kdf)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write(payload); err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			if !bytes.HasPrefix(buf.Bytes(), tc.header) {
				t.Fatalf("header = % x, want prefix % x", buf.Bytes()[:8], tc.header)
			}
			r, err := NewDecryptReader(bytes.NewReader(buf.Bytes()), "secret")
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(r)
			if err != nil || !bytes.Equal(got, payload) {
				t.Fatalf("round trip: %v (%d bytes)", err, len(got))
			}
			r, err = NewDecryptReader(bytes.NewReader(buf.Bytes()), "wrong")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.ReadAll(r); err != ErrBadPassword {
				t.Fatalf("wrong password: %v", err)
			}
		})
	}
}

// TestFormat2Argon2idHeader covers a format-2 header naming Argon2id. This
// release never writes one (Argon2id archives stay format 1 so older releases
// can restore them), but the format allows it and a later release might, so
// the reader must accept it.
func TestFormat2Argon2idHeader(t *testing.T) {
	var buf bytes.Buffer
	w, _ := NewEncryptWriterKDF(&buf, "secret", KDFArgon2id)
	w.Write([]byte("hello"))
	w.Close()
	old := buf.Bytes()
	v2 := append(append(append([]byte{}, old[:len(magic)]...), formatV2, byte(KDFArgon2id)), old[len(magic)+1:]...)
	r, err := NewDecryptReader(bytes.NewReader(v2), "secret")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := io.ReadAll(r); err != nil || string(got) != "hello" {
		t.Fatalf("format 2 / argon2id: %q, %v", got, err)
	}
}

func TestUnknownKDFRejected(t *testing.T) {
	for name, data := range map[string][]byte{
		"unknown kdf":    append([]byte("GWBK\x02\x09"), make([]byte, saltSize)...),
		"zero kdf":       append([]byte("GWBK\x02\x00"), make([]byte, saltSize)...),
		"future version": append([]byte("GWBK\x03\x01"), make([]byte, saltSize)...),
		"truncated salt": []byte("GWBK\x02\x02abc"),
		"missing kdf":    []byte("GWBK\x02"),
		"not a backup":   []byte("PK\x03\x04 a zip file"),
		"empty file":     nil,
	} {
		if _, err := NewDecryptReader(bytes.NewReader(data), "secret"); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if _, err := NewEncryptWriterKDF(io.Discard, "secret", KDF(9)); err == nil {
		t.Error("encrypting with an unknown derivation must fail")
	}
}

func TestKDFForSetting(t *testing.T) {
	for in, want := range map[string]KDF{"": KDFArgon2id, "argon2id": KDFArgon2id, "pbkdf2-sha256": KDFPBKDF2, "nonsense": KDFArgon2id} {
		if got := KDFForSetting(in); got != want {
			t.Errorf("KDFForSetting(%q) = %s, want %s", in, got, want)
		}
	}
}

// TestCreateFollowsHashingSetting checks the end-to-end path: with PBKDF2
// selected in the settings a backup is written in format 2 with the PBKDF2
// identifier, and it restores like any other.
func TestCreateFollowsHashingSetting(t *testing.T) {
	ctx := context.Background()
	st := storetest.Open(t)
	settings, err := st.LoadSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.General.PasswordHashing = "pbkdf2-sha256"
	settings.General.InstanceName = "PBKDF2 install"
	if err := st.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	info, err := Create(ctx, st, dir, "backup-pw", false, "test")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, info.FileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte{'G', 'W', 'B', 'K', formatV2, byte(KDFPBKDF2)}) {
		t.Fatalf("header = % x, want a format-2 PBKDF2 header", data[:6])
	}
	st2 := storetest.Open(t)
	if _, err := Restore(ctx, st2, path, "backup-pw", false); err != nil {
		t.Fatal(err)
	}
	back, err := st2.LoadSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if back.General.InstanceName != "PBKDF2 install" || back.General.PasswordHashing != "pbkdf2-sha256" {
		t.Fatalf("restored settings: %q / %q", back.General.InstanceName, back.General.PasswordHashing)
	}

	// Switching back to the default writes a format-1 archive again.
	settings.General.PasswordHashing = "argon2id"
	if err := st.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	info, err = Create(ctx, st, dir, "backup-pw", false, "test")
	if err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(dir, info.FileName))
	if data[len(magic)] != formatV1 {
		t.Fatalf("argon2id archive written as format %d, want 1", data[len(magic)])
	}
}

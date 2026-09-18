package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jxburros/GWatch/internal/model"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0}, {"v1.2.3", "1.2.3", 0}, {"1.10", "1.9", 1}, {"1.2", "1.2.1", -1}, {"2.0.0", "1.99.99", 1},
		{"1.0.0-rc1", "1.0.0", -1}, {"1.0.0", "1.0.0-rc1", 1}, {"1.0.0-rc2", "1.0.0-rc1", 1},
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	for _, v := range []string{"dev", "", "ci-abc1234", "v", "main"} {
		if !IsDev(v) {
			t.Errorf("IsDev(%q) should be true", v)
		}
	}
	for _, v := range []string{"1.0.0", "v2.3", "1.0.0-rc1"} {
		if IsDev(v) {
			t.Errorf("IsDev(%q) should be false", v)
		}
	}
	if ValidRepo("owner") || ValidRepo("a/b/c") || !ValidRepo("jxburros/GWatch") {
		t.Error("ValidRepo misclassifies")
	}
}

// pinKeys makes the given public keys the ones this build trusts for the rest
// of the test, the way the -X ldflags override does at build time.
func pinKeys(t *testing.T, pubs ...ed25519.PublicKey) {
	t.Helper()
	var lines []string
	for _, p := range pubs {
		lines = append(lines, FormatPublicKey(p))
	}
	old := releaseKeys
	releaseKeys = strings.Join(lines, ",")
	t.Cleanup(func() { releaseKeys = old })
}

// testKey returns a fresh signing key plus a signature file for payload.
func testKey(t *testing.T, payload []byte) (ed25519.PublicKey, ed25519.PrivateKey, string) {
	t.Helper()
	pub, priv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	return pub, priv, FormatSignature(pub, SignDigest(priv, digest[:]))
}

func TestCheckDownloadSwap(t *testing.T) {
	payload := []byte("#!/bin/sh\necho new\n")
	sum := sha256.Sum256(payload)
	pub, _, sigText := testKey(t, payload)
	pinKeys(t, pub)
	mux := http.NewServeMux()
	var srvURL string
	mux.HandleFunc("/repos/acme/gwatch/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Errorf("missing accept header")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tag_name":"v2.5.0","html_url":"https://example.com/rel","body":"notes","published_at":"2026-09-01T10:00:00Z","assets":[
			{"name":"gwatch-linux-amd64.sha256","size":70,"browser_download_url":"` + srvURL + `/dl/gwatch-linux-amd64.sha256"},
			{"name":"gwatch-linux-amd64","size":` + strconv.Itoa(len(payload)) + `,"browser_download_url":"` + srvURL + `/dl/gwatch-linux-amd64"},
			{"name":"gwatch-windows-amd64.exe","size":5,"browser_download_url":"` + srvURL + `/dl/win"}]}`))
	})
	mux.HandleFunc("/repos/acme/empty/releases/latest", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) })
	mux.HandleFunc("/dl/gwatch-linux-amd64", func(w http.ResponseWriter, r *http.Request) { w.Write(payload) })
	mux.HandleFunc("/dl/gwatch-linux-amd64.sha256", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(hex.EncodeToString(sum[:]) + "  gwatch-linux-amd64\n"))
	})
	mux.HandleFunc("/dl/gwatch-linux-amd64.sig", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sigText))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	srvURL = srv.URL

	c := &Client{APIBase: srv.URL, HTTP: srv.Client(), GOOS: "linux", GOARCH: "amd64"}
	info, err := c.Check(context.Background(), "acme/gwatch", "2.4.1")
	if err != nil {
		t.Fatal(err)
	}
	if !info.UpdateAvailable || info.LatestVersion != "2.5.0" || info.AssetName != "gwatch-linux-amd64" || info.ReleaseURL != "https://example.com/rel" || info.PublishedAt == nil {
		t.Fatalf("info: %+v", info)
	}
	if info2, _ := c.Check(context.Background(), "acme/gwatch", "2.5.0"); info2.UpdateAvailable {
		t.Fatal("same version should not be an update")
	}
	if info3, _ := c.Check(context.Background(), "acme/gwatch", "dev"); !info3.UpdateAvailable || !info3.CurrentIsDev {
		t.Fatal("dev builds should always see the release")
	}
	if _, err := c.Check(context.Background(), "acme/empty", "1.0"); err == nil || !strings.Contains(err.Error(), "no releases") {
		t.Fatalf("expected no-releases error, got %v", err)
	}
	if _, err := c.Check(context.Background(), "bad repo", "1.0"); err == nil {
		t.Fatal("expected invalid repo error")
	}
	win := &Client{APIBase: srv.URL, HTTP: srv.Client(), GOOS: "windows", GOARCH: "amd64"}
	if wi, _ := win.Check(context.Background(), "acme/gwatch", "1.0"); wi.AssetName != "gwatch-windows-amd64.exe" {
		t.Fatalf("windows asset: %+v", wi)
	}
	arm := &Client{APIBase: srv.URL, HTTP: srv.Client(), GOOS: "linux", GOARCH: "arm64"}
	if ai, _ := arm.Check(context.Background(), "acme/gwatch", "1.0"); ai.AssetURL != "" {
		t.Fatalf("arm64 should have no asset: %+v", ai)
	}

	dir := t.TempDir()
	exe := filepath.Join(dir, "gwatch")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	newPath, err := c.Download(context.Background(), info, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := Swap(exe, newPath); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(exe)
	old, _ := os.ReadFile(exe + ".old")
	if string(got) != string(payload) || string(old) != "old" {
		t.Fatalf("swap: exe=%q old=%q", got, old)
	}
	if !DirWritable(exe) {
		t.Fatal("temp dir should be writable")
	}
	// checksum mismatch is rejected
	bad := info
	bad.AssetURL = srv.URL + "/dl/win"
	bad.AssetSize = 0
	mux.HandleFunc("/dl/win", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("hello")) })
	mux.HandleFunc("/dl/win.sha256", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(strings.Repeat("0", 64))) })
	if _, err := c.Download(context.Background(), bad, dir); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected checksum error, got %v", err)
	}
	if _, err := c.Download(context.Background(), model_noAsset(), dir); err != ErrNoAsset {
		t.Fatalf("expected ErrNoAsset, got %v", err)
	}
}

// TestDownloadRequiresSignature walks the cases release verification has to get
// right: unsigned release, wrong signature, good signature, no key pinned into
// the build, and a sha256 sidecar that disagrees.
func TestDownloadRequiresSignature(t *testing.T) {
	payload := []byte("#!/bin/sh\necho signed\n")
	sum := sha256.Sum256(payload)
	pub, _, goodSig := testKey(t, payload)
	otherPub, otherPriv, _ := testKey(t, []byte("something else"))
	wrongDigest := sha256.Sum256([]byte("a different binary"))
	wrongSig := FormatSignature(otherPub, SignDigest(otherPriv, wrongDigest[:]))

	mux := http.NewServeMux()
	mux.HandleFunc("/dl/asset", func(w http.ResponseWriter, r *http.Request) { w.Write(payload) })
	mux.HandleFunc("/dl/asset.sha256", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(hex.EncodeToString(sum[:]) + "  asset\n"))
	})
	mux.HandleFunc("/dl/asset.sig", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(goodSig)) })
	mux.HandleFunc("/dl/unsigned", func(w http.ResponseWriter, r *http.Request) { w.Write(payload) })
	mux.HandleFunc("/dl/unsigned.sig", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) })
	mux.HandleFunc("/dl/wrongsig", func(w http.ResponseWriter, r *http.Request) { w.Write(payload) })
	mux.HandleFunc("/dl/wrongsig.sig", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(wrongSig)) })
	mux.HandleFunc("/dl/junksig", func(w http.ResponseWriter, r *http.Request) { w.Write(payload) })
	mux.HandleFunc("/dl/junksig.sig", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("not a signature file\n")) })
	mux.HandleFunc("/dl/badsum", func(w http.ResponseWriter, r *http.Request) { w.Write(payload) })
	mux.HandleFunc("/dl/badsum.sha256", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("0", 64) + "  badsum\n"))
	})
	mux.HandleFunc("/dl/badsum.sig", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(goodSig)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &Client{APIBase: srv.URL, HTTP: srv.Client(), GOOS: "linux", GOARCH: "amd64"}
	info := func(name string) model.UpdateInfo {
		return model.UpdateInfo{AssetName: name, AssetURL: srv.URL + "/dl/" + name, AssetSize: int64(len(payload))}
	}
	countFiles := func(dir string) int {
		ents, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		return len(ents)
	}

	// A build with no pinned key downloads nothing at all.
	pinKeys(t)
	dir := t.TempDir()
	if _, err := c.Download(context.Background(), info("asset"), dir); !errors.Is(err, ErrNoSigningKey) {
		t.Fatalf("unkeyed build should refuse to download, got %v", err)
	}
	if n := countFiles(dir); n != 0 {
		t.Fatalf("unkeyed build left %d files behind", n)
	}
	if c.SigningEnabled() || len(KeyIDs()) != 0 {
		t.Fatal("SigningEnabled should be false with no keys")
	}

	pinKeys(t, pub)
	if !c.SigningEnabled() || KeyIDs()[0] != KeyID(pub) {
		t.Fatalf("SigningEnabled/KeyIDs wrong: %v", KeyIDs())
	}
	cases := []struct {
		name, asset, want string
	}{
		{"unsigned release", "unsigned", "not signed"},
		{"signature from another key", "wrongsig", "signature verification failed"},
		{"unparseable signature", "junksig", "signature verification failed"},
		{"sha256 sidecar mismatch", "badsum", "checksum mismatch"},
	}
	for _, tc := range cases {
		dir := t.TempDir()
		got, err := c.Download(context.Background(), info(tc.asset), dir)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want error containing %q, got %v (%q)", tc.name, tc.want, err, got)
		}
		if n := countFiles(dir); n != 0 {
			t.Errorf("%s: %d temp files left behind", tc.name, n)
		}
	}

	dir = t.TempDir()
	path, err := c.Download(context.Background(), info("asset"), dir)
	if err != nil {
		t.Fatalf("properly signed asset should install: %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != string(payload) {
		t.Fatalf("downloaded %q", b)
	}

	// Several pinned keys: a release signed by any of them is accepted, which
	// is what makes key rotation possible.
	pinKeys(t, otherPub, pub)
	dir = t.TempDir()
	if _, err := c.Download(context.Background(), info("asset"), dir); err != nil {
		t.Fatalf("key rotation should keep the other key trusted: %v", err)
	}
}

// TestCheckWithoutKeyStillReports checks that a build with no signing key can
// still see that a release exists, and says why it cannot install it.
func TestCheckWithoutKeyStillReports(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/gwatch/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name":"v9.0.0","html_url":"https://example.com/rel","assets":[]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	pinKeys(t)
	c := &Client{APIBase: srv.URL, HTTP: srv.Client(), GOOS: "linux", GOARCH: "amd64"}
	info, err := c.Check(context.Background(), "acme/gwatch", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if !info.UpdateAvailable || info.LatestVersion != "9.0.0" {
		t.Fatalf("check should still report the release: %+v", info)
	}
	if !strings.Contains(info.Error, "no release signing key") {
		t.Fatalf("check should explain why updates are off: %q", info.Error)
	}
}

// TestSignRoundTrip covers what cmd/gwatch-sign does: make a key, write a .sig
// next to a file, verify it, and reject tampering.
func TestSignRoundTrip(t *testing.T) {
	pub, priv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	keys := []PublicKey{{ID: KeyID(pub), Key: pub}}

	// The seed round-trips through the form release.key and the CI secret hold.
	seeded, err := ParsePrivateKey("# a comment\n" + EncodeSeed(priv) + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if !seeded.Equal(priv) {
		t.Fatal("seed round-trip changed the key")
	}
	for _, bad := range []string{"not base64!", "", "AAAA"} {
		if _, err := ParsePrivateKey(bad); err == nil {
			t.Fatalf("ParsePrivateKey(%q) should fail", bad)
		}
	}

	// The public key round-trips through the release_keys.txt form.
	parsed, err := ParseKeys("# comment\n\n" + FormatPublicKey(pub) + " # trailing note\n")
	if err != nil || len(parsed) != 1 || !parsed[0].Key.Equal(pub) || parsed[0].ID != KeyID(pub) {
		t.Fatalf("ParseKeys: %v %v", parsed, err)
	}
	for _, bad := range []string{"ed25519:!!!", "ssh-rsa AAAA", "ed25519:AAAA"} {
		if _, err := ParseKeys(bad); err == nil {
			t.Fatalf("ParseKeys(%q) should fail", bad)
		}
	}

	dir := t.TempDir()
	file := filepath.Join(dir, "gwatch-linux-amd64")
	if err := os.WriteFile(file, []byte("binary contents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := VerifyFile(file, keys); !errors.Is(err, ErrUnsigned) {
		t.Fatalf("unsigned file: %v", err)
	}
	sigPath, err := SignFile(seeded, file)
	if err != nil {
		t.Fatal(err)
	}
	if sigPath != file+SignatureExt {
		t.Fatalf("signature path %q", sigPath)
	}
	if err := VerifyFile(file, keys); err != nil {
		t.Fatalf("freshly signed file should verify: %v", err)
	}

	raw, _ := os.ReadFile(sigPath)
	if !strings.HasPrefix(string(raw), SignatureFormat+"\n") {
		t.Fatalf("signature header: %q", raw)
	}
	sig, err := ParseSignature(string(raw))
	if err != nil || sig.KeyID != KeyID(pub) || len(sig.Sig) != ed25519.SignatureSize {
		t.Fatalf("ParseSignature: %+v %v", sig, err)
	}
	// Lenient on whitespace, strict on the header line.
	lenient := "  " + SignatureFormat + "  \n\n\tKey :  " + sig.KeyID + "\t\n  sig:   " +
		base64.StdEncoding.EncodeToString(sig.Sig) + "  \n\n"
	if got, err := ParseSignature(lenient); err != nil || got.KeyID != sig.KeyID {
		t.Fatalf("whitespace should be tolerated: %+v %v", got, err)
	}
	for _, bad := range []string{"gwatch-sig-v2\nkey: x\nsig: y\n", "", "key: x\nsig: y\n", SignatureFormat + "\nsig: zzz\n"} {
		if _, err := ParseSignature(bad); err == nil {
			t.Fatalf("ParseSignature(%q) should fail", bad)
		}
	}

	// Tamper with the file: the same signature must stop verifying.
	if err := os.WriteFile(file, []byte("binary contents, patched"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := VerifyFile(file, keys); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("tampered file: %v", err)
	}
	if err := VerifyFile(file, nil); !errors.Is(err, ErrNoSigningKey) {
		t.Fatalf("no keys: %v", err)
	}
}

// TestEmbeddedKeysParse guards release_keys.txt itself: whatever it holds must
// be readable, so a typo cannot silently turn updates off.
func TestEmbeddedKeysParse(t *testing.T) {
	if _, err := ParseKeys(embeddedReleaseKeys); err != nil {
		t.Fatalf("release_keys.txt does not parse: %v", err)
	}
}

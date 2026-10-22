package update

// Release signing. Every release asset gwatch-<os>-<arch>[.exe] is published
// with a sibling <asset>.sig text file:
//
//	gwatch-sig-v1
//	key: 1a2b3c4d
//	sig: <base64 ed25519 signature>
//
//	asset: gwatch-linux-amd64
//	version: 1.2.3
//	release-sig: <base64 ed25519 signature>
//
// The signatures are made over the SHA-256 digest of the asset rather than the
// asset itself, so signer and verifier can both stream a large file and only
// ever hold 32 bytes in memory. The public keys the running binary trusts are
// pinned at build time (release_keys.txt, or the releaseKeys ldflags override),
// so a release signed by anyone else is refused.
//
// release-sig is the one this build checks. It covers the asset's name (which
// names the product and the platform) and the release version alongside the
// digest, so a genuinely signed binary cannot be republished as something it
// is not: an old agent relabelled as a new one, or an agent renamed as a
// GWatch server build. sig covers the digest alone and is kept, first, only
// so that copies installed before release-sig existed can still verify and
// install the release that brings them this check; those copies ignore the
// lines they do not know.

import (
	"bufio"
	"crypto/ed25519"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// SignatureFormat is the first line of every .sig file.
const SignatureFormat = "gwatch-sig-v1"

// SignatureExt is appended to an asset name to get its signature file.
const SignatureExt = ".sig"

//go:embed release_keys.txt
var embeddedReleaseKeys string

// releaseKeys overrides the embedded key list at build time, for forks that
// publish their own releases:
//
//	go build -ldflags "-X github.com/jxburros/GWatch/internal/update.releaseKeys=ed25519:AAAA...,ed25519:BBBB..."
//
// Keys are separated by commas, newlines or spaces.
var releaseKeys string

// Errors returned by the verification path. They are worded for the user who
// sees them in Settings › Updates.
var (
	// ErrNoSigningKey means this build pins no public key, so nothing it
	// downloads could ever be trusted.
	ErrNoSigningKey = errors.New("this build has no release signing key, updates are disabled (see docs)")
	// ErrUnsigned means the release has no .sig asset for this file.
	ErrUnsigned = errors.New("the release is not signed")
	// ErrBadSignature means a signature was present but no pinned key accepts it.
	ErrBadSignature = errors.New("signature verification failed")
)

// PublicKey is a trusted release signing key.
type PublicKey struct {
	ID  string // 8 hex chars, the first 4 bytes of sha256(key)
	Key ed25519.PublicKey
}

// String renders the key the way release_keys.txt stores it.
func (k PublicKey) String() string { return FormatPublicKey(k.Key) }

// FormatPublicKey renders an ed25519 public key as "ed25519:<base64>".
func FormatPublicKey(pub ed25519.PublicKey) string {
	return "ed25519:" + base64.StdEncoding.EncodeToString(pub)
}

// KeyID is the short identifier written into a .sig file: the first 4 bytes of
// the SHA-256 of the public key, in hex.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:4])
}

// ParsePublicKey reads one "ed25519:<base64>" entry.
func ParsePublicKey(s string) (PublicKey, error) {
	s = strings.TrimSpace(s)
	rest, ok := strings.CutPrefix(s, "ed25519:")
	if !ok {
		return PublicKey{}, fmt.Errorf("public key %q: expected the form ed25519:<base64>", s)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rest))
	if err != nil {
		return PublicKey{}, fmt.Errorf("public key: not valid base64: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return PublicKey{}, fmt.Errorf("public key: got %d bytes, want %d", len(raw), ed25519.PublicKeySize)
	}
	pub := ed25519.PublicKey(raw)
	return PublicKey{ID: KeyID(pub), Key: pub}, nil
}

// ParseKeys reads a key list: one key per line, "#" comments and blank lines
// ignored. Commas also separate keys, so the ldflags override can be a single
// argument.
func ParseKeys(text string) ([]PublicKey, error) {
	var keys []PublicKey
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		for _, field := range strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
			k, err := ParsePublicKey(field)
			if err != nil {
				return nil, err
			}
			keys = append(keys, k)
		}
	}
	return keys, sc.Err()
}

// TrustedKeys returns the release signing keys this build pins: the ldflags
// override when set, otherwise release_keys.txt. A malformed entry yields no
// keys at all, which disables updates rather than trusting a partial list.
func TrustedKeys() []PublicKey {
	text := embeddedReleaseKeys
	if strings.TrimSpace(releaseKeys) != "" {
		text = releaseKeys
	}
	keys, err := ParseKeys(text)
	if err != nil {
		return nil
	}
	return keys
}

// KeyIDs lists the short ids of the pinned release keys, for display.
func KeyIDs() []string {
	keys := TrustedKeys()
	ids := make([]string, 0, len(keys))
	for _, k := range keys {
		ids = append(ids, k.ID)
	}
	return ids
}

// SigningEnabled reports whether this build pins at least one release signing
// key. When it does not, Check still reports what the latest release is but
// Download refuses to hand back a file.
func (c *Client) SigningEnabled() bool { return len(TrustedKeys()) > 0 }

// Signature is a parsed .sig file.
type Signature struct {
	KeyID string
	Sig   []byte
	// Asset, Version and ReleaseSig are the release binding; a file written
	// before it existed has none of them.
	Asset      string
	Version    string
	ReleaseSig []byte
}

// Release names what a release signature vouches for: one asset, under its
// published name, in one version of its product.
type Release struct {
	Asset   string // e.g. gwatch-agent-linux-amd64
	Version string // e.g. 1.2.3, without the tag's prefix
}

// releaseDomain separates release signatures from every other message the
// key signs, the bare digests of the legacy sig line included.
const releaseDomain = "gwatch-release-v1"

// ReleaseMessage is the exact byte string a release signature covers.
func ReleaseMessage(rel Release, digest []byte) []byte {
	return []byte(fmt.Sprintf("%s\nasset: %s\nversion: %s\nsha256: %s\n", releaseDomain, rel.Asset, rel.Version, hex.EncodeToString(digest)))
}

// validRelease rejects names and versions that could not appear in a
// release, or that would make the signed message ambiguous.
func validRelease(rel Release) error {
	if rel.Asset == "" || rel.Version == "" {
		return errors.New("a release signature needs both the asset name and the version")
	}
	if strings.ContainsAny(rel.Asset+rel.Version, " \t\r\n:/\\") {
		return fmt.Errorf("asset %q or version %q contains a character a release name cannot", rel.Asset, rel.Version)
	}
	return nil
}

// FormatSignature renders a signature file: the legacy signature over the
// digest alone and the release signature binding asset, version and digest.
func FormatSignature(pub ed25519.PublicKey, legacy []byte, rel Release, releaseSig []byte) string {
	return fmt.Sprintf("%s\nkey: %s\nsig: %s\nasset: %s\nversion: %s\nrelease-sig: %s\n",
		SignatureFormat, KeyID(pub), base64.StdEncoding.EncodeToString(legacy),
		rel.Asset, rel.Version, base64.StdEncoding.EncodeToString(releaseSig))
}

// ParseSignature reads a .sig file. The header line must be exactly the format
// marker; everything after it is parsed leniently on whitespace.
func ParseSignature(text string) (Signature, error) {
	var out Signature
	sc := bufio.NewScanner(strings.NewReader(text))
	if !sc.Scan() {
		return out, errors.New("signature file is empty")
	}
	if strings.TrimSpace(sc.Text()) != SignatureFormat {
		return out, fmt.Errorf("signature file: unknown format %q (want %s)", strings.TrimSpace(sc.Text()), SignatureFormat)
	}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return out, fmt.Errorf("signature file: cannot parse %q", line)
		}
		name, value = strings.ToLower(strings.TrimSpace(name)), strings.TrimSpace(value)
		switch name {
		case "key":
			out.KeyID = strings.ToLower(value)
		case "sig":
			raw, err := base64.StdEncoding.DecodeString(value)
			if err != nil {
				return out, fmt.Errorf("signature file: sig is not valid base64: %w", err)
			}
			out.Sig = raw
		case "asset":
			out.Asset = value
		case "version":
			out.Version = value
		case "release-sig":
			raw, err := base64.StdEncoding.DecodeString(value)
			if err != nil {
				return out, fmt.Errorf("signature file: release-sig is not valid base64: %w", err)
			}
			out.ReleaseSig = raw
		}
	}
	if err := sc.Err(); err != nil {
		return out, err
	}
	if len(out.Sig) != ed25519.SignatureSize {
		return out, fmt.Errorf("signature file: signature is %d bytes, want %d", len(out.Sig), ed25519.SignatureSize)
	}
	return out, nil
}

// VerifyRelease checks a .sig file against the SHA-256 digest of the asset it
// covers and the release that asset was offered as. Only the release
// signature counts: a file without one, or one naming a different asset or
// version, is refused however valid its legacy digest signature is, because
// that signature says nothing about which product or version the bytes are.
// It returns ErrNoSigningKey when keys is empty and ErrBadSignature when the
// file does not vouch for rel.
func VerifyRelease(digest []byte, sigText string, rel Release, keys []PublicKey) error {
	if len(keys) == 0 {
		return ErrNoSigningKey
	}
	if err := validRelease(rel); err != nil {
		return fmt.Errorf("%w: %s", ErrBadSignature, err)
	}
	sig, err := ParseSignature(sigText)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrBadSignature, err)
	}
	if len(sig.ReleaseSig) != ed25519.SignatureSize {
		return fmt.Errorf("%w: the signature does not name the release it is for (signed with an older gwatch-sign?)", ErrBadSignature)
	}
	if sig.Asset != rel.Asset || sig.Version != rel.Version {
		return fmt.Errorf("%w: the signature is for %s %s, not %s %s", ErrBadSignature, sig.Asset, sig.Version, rel.Asset, rel.Version)
	}
	msg := ReleaseMessage(rel, digest)
	// The key id is a hint, not a credential: try the key it names first, then
	// every other pinned key, so a re-signed release still verifies.
	for _, k := range keys {
		if k.ID == sig.KeyID && ed25519.Verify(k.Key, msg, sig.ReleaseSig) {
			return nil
		}
	}
	for _, k := range keys {
		if ed25519.Verify(k.Key, msg, sig.ReleaseSig) {
			return nil
		}
	}
	return ErrBadSignature
}

// FileDigest returns the SHA-256 digest of a file, read in a stream.
func FileDigest(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

// SignDigest signs a SHA-256 digest with priv. This is the legacy signature
// older installed copies check; see VerifyRelease for the one that binds the
// release.
func SignDigest(priv ed25519.PrivateKey, digest []byte) []byte {
	return ed25519.Sign(priv, digest)
}

// SignRelease signs the release binding of an asset with priv.
func SignRelease(priv ed25519.PrivateKey, rel Release, digest []byte) []byte {
	return ed25519.Sign(priv, ReleaseMessage(rel, digest))
}

// SignFile writes <path>.sig for path as an asset of the given release
// version, signed with priv. The asset name is the file's own name, which is
// the name it is published under.
func SignFile(priv ed25519.PrivateKey, path, version string) (string, error) {
	rel := Release{Asset: filepath.Base(path), Version: version}
	if err := validRelease(rel); err != nil {
		return "", err
	}
	digest, err := FileDigest(path)
	if err != nil {
		return "", err
	}
	pub, _ := priv.Public().(ed25519.PublicKey)
	out := path + SignatureExt
	body := FormatSignature(pub, SignDigest(priv, digest), rel, SignRelease(priv, rel, digest))
	if err := os.WriteFile(out, []byte(body), 0o644); err != nil {
		return "", err
	}
	return out, nil
}

// VerifyFile checks <path>.sig against path, as the asset of that name in the
// given release version, using keys.
func VerifyFile(path, version string, keys []PublicKey) error {
	digest, err := FileDigest(path)
	if err != nil {
		return err
	}
	text, err := os.ReadFile(path + SignatureExt)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrUnsigned
		}
		return err
	}
	return VerifyRelease(digest, string(text), Release{Asset: filepath.Base(path), Version: version}, keys)
}

// GenerateKey makes a new release signing key pair.
func GenerateKey() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(nil)
}

// EncodeSeed renders the private half of a key as a base64 32-byte seed, the
// form written to release.key and stored in the GWATCH_SIGNING_KEY secret.
func EncodeSeed(priv ed25519.PrivateKey) string {
	return base64.StdEncoding.EncodeToString(priv.Seed())
}

// ParsePrivateKey reads a base64 seed (32 bytes) or a full base64 ed25519
// private key (64 bytes). Surrounding whitespace and "#" comment lines are
// ignored, so a key file can carry a note.
func ParsePrivateKey(text string) (ed25519.PrivateKey, error) {
	var b64 string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		b64 = line
		break
	}
	if b64 == "" {
		return nil, errors.New("signing key is empty")
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("signing key: not valid base64: %w", err)
	}
	switch len(raw) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(raw), nil
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(raw), nil
	default:
		return nil, fmt.Errorf("signing key: got %d bytes, want a %d-byte seed", len(raw), ed25519.SeedSize)
	}
}

package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// MinPasswordLength is the shortest password a user account may have.
const MinPasswordLength = 8

// ErrBadPassword is returned by VerifyPassword when the password does not
// match the stored hash.
var ErrBadPassword = errors.New("incorrect password")

// argon2 parameters. Tuned for an always-on home server: ~64 MiB and a single
// pass over four lanes takes tens of milliseconds, which is plenty for a login
// form and cheap enough not to be a denial-of-service lever by itself (the
// login endpoint is rate limited on top).
const (
	argonTime    uint32 = 1
	argonMemory  uint32 = 64 * 1024 // KiB
	argonThreads uint8  = 4
	argonKeyLen  uint32 = 32
	saltLen             = 16
)

// ValidatePassword reports whether a proposed password is acceptable.
func ValidatePassword(pw string) error {
	if utf8.RuneCountInString(pw) < MinPasswordLength {
		return fmt.Errorf("the password must be at least %d characters", MinPasswordLength)
	}
	if len(pw) > 1024 {
		return errors.New("the password is too long")
	}
	return nil
}

// Algorithm names a password-hashing scheme. The values double as the
// setting GeneralSettings.PasswordHashing stores and as the identifier at the
// start of an encoded hash, so a stored hash always says which one made it.
type Algorithm string

const (
	// Argon2id is the default and the recommended choice: it is memory hard,
	// which is what makes guessing passwords on a GPU or a custom chip
	// expensive. It comes from golang.org/x/crypto, not the standard library.
	Argon2id Algorithm = "argon2id"
	// PBKDF2 is PBKDF2-HMAC-SHA256 from the standard library (crypto/pbkdf2).
	// It is offered for owners who would rather depend on nothing outside the
	// Go standard library (#49). It is not memory hard, so at an equal time
	// cost it is cheaper to attack with a GPU than Argon2id; for a monitor
	// that lives on a home or office LAN that trade-off is acceptable, but it
	// is opt-in and Argon2id stays the default.
	PBKDF2 Algorithm = "pbkdf2-sha256"
)

// PBKDF2Iterations is the work factor for new PBKDF2 hashes. 600,000 rounds of
// HMAC-SHA256 is the figure OWASP recommends; it takes a few hundred
// milliseconds on a small always-on box, which is in the same range as the
// Argon2id parameters above. The count is written into each hash, so it can be
// raised later without invalidating the hashes already stored.
const PBKDF2Iterations = 600_000

// pbkdf2MinIterations is the least work a stored PBKDF2 hash may claim. It is
// not a compatibility floor (no GWatch release ever wrote fewer rounds than
// PBKDF2Iterations); it stops a hash edited into the database by hand from
// quietly turning verification into almost no work at all.
const pbkdf2MinIterations = 100_000

// pbkdf2MaxIterations bounds the other direction: a hash claiming billions of
// rounds would make every sign-in for that name a denial-of-service lever.
const pbkdf2MaxIterations = 10_000_000

// ParseAlgorithm turns a setting value into an Algorithm. The empty string is
// what every settings document saved before #49 contains, and it means the
// default. ok is false for anything else that is not a known name.
func ParseAlgorithm(s string) (Algorithm, bool) {
	switch Algorithm(strings.TrimSpace(s)) {
	case "", Argon2id:
		return Argon2id, true
	case PBKDF2:
		return PBKDF2, true
	}
	return Argon2id, false
}

// HashPassword returns a PHC-style encoded argon2id hash:
//
//	$argon2id$v=19$m=65536,t=1,p=4$<b64 salt>$<b64 hash>
func HashPassword(pw string) (string, error) {
	return HashPasswordWith(pw, Argon2id)
}

// HashPasswordWith hashes pw with the chosen algorithm. Argon2id hashes look
// like the ones HashPassword has always produced; PBKDF2 hashes look like
//
//	$pbkdf2-sha256$i=600000$<b64 salt>$<b64 hash>
//
// with the same unpadded standard base64 for salt and hash. An unknown
// algorithm is an error rather than a silent fall back to the default, so a
// caller that passes something unvalidated finds out.
func HashPasswordWith(pw string, alg Algorithm) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	switch alg {
	case Argon2id:
		return encodeHash(pw, salt, argonTime, argonMemory, argonThreads, argonKeyLen), nil
	case PBKDF2:
		return encodePBKDF2(pw, salt, PBKDF2Iterations, int(argonKeyLen))
	}
	return "", fmt.Errorf("unknown password hashing algorithm %q", alg)
}

func encodePBKDF2(pw string, salt []byte, iter, keyLen int) (string, error) {
	sum, err := pbkdf2.Key(sha256.New, pw, salt, iter, keyLen)
	if err != nil {
		return "", fmt.Errorf("pbkdf2: %w", err)
	}
	b64 := base64.RawStdEncoding.EncodeToString
	return fmt.Sprintf("$%s$i=%d$%s$%s", PBKDF2, iter, b64(salt), b64(sum)), nil
}

// NeedsRehash reports whether a stored hash should be replaced by a fresh one
// made with alg. It is true when the hash was made by a different algorithm
// or with weaker parameters than the ones new hashes get today. The login
// handler asks this after a successful sign-in — the only moment the
// plaintext is at hand — so switching the setting migrates each account the
// next time its owner signs in, in either direction (#49).
func NeedsRehash(encoded string, alg Algorithm) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) < 2 || Algorithm(parts[1]) != alg {
		return true
	}
	switch alg {
	case Argon2id:
		want := fmt.Sprintf("m=%d,t=%d,p=%d", argonMemory, argonTime, argonThreads)
		return len(parts) != 6 || parts[3] != want
	case PBKDF2:
		iter, _, _, err := parsePBKDF2(parts)
		return err != nil || iter < PBKDF2Iterations
	}
	return false
}

func encodeHash(pw string, salt []byte, t, m uint32, p uint8, keyLen uint32) string {
	sum := argon2.IDKey([]byte(pw), salt, t, m, p, keyLen)
	b64 := base64.RawStdEncoding.EncodeToString
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, m, t, p, b64(salt), b64(sum))
}

// dummyHashes holds one valid hash per algorithm, each computed once on first
// use, that SpendVerifyTime checks against. They let a sign-in attempt for a
// name that does not exist spend the same work as one for a real account, so
// that the two cannot be told apart by how quickly they are rejected (#91).
// There is one per algorithm because the two cost different amounts of time:
// with PBKDF2 selected, the accounts that have signed in since carry PBKDF2
// hashes, and an unknown name has to look like them (#49).
var dummyHashes = map[Algorithm]func() string{
	Argon2id: sync.OnceValue(func() string { return dummyHashFor(Argon2id) }),
	PBKDF2:   sync.OnceValue(func() string { return dummyHashFor(PBKDF2) }),
}

func dummyHashFor(alg Algorithm) string {
	h, err := HashPasswordWith("gwatch-timing-equaliser", alg)
	if err != nil {
		return ""
	}
	return h
}

// SpendVerifyTime runs one password verification against a fixed hash made
// with alg and discards the result. The login handler calls it when no account
// matched the supplied name, passing the algorithm currently selected, so that
// an unknown user costs the same time as a wrong password for a real one and
// the timing reveals nothing (#91). An unknown alg falls back to Argon2id.
func SpendVerifyTime(password string, alg Algorithm) {
	f, ok := dummyHashes[alg]
	if !ok {
		f = dummyHashes[Argon2id]
	}
	if h := f(); h != "" {
		_ = VerifyPassword(h, password)
	}
}

// VerifyPassword checks pw against an encoded hash produced by HashPassword or
// HashPasswordWith, whichever algorithm made it: the hash names its own scheme,
// so changing the hashing setting never locks anybody out. It returns
// ErrBadPassword on a mismatch and a different error when the stored hash
// cannot be parsed.
func VerifyPassword(encoded, pw string) error {
	parts := strings.Split(encoded, "$")
	if len(parts) >= 2 && parts[0] == "" && Algorithm(parts[1]) == PBKDF2 {
		return verifyPBKDF2(parts, pw)
	}
	// ["", "argon2id", "v=19", "m=…,t=…,p=…", salt, hash]
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return errors.New("unsupported password hash format")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return errors.New("unsupported password hash version")
	}
	var m, t uint32
	var p uint8
	for _, kv := range strings.Split(parts[3], ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return errors.New("malformed password hash parameters")
		}
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return errors.New("malformed password hash parameters")
		}
		switch k {
		case "m":
			m = uint32(n)
		case "t":
			t = uint32(n)
		case "p":
			if n == 0 || n > 255 {
				return errors.New("malformed password hash parameters")
			}
			p = uint8(n)
		}
	}
	if m == 0 || t == 0 || p == 0 {
		return errors.New("malformed password hash parameters")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return errors.New("malformed password hash salt")
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return errors.New("malformed password hash")
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrBadPassword
	}
	return nil
}

// parsePBKDF2 reads the split form of a PBKDF2 hash:
// ["", "pbkdf2-sha256", "i=600000", salt, hash].
func parsePBKDF2(parts []string) (iter int, salt, want []byte, err error) {
	if len(parts) != 5 || parts[0] != "" || Algorithm(parts[1]) != PBKDF2 {
		return 0, nil, nil, errors.New("unsupported password hash format")
	}
	v, ok := strings.CutPrefix(parts[2], "i=")
	if !ok {
		return 0, nil, nil, errors.New("malformed password hash parameters")
	}
	n, perr := strconv.Atoi(v)
	if perr != nil || n < pbkdf2MinIterations || n > pbkdf2MaxIterations {
		return 0, nil, nil, errors.New("malformed password hash parameters")
	}
	salt, err = base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(salt) == 0 {
		return 0, nil, nil, errors.New("malformed password hash salt")
	}
	want, err = base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(want) == 0 || len(want) > 64 {
		return 0, nil, nil, errors.New("malformed password hash")
	}
	return n, salt, want, nil
}

func verifyPBKDF2(parts []string, pw string) error {
	iter, salt, want, err := parsePBKDF2(parts)
	if err != nil {
		return err
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, iter, len(want))
	if err != nil {
		return fmt.Errorf("pbkdf2: %w", err)
	}
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrBadPassword
	}
	return nil
}

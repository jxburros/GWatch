package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
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

// HashPassword returns a PHC-style encoded argon2id hash:
//
//	$argon2id$v=19$m=65536,t=1,p=4$<b64 salt>$<b64 hash>
func HashPassword(pw string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	return encodeHash(pw, salt, argonTime, argonMemory, argonThreads, argonKeyLen), nil
}

func encodeHash(pw string, salt []byte, t, m uint32, p uint8, keyLen uint32) string {
	sum := argon2.IDKey([]byte(pw), salt, t, m, p, keyLen)
	b64 := base64.RawStdEncoding.EncodeToString
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, m, t, p, b64(salt), b64(sum))
}

// VerifyPassword checks pw against an encoded hash produced by HashPassword.
// It returns ErrBadPassword on a mismatch and a different error when the
// stored hash cannot be parsed.
func VerifyPassword(encoded, pw string) error {
	parts := strings.Split(encoded, "$")
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

package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const (
	// SessionCookie is the name of the browser session cookie.
	SessionCookie = "gwatch_session"
	// SessionLifetime is how long a session stays valid without use. Every
	// request that presents a live session slides the expiry forward.
	SessionLifetime = 30 * 24 * time.Hour
	// APIKeyPrefix is the visible marker at the start of every API key.
	APIKeyPrefix = "gw_"
	// apiKeySecretLen is the number of random characters after the prefix.
	apiKeySecretLen = 40
	// KeyPrefixLen is how many characters of a key are stored in the clear so
	// the UI can show which key a row refers to ("gw_a1b2c3de").
	KeyPrefixLen = 12
)

// keyAlphabet is lowercase base32 without the easily confused characters
// removed — a plain 32-symbol alphabet keeps 5 bits per character.
const keyAlphabet = "abcdefghijklmnopqrstuvwxyz234567"

// NewSessionToken returns a fresh 32-byte session token as hex.
func NewSessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// NewAPIKey returns a new API key ("gw_" + 40 random base32 characters) and
// the prefix that is stored alongside its hash for display.
func NewAPIKey() (key, prefix string, err error) {
	b := make([]byte, apiKeySecretLen)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generate api key: %w", err)
	}
	var sb strings.Builder
	sb.WriteString(APIKeyPrefix)
	for _, v := range b {
		sb.WriteByte(keyAlphabet[int(v)%len(keyAlphabet)])
	}
	key = sb.String()
	return key, KeyPrefix(key), nil
}

// KeyPrefix returns the displayable leading part of an API key.
func KeyPrefix(key string) string {
	if len(key) <= KeyPrefixLen {
		return key
	}
	return key[:KeyPrefixLen]
}

// HashToken returns the sha256 hex digest used to store session tokens and API
// keys. Tokens are high-entropy random strings, so a plain digest (no salt, no
// stretching) is the right trade-off: it is not guessable and lookups stay a
// single indexed query.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// LooksLikeAPIKey reports whether s has the shape of a GWatch API key. It is
// only used to decide which credential a request is presenting, never to
// accept one.
func LooksLikeAPIKey(s string) bool {
	return strings.HasPrefix(s, APIKeyPrefix) && len(s) > len(APIKeyPrefix)+8
}

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
	// AgentTokenPrefix marks an agent enrolment token. It is deliberately
	// distinct from APIKeyPrefix: an agent token is not an API key and grants
	// nothing but the right to submit one machine's hardware readings, so the
	// two must never be mistaken for one another in a log or a config file.
	AgentTokenPrefix = "gwa_"
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

// NewAgentToken returns a new agent enrolment token and the prefix stored
// alongside its hash for display.
func NewAgentToken() (token, prefix string, err error) {
	b := make([]byte, apiKeySecretLen)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generate agent token: %w", err)
	}
	var sb strings.Builder
	sb.WriteString(AgentTokenPrefix)
	for _, v := range b {
		sb.WriteByte(keyAlphabet[int(v)%len(keyAlphabet)])
	}
	token = sb.String()
	return token, KeyPrefix(token), nil
}

// NewWallboardToken returns the random part of a wallboard's projected
// address. It is not a credential in the sense the others here are: it names
// one read-only board and nothing else, carries no prefix because it travels
// in a URL somebody has to type by hand, and is stored as it is so that the
// address can be shown again.
func NewWallboardToken() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate wallboard token: %w", err)
	}
	var sb strings.Builder
	for _, v := range b {
		sb.WriteByte(keyAlphabet[int(v)%len(keyAlphabet)])
	}
	return sb.String(), nil
}

// LooksLikeAgentToken reports whether s has the shape of an agent token. Like
// LooksLikeAPIKey it only decides which credential is being presented, never
// whether to accept it.
func LooksLikeAgentToken(s string) bool {
	return strings.HasPrefix(s, AgentTokenPrefix) && len(s) > len(AgentTokenPrefix)+8
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
	// AgentTokenPrefix starts with APIKeyPrefix, so an agent token would
	// otherwise be taken for a malformed API key and reported as one.
	return strings.HasPrefix(s, APIKeyPrefix) && !strings.HasPrefix(s, AgentTokenPrefix) &&
		len(s) > len(APIKeyPrefix)+8
}

// ---- pairing codes ----

// PairingCodeAlphabet is what a pairing code is drawn from: Crockford base32
// with every character a person reading one screen and typing on another
// keyboard confuses taken out — no I, L, O or U, and no 0 or 1 either, so
// there is nothing left for a misread O or l to be mistaken for. Thirty
// symbols remain, which is a little under five bits each.
const PairingCodeAlphabet = "23456789ABCDEFGHJKMNPQRSTVWXYZ"

// PairingCodeLen is how many significant characters a pairing code carries.
// Eight of them is 30^8, roughly 6.6e11 codes, or just over 39 bits. That is
// the balance the code exists to strike: it is typed by hand off a screen, so
// every extra character is a chance to get it wrong, but it also has to be
// hopeless to guess. It is, comfortably — a code is alive for fifteen minutes
// and a wrong one costs an attacker from the same per-IP failure budget as a
// wrong password (ten a minute), so a whole quarter of an hour of guessing
// covers about 150 of 660 billion possibilities. Fewer characters would start
// to make that arithmetic worth doing; more would make the code worse to read
// down a phone line, which is the only reason it is short in the first place.
const PairingCodeLen = 8

// pairingCodeGroup is how many characters sit between the dashes. Four and
// four is the shape a person keeps their place in while typing; the dashes are
// presentation only and are ignored on the way back in.
const pairingCodeGroup = 4

// NewPairingCode returns a fresh pairing code in its display form, "XXXX-XXXX".
func NewPairingCode() (string, error) {
	// 256 is not a multiple of 30, so reducing a random byte with % would
	// quietly make the first sixteen symbols more likely than the other
	// fourteen. Bytes at or above the largest multiple of 30 are thrown away
	// instead, which costs a few extra bytes of randomness and nothing else.
	const limit = 256 - (256 % len(PairingCodeAlphabet))
	out := make([]byte, 0, PairingCodeLen)
	buf := make([]byte, PairingCodeLen)
	for len(out) < PairingCodeLen {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("generate pairing code: %w", err)
		}
		for _, v := range buf {
			if int(v) >= limit {
				continue
			}
			out = append(out, PairingCodeAlphabet[int(v)%len(PairingCodeAlphabet)])
			if len(out) == PairingCodeLen {
				break
			}
		}
	}
	return FormatPairingCode(string(out)), nil
}

// FormatPairingCode groups significant characters for display. It assumes its
// input is already normalised; NormalizePairingCode is what produces that.
func FormatPairingCode(code string) string {
	var sb strings.Builder
	for i := 0; i < len(code); i++ {
		if i > 0 && i%pairingCodeGroup == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(code[i])
	}
	return sb.String()
}

// NormalizePairingCode turns a code as somebody typed it into the one
// canonical form that is hashed and compared, reporting whether it could be a
// pairing code at all.
//
// It is generous about everything that carries no meaning: case, spaces, tabs,
// and dashes wherever they land or fail to. An installer prompt gets pasted
// into, read aloud into, and re-typed with the dash in the wrong place, and
// none of that should be the difference between enrolling a machine and
// staring at an error. What it will not do is guess: a character outside the
// alphabet means the person misread something, and the honest answer is that
// this is not a code rather than a silently different one.
func NormalizePairingCode(s string) (string, bool) {
	var sb strings.Builder
	for _, r := range s {
		switch {
		case r == '-' || r == '_' || r == ' ' || r == '\t' || r == '\n' || r == '\r':
			continue
		case r >= 'a' && r <= 'z':
			r -= 'a' - 'A'
		}
		if !strings.ContainsRune(PairingCodeAlphabet, r) {
			return "", false
		}
		sb.WriteRune(r)
		if sb.Len() > PairingCodeLen {
			return "", false
		}
	}
	if sb.Len() != PairingCodeLen {
		return "", false
	}
	return FormatPairingCode(sb.String()), true
}

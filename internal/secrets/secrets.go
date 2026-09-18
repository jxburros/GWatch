// Package secrets provides a tiny envelope for encrypting individual
// configuration values at rest with a machine-local key file.
//
// The key file holds a hex-encoded 32-byte AES key and is created with 0600
// permissions the first time it is needed. Sealed values are stored as
// "enc:v1:" + base64url(nonce || ciphertext) so that legacy plaintext values
// (written before encryption existed) remain recognisable and readable.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Prefix marks a sealed value.
const Prefix = "enc:v1:"

// KeySize is the AES-256 key length in bytes.
const KeySize = 32

const nonceSize = 12

// ErrCannotDecrypt is returned when a sealed value cannot be opened with the
// current key (wrong or replaced key file, or tampered ciphertext).
var ErrCannotDecrypt = errors.New("secrets: cannot decrypt value")

// Box seals and opens individual values with a single symmetric key.
type Box struct {
	key []byte
}

// Load reads the key file at path, creating it with a fresh random key (mode
// 0600) if it does not exist.
func Load(path string) (*Box, error) {
	raw, err := os.ReadFile(path)
	if err == nil {
		key, err := parseKey(raw)
		if err != nil {
			return nil, fmt.Errorf("read key file %s: %w", path, err)
		}
		return &Box{key: key}, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read key file %s: %w", path, err)
	}
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	if err := writeKeyFile(path, key); err != nil {
		return nil, err
	}
	return &Box{key: key}, nil
}

// NewBox builds a Box from an existing 32-byte key (mainly for tests).
func NewBox(key []byte) (*Box, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("secrets: key must be %d bytes, got %d", KeySize, len(key))
	}
	dup := make([]byte, KeySize)
	copy(dup, key)
	return &Box{key: dup}, nil
}

func parseKey(raw []byte) ([]byte, error) {
	trimmed := strings.TrimSpace(string(raw))
	if len(trimmed) == hex.EncodedLen(KeySize) {
		key, err := hex.DecodeString(trimmed)
		if err != nil {
			return nil, fmt.Errorf("decode hex key: %w", err)
		}
		return key, nil
	}
	// Tolerate a raw 32-byte key file written by an older or external tool.
	if len(raw) == KeySize {
		key := make([]byte, KeySize)
		copy(key, raw)
		return key, nil
	}
	return nil, fmt.Errorf("expected a %d-byte key (hex or raw)", KeySize)
}

// writeKeyFile writes the key atomically: temp file in the same directory,
// then rename into place.
func writeKeyFile(path string, key []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create key dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".gwatch-key-*")
	if err != nil {
		return fmt.Errorf("create key file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod key file: %w", err)
	}
	if _, err := tmp.WriteString(hex.EncodeToString(key) + "\n"); err != nil {
		tmp.Close()
		return fmt.Errorf("write key file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync key file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close key file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("install key file: %w", err)
	}
	return nil
}

// IsSealed reports whether v is a sealed value produced by Seal.
func IsSealed(v string) bool { return strings.HasPrefix(v, Prefix) }

// Seal encrypts plain. The empty string seals to the empty string so that an
// unset secret stays visibly unset.
func (b *Box) Seal(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	if b == nil {
		return "", errors.New("secrets: no key loaded")
	}
	gcm, err := b.gcm()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	sealed := gcm.Seal(nil, nonce, []byte(plain), nil)
	return Prefix + base64.RawURLEncoding.EncodeToString(append(nonce, sealed...)), nil
}

// Open decrypts a sealed value. Values that are not sealed (legacy plaintext)
// are returned unchanged.
func (b *Box) Open(v string) (string, error) {
	if !IsSealed(v) {
		return v, nil
	}
	if b == nil {
		return "", ErrCannotDecrypt
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(v, Prefix))
	if err != nil || len(raw) <= nonceSize {
		return "", ErrCannotDecrypt
	}
	gcm, err := b.gcm()
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, raw[:nonceSize], raw[nonceSize:], nil)
	if err != nil {
		return "", ErrCannotDecrypt
	}
	return string(plain), nil
}

func (b *Box) gcm() (cipher.AEAD, error) {
	block, err := aes.NewCipher(b.key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

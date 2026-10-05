// gwatch-seal-secret emits only a GitHub-compatible encrypted secret envelope.
package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"golang.org/x/crypto/nacl/box"
	"os"
)

type envelope struct {
	EncryptedValue string `json:"encrypted_value"`
	KeyID          string `json:"key_id"`
}

func seal(secret, publicKey, keyID string) (envelope, error) {
	if secret == "" || keyID == "" {
		return envelope{}, errors.New("secret and destination key id are required")
	}
	raw, err := base64.StdEncoding.DecodeString(publicKey)
	if err != nil || len(raw) != 32 {
		return envelope{}, errors.New("destination key must be a base64 32-byte public key")
	}
	var key [32]byte
	copy(key[:], raw)
	ciphertext, err := box.SealAnonymous(nil, []byte(secret), &key, rand.Reader)
	if err != nil {
		return envelope{}, errors.New("could not encrypt secret")
	}
	return envelope{EncryptedValue: base64.StdEncoding.EncodeToString(ciphertext), KeyID: keyID}, nil
}
func main() {
	publicKey := flag.String("public-key", "", "destination GitHub Environment public key")
	keyID := flag.String("key-id", "", "destination Environment key id")
	flag.Parse()
	encrypted, err := seal(os.Getenv("GWATCH_SIGNING_KEY"), *publicKey, *keyID)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err = json.NewEncoder(os.Stdout).Encode(encrypted); err != nil {
		fmt.Fprintln(os.Stderr, "cannot write encrypted envelope")
		os.Exit(1)
	}
}

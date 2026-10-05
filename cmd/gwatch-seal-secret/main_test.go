package main

import (
	"crypto/rand"
	"encoding/base64"
	"golang.org/x/crypto/nacl/box"
	"testing"
)

func TestSealedEnvelopeRoundTrip(t *testing.T) {
	public, private, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	out, err := seal("test-only-secret", base64.StdEncoding.EncodeToString(public[:]), "destination-key")
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(out.EncryptedValue)
	if err != nil {
		t.Fatal(err)
	}
	plain, ok := box.OpenAnonymous(nil, ciphertext, public, private)
	if !ok || string(plain) != "test-only-secret" || out.KeyID != "destination-key" {
		t.Fatal("sealed envelope does not decrypt")
	}
	_, wrong, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := box.OpenAnonymous(nil, ciphertext, public, wrong); ok {
		t.Fatal("wrong recipient decrypted")
	}
}
func TestRejectInvalidDestination(t *testing.T) {
	for _, key := range []string{"", "garbage", "AAAA"} {
		if _, err := seal("secret", key, "id"); err == nil {
			t.Fatal("invalid key accepted")
		}
	}
}

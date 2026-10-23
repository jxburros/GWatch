package auth

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestPBKDF2Hashing covers the standard-library alternative (#49): the encoded
// form, verification, rejection of a wrong password, and that a hash made by
// either algorithm verifies through the one VerifyPassword, which is what lets
// the setting be switched without locking anybody out.
func TestPBKDF2Hashing(t *testing.T) {
	h, err := HashPasswordWith("correct horse battery", PBKDF2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, fmt.Sprintf("$pbkdf2-sha256$i=%d$", PBKDF2Iterations)) {
		t.Fatalf("encoded form: %s", h)
	}
	if PBKDF2Iterations < 600_000 {
		t.Fatalf("PBKDF2 work factor %d is below the 600,000 the issue asks for", PBKDF2Iterations)
	}
	if err := VerifyPassword(h, "correct horse battery"); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := VerifyPassword(h, "wrong horse battery"); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("wrong password: %v", err)
	}
	if h2, _ := HashPasswordWith("correct horse battery", PBKDF2); h2 == h {
		t.Fatal("salt is not random")
	}
	a, err := HashPasswordWith("correct horse battery", Argon2id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(a, "$argon2id$") {
		t.Fatalf("argon2id encoded form: %s", a)
	}
	if err := VerifyPassword(a, "correct horse battery"); err != nil {
		t.Fatalf("argon2id verify: %v", err)
	}
	if _, err := HashPasswordWith("x", Algorithm("md5")); err == nil {
		t.Fatal("an unknown algorithm must be refused, not silently replaced")
	}
}

// TestPBKDF2KnownAnswer pins the derivation to an independent reference: the
// hash below was computed outside GWatch, with Python's
// hashlib.pbkdf2_hmac("sha256", b"correct horse battery", b"gwatch-salt-1234",
// 600000, 32), so a change in how the salt, the iteration count or the
// encoding is fed in shows up here as a failure rather than as accounts that
// stop signing in after an upgrade.
func TestPBKDF2KnownAnswer(t *testing.T) {
	const h = "$pbkdf2-sha256$i=600000$Z3dhdGNoLXNhbHQtMTIzNA$OB5dyNDxPE3H3PqLj+8YXz0NFsAvmHLG9pzdaL7fqJk"
	if err := VerifyPassword(h, "correct horse battery"); err != nil {
		t.Fatalf("known answer: %v", err)
	}
	if err := VerifyPassword(h, "correct horse batterY"); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("known answer, wrong password: %v", err)
	}
}

func TestPBKDF2Malformed(t *testing.T) {
	for _, bad := range []string{
		"$pbkdf2-sha256$i=600000$YWJj",         // too few fields
		"$pbkdf2-sha256$n=600000$YWJj$YWJj",    // wrong parameter name
		"$pbkdf2-sha256$i=abc$YWJj$YWJj",       // not a number
		"$pbkdf2-sha256$i=1$YWJj$YWJj",         // far too little work
		"$pbkdf2-sha256$i=999999999$YWJj$YWJj", // far too much work
		"$pbkdf2-sha256$i=600000$!!!$YWJj",     // bad salt
		"$pbkdf2-sha256$i=600000$YWJj$",        // empty hash
		"$pbkdf2-sha1$i=600000$YWJj$YWJj",      // unsupported hash function
	} {
		if err := VerifyPassword(bad, "x"); err == nil || errors.Is(err, ErrBadPassword) {
			t.Errorf("malformed hash %q must be an error, got %v", bad, err)
		}
	}
}

func TestNeedsRehash(t *testing.T) {
	argon, _ := HashPasswordWith("pw-pw-pw-pw", Argon2id)
	pb, _ := HashPasswordWith("pw-pw-pw-pw", PBKDF2)
	weakArgon := encodeHash("pw-pw-pw-pw", []byte("0123456789abcdef"), 1, 8*1024, 1, 32)
	weakPB, err := encodePBKDF2("pw-pw-pw-pw", []byte("0123456789abcdef"), 200_000, 32)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		hash string
		alg  Algorithm
		want bool
	}{
		{"argon2id kept", argon, Argon2id, false},
		{"argon2id to pbkdf2", argon, PBKDF2, true},
		{"pbkdf2 kept", pb, PBKDF2, false},
		{"pbkdf2 to argon2id", pb, Argon2id, true},
		{"weak argon2id", weakArgon, Argon2id, true},
		{"weak pbkdf2", weakPB, PBKDF2, true},
		{"garbage", "garbage", Argon2id, true},
	}
	for _, c := range cases {
		if got := NeedsRehash(c.hash, c.alg); got != c.want {
			t.Errorf("%s: NeedsRehash = %v, want %v", c.name, got, c.want)
		}
	}
	// A weaker but still accepted PBKDF2 hash keeps verifying until it is
	// replaced: raising the work factor must not lock anybody out.
	if err := VerifyPassword(weakPB, "pw-pw-pw-pw"); err != nil {
		t.Fatalf("older PBKDF2 hash: %v", err)
	}
}

func TestParseAlgorithm(t *testing.T) {
	for in, want := range map[string]Algorithm{"": Argon2id, "argon2id": Argon2id, " pbkdf2-sha256 ": PBKDF2} {
		got, ok := ParseAlgorithm(in)
		if !ok || got != want {
			t.Errorf("ParseAlgorithm(%q) = %q, %v", in, got, ok)
		}
	}
	if _, ok := ParseAlgorithm("bcrypt"); ok {
		t.Error("bcrypt is not an option")
	}
}

// TestSpendVerifyTimeEitherAlgorithm checks that the timing equaliser has a
// usable hash for each algorithm: a malformed dummy would make it return at
// once, and an unknown name would again be rejected faster than a real one.
func TestSpendVerifyTimeEitherAlgorithm(t *testing.T) {
	for _, alg := range []Algorithm{Argon2id, PBKDF2} {
		h := dummyHashes[alg]()
		if !strings.HasPrefix(h, "$"+string(alg)+"$") {
			t.Errorf("%s dummy hash has the wrong form: %q", alg, h)
		}
		if err := VerifyPassword(h, "not it"); !errors.Is(err, ErrBadPassword) {
			t.Errorf("%s dummy hash is not usable: %v", alg, err)
		}
		SpendVerifyTime("not it", alg)
	}
	SpendVerifyTime("not it", Algorithm("unknown"))
}

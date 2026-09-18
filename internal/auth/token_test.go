package auth

import (
	"strings"
	"testing"
)

func TestPairingCodeShape(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		code, err := NewPairingCode()
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != PairingCodeLen+1 || code[PairingCodeLen/2] != '-' {
			t.Fatalf("a code should read as XXXX-XXXX, got %q", code)
		}
		for _, r := range strings.ReplaceAll(code, "-", "") {
			if !strings.ContainsRune(PairingCodeAlphabet, r) {
				t.Fatalf("code %q uses %q, which is not in the alphabet", code, r)
			}
		}
		// A repeat in two hundred draws from 6.6e11 possibilities would mean
		// the randomness is not what it claims to be.
		if seen[code] {
			t.Fatalf("code %q was generated twice", code)
		}
		seen[code] = true
		// Round-tripping its own output is the property everything else rests
		// on: the code shown to a person is the code that will be hashed.
		if back, ok := NormalizePairingCode(code); !ok || back != code {
			t.Fatalf("a freshly minted code did not normalise to itself: %q -> %q (%v)", code, back, ok)
		}
	}
}

// The alphabet is the whole reason the code is typeable, so the characters it
// leaves out must stay left out.
func TestPairingCodeAlphabetExcludesTheConfusableCharacters(t *testing.T) {
	for _, r := range "ILOU01" {
		if strings.ContainsRune(PairingCodeAlphabet, r) {
			t.Errorf("%q should not be in the pairing alphabet", r)
		}
	}
	if len(PairingCodeAlphabet) != 30 {
		t.Errorf("the alphabet should have 30 symbols, has %d", len(PairingCodeAlphabet))
	}
}

func TestNormalizePairingCode(t *testing.T) {
	const want = "A2B3-C4D5"
	for _, in := range []string{
		"A2B3-C4D5",
		"a2b3-c4d5",
		"A2B3C4D5",
		"a2b3c4d5",
		"  A2B3 - C4D5  ",
		"A2 B3 C4 D5",
		"a-2-b-3-c-4-d-5",
		"A2B3-C4D5\n",
		"A2B3_C4D5",
	} {
		got, ok := NormalizePairingCode(in)
		if !ok || got != want {
			t.Errorf("%q normalised to %q (%v), want %q", in, got, ok, want)
		}
	}

	for _, in := range []string{
		"",
		"A2B3-C4D",      // too short
		"A2B3-C4D56",    // too long
		"A2B3-C4DO",     // O is not in the alphabet, and is not folded to 0
		"A2B3-C4D1",     // nor is 1 folded to I or L
		"A2B3-C4D!",     // punctuation that is not a separator
		"A2B3-C4Dé",     // outside ASCII entirely
		"gwa_abcdefgh",  // an agent token is not a pairing code
		"A2B3-C4D5-E6F", // a third group
	} {
		if got, ok := NormalizePairingCode(in); ok {
			t.Errorf("%q should not be a pairing code, normalised to %q", in, got)
		}
	}
}

func TestFormatPairingCode(t *testing.T) {
	if got := FormatPairingCode("A2B3C4D5"); got != "A2B3-C4D5" {
		t.Errorf("got %q", got)
	}
	if got := FormatPairingCode(""); got != "" {
		t.Errorf("got %q", got)
	}
}

// A pairing code and an agent token are different credentials and must never
// be mistaken for one another, in a log or anywhere else.
func TestPairingCodeIsNotTakenForAToken(t *testing.T) {
	code, err := NewPairingCode()
	if err != nil {
		t.Fatal(err)
	}
	if LooksLikeAgentToken(code) || LooksLikeAPIKey(code) {
		t.Errorf("%q was taken for a token", code)
	}
}

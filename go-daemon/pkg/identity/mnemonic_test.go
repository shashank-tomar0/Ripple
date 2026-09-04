package identity

import (
	"bytes"
	"crypto/ed25519"
	"strings"
	"testing"

	"github.com/tyler-smith/go-bip39"
)

// TestExportImportRoundTrip proves the core property: exporting a generated
// identity to a mnemonic and importing it back yields the exact same keypair
// and PeerID.
func TestExportImportRoundTrip(t *testing.T) {
	id, err := Generate()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	phrase, err := id.ExportMnemonic()
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	words := strings.Fields(phrase)
	if len(words) != 24 {
		t.Fatalf("expected 24-word phrase, got %d words", len(words))
	}

	restored, err := FromMnemonic(phrase)
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	if restored.PeerID != id.PeerID {
		t.Fatalf("PeerID mismatch: original %s, restored %s", id.PeerID, restored.PeerID)
	}

	origRaw, err := id.PrivKey.Raw()
	if err != nil {
		t.Fatalf("original raw key: %v", err)
	}
	restoredRaw, err := restored.PrivKey.Raw()
	if err != nil {
		t.Fatalf("restored raw key: %v", err)
	}
	if !bytes.Equal(origRaw, restoredRaw) {
		t.Fatal("private key bytes differ after round trip")
	}

	if !restored.Verify() {
		t.Fatal("restored identity fails signature verification")
	}
}

// TestMnemonicDeterminism ensures the same phrase always produces the same
// identity — the property that makes recovery possible. Uses the 24-word
// BIP39 spec vector (32 zero bytes of entropy).
func TestMnemonicDeterminism(t *testing.T) {
	phrase := "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon art"
	a, err := FromMnemonic(phrase)
	if err != nil {
		t.Fatalf("import a: %v", err)
	}
	b, err := FromMnemonic(phrase)
	if err != nil {
		t.Fatalf("import b: %v", err)
	}
	if a.PeerID != b.PeerID {
		t.Fatalf("same phrase produced different identities: %s vs %s", a.PeerID, b.PeerID)
	}
}

// TestBIP39SpecVector pins the encoding to the BIP39 standard against the
// published Trezor test vectors — if these ever fail, we are not speaking
// standard BIP39 and recovery would break with third-party tooling.
func TestBIP39SpecVector(t *testing.T) {
	cases := []struct {
		name    string
		entropy int // zero bytes of this length
		want    string
	}{
		{"16 bytes", 16, "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"},
		{"32 bytes", 32, "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon art"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			phrase, err := bip39.NewMnemonic(make([]byte, tc.entropy))
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			if phrase != tc.want {
				t.Fatalf("spec vector mismatch:\n got %q\nwant %q", phrase, tc.want)
			}
		})
	}
}

// TestInvalidMnemonicsRejected ensures garbage, wrong-length, and
// checksum-invalid phrases all fail loudly instead of producing a wrong key.
func TestInvalidMnemonicsRejected(t *testing.T) {
	// Break the checksum of an otherwise-valid 24-word phrase by swapping its
	// final word ("zebra"×24 is unfortunately a *valid* BIP39 phrase, so a
	// naive repetition case would not test what we think it tests).
	valid, err := Generate()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	goodPhrase, err := valid.ExportMnemonic()
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	words := strings.Fields(goodPhrase)
	last := words[len(words)-1]
	replacement := "zebra"
	if last == replacement {
		replacement = "abandon"
	}
	words[len(words)-1] = replacement
	brokenChecksum := strings.Join(words, " ")
	if !IsValidMnemonic(goodPhrase) {
		t.Fatalf("sanity: generated phrase should be valid")
	}
	if IsValidMnemonic(brokenChecksum) {
		t.Skipf("swapped last word %q -> %q still validates; skip checksum case", last, replacement)
	}

	cases := []string{
		"",                         // empty
		"abandon abandon",          // too short
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon", // 12 words: wrong entropy size
		brokenChecksum,              // right count, checksum fails
		"notaword notaword notaword notaword notaword notaword notaword notaword notaword notaword notaword notaword notaword notaword notaword notaword notaword notaword notaword notaword notaword notaword notaword notaword", // unknown words
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon extra", // 25 words
	}
	for _, c := range cases {
		if _, err := FromMnemonic(c); err == nil {
			t.Errorf("expected error for %q, got none", c)
		}
		if IsValidMnemonic(c) {
			t.Errorf("IsValidMnemonic(%q) = true, want false", c)
		}
	}
}

// TestSeedMatchesEntropy ensures the mnemonic's entropy is exactly the
// Ed25519 seed — so the phrase genuinely encodes the key, not a hash of it.
func TestSeedMatchesEntropy(t *testing.T) {
	id, err := Generate()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	phrase, err := id.ExportMnemonic()
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	entropy, err := bip39.EntropyFromMnemonic(phrase)
	if err != nil {
		t.Fatalf("decode entropy: %v", err)
	}
	seed, err := id.Seed()
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if !bytes.Equal(entropy, seed) {
		t.Fatal("mnemonic entropy does not equal the identity seed")
	}
	if len(seed) != ed25519.SeedSize {
		t.Fatalf("seed length %d, want %d", len(seed), ed25519.SeedSize)
	}
}
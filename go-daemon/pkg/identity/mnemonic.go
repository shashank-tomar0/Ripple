// Mnemonic backup for Ripple identities.
//
// An Ed25519 keypair is fully determined by its 32-byte seed. We encode that
// seed as a standard BIP39 mnemonic (24 words, with a built-in checksum), so
// the same phrase on any device recreates the exact same PeerID. Losing the
// phone no longer means losing the identity.
//
// The mnemonic IS the key: whoever holds the words controls the identity, so
// treat them like a private key — never type them into an untrusted device.
package identity

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"strings"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/tyler-smith/go-bip39"
)

// ErrInvalidMnemonic is returned when a phrase fails BIP39 checksum validation
// or does not encode exactly 32 bytes of entropy.
var ErrInvalidMnemonic = errors.New("invalid mnemonic: bad checksum or wrong length")

// Seed returns the 32-byte Ed25519 seed that fully determines this identity.
//
// libp2p stores Ed25519 private keys as 64 bytes (seed || public key); the
// seed alone is sufficient to recreate the keypair.
func (id *Identity) Seed() ([]byte, error) {
	raw, err := id.PrivKey.Raw()
	if err != nil {
		return nil, fmt.Errorf("get raw private key: %w", err)
	}
	if len(raw) < ed25519.SeedSize {
		return nil, fmt.Errorf("raw key too short: got %d bytes, want >= %d", len(raw), ed25519.SeedSize)
	}
	seed := make([]byte, ed25519.SeedSize)
	copy(seed, raw[:ed25519.SeedSize])
	return seed, nil
}

// ExportMnemonic returns the identity's 32-byte seed as a 24-word BIP39
// mnemonic phrase. The same phrase fed to FromMnemonic reproduces this exact
// identity (same PeerID, same keypair).
func (id *Identity) ExportMnemonic() (string, error) {
	seed, err := id.Seed()
	if err != nil {
		return "", err
	}
	phrase, err := bip39.NewMnemonic(seed)
	if err != nil {
		return "", fmt.Errorf("encode mnemonic: %w", err)
	}
	return phrase, nil
}

// FromMnemonic recreates an identity from a BIP39 mnemonic phrase.
//
// The phrase must encode exactly 32 bytes of entropy (a 24-word BIP39
// phrase). Checksum errors, wrong word counts, and unknown words all fail
// with ErrInvalidMnemonic rather than silently producing a wrong key.
func FromMnemonic(phrase string) (*Identity, error) {
	entropy, err := bip39.EntropyFromMnemonic(strings.TrimSpace(phrase))
	if err != nil {
		return nil, ErrInvalidMnemonic
	}
	if len(entropy) != ed25519.SeedSize {
		return nil, fmt.Errorf("%w: got %d bytes of entropy, want %d", ErrInvalidMnemonic, len(entropy), ed25519.SeedSize)
	}

	// Rebuild the 64-byte libp2p raw key format: seed || public key.
	pub := ed25519.NewKeyFromSeed(entropy).Public().(ed25519.PublicKey)
	raw := make([]byte, 0, ed25519.SeedSize+len(pub))
	raw = append(raw, entropy...)
	raw = append(raw, pub...)

	priv, err := crypto.UnmarshalEd25519PrivateKey(raw)
	if err != nil {
		return nil, fmt.Errorf("unmarshal private key: %w", err)
	}

	pubKey := priv.GetPublic()
	pid, err := peer.IDFromPublicKey(pubKey)
	if err != nil {
		return nil, fmt.Errorf("derive peer ID: %w", err)
	}

	return &Identity{PrivKey: priv, PubKey: pubKey, PeerID: pid}, nil
}

// IsValidMnemonic reports whether a phrase is a well-formed 24-word BIP39
// mnemonic encoding 32 bytes of entropy. Useful for validating input before
// attempting recovery.
func IsValidMnemonic(phrase string) bool {
	entropy, err := bip39.EntropyFromMnemonic(strings.TrimSpace(phrase))
	return err == nil && len(entropy) == ed25519.SeedSize
}
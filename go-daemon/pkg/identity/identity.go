// Package identity manages cryptographic identities for Ripple peers.
//
// Each peer generates an Ed25519 keypair on first launch, stored as a PEM file.
// The PeerID is derived from the public key and serves as the peer's address
// in the libp2p network. No accounts, no phone numbers, no central registry.
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

// ErrNoIdentity is returned when no identity file exists at the given path.
var ErrNoIdentity = errors.New("no identity file found")

// Identity holds a peer's cryptographic keypair and derived PeerID.
type Identity struct {
	PrivKey crypto.PrivKey
	PubKey  crypto.PubKey
	PeerID  peer.ID
}

// Generate creates a new Ed25519 keypair and returns an Identity.
func Generate() (*Identity, error) {
	priv, pub, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate keypair: %w", err)
	}

	pid, err := peer.IDFromPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("derive peer ID: %w", err)
	}

	return &Identity{
		PrivKey: priv,
		PubKey:  pub,
		PeerID:  pid,
	}, nil
}

// LoadOrCreate loads an existing identity from path or creates a new one.
func LoadOrCreate(path string) (*Identity, error) {
	id, err := Load(path)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, ErrNoIdentity) {
		return nil, err
	}

	id, err = Generate()
	if err != nil {
		return nil, err
	}

	if err := Save(path, id); err != nil {
		return nil, fmt.Errorf("save identity: %w", err)
	}

	return id, nil
}

// Load reads an identity from a PEM-encoded file on disk.
func Load(path string) (*Identity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNoIdentity
		}
		return nil, fmt.Errorf("read identity file: %w", err)
	}

	block, _ := pem.Decode(data)
	if block == nil || block.Type != "RIPPLE IDENTITY" {
		return nil, errors.New("invalid identity file: expected PEM block")
	}

	priv, err := crypto.UnmarshalEd25519PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("unmarshal private key: %w", err)
	}

	pub := priv.GetPublic()
	pid, err := peer.IDFromPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("derive peer ID: %w", err)
	}

	return &Identity{PrivKey: priv, PubKey: pub, PeerID: pid}, nil
}

// Save writes the identity to a PEM-encoded file on disk.
func Save(path string, id *Identity) error {
	raw, err := id.PrivKey.Raw()
	if err != nil {
		return fmt.Errorf("get raw private key: %w", err)
	}

	block := &pem.Block{
		Type:  "RIPPLE IDENTITY",
		Bytes: raw,
	}

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create identity directory: %w", err)
	}

	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0600); err != nil {
		return fmt.Errorf("write identity file: %w", err)
	}

	return nil
}

// PeerIDFromBytes parses a PeerID from its byte representation.
func PeerIDFromBytes(b []byte) (peer.ID, error) {
	return peer.IDFromBytes(b)
}

// Verify that an Ed25519 keypair was generated correctly.
func (id *Identity) Verify() bool {
	// Sign a test message and verify
	msg := []byte("ripple-identity-verify")
	sig, err := id.PrivKey.Sign(msg)
	if err != nil {
		return false
	}
	ok, err := id.PubKey.Verify(msg, sig)
	return ok && err == nil
}

// ShortID returns the first 8 characters of the PeerID for display.
func (id *Identity) ShortID() string {
	s := id.PeerID.String()
	if len(s) > 8 {
		s = s[:8]
	}
	return s
}

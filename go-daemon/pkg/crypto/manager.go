// Package crypto provides end-to-end encryption using NaCl box (Curve25519 + XSalsa20-Poly1305).
//
// Each peer generates a Curve25519 keypair on first run, stored in ~/.ripple/e2e-keys/.
// Per-contact shared keys are derived via ECDH (scalarMult of our private key + peer's public key).
// Messages are encrypted per-recipient using NaCl secretbox with a per-message nonce.
package crypto

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/message"

	"golang.org/x/crypto/nacl/box"
	"golang.org/x/crypto/nacl/secretbox"
)

// ErrNoSharedKey is returned when no shared key exists for a peer.
var ErrNoSharedKey = errors.New("no shared key for peer")

// ErrNoPeerKey is returned when a peer's public key is not known.
var ErrNoPeerKey = errors.New("peer public key not known")

// ErrNotEncrypted is returned when trying to decrypt an unencrypted message.
var ErrNotEncrypted = errors.New("message is not encrypted")

// Manager handles E2E encryption state for the local peer.
type Manager struct {
	Keypair      *Keypair
	dataDir      string
	knownKeys    map[string]*[32]byte // peerID -> public key
	sharedCache  map[string]*[32]byte // peerID -> derived shared key (cached)
	mu           sync.RWMutex
	knownKeysPath string
}

// KnownKeysFile is the on-disk JSON format for storing known peer public keys.
type KnownKeysFile struct {
	Keys map[string]string `json:"keys"` // peerID -> hex-encoded public key
}

// NewManager creates a new E2E encryption manager.
func NewManager(dataDir string) (*Manager, error) {
	keysDir := filepath.Join(dataDir, "e2e-keys")

	// Load or create our keypair
	kp, err := LoadOrCreateKeypair(keysDir)
	if err != nil {
		return nil, fmt.Errorf("load/create keypair: %w", err)
	}

	m := &Manager{
		Keypair:       kp,
		dataDir:       keysDir,
		knownKeys:     make(map[string]*[32]byte),
		sharedCache:   make(map[string]*[32]byte),
		knownKeysPath: filepath.Join(keysDir, "known_keys.json"),
	}

	// Load known peer keys from disk
	if err := m.LoadKnownKeys(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("load known keys: %w", err)
	}

	return m, nil
}

// GetSharedKey derives (or returns cached) shared key for a peer.
func (m *Manager) GetSharedKey(peerID string) (*[32]byte, error) {
	m.mu.RLock()
	if cached, ok := m.sharedCache[peerID]; ok {
		m.mu.RUnlock()
		return cached, nil
	}
	if _, ok := m.knownKeys[peerID]; !ok {
		m.mu.RUnlock()
		return nil, ErrNoSharedKey
	}
	m.mu.RUnlock()

	// Derive and cache
	return m.deriveAndCacheSharedKey(peerID)
}

// deriveAndCacheSharedKey derives the shared key for a peer and caches it.
// Must be called with write lock held or after releasing read lock.
func (m *Manager) deriveAndCacheSharedKey(peerID string) (*[32]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after acquiring write lock
	if cached, ok := m.sharedCache[peerID]; ok {
		return cached, nil
	}

	peerPub, ok := m.knownKeys[peerID]
	if !ok {
		return nil, ErrNoPeerKey
	}

	shared := SharedSecret(&m.Keypair.PrivateKey, peerPub)
	m.sharedCache[peerID] = &shared
	return &shared, nil
}

// SetPeerKey stores a peer's public key (received via QR or KeyExchange).
func (m *Manager) SetPeerKey(peerID string, pubKey [32]byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Store the key
	m.knownKeys[peerID] = &pubKey

	// Invalidate cached shared key if exists
	delete(m.sharedCache, peerID)

	// Persist to disk
	return m.SaveKnownKeys()
}

// GetPeerKey retrieves a peer's stored public key.
func (m *Manager) GetPeerKey(peerID string) (*[32]byte, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	pub, ok := m.knownKeys[peerID]
	return pub, ok
}

// EncryptMessage encrypts the payload of a message for its recipient.
// Returns the encrypted payload and sets the Nonce and KeyID fields.
func (m *Manager) EncryptMessage(msg *message.Message) error {
	if msg.Recipient == "" {
		// Broadcast messages are not E2E encrypted
		return nil
	}

	sharedKey, err := m.GetSharedKey(msg.Recipient)
	if err != nil {
		// No shared key yet - can't encrypt
		return err
	}

	encryptedPayload, err := Encrypt([]byte(msg.Payload), sharedKey)
	if err != nil {
		return fmt.Errorf("encrypt payload: %w", err)
	}

	// Set encryption metadata
	msg.Payload = encryptedPayload
	msg.Nonce = encryptedPayload[:48] // First 48 hex chars = 24-byte nonce
	msg.KeyID = hex.EncodeToString(m.Keypair.PublicKey[:8]) // First 8 bytes of our public key

	return nil
}

// DecryptMessage decrypts the payload of a received message.
func (m *Manager) DecryptMessage(msg *message.Message) error {
	if !msg.IsEncrypted() {
		return ErrNotEncrypted
	}

	if msg.Sender == "" {
		return errors.New("encrypted message missing sender")
	}

	sharedKey, err := m.GetSharedKey(msg.Sender)
	if err != nil {
		// Try to derive if we have the peer's key but not cached
		if pub, ok := m.GetPeerKey(msg.Sender); ok {
			shared := SharedSecret(&m.Keypair.PrivateKey, pub)
			m.mu.Lock()
			m.sharedCache[msg.Sender] = &shared
			m.mu.Unlock()
			sharedKey = &shared
		} else {
			return fmt.Errorf("no shared key for sender %s: %w", msg.Sender, ErrNoSharedKey)
		}
	}

	plaintext, err := Decrypt(msg.Payload, sharedKey)
	if err != nil {
		return fmt.Errorf("decrypt payload: %w", err)
	}

	msg.Payload = string(plaintext)
	msg.Nonce = ""
	msg.KeyID = ""
	return nil
}

// LoadKnownKeys loads known public keys from disk.
func (m *Manager) LoadKnownKeys() error {
	data, err := os.ReadFile(m.knownKeysPath)
	if err != nil {
		return err
	}

	var kf KnownKeysFile
	if err := json.Unmarshal(data, &kf); err != nil {
		return fmt.Errorf("unmarshal known keys: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for peerID, hexKey := range kf.Keys {
		keyBytes, err := hex.DecodeString(hexKey)
		if err != nil || len(keyBytes) != 32 {
			continue // Skip invalid keys
		}
		var key [32]byte
		copy(key[:], keyBytes)
		m.knownKeys[peerID] = &key
	}

	return nil
}

// SaveKnownKeys persists known public keys to disk.
func (m *Manager) SaveKnownKeys() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	kf := KnownKeysFile{
		Keys: make(map[string]string, len(m.knownKeys)),
	}
	for peerID, key := range m.knownKeys {
		kf.Keys[peerID] = hex.EncodeToString(key[:])
	}

	data, err := json.MarshalIndent(kf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal known keys: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(m.knownKeysPath), 0700); err != nil {
		return fmt.Errorf("create keys dir: %w", err)
	}

	return os.WriteFile(m.knownKeysPath, data, 0600)
}

// MyPublicKeyHex returns our public key as hex string (for QR display).
func (m *Manager) MyPublicKeyHex() string {
	return hex.EncodeToString(m.Keypair.PublicKey[:])
}

// MyKeyID returns our KeyID (first 8 bytes of public key as hex).
func (m *Manager) MyKeyID() string {
	return hex.EncodeToString(m.Keypair.PublicKey[:8])
}
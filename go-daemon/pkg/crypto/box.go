// Package crypto provides end-to-end encryption using NaCl box (Curve25519 + XSalsa20-Poly1305).
//
// Each peer generates a Curve25519 keypair on first run, stored in ~/.ripple/e2e-keys/.
// Per-contact shared keys are derived via ECDH (scalarMult of our private key + peer's public key).
// Messages are encrypted per-recipient using NaCl secretbox with a per-message nonce.
package crypto

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/crypto/nacl/box"
	"golang.org/x/crypto/nacl/secretbox"

	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/message"
)

// ErrNoKeypair is returned when no keypair exists at the expected path.
var ErrNoKeypair = errors.New("no E2E keypair found")

// ErrNoSharedKey is returned when no shared key exists for a peer.
var ErrNoSharedKey = errors.New("no shared key for peer")

// ErrNotEncrypted is returned when trying to decrypt an unencrypted message.
var ErrNotEncrypted = errors.New("message is not encrypted")

// Keypair is a Curve25519 keypair for E2E encryption.
type Keypair struct {
	PublicKey  [32]byte
	PrivateKey [32]byte
}

// SessionKey is a per-conversation symmetric key derived from ECDH.
type SessionKey [32]byte

// KeypairFile is the on-disk JSON format for storing the keypair.
type KeypairFile struct {
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
}

// GenerateKeypair creates a new Curve25519 keypair for E2E encryption.
func GenerateKeypair() (*Keypair, error) {
	pub, priv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate keypair: %w", err)
	}
	return &Keypair{PublicKey: *pub, PrivateKey: *priv}, nil
}

// LoadOrCreateKeypair loads keys from ~/.ripple/e2e-keys/ or creates new ones.
func LoadOrCreateKeypair(dataDir string) (*Keypair, error) {
	keysDir := filepath.Join(dataDir, "e2e-keys")
	keypairPath := filepath.Join(keysDir, "keypair.json")

	kp, err := LoadKeypair(keypairPath)
	if err == nil {
		return kp, nil
	}
	if !errors.Is(err, ErrNoKeypair) {
		return nil, err
	}

	// Create new keypair
	kp, err = GenerateKeypair()
	if err != nil {
		return nil, err
	}

	if err := SaveKeypair(keypairPath, kp); err != nil {
		return nil, err
	}

	return kp, nil
}

// SaveKeypair writes the keypair to a file (encrypted with file permissions 0600).
func SaveKeypair(path string, kp *Keypair) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create keys directory: %w", err)
	}

	data := KeypairFile{
		PublicKey:  hex.EncodeToString(kp.PublicKey[:]),
		PrivateKey: hex.EncodeToString(kp.PrivateKey[:]),
	}

	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal keypair: %w", err)
	}

	if err := os.WriteFile(path, jsonData, 0600); err != nil {
		return fmt.Errorf("write keypair file: %w", err)
	}

	return nil
}

// LoadKeypair reads a keypair from a file.
func LoadKeypair(path string) (*Keypair, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNoKeypair
		}
		return nil, fmt.Errorf("read keypair file: %w", err)
	}

	var kf KeypairFile
	if err := json.Unmarshal(data, &kf); err != nil {
		return nil, fmt.Errorf("unmarshal keypair: %w", err)
	}

	pubBytes, err := hex.DecodeString(kf.PublicKey)
	if err != nil || len(pubBytes) != 32 {
		return nil, errors.New("invalid public key in keypair file")
	}
	privBytes, err := hex.DecodeString(kf.PrivateKey)
	if err != nil || len(privBytes) != 32 {
		return nil, errors.New("invalid private key in keypair file")
	}

	var kp Keypair
	copy(kp.PublicKey[:], pubBytes)
	copy(kp.PrivateKey[:], privBytes)

	return &kp, nil
}

// SharedSecret computes ECDH shared secret = scalarMult(privateKey, peerPublicKey).
// This is the per-contact shared key used for message encryption.
func SharedSecret(private, peerPublic *[32]byte) [32]byte {
	var shared [32]byte
	box.Precompute(&shared, peerPublic, private)
	return shared
}

// NonceSize is the size of a NaCl secretbox nonce (24 bytes).
const NonceSize = 24

// Encrypt encrypts a plaintext message for a specific recipient.
// Uses NaCl secretbox with a per-message random nonce.
// The shared key is derived from our private key + recipient's public key.
// Returns hex-encoded: nonce || ciphertext
func Encrypt(plaintext []byte, key *[32]byte) (string, error) {
	var nonce [NonceSize]byte
	if _, err := io.ReadFull(rand.Reader, nonce[:]); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}

	// secretbox encrypts with the 32-byte key
	ciphertext := secretbox.Seal(nil, plaintext, &nonce, key)

	// Encode as hex: nonce || ciphertext
	out := make([]byte, NonceSize+len(ciphertext))
	copy(out[:NonceSize], nonce[:])
	copy(out[NonceSize:], ciphertext)

	return hex.EncodeToString(out), nil
}

// Decrypt decrypts a ciphertext (nonce || ciphertext) using the shared key.
// Returns the plaintext.
func Decrypt(ciphertextHex string, key *[32]byte) ([]byte, error) {
	ciphertext, err := hex.DecodeString(ciphertextHex)
	if err != nil {
		return nil, fmt.Errorf("decode hex: %w", err)
	}

	if len(ciphertext) < NonceSize {
		return nil, errors.New("ciphertext too short")
	}

	var nonce [NonceSize]byte
	copy(nonce[:], ciphertext[:NonceSize])

	plaintext, ok := secretbox.Open(nil, ciphertext[NonceSize:], &nonce, key)
	if !ok {
		return nil, errors.New("decrypt failed: invalid key or corrupted ciphertext")
	}

	return plaintext, nil
}

// PublicKeyHex returns the public key as a hex string.
func (kp *Keypair) PublicKeyHex() string {
	return hex.EncodeToString(kp.PublicKey[:])
}

// PrivateKeyHex returns the private key as a hex string.
func (kp *Keypair) PrivateKeyHex() string {
	return hex.EncodeToString(kp.PrivateKey[:])
}

// KeyID returns the first 8 hex characters of the public key for identification.
func (kp *Keypair) KeyID() string {
	return kp.PublicKeyHex()[:8]
}

// KnownKeysFile is the on-disk JSON format for storing known peer public keys.
type KnownKeysFile struct {
	Keys map[string]string `json:"keys"` // peerID -> hex-encoded public key
}

// Manager handles E2E encryption state for the local peer.
type Manager struct {
	Keypair     *Keypair
	dataDir     string
	knownKeys   map[string]*[32]byte // peerID -> public key
	sharedCache map[string]*[32]byte // peerID -> derived shared key (cached)
	mu          sync.RWMutex
	knownKeysPath string
}

// NewManager creates a new E2E encryption manager.
func NewManager(dataDir string) (*Manager, error) {
	keysDir := filepath.Join(dataDir, "e2e-keys")

	kp, err := LoadOrCreateKeypair(keysDir)
	if err != nil {
		return nil, fmt.Errorf("load/create keypair: %w", err)
	}

	knownKeysPath := filepath.Join(keysDir, "known_keys.json")

	m := &Manager{
		Keypair:        kp,
		dataDir:        dataDir,
		knownKeys:      make(map[string]*[32]byte),
		sharedCache:    make(map[string]*[32]byte),
		knownKeysPath:  knownKeysPath,
	}

	// Load known peer keys
	if err := m.LoadKnownKeys(); err != nil {
		// Non-fatal: just log and continue with empty known keys
		fmt.Fprintf(os.Stderr, "⚠️  Failed to load known keys: %v\n", err)
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
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after acquiring write lock
	if cached, ok := m.sharedCache[peerID]; ok {
		return cached, nil
	}

	pubKey, ok := m.knownKeys[peerID]
	if !ok {
		return nil, ErrNoSharedKey
	}

	shared := SharedSecret(&m.Keypair.PrivateKey, pubKey)
	m.sharedCache[peerID] = &shared
	return &shared, nil
}

// SetPeerKey stores a peer's public key (received via QR or KeyExchange).
func (m *Manager) SetPeerKey(peerID string, pubKey [32]byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.knownKeys[peerID] = &pubKey
	// Invalidate shared cache for this peer
	delete(m.sharedCache, peerID)

	return m.saveKnownKeysLocked()
}

// GetPeerKey retrieves a peer's stored public key.
func (m *Manager) GetPeerKey(peerID string) (*[32]byte, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	pk, ok := m.knownKeys[peerID]
	return pk, ok
}

// MyPublicKeyHex returns our public key as hex string (for QR display).
func (m *Manager) MyPublicKeyHex() string {
	if m.Keypair == nil {
		return ""
	}
	return m.Keypair.PublicKeyHex()
}

// MyKeyID returns our key ID (first 8 hex chars of public key).
func (m *Manager) MyKeyID() string {
	return m.Keypair.KeyID()
}

// LoadKnownKeys loads known public keys from disk.
func (m *Manager) LoadKnownKeys() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.knownKeysPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No known keys file yet
		}
		return fmt.Errorf("read known keys: %w", err)
	}

	var kf KnownKeysFile
	if err := json.Unmarshal(data, &kf); err != nil {
		return fmt.Errorf("unmarshal known keys: %w", err)
	}

	for peerID, hexKey := range kf.Keys {
		keyBytes, err := hex.DecodeString(hexKey)
		if err != nil || len(keyBytes) != 32 {
			fmt.Fprintf(os.Stderr, "⚠️  Invalid key for peer %s: %v\n", peerID, err)
			continue
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
	return m.saveKnownKeysLocked()
}

// saveKnownKeysLocked persists known public keys to disk.
// The caller must already hold m.mu (read or write).
func (m *Manager) saveKnownKeysLocked() error {
	if err := os.MkdirAll(filepath.Dir(m.knownKeysPath), 0700); err != nil {
		return fmt.Errorf("create keys directory: %w", err)
	}

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

	return os.WriteFile(m.knownKeysPath, data, 0600)
}

// ImportKeyFromHex imports a peer's public key from a hex string.
func (m *Manager) ImportKeyFromHex(peerID, hexKey string) error {
	keyBytes, err := hex.DecodeString(hexKey)
	if err != nil || len(keyBytes) != 32 {
		return errors.New("invalid public key hex (must be 64 hex chars = 32 bytes)")
	}
	var key [32]byte
	copy(key[:], keyBytes)
	return m.SetPeerKey(peerID, key)
}

// HasPeerKey returns true if we have a public key for the given peer.
func (m *Manager) HasPeerKey(peerID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.knownKeys[peerID]
	return ok
}

// KnownPeerIDs returns a list of peer IDs we have keys for.
func (m *Manager) KnownPeerIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.knownKeys))
	for id := range m.knownKeys {
		ids = append(ids, id)
	}
	return ids
}

// HandleKeyExchangeMessage processes an incoming key exchange message.
// Stores the peer's Curve25519 public key so future messages can be encrypted.
func (m *Manager) HandleKeyExchangeMessage(msg *message.Message) error {
	var payload message.KeyExchangePayload
	if err := json.Unmarshal([]byte(msg.Payload), &payload); err != nil {
		return fmt.Errorf("parse key exchange: %w", err)
	}

	pubBytes, err := hex.DecodeString(payload.PublicKeyHex)
	if err != nil {
		return fmt.Errorf("decode public key: %w", err)
	}

	var pubKey [32]byte
	copy(pubKey[:], pubBytes[:32])

	m.SetPeerKey(msg.Sender, pubKey)
	return nil
}

// ShouldSendKeyExchange returns true if we should send a key exchange to this peer.
func (m *Manager) ShouldSendKeyExchange(peerID string) bool {
	_, has := m.GetPeerKey(peerID)
	return !has
}

// EncryptMessage encrypts the payload of a message for its recipient.
// Sets the Nonce and KeyID fields for decryption on the recipient side.
func (m *Manager) EncryptMessage(msg *message.Message) error {
	if msg.Recipient == "" {
		// Broadcast messages are not E2E encrypted
		return nil
	}

	sharedKey, err := m.GetSharedKey(msg.Recipient)
	if err != nil {
		return err
	}

	encryptedPayload, err := Encrypt([]byte(msg.Payload), sharedKey)
	if err != nil {
		return fmt.Errorf("encrypt payload: %w", err)
	}

	msg.Payload = encryptedPayload
	msg.Nonce = encryptedPayload[:48] // First 48 hex chars = 24-byte nonce
	msg.KeyID = m.MyKeyID()

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
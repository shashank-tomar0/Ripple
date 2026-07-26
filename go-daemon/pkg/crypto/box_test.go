package crypto

import (
	"testing"
	"encoding/hex"
)

func TestGenerateKeypair(t *testing.T) {
	kp, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair() error = %v", err)
	}
	if kp.PublicKey == [32]byte{} {
		t.Error("GenerateKeypair() returned zero public key")
	}
	if kp.PrivateKey == [32]byte{} {
		t.Error("GenerateKeypair() returned zero private key")
	}
}

func TestEncryptDecrypt(t *testing.T) {
	// Generate two keypairs for Alice and Bob
	alice, _ := GenerateKeypair()
	bob, _ := GenerateKeypair()

	// Derive shared secret (Alice's perspective)
	aliceShared := SharedSecret(&alice.PrivateKey, &bob.PublicKey)

	// Derive shared secret (Bob's perspective)
	bobShared := SharedSecret(&bob.PrivateKey, &alice.PublicKey)

	// Verify both sides get the same key
	if aliceShared != bobShared {
		t.Error("Shared secrets don't match")
	}

	// Test encryption/decryption
	plaintext := []byte("hello this is a secret message")
	ciphertext, err := Encrypt(plaintext, &aliceShared)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	decrypted, err := Decrypt(ciphertext, &bobShared)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}

	if string(decrypted) != string(plaintext) {
		t.Errorf("Decrypt() = %s, want %s", decrypted, plaintext)
	}
}

func TestKeypairPersistence(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/test-key.json"

	kp1, _ := GenerateKeypair()
	if err := SaveKeypair(path, kp1); err != nil {
		t.Fatalf("SaveKeypair() error = %v", err)
	}

	kp2, err := LoadKeypair(path)
	if err != nil {
		t.Fatalf("LoadKeypair() error = %v", err)
	}

	if kp1.PublicKey != kp2.PublicKey {
		t.Error("Public keys don't match after save/load")
	}
	if kp1.PrivateKey != kp2.PrivateKey {
		t.Error("Private keys don't match after save/load")
	}
}

func TestEncryptWrongKey(t *testing.T) {
	alice, _ := GenerateKeypair()
	bob, _ := GenerateKeypair()
	eve, _ := GenerateKeypair()

	aliceShared := SharedSecret(&alice.PrivateKey, &bob.PublicKey)
	eveShared := SharedSecret(&eve.PrivateKey, &bob.PublicKey)

	// Alice encrypts for Bob
	plaintext := []byte("secret for bob")
	ciphertext, _ := Encrypt(plaintext, &aliceShared)

	// Eve tries to decrypt (should fail or return garbage)
	decrypted, _ := Decrypt(ciphertext, &eveShared)
	if string(decrypted) == string(plaintext) {
		t.Error("Eve was able to decrypt Alice's message!")
	}
}

func TestPublicKeyHex(t *testing.T) {
	kp, _ := GenerateKeypair()
	hexStr := kp.PublicKeyHex()

	// Should be 64 hex chars (32 bytes)
	if len(hexStr) != 64 {
		t.Errorf("PublicKeyHex() length = %d, want 64", len(hexStr))
	}

	// Should decode back to same bytes
	decoded, err := hex.DecodeString(hexStr)
	if err != nil {
		t.Errorf("Decode hex error = %v", err)
	}
	if len(decoded) != 32 {
		t.Errorf("Decoded length = %d, want 32", len(decoded))
	}

	var pk [32]byte
	copy(pk[:], decoded)
	if pk != kp.PublicKey {
		t.Error("Decoded public key doesn't match original")
	}
}

func TestKeyID(t *testing.T) {
	kp, _ := GenerateKeypair()
	keyID := kp.KeyID()

	// Should be 8 hex chars (first 4 bytes)
	if len(keyID) != 8 {
		t.Errorf("KeyID() length = %d, want 8", len(keyID))
	}

	// Should match first 8 chars of public key hex
	expected := kp.PublicKeyHex()[:8]
	if keyID != expected {
		t.Errorf("KeyID() = %s, want %s", keyID, expected)
	}
}

func TestSharedSecretSymmetry(t *testing.T) {
	// Test that shared secret is symmetric: Alice->Bob == Bob->Alice
	alice, _ := GenerateKeypair()
	bob, _ := GenerateKeypair()

	shared1 := SharedSecret(&alice.PrivateKey, &bob.PublicKey)
	shared2 := SharedSecret(&bob.PrivateKey, &alice.PublicKey)

	if shared1 != shared2 {
		t.Error("Shared secret is not symmetric")
	}
}

func TestMultipleEncryptDecrypt(t *testing.T) {
	alice, _ := GenerateKeypair()
	bob, _ := GenerateKeypair()

	shared := SharedSecret(&alice.PrivateKey, &bob.PublicKey)

	// Encrypt multiple messages
	messages := [][]byte{
		[]byte("short"),
		[]byte("medium length message for testing"),
		[]byte(""),
		make([]byte, 1000), // 1KB
	}

	for i, msg := range messages {
		ciphertext, err := Encrypt(msg, &shared)
		if err != nil {
			t.Errorf("Encrypt(%d) error = %v", i, err)
			continue
		}

		decrypted, err := Decrypt(ciphertext, &shared)
		if err != nil {
			t.Errorf("Decrypt(%d) error = %v", i, err)
			continue
		}

		if string(decrypted) != string(msg) {
			t.Errorf("Message %d: decrypted != original", i)
		}
	}
}

func TestLoadOrCreateKeypair(t *testing.T) {
	dir := t.TempDir()

	// First call should create
	kp1, err := LoadOrCreateKeypair(dir)
	if err != nil {
		t.Fatalf("LoadOrCreateKeypair() error = %v", err)
	}

	// Second call should load same key
	kp2, err := LoadOrCreateKeypair(dir)
	if err != nil {
		t.Fatalf("LoadOrCreateKeypair() second call error = %v", err)
	}

	if kp1.PublicKey != kp2.PublicKey {
		t.Error("Loaded keypair doesn't match created keypair")
	}
	if kp1.PrivateKey != kp2.PrivateKey {
		t.Error("Loaded private key doesn't match created private key")
	}
}

func TestImportKeyFromHex(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{
		Keypair:      nil, // Will be set in NewManager
		dataDir:      dir,
		knownKeys:    make(map[string]*[32]byte),
		sharedCache:  make(map[string]*[32]byte),
		knownKeysPath: dir + "/known_keys.json",
	}

	// Generate a keypair to get a valid public key
	kp, _ := GenerateKeypair()
	hexKey := kp.PublicKeyHex()

	err := m.ImportKeyFromHex("peer123", hexKey)
	if err != nil {
		t.Fatalf("ImportKeyFromHex() error = %v", err)
	}

	// Verify it was stored
	pk, ok := m.GetPeerKey("peer123")
	if !ok {
		t.Error("GetPeerKey() returned false after import")
	}
	if pk == nil || *pk != kp.PublicKey {
		t.Error("Imported public key doesn't match")
	}
}

func TestImportInvalidKey(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{
		Keypair:      nil,
		dataDir:      dir,
		knownKeys:    make(map[string]*[32]byte),
		sharedCache:  make(map[string]*[32]byte),
		knownKeysPath: dir + "/known_keys.json",
	}

	// Too short
	err := m.ImportKeyFromHex("peer123", "abc")
	if err == nil {
		t.Error("ImportKeyFromHex() should fail for short key")
	}

	// Invalid hex
	err = m.ImportKeyFromHex("peer123", "zzzzzzzz")
	if err == nil {
		t.Error("ImportKeyFromHex() should fail for invalid hex")
	}
}

func TestHasPeerKey(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{
		Keypair:      nil,
		dataDir:      dir,
		knownKeys:    make(map[string]*[32]byte),
		sharedCache:  make(map[string]*[32]byte),
		knownKeysPath: dir + "/known_keys.json",
	}

	kp, _ := GenerateKeypair()
	m.ImportKeyFromHex("peer1", kp.PublicKeyHex())

	if !m.HasPeerKey("peer1") {
		t.Error("HasPeerKey() should return true for known peer")
	}
	if m.HasPeerKey("peer2") {
		t.Error("HasPeerKey() should return false for unknown peer")
	}
}

func TestManagerGetSharedKey(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{
		Keypair:      nil,
		dataDir:      dir,
		knownKeys:    make(map[string]*[32]byte),
		sharedCache:  make(map[string]*[32]byte),
		knownKeysPath: dir + "/known_keys.json",
	}

	alice, _ := GenerateKeypair()
	bob, _ := GenerateKeypair()
	m.Keypair = &alice
	m.ImportKeyFromHex("bob", bob.PublicKeyHex())

	shared, err := m.GetSharedKey("bob")
	if err != nil {
		t.Fatalf("GetSharedKey() error = %v", err)
	}

	expected := SharedSecret(&alice.PrivateKey, &bob.PublicKey)
	if *shared != expected {
		t.Error("GetSharedKey() returned wrong shared key")
	}

	// Second call should return cached
	shared2, err := m.GetSharedKey("bob")
	if err != nil {
		t.Fatalf("GetSharedKey() second call error = %v", err)
	}
	if shared2 != shared {
		t.Error("GetSharedKey() should return cached key on second call")
	}
}

func TestManagerShouldSendKeyExchange(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{
		Keypair:      nil,
		dataDir:      dir,
		knownKeys:    make(map[string]*[32]byte),
		sharedCache:  make(map[string]*[32]byte),
		knownKeysPath: dir + "/known_keys.json",
	}

	// Should send key exchange for unknown peer
	if !m.ShouldSendKeyExchange("unknown_peer") {
		t.Error("ShouldSendKeyExchange() should return true for unknown peer")
	}

	// Import key and check it returns false
	kp, _ := GenerateKeypair()
	m.ImportKeyFromHex("known_peer", kp.PublicKeyHex())
	if m.ShouldSendKeyExchange("known_peer") {
		t.Error("ShouldSendKeyExchange() should return false for known peer")
	}
}

func TestHandleKeyExchangeMessage(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{
		Keypair:      nil,
		dataDir:      dir,
		knownKeys:    make(map[string]*[32]byte),
		sharedCache:  make(map[string]*[32]byte),
		knownKeysPath: dir + "/known_keys.json",
	}

	alice, _ := GenerateKeypair()
	m.Keypair = &alice

	// Create a key exchange message from Bob
	bob, _ := GenerateKeypair()
	msg := NewKeyExchange("bob_id", "Bob", bob.PublicKeyHex())

	// Handle the key exchange
	err := m.HandleKeyExchangeMessage(msg)
	if err != nil {
		t.Fatalf("HandleKeyExchangeMessage() error = %v", err)
	}

	// Verify Bob's key was stored
	pk, ok := m.GetPeerKey("bob_id")
	if !ok {
		t.Error("Peer key not stored after HandleKeyExchangeMessage")
	}
	if *pk != bob.PublicKey {
		t.Error("Stored public key doesn't match")
	}

	// Verify we can now derive shared key
	shared, err := m.GetSharedKey("bob_id")
	if err != nil {
		t.Fatalf("GetSharedKey() after key exchange error = %v", err)
	}

	expected := SharedSecret(&alice.PrivateKey, &bob.PublicKey)
	if *shared != expected {
		t.Error("Shared key derived from key exchange is incorrect")
	}
}
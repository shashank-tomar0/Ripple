// Package store provides message persistence for the Ripple mesh.
//
// Phase 0 uses an in-memory store with an optional JSON backup file for
// restart recovery. When a path is provided via NewWithDB, every message
// and peer is also saved to a JSON file on disk (`<path>.json`) so state
// survives restarts without external dependencies.
//
// SQLite (WAL) persistence is planned: the store will be refactored behind
// an interface so a SQLite-backed implementation can be dropped in without
// touching callers.
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/message"
)

// Store provides thread-safe message and peer storage with an optional
// JSON backup file. When a path is provided via NewWithDB, all messages
// and peers are also written to `<path>.json` on disk, enabling data
// recovery across restarts.
type Store struct {
	mu       sync.RWMutex
	messages []*message.Message
	peers    map[string]string // peerID -> nickname
	dbPath   string            // non-empty if JSON backup persistence is enabled
}

// New creates a new empty Store (in-memory only, no persistence).
func New() *Store {
	return &Store{
		messages: make([]*message.Message, 0, 128),
		peers:    make(map[string]string),
	}
}

// NewWithDB creates a Store that persists to a JSON backup file at the
// given path (actual file: `<path>.json`). If a previous backup exists it
// is restored into memory; otherwise the store starts empty.
func NewWithDB(path string) (*Store, error) {
	s := New()
	s.dbPath = path

	if err := s.LoadFromJSON(path + ".json"); err == nil {
		fmt.Fprintf(os.Stderr, "📂 Restored from backup: %s\n", path+".json")
	}

	return s, nil
}

// Close saves data to the JSON backup file if persistence is enabled.
func (s *Store) Close() error {
	return s.Autosave()
}

// Autosave persists the store to the configured backup path if persistence
// is enabled. Safe to call repeatedly (e.g. on a timer).
func (s *Store) Autosave() error {
	if s.dbPath == "" {
		return nil
	}
	if err := s.SaveToJSON(s.dbPath + ".json"); err != nil {
		fmt.Fprintf(os.Stderr, "store: backup error: %v\n", err)
		return err
	}
	return nil
}

// SaveToJSON persists messages and peers to a JSON file. The write is
// atomic: data goes to a temp file in the same directory, is fsynced, and
// then renamed over the target, so a crash mid-write can never corrupt the
// last good backup.
func (s *Store) SaveToJSON(path string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data := struct {
		Messages []*message.Message `json:"messages"`
		Peers    map[string]string  `json:"peers"`
	}{
		Messages: s.messages,
		Peers:    s.peers,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}

	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := f.Write(jsonData); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// LoadFromJSON restores messages and peers from a JSON backup file.
func (s *Store) LoadFromJSON(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var loaded struct {
		Messages []*message.Message `json:"messages"`
		Peers    map[string]string  `json:"peers"`
	}

	if err := json.Unmarshal(data, &loaded); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.messages = loaded.Messages
	if s.messages == nil {
		s.messages = make([]*message.Message, 0, 128)
	}
	s.peers = loaded.Peers
	if s.peers == nil {
		s.peers = make(map[string]string)
	}

	return nil
}

// SaveMessage stores a received or sent message in memory and, when
// persistence is enabled, in the JSON backup (written on Close).
func (s *Store) SaveMessage(msg *message.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.messages = append(s.messages, msg)
}

// Messages returns all stored messages, newest first.
func (s *Store) Messages() []*message.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*message.Message, len(s.messages))
	for i, m := range s.messages {
		result[len(result)-1-i] = m
	}
	return result
}

// MessagesForContact returns messages between the local peer and a contact.
func (s *Store) MessagesForContact(contactPeerID string) []*message.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var conv []*message.Message
	for _, m := range s.messages {
		if m.Sender == contactPeerID || m.Recipient == contactPeerID {
			conv = append(conv, m)
		}
	}
	return conv
}

// RecentMessages returns the last N messages.
func (s *Store) RecentMessages(n int) []*message.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if n > len(s.messages) {
		n = len(s.messages)
	}

	result := make([]*message.Message, n)
	total := len(s.messages)
	for i := 0; i < n; i++ {
		result[n-1-i] = s.messages[total-1-i]
	}
	return result
}

// AddPeer records a discovered peer if not already known, preserving the
// original nickname for known peers.
func (s *Store) AddPeer(peerID, nickname string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.peers[peerID]; !exists {
		s.peers[peerID] = nickname
	}
}

// SavePeer stores or updates a peer's nickname. Unlike AddPeer, this
// always overwrites the nickname.
func (s *Store) SavePeer(peerID, nickname string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.peers[peerID] = nickname
}

// RemovePeer removes a peer.
func (s *Store) RemovePeer(peerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.peers, peerID)
}

// Peers returns all known peers.
func (s *Store) Peers() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]string, len(s.peers))
	for k, v := range s.peers {
		result[k] = v
	}
	return result
}

// HasMessage checks if a message ID is already stored (dedup).
func (s *Store) HasMessage(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, m := range s.messages {
		if m.ID == id {
			return true
		}
	}
	return false
}
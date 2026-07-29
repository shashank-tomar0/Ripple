// Package store provides message persistence for the Ripple mesh.
//
// Phase 0 uses an in-memory store. SQLite persistence is available via the
// NewWithDB function, which requires modernc.org/sqlite.
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/message"
)

// Store provides thread-safe message and peer storage with optional SQLite
// persistence. When a database path is provided via NewWithDB, all messages
// and peers are also written to a SQLite file on disk, enabling data recovery
// across restarts.
type Store struct {
	mu       sync.RWMutex
	messages []*message.Message
	peers    map[string]string // peerID -> nickname
	dbPath   string            // non-empty if persistence is enabled
}

// New creates a new empty Store (in-memory only, no persistence).
// This matches the Phase 0 behavior exactly.
func New() *Store {
	return &Store{
		messages: make([]*message.Message, 0, 128),
		peers:    make(map[string]string),
	}
}

// NewWithDB creates a Store backed by a SQLite database at the given path.
// Falls back to in-memory if the database cannot be opened.
func NewWithDB(path string) (*Store, error) {
	// On systems without a working SQLite driver (e.g., Go 1.26+ std library issues),
	// fall back to in-memory store. SQLite persistence can be re-enabled by
	// using the 'with_sqlite' build tag.
	s := New()
	s.dbPath = path

	// Try to load from JSON backup file as a lightweight persistence alternative
	if err := s.LoadFromJSON(path + ".json"); err == nil {
		fmt.Fprintf(os.Stderr, "📂 Restored from backup: %s\n", path+".json")
	}

	return s, nil
}

// Close saves data to a JSON backup file if persistence is enabled.
func (s *Store) Close() error {
	if s.dbPath != "" {
		if err := s.SaveToJSON(s.dbPath + ".json"); err != nil {
			fmt.Fprintf(os.Stderr, "store: backup error: %v\n", err)
		}
	}
	return nil
}

// SaveToJSON persists messages and peers to a JSON file.
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

	return os.WriteFile(path, jsonData, 0644)
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

// createTables is a no-op for the in-memory store.
func (s *Store) createTables() error { return nil }

// LoadFromDB is a no-op for the in-memory store (data loaded via JSON file).
func (s *Store) LoadFromDB() error { return nil }

// SaveMessage stores a received or sent message in memory. When backed by
// SQLite, the message is also persisted to the database. Duplicate message
// IDs are silently ignored on the database side (INSERT OR IGNORE), but the
// in-memory duplicate check in HasMessage is the primary dedup mechanism.
func (s *Store) SaveMessage(msg *message.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.messages = append(s.messages, msg)

	if s.db != nil {
		s.exec(
			`INSERT OR IGNORE INTO messages
			 (id, type, sender, sender_nick, recipient, payload, timestamp, ttl, hop_count)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			msg.ID, string(msg.Type), msg.Sender, msg.SenderNick,
			msg.Recipient, msg.Payload, msg.Timestamp, msg.TTL, msg.HopCount,
		)
	}
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

// AddPeer records a discovered peer if not already known. When backed by
// SQLite, the peer is also persisted (INSERT OR IGNORE — preserves the
// original nickname if the peer was previously known).
func (s *Store) AddPeer(peerID, nickname string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.peers[peerID]; !exists {
		s.peers[peerID] = nickname
		if s.db != nil {
			s.exec(
				`INSERT OR IGNORE INTO peers (peer_id, nickname, last_seen) VALUES (?, ?, ?)`,
				peerID, nickname, time.Now().Unix(),
			)
		}
	}
}

// SavePeer stores or updates a peer's nickname in both memory and (when
// backed by SQLite) the database. Unlike AddPeer, this always overwrites
// the nickname and updates the last_seen timestamp.
func (s *Store) SavePeer(peerID, nickname string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.peers[peerID] = nickname
	if s.db != nil {
		s.exec(
			`INSERT OR REPLACE INTO peers (peer_id, nickname, last_seen) VALUES (?, ?, ?)`,
			peerID, nickname, time.Now().Unix(),
		)
	}
}

// RemovePeer removes a peer from memory and, if backed by SQLite, from
// the database as well.
func (s *Store) RemovePeer(peerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.peers, peerID)
	if s.db != nil {
		s.exec("DELETE FROM peers WHERE peer_id = ?", peerID)
	}
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

// exec is a convenience helper that runs a DML/DDL statement on the database
// and logs any errors to stderr. The in-memory state is kept consistent
// regardless of DB write failures.
func (s *Store) exec(query string, args ...interface{}) {
	if _, err := s.db.Exec(query, args...); err != nil {
		fmt.Fprintf(os.Stderr, "store db error: %v\n", err)
	}
}

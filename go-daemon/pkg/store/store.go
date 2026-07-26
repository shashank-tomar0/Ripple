// Package store provides message persistence for the Ripple mesh.
//
// Phase 0 uses an in-memory store. Phase 1+ adds optional SQLite persistence
// for message history, contact lists, and offline message queues.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/message"
	_ "modernc.org/sqlite"
)

// Store provides thread-safe message and peer storage with optional SQLite
// persistence. When a database path is provided via NewWithDB, all messages
// and peers are also written to a SQLite file on disk, enabling data recovery
// across restarts.
type Store struct {
	mu       sync.RWMutex
	messages []*message.Message
	peers    map[string]string // peerID -> nickname
	db       *sql.DB           // nil when running without persistence
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
// It opens the database, creates the schema if needed, and loads any existing
// messages and peers into memory. Returns an error if the database cannot be
// opened or initialized — callers should fall back to New() on error.
func NewWithDB(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// Enable WAL mode for better concurrent read/write performance
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}

	s := &Store{
		messages: make([]*message.Message, 0, 128),
		peers:    make(map[string]string),
		db:       db,
	}

	if err := s.createTables(); err != nil {
		db.Close()
		return nil, err
	}

	if err := s.LoadFromDB(); err != nil {
		db.Close()
		return nil, err
	}

	return s, nil
}

// Close cleanly shuts down the database connection. Safe to call when the
// store has no database backing (it becomes a no-op). After Close, the store
// continues to serve in-memory data.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		err := s.db.Close()
		s.db = nil
		return err
	}
	return nil
}

// createTables ensures the SQLite schema exists for messages and peers.
// Called once during initialization in NewWithDB.
func (s *Store) createTables() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL,
			sender TEXT NOT NULL,
			sender_nick TEXT DEFAULT '',
			recipient TEXT DEFAULT '',
			payload TEXT NOT NULL,
			timestamp INTEGER NOT NULL,
			ttl INTEGER DEFAULT 16,
			hop_count INTEGER DEFAULT 0
		);
		CREATE TABLE IF NOT EXISTS peers (
			peer_id TEXT PRIMARY KEY,
			nickname TEXT NOT NULL,
			last_seen INTEGER NOT NULL
		);
	`)
	if err != nil {
		return fmt.Errorf("create tables: %w", err)
	}
	return nil
}

// LoadFromDB restores messages and peers from the SQLite database into memory.
// Messages are loaded in chronological order (oldest first); peers are loaded
// by peer_id. This is called automatically by NewWithDB on startup.
func (s *Store) LoadFromDB() error {
	rows, err := s.db.Query(
		`SELECT id, type, sender, sender_nick, recipient, payload, timestamp, ttl, hop_count
		 FROM messages ORDER BY timestamp ASC`,
	)
	if err != nil {
		return fmt.Errorf("query messages: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var msg message.Message
		var msgType string
		var recipient, senderNick sql.NullString
		if err := rows.Scan(
			&msg.ID, &msgType, &msg.Sender, &senderNick,
			&recipient, &msg.Payload, &msg.Timestamp,
			&msg.TTL, &msg.HopCount,
		); err != nil {
			return fmt.Errorf("scan message: %w", err)
		}
		msg.Type = message.MessageType(msgType)
		if senderNick.Valid {
			msg.SenderNick = senderNick.String
		}
		if recipient.Valid {
			msg.Recipient = recipient.String
		}
		s.messages = append(s.messages, &msg)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate messages: %w", err)
	}

	// Load peers
	peerRows, err := s.db.Query("SELECT peer_id, nickname FROM peers")
	if err != nil {
		return fmt.Errorf("query peers: %w", err)
	}
	defer peerRows.Close()

	for peerRows.Next() {
		var peerID, nickname string
		if err := peerRows.Scan(&peerID, &nickname); err != nil {
			return fmt.Errorf("scan peer: %w", err)
		}
		s.peers[peerID] = nickname
	}
	return peerRows.Err()
}

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

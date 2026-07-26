// Package store provides message persistence for the Ripple mesh.
//
// Phase 0 uses an in-memory store. This will be backed by SQLite in Phase 1
// for message history, contact lists, and offline message queues.
package store

import (
	"sync"

	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/message"
)

// Store provides thread-safe message and peer storage.
type Store struct {
	mu       sync.RWMutex
	messages []*message.Message
	peers    map[string]string // peerID -> nickname
}

// New creates a new empty Store.
func New() *Store {
	return &Store{
		messages: make([]*message.Message, 0, 128),
		peers:    make(map[string]string),
	}
}

// SaveMessage stores a received or sent message.
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

	// Phase 1: or filter for group or own messages
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

// AddPeer records a discovered peer.
func (s *Store) AddPeer(peerID, nickname string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.peers[peerID]; !exists {
		s.peers[peerID] = nickname
	}
}

// RemovePeer removes a peer from the store.
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

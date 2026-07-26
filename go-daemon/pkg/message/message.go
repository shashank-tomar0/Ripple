// Package message defines the message types exchanged over the Ripple mesh.
//
// Messages are serialized as JSON for simplicity in Phase 0. In production,
// this would be replaced by Protocol Buffers for efficiency and schema
// enforcement. Each message carries enough metadata for store-and-forward
// mesh delivery: sender, recipient, unique ID, timestamp, and TTL.
package message

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// MessageType indicates the kind of content in a message.
type MessageType string

const (
	TypeChat       MessageType = "chat"        // Simple text chat message
	TypeFile       MessageType = "file"        // File transfer (Phase 1+)
	TypeSOS        MessageType = "sos"         // Emergency broadcast
	TypeDeliveryAck MessageType = "delivery_ack" // Delivery confirmation
	TypePeerInfo   MessageType = "peer_info"   // Peer metadata exchange
)

// Message is the universal envelope for all Ripple mesh messages.
type Message struct {
	// ID is a unique message identifier (random hex).
	ID string `json:"id"`

	// Type categorizes the message content.
	Type MessageType `json:"type"`

	// Sender is the PeerID of the originating peer.
	Sender string `json:"sender"`

	// SenderNick is a human-readable nickname of the sender.
	SenderNick string `json:"sender_nick,omitempty"`

	// Recipient is the PeerID of the intended recipient.
	// Empty = broadcast to all peers in the rendezvous group.
	Recipient string `json:"recipient,omitempty"`

	// Payload contains the message body.
	Payload string `json:"payload"`

	// Timestamp is Unix nanoseconds when the message was created.
	Timestamp int64 `json:"ts"`

	// TTL is the remaining time-to-live in mesh hops.
	TTL int `json:"ttl"`

	// HopCount tracks how many relays this message has passed through.
	HopCount int `json:"hops"`
}

// NewChat creates a new chat message from sender to recipient.
// If recipient is empty, the message is a broadcast.
func NewChat(sender, senderNick, recipient, text string) *Message {
	return &Message{
		ID:         newID(),
		Type:       TypeChat,
		Sender:     sender,
		SenderNick: senderNick,
		Recipient:  recipient,
		Payload:    text,
		Timestamp:  time.Now().UnixNano(),
		TTL:        16,
		HopCount:   0,
	}
}

// NewDeliveryAck creates a delivery acknowledgment.
func NewDeliveryAck(sender, ackForID string) *Message {
	return &Message{
		ID:        newID(),
		Type:      TypeDeliveryAck,
		Sender:    sender,
		Payload:   ackForID,
		Timestamp: time.Now().UnixNano(),
		TTL:       4,
	}
}

// Serialize encodes the message as JSON bytes.
func (m *Message) Serialize() ([]byte, error) {
	return json.Marshal(m)
}

// DeserializeMessage decodes a JSON byte slice into a Message.
func DeserializeMessage(data []byte) (*Message, error) {
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("deserialize message: %w", err)
	}
	if msg.ID == "" {
		return nil, fmt.Errorf("message missing ID")
	}
	return &msg, nil
}

// newID generates a random hex string for message identification.
func newID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// IsExpired returns true if the message TTL has reached zero.
func (m *Message) IsExpired() bool {
	return m.TTL <= 0
}

// DecrementTTL reduces TTL by one hop.
func (m *Message) DecrementTTL() {
	if m.TTL > 0 {
		m.TTL--
	}
	m.HopCount++
}

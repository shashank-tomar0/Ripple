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
	TypeChat         MessageType = "chat"         // Simple text chat message
	TypeFile         MessageType = "file"         // File transfer (Phase 1+)
	TypeSOS          MessageType = "sos"          // Emergency broadcast
	TypeDeliveryAck  MessageType = "delivery_ack" // Delivery confirmation
	TypePeerInfo     MessageType = "peer_info"    // Peer metadata exchange
)

// DeliveryStatus represents the state of a message delivery.
type DeliveryStatus string

const (
	DeliverySent      DeliveryStatus = "sent"       // Message published to mesh
	DeliveryReceived  DeliveryStatus = "received"   // Recipient's node received it
	DeliveryDelivered DeliveryStatus = "delivered"  // Recipient's app displayed it
	DeliveryRead      DeliveryStatus = "read"       // Recipient opened/read it
	DeliveryFailed    DeliveryStatus = "failed"     // Could not deliver
)

// DeliveryInfo is the payload for delivery-related messages.
type DeliveryInfo struct {
	MessageID      string         `json:"msg_id"`
	Status         DeliveryStatus `json:"status"`
	RecipientPeer  string         `json:"recipient_peer"`
	OriginalSender string         `json:"original_sender"`
	Timestamp      int64          `json:"ts"`
	HopCount       int            `json:"hops"`
	Error          string         `json:"error,omitempty"`
}

// SOSUrgency represents the urgency level of an SOS alert.
type SOSUrgency string

const (
	SOSUrgencyLow      SOSUrgency = "low"
	SOSUrgencyMedium   SOSUrgency = "medium"
	SOSUrgencyHigh     SOSUrgency = "high"    // default
	SOSUrgencyCritical SOSUrgency = "critical"
)

// SOSPayload is the structured payload for SOS messages.
// This gets JSON-serialized into Message.Payload.
type SOSPayload struct {
	Urgency     SOSUrgency `json:"urgency"`
	Message     string     `json:"message"`
	Latitude    float64    `json:"lat,omitempty"`
	Longitude   float64    `json:"lon,omitempty"`
	Accuracy    float64    `json:"accuracy,omitempty"` // meters
	Timestamp   int64      `json:"ts"`
	AutoExpire  int        `json:"expire_minutes"` // minutes until auto-expire (default 60)
	AckRequired bool       `json:"ack_required"`  // true = sender needs delivery confirmation
}

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

	// Nonce is the NaCl encryption nonce (for E2E encrypted messages).
	// Empty when the message is not encrypted.
	Nonce string `json:"nonce,omitempty"`

	// KeyID identifies which public key was used to encrypt.
	// Format: first 8 hex chars of sender's Curve25519 public key.
	KeyID string `json:"key_id,omitempty"`
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

// NewDeliveryAck creates a delivery acknowledgment message.
// The payload carries a DeliveryInfo JSON structure.
func NewDeliveryAck(sender, ackForID, recipient string, status DeliveryStatus, hopCount int, errMsg string) *Message {
	info := DeliveryInfo{
		MessageID:      ackForID,
		Status:         status,
		RecipientPeer:  recipient,
		OriginalSender: sender,
		Timestamp:      time.Now().UnixNano(),
		HopCount:       hopCount,
		Error:          errMsg,
	}
	payloadBytes, _ := json.Marshal(info)
	return &Message{
		ID:         newID(),
		Type:       TypeDeliveryAck,
		Sender:     sender,
		Payload:    string(payloadBytes),
		Timestamp:  time.Now().UnixNano(),
		TTL:        4, // Delivery acks use shorter TTL
		HopCount:   0,
	}
}

// NewDeliveryStatus creates a message to notify the sender about delivery status.
// Used when recipient receives/displays/reads a message.
func NewDeliveryStatus(sender, recipient, originalMsgID string, status DeliveryStatus) *Message {
	return NewDeliveryAck(sender, originalMsgID, recipient, status, 0, "")
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

// FileMessagePayload is the payload for file transfer metadata messages.
// This gets JSON-serialized into Message.Payload.
type FileMessagePayload struct {
	FileID     string  `json:"file_id"`
	FileName   string  `json:"filename"`
	FileSize   int64   `json:"file_size"`
	MimeType   string  `json:"mime_type"`
	ChunkCount int     `json:"chunk_count"`
	ChunkIdx   int     `json:"chunk_idx,omitempty"`
	ChunkSize  int     `json:"chunk_size,omitempty"`
	Status     string  `json:"status,omitempty"` // started | progress | complete | failed
	Progress   float64 `json:"progress,omitempty"`
	OutputPath string  `json:"output_path,omitempty"`
}

// NewFileMetadata creates a file transfer metadata message.
func NewFileMetadata(sender, senderNick, recipient, fileName, mimeType string, fileSize int64, chunkCount int) *Message {
	payload := FileMessagePayload{
		FileID:     newID(),
		FileName:   fileName,
		FileSize:   fileSize,
		MimeType:   mimeType,
		ChunkCount: chunkCount,
		Status:     "started",
	}
	payloadBytes, _ := json.Marshal(payload)
	return &Message{
		ID:         newID(),
		Type:       TypeFile,
		Sender:     sender,
		SenderNick: senderNick,
		Recipient:  recipient,
		Payload:    string(payloadBytes),
		Timestamp:  time.Now().UnixNano(),
		TTL:        16,
		HopCount:   0,
	}
}

// NewFileProgress creates a file transfer progress notification message.
func NewFileProgress(sender, recipient, fileID string, chunkIdx, chunkCount int, progress float64) *Message {
	payload := FileMessagePayload{
		FileID:     fileID,
		ChunkIdx:   chunkIdx,
		ChunkCount: chunkCount,
		Status:     "progress",
		Progress:   progress,
	}
	payloadBytes, _ := json.Marshal(payload)
	return &Message{
		ID:        newID(),
		Type:      TypeFile,
		Sender:    sender,
		Recipient: recipient,
		Payload:   string(payloadBytes),
		Timestamp: time.Now().UnixNano(),
		TTL:       4,
		HopCount:  0,
	}
}

// NewFileComplete creates a file transfer completion notification message.
func NewFileComplete(sender, recipient, fileID, fileName, outputPath string) *Message {
	payload := FileMessagePayload{
		FileID:     fileID,
		FileName:   fileName,
		Status:     "complete",
		OutputPath: outputPath,
	}
	payloadBytes, _ := json.Marshal(payload)
	return &Message{
		ID:        newID(),
		Type:      TypeFile,
		Sender:    sender,
		Recipient: recipient,
		Payload:   string(payloadBytes),
		Timestamp: time.Now().UnixNano(),
		TTL:       4,
		HopCount:  0,
	}
}

// newID generates a random hex string for message identification.
func newID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// NewSOS creates a new SOS broadcast message with high TTL.
func NewSOS(sender, senderNick, message string, urgency SOSUrgency,
	lat, lon float64, accuracy float64) *Message {
	payload := SOSPayload{
		Urgency:     urgency,
		Message:     message,
		Latitude:    lat,
		Longitude:   lon,
		Accuracy:    accuracy,
		Timestamp:   time.Now().UnixMilli(),
		AutoExpire:  60, // default 60 minutes
		AckRequired: true,
	}
	payloadBytes, _ := json.Marshal(payload)
	return &Message{
		ID:         newID(),
		Type:       TypeSOS,
		Sender:     sender,
		SenderNick: senderNick,
		Recipient:  "", // broadcast
		Payload:    string(payloadBytes),
		Timestamp:  time.Now().UnixNano(),
		TTL:        64, // High TTL for SOS propagation
		HopCount:   0,
	}
}

// ParseSOSPayload extracts the SOSPayload from a message.
func ParseSOSPayload(msg *Message) (*SOSPayload, error) {
	if msg.Type != TypeSOS {
		return nil, fmt.Errorf("message is not an SOS type")
	}
	var payload SOSPayload
	if err := json.Unmarshal([]byte(msg.Payload), &payload); err != nil {
		return nil, fmt.Errorf("parse SOS payload: %w", err)
	}
	return &payload, nil
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

// IsEncrypted returns true if the message payload is E2E encrypted.
func (m *Message) IsEncrypted() bool {
	return m.Nonce != ""
}

package message

import (
	"testing"
	"encoding/json"
)

func TestNewChat(t *testing.T) {
	msg := NewChat("sender123", "Alice", "recip456", "Hello!")

	if msg.Type != TypeChat {
		t.Errorf("Type = %s, want %s", msg.Type, TypeChat)
	}
	if msg.Sender != "sender123" {
		t.Errorf("Sender = %s, want sender123", msg.Sender)
	}
	if msg.Payload != "Hello!" {
		t.Errorf("Payload = %s, want Hello!", msg.Payload)
	}
	if msg.TTL != 16 {
		t.Errorf("TTL = %d, want 16", msg.TTL)
	}
	if msg.ID == "" {
		t.Error("ID should not be empty")
	}
}

func TestSerializeDeserialize(t *testing.T) {
	original := NewChat("sender123", "Alice", "", "Hello mesh!")

	data, err := original.Serialize()
	if err != nil {
		t.Fatalf("Serialize() error = %v", err)
	}

	restored, err := DeserializeMessage(data)
	if err != nil {
		t.Fatalf("DeserializeMessage() error = %v", err)
	}

	if restored.ID != original.ID {
		t.Errorf("ID = %s, want %s", restored.ID, original.ID)
	}
	if restored.Payload != original.Payload {
		t.Errorf("Payload = %s, want %s", restored.Payload, original.Payload)
	}
	if restored.TTL != original.TTL {
		t.Errorf("TTL = %d, want %d", restored.TTL, original.TTL)
	}
}

func TestMissingID(t *testing.T) {
	data := []byte(`{"type":"chat","sender":"test","payload":"hello","ts":123}`)
	_, err := DeserializeMessage(data)
	if err == nil {
		t.Error("Expected error for missing ID")
	}
}

func TestSOSPayload(t *testing.T) {
	payload := SOSPayload{
		Urgency:   SOSUrgencyCritical,
		Message:   "Need help!",
		Latitude:  37.7749,
		Longitude: -122.4194,
		Accuracy:  10.0,
	}

	data, _ := json.Marshal(payload)

	var restored SOSPayload
	json.Unmarshal(data, &restored)

	if restored.Urgency != SOSUrgencyCritical {
		t.Errorf("Urgency = %s, want %s", restored.Urgency, SOSUrgencyCritical)
	}
	if restored.Latitude != 37.7749 {
		t.Errorf("Latitude = %f, want 37.7749", restored.Latitude)
	}
}

func TestDeliveryInfo(t *testing.T) {
	info := DeliveryInfo{
		MessageID:      "msg123",
		Status:         DeliveryDelivered,
		OriginalSender: "sender123",
		Timestamp:      1700000000,
	}

	data, _ := json.Marshal(info)

	var restored DeliveryInfo
	json.Unmarshal(data, &restored)

	if restored.Status != DeliveryDelivered {
		t.Errorf("Status = %s, want %s", restored.Status, DeliveryDelivered)
	}
}

func TestKeyExchange(t *testing.T) {
	msg := NewKeyExchange("sender123", "Alice", "abcdef0123456789")

	if msg.Type != TypeKeyExchange {
		t.Errorf("Type = %s, want %s", msg.Type, TypeKeyExchange)
	}
	if msg.TTL != 8 {
		t.Errorf("TTL = %d, want 8 for key exchange", msg.TTL)
	}

	var payload KeyExchangePayload
	if err := json.Unmarshal([]byte(msg.Payload), &payload); err != nil {
		t.Fatalf("Unmarshal payload error = %v", err)
	}

	if payload.PublicKeyHex != "abcdef0123456789" {
		t.Errorf("PublicKeyHex = %s, want abcdef0123456789", payload.PublicKeyHex)
	}
	if payload.Nickname != "Alice" {
		t.Errorf("Nickname = %s, want Alice", payload.Nickname)
	}
}

func TestKeyExchangePayloadJSON(t *testing.T) {
	payload := KeyExchangePayload{
		PublicKeyHex: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
		Nickname:     "TestUser",
		Timestamp:    1234567890,
	}

	data, _ := json.Marshal(payload)

	var restored KeyExchangePayload
	json.Unmarshal(data, &restored)

	if restored.PublicKeyHex != payload.PublicKeyHex {
		t.Errorf("PublicKeyHex mismatch")
	}
	if restored.Nickname != payload.Nickname {
		t.Errorf("Nickname mismatch")
	}
	if restored.Timestamp != payload.Timestamp {
		t.Errorf("Timestamp mismatch")
	}
}

func TestFileMessagePayload(t *testing.T) {
	payload := FileMessagePayload{
		FileID:     "file123",
		FileName:   "test.txt",
		FileSize:   1024,
		MimeType:   "text/plain",
		ChunkCount: 1,
		Status:     "started",
	}

	data, _ := json.Marshal(payload)

	var restored FileMessagePayload
	json.Unmarshal(data, &restored)

	if restored.FileName != "test.txt" {
		t.Errorf("FileName = %s, want test.txt", restored.FileName)
	}
	if restored.FileSize != 1024 {
		t.Errorf("FileSize = %d, want 1024", restored.FileSize)
	}
}
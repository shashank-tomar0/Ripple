package codec

import (
	"testing"

	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/message"
)

func TestRoundtripChat(t *testing.T) {
	original := message.NewChat("peer1", "Alice", "peer2", "Hello! How are you?")
	testRoundtrip(t, original)
}

func TestRoundtripBroadcast(t *testing.T) {
	original := message.NewChat("peer1", "Alice", "", "Hello everyone in the mesh!")
	testRoundtrip(t, original)
}

func TestRoundtripSOS(t *testing.T) {
	original := message.NewSOS("peer1", "Alice", "Need help at north ridge!",
		message.SOSUrgencyCritical, 37.7749, -122.4194, 10.0)
	testRoundtrip(t, original)
}

func TestRoundtripDeliveryAck(t *testing.T) {
	original := message.NewDeliveryAck("peer2", "msg123", "peer1", message.DeliveryDelivered, 3, "")
	testRoundtrip(t, original)
}

func TestRoundtripFileMetadata(t *testing.T) {
	original := message.NewFileMetadata("peer1", "Alice", "peer2", "photo.jpg", "image/jpeg", 2048576, 32)
	testRoundtrip(t, original)
}

func TestRoundtripFileProgress(t *testing.T) {
	original := message.NewFileProgress("peer1", "peer2", "file123", 14, 32, 0.4375)
	testRoundtrip(t, original)
}

func TestRoundtripFileComplete(t *testing.T) {
	original := message.NewFileComplete("peer1", "peer2", "file123", "photo.jpg", "/data/files/photo.jpg")
	testRoundtrip(t, original)
}

func TestRoundtripKeyExchange(t *testing.T) {
	original := message.NewKeyExchange("peer1", "Alice", "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789")
	testRoundtrip(t, original)
}

func TestRoundtripEncrypted(t *testing.T) {
	original := message.NewChat("peer1", "Alice", "peer2", "this is secret")
	original.Nonce = "abcd1234efgh5678"
	original.KeyID = "a1b2c3d4"
	testRoundtrip(t, original)
}

func TestRoundtripAllFields(t *testing.T) {
	// Message with every optional field filled
	original := &message.Message{
		ID:         "abcdef1234567890abcdef1234567890",
		Type:       message.TypeChat,
		Sender:     "12D3KooW9abcdefghij1234567890abcdefghij1234567890abcdefghij",
		SenderNick: "AliceTheExplorer",
		Recipient:  "12D3KooW8zyxwvutsrq1234567890zyxwvutsrq1234567890zyxwvutsrq",
		Payload:    "This is a longer message that should still be encoded efficiently by the binary codec.",
		Timestamp:  1700000000000000000,
		TTL:        64,
		HopCount:   7,
		Nonce:      "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1",
		KeyID:      "deadbeef",
	}
	testRoundtrip(t, original)
}

func TestEmptyMessage(t *testing.T) {
	// Marshal should succeed but produce data that fails to unmarshal
	msg := &message.Message{ID: ""}
	data, err := Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	// Unmarshal should fail due to missing ID
	_, err = Unmarshal(data)
	if err == nil {
		t.Error("expected error for empty message (no ID) on unmarshal")
	}
}

func TestIsBinaryFormat(t *testing.T) {
	if IsBinaryFormat([]byte{0x00}) != true {
		t.Error("binary data should be detected as binary")
	}
	if IsBinaryFormat([]byte{'{'}) != false {
		t.Error("JSON data should not be detected as binary")
	}
	if IsBinaryFormat(nil) != false {
		t.Error("nil should not be binary")
	}
}

func TestMTUFriendlySplit(t *testing.T) {
	data := make([]byte, 100)
	frames := MTUFriendlySplit(data)
	if len(frames) != 5 {
		t.Errorf("expected 5 frames (20 bytes each), got %d", len(frames))
	}
	for i, f := range frames {
		if len(f) > 20 {
			t.Errorf("frame %d exceeds MTU: %d bytes", i, len(f))
		}
	}

	// Small data
	small := MTUFriendlySplit([]byte("hello"))
	if len(small) != 1 {
		t.Errorf("expected 1 frame for small data, got %d", len(small))
	}
}

func TestSizeReduction(t *testing.T) {
	// Verify binary is significantly smaller than JSON for a typical message
	msg := message.NewChat("12D3KooW9abcdefghij1234567890abcdefghij", "Alice", "12D3KooW8zyxwvutsrq1234567890zyxwvutsrq", "Hello!")
	ratio := SizeRatio(msg)
	if ratio < 1.4 {
		t.Errorf("binary should be at least 40%% smaller than JSON estimate, got ratio %.2f", ratio)
	}
}

func TestShortTypeName(t *testing.T) {
	data, _ := Marshal(message.NewChat("a", "b", "", "hi"))
	if name := ShortTypeName(data); name != "chat" {
		t.Errorf("expected 'chat', got '%s'", name)
	}

	data2, _ := Marshal(message.NewSOS("a", "b", "help", message.SOSUrgencyCritical, 0, 0, 0))
	if name := ShortTypeName(data2); name != "sos" {
		t.Errorf("expected 'sos', got '%s'", name)
	}
}

// Benchmarks

func BenchmarkMarshalChat(b *testing.B) {
	msg := message.NewChat("12D3KooW9abcdefghij1234567890abcdefghij", "Alice", "12D3KooW8zyxwvutsrq1234567890zyxwvutsrq", "Hello, this is a test message!")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Marshal(msg)
	}
}

func BenchmarkUnmarshalChat(b *testing.B) {
	msg := message.NewChat("12D3KooW9abcdefghij1234567890abcdefghij", "Alice", "12D3KooW8zyxwvutsrq1234567890zyxwvutsrq", "Hello, this is a test message!")
	data, _ := Marshal(msg)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Unmarshal(data)
	}
}

func BenchmarkJSONMarshal(b *testing.B) {
	msg := message.NewChat("12D3KooW9abcdefghij1234567890abcdefghij", "Alice", "12D3KooW8zyxwvutsrq1234567890zyxwvutsrq", "Hello, this is a test message!")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		msg.Serialize()
	}
}

// Helpers

func testRoundtrip(t *testing.T, original *message.Message) {
	t.Helper()

	data, err := Marshal(original)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	decoded, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal error: %v (data len=%d, type=%s)", err, len(data), original.Type)
	}

	// Compare fields
	if decoded.ID != original.ID {
		t.Errorf("ID: %s != %s", decoded.ID, original.ID)
	}
	if decoded.Type != original.Type {
		t.Errorf("Type: %s != %s", decoded.Type, original.Type)
	}
	if decoded.Sender != original.Sender {
		t.Errorf("Sender: %s != %s", decoded.Sender, original.Sender)
	}
	if decoded.SenderNick != original.SenderNick {
		t.Errorf("SenderNick: %s != %s", decoded.SenderNick, original.SenderNick)
	}
	if decoded.Recipient != original.Recipient {
		t.Errorf("Recipient: %s != %s", decoded.Recipient, original.Recipient)
	}
	if decoded.Payload != original.Payload {
		t.Errorf("Payload: %s != %s", decoded.Payload, original.Payload)
	}
	if decoded.Timestamp != original.Timestamp {
		t.Errorf("Timestamp: %d != %d", decoded.Timestamp, original.Timestamp)
	}
	if decoded.TTL != original.TTL {
		t.Errorf("TTL: %d != %d", decoded.TTL, original.TTL)
	}
	if decoded.HopCount != original.HopCount {
		t.Errorf("HopCount: %d != %d", decoded.HopCount, original.HopCount)
	}
	if decoded.Nonce != original.Nonce {
		t.Errorf("Nonce: %s != %s", decoded.Nonce, original.Nonce)
	}
	if decoded.KeyID != original.KeyID {
		t.Errorf("KeyID: %s != %s", decoded.KeyID, original.KeyID)
	}

	// Size reporting
	t.Logf("  %s: %d bytes binary (vs ~%d bytes JSON)", original.Type, len(data), len(data)*3)
}

func BenchmarkRoundtrip(b *testing.B) {
	original := message.NewChat("12D3KooW9abcdefghij1234567890abcdefghij", "Alice", "12D3KooW8zyxwvutsrq1234567890zyxwvutsrq", "Hello, this is a test message that is reasonably long!")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		data, _ := Marshal(original)
		Unmarshal(data)
	}
}

package store

import (
	"testing"
	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/message"
)

func TestNew(t *testing.T) {
	s := New()
	if s == nil {
		t.Fatal("New() returned nil")
	}
	msgs := s.Messages()
	if len(msgs) != 0 {
		t.Errorf("Messages() = %d, want 0", len(msgs))
	}
}

func TestSaveAndRetrieve(t *testing.T) {
	s := New()
	msg := message.NewChat("sender", "Alice", "recip", "hello")

	s.SaveMessage(msg)

	msgs := s.Messages()
	if len(msgs) != 1 {
		t.Fatalf("Messages() = %d, want 1", len(msgs))
	}
	if msgs[0].Payload != "hello" {
		t.Errorf("Payload = %s, want hello", msgs[0].Payload)
	}
}

func TestDedup(t *testing.T) {
	s := New()
	msg := message.NewChat("sender", "Alice", "", "hello")

	s.SaveMessage(msg)
	s.SaveMessage(msg) // duplicate save

	// Store saves all messages, HasMessage checks dedup
	if !s.HasMessage(msg.ID) {
		t.Error("HasMessage() should return true for saved message")
	}
}

func TestMessagesForContact(t *testing.T) {
	s := New()

	msg1 := message.NewChat("alice", "Alice", "bob", "hi bob")
	msg2 := message.NewChat("bob", "Bob", "alice", "hi alice")
	msg3 := message.NewChat("carol", "Carol", "", "hello everyone")

	s.SaveMessage(msg1)
	s.SaveMessage(msg2)
	s.SaveMessage(msg3)

	conv := s.MessagesForContact("bob")
	// Only msg1 has bob as recipient (msg2 is from bob, msg3 is broadcast)
	if len(conv) != 1 {
		t.Errorf("MessagesForContact(bob) = %d, want 1", len(conv))
	}
}

func TestRecentMessages(t *testing.T) {
	s := New()
	for i := 0; i < 10; i++ {
		msg := message.NewChat("sender", "Alice", "", "msg")
		s.SaveMessage(msg)
	}

	recent := s.RecentMessages(3)
	if len(recent) != 3 {
		t.Errorf("RecentMessages(3) = %d, want 3", len(recent))
	}
}

func TestPeers(t *testing.T) {
	s := New()
	s.AddPeer("peer1", "Alice")
	s.AddPeer("peer2", "Bob")
	s.AddPeer("peer1", "Alice Dup") // should not add duplicate

	peers := s.Peers()
	if len(peers) != 2 {
		t.Errorf("Peers() = %d, want 2", len(peers))
	}

	s.RemovePeer("peer2")
	if len(s.Peers()) != 1 {
		t.Errorf("After remove, Peers() = %d, want 1", len(s.Peers()))
	}
}

func TestAddPeerWithDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/test.db"

	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB() error = %v", err)
	}
	defer s.Close()

	s.AddPeer("peer1", "Alice")
	peers := s.Peers()
	if len(peers) != 1 {
		t.Errorf("Peers() = %d, want 1", len(peers))
	}
	if peers["peer1"] != "Alice" {
		t.Errorf("peer nickname = %s, want Alice", peers["peer1"])
	}
}

func TestSaveMessageWithDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/test.db"

	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB() error = %v", err)
	}
	defer s.Close()

	msg := message.NewChat("sender", "Alice", "recip", "hello")
	s.SaveMessage(msg)

	msgs := s.Messages()
	if len(msgs) != 1 {
		t.Errorf("Messages() = %d, want 1", len(msgs))
	}
	if msgs[0].Payload != "hello" {
		t.Errorf("Payload = %s, want hello", msgs[0].Payload)
	}
}

func TestMessagesForContactWithDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/test.db"

	s, err := NewWithDB(dbPath)
	if err != nil {
		t.Fatalf("NewWithDB() error = %v", err)
	}
	defer s.Close()

	msg1 := message.NewChat("alice", "Alice", "bob", "hi bob")
	msg2 := message.NewChat("bob", "Bob", "alice", "hi alice")

	s.SaveMessage(msg1)
	s.SaveMessage(msg2)

	conv := s.MessagesForContact("bob")
	if len(conv) != 1 {
		t.Errorf("MessagesForContact(bob) = %d, want 1", len(conv))
	}
}
package mesh

import (
	"context"
	"crypto/rand"
	"sync"
	"testing"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/message"
)

// TestRelayEvents verifies that every message passing through a node emits
// an accurate, ordered relay event: a fresh message produces `received`
// then `forwarded`, a duplicate produces `dropped`, and a message arriving
// with an exhausted TTL produces `received` then `dropped`.
//
// This is the contract the live topology visualization renders — if these
// events are wrong, the on-screen animation would be showing fiction.
func TestRelayEvents(t *testing.T) {
	priv, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	n, err := NewNode(context.Background(), priv, 0, WithMDNS(false))
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	defer n.Close()

	var mu sync.Mutex
	var events []RelayEvent
	n.OnRelay = func(evt RelayEvent) {
		mu.Lock()
		events = append(events, evt)
		mu.Unlock()
	}

	// A fresh message: must arrive (`received`) and be relayed (`forwarded`).
	msg := message.NewChat("peerA", "alice", "", "hello mesh")
	n.deliverMessage(msg, "peerA")

	// Same message again: dedup must drop it.
	n.deliverMessage(msg, "peerA")

	// A message whose TTL is already exhausted: `received`, then `dropped`.
	expired := message.NewChat("peerB", "bob", "", "too far")
	expired.TTL = 0
	n.deliverMessage(expired, "peerB")

	mu.Lock()
	defer mu.Unlock()

	// Fresh message: received → forwarded → (second copy dropped as dup).
	wantFresh := []RelayAction{RelayReceived, RelayForwarded, RelayDropped}
	var freshSeq, expiredSeq []RelayAction
	var forwarded *RelayEvent
	for i := range events {
		switch events[i].MessageID {
		case msg.ID:
			freshSeq = append(freshSeq, events[i].Action)
			if events[i].Action == RelayForwarded {
				forwarded = &events[i]
			}
		case expired.ID:
			expiredSeq = append(expiredSeq, events[i].Action)
		}
	}
	assertActions(t, "fresh message", freshSeq, wantFresh)
	assertActions(t, "TTL-expired message", expiredSeq, []RelayAction{RelayReceived, RelayDropped})

	// The forwarded event must reflect the post-decrement state.
	if forwarded == nil {
		t.Fatal("no forwarded event for fresh message")
	}
	if forwarded.Hops != 1 || forwarded.TTL != msg.TTL || forwarded.From != "peerA" {
		t.Fatalf("forwarded event state wrong: %+v", forwarded)
	}

	// Timestamps and message types must be populated — the map animation and
	// audit trail depend on them.
	for _, e := range events {
		if e.Timestamp == 0 {
			t.Fatalf("event missing timestamp: %+v", e)
		}
		if e.Type == "" {
			t.Fatalf("event missing message type: %+v", e)
		}
	}
}

func assertActions(t *testing.T, label string, got, want []RelayAction) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s events = %v, want %v", label, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s event %d = %s, want %s (full: %v)", label, i, got[i], want[i], got)
		}
	}
}

// TestRelayPublishNilTopic guards the relay path against a node that has not
// been Start()ed: forwarding must not panic, and the message must still be
// delivered to the application layer.
func TestRelayPublishNilTopic(t *testing.T) {
	priv, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	n, err := NewNode(context.Background(), priv, 0, WithMDNS(false))
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	defer n.Close()

	delivered := make(chan *message.Message, 1)
	n.OnMessage = func(m *message.Message) {
		delivered <- m
	}

	msg := message.NewChat("peerA", "alice", "", "hello")
	n.deliverMessage(msg, "peerA") // must not panic despite nil Topic

	select {
	case got := <-delivered:
		if got.ID != msg.ID {
			t.Fatalf("delivered wrong message: %s", got.ID)
		}
	default:
		t.Fatal("message was not delivered to the application layer")
	}
}

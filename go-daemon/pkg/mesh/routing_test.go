package mesh

import (
	"context"
	"crypto/rand"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/message"
)

// newTestNode builds a mesh node with a spray budget and no mDNS
// (deterministic topology via explicit dials only).
func newTestNode(t *testing.T, budget int) *Node {
	t.Helper()
	priv, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	n, err := NewNode(context.Background(), priv, 0, WithMDNS(false), WithSprayBudget(budget))
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	t.Cleanup(n.Close)
	return n
}

// dial makes dst connect to src and waits until BOTH sides know each other
// at the connection level (routing candidates are connection-driven).
func dial(t *testing.T, src, dst *Node) {
	t.Helper()
	if err := dst.ConnectToPeer(src.MultiaddrString()); err != nil {
		t.Fatalf("dial %s → %s: %v", dst.PeerID.ShortString(), src.PeerID.ShortString(), err)
	}
	waitFor(t, fmt.Sprintf("%s to learn %s", src.PeerID.ShortString(), dst.PeerID.ShortString()),
		func() bool { return containsPeer(src.KnownPeers(), dst.PeerID) })
	waitFor(t, fmt.Sprintf("%s to learn %s", dst.PeerID.ShortString(), src.PeerID.ShortString()),
		func() bool { return containsPeer(dst.KnownPeers(), src.PeerID) })
}

func containsPeer(peers []peer.ID, want peer.ID) bool {
	for _, p := range peers {
		if p == want {
			return true
		}
	}
	return false
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

// collectMessages returns a channel fed by OnMessage and a registry of what
// arrived (guarded by its own mutex).
type msgRegistry struct {
	mu   sync.Mutex
	msgs map[string]*message.Message
	ch   chan *message.Message
}

func newMsgRegistry() *msgRegistry {
	return &msgRegistry{msgs: make(map[string]*message.Message), ch: make(chan *message.Message, 64)}
}

func (r *msgRegistry) onMessage(m *message.Message) {
	r.mu.Lock()
	r.msgs[m.ID] = m
	r.mu.Unlock()
	r.ch <- m
}

func (r *msgRegistry) get(id string) (*message.Message, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.msgs[id]
	return m, ok
}

// TestSprayChainBudgetBoundsCopies is the live-wire version of the
// simulator's headline claim: a DM through a relay chain delivers with a
// BOUNDED number of copies, not one per node.
//
// Topology: n1 ↔ n2 ↔ n3 (n1 and n3 never connect directly), budget L=2.
// n1's only candidate is n2 → spray (copies 1→2). n2's only candidate is
// n3, the destination → deliver (a move, no new copy). The message must
// arrive at n3 with CopiesMade == 2 and no more than 2 physical copies may
// exist anywhere.
func TestSprayChainBudgetBoundsCopies(t *testing.T) {
	n1 := newTestNode(t, 2)
	n2 := newTestNode(t, 2)
	n3 := newTestNode(t, 2)

	reg3 := newMsgRegistry()
	n3.OnMessage = reg3.onMessage
	reg1 := newMsgRegistry()
	n1.OnMessage = reg1.onMessage

	// Relay event capture on the middle node.
	var mu sync.Mutex
	var n2Events []RelayEvent
	n2.OnRelay = func(evt RelayEvent) {
		mu.Lock()
		n2Events = append(n2Events, evt)
		mu.Unlock()
	}

	dial(t, n1, n2)
	dial(t, n2, n3)

	dm := message.NewChat(n1.PeerID.String(), "node1", n3.PeerID.String(), "chain delivery")
	if err := n1.SendMessage(dm); err != nil {
		t.Fatalf("send DM: %v", err)
	}

	// The destination must receive it.
	waitFor(t, "n3 to receive the DM", func() bool {
		_, ok := reg3.get(dm.ID)
		return ok
	})
	got, _ := reg3.get(dm.ID)
	if got.CopiesMade != 2 {
		t.Fatalf("delivered copy has CopiesMade=%d, want 2 (budget L=2)", got.CopiesMade)
	}

	// The middle node must have seen exactly received → forwarded.
	mu.Lock()
	var acts []RelayAction
	for _, e := range n2Events {
		if e.MessageID == dm.ID {
			acts = append(acts, e.Action)
		}
	}
	mu.Unlock()
	if len(acts) != 2 || acts[0] != RelayReceived || acts[1] != RelayForwarded {
		t.Fatalf("n2 relay events for DM = %v, want [received forwarded]", acts)
	}

	// n1 must never re-receive its own message (no ping-pong back-send).
	select {
	case m := <-reg1.ch:
		// The auto-ack from n3 is expected; our own DM coming back is not.
		if m.ID == dm.ID {
			t.Fatal("sender received its own message back — ping-pong back-send")
		}
	case <-time.After(1 * time.Second):
		// ack may take a moment; nothing to assert here
	}
}

// TestSprayStarSkipsUnneededPeers is the copy-economy assertion: with
// budget L=1 and the destination directly connected, a spur node that
// epidemic flooding WOULD have touched must never receive a copy.
func TestSprayStarSkipsUnneededPeers(t *testing.T) {
	n1 := newTestNode(t, 1)
	n2 := newTestNode(t, 1) // the destination
	n3 := newTestNode(t, 1) // the spur — must stay untouched

	reg2 := newMsgRegistry()
	n2.OnMessage = reg2.onMessage
	reg3 := newMsgRegistry()
	n3.OnMessage = reg3.onMessage

	dial(t, n1, n2)
	dial(t, n1, n3)

	dm := message.NewChat(n1.PeerID.String(), "node1", n2.PeerID.String(), "star delivery")
	if err := n1.SendMessage(dm); err != nil {
		t.Fatalf("send DM: %v", err)
	}

	waitFor(t, "n2 to receive the DM", func() bool {
		_, ok := reg2.get(dm.ID)
		return ok
	})
	got, _ := reg2.get(dm.ID)
	if got.CopiesMade != 1 {
		t.Fatalf("delivered copy has CopiesMade=%d, want 1 (budget L=1)", got.CopiesMade)
	}

	// The spur must NEVER see the message (flooding would have delivered it).
	select {
	case m := <-reg3.ch:
		t.Fatalf("spur node received a copy of %s — spray budget was not enforced", m.ID)
	case <-time.After(1500 * time.Millisecond):
		// good: nothing arrived
	}
}

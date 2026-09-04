// Package mesh implements the Ripple peer-to-peer mesh networking layer.
//
// Built on libp2p, it provides:
//   - TCP transport with automatic NAT traversal
//   - mDNS peer discovery on local networks
//   - GossipSub pubsub for message broadcasting
//   - Direct peer-to-peer streams for 1:1 messaging
//   - Store-and-forward message relay
//
// Every peer in the mesh acts as both client and relay, forwarding messages
// to peers it knows about. This creates a decentralized network where
// messages can hop through intermediate devices to reach their destination.
package mesh

import (
	"context"
	"fmt"
	"io"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	"github.com/libp2p/go-libp2p/p2p/net/connmgr"
	"github.com/multiformats/go-multiaddr"

	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/message"
	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/routing"
)

const (
	// ProtocolID for direct peer-to-peer Ripple streams.
	ProtocolID = protocol.ID("/ripple/chat/1.0.0")

	// PubSubTopic is the GossipSub topic for mesh broadcast.
	PubSubTopic = "ripple-mesh-v1"

	// ServiceName for mDNS peer discovery.
	ServiceName = "ripple-mesh"

	// BufferSize for stream I/O.
	BufferSize = 4096

	// DefaultMessageTimeout for sending a message.
	DefaultMessageTimeout = 30 * time.Second
)

// Node represents a single peer in the Ripple mesh network.
type Node struct {
	ctx    context.Context
	cancel context.CancelFunc

	Host   host.Host
	PubSub *pubsub.PubSub
	Topic  *pubsub.Topic
	Sub    *pubsub.Subscription

	// Identity
	PrivKey crypto.PrivKey
	PeerID  peer.ID
	Nick    string

	// Peers we've discovered or connected to
	peerLock sync.RWMutex
	peers    map[peer.ID]bool

	// Message callbacks
	OnMessage   func(*message.Message)
	OnPeerJoin  func(peer.ID)
	OnPeerLeave func(peer.ID)

	// OnRelay is invoked for every message that passes through this node,
	// whether it was received, forwarded, or dropped. It feeds the live
	// relay visualization and the delivery audit trail.
	OnRelay func(RelayEvent)

	// Forwarder is the destination-aware forwarding decision layer. The
	// default (nil) is epidemic flooding; WithSprayBudget installs the
	// PROPHET-guided spray-and-wait forwarder. The exact same decision
	// code drives the deterministic simulator in pkg/routing/sim.
	Forwarder routing.Forwarder

	// MDNSDisabled suppresses mDNS peer discovery when true (must be set
	// via WithMDNS before Start). Peers then only appear via explicit
	// ConnectToPeer dials — needed for deterministic test topologies.
	MDNSDisabled bool

	// Message relay
	seenLock sync.RWMutex
	seen     map[string]bool // dedup message IDs

	// RouteState guards destination-aware routing state: per-peer
	// predictability (encounters) and the per-message ledger of peers we
	// have handed copies to.
	routeLock  sync.Mutex
	encounters map[string]routing.EncounterStat
	sprayed    map[string]map[string]bool // msgID → peers handed a copy

	// SprayBudget is the destination-aware routing copy budget L: at most L
	// physical copies of an addressed message exist at any time. 0 disables
	// routing — the node floods (epidemic re-broadcast), the historical
	// behavior. Broadcasts (no recipient) always flood regardless.
	SprayBudget int

	// Logging
	Debug bool
	Log   *log.Logger
}

// RelayAction describes what happened to a message at this node.
type RelayAction string

const (
	// RelayReceived: the message arrived at this node.
	RelayReceived RelayAction = "received"
	// RelayForwarded: this node relayed the message onward.
	RelayForwarded RelayAction = "forwarded"
	// RelayDropped: the message died here (TTL expired or duplicate).
	RelayDropped RelayAction = "dropped"
	// RelaySent: a locally-originated message was handed to the mesh.
	RelaySent RelayAction = "sent"
)

// RelayEvent describes a message's passage through this node. Every field
// is factual: the message ID, the action taken, the peer it came from
// (empty for locally sent messages), and the message's hop/TTL/copy state
// at the moment of the event.
type RelayEvent struct {
	MessageID string
	Type      message.MessageType
	Action    RelayAction
	From      string // peer this message arrived from; "" for local sends
	Hops      int
	TTL       int
	Copies    int   // physical copies of the message that exist, as known here
	Timestamp int64 // Unix nanoseconds
}

// Option configures a Node.
type Option func(*Node)

// WithDebug enables debug logging.
func WithDebug(enabled bool) Option {
	return func(n *Node) {
		n.Debug = enabled
	}
}

// WithNickname sets the display nickname.
func WithNickname(nick string) Option {
	return func(n *Node) {
		n.Nick = nick
	}
}

// WithMDNS controls mDNS peer discovery. Disable it for deterministic
// topologies where peers must only arrive via explicit bootstrap dials.
func WithMDNS(enabled bool) Option {
	return func(n *Node) {
		n.MDNSDisabled = !enabled
	}
}

// WithSprayBudget enables destination-aware routing for addressed messages
// with copy budget L (the original copy counts as one). L <= 0 keeps the
// epidemic flood. Broadcasts (no recipient) always flood.
func WithSprayBudget(L int) Option {
	return func(n *Node) {
		if L <= 0 {
			n.SprayBudget = 0
			n.Forwarder = nil
			return
		}
		n.SprayBudget = L
		n.Forwarder = routing.SprayAndWait{L: L}
	}
}

// NewNode creates a new Ripple mesh node.
func NewNode(ctx context.Context, privKey crypto.PrivKey, listenPort int, opts ...Option) (*Node, error) {
	ctx, cancel := context.WithCancel(ctx)

	pub := privKey.GetPublic()
	pid, err := peer.IDFromPublicKey(pub)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("derive peer ID: %w", err)
	}

	n := &Node{
		ctx:        ctx,
		cancel:     cancel,
		PrivKey:    privKey,
		PeerID:     pid,
		peers:      make(map[peer.ID]bool),
		seen:       make(map[string]bool),
		encounters: make(map[string]routing.EncounterStat),
		sprayed:    make(map[string]map[string]bool),
		Log:        log.Default(),
	}

	for _, opt := range opts {
		opt(n)
	}

	// Build multiaddress
	sourceMultiAddr := fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", listenPort)

	// Connection manager: keep up to 100 connections, trim at 80
	cm, err := connmgr.NewConnManager(80, 100, connmgr.WithGracePeriod(time.Minute))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("connection manager: %w", err)
	}

	// Create libp2p host
	h, err := libp2p.New(
		libp2p.ListenAddrStrings(sourceMultiAddr),
		libp2p.Identity(privKey),
		libp2p.ConnectionManager(cm),
		libp2p.NATPortMap(),
		libp2p.EnableNATService(),
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create libp2p host: %w", err)
	}
	n.Host = h

	// Set stream handler for direct peer-to-peer chat
	h.SetStreamHandler(ProtocolID, n.handleStream)

	// Track peers at the CONNECTION level, not just when a stream or mDNS
	// event happens: any established connection is a peer that destination-
	// aware routing may hand a copy to.
	h.Network().Notify(&network.NotifyBundle{
		ConnectedF: func(net network.Network, conn network.Conn) {
			// Record the address we actually connected to: without this, a
			// peer that DIALS us is reachable for the connection it made but
			// not dialable back ("no addresses") until Identify runs — a
			// fatal gap for direct-stream routing, invisible under pubsub.
			net.Peerstore().AddAddr(conn.RemotePeer(), conn.RemoteMultiaddr(), peerstore.PermanentAddrTTL)
			n.addPeer(conn.RemotePeer())
		},
		DisconnectedF: func(net network.Network, conn network.Conn) {
			n.removePeer(conn.RemotePeer())
		},
	})

	if n.Debug {
		n.Log.Printf("🔗 libp2p host: %s", h.ID())
		for _, addr := range h.Addrs() {
			n.Log.Printf("   listen: %s/p2p/%s", addr, h.ID())
		}
	}

	return n, nil
}

// Start initializes pubsub and discovery, then blocks until context is cancelled.
func (n *Node) Start() error {
	// Initialize pubsub
	ps, err := pubsub.NewGossipSub(n.ctx, n.Host,
		pubsub.WithMessageSigning(true),
		pubsub.WithStrictSignatureVerification(false), // Allow unsigned for demo; enable in production
	)
	if err != nil {
		return fmt.Errorf("create pubsub: %w", err)
	}
	n.PubSub = ps

	// Join topic
	topic, err := ps.Join(PubSubTopic)
	if err != nil {
		return fmt.Errorf("join topic: %w", err)
	}
	n.Topic = topic

	// Subscribe
	sub, err := topic.Subscribe()
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	n.Sub = sub

	// Start pubsub message handler
	go n.handlePubSub(sub)

	// Start mDNS discovery (unless explicitly disabled for deterministic
	// topologies such as relay-chain tests)
	if !n.MDNSDisabled {
		n.startMDNS()
	}

	if n.Debug {
		n.Log.Printf("📡 PubSub topic: %s", PubSubTopic)
	}

	// Start periodic dedup cache cleanup
	n.StartSeenCleanup(n.ctx)

	return nil
}

// StartSeenCleanup periodically prunes old message IDs from the dedup map.
// Prevents unbounded memory growth. Runs until ctx is cancelled.
func (n *Node) StartSeenCleanup(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				n.seenLock.Lock()
				// Reset the map periodically — old entries expire naturally
				// since message IDs are random and collisions are astronomically unlikely
				n.seen = make(map[string]bool)
				n.seenLock.Unlock()
				n.routeLock.Lock()
				// The sprayed-copy ledger has the same lifetime as dedup: a
				// message older than the dedup window is dead to this node.
				n.sprayed = make(map[string]map[string]bool)
				n.routeLock.Unlock()
				if n.Debug {
					n.Log.Printf("pruned dedup cache")
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Close shuts down the node.
func (n *Node) Close() {
	n.cancel()
	if err := n.Host.Close(); err != nil {
		n.Log.Printf("error closing host: %v", err)
	}
}

// SendMessage sends a message through the mesh.
// If the recipient is set and known, it sends via direct stream first.
// Otherwise, it broadcasts via GossipSub.
func (n *Node) SendMessage(msg *message.Message) error {
	data, err := msg.Serialize()
	if err != nil {
		return fmt.Errorf("serialize message: %w", err)
	}

	if n.Debug {
		shortID := msg.ID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		n.Log.Printf("📤 sending message %s (type: %s, to: %q)", shortID, msg.Type, msg.Recipient)
	}

	// Destination-aware routing applies only to addressed messages on nodes
	// with a configured budget. Broadcasts and budget-less nodes keep the
	// epidemic flood exactly as before.
	if msg.Recipient != "" && n.SprayBudget > 0 {
		if msg.CopiesMade < 1 {
			msg.CopiesMade = 1
		}
		n.emitRelay(RelayEvent{
			MessageID: msg.ID,
			Type:      msg.Type,
			Action:    RelaySent,
			Hops:      0,
			TTL:       msg.TTL,
			Copies:    msg.CopiesMade,
			Timestamp: time.Now().UnixNano(),
		})
		n.routeToPeers(msg, "")
		return nil
	}

	n.emitRelay(RelayEvent{
		MessageID: msg.ID,
		Type:      msg.Type,
		Action:    RelaySent,
		Hops:      0,
		TTL:       msg.TTL,
		Copies:    msg.CopiesMade,
		Timestamp: time.Now().UnixNano(),
	})

	// If we have a specific recipient, try direct stream first
	if msg.Recipient != "" {
		recipientPID, err := peer.Decode(msg.Recipient)
		if err == nil {
			err = n.sendDirect(recipientPID, data)
			if err == nil {
				return nil // Sent directly
			}
			if n.Debug {
				n.Log.Printf("direct send failed, falling back to pubsub: %v", err)
			}
		}
	}

	// Publish to the mesh via GossipSub
	return n.Publish(msg)
}

// Publish broadcasts a message to all peers via GossipSub.
func (n *Node) Publish(msg *message.Message) error {
	data, err := msg.Serialize()
	if err != nil {
		return err
	}
	return n.Topic.Publish(n.ctx, data)
}

// sendDirect sends data directly to a specific peer via a new stream.
func (n *Node) sendDirect(pid peer.ID, data []byte) error {
	ctx, cancel := context.WithTimeout(n.ctx, DefaultMessageTimeout)
	defer cancel()

	s, err := n.Host.NewStream(ctx, pid, ProtocolID)
	if err != nil {
		return fmt.Errorf("open stream to %s: %w", pid.ShortString(), err)
	}
	defer s.Close()

	_, err = s.Write(data)
	if err != nil {
		return fmt.Errorf("write stream: %w", err)
	}

	return nil
}

// handleStream processes incoming direct streams from peers.
func (n *Node) handleStream(s network.Stream) {
	pid := s.Conn().RemotePeer()
	if n.Debug {
		n.Log.Printf("📩 incoming stream from %s", pid.ShortString())
	}

	// Peer membership follows CONNECTIONS, not streams: a peer that closes
	// one stream may still be connected (and reachable) for the next. The
	// connection notifiee removes peers when the connection itself drops.
	// (Removing here made peers vanish from KnownPeers after a single
	// stream — fatal for direct-stream routing, invisible under pubsub.)
	n.addPeer(pid)
	defer s.Close()

	// Set a read deadline
	s.SetDeadline(time.Now().Add(DefaultMessageTimeout))

	t0 := time.Now()
	if n.Debug {
		n.Log.Printf("stream from %s: reading", pid.ShortString())
	}
	data, err := io.ReadAll(s)
	if n.Debug {
		n.Log.Printf("stream from %s: read %d bytes in %s", pid.ShortString(), len(data), time.Since(t0).Round(time.Millisecond))
	}
	if err != nil {
		if n.Debug {
			n.Log.Printf("stream read error from %s: %v", pid.ShortString(), err)
		}
		return
	}

	msg, err := message.DeserializeMessage(data)
	if err != nil {
		if n.Debug {
			n.Log.Printf("invalid message from %s: %v", pid.ShortString(), err)
		}
		return
	}

	n.deliverMessage(msg, pid.String())
}

// handlePubSub processes messages received from the GossipSub topic.
func (n *Node) handlePubSub(sub *pubsub.Subscription) {
	for {
		pbMsg, err := sub.Next(n.ctx)
		if err != nil {
			if n.ctx.Err() == nil {
				n.Log.Printf("pubsub error: %v", err)
			}
			return
		}

		// Record the relaying peer
		relayPeer := pbMsg.ReceivedFrom

		msg, err := message.DeserializeMessage(pbMsg.Data)
		if err != nil {
			if n.Debug {
				n.Log.Printf("invalid pubsub message from %s: %v", relayPeer.ShortString(), err)
			}
			continue
		}

		if n.Debug {
			shortID := msg.ID
			shortSender := msg.Sender
			if len(shortID) > 8 {
				shortID = shortID[:8]
			}
			if len(shortSender) > 8 {
				shortSender = shortSender[:8]
			}
			n.Log.Printf("📨 pubsub message %s from %s via %s (hops: %d)",
				shortID, shortSender, relayPeer.ShortString(), msg.HopCount)
		}

		n.deliverMessage(msg, relayPeer.String())
	}
}

// deliverMessage processes and optionally relays an incoming message.
// The from parameter is the peer the message arrived through ("" if unknown).
func (n *Node) deliverMessage(msg *message.Message, from string) {
	// Dedup: skip if we've already seen this message
	n.seenLock.Lock()
	if n.seen[msg.ID] {
		n.seenLock.Unlock()
		n.emitRelay(RelayEvent{
			MessageID: msg.ID,
			Type:      msg.Type,
			Action:    RelayDropped,
			From:      from,
			Hops:      msg.HopCount,
			TTL:       msg.TTL,
			Copies:    msg.CopiesMade,
			Timestamp: time.Now().UnixNano(),
		})
		return
	}
	n.seen[msg.ID] = true
	n.seenLock.Unlock()

	// A message from a real peer IS an encounter: refresh predictability.
	if from != "" {
		n.recordEncounter(from, time.Now().Unix())
	}

	// Emit the arrival event.
	n.emitRelay(RelayEvent{
		MessageID: msg.ID,
		Type:      msg.Type,
		Action:    RelayReceived,
		From:      from,
		Hops:      msg.HopCount,
		TTL:       msg.TTL,
		Copies:    msg.CopiesMade,
		Timestamp: time.Now().UnixNano(),
	})

	// Add sender to known peers
	senderPID, senderErr := peer.Decode(msg.Sender)
	if senderErr == nil && senderPID != n.Host.ID() {
		n.addPeer(senderPID)
	}

	// Relay if TTL > 0 (store-and-forward)
	if msg.TTL > 0 && (senderErr != nil || senderPID != n.Host.ID()) {
		msg.DecrementTTL()
		n.forwardMessage(msg, from)
	} else if msg.TTL <= 0 && (senderErr != nil || senderPID != n.Host.ID()) {
		// TTL exhausted: the message dies at this node.
		n.emitRelay(RelayEvent{
			MessageID: msg.ID,
			Type:      msg.Type,
			Action:    RelayDropped,
			From:      from,
			Hops:      msg.HopCount,
			TTL:       msg.TTL,
			Copies:    msg.CopiesMade,
			Timestamp: time.Now().UnixNano(),
		})
	}

	// Deliver to application layer
	if n.OnMessage != nil {
		n.OnMessage(msg)
	}

	// Auto-acknowledge delivery receipt for messages addressed to us
	// (chat, file, sos messages that have us as the recipient)
	if msg.Recipient == n.Host.ID().String() && msg.Sender != n.Host.ID().String() {
		// Don't auto-ack our own messages
		if msg.Type == message.TypeChat || msg.Type == message.TypeFile || msg.Type == message.TypeSOS {
			// Send "received" ack immediately, addressed back to the original sender
			ack := message.NewDeliveryAck(n.Host.ID().String(), msg.ID, msg.Sender, message.DeliveryReceived, 0, "")
			// Use a short TTL for acks (high priority, short distance)
			ack.TTL = 8
			_ = n.SendMessage(ack)
		}
	}
}

// emitRelay fires the OnRelay callback if one is registered.
func (n *Node) emitRelay(evt RelayEvent) {
	if n.OnRelay != nil {
		n.OnRelay(evt)
	}
}

// forwardMessage hands a received message onward. Destination-aware nodes
// route it through the decision layer; everyone else floods via pubsub
// (the historical epidemic behavior).
func (n *Node) forwardMessage(msg *message.Message, from string) {
	if n.SprayBudget > 0 && msg.Recipient != "" {
		n.routeToPeers(msg, from)
		return
	}

	n.emitRelay(RelayEvent{
		MessageID: msg.ID,
		Type:      msg.Type,
		Action:    RelayForwarded,
		From:      from,
		Hops:      msg.HopCount,
		TTL:       msg.TTL,
		Copies:    msg.CopiesMade,
		Timestamp: time.Now().UnixNano(),
	})
	go func() {
		if n.Topic == nil {
			return // not started; nothing to relay onto
		}
		data, _ := msg.Serialize()
		if err := n.Topic.Publish(n.ctx, data); err != nil && n.Debug {
			n.Log.Printf("relay error: %v", err)
		}
	}()
}

// routeToPeers forwards an addressed message to the peers the routing
// decision layer selects, over direct streams — never back to the peer it
// arrived from. The spray budget travels on the wire (CopiesMade), so every
// carrier shares the same accounting.
func (n *Node) routeToPeers(msg *message.Message, from string) {
	n.routeLock.Lock()
	defer n.routeLock.Unlock()

	f := n.Forwarder
	if f == nil {
		f = routing.SprayAndWait{L: n.SprayBudget}
	}
	me := n.Host.ID().String()
	state := routing.MessageState{
		ID:         msg.ID,
		Src:        msg.Sender,
		Dst:        msg.Recipient,
		CopiesMade: msg.CopiesMade,
		TTL:        int64(msg.TTL),
	}
	// Our own predictability for the destination. The PEER's predictability
	// (driving PROPHET handoff) requires predictability-vector exchange at
	// encounter — a later protocol slice; the decision layer already
	// implements handoff for the simulator.
	myStat := n.statForDestinationLocked(msg.Recipient, time.Now().Unix())

	for _, pid := range n.routingCandidates(from) {
		peerStr := pid.String()
		peerHoldsCopy := n.sprayed[msg.ID][peerStr]
		action := f.Decide(&state, me, peerStr, myStat, routing.EncounterStat{}, peerHoldsCopy)

		switch action {
		case routing.ActionSpray:
			// A brand-new copy: consume one unit of the budget, carried on
			// the wire so downstream carriers see the updated count.
			state.CopiesMade++
			msg.CopiesMade = state.CopiesMade
			fallthrough
		case routing.ActionDeliver, routing.ActionHandoff:
			n.markSprayed(msg.ID, peerStr)
			n.emitRelay(RelayEvent{
				MessageID: msg.ID,
				Type:      msg.Type,
				Action:    RelayForwarded,
				From:      from,
				Hops:      msg.HopCount,
				TTL:       msg.TTL,
				Copies:    msg.CopiesMade,
				Timestamp: time.Now().UnixNano(),
			})
			n.sendCopy(pid, msg)
		default:
			// ActionKeep: budget spent, no better relay in view.
			// ActionDup: peer already holds a copy from us.
		}
	}
}

// sendCopy pushes the message to a peer over a direct stream.
func (n *Node) sendCopy(pid peer.ID, msg *message.Message) {
	data, err := msg.Serialize()
	if err != nil {
		if n.Debug {
			n.Log.Printf("route serialize error: %v", err)
		}
		return
	}
	if err := n.sendDirect(pid, data); err != nil && n.Debug {
		n.Log.Printf("route send to %s failed: %v", pid.ShortString(), err)
	}
}

// recordEncounter refreshes our predictability for a peer we just met.
// Transitivity needs the peer's own predictability vector (exchanged at
// encounter in a later slice), so this applies the encounter and aging
// kinetics only.
func (n *Node) recordEncounter(peerID string, now int64) {
	n.routeLock.Lock()
	defer n.routeLock.Unlock()

	stat, ok := n.encounters[peerID]
	if !ok {
		stat = routing.EncounterStat{Peer: peerID}
	}
	stat.AgePredictability(stat.Aging(now))
	stat.RecordEncounter(now, nil)
	n.encounters[peerID] = stat
}

// statForDestinationLocked returns our current predictability for dst,
// aged up to the present. Caller holds routeLock.
func (n *Node) statForDestinationLocked(dst string, now int64) routing.EncounterStat {
	stat, ok := n.encounters[dst]
	if !ok {
		return routing.EncounterStat{Peer: dst}
	}
	stat.AgePredictability(stat.Aging(now))
	return stat
}

// routingCandidates lists known peers a message may be forwarded to: every
// connected peer except ourselves and the peer it arrived from (never hand
// a copy back to its source — that is pure ping-pong waste). Sorted so the
// decisions are deterministic regardless of map iteration order.
func (n *Node) routingCandidates(from string) []peer.ID {
	peers := n.KnownPeers()
	out := make([]peer.ID, 0, len(peers))
	me := n.Host.ID().String()
	for _, p := range peers {
		if p.String() == me || (from != "" && p.String() == from) {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// markSprayed records that we handed a copy of msgID to peer (dedup: don't
// hand a second copy to the same peer). Caller holds routeLock.
func (n *Node) markSprayed(msgID, peer string) {
	if n.sprayed[msgID] == nil {
		n.sprayed[msgID] = make(map[string]bool)
	}
	n.sprayed[msgID][peer] = true
}

// KnownPeers returns the list of peer IDs we've connected to.
func (n *Node) KnownPeers() []peer.ID {
	n.peerLock.RLock()
	defer n.peerLock.RUnlock()

	peers := make([]peer.ID, 0, len(n.peers))
	for pid := range n.peers {
		peers = append(peers, pid)
	}
	return peers
}

// addPeer records a new peer connection.
func (n *Node) addPeer(pid peer.ID) {
	n.peerLock.Lock()
	defer n.peerLock.Unlock()

	if !n.peers[pid] {
		n.peers[pid] = true
		if n.Debug {
			n.Log.Printf("➕ peer connected: %s", pid.ShortString())
		}
		if n.OnPeerJoin != nil {
			n.OnPeerJoin(pid)
		}
	}
}

// removePeer records a peer disconnection.
func (n *Node) removePeer(pid peer.ID) {
	n.peerLock.Lock()
	defer n.peerLock.Unlock()

	delete(n.peers, pid)
	if n.Debug {
		n.Log.Printf("➖ peer disconnected: %s", pid.ShortString())
	}
	if n.OnPeerLeave != nil {
		n.OnPeerLeave(pid)
	}
}

// --- mDNS Discovery ---

type discoveryNotifee struct {
	Node *Node
}

func (n *discoveryNotifee) HandlePeerFound(pi peer.AddrInfo) {
	if pi.ID == n.Node.Host.ID() {
		return // Skip self
	}
	if n.Node.Debug {
		n.Node.Log.Printf("🔍 discovered peer via mDNS: %s", pi.ID.ShortString())
	}
	n.Node.addPeer(pi.ID)

	// Connect to the discovered peer
	go func() {
		ctx, cancel := context.WithTimeout(n.Node.ctx, 10*time.Second)
		defer cancel()

		if err := n.Node.Host.Connect(ctx, pi); err != nil {
			if n.Node.Debug {
				n.Node.Log.Printf("connect to discovered peer %s: %v", pi.ID.ShortString(), err)
			}
			return
		}

		if n.Node.Debug {
			n.Node.Log.Printf("✅ connected to %s", pi.ID.ShortString())
		}
	}()
}

// startMDNS starts mDNS peer discovery on the local network.
func (n *Node) startMDNS() {
	service := mdns.NewMdnsService(n.Host, ServiceName, &discoveryNotifee{Node: n})
	if err := service.Start(); err != nil {
		n.Log.Printf("mDNS start error: %v", err)
		return
	}
	if n.Debug {
		n.Log.Printf("🔍 mDNS discovery started on %s", ServiceName)
	}
}

// ConnectToPeer dials a peer by their multiaddress string.
func (n *Node) ConnectToPeer(addrStr string) error {
	maddr, err := multiaddr.NewMultiaddr(addrStr)
	if err != nil {
		return fmt.Errorf("parse multiaddr: %w", err)
	}

	// Extract peer ID from the multiaddress
	pidStr, err := maddr.ValueForProtocol(multiaddr.P_P2P)
	if err != nil {
		return fmt.Errorf("extract peer ID: %w", err)
	}
	pid, err := peer.Decode(pidStr)
	if err != nil {
		return fmt.Errorf("decode peer ID: %w", err)
	}

	pi := peer.AddrInfo{
		ID:    pid,
		Addrs: []multiaddr.Multiaddr{maddr},
	}

	ctx, cancel := context.WithTimeout(n.ctx, 10*time.Second)
	defer cancel()

	if err := n.Host.Connect(ctx, pi); err != nil {
		return fmt.Errorf("connect to peer: %w", err)
	}

	n.addPeer(pid)
	return nil
}

// AddrList returns the current multiaddresses of this node.
func (n *Node) AddrList() []string {
	addrs := n.Host.Addrs()
	result := make([]string, len(addrs))
	for i, addr := range addrs {
		result[i] = fmt.Sprintf("%s/p2p/%s", addr, n.Host.ID())
	}
	return result
}

// MultiaddrString returns a single multiaddress string for this node.
func (n *Node) MultiaddrString() string {
	addrs := n.Host.Addrs()
	if len(addrs) == 0 {
		return ""
	}
	return fmt.Sprintf("%s/p2p/%s", addrs[0], n.Host.ID())
}

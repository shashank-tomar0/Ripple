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
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	"github.com/libp2p/go-libp2p/p2p/net/connmgr"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/multiformats/go-multiaddr"

	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/message"
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

	// Message relay
	seenLock sync.RWMutex
	seen     map[string]bool // dedup message IDs

	// Delivery receipt manager
	// ReceiptManager interface{} // placeholder for delivery receipts (to avoid import cycle)

	// Logging
	Debug bool
	log   *log.Logger
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
		ctx:     ctx,
		cancel:  cancel,
		PrivKey: privKey,
		PeerID:  pid,
		peers:   make(map[peer.ID]bool),
		seen:    make(map[string]bool),
		log:     log.Default(),
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

	if n.Debug {
		n.log.Printf("🔗 libp2p host: %s", h.ID())
		for _, addr := range h.Addrs() {
			n.log.Printf("   listen: %s/p2p/%s", addr, h.ID())
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

	// Start mDNS discovery
	n.startMDNS()

	if n.Debug {
		n.log.Printf("📡 PubSub topic: %s", PubSubTopic)
	}

	return nil
}

// Close shuts down the node.
func (n *Node) Close() {
	n.cancel()
	if err := n.Host.Close(); err != nil {
		n.log.Printf("error closing host: %v", err)
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
		n.log.Printf("📤 sending message %s (type: %s, to: %q)", shortID, msg.Type, msg.Recipient)
	}

	// If we have a specific recipient, try direct stream first
	if msg.Recipient != "" {
		recipientPID, err := peer.Decode(msg.Recipient)
		if err == nil {
			err = n.sendDirect(recipientPID, data)
			if err == nil {
				return nil // Sent directly
			}
			if n.Debug {
				n.log.Printf("direct send failed, falling back to pubsub: %v", err)
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
		n.log.Printf("📩 incoming stream from %s", pid.ShortString())
	}

	n.addPeer(pid)
	defer n.removePeer(pid)
	defer s.Close()

	// Set a read deadline
	s.SetDeadline(time.Now().Add(DefaultMessageTimeout))

	data, err := io.ReadAll(s)
	if err != nil {
		if n.Debug {
			n.log.Printf("stream read error from %s: %v", pid.ShortString(), err)
		}
		return
	}

	msg, err := message.DeserializeMessage(data)
	if err != nil {
		if n.Debug {
			n.log.Printf("invalid message from %s: %v", pid.ShortString(), err)
		}
		return
	}

	n.deliverMessage(msg)
}

// handlePubSub processes messages received from the GossipSub topic.
func (n *Node) handlePubSub(sub *pubsub.Subscription) {
	for {
		pbMsg, err := sub.Next(n.ctx)
		if err != nil {
			if n.ctx.Err() == nil {
				n.log.Printf("pubsub error: %v", err)
			}
			return
		}

		// Record the relaying peer
		relayPeer := pbMsg.ReceivedFrom.ShortString()

		msg, err := message.DeserializeMessage(pbMsg.Data)
		if err != nil {
			if n.Debug {
				n.log.Printf("invalid pubsub message from %s: %v", relayPeer, err)
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
			n.log.Printf("📨 pubsub message %s from %s via %s (hops: %d)",
				shortID, shortSender, relayPeer, msg.HopCount)
		}

		n.deliverMessage(msg)
	}
}

// deliverMessage processes and optionally relays an incoming message.
func (n *Node) deliverMessage(msg *message.Message) {
	// Dedup: skip if we've already seen this message
	n.seenLock.Lock()
	if n.seen[msg.ID] {
		n.seenLock.Unlock()
		return
	}
	n.seen[msg.ID] = true
	n.seenLock.Unlock()

	// Add sender to known peers
	senderPID, senderErr := peer.Decode(msg.Sender)
	if senderErr == nil && senderPID != n.Host.ID() {
		n.addPeer(senderPID)
	}

	// Relay if TTL > 0 (store-and-forward)
	if msg.TTL > 0 && (senderErr != nil || senderPID != n.Host.ID()) {
		msg.DecrementTTL()
		go func() {
			data, _ := msg.Serialize()
			if err := n.Topic.Publish(n.ctx, data); err != nil && n.Debug {
				n.log.Printf("relay error: %v", err)
			}
		}()
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
			// Send "received" ack immediately
			ack := message.NewDeliveryAck(n.Host.ID().String(), msg.ID, message.DeliveryReceived)
			// Use a short TTL for acks (high priority, short distance)
			ack.TTL = 8
			_ = n.SendMessage(ack)
		}
	}
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
			n.log.Printf("➕ peer connected: %s", pid.ShortString())
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
		n.log.Printf("➖ peer disconnected: %s", pid.ShortString())
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
		n.Node.log.Printf("🔍 discovered peer via mDNS: %s", pi.ID.ShortString())
	}
	n.Node.addPeer(pi.ID)

	// Connect to the discovered peer
	go func() {
		ctx, cancel := context.WithTimeout(n.Node.ctx, 10*time.Second)
		defer cancel()

		if err := n.Node.Host.Connect(ctx, pi); err != nil {
			if n.Node.Debug {
				n.Node.log.Printf("connect to discovered peer %s: %v", pi.ID.ShortString(), err)
			}
			return
		}

		if n.Node.Debug {
			n.Node.log.Printf("✅ connected to %s", pi.ID.ShortString())
		}
	}()
}

// startMDNS starts mDNS peer discovery on the local network.
func (n *Node) startMDNS() {
	service := mdns.NewMdnsService(n.Host, ServiceName, &discoveryNotifee{Node: n})
	if err := service.Start(); err != nil {
		n.log.Printf("mDNS start error: %v", err)
		return
	}
	if n.Debug {
		n.log.Printf("🔍 mDNS discovery started on %s", ServiceName)
	}
}

// ConnectToPeer dials a peer by their multiaddress string.
func (n *Node) ConnectToPeer(addrStr string) error {
	maddr, err := multiaddr.NewMultiaddr(addrStr)
	if err != nil {
		return fmt.Errorf("parse multiaddr: %w", err)
	}

	// Extract peer ID from the multiaddress
	pid, err := peer.Decode(maddr.ValueForProtocol(multiaddr.P_P2P))
	if err != nil {
		return fmt.Errorf("extract peer ID: %w", err)
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

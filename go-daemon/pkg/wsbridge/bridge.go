// Package wsbridge provides a WebSocket interface for the Ripple mesh daemon.
//
// It allows Flutter mobile app clients to connect via WebSocket and interact
// with the libp2p mesh network — sending/receiving messages, getting peer
// join/leave notifications, and receiving identity info on connection.
package wsbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/crypto"
	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/message"
	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/mesh"
)

const (
	// DefaultPort is the default WebSocket server listen port.
	DefaultPort = 9876

	// Time allowed to write a message to the WebSocket peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the WebSocket peer.
	pongWait = 60 * time.Second

	// pingPeriod is the period between ping messages (must be less than pongWait).
	pingPeriod = (pongWait * 9) / 10

	// maxMessageSize is the maximum size of a message from a WebSocket client.
	maxMessageSize = 65536

	// sendBufSize is the size of the buffered send channel per client.
	sendBufSize = 256
)

// upgrader configures the WebSocket HTTP-to-WS upgrade.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// Bridge provides a WebSocket server that bridges the Ripple mesh network
// to connected Flutter app clients.
type Bridge struct {
	node       *mesh.Node
	peerID     string
	nickname   string
	addr       string
	e2eManager *crypto.Manager

	clients     map[*Client]bool
	clientsMu   sync.RWMutex

	peerNicknames   map[peer.ID]string
	peerNicknamesMu sync.RWMutex

	server  *http.Server
	log     *log.Logger
	started bool
	closeCh chan struct{}
}

// Client represents a single connected WebSocket client.
type Client struct {
	bridge *Bridge
	conn   *websocket.Conn
	send   chan []byte
	mu     sync.Mutex // protects concurrent writes to conn
}

// identityMessage is sent to clients immediately on connection.
type identityMessage struct {
	Type       string `json:"type"`
	PeerID     string `json:"peer_id"`
	Nickname   string `json:"nickname"`
	PublicKey  string `json:"public_key,omitempty"`
}

// peerEvent is sent when a peer joins or leaves the mesh.
type peerEvent struct {
	Type string   `json:"type"`
	Peer peerInfo `json:"peer"`
}

// peerInfo carries peer identification and status.
type peerInfo struct {
	PeerID   string `json:"peer_id"`
	Nickname string `json:"nickname"`
	IsOnline bool   `json:"is_online"`
}

// wsIncoming is a message received from a WebSocket client.
type wsIncoming struct {
	Type       string `json:"type"`
	ID         string `json:"id"`
	Sender     string `json:"sender"`
	SenderNick string `json:"sender_nick"`
	Recipient  string `json:"recipient"`
	Payload    string `json:"payload"`
	Timestamp  int64  `json:"ts"`
}

// NewBridge creates a new WebSocket bridge.
//
// The bridge is bound to the given mesh node and will relay messages between
// the mesh and WebSocket clients. The peerID and nickname identify the local
// node to connecting clients. The e2eManager handles E2E encryption.
// The bridge starts in the stopped state; call Start() to begin accepting connections.
func NewBridge(node *mesh.Node, peerID, nickname string, port int, e2eManager *crypto.Manager) *Bridge {
	addr := fmt.Sprintf(":%d", port)
	return &Bridge{
		node:        node,
		peerID:      peerID,
		nickname:    nickname,
		addr:        addr,
		e2eManager:  e2eManager,
		clients:     make(map[*Client]bool),
		peerNicknames: make(map[peer.ID]string),
		log:         log.New(log.Writer(), "[wsbridge] ", log.LstdFlags),
		closeCh:     make(chan struct{}),
	}
}

// Start begins the WebSocket bridge. It:
//   - Registers mesh callbacks (OnMessage, OnPeerJoin, OnPeerLeave)
//   - Starts the HTTP server on the configured address
//   - Returns immediately; the server runs in a background goroutine
func (b *Bridge) Start() error {
	if b.started {
		return fmt.Errorf("bridge already started")
	}
	b.started = true

	// Wrap existing OnMessage callback
	origOnMsg := b.node.OnMessage
	b.node.OnMessage = func(msg *message.Message) {
		if origOnMsg != nil {
			origOnMsg(msg)
		}
		b.handleMeshMessage(msg)
	}

	// Wrap existing OnPeerJoin callback
	origJoin := b.node.OnPeerJoin
	b.node.OnPeerJoin = func(pid peer.ID) {
		if origJoin != nil {
			origJoin(pid)
		}
		b.handlePeerJoin(pid)
	}

	// Wrap existing OnPeerLeave callback
	origLeave := b.node.OnPeerLeave
	b.node.OnPeerLeave = func(pid peer.ID) {
		if origLeave != nil {
			origLeave(pid)
		}
		b.handlePeerLeave(pid)
	}

	// Set up HTTP routes
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", b.handleWS)
	mux.HandleFunc("/health", b.handleHealth)

	b.server = &http.Server{
		Addr:    b.addr,
		Handler: mux,
	}

	// Start the HTTP server in a background goroutine
	go func() {
		b.log.Printf("WebSocket server listening on %s/ws", b.addr)
		if err := b.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			b.log.Printf("HTTP server error: %v", err)
		}
	}()

	return nil
}

// Stop gracefully shuts down the bridge, closing all client connections
// and stopping the HTTP server. It blocks until shutdown is complete or
// the 5-second timeout expires.
func (b *Bridge) Stop() {
	close(b.closeCh)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Close all clients
	b.clientsMu.Lock()
	for client := range b.clients {
		delete(b.clients, client)
		close(client.send)
		client.conn.Close()
	}
	b.clientsMu.Unlock()

	// Shutdown HTTP server
	if b.server != nil {
		if err := b.server.Shutdown(ctx); err != nil {
			b.log.Printf("HTTP shutdown error: %v", err)
		}
	}
}

// handleWS upgrades an HTTP connection to WebSocket and registers the client.
func (b *Bridge) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		b.log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	client := &Client{
		bridge: b,
		conn:   conn,
		send:   make(chan []byte, sendBufSize),
	}

	b.clientsMu.Lock()
	b.clients[client] = true
	clientCount := len(b.clients)
	b.clientsMu.Unlock()

	b.log.Printf("WebSocket client connected (%d total)", clientCount)

	// Send identity information immediately on connection
	b.sendIdentity(client)

	// Start read and write pump goroutines
	go client.writePump()
	go client.readPump()
}

// handleHealth returns a simple JSON health check.
func (b *Bridge) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	b.clientsMu.RLock()
	count := len(b.clients)
	b.clientsMu.RUnlock()

	resp := map[string]interface{}{
		"status":   "ok",
		"peer_id":  b.peerID,
		"nickname": b.nickname,
		"clients":  count,
	}
	json.NewEncoder(w).Encode(resp)
}

// sendIdentity sends the identity message to a single client.
func (b *Bridge) sendIdentity(client *Client) {
	msg := identityMessage{
		Type:       "identity",
		PeerID:     b.peerID,
		Nickname:   b.nickname,
		PublicKey:  b.e2eManager.MyPublicKeyHex(),
	}
	data, err := json.Marshal(msg)
	if err != nil {
		b.log.Printf("identity marshal error: %v", err)
		return
	}
	select {
	case client.send <- data:
	default:
		// Client send buffer full; skip identity (will be re-sent on reconnect)
	}
}

// broadcast sends data to all connected WebSocket clients. Slow clients
// that have a full send buffer are skipped to avoid blocking.
func (b *Bridge) broadcast(data []byte) {
	b.clientsMu.RLock()
	defer b.clientsMu.RUnlock()

	for client := range b.clients {
		select {
		case client.send <- data:
		default:
			// Client buffer full; skip this message for this client
		}
	}
}

// handleMeshMessage is called when the mesh node receives a message.
// It decrypts E2E encrypted messages, caches the sender's nickname,
// and forwards the message to all WebSocket clients.
func (b *Bridge) handleMeshMessage(msg *message.Message) {
	// Cache sender nickname for peer display purposes
	if msg.Sender != "" {
		pid, err := peer.Decode(msg.Sender)
		if err == nil && msg.SenderNick != "" {
			b.peerNicknamesMu.Lock()
			b.peerNicknames[pid] = msg.SenderNick
			b.peerNicknamesMu.Unlock()
		}
	}

	// Try to decrypt if this is an E2E encrypted message
	encrypted := msg.IsEncrypted()
	if encrypted && b.e2eManager != nil {
		if err := b.e2eManager.DecryptMessage(msg); err != nil {
			b.log.Printf("⚠️  Failed to decrypt E2E message from %s: %v", msg.Sender, err)
			// Don't forward undecryptable messages
			return
		}
	}

	// File-type messages use structured file event format
	if msg.Type == message.TypeFile {
		b.BroadcastFileNotification(msg)
		return
	}

	// SOS-type messages get special handling with extra fields
	if msg.Type == message.TypeSOS {
		b.BroadcastSOS(msg)
		return
	}

	// Delivery acknowledgment messages - forward to Flutter
	if msg.Type == message.TypeDeliveryAck {
		b.BroadcastDeliveryReceipt(msg)
		return
	}

	// Key exchange messages - handle E2E key setup
	if msg.Type == message.TypeKeyExchange {
		b.handleKeyExchange(msg)
		return
	}

	// Default: serialize the mesh message and broadcast to all WS clients.
	// Add encrypted flag so Flutter can show the padlock icon.
	data, err := msg.Serialize()
	if err != nil {
		b.log.Printf("message serialize error: %v", err)
		return
	}

	var wsMsg map[string]interface{}
	if err := json.Unmarshal(data, &wsMsg); err != nil {
		b.log.Printf("message unmarshal error: %v", err)
		return
	}
	wsMsg["encrypted"] = encrypted

	data, err = json.Marshal(wsMsg)
	if err != nil {
		b.log.Printf("message marshal error: %v", err)
		return
	}
	b.broadcast(data)
}

// BroadcastSOS sends an SOS message to all WebSocket clients with
// special flags for high-visibility UI treatment.
func (b *Bridge) BroadcastSOS(msg *message.Message) {
	// Parse SOS payload for extra fields
	var sosPayload message.SOSPayload
	if err := json.Unmarshal([]byte(msg.Payload), &sosPayload); err != nil {
		b.log.Printf("SOS payload parse error: %v", err)
	}

	// Build the WebSocket message with SOS-specific fields
	wsMsg := map[string]interface{}{
		"type":        "sos",
		"id":          msg.ID,
		"sender":      msg.Sender,
		"sender_nick": msg.SenderNick,
		"payload":     msg.Payload,
		"ts":          msg.Timestamp,
		"ttl":         msg.TTL,
		"hops":        msg.HopCount,
		"sos":         true,
		"urgency":     sosPayload.Urgency,
		"message":     sosPayload.Message,
		"lat":         sosPayload.Latitude,
		"lon":         sosPayload.Longitude,
		"accuracy":    sosPayload.Accuracy,
		"expire_min":  sosPayload.AutoExpire,
		"ack_required": sosPayload.AckRequired,
	}

	data, err := json.Marshal(wsMsg)
	if err != nil {
		b.log.Printf("SOS marshal error: %v", err)
		return
	}
	b.broadcast(data)
}

// BroadcastFileNotification parses a file-type message's payload and broadcasts
// a structured file event to all connected WebSocket clients.
//
// WS message format varies by status:
//
//	started:  { type:"file", file_id, filename, file_size, mime_type, chunk_count, status:"started", sender, sender_nick, ts }
//	progress: { type:"file", file_id, status:"progress", progress, chunk_idx, chunk_size }
//	complete: { type:"file", file_id, status:"complete", filename, output_path }
func (b *Bridge) BroadcastFileNotification(msg *message.Message) {
	var fpayload message.FileMessagePayload
	if err := json.Unmarshal([]byte(msg.Payload), &fpayload); err != nil {
		b.log.Printf("file notification parse error: %v", err)
		return
	}

	// Build the file event, adding extra fields per status
	event := map[string]interface{}{
		"type":    "file",
		"file_id": fpayload.FileID,
		"status":  fpayload.Status,
	}

	switch fpayload.Status {
	case "started":
		event["filename"] = fpayload.FileName
		event["file_size"] = fpayload.FileSize
		event["mime_type"] = fpayload.MimeType
		event["chunk_count"] = fpayload.ChunkCount
		event["sender"] = msg.Sender
		event["sender_nick"] = msg.SenderNick
		event["ts"] = msg.Timestamp

	case "progress":
		event["progress"] = fpayload.Progress
		event["chunk_idx"] = fpayload.ChunkIdx
		event["chunk_size"] = fpayload.ChunkSize

	case "complete":
		event["filename"] = fpayload.FileName
		if fpayload.OutputPath != "" {
			event["output_path"] = fpayload.OutputPath
		}

	case "failed":
		event["sender"] = msg.Sender
		event["sender_nick"] = msg.SenderNick
	}

	data, err := json.Marshal(event)
	if err != nil {
		b.log.Printf("file event marshal error: %v", err)
		return
	}
	b.broadcast(data)
}

// handlePeerJoin broadcasts a peer_join event to all WebSocket clients.
func (b *Bridge) handlePeerJoin(pid peer.ID) {
	nickname := b.getPeerNickname(pid)

	evt := peerEvent{
		Type: "peer_join",
		Peer: peerInfo{
			PeerID:   pid.String(),
			Nickname: nickname,
			IsOnline: true,
		},
	}
	b.sendPeerEvent(evt)
}

// handlePeerLeave broadcasts a peer_leave event to all WebSocket clients.
func (b *Bridge) handlePeerLeave(pid peer.ID) {
	nickname := b.getPeerNickname(pid)

	evt := peerEvent{
		Type: "peer_leave",
		Peer: peerInfo{
			PeerID:   pid.String(),
			Nickname: nickname,
			IsOnline: false,
		},
	}
	b.sendPeerEvent(evt)
}

// getPeerNickname returns the cached nickname for a peer, falling back to
// the short peer ID string if no nickname has been learned yet.
func (b *Bridge) getPeerNickname(pid peer.ID) string {
	b.peerNicknamesMu.RLock()
	nick, ok := b.peerNicknames[pid]
	b.peerNicknamesMu.RUnlock()
	if ok && nick != "" {
		return nick
	}
	return pid.ShortString()
}

// sendPeerEvent marshals and broadcasts a peer event to all clients.
func (b *Bridge) sendPeerEvent(evt peerEvent) {
	data, err := json.Marshal(evt)
	if err != nil {
		b.log.Printf("peer event marshal error: %v", err)
		return
	}
	b.broadcast(data)
}

// BroadcastDeliveryReceipt parses a delivery acknowledgment message and broadcasts
// it to all connected WebSocket clients.
//
// WS message format:
// {
//   "type": "delivery_ack",
//   "msg_id": "abc123",
//   "status": "delivered",
//   "original_sender": "12D3...",
//   "ts": 1700000000000000000,
//   "hops": 3
// }
func (b *Bridge) BroadcastDeliveryReceipt(msg *message.Message) {
	var info message.DeliveryInfo
	if err := json.Unmarshal([]byte(msg.Payload), &info); err != nil {
		b.log.Printf("delivery receipt parse error: %v", err)
		return
	}

	wsMsg := map[string]interface{}{
		"type":            "delivery_ack",
		"msg_id":          info.MessageID,
		"status":          string(info.Status),
		"original_sender": info.OriginalSender,
		"ts":              info.Timestamp,
		"hops":            info.HopCount,
	}

	if info.Error != "" {
		wsMsg["error"] = info.Error
	}

	data, err := json.Marshal(wsMsg)
	if err != nil {
		b.log.Printf("delivery receipt marshal error: %v", err)
		return
	}
	b.broadcast(data)
}

// handleKeyExchange processes an incoming key exchange message.
// It stores the peer's public key in the E2E manager and broadcasts
// the key exchange info to Flutter clients.
func (b *Bridge) handleKeyExchange(msg *message.Message) {
	if b.e2eManager == nil {
		b.log.Printf("⚠️  No E2E manager configured, ignoring key exchange from %s", msg.Sender)
		return
	}

	if err := b.e2eManager.HandleKeyExchangeMessage(msg); err != nil {
		b.log.Printf("⚠️  Failed to handle key exchange from %s: %v", msg.Sender, err)
		return
	}

	// Broadcast the key exchange info to Flutter clients so they know
	// E2E is now established with this peer
	var payload message.KeyExchangePayload
	if err := json.Unmarshal([]byte(msg.Payload), &payload); err != nil {
		b.log.Printf("key exchange payload parse error: %v", err)
		return
	}

	wsMsg := map[string]interface{}{
		"type":        "key_exchange",
		"peer_id":     msg.Sender,
		"peer_nick":   payload.Nickname,
		"public_key":  payload.PublicKeyHex,
		"ts":          msg.Timestamp,
	}

	data, err := json.Marshal(wsMsg)
	if err != nil {
		b.log.Printf("key exchange marshal error: %v", err)
		return
	}
	b.broadcast(data)
}

// --- Client I/O ---

// unregisterClient removes a client from the bridge and closes its send
// channel. Safe to call multiple times: only the caller that removed the
// client from the map closes the channel, so it is never closed twice.
func (b *Bridge) unregisterClient(client *Client) {
	b.clientsMu.Lock()
	defer b.clientsMu.Unlock()
	if _, ok := b.clients[client]; ok {
		delete(b.clients, client)
		close(client.send)
	}
}

// readPump reads messages from the WebSocket connection and forwards them
// to the mesh network. It runs in its own goroutine per client.
func (c *Client) readPump() {
	defer func() {
		c.bridge.unregisterClient(c)
		c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(
				err,
				websocket.CloseGoingAway,
				websocket.CloseNormalClosure,
				websocket.CloseNoStatusReceived,
			) {
				c.bridge.log.Printf("WebSocket read error: %v", err)
			}
			break
		}

		c.handleIncoming(data)
	}
}

// writePump writes messages from the send channel to the WebSocket connection.
// It also handles ping/pong keep-alive. It runs in its own goroutine per client.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case data, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// Channel was closed; send close frame
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			c.mu.Lock()
			err := c.conn.WriteMessage(websocket.TextMessage, data)
			c.mu.Unlock()
			if err != nil {
				c.bridge.log.Printf("WebSocket write error: %v", err)
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			c.mu.Lock()
			err := c.conn.WriteMessage(websocket.PingMessage, nil)
			c.mu.Unlock()
			if err != nil {
				return
			}

		case <-c.bridge.closeCh:
			// Bridge is shutting down; close cleanly
			c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(
				websocket.CloseNormalClosure, "server shutting down",
			))
			return
		}
	}
}

// handleIncoming processes a single message received from a WebSocket client.
func (c *Client) handleIncoming(data []byte) {
	var incoming wsIncoming
	if err := json.Unmarshal(data, &incoming); err != nil {
		c.bridge.log.Printf("invalid WS message (malformed JSON): %v", err)
		c.sendError("malformed message")
		return
	}

	switch incoming.Type {
	case "chat":
		c.handleIncomingChat(incoming)
	case "ping":
		// Respond to client pings with a pong
		c.sendPong()
	default:
		c.bridge.log.Printf("unknown WS message type: %s", incoming.Type)
		c.sendError(fmt.Sprintf("unknown message type: %s", incoming.Type))
	}
}

// handleIncomingChat processes a chat message from a WebSocket client and
// forwards it into the mesh network. Encrypts if we have the recipient's key.
func (c *Client) handleIncomingChat(incoming wsIncoming) {
	// Use the provided ID and timestamp, but set sender from the local node
	// to prevent spoofing. The Flutter app should send the local peer ID,
	// but we enforce it server-side.
	msg := &message.Message{
		ID:         incoming.ID,
		Type:       message.TypeChat,
		Sender:     c.bridge.peerID,
		SenderNick: c.bridge.nickname,
		Recipient:  incoming.Recipient,
		Payload:    incoming.Payload,
		Timestamp:  incoming.Timestamp,
		TTL:        16,
		HopCount:   0,
	}

	// If the client didn't provide a timestamp, use the current time
	if msg.Timestamp == 0 {
		msg.Timestamp = time.Now().UnixNano()
	}

	// If the client didn't provide an ID, NewChat would generate one,
	// but we create the struct directly so validate it's non-empty
	if msg.ID == "" {
		msg = message.NewChat(c.bridge.peerID, c.bridge.nickname, incoming.Recipient, incoming.Payload)
	}

	// Try to encrypt if we have E2E manager and recipient's public key
	if c.bridge.e2eManager != nil && incoming.Recipient != "" {
		if err := c.bridge.e2eManager.EncryptMessage(msg); err != nil {
			// Log but don't fail - fall back to unencrypted
			c.bridge.log.Printf("⚠️  E2E encrypt failed for %s, sending unencrypted: %v", incoming.Recipient, err)
		}
	}

	if err := c.bridge.node.SendMessage(msg); err != nil {
		c.bridge.log.Printf("send message error: %v", err)
		c.sendError(fmt.Sprintf("send failed: %v", err))
		return
	}
}

// sendError sends an error message back to this specific client.
func (c *Client) sendError(text string) {
	errMsg := map[string]string{
		"type":    "error",
		"payload": text,
	}
	data, err := json.Marshal(errMsg)
	if err != nil {
		return
	}
	select {
	case c.send <- data:
	default:
	}
}

// sendPong responds to a client ping message.
func (c *Client) sendPong() {
	pong := map[string]string{
		"type": "pong",
	}
	data, err := json.Marshal(pong)
	if err != nil {
		return
	}
	select {
	case c.send <- data:
	default:
	}
}

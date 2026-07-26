// rippled is the Ripple peer-to-peer mesh messaging daemon.
//
// Usage:
//
//	rippled                    # Start with default settings (random port)
//	rippled -port 9000         # Start on a specific port
//	rippled -nick alice        # Set display name
//	rippled -peers /ip4/...    # Connect to known peers
//	rippled -debug             # Verbose logging
//
// Once running, type your message and press Enter to broadcast to all
// peers in the mesh. Type /help for available commands.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/config"
	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/crypto"
	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/filetransfer"
	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/identity"
	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/mesh"
	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/message"
	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/sos"
	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/store"
	"github.com/shashank-tomar0/Ripple/go-daemon/pkg/wsbridge"
)

const (
	banner = `
╔══════════════════════════════════════╗
║          R I P P L E                ║
║    Offline Mesh Messenger v0.1      ║
║    Messages ripple outward —        ║
║    no internet needed               ║
╚══════════════════════════════════════╝
`
	helpText = `
Available Commands:
  /help          Show this help
  /me            Show your peer ID
  /peers         List connected peers
  /msg <id>      Send DM to a peer (by short ID)
  /nick <name>   Change display name
  /addrs         Show your multiaddresses
  /connect <addr> Connect to a specific peer
  /clear         Clear screen
  /stats         Show mesh stats
  /quit          Exit

Any other text is broadcast to all peers in the mesh.
`
)

var (
	node     *mesh.Node
	msgStore *store.Store
	nickname string
	peerID   string
)

func main() {
	cfg := config.Parse()

	fmt.Print(banner)
	fmt.Printf("🔐 Starting Ripple...\n")

	// Load or create identity
	id, err := identity.LoadOrCreate(cfg.IdentityPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Identity error: %v\n", err)
		os.Exit(1)
	}
	peerID = id.PeerID.String()
	nickname = cfg.Nickname
	fmt.Printf("🔑 Identity: %s (%s)\n", id.ShortID(), cfg.IdentityPath)

	// Verify identity works
	if !id.Verify() {
		fmt.Fprintf(os.Stderr, "❌ Identity verification failed\n")
		os.Exit(1)
	}

	// Create message store (in-memory or SQLite-backed)
	if cfg.DBPath != "" {
		var err error
		msgStore, err = store.NewWithDB(cfg.DBPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  Could not open message database: %v\n", err)
			fmt.Println("   Falling back to in-memory store")
			msgStore = store.New()
		} else {
			fmt.Printf("🗄️  Message store: %s\n", cfg.DBPath)
		}
	} else {
		msgStore = store.New()
	}

	// Create mesh node
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	n, err := mesh.NewNode(ctx, id.PrivKey, cfg.ListenPort,
		mesh.WithDebug(cfg.Debug),
		mesh.WithNickname(nickname),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Mesh error: %v\n", err)
		os.Exit(1)
	}
	node = n

	// Initialize E2E encryption manager
	e2eManager, err := crypto.NewManager(cfg.DataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  E2E encryption unavailable: %v\n", err)
		fmt.Println("   Messages will be sent without encryption.")
		e2eManager = nil
	} else {
		fmt.Printf("🔐 E2E encryption ready (key: %s…)\n", e2eManager.MyKeyID())
	}

	// Set up message handler
	n.OnMessage = handleIncomingMessage
	n.OnPeerJoin = handlePeerJoin
	n.OnPeerLeave = handlePeerLeave

	// Start the mesh
	if err := n.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Start error: %v\n", err)
		os.Exit(1)
	}

	// Connect to any specified relay peers
	for _, p := range cfg.RelayPeers {
		fmt.Printf("🔗 Connecting to %s...\n", p)
		if err := n.ConnectToPeer(p); err != nil {
			fmt.Printf("⚠️  Could not connect to %s: %v\n", p, err)
		}
	}

	// Print network info
	fmt.Printf("🌐 Mesh peer ID:  %s\n", id.PeerID.String())
	fmt.Printf("🌐 Nickname:      %s\n", nickname)
	for _, addr := range n.AddrList() {
		fmt.Printf("   📍 %s\n", addr)
	}
	fmt.Println()

	// Start WebSocket bridge for Flutter app connectivity
	bridge := wsbridge.NewBridge(n, id.PeerID.String(), nickname, cfg.WSPort, e2eManager)
	if err := bridge.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ WebSocket bridge error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("🔌 WebSocket bridge: ws://localhost:%d/ws\n", cfg.WSPort)
	fmt.Println()

	// Start file transfer manager
	ftManager := filetransfer.NewManager(n.Host, cfg.DataDir)
	ftManager.OnFileNotification = func(msg *message.Message) {
		bridge.BroadcastFileNotification(msg)
	}
	ftManager.Start(ctx)
	fmt.Printf("📁 File transfer manager ready (chunk size: %dKB)\n", filetransfer.DefaultChunkSize/1024)
	fmt.Println()

	// Start SOS emergency broadcast manager
	sosManager := sos.NewManager(n)
	sosManager.OnSOSReceived = func(alert *sos.ActiveAlert) {
		bridge.BroadcastSOS(alert.Message)
	}
	sosManager.OnSOSExpired = func(alertID string) {
		if n.Debug {
			n.Log.Printf("🚨 SOS alert expired: %s", alertID[:8])
		}
	}
	sosManager.Start(ctx)
	fmt.Printf("🚨 SOS emergency broadcast manager ready (TTL=%d, active=%v)\n", sos.MaxTTL, sos.ActiveDuration)
	fmt.Println()

	fmt.Printf("✅ Ripple is running! Type /help for commands.\n\n")

	// Handle shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Start CLI input reader
	go handleInput()

	// Wait for shutdown signal
	<-sigCh
	fmt.Println("\n\n👋 Shutting down Ripple...")
	n.Close()
	if msgStore != nil {
		msgStore.Close()
	}
	fmt.Println("Goodbye!")
}

// handleInput reads commands from stdin and processes them.
func handleInput() {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "/") {
			handleCommand(line)
		} else {
			broadcastMessage(line)
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "Input error: %v\n", err)
	}
}

// handleCommand processes slash commands.
func handleCommand(cmd string) {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return
	}

	switch parts[0] {
	case "/help":
		fmt.Print(helpText)

	case "/me":
		fmt.Printf("Your identity:\n")
		fmt.Printf("  PeerID:   %s\n", peerID)
		fmt.Printf("  Nickname: %s\n", nickname)

	case "/peers":
		peers := node.KnownPeers()
		if len(peers) == 0 {
			fmt.Println("🌑 No peers connected. Your mesh is empty.")
			fmt.Println("   Share your address (use /addrs) or connect to others (/connect).")
			return
		}
		fmt.Printf("📡 Connected peers (%d):\n", len(peers))
		for _, p := range peers {
			fmt.Printf("  • %s\n", p.ShortString())
		}

	case "/addrs":
		fmt.Println("📍 Your multiaddresses (share these with others):")
		for _, addr := range node.AddrList() {
			fmt.Printf("  %s\n", addr)
		}

	case "/nick":
		if len(parts) < 2 {
			fmt.Println("Usage: /nick <name>")
			return
		}
		nickname = strings.Join(parts[1:], " ")
		fmt.Printf("✅ Nickname changed to: %s\n", nickname)

	case "/connect":
		if len(parts) < 2 {
			fmt.Println("Usage: /connect <multiaddr>")
			return
		}
		addr := parts[1]
		fmt.Printf("🔗 Connecting to %s...\n", addr)
		if err := node.ConnectToPeer(addr); err != nil {
			fmt.Printf("❌ Connection failed: %v\n", err)
		}

	case "/msg":
		if len(parts) < 3 {
			fmt.Println("Usage: /msg <short-peer-id> <message>")
			return
		}
		targetShort := parts[1]
		text := strings.Join(parts[2:], " ")

		// Find peer with matching short ID
		peers := node.KnownPeers()
		var targetID string
		for _, p := range peers {
			if strings.HasPrefix(p.ShortString(), targetShort) {
				targetID = p.String()
				break
			}
		}
		if targetID == "" {
			fmt.Printf("❌ No peer found with ID starting with '%s'\n", targetShort)
			return
		}

		msg := message.NewChat(peerID, nickname, targetID, text)
		if err := node.SendMessage(msg); err != nil {
			fmt.Printf("❌ Send error: %v\n", err)
			return
		}
		msgStore.SaveMessage(msg)
		fmt.Printf("📤 [DM to %s] %s\n", targetShort, text)

	case "/stats":
		stats()

	case "/clear":
		fmt.Print("\033[H\033[2J")

	case "/quit":
		fmt.Println("👋 Shutting down...")
		os.Exit(0)

	default:
		fmt.Printf("Unknown command: %s (type /help)\n", parts[0])
	}
}

// broadcastMessage sends a chat message to all peers in the mesh.
func broadcastMessage(text string) {
	msg := message.NewChat(peerID, nickname, "", text)
	if err := node.SendMessage(msg); err != nil {
		fmt.Printf("❌ Send error: %v\n", err)
		return
	}
	msgStore.SaveMessage(msg)

	// Show sent confirmation
	peers := node.KnownPeers()
	if len(peers) == 0 {
		fmt.Printf("📤 [you → 🌑 mesh (alone)] %s\n", text)
	} else {
		fmt.Printf("📤 [you → %d peers] %s\n", len(peers), text)
	}
}

// handleIncomingMessage is called when the mesh receives a message.
func handleIncomingMessage(msg *message.Message) {
	// Dedup: skip already-seen messages
	if msgStore.HasMessage(msg.ID) {
		return
	}

	msgStore.SaveMessage(msg)

	// Format display
	sender := msg.SenderNick
	if sender == "" {
		sender = msg.Sender
	}
	if len(sender) > 16 {
		sender = sender[:16] + "…"
	}

	// Calculate delay
	delay := time.Duration(0)
	if msg.Timestamp > 0 {
		delay = time.Duration(time.Now().UnixNano() - msg.Timestamp)
	}

	// Show the message
	hops := ""
	if msg.HopCount > 0 {
		hops = fmt.Sprintf(" [%d hop(s)]", msg.HopCount)
	}
	delayStr := ""
	if delay > time.Second {
		delayStr = fmt.Sprintf(" (%s delayed)", delay.Round(time.Second))
	}

	switch msg.Type {
	case message.TypeChat:
		if msg.Recipient != "" {
			fmt.Printf("\n📩 [DM from %s]%s%s: %s\n", sender, hops, delayStr, msg.Payload)
		} else {
			fmt.Printf("\n📩 [%s]%s%s: %s\n", sender, hops, delayStr, msg.Payload)
		}
	case message.TypeDeliveryAck:
		fmt.Printf("\n✅ Delivery confirmed for message %s\n", msg.Payload)
	case message.TypeSOS:
		fmt.Printf("\n🚨 [SOS from %s]%s: %s\n", sender, hops, msg.Payload)
	default:
		fmt.Printf("\n📩 [%s type=%s]: %s\n", sender, msg.Type, msg.Payload)
	}

	// Print prompt re-prompt
	fmt.Print("> ")
}

// handlePeerJoin is called when a new peer connects.
func handlePeerJoin(pid peer.ID) {
	fmt.Printf("\n🔗 Peer connected: %s\n", pid.ShortString())
	fmt.Print("> ")
}

// handlePeerLeave is called when a peer disconnects.
func handlePeerLeave(pid peer.ID) {
	fmt.Printf("\n🔗 Peer disconnected: %s\n", pid.ShortString())
	fmt.Print("> ")
}

// stats prints mesh statistics.
func stats() {
	peers := node.KnownPeers()
	msgs := msgStore.Messages()

	fmt.Println("📊 Mesh Statistics:")
	fmt.Printf("  Connected peers: %d\n", len(peers))
	fmt.Printf("  Messages sent/received: %d\n", len(msgs))

	// Count by type
	chatCount := 0
	for _, m := range msgs {
		if m.Type == message.TypeChat {
			chatCount++
		}
	}
	fmt.Printf("  Chat messages: %d\n", chatCount)

	// Uptime
	fmt.Printf("  Peer ID: %s\n", peerID)
	fmt.Printf("  Nickname: %s\n", nickname)

	if len(peers) > 0 {
		fmt.Println("  Peers:")
		for _, p := range peers {
			fmt.Printf("    • %s\n", p.ShortString())
		}
	}
}

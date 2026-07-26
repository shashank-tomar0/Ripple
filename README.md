# 🌊 Ripple — Offline Mesh Messenger

**Ripple** is a peer-to-peer messaging app that works without internet, cellular towers, or any central infrastructure. Messages ripple outward through nearby devices, creating a mesh network that grows with every peer.

```
Messages travel device → device → device
No internet. No SIM. No servers.
Your phone becomes a relay for the community.
```

---

## 🚀 Quick Start

### Prerequisites
- Go 1.26+ (for the daemon)
- A laptop with WiFi (for Phase 0 local testing)

### Run

```bash
# Clone and build
git clone https://github.com/shashank-tomar0/Ripple.git
cd Ripple/go-daemon
go build -o ripple ./cmd/rippled

# Start a node
./rippled -nick alice

# On another machine (or another terminal)
./rippled -nick bob -port 9001
```

Once both are running on the same WiFi network, mDNS automatically discovers peers. Type a message and press Enter — it broadcasts to all connected peers.

### Commands

| Command | Description |
|---|---|
| `/help` | Show all commands |
| `/me` | Show your PeerID and nickname |
| `/peers` | List connected peers |
| `/msg <short-id> <text>` | Send a direct message |
| `/addrs` | Show your multiaddresses |
| `/connect <addr>` | Connect to a specific peer |
| `/nick <name>` | Change your display name |
| `/stats` | Show mesh statistics |
| `/quit` | Exit |

---

## 🏗️ Architecture

```
┌──────────────────────────────────────────┐
│           Ripple Architecture            │
├──────────────────────────────────────────┤
│                                          │
│  ┌────────────────────────────────────┐  │
│  │         Flutter App (Phase 1)      │  │
│  │  ┌──────┐ ┌──────┐ ┌───────────┐  │  │
│  │  │ Chat │ │Contacts│ │ Mesh Map  │  │  │
│  │  └──┬───┘ └──┬───┘ └─────┬─────┘  │  │
│  │     └─────┬──┴───────────┘        │  │
│  │           │ gRPC                  │  │
│  └───────────┼────────────────────────┘  │
│              │                            │
│  ┌───────────▼────────────────────────┐  │
│  │         Go Daemon (libp2p)         │  │
│  │                                    │  │
│  │  ┌────────┐ ┌────────┐ ┌────────┐  │  │
│  │  │ Identity│ │ Mesh   │ │ Store  │  │  │
│  │  │ (Ed25519)│ │ (libp2p)│ │(msgs) │  │  │
│  │  └────────┘ └────────┘ └────────┘  │  │
│  │                                    │  │
│  │  ┌──────────────────────────────┐  │  │
│  │  │  Transports: TCP │ BLE │ WiFi│  │  │
│  │  └──────────────────────────────┘  │  │
│  └────────────────────────────────────┘  │
│                                          │
└──────────────────────────────────────────┘
```

### Phase 0 (Current)

- ✅ TCP transport over libp2p
- ✅ Ed25519 cryptographic identity
- ✅ mDNS local peer discovery
- ✅ GossipSub pubsub messaging
- ✅ Direct peer-to-peer streams
- ✅ Store-and-forward message relay
- ✅ Terminal chat UI

### Phase 1 (Coming)

- [ ] BLE transport for device-to-device
- [ ] Cross-platform Flutter UI
- [ ] SQLite message persistence
- [ ] E2E encryption (X3DH ratchet)
- [ ] QR code contact exchange

### Phase 2

- [ ] WiFi Direct for file transfer
- [ ] Mesh routing visualization
- [ ] Background mode (Android foreground service)
- [ ] File chunking + resume

---

## 🧪 Testing the Mesh

### Single machine (two terminals):

```bash
# Terminal 1
cd Ripple/go-daemon
go run ./cmd/rippled -nick alice -port 9000

# Terminal 2
go run ./cmd/rippled -nick bob -port 9001
```

Both discover each other via mDNS within seconds. Messages typed in one appear in the other.

### Multiple machines (same WiFi):

```bash
# Machine 1
go run ./cmd/rippled -nick alice

# Machine 2 — connect directly
go run ./cmd/rippled -nick bob \
  -peers /ip4/192.168.1.100/tcp/9000/p2p/<Machine1PeerID>
```

To get the peer ID and address, use `/addrs` on Machine 1.

---

## 🔐 Security Model

| Property | How |
|---|---|
| **Identity** | Ed25519 keypair generated on first launch |
| **No accounts** | No phone number, email, or central registry |
| **Message signing** | All pubsub messages signed with private key |
| **E2E encryption** | Coming in Phase 1 (X3DH double ratchet) |

---

## 📁 Project Structure

```
Ripple/
├── go-daemon/              # Go networking core
│   ├── cmd/rippled/        # CLI entry point
│   └── pkg/
│       ├── identity/       # Ed25519 key management
│       ├── config/         # CLI flags
│       ├── mesh/           # libp2p networking
│       ├── message/        # Message types
│       └── store/          # Message storage
├── flutter-app/            # Cross-platform UI (Phase 1)
├── protos/                 # Protocol Buffers
└── docs/                   # Documentation
```

---

## 📜 License

MIT

---

> **Built with** [libp2p](https://libp2p.io/) — the modular peer-to-peer networking stack.

# Ripple Architecture

## Overview

Ripple is a peer-to-peer mesh messaging application. Messages travel directly between devices without any central server or internet connection. Each device acts as both a client (sending/receiving messages) and a relay (forwarding messages for other devices).

## Core Principles

1. **No central infrastructure** — No servers, no cloud, no SIM card required
2. **Privacy by design** — Identity is a cryptographic keypair, not a phone number
3. **Offline-first** — Works with or without internet connectivity
4. **Mesh architecture** — Every peer relays messages, extending network range
5. **Store-and-forward** — Messages are buffered and delivered when peers reconnect

## Network Stack

```
┌─────────────────────────────────────────────┐
│              Application Layer               │
│         Ripple Protocol (JSON/Protobuf)       │
├─────────────────────────────────────────────┤
│              PubSub Layer                    │
│             GossipSub (libp2p)               │
├─────────────────────────────────────────────┤
│           Peer Discovery Layer               │
│      mDNS (local) │ DHT (wide area)          │
├─────────────────────────────────────────────┤
│           Transport Layer                    │
│  TCP │ BLE │ WiFi Direct │ LAN (Phase 1+)   │
├─────────────────────────────────────────────┤
│            Network Layer                     │
│    libp2p (NAT traversal, streams, etc.)     │
└─────────────────────────────────────────────┘
```

## Phase 0 Design Decisions

### Why libp2p?

libp2p provides a complete peer-to-peer networking stack out of the box:

- **Transport abstraction** — Switch between TCP, BLE, WiFi Direct without changing application code
- **Peer identity** — Built-in key management and PeerID derivation
- **mDNS discovery** — Zero-configuration peer discovery on local networks
- **GossipSub** — Efficient pubsub messaging for mesh broadcasting
- **Stream multiplexing** — Multiple protocol streams over a single connection
- **NAT traversal** — AutoNAT, relay, and hole-punching for complex network topologies

### Why JSON for Phase 0?

Messages use JSON serialization in Phase 0 for debugging simplicity. Phase 1+ will migrate to Protocol Buffers for:

- Smaller message sizes (critical over BLE's limited bandwidth)
- Schema enforcement and backwards compatibility
- Generated code for cross-platform (Go + Dart)

### Store-and-Forward

Every peer that receives a message with TTL > 0:

1. Records the message ID in a bloom filter (dedup)
2. Decrements TTL
3. Republishes to the GossipSub topic
4. If TTL reaches 0, the message is consumed but not relayed

This creates a controlled flood fill through the mesh. Duplicate suppression prevents broadcast storms.

## Message Flow

```
Alice                          Bob                          Carol
  │                             │                            │
  │─── Chat Message ──────────►│                            │
  │    (Direct Stream)         │                            │
  │                             │                            │
  │                             │─── Relay Message ────────►│
  │                             │    (GossipSub)            │
  │                             │                            │
  │                             │                            │
  │◄─── Delivery Ack ──────────│                            │
  │    (Direct Stream)         │                            │
  │                             │                            │
```

### Detailed Flow

1. **Alice sends a message** → The message is serialized with a unique ID, timestamp, sender, recipient (optional), and TTL

2. **Direct delivery attempt** → If Alice knows Bob's PeerID and has a direct connection, the message is sent via a direct libp2p stream

3. **Mesh broadcast** → If direct delivery fails (Bob is not directly connected), the message is published to the GossipSub topic

4. **Relay propagation** → Every peer in the topic receives the message, checks the dedup bloom filter, decrements TTL, and re-publishes

5. **Bob receives** → Bob's pubsub subscription delivers the message. The application layer checks if Bob is the intended recipient (or if it's a broadcast)

6. **Delivery confirmation** → Bob sends a delivery acknowledgment back through the mesh

## Identity System

### Key Generation

```
First launch:
  1. Generate Ed25519 keypair (crypto/rand)
  2. Derive PeerID = hash(public_key)
  3. Store private key in ~/.ripple/identity.pem (PEM-encoded)
  4. Every subsequent launch loads from this file
```

### Contact Exchange (Coming Phase 1)

```
  1. Alice displays a QR code containing: PeerID + public key + nickname
  2. Bob scans the QR code with his phone
  3. Bob's app stores Alice's public key for E2E encryption
  4. Bob sends an encrypted introduction message to Alice
```

## Troubleshooting

### Peers not discovering each other

1. Both machines must be on the same subnet (mDNS doesn't cross routers)
2. Check that no firewall is blocking TCP connections
3. Verify with `/addrs` on both nodes — they should show TCP addresses
4. Try manual connection: `/connect /ip4/<IP>/tcp/<PORT>/p2p/<PEERID>`

### Messages not being received

1. Check `/peers` — if no peers are listed, the mesh isn't connected
2. Verify the network allows UDP multicast (required for mDNS)
3. Check the terminal — messages appear above the input prompt

## References

- [libp2p documentation](https://docs.libp2p.io/)
- [GossipSub specification](https://github.com/libp2p/specs/tree/master/pubsub/gossipsub)
- [mDNS specification (RFC 6762)](https://datatracker.ietf.org/doc/html/rfc6762)

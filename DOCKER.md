# Ripple Docker Compose — 3-Node Mesh Testing

Quick way to test a 3-node Ripple mesh locally.

## Prerequisites

- Docker + Docker Compose
- (Optional) `websocat` or `wscat` for WebSocket testing

## Quick Start

```bash
# Start the 3-node mesh
docker compose up -d

# Watch logs (shows peer discovery and message relay)
docker compose logs -f

# Stop and cleanup
docker compose down -v
```

## Port Mapping

| Node | libp2p TCP | WebSocket Bridge | Nickname |
|------|------------|------------------|----------|
| node1 | 9001 | 9871 | `node1` |
| node2 | 9002 | 9872 | `node2` |
| node3 | 9003 | 9873 | `node3` |

## Testing Mesh Communication

### Option 1: WebSocket (using `websocat`)

```bash
# Terminal 1: connect to node1 WebSocket
websocat ws://localhost:9871/ws

# Terminal 2: connect to node2 WebSocket
websocat ws://localhost:9872/ws

# In Terminal 1, send a chat message:
{"type":"chat","sender":"12D3...node1...","sender_nick":"node1","recipient":"","payload":"hello from node1","ts":1700000000000000000,"ttl":16,"hops":0}

# Terminal 2 should receive it and display it
```

### Option 2: Flutter App

Run the Flutter app and point it at one of the nodes:

```bash
cd flutter-app
flutter run --dart-define=WS_HOST=localhost --dart-define=WS_PORT=9871
```

### Option 3: CLI

```bash
# Run the Go CLI against a node
go run ./go-daemon/cmd/rippled -port 9001 -nick alice -peers /ip4/127.0.0.1/tcp/9000/p2p/<node1-peer-id>
```

## What to Verify

1. **Peer discovery** — logs should show `mDNS discovery started` and `discovered peer via mDNS`
2. **Connection** — `connected to <peer-id>` should appear within ~10s
3. **Message relay** — a message sent from node1 should appear on node2 and node3
4. **File transfer** — send a file from node1, verify node2 receives progress events
5. **SOS broadcast** — trigger an SOS from node1, verify all nodes receive it

## Troubleshooting

**Nodes not discovering each other:**
- Check `docker compose logs` for `mDNS start error`
- On Linux, mDNS requires `avahi-daemon` or host networking mode
- Try `--network=host` mode instead of bridge (requires root)

**WebSocket connection refused:**
- Verify the bridge started: logs should show `WebSocket bridge: ws://0.0.0.0:9876/ws`
- Check port mapping in docker-compose.yml

**Identity/DB errors:**
- The `-db` flag points to `/data/ripple.db` in the volume
- `docker compose down -v` removes volumes for a clean slate

## Cleanup

```bash
docker compose down -v  # removes containers and volumes
```
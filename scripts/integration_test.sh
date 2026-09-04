#!/usr/bin/env bash
# Ripple integration test — REAL end-to-end mesh delivery, no mocks.
#
# Topology: a linear chain node1 ↔ node2 ↔ node3 with mDNS disabled.
# node1 and node3 are never directly connected, so the ONLY way a message
# from node1 reaches node3 is store-and-forward relay through node2.
#
# What this proves:
#   1. A broadcast from node1 arrives at node3 through node2's relay, and
#      node2's WebSocket bridge emits factual `relay` events: received
#      (from node1) then forwarded.
#   2. A DM from node1 addressed to node3 (a peer it is NOT connected to)
#      is routed through the mesh, node3 auto-acks it, and the `received`
#      delivery receipt travels back to node1.
#
# Every assertion greps real process output (logs and bridge frames).
# Exit code is non-zero if any step fails.
set -euo pipefail

# Git Bash on Windows converts /ip4/... arguments into Windows paths
# (C:/ProgramFiles/Git/ip4/...). Disable that so multiaddrs survive
# untouched. Harmless no-op on Linux/macOS.
export MSYS_NO_PATHCONV=1

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_DIR="$ROOT/go-daemon"
WORK="$(mktemp -d)"
# Native tools (go, rippled) need a Windows path on Git Bash; convert it.
# On Linux/macOS cygpath does not exist and WORK is already native.
WORK_NATIVE="$(cygpath -w "$WORK" 2>/dev/null || echo "$WORK")"
NODE1_PID="" NODE2_PID="" NODE3_PID="" NODE4_PID="" PROBE_PID=""

cleanup() {
  [ -n "$PROBE_PID" ] && kill $PROBE_PID 2>/dev/null || true
  # Signal the daemons, then WAIT for them to exit and flush before
  # removing their data dirs — otherwise rm can race a still-running node
  # that is writing into its directory (a real CI failure we hit).
  for p in "$NODE1_PID" "$NODE2_PID" "$NODE3_PID" "$NODE4_PID"; do
    [ -n "$p" ] && kill "$p" 2>/dev/null || true
  done
  for _ in 1 2 3 4 5; do
    alive=""
    for p in "$NODE1_PID" "$NODE2_PID" "$NODE3_PID" "$NODE4_PID"; do
      [ -n "$p" ] && kill -0 "$p" 2>/dev/null && alive="$alive $p"
    done
    [ -n "$alive" ] || break
    sleep 1
  done
  for p in "$NODE1_PID" "$NODE2_PID" "$NODE3_PID" "$NODE4_PID"; do
    [ -n "$p" ] && kill -9 "$p" 2>/dev/null || true
  done
  rm -rf "$WORK" 2>/dev/null || { sleep 1; rm -rf "$WORK" 2>/dev/null || true; }
}
trap cleanup EXIT

fail() { echo "❌ $1"; exit 1; }
pass() { echo "✅ $1"; }

echo "═══ Ripple integration test — 3-node relay chain ═══"
echo "workdir: $WORK"

# ── Build ──────────────────────────────────────────────────────────────
# Go appends .exe on Windows (Git Bash / MSYS) but not on Linux/macOS.
case "$(uname -s)" in
  MINGW*|MSYS*|CYGWIN*) EXE=".exe" ;;
  *) EXE="" ;;
esac
(cd "$GO_DIR" && go build -o "$WORK_NATIVE/rippled$EXE" ./cmd/rippled)
(cd "$GO_DIR" && go build -o "$WORK_NATIVE/wsprobe$EXE" ./integration/wsprobe)
RIPPLED="$WORK_NATIVE/rippled$EXE"
WSPROBE="$WORK_NATIVE/wsprobe$EXE"
DATA1="$WORK_NATIVE/1"; DATA2="$WORK_NATIVE/2"; DATA3="$WORK_NATIVE/3"
pass "built rippled + wsprobe"

P1=19401; P2=19402; P3=19403
W1=19871; W2=19872; W3=19873

# ── Start node1 (no bootstrap; the chain head) ─────────────────────────
"$RIPPLED" -port $P1 -wsport $W1 -data "$DATA1" -nick node1 -debug -nomdns \
  >"$WORK/node1.log" 2>&1 &
NODE1_PID=$!
for i in $(seq 1 30); do
  grep -q "Mesh peer ID" "$WORK/node1.log" && break
  [ "$i" -eq 30 ] && fail "node1 never started: $(tail -20 "$WORK/node1.log")"
  sleep 1
done
PEER1=$(grep -oE 'Mesh peer ID:[[:space:]]+[0-9A-Za-z]+' "$WORK/node1.log" | head -1 | sed -E 's/.*:[[:space:]]+//')
[ -n "$PEER1" ] || fail "could not parse node1 peer ID"
pass "node1 up: $PEER1"

# ── Start node2, bootstrapping to node1 ────────────────────────────────
"$RIPPLED" -port $P2 -wsport $W2 -data "$DATA2" -nick node2 -debug -nomdns \
  -peers "/ip4/127.0.0.1/tcp/$P1/p2p/$PEER1" >"$WORK/node2.log" 2>&1 &
NODE2_PID=$!
for i in $(seq 1 30); do
  grep -q "Peer connected" "$WORK/node2.log" && break
  [ "$i" -eq 30 ] && fail "node2 never joined: $(tail -20 "$WORK/node2.log")"
  sleep 1
done
grep -q "Mesh peer ID" "$WORK/node2.log" || fail "node2 never announced identity"
PEER2=$(grep -oE 'Mesh peer ID:[[:space:]]+[0-9A-Za-z]+' "$WORK/node2.log" | head -1 | sed -E 's/.*:[[:space:]]+//')
pass "node2 up: $PEER2 (dialed node1)"

# ── Start node3, bootstrapping to node2 (NOT node1 — forced relay path) ─
"$RIPPLED" -port $P3 -wsport $W3 -data "$DATA3" -nick node3 -debug -nomdns \
  -peers "/ip4/127.0.0.1/tcp/$P2/p2p/$PEER2" >"$WORK/node3.log" 2>&1 &
NODE3_PID=$!
for i in $(seq 1 30); do
  grep -q "Peer connected" "$WORK/node3.log" && break
  [ "$i" -eq 30 ] && fail "node3 never joined: $(tail -20 "$WORK/node3.log")"
  sleep 1
done
grep -q "Mesh peer ID" "$WORK/node3.log" || fail "node3 never announced identity"
PEER3=$(grep -oE 'Mesh peer ID:[[:space:]]+[0-9A-Za-z]+' "$WORK/node3.log" | head -1 | sed -E 's/.*:[[:space:]]+//')
pass "node3 up: $PEER3 (dialed node2 only — node1↔node3 are NOT connected)"

# Give GossipSub time to graft the topic mesh along the chain.
sleep 3

# ── Phase A: broadcast relayed through node2 ──────────────────────────
echo "─── Phase A: broadcast from node1 must reach node3 via node2 ───"
PAYLOAD_A="ripple-relay-a-$(date +%s)"
"$WSPROBE" -mode listen -url "ws://localhost:$W2/ws" -timeout 12s >"$WORK/node2_frames.jsonl" 2>"$WORK/probe2.err" &
PROBE_PID=$!
sleep 1  # let the probe connect and receive identity

"$WSPROBE" -mode send -url "ws://localhost:$W1/ws" -payload "$PAYLOAD_A" -timeout 3s \
  || fail "send phase A failed"
wait "$PROBE_PID"; PROBE_PID=""
# The listen probe exits non-zero only on a premature read error.
grep -qF "listen finished" "$WORK/node2_frames.jsonl" || fail "node2 probe failed: $(cat "$WORK/probe2.err")"

# node3's log must contain the payload (it can only have arrived via node2).
grep -qF "$PAYLOAD_A" "$WORK/node3.log" || {
  echo "--- node3 log ---"; tail -30 "$WORK/node3.log"; fail "payload A NOT delivered to node3"
}
pass "payload A delivered to node3 (through node2)"

# node2's bridge must have relay frames: received then forwarded, from node1.
# First find the message id from the chat frame that carries the payload.
MSG_A=$(grep -F "$PAYLOAD_A" "$WORK/node2_frames.jsonl" \
  | grep -oE '"id":"[0-9a-f]+"' | head -1 | sed -E 's/.*:"//;s/"//')
[ -n "$MSG_A" ] || fail "could not find message id for payload A in node2 frames"
pass "message id for payload A: $MSG_A"

RELAY_A=$(grep -F "$MSG_A" "$WORK/node2_frames.jsonl" | grep '"type":"relay"' || true)
echo "$RELAY_A" | grep -q '"action":"received"' || fail "node2 never reported received for $MSG_A"
echo "$RELAY_A" | grep -q '"action":"forwarded"' || fail "node2 never reported forwarded for $MSG_A"
echo "$RELAY_A" | grep -qF "\"from\":\"$PEER1\"" || fail "relay events for $MSG_A not attributed to node1"
pass "node2 emitted received+forwarded relay events for $MSG_A (from node1)"

# ── Phase B: DM to an unreachable peer + delivery receipt round-trip ──
echo "─── Phase B: DM node1→node3 routes via node2 and comes back acked ───"
PAYLOAD_B="ripple-dm-b-$(date +%s)"
"$WSPROBE" -mode listen -url "ws://localhost:$W1/ws" -timeout 12s >"$WORK/node1_frames.jsonl" 2>"$WORK/probe1.err" &
PROBE_PID=$!
sleep 1

"$WSPROBE" -mode send -url "ws://localhost:$W1/ws" -payload "$PAYLOAD_B" -recipient "$PEER3" -timeout 3s \
  || fail "send phase B failed"
wait "$PROBE_PID"; PROBE_PID=""
grep -qF "listen finished" "$WORK/node1_frames.jsonl" || fail "node1 probe failed: $(cat "$WORK/probe1.err")"

# node3 must have received the DM (through node2 — node1 cannot dial node3).
grep -qF "$PAYLOAD_B" "$WORK/node3.log" || {
  echo "--- node3 log ---"; tail -30 "$WORK/node3.log"; fail "DM B NOT delivered to node3"
}
pass "DM B delivered to node3 through the mesh"

# The auto-ack must travel back: node1's bridge shows a delivery_ack whose
# msg_id matches the DM. The DM's own chat frame echoes back through node2's
# relay, so extract its id from node1's frames.
MSG_B=$(grep -F "$PAYLOAD_B" "$WORK/node1_frames.jsonl" \
  | grep -oE '"id":"[0-9a-f]+"' | head -1 | sed -E 's/.*:"//;s/"//')
[ -n "$MSG_B" ] || fail "could not find message id for DM B in node1 frames"

ACK_B=$(grep -F "$MSG_B" "$WORK/node1_frames.jsonl" | grep '"type":"delivery_ack"' || true)
[ -n "$ACK_B" ] || fail "no delivery receipt for DM B reached node1"
echo "$ACK_B" | grep -q '"status":"received"' || fail "delivery receipt for DM B is not 'received'"
pass "delivery receipt (received) for $MSG_B travelled back to node1"

# ── Phase C: identity recovery — a lost device must be recoverable ──
echo "─── Phase C: BIP39 backup phrase restores the exact same identity ───"
PHRASE=$("$RIPPLED" -data "$DATA1" -export-seed 2>&1) \
  || fail "export-seed failed: $PHRASE"
WORDS=$(echo "$PHRASE" | wc -w | tr -d ' ')
[ "$WORDS" -eq 24 ] || fail "exported phrase has $WORDS words, want 24"

echo "📤 exported 24-word phrase for $PEER1"

DATA1R="$WORK_NATIVE/1-recovered"
RESTORED=$("$RIPPLED" -data "$DATA1R" -import-seed "$PHRASE" 2>&1) \
  || fail "import-seed failed: $RESTORED"
echo "$RESTORED" | grep -qF "$PEER1" || fail "restored identity != node1: $RESTORED"
[ -f "$DATA1R/identity.pem" ] || fail "import-seed did not write identity.pem"
pass "identity recovered from phrase: $PEER1"

# The restored identity file must export the SAME phrase (byte-identical key).
PHRASE2=$("$RIPPLED" -data "$DATA1R" -export-seed 2>&1)
[ "$PHRASE" = "$PHRASE2" ] || fail "restored identity exports a different phrase"
pass "restored identity exports the identical phrase (key round-trips byte-identically)"

# Garbage must be rejected loudly, not silently accepted.
if "$RIPPLED" -data "$WORK_NATIVE/1-bad" -import-seed "abandon abandon abandon" >/dev/null 2>&1; then
  fail "import-seed accepted a garbage phrase"
fi
pass "garbage backup phrase rejected with an error"

# ── Phase D: the app-facing bridge serves the same phrase as the CLI ──
echo "─── Phase D: seed_export over the bridge matches the CLI phrase ───"
BRIDGE_PHRASE=$("$WSPROBE" -mode seed -url "ws://localhost:$W1/ws" -timeout 5s 2>&1) \
  || fail "bridge seed_export failed: $BRIDGE_PHRASE"
BRIDGE_WORDS=$(echo "$BRIDGE_PHRASE" | wc -w | tr -d ' ')
[ "$BRIDGE_WORDS" -eq 24 ] || fail "bridge phrase has $BRIDGE_WORDS words, want 24"
[ "$BRIDGE_PHRASE" = "$PHRASE" ] || fail "bridge phrase differs from the CLI phrase"
pass "bridge seed_export serves the identical 24-word phrase as the CLI"

# ── Phase E: destination-aware routing (spray-and-wait) on the wire ──
# Fresh 4-node topology in spray mode: e1↔e2↔e3 chain plus spur e2↔e4.
# e3 is reachable only through e2; e4 is a node that epidemic flooding
# would hand a copy to, but bounded-copy routing must skip. The DM must
# deliver through the same relay path as Phase B, with the copy budget
# observable on the wire (copies=2) and the spur untouched.
echo "─── Phase E: spray routing — same delivery, bounded copies ───"
# Stop the epidemic-phase daemons first (they reuse the PID variables).
for p in "$NODE1_PID" "$NODE2_PID" "$NODE3_PID"; do
  [ -n "$p" ] && kill "$p" 2>/dev/null || true
  [ -n "$p" ] && wait "$p" 2>/dev/null || true
  [ -n "$p" ] && kill -9 "$p" 2>/dev/null || true
done
NODE1_PID=""; NODE2_PID=""; NODE3_PID=""
sleep 1

PE1=19411; PE2=19412; PE3=19413; PE4=19414
WE1=19881; WE2=19882; WE3=19883; WE4=19884
SPRAY="-routing spray -spray-budget 2"

"$RIPPLED" -port $PE1 -wsport $WE1 -data "$WORK_NATIVE/E1" -nick e1 -debug -nomdns $SPRAY \
  >"$WORK/e1.log" 2>&1 &
NODE1_PID=$!
for i in $(seq 1 30); do
  grep -q "Mesh peer ID" "$WORK/e1.log" && break
  [ "$i" -eq 30 ] && fail "e1 never started: $(tail -20 "$WORK/e1.log")"
  sleep 1
done
PEER_E1=$(grep -oE 'Mesh peer ID:[[:space:]]+[0-9A-Za-z]+' "$WORK/e1.log" | head -1 | sed -E 's/.*:[[:space:]]+//')
[ -n "$PEER_E1" ] || fail "could not parse e1 peer ID"
grep -q "spray-and-wait" "$WORK/e1.log" || fail "e1 did not enable spray routing"
pass "e1 up with spray-and-wait (L=2)"

"$RIPPLED" -port $PE2 -wsport $WE2 -data "$WORK_NATIVE/E2" -nick e2 -debug -nomdns $SPRAY \
  -peers "/ip4/127.0.0.1/tcp/$PE1/p2p/$PEER_E1" >"$WORK/e2.log" 2>&1 &
NODE2_PID=$!
for i in $(seq 1 30); do
  grep -q "Peer connected" "$WORK/e2.log" && break
  [ "$i" -eq 30 ] && fail "e2 never joined: $(tail -20 "$WORK/e2.log")"
  sleep 1
done
PEER_E2=$(grep -oE 'Mesh peer ID:[[:space:]]+[0-9A-Za-z]+' "$WORK/e2.log" | head -1 | sed -E 's/.*:[[:space:]]+//')
pass "e2 up (dialed e1)"

"$RIPPLED" -port $PE3 -wsport $WE3 -data "$WORK_NATIVE/E3" -nick e3 -debug -nomdns $SPRAY \
  -peers "/ip4/127.0.0.1/tcp/$PE2/p2p/$PEER_E2" >"$WORK/e3.log" 2>&1 &
NODE3_PID=$!
for i in $(seq 1 30); do
  grep -q "Peer connected" "$WORK/e3.log" && break
  [ "$i" -eq 30 ] && fail "e3 never joined: $(tail -20 "$WORK/e3.log")"
  sleep 1
done
PEER_E3=$(grep -oE 'Mesh peer ID:[[:space:]]+[0-9A-Za-z]+' "$WORK/e3.log" | head -1 | sed -E 's/.*:[[:space:]]+//')
pass "e3 up (dialed e2 only — e1↔e3 NOT connected)"

"$RIPPLED" -port $PE4 -wsport $WE4 -data "$WORK_NATIVE/E4" -nick e4 -debug -nomdns $SPRAY \
  -peers "/ip4/127.0.0.1/tcp/$PE2/p2p/$PEER_E2" >"$WORK/e4.log" 2>&1 &
NODE4_PID=$!
for i in $(seq 1 30); do
  grep -q "Peer connected" "$WORK/e4.log" && break
  [ "$i" -eq 30 ] && fail "e4 never joined: $(tail -20 "$WORK/e4.log")"
  sleep 1
done
pass "e4 up (spur node on e2 — flood would hand it a copy)"

# Give GossipSub time to graft (broadcasts still ride pubsub) and the
# connection ledger time to settle.
sleep 3

PAYLOAD_E="ripple-spray-e-$(date +%s)"
"$WSPROBE" -mode listen -url "ws://localhost:$WE1/ws" -timeout 15s \
  >"$WORK/e1_frames.jsonl" 2>"$WORK/probe_e1.err" &
PROBE1_PID=$!
"$WSPROBE" -mode listen -url "ws://localhost:$WE3/ws" -timeout 15s \
  >"$WORK/e3_frames.jsonl" 2>"$WORK/probe_e3.err" &
PROBE2_PID=$!
"$WSPROBE" -mode listen -url "ws://localhost:$WE4/ws" -timeout 15s \
  >"$WORK/e4_frames.jsonl" 2>"$WORK/probe_e4.err" &
PROBE3_PID=$!
PROBE_PID="$PROBE1_PID $PROBE2_PID $PROBE3_PID"
sleep 1  # let the probes connect and receive identity

"$WSPROBE" -mode send -url "ws://localhost:$WE1/ws" -payload "$PAYLOAD_E" -recipient "$PEER_E3" -timeout 3s \
  || fail "send phase E failed"

wait "$PROBE1_PID" "$PROBE2_PID" "$PROBE3_PID"; PROBE_PID=""
grep -qF "listen finished" "$WORK/e1_frames.jsonl" || fail "e1 probe failed: $(cat "$WORK/probe_e1.err")"
grep -qF "listen finished" "$WORK/e3_frames.jsonl" || fail "e3 probe failed: $(cat "$WORK/probe_e3.err")"
grep -qF "listen finished" "$WORK/e4_frames.jsonl" || fail "e4 probe failed: $(cat "$WORK/probe_e4.err")"

# 1. The DM must be delivered to e3 through the same 2-hop relay as Phase B.
grep -qF "$PAYLOAD_E" "$WORK/e3.log" || {
  echo "--- e3 log ---"; tail -30 "$WORK/e3.log"; fail "spray DM E NOT delivered to e3"
}
pass "spray DM E delivered to e3 through e2 (2 hops, bounded copies)"

# 2. The relay event at e3 must carry the copy count: budget L=2 → copies=2.
MSG_E=$(grep -F "$PAYLOAD_E" "$WORK/e3_frames.jsonl" \
  | grep -oE '"id":"[0-9a-f]+"' | head -1 | sed -E 's/.*:"//;s/"//')
[ -n "$MSG_E" ] || fail "could not find message id for payload E in e3 frames"
RELAY_E=$(grep -F "$MSG_E" "$WORK/e3_frames.jsonl" | grep '"type":"relay"' || true)
echo "$RELAY_E" | grep -q '"action":"received"' || fail "e3 never reported received for $MSG_E"
echo "$RELAY_E" | grep -q '"copies":2' || fail "relay event for $MSG_E shows copies != 2 (budget not honored on the wire)"
pass "budget observable on the wire: relay event at e3 shows copies=2"

# 3. The delivery receipt must travel back to e1 over the routed path.
ACK_E=$(grep -F "$MSG_E" "$WORK/e1_frames.jsonl" | grep '"type":"delivery_ack"' || true)
[ -n "$ACK_E" ] || fail "no delivery receipt for spray DM E reached e1"
echo "$ACK_E" | grep -q '"status":"received"' || fail "delivery receipt for E is not 'received'"
pass "delivery receipt for $MSG_E returned to e1 over the routed path"

# 4. The spur node must be untouched — epidemic flooding hands a copy to
#    every connected node (Phases A–B proved that), bounded routing must
#    not waste its budget on a node that is not on the way to e3.
if grep -qF "$PAYLOAD_E" "$WORK/e4.log"; then
  fail "spur node e4 received the DM — spray wasted a copy (epidemic would)"
fi
if grep -qF "$PAYLOAD_E" "$WORK/e4_frames.jsonl"; then
  fail "spur node e4's bridge saw the DM — spray wasted a copy"
fi
pass "spur node e4 untouched — bounded routing spent exactly L=2 copies"

# ── Wrap up ───────────────────────────────────────────────────────────
echo
echo "══════════════════════════════════════════════════════════════"
echo "ALL INTEGRATION ASSERTIONS PASSED — real multi-hop mesh delivery"
echo "══════════════════════════════════════════════════════════════"

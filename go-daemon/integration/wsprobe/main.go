// Command wsprobe is a test harness for a Ripple daemon's WebSocket bridge.
//
// Two modes:
//
//	send   — send one chat message (optionally addressed to a recipient)
//	         and watch briefly for error frames. Used to prove real
//	         delivery: the CI test asserts the payload arrives elsewhere.
//
//	listen — connect and print every frame the bridge emits as one JSON
//	         line per frame, until the timeout. Used to assert relay events:
//	         the CI test greps the output for relay frames with specific
//	         message IDs and actions.
//
//	seed   — request the node's BIP39 backup phrase over the bridge and
//	         print it, exiting non-zero unless a valid 24-word phrase
//	         arrives. Used to prove the app-facing backup path serves the
//	         real identity key.
//
//	sos-send — broadcast a real SOS frame over the bridge, then wait for
//	         the delivery receipt to round-trip: the bridge echoes our own
//	         SOS (id extractable), and the mesh's ack arrives as a
//	         delivery_ack for that id. Exits non-zero if no ack arrives.
//
//	sos-listen — wait for an `sos` frame from the mesh (someone else's
//	         alert), assert its urgency/message, print it, exit 0.
//
// Usage:
//
//	go run ./integration/wsprobe -mode send -url ws://localhost:9876/ws -payload "hello"
//	go run ./integration/wsprobe -mode listen -url ws://localhost:9876/ws -timeout 8s > frames.jsonl
//	go run ./integration/wsprobe -mode seed -url ws://localhost:9876/ws
//	go run ./integration/wsprobe -mode sos-send -url ws://localhost:9876/ws -payload "help" -timeout 10s
//	go run ./integration/wsprobe -mode sos-listen -url ws://localhost:9876/ws -payload "help" -timeout 10s
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	mode := flag.String("mode", "send", "probe mode: send | listen | seed | sos-send | sos-listen")
	url := flag.String("url", "ws://localhost:9876/ws", "daemon WebSocket bridge URL")
	payload := flag.String("payload", "hello ripple mesh", "chat payload to send")
	recipient := flag.String("recipient", "", "peer ID to address the message to (empty = broadcast)")
	urgency := flag.String("urgency", "high", "SOS urgency for sos-send")
	timeout := flag.Duration("timeout", 5*time.Second, "how long to watch/listen")
	flag.Parse()

	conn, _, err := websocket.DefaultDialer.Dial(*url, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ dial %s: %v\n", *url, err)
		os.Exit(1)
	}
	defer conn.Close()

	switch *mode {
	case "send":
		sendMode(conn, *payload, *recipient, *timeout)
	case "listen":
		listenMode(conn, *timeout)
	case "seed":
		seedMode(conn, *timeout)
	case "sos-send":
		sosSendMode(conn, *payload, *urgency, *timeout)
	case "sos-listen":
		sosListenMode(conn, *payload, *timeout)
	default:
		fmt.Fprintf(os.Stderr, "❌ unknown mode %q (want send|listen|seed|sos-send|sos-listen)\n", *mode)
		os.Exit(1)
	}
}

func sendMode(conn *websocket.Conn, payload, recipient string, timeout time.Duration) {
	// Minimal chat frame: the daemon fills in id, sender and timestamp.
	body, err := json.Marshal(map[string]string{
		"type":      "chat",
		"payload":   payload,
		"recipient": recipient,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ marshal frame: %v\n", err)
		os.Exit(1)
	}

	if err := conn.WriteMessage(websocket.TextMessage, body); err != nil {
		fmt.Fprintf(os.Stderr, "❌ write frame: %v\n", err)
		os.Exit(1)
	}
	target := "broadcast"
	if recipient != "" {
		target = "DM to " + recipient
	}
	fmt.Printf("📤 sent (%s): %s\n", target, payload)

	// Watch for an error frame from the bridge until the deadline.
	conn.SetReadDeadline(time.Now().Add(timeout))
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			break // read deadline or normal close — expected
		}
		var frame map[string]interface{}
		if json.Unmarshal(data, &frame) == nil && frame["type"] == "error" {
			fmt.Fprintf(os.Stderr, "❌ bridge error: %s\n", frame["payload"])
			os.Exit(1)
		}
	}
	fmt.Println("✅ probe finished, no bridge errors")
}

// seedMode requests the bridge's seed_export frame and validates the reply.
// Prints the phrase on success; exits non-zero on any failure so scripts can
// assert on the exit code.
func seedMode(conn *websocket.Conn, timeout time.Duration) {
	body, err := json.Marshal(map[string]string{"type": "seed_export"})
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ marshal seed_export: %v\n", err)
		os.Exit(1)
	}
	if err := conn.WriteMessage(websocket.TextMessage, body); err != nil {
		fmt.Fprintf(os.Stderr, "❌ write seed_export: %v\n", err)
		os.Exit(1)
	}

	conn.SetReadDeadline(time.Now().Add(timeout))
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ no seed_export_reply before deadline: %v\n", err)
			os.Exit(1)
		}
		var frame map[string]interface{}
		if json.Unmarshal(data, &frame) != nil {
			continue
		}
		switch frame["type"] {
		case "seed_export_reply":
			phrase, _ := frame["mnemonic"].(string)
			if len(strings.Fields(phrase)) != 24 {
				fmt.Fprintf(os.Stderr, "❌ seed_export_reply is not 24 words: %q\n", phrase)
				os.Exit(1)
			}
			fmt.Println(phrase)
			return
		case "error":
			fmt.Fprintf(os.Stderr, "❌ bridge error: %v\n", frame["payload"])
			os.Exit(1)
		}
	}
}

// sosSendMode broadcasts a real SOS and waits for the delivery receipt to
// round-trip: the bridge echoes our own alert (giving us the message ID),
// and the mesh ack arrives as a delivery_ack addressed to that ID.
func sosSendMode(conn *websocket.Conn, msg, urgency string, timeout time.Duration) {
	payload, err := json.Marshal(map[string]interface{}{
		"urgency":        urgency,
		"message":        msg,
		"expire_minutes": 60,
		"ack_required":   true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ marshal sos payload: %v\n", err)
		os.Exit(1)
	}
	body, err := json.Marshal(map[string]string{
		"type":    "sos",
		"payload": string(payload),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ marshal sos frame: %v\n", err)
		os.Exit(1)
	}
	if err := conn.WriteMessage(websocket.TextMessage, body); err != nil {
		fmt.Fprintf(os.Stderr, "❌ write sos frame: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("🚨 SOS sent: %s (urgency=%s)\n", msg, urgency)

	// Wait for our own echo (to learn the daemon-generated ID), then for
	// the delivery_ack carrying that ID.
	conn.SetReadDeadline(time.Now().Add(timeout))
	var sosID string
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ no delivery receipt for SOS before deadline: %v\n", err)
			os.Exit(1)
		}
		var frame map[string]interface{}
		if json.Unmarshal(data, &frame) != nil {
			continue
		}
		switch frame["type"] {
		case "sos":
			if id, ok := frame["id"].(string); ok && id != "" {
				sosID = id
			}
		case "delivery_ack":
			if sosID == "" {
				continue // ack for something else; we need our echo first
			}
			if frame["msg_id"] == sosID && frame["status"] == "received" {
				fmt.Printf("✅ SOS delivery receipt received for %s\n", sosID)
				return
			}
		case "error":
			fmt.Fprintf(os.Stderr, "❌ bridge error: %v\n", frame["payload"])
			os.Exit(1)
		}
	}
}

// sosListenMode waits for an SOS alert broadcast from the mesh, asserts it
// carries the expected message, prints it, and exits 0.
func sosListenMode(conn *websocket.Conn, wantMsg string, timeout time.Duration) {
	hello, _ := json.Marshal(map[string]string{"type": "ping"})
	if err := conn.WriteMessage(websocket.TextMessage, hello); err != nil {
		fmt.Fprintf(os.Stderr, "❌ write ping: %v\n", err)
		os.Exit(1)
	}

	conn.SetReadDeadline(time.Now().Add(timeout))
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ no SOS frame before deadline: %v\n", err)
			os.Exit(1)
		}
		var frame map[string]interface{}
		if json.Unmarshal(data, &frame) != nil {
			continue
		}
		if frame["type"] != "sos" {
			continue
		}
		if got, _ := frame["message"].(string); got != wantMsg {
			fmt.Fprintf(os.Stderr, "❌ SOS message %q != expected %q\n", got, wantMsg)
			os.Exit(1)
		}
		if _, ok := frame["urgency"].(string); !ok || frame["urgency"] == "" {
			fmt.Fprintf(os.Stderr, "❌ SOS frame missing urgency\n")
			os.Exit(1)
		}
		fmt.Printf("✅ SOS received: %s\n", string(data))
		return
	}
}

func listenMode(conn *websocket.Conn, timeout time.Duration) {
	// Greet so the bridge knows we are a client; then print everything.
	hello, _ := json.Marshal(map[string]string{"type": "ping"})
	if err := conn.WriteMessage(websocket.TextMessage, hello); err != nil {
		fmt.Fprintf(os.Stderr, "❌ write ping: %v\n", err)
		os.Exit(1)
	}

	deadline := time.Now().Add(timeout)
	conn.SetReadDeadline(deadline)
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if time.Now().Before(deadline) {
				fmt.Fprintf(os.Stderr, "❌ read error before deadline: %v\n", err)
				os.Exit(1)
			}
			break // expected: deadline reached
		}
		// Print raw JSON on its own line for deterministic grepping.
		fmt.Println(string(data))
	}
	fmt.Println("✅ listen finished")
}

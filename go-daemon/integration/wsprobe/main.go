// Command wsprobe sends one chat message into a Ripple daemon's WebSocket
// bridge and watches briefly for error frames.
//
// It exists for the CI integration test: after the probe sends a payload
// through node1's bridge, the test asserts the payload shows up in the
// logs of the other mesh nodes — proving real end-to-end delivery rather
// than just open ports.
//
// Usage:
//
//	go run ./integration/wsprobe -url ws://localhost:9876/ws -payload "hello mesh"
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	url := flag.String("url", "ws://localhost:9876/ws", "daemon WebSocket bridge URL")
	payload := flag.String("payload", "hello ripple mesh", "chat payload to send")
	timeout := flag.Duration("timeout", 5*time.Second, "how long to watch for error frames after sending")
	flag.Parse()

	conn, _, err := websocket.DefaultDialer.Dial(*url, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ dial %s: %v\n", *url, err)
		os.Exit(1)
	}
	defer conn.Close()

	// Minimal chat frame: the daemon fills in id, sender and timestamp
	// (an empty recipient means broadcast). Payload is JSON-marshaled so
	// escaping is always correct.
	body, err := json.Marshal(map[string]string{
		"type":      "chat",
		"payload":   *payload,
		"recipient": "",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ marshal frame: %v\n", err)
		os.Exit(1)
	}

	if err := conn.WriteMessage(websocket.TextMessage, body); err != nil {
		fmt.Fprintf(os.Stderr, "❌ write frame: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("📤 sent via %s: %s\n", *url, *payload)

	// Watch for an error frame from the bridge until the deadline.
	conn.SetReadDeadline(time.Now().Add(*timeout))
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

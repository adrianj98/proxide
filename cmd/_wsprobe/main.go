// Test-only WebSocket probe: connects to a ws URL, sends a few messages, and
// verifies they are echoed back. Exits 0 on success, 1 on any failure.
// Used by scripts/functional-test.sh to exercise WebSocket through the tunnel.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/coder/websocket"
)

func main() {
	url := flag.String("url", "", "websocket URL, e.g. ws://127.0.0.1:18080/ws")
	flag.Parse()
	if *url == "" {
		fmt.Fprintln(os.Stderr, "wsprobe: -url is required")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, _, err := websocket.Dial(ctx, *url, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "wsprobe: dial:", err)
		os.Exit(1)
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	for _, msg := range []string{"ping1", "ping2", "hello-ws"} {
		if err := c.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
			fmt.Fprintln(os.Stderr, "wsprobe: write:", err)
			os.Exit(1)
		}
		_, data, err := c.Read(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "wsprobe: read:", err)
			os.Exit(1)
		}
		if string(data) != msg {
			fmt.Fprintf(os.Stderr, "wsprobe: echo mismatch: got %q want %q\n", data, msg)
			os.Exit(1)
		}
	}
	fmt.Println("wsprobe: ok")
}

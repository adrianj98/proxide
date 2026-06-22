package transport

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"

	"github.com/coder/websocket"
)

// authHeader carries the shared secret on the websocket upgrade request.
const authHeader = "Authorization"

func bearer(token string) string { return "Bearer " + token }

// WSDialer is the agent-side transport: it dials the edge over websocket and
// presents the connection as a net.Conn for yamux.
type WSDialer struct {
	// URL is the edge tunnel endpoint, e.g. ws://edge:7000/tunnel or wss://...
	URL string
	// Token is the shared secret presented to the edge.
	Token string
	// Insecure skips TLS certificate verification (wss with self-signed certs).
	Insecure bool
}

// Dial opens an authenticated websocket to the edge. The returned net.Conn is
// long-lived; its lifetime is decoupled from the handshake ctx.
func (d *WSDialer) Dial(ctx context.Context) (net.Conn, error) {
	opts := &websocket.DialOptions{HTTPHeader: http.Header{}}
	if d.Token != "" {
		opts.HTTPHeader.Set(authHeader, bearer(d.Token))
	}
	if d.Insecure {
		opts.HTTPClient = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}
	}

	c, _, err := websocket.Dial(ctx, d.URL, opts)
	if err != nil {
		return nil, fmt.Errorf("websocket dial %s: %w", d.URL, err)
	}
	// yamux manages its own flow control; remove the per-message read cap.
	c.SetReadLimit(-1)

	// Use a background context so the tunnel is not torn down when the dial
	// ctx is cancelled after a successful handshake.
	return websocket.NetConn(context.Background(), c, websocket.MessageBinary), nil
}

// WSListener is the edge-side transport handler. It authenticates incoming
// upgrade requests and hands the resulting net.Conn to OnConn.
type WSListener struct {
	// Token is the expected shared secret. Empty disables auth (dev only).
	Token string
	// OnConn is invoked with each accepted tunnel connection. It should block
	// for the lifetime of the connection (the http handler returns when it does).
	OnConn func(conn net.Conn)
}

// ServeHTTP implements http.Handler for the tunnel endpoint.
func (l *WSListener) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if l.Token != "" {
		got := r.Header.Get(authHeader)
		want := bearer(l.Token)
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		// Accept already wrote a response on failure.
		return
	}
	c.SetReadLimit(-1)

	conn := websocket.NetConn(context.Background(), c, websocket.MessageBinary)
	l.OnConn(conn)
}

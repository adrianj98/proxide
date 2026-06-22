// Package transport abstracts the control connection between the agent
// (Server 1) and the edge (Server 2). The connection is presented as a
// net.Conn so that a stream multiplexer (yamux) can run on top of it.
//
// websocket is the first concrete implementation. The Transport interface
// exists so that other "connection styles" (raw TCP, gRPC, QUIC, ...) can be
// added later without touching the tunnel or server logic.
package transport

import (
	"context"
	"net"
)

// Dialer establishes the outbound control connection from the agent to the
// edge. The returned net.Conn carries the multiplexed tunnel.
type Dialer interface {
	// Dial opens an authenticated control connection to the edge.
	Dial(ctx context.Context) (net.Conn, error)
}

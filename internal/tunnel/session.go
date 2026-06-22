// Package tunnel layers stream multiplexing (yamux) over a single control
// connection and provides byte-level piping between connections.
//
// Roles: the agent dials the edge, so it is the yamux *client*; the edge
// accepts, so it is the yamux *server*. yamux allows either side to open
// streams, so the edge opens a stream per inbound public connection while the
// agent accepts streams and dials the local target.
package tunnel

import (
	"io"
	"net"
	"time"

	"github.com/hashicorp/yamux"
)

// config returns the shared yamux configuration with keepalive enabled so dead
// connections are detected promptly on both ends.
func config() *yamux.Config {
	c := yamux.DefaultConfig()
	c.EnableKeepAlive = true
	c.KeepAliveInterval = 15 * time.Second
	c.ConnectionWriteTimeout = 15 * time.Second
	// Silence yamux's internal logging; callers handle logging. yamux requires
	// exactly one of LogOutput/Logger to be set.
	c.LogOutput = io.Discard
	return c
}

// Client wraps the agent side of the control connection.
func Client(conn net.Conn) (*yamux.Session, error) {
	return yamux.Client(conn, config())
}

// Server wraps the edge side of the control connection.
func Server(conn net.Conn) (*yamux.Session, error) {
	return yamux.Server(conn, config())
}

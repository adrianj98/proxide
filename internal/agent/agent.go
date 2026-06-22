// Package agent implements Server 1: the side that runs inside the no-ingress
// container. It dials out to the edge, then accepts multiplexed streams and
// forwards each one to the local target service.
package agent

import (
	"context"
	"log"
	"net"
	"time"

	"github.com/alertd/devproxy/internal/transport"
	"github.com/alertd/devproxy/internal/tunnel"
)

// Config holds the agent settings.
type Config struct {
	EdgeURL string // edge tunnel endpoint, e.g. ws://edge:7000/tunnel
	Target  string // local service to forward to, e.g. 127.0.0.1:8080
	Token   string // shared secret presented to the edge

	MinBackoff time.Duration // initial reconnect delay (default 1s)
	MaxBackoff time.Duration // max reconnect delay (default 30s)
}

// Agent is the agent runtime.
type Agent struct {
	cfg    Config
	dialer transport.Dialer
}

// New creates an Agent.
func New(cfg Config) *Agent {
	if cfg.MinBackoff == 0 {
		cfg.MinBackoff = time.Second
	}
	if cfg.MaxBackoff == 0 {
		cfg.MaxBackoff = 30 * time.Second
	}
	return &Agent{
		cfg:    cfg,
		dialer: &transport.WSDialer{URL: cfg.EdgeURL, Token: cfg.Token},
	}
}

// Run connects to the edge and serves streams, reconnecting with exponential
// backoff until ctx is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	backoff := a.cfg.MinBackoff
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		connected, err := a.connectAndServe(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// A connection that was actually established resets the backoff, so a
		// long-lived tunnel that later drops reconnects quickly.
		if connected {
			backoff = a.cfg.MinBackoff
		}
		log.Printf("agent: connection lost: %v; reconnecting in %s", err, backoff)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > a.cfg.MaxBackoff {
			backoff = a.cfg.MaxBackoff
		}
	}
}

// connectAndServe dials the edge and serves accepted streams until the session
// dies. The bool reports whether the session was successfully established
// (used to reset reconnect backoff). It returns the error that ended the session.
func (a *Agent) connectAndServe(ctx context.Context) (bool, error) {
	conn, err := a.dialer.Dial(ctx)
	if err != nil {
		return false, err
	}

	sess, err := tunnel.Client(conn)
	if err != nil {
		_ = conn.Close()
		return false, err
	}
	defer sess.Close()

	log.Printf("agent: connected to edge %s, forwarding to %s", a.cfg.EdgeURL, a.cfg.Target)

	for {
		stream, err := sess.Accept()
		if err != nil {
			return true, err
		}
		go a.serveStream(stream)
	}
}

// serveStream dials the local target and pipes bytes to/from the stream.
func (a *Agent) serveStream(stream net.Conn) {
	target, err := net.Dial("tcp", a.cfg.Target)
	if err != nil {
		log.Printf("agent: dial target %s: %v", a.cfg.Target, err)
		_ = stream.Close()
		return
	}
	tunnel.Pipe(stream, target)
}

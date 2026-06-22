// Package edge implements Server 2: the publicly reachable side of the tunnel.
//
// It runs two listeners:
//   - control plane: a websocket endpoint the agent dials out to. The accepted
//     connection becomes a yamux session.
//   - public plane: a plain TCP port that external clients connect to. Each
//     inbound connection is forwarded over a new yamux stream to the agent.
//
// Single-agent model: the most recent agent connection wins; any previous
// session is closed. Public connections that arrive while no agent is connected
// are closed immediately.
package edge

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"sync"

	"github.com/alertd/devproxy/internal/transport"
	"github.com/alertd/devproxy/internal/tunnel"
	"github.com/hashicorp/yamux"
)

// Config holds the edge server settings.
type Config struct {
	ControlAddr string // address for the agent websocket control plane, e.g. ":7000"
	PublicAddr  string // address for inbound public traffic, e.g. ":8080"
	Token       string // shared secret expected from agents ("" disables auth)
	TLSCert     string // optional cert file for the control plane (enables wss)
	TLSKey      string // optional key file for the control plane

	AdminAddr    string // optional address for the admin web UI ("" disables it)
	AdminTLSCert string // cert for the admin UI (falls back to TLSCert)
	AdminTLSKey  string // key for the admin UI (falls back to TLSKey)
}

// ErrNoAgent is returned when an operation needs the agent but none is connected.
var ErrNoAgent = errors.New("no agent connected")

// Server is the edge runtime.
type Server struct {
	cfg Config

	mu      sync.Mutex
	session *yamux.Session // current agent session, or nil

	sessions *adminSessions // admin UI login sessions
}

// New creates an edge Server.
func New(cfg Config) *Server {
	return &Server{cfg: cfg, sessions: newAdminSessions()}
}

// setSession installs a new agent session, closing any prior one.
func (s *Server) setSession(sess *yamux.Session) {
	s.mu.Lock()
	old := s.session
	s.session = sess
	s.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
}

// clearSession removes sess if it is still the current one.
func (s *Server) clearSession(sess *yamux.Session) {
	s.mu.Lock()
	if s.session == sess {
		s.session = nil
	}
	s.mu.Unlock()
}

// currentSession returns the active agent session, or nil.
func (s *Server) currentSession() *yamux.Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.session
}

// handleAgentConn turns an accepted control connection into a yamux session and
// keeps it as the active tunnel until it closes.
func (s *Server) handleAgentConn(conn net.Conn) {
	sess, err := tunnel.Server(conn)
	if err != nil {
		log.Printf("edge: yamux server: %v", err)
		_ = conn.Close()
		return
	}
	log.Printf("edge: agent connected from %s", conn.RemoteAddr())
	s.setSession(sess)

	// Block until the session dies so the underlying websocket handler stays
	// open for the connection's lifetime.
	<-sess.CloseChan()
	s.clearSession(sess)
	log.Printf("edge: agent disconnected from %s", conn.RemoteAddr())
}

// servePublicConn forwards one inbound public connection over the tunnel.
func (s *Server) servePublicConn(pc net.Conn) {
	sess := s.currentSession()
	if sess == nil {
		log.Printf("edge: dropping %s, no agent connected", pc.RemoteAddr())
		_ = pc.Close()
		return
	}

	stream, err := sess.Open()
	if err != nil {
		log.Printf("edge: open stream: %v", err)
		_ = pc.Close()
		return
	}
	if err := tunnel.WriteStreamType(stream, tunnel.StreamProxy); err != nil {
		_ = stream.Close()
		_ = pc.Close()
		return
	}
	tunnel.Pipe(pc, stream)
}

// RunCommand opens an exec stream to the agent and returns a reader of the
// command's combined stdout/stderr. Closing the returned ReadCloser tells the
// agent to terminate the command.
func (s *Server) RunCommand(cmd string) (io.ReadCloser, error) {
	sess := s.currentSession()
	if sess == nil {
		return nil, ErrNoAgent
	}
	stream, err := sess.Open()
	if err != nil {
		return nil, err
	}
	if err := tunnel.WriteStreamType(stream, tunnel.StreamExec); err != nil {
		_ = stream.Close()
		return nil, err
	}
	if err := tunnel.WriteExecRequest(stream, cmd); err != nil {
		_ = stream.Close()
		return nil, err
	}
	return stream, nil
}

// Run starts both listeners and blocks until ctx is cancelled or a listener
// fails fatally.
func (s *Server) Run(ctx context.Context) error {
	// Control plane (websocket).
	mux := http.NewServeMux()
	mux.Handle("/tunnel", &transport.WSListener{
		Token:  s.cfg.Token,
		OnConn: s.handleAgentConn,
	})
	control := &http.Server{Addr: s.cfg.ControlAddr, Handler: mux}

	// Public plane (plain TCP).
	pub, err := net.Listen("tcp", s.cfg.PublicAddr)
	if err != nil {
		return err
	}

	// Admin web UI (optional).
	var admin *http.Server
	if s.cfg.AdminAddr != "" {
		if s.cfg.Token == "" {
			_ = pub.Close()
			return errors.New("admin UI requires --token for login")
		}
		admin = &http.Server{Addr: s.cfg.AdminAddr, Handler: s.adminHandler()}
	}

	errCh := make(chan error, 3)

	go func() {
		log.Printf("edge: control plane on %s/tunnel (tls=%t)", s.cfg.ControlAddr, s.cfg.TLSCert != "")
		var err error
		if s.cfg.TLSCert != "" && s.cfg.TLSKey != "" {
			err = control.ListenAndServeTLS(s.cfg.TLSCert, s.cfg.TLSKey)
		} else {
			err = control.ListenAndServe()
		}
		if !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	go func() {
		log.Printf("edge: public plane on %s", s.cfg.PublicAddr)
		for {
			pc, err := pub.Accept()
			if err != nil {
				errCh <- err
				return
			}
			go s.servePublicConn(pc)
		}
	}()

	if admin != nil {
		go func() {
			cert, key := s.adminTLS()
			log.Printf("edge: admin UI on %s (tls=%t)", s.cfg.AdminAddr, cert != "")
			var err error
			if cert != "" && key != "" {
				err = admin.ListenAndServeTLS(cert, key)
			} else {
				log.Printf("edge: WARNING admin UI is serving WITHOUT TLS; the login token will be sent in cleartext")
				err = admin.ListenAndServe()
			}
			if !errors.Is(err, http.ErrServerClosed) {
				errCh <- err
			}
		}()
	}

	shutdown := func() {
		_ = control.Shutdown(context.Background())
		_ = pub.Close()
		if admin != nil {
			_ = admin.Shutdown(context.Background())
		}
	}

	select {
	case <-ctx.Done():
		shutdown()
		return ctx.Err()
	case err := <-errCh:
		shutdown()
		return err
	}
}

// adminTLS returns the cert/key for the admin UI, falling back to the control
// plane's cert/key when admin-specific ones are not set.
func (s *Server) adminTLS() (cert, key string) {
	cert, key = s.cfg.AdminTLSCert, s.cfg.AdminTLSKey
	if cert == "" && key == "" {
		cert, key = s.cfg.TLSCert, s.cfg.TLSKey
	}
	return cert, key
}

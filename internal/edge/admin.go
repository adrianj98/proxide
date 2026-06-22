package edge

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/alertd/devproxy/internal/tunnel"
)

const (
	sessionCookie = "devproxy_session"
	sessionTTL    = 12 * time.Hour
	maxCommand    = 1 << 20 // 1 MiB
)

// adminSession is a logged-in console session. cwd persists between commands.
type adminSession struct {
	expiry time.Time
	cwd    string
}

// adminSessions is an in-memory store of valid admin login sessions. Sessions do
// not survive an edge restart.
type adminSessions struct {
	mu sync.Mutex
	m  map[string]*adminSession // session id -> session
}

func newAdminSessions() *adminSessions {
	return &adminSessions{m: make(map[string]*adminSession)}
}

func (a *adminSessions) create() string {
	id := randomToken()
	a.mu.Lock()
	a.m[id] = &adminSession{expiry: time.Now().Add(sessionTTL)}
	a.mu.Unlock()
	return id
}

func (a *adminSessions) valid(id string) bool {
	if id == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.m[id]
	if !ok {
		return false
	}
	if time.Now().After(s.expiry) {
		delete(a.m, id)
		return false
	}
	return true
}

// cwd returns the session's current working directory ("" = agent default).
func (a *adminSessions) cwd(id string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s, ok := a.m[id]; ok {
		return s.cwd
	}
	return ""
}

// setCwd records the directory the last command ended in.
func (a *adminSessions) setCwd(id, cwd string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s, ok := a.m[id]; ok {
		s.cwd = cwd
	}
}

func (a *adminSessions) delete(id string) {
	a.mu.Lock()
	delete(a.m, id)
	a.mu.Unlock()
}

func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// adminHandler builds the admin UI router.
func (s *Server) adminHandler() http.Handler {
	cert, _ := s.adminTLS()
	secureCookies := cert != ""

	mux := http.NewServeMux()
	mux.HandleFunc("/login", s.handleLogin(secureCookies))
	mux.HandleFunc("/logout", s.handleLogout(secureCookies))
	mux.HandleFunc("/exec", s.requireAuth(s.handleExec))
	mux.HandleFunc("/cwd", s.requireAuth(s.handleCwd))
	mux.HandleFunc("/", s.requireAuth(s.handleConsole))
	return mux
}

// requireAuth wraps a handler, redirecting unauthenticated browsers to /login
// and rejecting unauthenticated API calls with 401.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || !s.sessions.valid(c.Value) {
			if r.Method == http.MethodGet && r.Header.Get("Accept") != "text/plain" {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *Server) handleLogin(secure bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeHTML(w, loginHTML)
		case http.MethodPost:
			token := r.FormValue("token")
			if subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.Token)) != 1 {
				w.WriteHeader(http.StatusUnauthorized)
				writeHTML(w, loginErrorHTML)
				return
			}
			http.SetCookie(w, &http.Cookie{
				Name:     sessionCookie,
				Value:    s.sessions.create(),
				Path:     "/",
				HttpOnly: true,
				Secure:   secure,
				SameSite: http.SameSiteStrictMode,
			})
			http.Redirect(w, r, "/", http.StatusSeeOther)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func (s *Server) handleLogout(secure bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(sessionCookie); err == nil {
			s.sessions.delete(c.Value)
		}
		http.SetCookie(w, &http.Cookie{
			Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
			HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode,
		})
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	}
}

func (s *Server) handleConsole(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	writeHTML(w, consoleHTML)
}

// sessionID returns the (already-validated) session id from the request cookie.
func sessionID(r *http.Request) string {
	if c, err := r.Cookie(sessionCookie); err == nil {
		return c.Value
	}
	return ""
}

// handleCwd returns the session's current working directory as plain text.
func (s *Server) handleCwd(w http.ResponseWriter, r *http.Request) {
	cwd := s.sessions.cwd(sessionID(r))
	if cwd == "" {
		cwd = "~"
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, cwd)
}

// handleExec runs a command inside the container starting in the session's cwd,
// streams the output back, and persists the directory the command ended in.
func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxCommand))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	cmd := string(body)
	if cmd == "" {
		http.Error(w, "empty command", http.StatusBadRequest)
		return
	}

	id := sessionID(r)
	stream, err := s.RunCommand(s.sessions.cwd(id), cmd)
	if errors.Is(err, ErrNoAgent) {
		http.Error(w, "no agent connected", http.StatusServiceUnavailable)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer stream.Close()

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	flusher, _ := w.(http.Flusher)

	// Stop the command if the client goes away.
	go func() {
		<-r.Context().Done()
		_ = stream.Close()
	}()

	for {
		frame, payload, err := tunnel.ReadExecFrame(stream)
		if err != nil {
			return
		}
		switch frame {
		case tunnel.ExecOutput:
			if _, err := w.Write(payload); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		case tunnel.ExecResult:
			_, cwd := tunnel.ParseExecResult(payload)
			if cwd != "" {
				s.sessions.setCwd(id, cwd)
			}
			return
		}
	}
}

func writeHTML(w http.ResponseWriter, html string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, html)
}

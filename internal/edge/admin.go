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
)

const (
	sessionCookie = "devproxy_session"
	sessionTTL    = 12 * time.Hour
	maxCommand    = 1 << 20 // 1 MiB
)

// adminSessions is an in-memory store of valid admin login sessions. Sessions do
// not survive an edge restart.
type adminSessions struct {
	mu sync.Mutex
	m  map[string]time.Time // session id -> expiry
}

func newAdminSessions() *adminSessions {
	return &adminSessions{m: make(map[string]time.Time)}
}

func (a *adminSessions) create() string {
	id := randomToken()
	a.mu.Lock()
	a.m[id] = time.Now().Add(sessionTTL)
	a.mu.Unlock()
	return id
}

func (a *adminSessions) valid(id string) bool {
	if id == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	exp, ok := a.m[id]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(a.m, id)
		return false
	}
	return true
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

// handleExec runs a command inside the container and streams the output back.
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

	rc, err := s.RunCommand(cmd)
	if errors.Is(err, ErrNoAgent) {
		http.Error(w, "no agent connected", http.StatusServiceUnavailable)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	// Stop the command if the client goes away.
	go func() {
		<-r.Context().Done()
		_ = rc.Close()
	}()

	fw := &flushWriter{w: w}
	if f, ok := w.(http.Flusher); ok {
		fw.f = f
	}
	_, _ = io.Copy(fw, rc)
}

// flushWriter flushes after every write so output streams to the browser live.
type flushWriter struct {
	w io.Writer
	f http.Flusher
}

func (fw *flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if fw.f != nil {
		fw.f.Flush()
	}
	return n, err
}

func writeHTML(w http.ResponseWriter, html string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, html)
}

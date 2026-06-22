// Package agent implements Server 1: the side that runs inside the no-ingress
// container. It dials out to the edge, then accepts multiplexed streams and
// forwards each one to the local target service.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/alertd/devproxy/internal/transport"
	"github.com/alertd/devproxy/internal/tunnel"
)

// Config holds the agent settings.
type Config struct {
	EdgeURL  string // edge tunnel endpoint, e.g. ws://edge:7000/tunnel
	Target   string // local service to forward to, e.g. 127.0.0.1:8080
	Token    string // shared secret presented to the edge
	Insecure bool   // skip TLS verification for wss with self-signed certs

	// Shell is the shell invocation used for admin-console commands, e.g.
	// "bash -lc" or "/bin/sh -c". Empty means auto-detect by OS.
	Shell string

	MinBackoff time.Duration // initial reconnect delay (default 1s)
	MaxBackoff time.Duration // max reconnect delay (default 30s)
}

// Agent is the agent runtime.
type Agent struct {
	cfg    Config
	dialer transport.Dialer
	shell  []string // shell + flag, e.g. ["bash","-lc"]; command appended as last arg
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
		dialer: &transport.WSDialer{URL: cfg.EdgeURL, Token: cfg.Token, Insecure: cfg.Insecure},
		shell:  resolveShell(cfg.Shell),
	}
}

// resolveShell returns the shell invocation for running console commands. An
// explicit override is split on spaces; otherwise it defaults by OS (bash on
// Unix, cmd on Windows where bash is usually absent).
func resolveShell(override string) []string {
	if s := strings.Fields(override); len(s) > 0 {
		return s
	}
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/C"}
	}
	return []string{"bash", "-lc"}
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

// serveStream reads the stream type and dispatches to the proxy or exec handler.
func (a *Agent) serveStream(stream net.Conn) {
	t, err := tunnel.ReadStreamType(stream)
	if err != nil {
		_ = stream.Close()
		return
	}
	switch t {
	case tunnel.StreamExec:
		a.serveExec(stream)
	default:
		a.serveProxy(stream)
	}
}

// serveProxy dials the local target and pipes bytes to/from the stream.
func (a *Agent) serveProxy(stream net.Conn) {
	target, err := net.Dial("tcp", a.cfg.Target)
	if err != nil {
		log.Printf("agent: dial target %s: %v", a.cfg.Target, err)
		_ = stream.Close()
		return
	}
	tunnel.Pipe(stream, target)
}

// frameWriter wraps each write from a command's stdout/stderr in an exec output
// frame. It is mutex-guarded because stdout and stderr write concurrently and
// yamux streams are not safe for concurrent writers.
type frameWriter struct {
	mu sync.Mutex
	w  net.Conn
}

func (f *frameWriter) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := tunnel.WriteExecOutput(f.w, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// validDir returns p if it is an existing directory, else "".
func validDir(p string) string {
	if p == "" {
		return ""
	}
	if fi, err := os.Stat(p); err == nil && fi.IsDir() {
		return p
	}
	return ""
}

// serveExec runs a command inside the container starting in the requested
// directory, streams its combined stdout/stderr back as output frames, and ends
// with a result frame carrying the exit code and the directory the command
// finished in (so the edge can persist it for the next command). The command is
// killed if the edge closes the stream (e.g. the operator navigated away).
func (a *Agent) serveExec(stream net.Conn) {
	defer stream.Close()

	reqCwd, cmdStr, err := tunnel.ReadExecRequest(stream)
	if err != nil {
		return
	}
	log.Printf("agent: exec (cwd=%q): %s", reqCwd, cmdStr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Kill the command if the stream is closed from the edge side.
	go func() {
		buf := make([]byte, 1)
		for {
			if _, err := stream.Read(buf); err != nil {
				cancel()
				return
			}
		}
	}()

	out := &frameWriter{w: stream}
	startDir := validDir(reqCwd)
	finalCwd := startDir

	// On POSIX shells, capture the shell's final $PWD to a temp file (kept out
	// of stdout/stderr) so a `cd` persists to the next command.
	cmdLine := cmdStr
	pwdFile := ""
	if a.shell[0] != "cmd" {
		if f, err := os.CreateTemp("", "devproxy-cwd-*"); err == nil {
			pwdFile = f.Name()
			_ = f.Close()
			defer os.Remove(pwdFile)
			cmdLine = cmdStr + "\n__dp_rc=$?; pwd > '" + pwdFile + "' 2>/dev/null; exit $__dp_rc"
		}
	}

	args := append(append([]string{}, a.shell[1:]...), cmdLine)
	cmd := exec.CommandContext(ctx, a.shell[0], args...)
	cmd.Dir = startDir
	cmd.Stdout = out
	cmd.Stderr = out

	var exitCode int32
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exitCode = int32(ee.ExitCode())
		} else {
			fmt.Fprintf(out, "\n[devproxy: %v]\n", err)
			exitCode = -1
		}
	}

	if pwdFile != "" {
		if data, err := os.ReadFile(pwdFile); err == nil {
			if s := strings.TrimRight(string(data), "\r\n"); s != "" {
				finalCwd = s
			}
		}
	}

	_ = tunnel.WriteExecResult(stream, exitCode, finalCwd)
}

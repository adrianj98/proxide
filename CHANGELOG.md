# Changelog

All notable changes to devproxy are recorded here.

## [Unreleased]

### Added
- **L4 reverse tunnel core.** Outbound-only agent (Server 1, `cmd/agent`) dials
  the public edge (Server 2, `cmd/edge`) over a websocket; yamux multiplexes one
  stream per inbound public connection. Edge pipes public TCP traffic through the
  tunnel to the agent, which forwards to a local target service.
  - `internal/transport` — pluggable `Dialer` interface; websocket dialer/listener
    with bearer-token auth (constant-time compare, `401` on mismatch).
  - `internal/tunnel` — yamux session wrap (keepalive enabled) + bidirectional
    `Pipe`.
  - `internal/edge` — control plane (ws, optional TLS) + public TCP plane;
    single-agent model (newest wins); drops public conns when no agent connected.
  - `internal/agent` — dial + reconnect loop with exponential backoff (1s→30s,
    reset on success); accepts streams and dials the local target.
- **Optional TLS** on the control plane (`--tls-cert`/`--tls-key` → `wss://`),
  with `--insecure` on the agent for self-signed certs in development.
- **Docs**: `README.md`, `docs/user-guide.md`, `ofk/architecture.okf`.
- Test-only target server (`cmd/_testserver`) serving HTTP, SSE, and a WebSocket
  echo endpoint for end-to-end verification.
- **Releases & install.** Tag-triggered GitHub Actions workflow
  (`.github/workflows/release.yml`) cross-compiles both binaries for
  linux/darwin × amd64/arm64 into per-platform tarballs + `checksums.txt` and
  publishes a GitHub Release. `scripts/build-release.sh` does the cross-compile;
  `scripts/install.sh` provides a `curl | sh` installer.
- **Version reporting.** `internal/buildinfo.Version` (set via ldflags at
  release time) and a `-version` flag on both binaries.
- **Functional tests.** `scripts/functional-test.sh` drives the real edge +
  agent + pseudo target service end-to-end (HTTP, 20× concurrency, SSE,
  WebSocket via `cmd/_wsprobe`, token auth `401`/`426`, agent reconnect). The
  release workflow now runs `go vet`, `go test`, and the functional test in a
  `test` job that gates the `release` job (no publish unless tests pass).

### Verified
- HTTP/1.1 (incl. keep-alive), 10× concurrent requests (yamux multiplexing),
  SSE streaming, WebSocket echo — all through the tunnel.
- Auth rejection with a wrong token (`401`).
- Automatic agent reconnect after an edge restart.
- TLS (`wss://`) control plane end-to-end with a self-signed cert.

### Added (admin console)
- **Web admin console on the edge.** Optional `--admin-addr` serves a login +
  console UI (separate listener, TLS via `--admin-tls-cert/--admin-tls-key`,
  falling back to the control-plane cert). Login uses the agent `--token`; the
  server refuses to start the UI without a token. Sessions are in-memory cookies
  (`HttpOnly`, `Secure` under TLS, `SameSite=Strict`).
- **Run commands inside the container.** The console runs a command through the
  tunnel: the edge opens an exec stream, the agent runs it via a shell and
  streams combined stdout/stderr back live. New tunnel stream-type framing
  (`internal/tunnel/protocol.go`) distinguishes proxy vs exec streams; the
  command is killed if the operator disconnects.

### Added (platforms)
- **Windows builds.** Release now also produces `windows/amd64` and
  `windows/arm64` (`.zip` archives with `.exe` binaries). The agent shell for
  console commands auto-selects `cmd /C` on Windows (`bash -lc` on Unix), with a
  `--shell` override.

### Changed
- Default `--control-addr` is now `:7223` (was `:7000`) so the edge runs on
  macOS out of the box — port `7000` is owned by the macOS AirPlay Receiver,
  which returns `403`.

### Notes
- macOS: avoid `--control-addr :7000`/`:5000` (AirPlay Receiver, returns `403`).

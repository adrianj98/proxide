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

### Verified
- HTTP/1.1 (incl. keep-alive), 10× concurrent requests (yamux multiplexing),
  SSE streaming, WebSocket echo — all through the tunnel.
- Auth rejection with a wrong token (`401`).
- Automatic agent reconnect after an edge restart.
- TLS (`wss://`) control plane end-to-end with a self-signed cert.

### Changed
- Default `--control-addr` is now `:7223` (was `:7000`) so the edge runs on
  macOS out of the box — port `7000` is owned by the macOS AirPlay Receiver,
  which returns `403`.

### Notes
- macOS: avoid `--control-addr :7000`/`:5000` (AirPlay Receiver, returns `403`).

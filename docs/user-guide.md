# devproxy — User Guide

devproxy exposes a service running in a no-ingress container by tunnelling
inbound public traffic to an outbound-only agent. This guide covers building,
configuring, and running both sides, plus protocol and security notes.

## Contents
- [Concepts](#concepts)
- [Build](#build)
- [Running the edge (Server 2)](#running-the-edge-server-2)
- [Running the agent (Server 1)](#running-the-agent-server-1)
- [TLS](#tls)
- [Supported protocols](#supported-protocols)
- [How it works](#how-it-works)
- [Troubleshooting](#troubleshooting)
- [Limitations](#limitations)

## Concepts

| Role | Binary | Where it runs | What it does |
|------|--------|---------------|--------------|
| **Edge** (Server 2) | `cmd/edge` | publicly reachable host | accepts the agent's websocket; listens on a public port; forwards inbound connections through the tunnel |
| **Agent** (Server 1) | `cmd/agent` | inside the no-ingress container | dials *out* to the edge; forwards tunnelled streams to the local target service |

Only the **agent** makes a connection, and it is **outbound**, so the container
needs no inbound firewall rules or public IP.

## Build

```bash
go build -o bin/edge  ./cmd/edge
go build -o bin/agent ./cmd/agent
```

Both produce static binaries that are easy to drop into a container image.

## Running the edge (Server 2)

```bash
./bin/edge --control-addr :7000 --public-addr :8080 --token secret
```

| Flag | Default | Description |
|------|---------|-------------|
| `--control-addr` | `:7000` | address where the agent connects (websocket `/tunnel`) |
| `--public-addr` | `:8080` | address where external clients connect |
| `--token` | `$DEVPROXY_TOKEN` | shared secret the agent must present; empty disables auth (logs a warning) |
| `--tls-cert` | — | TLS certificate file for the control plane (enables `wss://`) |
| `--tls-key` | — | TLS key file for the control plane |

The edge follows a **single-agent** model: the most recent agent connection wins
and any previous one is closed. Public connections that arrive while no agent is
connected are dropped immediately.

## Running the agent (Server 1)

```bash
./bin/agent --edge-url ws://EDGE_HOST:7000/tunnel --target 127.0.0.1:9000 --token secret
```

| Flag | Default | Description |
|------|---------|-------------|
| `--edge-url` | — (required) | edge tunnel endpoint, e.g. `ws://host:7000/tunnel` or `wss://...` |
| `--target` | — (required) | local service to forward to, `host:port` (e.g. `127.0.0.1:9000`) |
| `--token` | `$DEVPROXY_TOKEN` | shared secret presented to the edge |
| `--insecure` | `false` | skip TLS verification (for `wss://` with self-signed certs; dev only) |

The agent reconnects automatically with exponential backoff (1s → 30s), and the
backoff resets after any successful connection.

## TLS

The **control plane** (agent ↔ edge) can run over TLS so the tunnel and token
are encrypted in transit:

```bash
# edge with a real or self-signed cert
./bin/edge --control-addr :7000 --public-addr :8080 --token secret \
  --tls-cert /path/cert.pem --tls-key /path/key.pem

# agent over wss
./bin/agent --edge-url wss://EDGE_HOST:7000/tunnel --target 127.0.0.1:9000 --token secret
```

With a self-signed cert in development, add `--insecure` to the agent.

For the **public plane**, devproxy is L4 and does not terminate TLS itself. To
serve HTTPS to the outside world, have your container service hold the
certificate and terminate TLS there — the encrypted bytes are piped straight
through (TLS pass-through). (A real-cert / ACME terminating edge is a future
enhancement.)

## Supported protocols

Because the edge pipes raw bytes, every TCP-based protocol works without special
handling:

| Protocol | Status | Notes |
|----------|--------|-------|
| HTTP/1.1 | ✅ | including keep-alive (multiple requests per connection) |
| HTTP/2 | ✅ | tunnelled as a byte stream |
| SSE | ✅ | long-lived streaming bodies pass through unbuffered |
| WebSocket | ✅ | full bidirectional |
| HTTP/3 (QUIC/UDP) | ❌ | not yet; clients fall back to HTTP/2 |

## How it works

1. The agent dials the edge's `/tunnel` websocket and presents the bearer token.
2. The edge validates the token and wraps the websocket as a `net.Conn`.
3. Both sides run [yamux](https://github.com/hashicorp/yamux) over that conn
   (agent = client, edge = server) — a multiplexer that carries many independent
   streams over the one connection, with built-in keepalive.
4. For each inbound connection on the public port, the edge **opens a yamux
   stream**; the agent **accepts** it, dials the local target, and the two are
   piped byte-for-byte in both directions.

## Troubleshooting

- **`403` on connect (macOS)** — port `7000` (and `5000`) is used by the macOS
  AirPlay Receiver, which returns `403`. Use a different `--control-addr`
  (e.g. `:17000`) or disable AirPlay Receiver in System Settings → General →
  AirDrop & Handoff.
- **`401` on connect** — token mismatch between agent and edge.
- **`dropping … no agent connected`** in the edge log — a public request arrived
  while no agent was connected (agent down / reconnecting).
- **`dial target …: connection refused`** in the agent log — the local target
  service isn't listening at `--target`.

## Limitations

- One service per tunnel (one edge ↔ one agent ↔ one target).
- No HTTP/3 / UDP.
- No L7 features (hostname/path routing, request inspection, header rewriting).
- Public plane is L4 only; HTTPS termination must happen at the container service.

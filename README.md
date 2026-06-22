# devproxy

An outbound-only **reverse tunnel** for exposing a service that runs in a
container with **no ingress**. Same shape as ngrok / cloudflared / frp / inlets.

```
[ public client ] --TCP--> [ Edge (Server 2) ]
                                |  one yamux stream per connection
                            [ websocket ]  <-- dialed OUT by the agent
                                |
                            [ Agent (Server 1) ] --TCP--> [ your container service ]
```

- **Agent (Server 1)** runs inside the no-ingress container. It dials *out* to
  the edge over a websocket and forwards traffic to the local service.
- **Edge (Server 2)** runs somewhere publicly reachable. It accepts the agent's
  websocket and listens on a public port; inbound traffic is forwarded back
  through the tunnel.

It is an **L4 (byte-stream) tunnel**: the edge never parses HTTP, it just pipes
bytes. As a result **WebSocket, HTTP/1.1, HTTP/2, and SSE all work transparently**
with no protocol-specific code. Concurrency comes from
[yamux](https://github.com/hashicorp/yamux) multiplexing — every inbound
connection becomes its own stream over the single websocket.

## Install

Install the latest release binaries (`devproxy-edge` and `devproxy-agent`) with:

```bash
curl -fsSL https://raw.githubusercontent.com/alertd/devproxy/main/scripts/install.sh | sh
```

Supports Linux and macOS on amd64/arm64 (Windows builds ship as
`windows_*.zip` on the release page; the installer itself is POSIX-only).
Overrides:
`DEVPROXY_VERSION` (tag, default `latest`), `DEVPROXY_BIN_DIR` (default
`/usr/local/bin`), `DEVPROXY_REPO`. Releases are published automatically when a
`v*` tag is pushed (see [.github/workflows/release.yml](.github/workflows/release.yml)).

## Quick start

Or build from source:

```bash
go build -o bin/edge ./cmd/edge
go build -o bin/agent ./cmd/agent

# On the public host (Server 2):
./bin/edge --control-addr :7223 --public-addr :8080 --token secret

# Inside the container (Server 1), forwarding to a local service on :9000:
./bin/agent --edge-url ws://EDGE_HOST:7223/tunnel --target 127.0.0.1:9000 --token secret

# Anyone can now reach the container service via the edge:
curl http://EDGE_HOST:8080/
```

See [docs/user-guide.md](docs/user-guide.md) for full flag reference, TLS setup,
and protocol notes. Architecture is documented in
[ofk/architecture.okf](ofk/architecture.okf).

## Status / scope

Supported today: TCP-based protocols — HTTP/1.1, HTTP/2, SSE, WebSocket; token
auth; optional TLS (`wss://`) on the control plane; automatic agent reconnect;
an optional web **admin console** (`--admin-addr`) to run shell commands inside
the container through the tunnel; Linux/macOS/Windows binaries.

Not yet (future phases): HTTP/3 (QUIC/UDP), L7 hostname/path routing, request
inspection dashboard, multiple tunnels behind one edge.

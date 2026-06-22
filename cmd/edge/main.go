// Command edge runs Server 2: the public-facing side of the devproxy tunnel.
//
// It accepts an outbound websocket connection from an agent and forwards
// inbound public TCP traffic to that agent over multiplexed streams.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/alertd/devproxy/internal/buildinfo"
	"github.com/alertd/devproxy/internal/edge"
)

func main() {
	cfg := edge.Config{}
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.StringVar(&cfg.ControlAddr, "control-addr", ":7223", "address for the agent websocket control plane")
	flag.StringVar(&cfg.PublicAddr, "public-addr", ":8080", "address for inbound public traffic")
	flag.StringVar(&cfg.Token, "token", os.Getenv("DEVPROXY_TOKEN"), "shared secret expected from agents (or DEVPROXY_TOKEN)")
	flag.StringVar(&cfg.TLSCert, "tls-cert", "", "TLS certificate file for the control plane (enables wss)")
	flag.StringVar(&cfg.TLSKey, "tls-key", "", "TLS key file for the control plane")
	flag.StringVar(&cfg.AdminAddr, "admin-addr", "", "address for the admin web UI, e.g. :9443 (empty disables it)")
	flag.StringVar(&cfg.AdminTLSCert, "admin-tls-cert", "", "TLS cert for the admin UI (falls back to --tls-cert)")
	flag.StringVar(&cfg.AdminTLSKey, "admin-tls-key", "", "TLS key for the admin UI (falls back to --tls-key)")
	flag.Parse()

	if *showVersion {
		fmt.Printf("devproxy-edge %s\n", buildinfo.Version)
		return
	}

	if cfg.Token == "" {
		log.Printf("edge: WARNING running without --token; any agent may connect")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := edge.New(cfg).Run(ctx); err != nil && err != context.Canceled {
		log.Fatalf("edge: %v", err)
	}
}

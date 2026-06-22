// Command agent runs Server 1: the side that runs inside the no-ingress
// container. It dials out to the edge and forwards tunnelled traffic to a local
// target service.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/alertd/devproxy/internal/agent"
	"github.com/alertd/devproxy/internal/buildinfo"
)

func main() {
	cfg := agent.Config{}
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.StringVar(&cfg.EdgeURL, "edge-url", "", "edge tunnel endpoint, e.g. ws://edge:7000/tunnel or wss://...")
	flag.StringVar(&cfg.Target, "target", "", "local service to forward to, e.g. 127.0.0.1:8080")
	flag.StringVar(&cfg.Token, "token", os.Getenv("DEVPROXY_TOKEN"), "shared secret presented to the edge (or DEVPROXY_TOKEN)")
	flag.BoolVar(&cfg.Insecure, "insecure", false, "skip TLS verification (wss with self-signed certs; dev only)")
	flag.StringVar(&cfg.Shell, "shell", "", "shell for admin-console commands, e.g. \"bash -lc\" (default: bash on Unix, cmd on Windows)")
	flag.Parse()

	if *showVersion {
		fmt.Printf("devproxy-agent %s\n", buildinfo.Version)
		return
	}

	if cfg.EdgeURL == "" || cfg.Target == "" {
		log.Fatalf("agent: --edge-url and --target are required")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := agent.New(cfg).Run(ctx); err != nil && err != context.Canceled {
		log.Fatalf("agent: %v", err)
	}
}

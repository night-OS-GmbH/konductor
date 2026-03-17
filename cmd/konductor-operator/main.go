package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/night-OS-GmbH/konductor/internal/hetzner"
	"github.com/night-OS-GmbH/konductor/internal/operator"
	"github.com/night-OS-GmbH/konductor/internal/talos"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "konductor-operator: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// --- Hetzner provider ---
	hcloudToken := os.Getenv("HCLOUD_TOKEN")
	if hcloudToken == "" {
		return fmt.Errorf("HCLOUD_TOKEN environment variable is required")
	}

	hetznerClient, err := hetzner.NewClient(hcloudToken)
	if err != nil {
		return fmt.Errorf("creating Hetzner client: %w", err)
	}
	prov := hetzner.NewProvider(hetznerClient)

	// --- Talos client ---
	talosConfigPath := os.Getenv("TALOS_CONFIG_PATH")
	if talosConfigPath == "" {
		talosConfigPath = "/var/run/secrets/talos.dev/config"
	}

	var talosOpts []talos.ClientOption
	if endpoint := os.Getenv("TALOS_ENDPOINT"); endpoint != "" {
		talosOpts = append(talosOpts, talos.WithEndpoint(endpoint))
	}

	talosClient := talos.NewClient(talosConfigPath, talosOpts...)

	// --- Operator config ---
	cfg := operator.OperatorConfig{
		Provider:         prov,
		TalosClient:      talosClient,
		LeaderElection:   os.Getenv("LEADER_ELECTION") != "false",
		LeaderElectionID: envOrDefault("LEADER_ELECTION_ID", "konductor-operator-leader"),
	}

	return operator.Run(ctx, cfg)
}

// envOrDefault returns the value of the environment variable or a fallback default.
func envOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

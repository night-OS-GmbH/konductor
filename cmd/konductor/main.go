package main

import (
	"fmt"
	"os"

	"github.com/night-OS-GmbH/konductor/internal/config"
	"github.com/night-OS-GmbH/konductor/internal/tui"
	"github.com/night-OS-GmbH/konductor/pkg/version"
	"github.com/spf13/cobra"
)

var (
	flagKubeconfig string
	flagContext    string
)

func main() {
	root := &cobra.Command{
		Use:   "konductor",
		Short: "Kubernetes cluster management for Talos + Hetzner",
		Long:  "Konductor — intelligent cluster management, scaling, and health monitoring for Talos Linux on Hetzner Cloud.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			return tui.Run(cfg)
		},
	}

	root.PersistentFlags().StringVar(&flagKubeconfig, "kubeconfig", "", "path to kubeconfig file")
	root.PersistentFlags().StringVar(&flagContext, "context", "", "kubeconfig context to use")

	root.AddCommand(uiCmd())
	root.AddCommand(statusCmd())
	root.AddCommand(nodesCmd())
	root.AddCommand(scaleCmd())
	root.AddCommand(versionCmd())
	root.AddCommand(operatorCmd())

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func loadConfig() (*config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}

	// Override kubeconfig from flag.
	if flagKubeconfig != "" && len(cfg.Clusters) > 0 {
		cfg.Clusters[0].Kubeconfig = flagKubeconfig
	}

	return cfg, nil
}

func uiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ui",
		Short: "Launch the interactive TUI dashboard",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			return tui.Run(cfg)
		},
	}
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show cluster status",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			_ = cfg
			fmt.Println("Cluster status: coming soon")
			return nil
		},
	}
}

func nodesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "nodes",
		Short: "List cluster nodes",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("Node listing: coming soon")
			return nil
		},
	}
}

func scaleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scale",
		Short: "Scale cluster nodes",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "up [count]",
		Short: "Scale up by adding nodes",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("Scale up: coming soon")
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "down [count]",
		Short: "Scale down by removing nodes",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("Scale down: coming soon")
			return nil
		},
	})

	return cmd
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("konductor %s\n", version.Version)
		},
	}
}

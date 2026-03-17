package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/night-OS-GmbH/konductor/internal/installer"
	"github.com/spf13/cobra"
)

func operatorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "operator",
		Short: "Manage the Konductor operator lifecycle",
		Long:  "Install, uninstall, and check the status of the Konductor operator running in-cluster.",
	}

	cmd.AddCommand(operatorInstallCmd())
	cmd.AddCommand(operatorUninstallCmd())
	cmd.AddCommand(operatorStatusCmd())
	cmd.AddCommand(operatorSetupImageCmd())

	return cmd
}

func operatorInstallCmd() *cobra.Command {
	var (
		namespace    string
		image        string
		hetznerToken string
		talosConfig  string
		dryRun       bool
	)

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install the Konductor operator into the cluster",
		Long: `Install the Konductor operator along with its CRDs, RBAC rules,
and required secrets into the target Kubernetes cluster.

The Hetzner Cloud token can be provided via --hetzner-token flag or
the HCLOUD_TOKEN environment variable.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Resolve Hetzner token from flag or environment.
			token := hetznerToken
			if token == "" {
				token = os.Getenv("HCLOUD_TOKEN")
			}
			if token == "" && !dryRun {
				return fmt.Errorf("Hetzner Cloud token is required: set --hetzner-token or HCLOUD_TOKEN env var")
			}

			// Read Talos worker config from file.
			var talosConfigContent string
			if talosConfig != "" {
				data, err := os.ReadFile(talosConfig)
				if err != nil {
					return fmt.Errorf("reading talos config from %s: %w", talosConfig, err)
				}
				talosConfigContent = string(data)
			} else if !dryRun {
				return fmt.Errorf("Talos worker config is required: set --talos-config to the path of the worker machine config")
			}

			// Create installer to determine the active context.
			inst, err := installer.NewInstaller(flagKubeconfig, flagContext)
			if err != nil {
				return fmt.Errorf("connecting to cluster: %w", err)
			}

			// Confirmation prompt (skipped in dry-run mode).
			if !dryRun {
				fmt.Printf("Install Konductor operator to cluster %q (namespace: %s)? [y/N] ", inst.ActiveContext(), namespace)
				reader := bufio.NewReader(os.Stdin)
				answer, _ := reader.ReadString('\n')
				answer = strings.TrimSpace(strings.ToLower(answer))
				if answer != "y" && answer != "yes" {
					fmt.Println("Installation cancelled.")
					return nil
				}
			}

			opts := installer.InstallOptions{
				Kubeconfig:  flagKubeconfig,
				Context:     flagContext,
				Namespace:   namespace,
				Image:       image,
				HCloudToken: token,
				TalosConfig: talosConfigContent,
				DryRun:      dryRun,
			}

			if dryRun {
				fmt.Println("# Dry run — manifests that would be applied:")
				fmt.Println()
			} else {
				fmt.Printf("Installing Konductor operator...\n\n")
			}

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			if err := inst.Install(ctx, opts); err != nil {
				return fmt.Errorf("installation failed: %w", err)
			}

			if !dryRun {
				fmt.Printf("\nKonductor operator installed successfully in namespace %q.\n", namespace)
				fmt.Println("Run 'konductor operator status' to verify the deployment.")
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&namespace, "namespace", installer.DefaultNamespace, "namespace for the operator")
	cmd.Flags().StringVar(&image, "image", installer.DefaultImage, "operator container image")
	cmd.Flags().StringVar(&hetznerToken, "hetzner-token", "", "Hetzner Cloud API token (or set HCLOUD_TOKEN)")
	cmd.Flags().StringVar(&talosConfig, "talos-config", "", "path to Talos worker machine config file")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print rendered manifests without applying")

	return cmd
}

func operatorUninstallCmd() *cobra.Command {
	var namespace string

	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the Konductor operator from the cluster",
		Long: `Uninstall the Konductor operator and all associated resources (CRDs, RBAC,
secrets, namespace) from the target Kubernetes cluster.

WARNING: This will also remove all NodePool and NodeClaim custom resources.
Existing nodes will not be terminated, but autoscaling will stop.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			inst, err := installer.NewInstaller(flagKubeconfig, flagContext)
			if err != nil {
				return fmt.Errorf("connecting to cluster: %w", err)
			}

			fmt.Printf("Uninstall Konductor operator from cluster %q (namespace: %s)? [y/N] ", inst.ActiveContext(), namespace)
			reader := bufio.NewReader(os.Stdin)
			answer, _ := reader.ReadString('\n')
			answer = strings.TrimSpace(strings.ToLower(answer))
			if answer != "y" && answer != "yes" {
				fmt.Println("Uninstallation cancelled.")
				return nil
			}

			fmt.Printf("Uninstalling Konductor operator...\n\n")

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			if err := inst.Uninstall(ctx, namespace); err != nil {
				return fmt.Errorf("uninstallation failed: %w", err)
			}

			fmt.Println("\nKonductor operator uninstalled successfully.")
			return nil
		},
	}

	cmd.Flags().StringVar(&namespace, "namespace", installer.DefaultNamespace, "namespace of the operator")

	return cmd
}

func operatorStatusCmd() *cobra.Command {
	var namespace string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the status of the Konductor operator",
		RunE: func(cmd *cobra.Command, args []string) error {
			inst, err := installer.NewInstaller(flagKubeconfig, flagContext)
			if err != nil {
				return fmt.Errorf("connecting to cluster: %w", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			status, err := inst.Status(ctx, namespace)
			if err != nil {
				return fmt.Errorf("checking operator status: %w", err)
			}

			fmt.Printf("Cluster:    %s\n", inst.ActiveContext())
			fmt.Printf("Namespace:  %s\n", status.Namespace)

			if !status.Installed {
				fmt.Println("Status:     Not installed")
				fmt.Println("\nRun 'konductor operator install' to deploy the operator.")
				return nil
			}

			if status.Ready {
				fmt.Println("Status:     Running")
			} else {
				fmt.Println("Status:     Not ready")
			}

			if status.Version != "" {
				fmt.Printf("Version:    %s\n", status.Version)
			}

			fmt.Printf("NodePools:  %d\n", status.NodePools)
			fmt.Printf("NodeClaims: %d\n", status.NodeClaims)

			return nil
		},
	}

	cmd.Flags().StringVar(&namespace, "namespace", installer.DefaultNamespace, "namespace of the operator")

	return cmd
}

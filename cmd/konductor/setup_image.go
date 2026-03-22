package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/night-OS-GmbH/konductor/internal/hetzner"
	"github.com/night-OS-GmbH/konductor/internal/k8s"
	"github.com/spf13/cobra"
)

func operatorSetupImageCmd() *cobra.Command {
	var (
		talosVersion string
		arch         string
		schematicID  string
		hcloudToken  string
		autoDetect   bool
	)

	cmd := &cobra.Command{
		Use:   "setup-image",
		Short: "Create a Talos Linux snapshot on Hetzner Cloud",
		Long: `Creates a Hetzner Cloud snapshot from the Talos Image Factory.
The operator uses this snapshot to provision new nodes without ISO mounting.

By default, the Talos version is auto-detected from the current cluster.
Use --talos-version to override, or --no-detect to skip auto-detection.

This needs to be run once per Talos version. When upgrading Talos,
run this command again with the new version to create a fresh snapshot.

Uses the global --kubeconfig and --context flags to determine which cluster.

Examples:
  # Auto-detect version from current cluster
  konductor operator setup-image

  # Specific cluster context
  konductor --context nos-prod operator setup-image

  # Override version manually
  konductor operator setup-image --talos-version v1.12.5

  # For a Talos upgrade: create new image, then upgrade nodes
  konductor operator setup-image --talos-version v1.12.5
  talosctl upgrade --nodes <IP> --image factory.talos.dev/installer/...:v1.12.5`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if hcloudToken == "" {
				hcloudToken = os.Getenv("HCLOUD_TOKEN")
			}
			if hcloudToken == "" {
				return fmt.Errorf("--hetzner-token or HCLOUD_TOKEN environment variable required")
			}

			// Auto-detect Talos version from cluster if not specified.
			if talosVersion == "" && autoDetect {
				fmt.Printf("Detecting Talos version from cluster")
				if flagContext != "" {
					fmt.Printf(" (context: %s)", flagContext)
				}
				fmt.Println("...")

				client, err := k8s.Connect(k8s.ConnectOptions{
					Kubeconfig: flagKubeconfig,
					Context:    flagContext,
				})
				if err != nil {
					return fmt.Errorf("connecting to cluster for version detection: %w\nUse --talos-version to specify manually", err)
				}

				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				ver, err := client.GetTalosVersion(ctx)
				cancel()
				if err != nil || ver == "" {
					return fmt.Errorf("could not detect Talos version from cluster\nUse --talos-version to specify manually")
				}

				talosVersion = ver
				fmt.Printf("Detected Talos %s\n\n", talosVersion)
			}

			if talosVersion == "" {
				return fmt.Errorf("--talos-version is required (or connect to a Talos cluster for auto-detection)")
			}

			hcloudClient, err := hetzner.NewClient(hcloudToken)
			if err != nil {
				return fmt.Errorf("creating Hetzner client: %w", err)
			}
			prov := hetzner.NewProvider(hcloudClient)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()

			// Check if snapshot already exists.
			existing, err := prov.FindTalosImage(ctx, talosVersion, arch)
			if err != nil {
				return fmt.Errorf("checking for existing image: %w", err)
			}
			if existing != nil {
				fmt.Printf("Talos %s (%s) snapshot already exists:\n", talosVersion, arch)
				fmt.Printf("  Image ID:  %d\n", existing.ID)
				fmt.Printf("  Created:   %s\n", existing.Created.Format("2006-01-02 15:04"))
				fmt.Println("\nNo action needed. To force recreation, delete the snapshot first.")
				return nil
			}

			fmt.Printf("Creating Talos %s (%s) snapshot on Hetzner Cloud...\n\n", talosVersion, arch)

			result, err := prov.CreateTalosImage(ctx, hetzner.ImageCreateOpts{
				TalosVersion: talosVersion,
				Arch:         arch,
				SchematicID:  schematicID,
				OnProgress: func(step, total int, message string) {
					fmt.Printf("[%d/%d] %s\n", step, total, message)
				},
			})
			if err != nil {
				return err
			}

			fmt.Printf("\nTalos snapshot created successfully!\n\n")
			fmt.Printf("  Image ID:      %d\n", result.ImageID)
			fmt.Printf("  Talos Version: %s\n", result.TalosVersion)
			fmt.Printf("  Architecture:  %s\n", result.Arch)
			fmt.Printf("  Labels:        os=talos, talos-version=%s, arch=%s\n\n", result.TalosVersion, result.Arch)
			fmt.Println("The operator will find and use this snapshot automatically.")

			return nil
		},
	}

	cmd.Flags().StringVar(&talosVersion, "talos-version", "", "Talos version (auto-detected from cluster if empty)")
	cmd.Flags().StringVar(&arch, "arch", "amd64", "architecture (amd64 or arm64)")
	cmd.Flags().StringVar(&schematicID, "schematic-id", hetzner.DefaultSchematicID, "Talos Image Factory schematic ID")
	cmd.Flags().StringVar(&hcloudToken, "hetzner-token", "", "Hetzner Cloud API token (or HCLOUD_TOKEN env)")
	cmd.Flags().BoolVar(&autoDetect, "detect", true, "auto-detect Talos version from cluster")

	return cmd
}

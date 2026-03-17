package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/night-OS-GmbH/konductor/internal/hetzner"
	"github.com/night-OS-GmbH/konductor/internal/k8s"
	"github.com/spf13/cobra"
)

// defaultSchematicID is the vanilla Talos schematic (no extensions).
const defaultSchematicID = "376567988ad370138ad8b2698212367b8edcb69b5fd68c80be1f2ec7d603b4ba"

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

			imageURL := fmt.Sprintf("https://factory.talos.dev/image/%s/%s/hcloud-%s.raw.xz",
				schematicID, talosVersion, arch)

			fmt.Printf("Creating Talos %s (%s) snapshot on Hetzner Cloud...\n\n", talosVersion, arch)

			// Step 1: Create temporary server.
			fmt.Println("[1/6] Creating temporary server...")
			tempServer, _, err := hcloudClient.API().Server.Create(ctx, hcloud.ServerCreateOpts{
				Name:       "konductor-talos-builder",
				ServerType: &hcloud.ServerType{Name: "cx22"},
				Image:      &hcloud.Image{Name: "debian-12"},
				Location:   &hcloud.Location{Name: "nbg1"},
				Labels:     map[string]string{"purpose": "talos-snapshot-builder"},
			})
			if err != nil {
				return fmt.Errorf("creating temp server: %w", err)
			}
			serverID := tempServer.Server.ID
			serverIP := ""
			if tempServer.Server.PublicNet.IPv4.IP != nil {
				serverIP = tempServer.Server.PublicNet.IPv4.IP.String()
			}

			// Always clean up the temporary server.
			defer func() {
				fmt.Println("\n[6/6] Cleaning up temporary server...")
				cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cleanCancel()
				_, _, _ = hcloudClient.API().Server.DeleteWithResult(cleanCtx, &hcloud.Server{ID: serverID})
				fmt.Println("Temporary server deleted.")
			}()

			// Step 2: Enable rescue mode.
			fmt.Println("[2/6] Enabling rescue mode...")
			rescueResult, _, err := hcloudClient.API().Server.EnableRescue(ctx, &hcloud.Server{ID: serverID}, hcloud.ServerEnableRescueOpts{
				Type: hcloud.ServerRescueTypeLinux64,
			})
			if err != nil {
				return fmt.Errorf("enabling rescue mode: %w", err)
			}

			// Step 3: Reboot into rescue.
			fmt.Println("[3/6] Rebooting into rescue mode...")
			_, _, err = hcloudClient.API().Server.Reset(ctx, &hcloud.Server{ID: serverID})
			if err != nil {
				return fmt.Errorf("resetting server: %w", err)
			}

			fmt.Println("    Waiting for rescue system (30s)...")
			time.Sleep(30 * time.Second)

			// Step 4: User writes image via SSH.
			fmt.Println("[4/6] Write Talos image to disk")
			fmt.Println()
			fmt.Println("    Run this command in another terminal:")
			fmt.Println()
			fmt.Printf("    ssh -o StrictHostKeyChecking=no root@%s \\\n", serverIP)
			fmt.Printf("      'wget -qO- %s | xz -d | dd of=/dev/sda bs=4M && sync'\n", imageURL)
			fmt.Println()
			fmt.Printf("    Root password: %s\n", rescueResult.RootPassword)
			fmt.Println()
			fmt.Print("    Press Enter after the image has been written... ")
			fmt.Scanln()

			// Step 5: Power off and snapshot.
			fmt.Println("[5/6] Creating snapshot...")
			_, _, err = hcloudClient.API().Server.Poweroff(ctx, &hcloud.Server{ID: serverID})
			if err != nil {
				return fmt.Errorf("powering off server: %w", err)
			}
			time.Sleep(5 * time.Second)

			imgDesc := fmt.Sprintf("Talos %s %s", talosVersion, arch)
			snapshotResult, _, err := hcloudClient.API().Server.CreateImage(ctx, &hcloud.Server{ID: serverID}, &hcloud.ServerCreateImageOpts{
				Type:        hcloud.ImageTypeSnapshot,
				Description: &imgDesc,
				Labels: map[string]string{
					"os":            "talos",
					"talos-version": talosVersion,
					"arch":          arch,
				},
			})
			if err != nil {
				return fmt.Errorf("creating snapshot: %w", err)
			}

			fmt.Printf("\nTalos snapshot created successfully!\n\n")
			fmt.Printf("  Image ID:      %d\n", snapshotResult.Image.ID)
			fmt.Printf("  Talos Version: %s\n", talosVersion)
			fmt.Printf("  Architecture:  %s\n", arch)
			fmt.Printf("  Labels:        os=talos, talos-version=%s, arch=%s\n\n", talosVersion, arch)
			fmt.Println("The operator will find and use this snapshot automatically.")

			return nil
		},
	}

	cmd.Flags().StringVar(&talosVersion, "talos-version", "", "Talos version (auto-detected from cluster if empty)")
	cmd.Flags().StringVar(&arch, "arch", "amd64", "architecture (amd64 or arm64)")
	cmd.Flags().StringVar(&schematicID, "schematic-id", defaultSchematicID, "Talos Image Factory schematic ID")
	cmd.Flags().StringVar(&hcloudToken, "hetzner-token", "", "Hetzner Cloud API token (or HCLOUD_TOKEN env)")
	cmd.Flags().BoolVar(&autoDetect, "detect", true, "auto-detect Talos version from cluster")

	return cmd
}

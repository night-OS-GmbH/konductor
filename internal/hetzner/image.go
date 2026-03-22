package hetzner

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"golang.org/x/crypto/ssh"
)

// DefaultSchematicID is the vanilla Talos schematic (no extensions).
const DefaultSchematicID = "376567988ad370138ad8b2698212367b8edcb69b5fd68c80be1f2ec7d603b4ba"

// ImageCreateOpts holds parameters for creating a Talos snapshot on Hetzner Cloud.
type ImageCreateOpts struct {
	// TalosVersion is the Talos release to snapshot (e.g. "v1.11.5").
	TalosVersion string

	// Arch is the CPU architecture: "amd64" or "arm64".
	Arch string

	// SchematicID is the Talos Image Factory schematic. Defaults to DefaultSchematicID.
	SchematicID string

	// Location is the Hetzner datacenter for the temporary build server. Defaults to "nbg1".
	Location string

	// OnProgress is an optional callback invoked at each step boundary.
	OnProgress func(step, total int, message string)
}

// ImageCreateResult holds the result of a successful snapshot creation.
type ImageCreateResult struct {
	ImageID      int64
	TalosVersion string
	Arch         string
}

// CreateTalosImage creates a Talos Linux snapshot on Hetzner Cloud by:
//  1. Creating a temporary server
//  2. Enabling rescue mode
//  3. Rebooting into rescue
//  4. Writing the Talos image via automated SSH
//  5. Powering off and creating a labeled snapshot
//  6. Cleaning up the temporary server
//
// The entire process takes 3-5 minutes and requires no manual intervention.
func (p *HetznerProvider) CreateTalosImage(ctx context.Context, opts ImageCreateOpts) (*ImageCreateResult, error) {
	if opts.TalosVersion == "" {
		return nil, fmt.Errorf("TalosVersion is required")
	}
	if opts.Arch == "" {
		opts.Arch = "amd64"
	}
	if opts.SchematicID == "" {
		opts.SchematicID = DefaultSchematicID
	}
	if opts.Location == "" {
		opts.Location = "nbg1"
	}

	progress := opts.OnProgress
	if progress == nil {
		progress = func(int, int, string) {}
	}

	const totalSteps = 6

	// Step 1: Create temporary server.
	progress(1, totalSteps, "Creating temporary server...")
	serverType := builderServerType(opts.Arch)
	tempServer, _, err := p.client.api.Server.Create(ctx, hcloud.ServerCreateOpts{
		Name:       "konductor-talos-builder",
		ServerType: &hcloud.ServerType{Name: serverType},
		Image:      &hcloud.Image{Name: "debian-12"},
		Location:   &hcloud.Location{Name: opts.Location},
		Labels:     map[string]string{"purpose": "talos-snapshot-builder"},
		// PublicNet intentionally omitted — Hetzner defaults to enabling
		// public IPv4+IPv6 when the field is absent from the request.
	})
	if err != nil {
		return nil, fmt.Errorf("creating temp server: %w", err)
	}

	serverID := tempServer.Server.ID

	// Always clean up the temporary server.
	defer func() {
		progress(totalSteps, totalSteps, "Cleaning up temporary server...")
		cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanCancel()
		_, _, _ = p.client.api.Server.DeleteWithResult(cleanCtx, &hcloud.Server{ID: serverID})
	}()

	// Wait for server to be fully running before enabling rescue.
	progress(1, totalSteps, "Waiting for server to be ready...")
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timeout waiting for server to start: %w", ctx.Err())
		default:
		}
		srv, _, err := p.client.api.Server.GetByID(ctx, serverID)
		if err != nil {
			return nil, fmt.Errorf("polling server status: %w", err)
		}
		if srv != nil && srv.Status == hcloud.ServerStatusRunning {
			break
		}
		time.Sleep(3 * time.Second)
	}

	// Re-fetch server to get the assigned public IP.
	srv, _, err := p.client.api.Server.GetByID(ctx, serverID)
	if err != nil {
		return nil, fmt.Errorf("fetching server details: %w", err)
	}
	serverIP := ""
	if srv.PublicNet.IPv4.IP != nil {
		serverIP = srv.PublicNet.IPv4.IP.String()
	}
	if serverIP == "" {
		return nil, fmt.Errorf("server has no public IPv4 — check Hetzner project network settings")
	}

	// Step 2: Enable rescue mode.
	progress(2, totalSteps, "Enabling rescue mode...")
	rescueResult, _, err := p.client.api.Server.EnableRescue(ctx, &hcloud.Server{ID: serverID}, hcloud.ServerEnableRescueOpts{
		Type: hcloud.ServerRescueTypeLinux64,
	})
	if err != nil {
		return nil, fmt.Errorf("enabling rescue mode: %w", err)
	}

	// Step 3: Reboot into rescue.
	progress(3, totalSteps, "Rebooting into rescue mode...")
	_, _, err = p.client.api.Server.Reset(ctx, &hcloud.Server{ID: serverID})
	if err != nil {
		return nil, fmt.Errorf("resetting server: %w", err)
	}

	// Step 4: Write Talos image via automated SSH.
	progress(4, totalSteps, "Writing Talos image to disk (this takes 1-2 minutes)...")

	sshConfig := &ssh.ClientConfig{
		User: "root",
		Auth: []ssh.AuthMethod{
			ssh.Password(rescueResult.RootPassword),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec // temporary throwaway server
		Timeout:         10 * time.Second,
	}

	sshClient, err := waitForSSH(ctx, serverIP+":22", sshConfig)
	if err != nil {
		return nil, fmt.Errorf("connecting to rescue system via SSH: %w", err)
	}
	defer sshClient.Close()

	imageURL := fmt.Sprintf("https://factory.talos.dev/image/%s/%s/hcloud-%s.raw.xz",
		opts.SchematicID, opts.TalosVersion, opts.Arch)

	session, err := sshClient.NewSession()
	if err != nil {
		return nil, fmt.Errorf("creating SSH session: %w", err)
	}
	defer session.Close()

	writeCmd := fmt.Sprintf("wget -qO- '%s' | xz -d | dd of=/dev/sda bs=4M 2>&1 && sync", imageURL)
	output, err := session.CombinedOutput(writeCmd)
	if err != nil {
		return nil, fmt.Errorf("writing Talos image: %w\nOutput: %s", err, string(output))
	}

	// Step 5: Power off and create snapshot.
	progress(5, totalSteps, "Creating snapshot...")
	_, _, err = p.client.api.Server.Poweroff(ctx, &hcloud.Server{ID: serverID})
	if err != nil {
		return nil, fmt.Errorf("powering off server: %w", err)
	}
	time.Sleep(5 * time.Second)

	imgDesc := fmt.Sprintf("Talos %s %s", opts.TalosVersion, opts.Arch)
	snapshotResult, _, err := p.client.api.Server.CreateImage(ctx, &hcloud.Server{ID: serverID}, &hcloud.ServerCreateImageOpts{
		Type:        hcloud.ImageTypeSnapshot,
		Description: &imgDesc,
		Labels: map[string]string{
			"os":            "talos",
			"talos-version": opts.TalosVersion,
			"arch":          opts.Arch,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("creating snapshot: %w", err)
	}

	imageID := snapshotResult.Image.ID

	// Wait for the snapshot to become available before returning.
	// The operator filters for available images, so we must not return early.
	progress(5, totalSteps, "Waiting for snapshot to become available...")
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timeout waiting for snapshot %d to become available: %w", imageID, ctx.Err())
		default:
		}
		img, _, err := p.client.api.Image.GetByID(ctx, imageID)
		if err != nil {
			return nil, fmt.Errorf("polling snapshot status: %w", err)
		}
		if img != nil && img.Status == hcloud.ImageStatusAvailable {
			break
		}
		time.Sleep(5 * time.Second)
	}

	return &ImageCreateResult{
		ImageID:      imageID,
		TalosVersion: opts.TalosVersion,
		Arch:         opts.Arch,
	}, nil
}

// waitForSSH polls the SSH port until the rescue system is reachable.
// Returns a connected SSH client or an error if the context expires.
func waitForSSH(ctx context.Context, addr string, config *ssh.ClientConfig) (*ssh.Client, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timeout waiting for SSH on %s: %w", addr, ctx.Err())
		default:
		}

		client, err := ssh.Dial("tcp", addr, config)
		if err == nil {
			return client, nil
		}

		// Check if it's a connection refused / timeout (expected while booting).
		if netErr, ok := err.(*net.OpError); ok && netErr.Timeout() {
			time.Sleep(3 * time.Second)
			continue
		}

		// SSH handshake errors during boot are expected — retry.
		time.Sleep(3 * time.Second)
	}
}

// ArchFromServerType derives the CPU architecture from the Hetzner server type.
// Hetzner ARM servers use the "cax" prefix; everything else is amd64.
func ArchFromServerType(serverType string) string {
	if strings.HasPrefix(serverType, "cax") {
		return "arm64"
	}
	return "amd64"
}

// builderServerType returns the Hetzner server type to use for building
// Talos snapshots. ARM images must be built on ARM hardware.
func builderServerType(arch string) string {
	if arch == "arm64" {
		return "cax11"
	}
	return "cx23"
}

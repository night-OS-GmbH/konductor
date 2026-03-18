package components

import (
	"context"
	"fmt"

	"github.com/night-OS-GmbH/konductor/internal/cluster"
	"github.com/night-OS-GmbH/konductor/internal/installer"
)

// KonductorOperator is a thin wrapper around the existing installer package
// that implements the Component interface for consistent health checking.
type KonductorOperator struct {
	installer  *installer.Installer
	namespace  string
	kubeconfig string
	context    string
}

// NewKonductorOperator creates a KonductorOperator component manager.
// It wraps the existing installer for install/status operations.
func NewKonductorOperator(inst *installer.Installer, namespace string) *KonductorOperator {
	if namespace == "" {
		namespace = installer.DefaultNamespace
	}
	return &KonductorOperator{
		installer: inst,
		namespace: namespace,
	}
}

func (k *KonductorOperator) Name() string {
	return "konductor-operator"
}

func (k *KonductorOperator) Description() string {
	return "In-cluster operator for automated node pool scaling and lifecycle management"
}

// Check delegates to the existing installer.Status() and maps the result
// to a ComponentStatus.
func (k *KonductorOperator) Check(ctx context.Context) (*cluster.ComponentStatus, error) {
	recommended := cluster.RecommendedVersions["konductor-operator"]

	status := &cluster.ComponentStatus{
		Name:          k.Name(),
		Description:   k.Description(),
		LatestVersion: recommended,
		Installable:   true,
	}

	opStatus, err := k.installer.Status(ctx, k.namespace)
	if err != nil {
		return status, fmt.Errorf("checking operator status: %w", err)
	}

	status.Installed = opStatus.Installed
	status.Healthy = opStatus.Ready
	status.Version = opStatus.Version
	// Only flag as outdated if both versions are semver and installed is older.
	// Non-semver tags like "main" or "latest" are not comparable.
	status.NeedsUpdate = status.Version != "" && cluster.VersionOlderThan(status.Version, recommended)

	return status, nil
}

// Install delegates to the existing installer.Install().
// Supported opts keys:
//   - "hcloud_token": Hetzner Cloud API token (required)
//   - "talos_config": Raw Talos worker machine config YAML (required)
//   - "image": Operator container image (optional, defaults to installer default)
func (k *KonductorOperator) Install(ctx context.Context, opts map[string]string) error {
	installOpts := installer.InstallOptions{
		Namespace:   k.namespace,
		HCloudToken: opts["hcloud_token"],
		TalosConfig: opts["talos_config"],
	}
	if img, ok := opts["image"]; ok && img != "" {
		installOpts.Image = img
	}

	return k.installer.Install(ctx, installOpts)
}

// Update reinstalls the operator with the latest image while preserving
// existing secrets. It reads the current secret values from the cluster
// before reapplying manifests.
func (k *KonductorOperator) Update(ctx context.Context) error {
	// Read existing secrets so we don't overwrite them with empty values.
	token, talosConfig, _ := k.installer.ReadSecrets(ctx, k.namespace)

	return k.installer.Install(ctx, installer.InstallOptions{
		Namespace:   k.namespace,
		Image:       installer.DefaultImage,
		HCloudToken: token,
		TalosConfig: talosConfig,
	})
}

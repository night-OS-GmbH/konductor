// Package cluster provides health checking and component management
// for Talos Linux clusters running on Hetzner Cloud.
package cluster

import "context"

// ComponentStatus describes the current state of a cluster component.
type ComponentStatus struct {
	// Name is the human-readable component name.
	Name string

	// Installed indicates whether the component is deployed in the cluster.
	Installed bool

	// Healthy indicates whether the component is running and ready.
	Healthy bool

	// Version is the currently installed version (e.g. image tag).
	Version string

	// LatestVersion is the recommended/latest known version.
	LatestVersion string

	// NeedsUpdate is true when Version differs from LatestVersion.
	NeedsUpdate bool

	// Installable indicates whether the component can be installed
	// from Konductor (some components may require manual setup).
	Installable bool

	// Description is a short explanation of what the component does.
	Description string
}

// Component is the interface that all managed cluster components implement.
// Each component knows how to check its own status, install itself, and
// update to the latest version.
type Component interface {
	// Name returns the unique identifier for this component.
	Name() string

	// Description returns a human-readable description of the component.
	Description() string

	// Check inspects the cluster and returns the component's current status.
	Check(ctx context.Context) (*ComponentStatus, error)

	// Install deploys the component into the cluster.
	// The opts map allows passing component-specific configuration
	// (e.g. API tokens, feature flags).
	Install(ctx context.Context, opts map[string]string) error

	// Update upgrades the component to the latest recommended version.
	Update(ctx context.Context) error
}

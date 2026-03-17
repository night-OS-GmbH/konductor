// Package cluster provides health checking and component management
// for Talos Linux clusters running on Hetzner Cloud.
package cluster

import (
	"context"
	"strconv"
	"strings"
)

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

// VersionOlderThan returns true if version a is strictly older than version b
// using semantic version comparison. Handles "v" prefix. Returns false if
// versions are equal or a is newer than b, or if parsing fails.
func VersionOlderThan(a, b string) bool {
	parseSemver := func(v string) (major, minor, patch int, ok bool) {
		v = strings.TrimPrefix(v, "v")
		parts := strings.SplitN(v, ".", 3)
		if len(parts) < 2 {
			return 0, 0, 0, false
		}
		major, err1 := strconv.Atoi(parts[0])
		minor, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil {
			return 0, 0, 0, false
		}
		if len(parts) == 3 {
			// Strip anything after a dash (e.g. "1.2.3-rc1")
			patchStr := parts[2]
			if idx := strings.IndexByte(patchStr, '-'); idx != -1 {
				patchStr = patchStr[:idx]
			}
			patch, _ = strconv.Atoi(patchStr)
		}
		return major, minor, patch, true
	}

	aMaj, aMin, aPat, aOk := parseSemver(a)
	bMaj, bMin, bPat, bOk := parseSemver(b)
	if !aOk || !bOk {
		return false
	}

	if aMaj != bMaj {
		return aMaj < bMaj
	}
	if aMin != bMin {
		return aMin < bMin
	}
	return aPat < bPat
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

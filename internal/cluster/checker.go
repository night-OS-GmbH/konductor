package cluster

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ClusterHealth holds the overall health status of a cluster,
// including connectivity, version info, and component statuses.
type ClusterHealth struct {
	// Connected indicates whether the cluster API server is reachable.
	Connected bool

	// K8sVersion is the Kubernetes server version (e.g. "v1.32.3").
	K8sVersion string

	// TalosVersion is the Talos OS version detected from node images (e.g. "v1.11.5").
	TalosVersion string

	// Components holds the status of each registered cluster component.
	Components []ComponentStatus
}

// Checker inspects a Kubernetes cluster and evaluates the health
// of all registered components.
type Checker struct {
	clientset  kubernetes.Interface
	components []Component
}

// NewChecker creates a Checker that uses the given Kubernetes clientset
// for connectivity checks and version detection.
func NewChecker(clientset kubernetes.Interface) *Checker {
	return &Checker{
		clientset: clientset,
	}
}

// AddComponent registers a component for health checking.
// Components are checked in the order they are added.
func (c *Checker) AddComponent(comp Component) {
	c.components = append(c.components, comp)
}

// Check performs a full cluster health evaluation:
//  1. Verifies API server connectivity
//  2. Retrieves Kubernetes and Talos version information
//  3. Checks each registered component's status
//
// Component check failures are captured in the component status rather
// than aborting the entire health check — a single broken component
// should not prevent reporting on the rest.
func (c *Checker) Check(ctx context.Context) (*ClusterHealth, error) {
	health := &ClusterHealth{}

	// 1. Check connectivity.
	serverVersion, err := c.clientset.Discovery().ServerVersion()
	if err != nil {
		// Cluster unreachable — return minimal health info.
		return health, fmt.Errorf("cluster not reachable: %w", err)
	}

	health.Connected = true
	health.K8sVersion = serverVersion.GitVersion

	// 2. Detect Talos version from node OS images.
	health.TalosVersion = c.detectTalosVersion(ctx)

	// 3. Check each component.
	for _, comp := range c.components {
		status, err := comp.Check(ctx)
		if err != nil {
			// Record the component as unhealthy rather than failing
			// the entire health check.
			health.Components = append(health.Components, ComponentStatus{
				Name:        comp.Name(),
				Description: comp.Description(),
				Installed:   false,
				Healthy:     false,
				Installable: true,
			})
			continue
		}
		health.Components = append(health.Components, *status)
	}

	return health, nil
}

// detectTalosVersion inspects node OS images to find the Talos version.
// Returns an empty string if this is not a Talos cluster.
func (c *Checker) detectTalosVersion(ctx context.Context) string {
	nodes, err := c.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 5})
	if err != nil {
		return ""
	}
	for _, node := range nodes.Items {
		if ver := parseTalosVersion(node.Status.NodeInfo.OSImage); ver != "" {
			return ver
		}
	}
	return ""
}

// parseTalosVersion extracts a version string from a Talos OS image
// identifier like "Talos (v1.11.5)".
func parseTalosVersion(osImage string) string {
	if len(osImage) < 7 {
		return ""
	}
	start := -1
	for i, c := range osImage {
		if c == 'v' && i+1 < len(osImage) && osImage[i+1] >= '0' && osImage[i+1] <= '9' {
			start = i
			break
		}
	}
	if start < 0 {
		return ""
	}
	end := start + 1
	for end < len(osImage) {
		ch := osImage[end]
		if (ch >= '0' && ch <= '9') || ch == '.' {
			end++
		} else {
			break
		}
	}
	return osImage[start:end]
}

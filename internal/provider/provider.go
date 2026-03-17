package provider

import "context"

// Provider defines the interface for infrastructure providers that manage compute nodes.
// Implementations handle the lifecycle of nodes (create, delete, inspect) for a specific
// cloud provider. Konductor uses this abstraction to remain provider-agnostic.
type Provider interface {
	// CreateNode provisions a new compute node with the given options.
	// The returned ProviderNode contains the provider-assigned ID and initial status.
	CreateNode(ctx context.Context, opts CreateNodeOpts) (*ProviderNode, error)

	// DeleteNode terminates and removes a node identified by its provider ID.
	// The providerID format is provider-specific (e.g., "hcloud://12345").
	DeleteNode(ctx context.Context, providerID string) error

	// GetNode retrieves the current state of a node by its provider ID.
	GetNode(ctx context.Context, providerID string) (*ProviderNode, error)

	// ListNodes returns all nodes matching the given label selector.
	// An empty label map returns all nodes managed by this provider.
	ListNodes(ctx context.Context, labels map[string]string) ([]*ProviderNode, error)
}

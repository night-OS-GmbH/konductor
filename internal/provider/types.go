package provider

import "time"

// ProviderNodeStatus represents the lifecycle state of a provider-managed node.
type ProviderNodeStatus string

const (
	NodeStatusPending      ProviderNodeStatus = "pending"
	NodeStatusInitializing ProviderNodeStatus = "initializing"
	NodeStatusRunning      ProviderNodeStatus = "running"
	NodeStatusStopping     ProviderNodeStatus = "stopping"
	NodeStatusStopped      ProviderNodeStatus = "stopped"
	NodeStatusDeleting     ProviderNodeStatus = "deleting"
	NodeStatusDeleted      ProviderNodeStatus = "deleted"
	NodeStatusError        ProviderNodeStatus = "error"
	NodeStatusUnknown      ProviderNodeStatus = "unknown"
)

// ProviderNode represents a compute node managed by an infrastructure provider.
type ProviderNode struct {
	// ProviderID is the unique identifier in provider-specific format (e.g., "hcloud://12345").
	ProviderID string

	// Name is the human-readable server name.
	Name string

	// Status is the current lifecycle state of the node.
	Status ProviderNodeStatus

	// InternalIP is the private network IP address.
	InternalIP string

	// ExternalIP is the public IP address (may be empty if not assigned).
	ExternalIP string

	// ServerType is the provider-specific instance type (e.g., "cpx31").
	ServerType string

	// Location is the datacenter or region identifier (e.g., "fsn1").
	Location string

	// Labels are the key-value metadata tags on the server.
	Labels map[string]string

	// CreatedAt is when the server was created.
	CreatedAt time.Time
}

// CreateNodeOpts contains the parameters for creating a new node.
type CreateNodeOpts struct {
	// Name is the desired server name (should be unique within the cluster).
	Name string

	// ServerType is the instance type to provision (e.g., "cpx31", "cx41").
	ServerType string

	// Location is the datacenter or region to place the node in (e.g., "fsn1").
	Location string

	// Labels are key-value metadata tags to apply to the server.
	Labels map[string]string

	// UserData is the cloud-init or machine config to pass as user-data (e.g., Talos worker config).
	UserData string

	// SSHKeyName is the name of the SSH key to attach (optional, primarily for rescue/debug).
	SSHKeyName string

	// NetworkName is the Hetzner Cloud network to attach the server to.
	NetworkName string

	// PlacementGroupName is the optional placement group for spread scheduling.
	PlacementGroupName string
}

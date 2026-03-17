package hetzner

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"

	"github.com/night-OS-GmbH/konductor/internal/provider"
)

const (
	// providerPrefix is the scheme used in provider IDs for Hetzner Cloud.
	providerPrefix = "hcloud://"

	// LabelManagedBy marks servers as managed by konductor.
	LabelManagedBy = "konductor.nightos.dev/managed-by"
	// LabelCluster identifies which cluster a server belongs to.
	LabelCluster = "konductor.nightos.dev/cluster"
	// LabelRole identifies the node role (worker, control-plane).
	LabelRole = "konductor.nightos.dev/role"
)

// HetznerProvider implements the provider.Provider interface for Hetzner Cloud.
type HetznerProvider struct {
	client *Client
}

// NewProvider creates a new Hetzner Cloud provider backed by the given client.
func NewProvider(client *Client) *HetznerProvider {
	return &HetznerProvider{
		client: client,
	}
}

// compile-time interface check
var _ provider.Provider = (*HetznerProvider)(nil)

// CreateNode provisions a new Hetzner Cloud server with user-data, attaches it to
// the specified network, and labels it with konductor metadata.
func (p *HetznerProvider) CreateNode(ctx context.Context, opts provider.CreateNodeOpts) (*provider.ProviderNode, error) {
	serverOpts := hcloud.ServerCreateOpts{
		Name: opts.Name,
		ServerType: &hcloud.ServerType{
			Name: opts.ServerType,
		},
		Location: &hcloud.Location{
			Name: opts.Location,
		},
		UserData: opts.UserData,
		Labels:   mergeLabels(opts.Labels),
	}

	// Attach to private network if specified.
	if opts.NetworkName != "" {
		network, _, err := p.client.api.Network.GetByName(ctx, opts.NetworkName)
		if err != nil {
			return nil, fmt.Errorf("looking up network %q: %w", opts.NetworkName, err)
		}
		if network == nil {
			return nil, fmt.Errorf("network %q not found", opts.NetworkName)
		}
		serverOpts.Networks = []*hcloud.Network{network}
	}

	// Attach SSH key if specified.
	if opts.SSHKeyName != "" {
		sshKey, _, err := p.client.api.SSHKey.GetByName(ctx, opts.SSHKeyName)
		if err != nil {
			return nil, fmt.Errorf("looking up SSH key %q: %w", opts.SSHKeyName, err)
		}
		if sshKey == nil {
			return nil, fmt.Errorf("SSH key %q not found", opts.SSHKeyName)
		}
		serverOpts.SSHKeys = []*hcloud.SSHKey{sshKey}
	}

	// Assign to placement group if specified.
	if opts.PlacementGroupName != "" {
		pg, _, err := p.client.api.PlacementGroup.GetByName(ctx, opts.PlacementGroupName)
		if err != nil {
			return nil, fmt.Errorf("looking up placement group %q: %w", opts.PlacementGroupName, err)
		}
		if pg == nil {
			return nil, fmt.Errorf("placement group %q not found", opts.PlacementGroupName)
		}
		serverOpts.PlacementGroup = pg
	}

	result, _, err := p.client.api.Server.Create(ctx, serverOpts)
	if err != nil {
		return nil, fmt.Errorf("creating server %q: %w", opts.Name, err)
	}

	return serverToProviderNode(result.Server), nil
}

// DeleteNode terminates a Hetzner Cloud server by its provider ID (format: "hcloud://<serverID>").
func (p *HetznerProvider) DeleteNode(ctx context.Context, providerID string) error {
	serverID, err := parseProviderID(providerID)
	if err != nil {
		return err
	}

	server := &hcloud.Server{ID: serverID}
	_, _, err = p.client.api.Server.DeleteWithResult(ctx, server)
	if err != nil {
		return fmt.Errorf("deleting server %d: %w", serverID, err)
	}

	return nil
}

// GetNode retrieves the current state of a Hetzner Cloud server by its provider ID.
func (p *HetznerProvider) GetNode(ctx context.Context, providerID string) (*provider.ProviderNode, error) {
	serverID, err := parseProviderID(providerID)
	if err != nil {
		return nil, err
	}

	server, _, err := p.client.api.Server.GetByID(ctx, serverID)
	if err != nil {
		return nil, fmt.Errorf("getting server %d: %w", serverID, err)
	}
	if server == nil {
		return nil, fmt.Errorf("server %d not found", serverID)
	}

	return serverToProviderNode(server), nil
}

// ListNodes returns all Hetzner Cloud servers matching the given label selector.
// Labels are combined with AND logic into a Hetzner label selector string.
func (p *HetznerProvider) ListNodes(ctx context.Context, labels map[string]string) ([]*provider.ProviderNode, error) {
	opts := hcloud.ServerListOpts{
		ListOpts: hcloud.ListOpts{
			PerPage: 50,
		},
	}

	// Build label selector from the requested labels.
	// Always filter for konductor-managed servers.
	selectorParts := []string{LabelManagedBy + "=konductor"}
	for k, v := range labels {
		selectorParts = append(selectorParts, k+"="+v)
	}
	opts.ListOpts.LabelSelector = strings.Join(selectorParts, ",")

	servers, err := p.client.api.Server.AllWithOpts(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("listing servers: %w", err)
	}

	nodes := make([]*provider.ProviderNode, 0, len(servers))
	for _, s := range servers {
		nodes = append(nodes, serverToProviderNode(s))
	}

	return nodes, nil
}

// serverToProviderNode converts a Hetzner Cloud server to a provider-agnostic ProviderNode.
func serverToProviderNode(server *hcloud.Server) *provider.ProviderNode {
	node := &provider.ProviderNode{
		ProviderID: fmt.Sprintf("%s%d", providerPrefix, server.ID),
		Name:       server.Name,
		Status:     mapServerStatus(server.Status),
		Labels:     server.Labels,
		CreatedAt:  server.Created,
	}

	// Server type.
	if server.ServerType != nil {
		node.ServerType = server.ServerType.Name
	}

	// Location.
	if server.Datacenter != nil && server.Datacenter.Location != nil {
		node.Location = server.Datacenter.Location.Name
	}

	// Public IP.
	if server.PublicNet.IPv4.IP != nil {
		node.ExternalIP = server.PublicNet.IPv4.IP.String()
	}

	// Private IP (first private network).
	if len(server.PrivateNet) > 0 && server.PrivateNet[0].IP != nil {
		node.InternalIP = server.PrivateNet[0].IP.String()
	}

	return node
}

// mapServerStatus converts a Hetzner server status to a provider-agnostic status.
func mapServerStatus(status hcloud.ServerStatus) provider.ProviderNodeStatus {
	switch status {
	case hcloud.ServerStatusInitializing:
		return provider.NodeStatusInitializing
	case hcloud.ServerStatusRunning:
		return provider.NodeStatusRunning
	case hcloud.ServerStatusStarting:
		return provider.NodeStatusPending
	case hcloud.ServerStatusStopping:
		return provider.NodeStatusStopping
	case hcloud.ServerStatusOff:
		return provider.NodeStatusStopped
	case hcloud.ServerStatusDeleting:
		return provider.NodeStatusDeleting
	default:
		return provider.NodeStatusUnknown
	}
}

// mergeLabels adds the konductor management label to user-provided labels.
func mergeLabels(labels map[string]string) map[string]string {
	merged := make(map[string]string, len(labels)+1)
	for k, v := range labels {
		merged[k] = v
	}
	merged[LabelManagedBy] = "konductor"
	return merged
}

// parseProviderID extracts the Hetzner server ID from a provider ID string (format: "hcloud://<serverID>").
func parseProviderID(providerID string) (int64, error) {
	if !strings.HasPrefix(providerID, providerPrefix) {
		return 0, fmt.Errorf("invalid hetzner provider ID %q: must start with %q", providerID, providerPrefix)
	}

	idStr := strings.TrimPrefix(providerID, providerPrefix)
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid hetzner server ID %q: %w", idStr, err)
	}

	return id, nil
}

package hetzner

import (
	"fmt"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

// Client wraps the Hetzner Cloud API client with konductor-specific configuration.
type Client struct {
	api *hcloud.Client
}

// NewClient creates a new Hetzner Cloud client with the given API token.
// The token can be obtained from the Hetzner Cloud Console under API tokens.
func NewClient(token string) (*Client, error) {
	if token == "" {
		return nil, fmt.Errorf("hetzner cloud API token must not be empty")
	}

	api := hcloud.NewClient(hcloud.WithToken(token))

	return &Client{
		api: api,
	}, nil
}

// API returns the underlying hcloud client for advanced operations.
func (c *Client) API() *hcloud.Client {
	return c.api
}

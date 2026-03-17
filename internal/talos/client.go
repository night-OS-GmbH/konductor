package talos

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Client wraps Talos operations, initially via talosctl exec calls.
// Future iterations can replace this with direct Talos gRPC API calls.
type Client struct {
	// talosConfigPath is the path to the Talos client configuration file.
	talosConfigPath string

	// endpoint overrides the Talos endpoint (optional, uses config default if empty).
	endpoint string

	// commandTimeout is the maximum duration for a talosctl command.
	commandTimeout time.Duration
}

// ClientOption configures the Talos client.
type ClientOption func(*Client)

// WithEndpoint sets a specific Talos endpoint to connect to.
func WithEndpoint(endpoint string) ClientOption {
	return func(c *Client) {
		c.endpoint = endpoint
	}
}

// WithCommandTimeout sets the maximum duration for talosctl commands.
func WithCommandTimeout(d time.Duration) ClientOption {
	return func(c *Client) {
		c.commandTimeout = d
	}
}

// NewClient creates a new Talos client using talosctl under the hood.
// The configPath should point to a valid Talos client configuration file
// (typically ~/.talos/config).
func NewClient(configPath string, opts ...ClientOption) *Client {
	c := &Client{
		talosConfigPath: configPath,
		commandTimeout:  2 * time.Minute,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// ResetNode performs a graceful reset of a Talos node, which wipes the node's state
// and triggers a reboot. This is used during scale-down to cleanly remove a node
// before deleting the underlying server.
//
// The nodeIP should be the IP address of the node to reset (typically the internal IP).
func (c *Client) ResetNode(ctx context.Context, nodeIP string) error {
	args := []string{
		"reset",
		"--nodes", nodeIP,
		"--graceful=true",
		"--reboot=false",
	}

	if _, err := c.run(ctx, args...); err != nil {
		return fmt.Errorf("resetting node %s: %w", nodeIP, err)
	}

	return nil
}

// GetNodeStatus checks the health/status of a Talos node by running `talosctl health`
// against the specific node IP. Returns nil if the node is healthy.
func (c *Client) GetNodeStatus(ctx context.Context, nodeIP string) error {
	args := []string{
		"health",
		"--nodes", nodeIP,
		"--wait-timeout", "10s",
	}

	if _, err := c.run(ctx, args...); err != nil {
		return fmt.Errorf("checking health of node %s: %w", nodeIP, err)
	}

	return nil
}

// ApplyConfig applies a Talos machine configuration to a node.
// The config should be the full YAML machine configuration.
func (c *Client) ApplyConfig(ctx context.Context, nodeIP string, config string, insecure bool) error {
	args := []string{
		"apply-config",
		"--nodes", nodeIP,
		"--file", "/dev/stdin",
	}
	if insecure {
		args = append(args, "--insecure")
	}

	if _, err := c.runWithStdin(ctx, config, args...); err != nil {
		return fmt.Errorf("applying config to node %s: %w", nodeIP, err)
	}

	return nil
}

// run executes a talosctl command with the configured client settings.
func (c *Client) run(ctx context.Context, args ...string) (string, error) {
	return c.runWithStdin(ctx, "", args...)
}

// runWithStdin executes a talosctl command with optional stdin input.
func (c *Client) runWithStdin(ctx context.Context, stdin string, args ...string) (string, error) {
	// Build the full argument list with global flags.
	fullArgs := c.globalArgs()
	fullArgs = append(fullArgs, args...)

	cmdCtx, cancel := context.WithTimeout(ctx, c.commandTimeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "talosctl", fullArgs...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}

	if err := cmd.Run(); err != nil {
		stderrStr := strings.TrimSpace(stderr.String())
		if stderrStr != "" {
			return "", fmt.Errorf("%w: %s", err, stderrStr)
		}
		return "", err
	}

	return strings.TrimSpace(stdout.String()), nil
}

// globalArgs returns the talosctl flags that apply to every command.
func (c *Client) globalArgs() []string {
	var args []string

	if c.talosConfigPath != "" {
		args = append(args, "--talosconfig", c.talosConfigPath)
	}

	if c.endpoint != "" {
		args = append(args, "--endpoints", c.endpoint)
	}

	return args
}

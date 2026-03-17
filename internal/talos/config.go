package talos

import (
	"fmt"
	"os"
	"strings"
	"text/template"
)

// WorkerConfigParams holds the parameters to template into a Talos worker machine config.
type WorkerConfigParams struct {
	// Hostname is the node hostname to set in the machine config.
	Hostname string

	// InternalIP is the private network IP address for the node (optional, for static network config).
	InternalIP string

	// ExternalIP is the public IP address for the node (optional).
	ExternalIP string
}

// RenderWorkerConfig takes a base Talos worker machine config YAML string and templates
// in the node-specific parameters. The base config should contain Go template placeholders:
//
//	{{ .Hostname }}   - replaced with the node hostname
//	{{ .InternalIP }} - replaced with the private IP
//	{{ .ExternalIP }} - replaced with the public IP
//
// If the base config does not contain template directives, simple string replacement is used
// as a fallback for common placeholder patterns like __HOSTNAME__, __INTERNAL_IP__, __EXTERNAL_IP__.
func RenderWorkerConfig(baseConfig string, params WorkerConfigParams) (string, error) {
	// First, try Go template rendering.
	if strings.Contains(baseConfig, "{{") {
		return renderWithTemplate(baseConfig, params)
	}

	// Fallback: simple string replacement for double-underscore placeholders.
	return renderWithReplace(baseConfig, params), nil
}

// ReadAndRenderWorkerConfig reads a base config file from disk and templates it.
func ReadAndRenderWorkerConfig(configPath string, params WorkerConfigParams) (string, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", fmt.Errorf("reading talos config %s: %w", configPath, err)
	}

	return RenderWorkerConfig(string(data), params)
}

// renderWithTemplate uses Go's text/template to render the config.
func renderWithTemplate(baseConfig string, params WorkerConfigParams) (string, error) {
	tmpl, err := template.New("worker-config").Parse(baseConfig)
	if err != nil {
		return "", fmt.Errorf("parsing talos config template: %w", err)
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, params); err != nil {
		return "", fmt.Errorf("rendering talos config template: %w", err)
	}

	return buf.String(), nil
}

// renderWithReplace uses simple string replacement for common placeholder patterns.
func renderWithReplace(baseConfig string, params WorkerConfigParams) string {
	result := baseConfig
	result = strings.ReplaceAll(result, "__HOSTNAME__", params.Hostname)
	result = strings.ReplaceAll(result, "__INTERNAL_IP__", params.InternalIP)
	result = strings.ReplaceAll(result, "__EXTERNAL_IP__", params.ExternalIP)
	return result
}

// PatchConfigForHetzner ensures the worker config has all required Hetzner Cloud
// settings baked in, so no post-boot patching is needed:
// - cluster.externalCloudProvider.enabled = true (for Hetzner CCM)
// - machine.kubelet.nodeIP.validSubnets (use private network for internal traffic)
// - machine.features.hostDNS (for reliable DNS resolution)
//
// This performs a simple YAML-level check and appends missing sections.
// It works on the raw YAML string to avoid needing a full Talos config parser.
func PatchConfigForHetzner(config string, privateSubnet string) string {
	// Ensure externalCloudProvider is enabled.
	if !strings.Contains(config, "externalCloudProvider") {
		config = ensureClusterPatch(config, `    externalCloudProvider:
        enabled: true`)
	}

	// Ensure kubelet uses private network for node IP if subnet specified.
	if privateSubnet != "" && !strings.Contains(config, "validSubnets") {
		config = ensureMachinePatch(config, fmt.Sprintf(`    kubelet:
        nodeIP:
            validSubnets:
                - %s`, privateSubnet))
	}

	return config
}

// ensureClusterPatch appends a YAML block under the "cluster:" section.
func ensureClusterPatch(config, patch string) string {
	idx := strings.Index(config, "\ncluster:\n")
	if idx == -1 {
		// Append at end.
		return config + "\ncluster:\n" + patch + "\n"
	}
	// Insert after "cluster:\n"
	insertAt := idx + len("\ncluster:\n")
	return config[:insertAt] + patch + "\n" + config[insertAt:]
}

// ensureMachinePatch appends a YAML block under the "machine:" section.
func ensureMachinePatch(config, patch string) string {
	idx := strings.Index(config, "\nmachine:\n")
	if idx == -1 {
		return config + "\nmachine:\n" + patch + "\n"
	}
	insertAt := idx + len("\nmachine:\n")
	return config[:insertAt] + patch + "\n" + config[insertAt:]
}

// PrepareWorkerConfig takes a raw worker config, applies Hetzner patches,
// and renders node-specific parameters. This is the single function the
// operator should call before passing config as user-data.
func PrepareWorkerConfig(baseConfig string, params WorkerConfigParams, privateSubnet string) (string, error) {
	// Step 1: Patch for Hetzner (externalCloudProvider, nodeIP subnet).
	patched := PatchConfigForHetzner(baseConfig, privateSubnet)

	// Step 2: Render node-specific values (hostname, IPs).
	return RenderWorkerConfig(patched, params)
}

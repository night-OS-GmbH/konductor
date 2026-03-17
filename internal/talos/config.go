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

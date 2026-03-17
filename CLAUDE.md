# Konductor

Intelligent cluster management TUI for Talos Linux on Hetzner Cloud.

## Tech Stack
- **Language:** Go 1.24+
- **TUI:** bubbletea + lipgloss + bubbles (Charm.sh ecosystem)
- **CLI:** cobra
- **Config:** YAML (~/.konductor/config.yaml)

## Project Structure
```
cmd/konductor/          # CLI entrypoint
internal/
  config/               # Config loading (YAML)
  tui/                  # TUI application
    styles/             # Theme, colors, reusable styles
    views/              # Tab views (cluster, nodes, scaling)
    components/         # Reusable TUI components
  k8s/                  # Kubernetes client (client-go)
  hetzner/              # Hetzner Cloud API client (hcloud-go)
  talos/                # Talos API client
  operator/             # Optional in-cluster operator
    crd/                # Custom Resource Definitions
pkg/version/            # Version info (ldflags)
```

## Build
```bash
go build ./cmd/konductor/
./konductor ui
```

## Architecture
- CLI connects directly to clusters (kubeconfig, hcloud token, talos config)
- Optional operator can be installed in-cluster for deeper metrics
- TUI uses Elm Architecture (bubbletea Model-Update-View)
- Dark theme with GitHub-inspired color palette

## Design Principles
- Single binary, no runtime dependencies
- Provider-agnostic architecture (Hetzner first, extensible later)
- Security-first: restricted permissions, audit logging
- No vendor lock-in

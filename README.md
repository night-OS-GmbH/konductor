# Konductor

Intelligent Kubernetes cluster management for **Talos Linux** on **Hetzner Cloud**.

A terminal-based dashboard and autoscaling operator — monitor your cluster, manage components, and scale nodes automatically. No browser, no YAML editing, no manual steps.

## Features

### Terminal Dashboard (TUI)

- **Dashboard** — Node health tiles with CPU/MEM sparklines, deployment status cards, namespace bars, pod health, alert ticker
- **Nodes** — Real-time node metrics, resource utilization, roles, conditions
- **Namespaces** — Pod counts, resource usage per namespace, `Enter` to jump to filtered pods
- **Pods** — Full pod table with sorting (`s`), namespace filter (`h/l`), log viewer (`Enter`), fullscreen logs (`f`)
- **Cluster** — Component health checks, interactive setup wizard, autoscaling dashboard

### Cluster Management

- **Component Health Checks** — Automatically detects metrics-server, Hetzner CCM, and Konductor Operator status with version tracking
- **Interactive Setup Wizard** — Install cluster components directly from the TUI (token input, config selection, progress feedback)
- **Context Switcher** — Switch Kubernetes context without leaving the TUI (`c`)
- **Talos Version Detection** — Reads Talos version from node OS images automatically

### Autoscaling Operator

- **Custom CRDs** — `NodePool` (scaling config) and `NodeClaim` (per-node lifecycle)
- **Provider-agnostic** — Hetzner Cloud as first provider, extensible to others
- **Decision Engine** — Scale-up on pending pods or CPU/MEM threshold breach, scale-down on sustained underutilization
- **Hysteresis** — Cooldown periods, stabilization windows, no flapping
- **Paused Mode** — Observe scaling decisions without executing them (`spec.scaling.paused: true`)
- **Talos Integration** — Automatic config patching (externalCloudProvider, nodeIP subnet), snapshot-based provisioning (no ISO mounting)

## Quick Start

### Install

```bash
go install github.com/night-OS-GmbH/konductor/cmd/konductor@latest
```

Or build from source:

```bash
git clone https://github.com/night-OS-GmbH/konductor.git
cd konductor
go build -o konductor ./cmd/konductor
```

### Launch TUI

```bash
# Uses default kubeconfig
konductor

# Specific cluster
konductor --context nos-dev
```

### Configuration

Config file at `~/.konductor/config.yaml`:

```yaml
clusters:
  - name: nos-dev
    kubeconfig: ~/.kube/config
    dashboard:
      watchNamespaces:
        - nightos-dev
        - kube-system
    scaling:
      minNodes: 3
      maxNodes: 20
      defaultType: cpx31
      cooldownMinutes: 10
```

## Keyboard Shortcuts

### Global

| Key | Action |
|-----|--------|
| `1-5` | Switch tabs |
| `Tab` / `Shift+Tab` | Next / previous tab |
| `c` | Context switcher |
| `r` | Refresh data |
| `q` | Quit |

### Pods Tab

| Key | Action |
|-----|--------|
| `j/k` | Navigate pods |
| `h/l` | Switch namespace |
| `s` | Cycle sort column |
| `S` | Reverse sort direction |
| `Enter` | Pod detail + logs |
| `l` | Toggle LIVE/PAUSED |
| `f` | Fullscreen logs |
| `Esc` | Back to list |

### Cluster Tab

| Key | Action |
|-----|--------|
| `j/k` | Navigate components |
| `Enter` | Install selected component |
| `u` | Update outdated component |
| `i` | Setup all missing components |

## Operator Setup

### 1. Create Talos Snapshot (one-time per version)

```bash
# Auto-detects Talos version from cluster
konductor operator setup-image --hetzner-token=$HCLOUD_TOKEN

# Or specify version manually
konductor operator setup-image --talos-version v1.11.5 --hetzner-token=$HCLOUD_TOKEN
```

### 2. Install Operator

From CLI:

```bash
konductor operator install \
  --hetzner-token=$HCLOUD_TOKEN \
  --talos-config=path/to/worker.yaml
```

Or from the TUI: Cluster tab → select "Konductor Operator" → `Enter` → follow the wizard.

### 3. Create a NodePool

```yaml
apiVersion: konductor.io/v1alpha1
kind: NodePool
metadata:
  name: dev-workers
spec:
  provider: hetzner
  providerConfig:
    serverType: cpx31
    location: nbg1
    network: nightos
  minNodes: 3
  maxNodes: 10
  scaling:
    paused: true  # Start in observe-only mode
    scaleUp:
      cpuThresholdPercent: 80
      memoryThresholdPercent: 80
      pendingPodsThreshold: 1
      stabilizationWindowSeconds: 60
      step: 1
    scaleDown:
      cpuThresholdPercent: 30
      memoryThresholdPercent: 30
      stabilizationWindowSeconds: 600
      step: 1
    cooldownSeconds: 300
  talos:
    configSecretRef: talos-worker-config
```

### 4. Go Live

Once you've observed the operator's decisions in paused mode:

```bash
kubectl patch nodepool dev-workers -p '{"spec":{"scaling":{"paused":false}}}'
```

## Architecture

```
konductor (CLI/TUI)              konductor-operator (in-cluster)
├── Dashboard                     ├── NodePool Controller
├── Node/Pod/Namespace views      ├── NodeClaim Controller
├── Cluster Health + Wizard       ├── Decision Engine
├── Context Switcher              ├── Metrics Collector
├── Log Viewer                    └── Provider (Hetzner)
└── Operator Installer
```

**Two binaries, one repo:**

| Binary | Purpose | Runs where |
|--------|---------|------------|
| `konductor` | TUI + CLI | Your workstation |
| `konductor-operator` | Autoscaling controller | In the K8s cluster |

**Provider Interface:**

```go
type Provider interface {
    CreateNode(ctx, opts) (*ProviderNode, error)
    DeleteNode(ctx, providerID) error
    GetNode(ctx, providerID) (*ProviderNode, error)
    ListNodes(ctx, labels) ([]*ProviderNode, error)
}
```

Hetzner Cloud is the first implementation. The interface allows adding other providers.

## Managed Components

Konductor can install and manage these cluster components:

| Component | Purpose | Installed by |
|-----------|---------|-------------|
| **metrics-server** | CPU/MEM metrics for dashboard and HPA | Embedded manifest |
| **Hetzner CCM** | Cloud provider integration (node IPs, load balancers) | Embedded manifest |
| **Konductor Operator** | Node autoscaling | Embedded manifests + CRDs |

## Development

```bash
# Build both binaries
go build -o konductor ./cmd/konductor
go build -o konductor-operator ./cmd/konductor-operator

# Run tests
go test ./...

# Run TUI
./konductor
```

### Project Structure

```
cmd/
  konductor/              CLI + TUI entrypoint
  konductor-operator/     Operator entrypoint
api/v1alpha1/             CRD types (NodePool, NodeClaim)
internal/
  cluster/                Component health checks + installers
  config/                 YAML config loading
  hetzner/                Hetzner Cloud provider
  installer/              Operator manifest installer
  k8s/                    Kubernetes client layer
  operator/               Controller logic + decision engine
  provider/               Provider interface
  talos/                  Talos config + node management
  tui/                    Terminal UI (bubbletea)
    views/                Tab views (dashboard, nodes, pods, ...)
    styles/               Theme + styling
pkg/version/              Version info
```

## Requirements

- Go 1.25+
- Access to a Kubernetes cluster (kubeconfig)
- For autoscaling: Hetzner Cloud account + Talos Linux cluster

## License

Apache License 2.0 — see [LICENSE](LICENSE) for details.

Copyright 2025-2026 Night-OS GmbH

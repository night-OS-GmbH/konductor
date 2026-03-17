package tui

import (
	"github.com/night-OS-GmbH/konductor/internal/k8s"
	"github.com/night-OS-GmbH/konductor/internal/tui/views/scaling"
)

// allDataMsg carries all cluster data fetched in one batch.
type allDataMsg struct {
	nodes       []k8s.NodeInfo
	pods        []k8s.PodInfo
	namespaces  []k8s.NamespaceInfo
	deployments []k8s.DeploymentInfo
	alerts      []k8s.Alert
	k8sVersion  string
	hasMetrics  bool
	err         error
}

type tickMsg struct{}

// contextSwitchedMsg signals the client has reconnected to a new context.
type contextSwitchedMsg struct {
	client *k8s.Client
	err    error
}

// logsDataMsg carries fetched log content.
type logsDataMsg struct {
	podName   string
	namespace string
	content   string
	err       error
}

// scalingDataMsg carries operator/scaling CRD data.
type scalingDataMsg struct {
	info *k8s.ScalingInfo
	err  error
}

// clusterHealthMsg carries the result of a cluster health check.
type clusterHealthMsg struct {
	health *scaling.ClusterHealthData
	err    error
}

// installResultMsg carries the result of a component installation.
type installResultMsg struct {
	component string
	err       error
}

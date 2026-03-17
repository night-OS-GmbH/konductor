package k8s

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NodeInfo holds aggregated node information for display.
type NodeInfo struct {
	Name       string
	Status     string
	Roles      []string
	InternalIP string
	ExternalIP string
	Age        time.Duration
	Version    string

	// From node status.
	OSImage          string
	KernelVersion    string
	ContainerRuntime string

	// Labels of interest.
	InstanceType string
	Zone         string
	Arch         string

	// Resource capacity.
	CPUCapacity    resource.Quantity
	MemoryCapacity resource.Quantity
	PodCapacity    resource.Quantity

	// Resource usage (from metrics API, may be zero).
	CPUUsage    resource.Quantity
	MemoryUsage resource.Quantity

	// Computed percentages (0-100).
	CPUPercent    float64
	MemoryPercent float64

	// Conditions.
	Conditions []NodeCondition

	// Taints.
	TaintCount int
	Unschedulable bool
}

// GetTalosVersion detects the Talos version running on the cluster by inspecting
// node OS images. Returns e.g. "v1.11.5". Returns empty string if not a Talos cluster.
func (c *Client) GetTalosVersion(ctx context.Context) (string, error) {
	nodeList, err := c.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 5})
	if err != nil {
		return "", err
	}
	for _, node := range nodeList.Items {
		osImage := node.Status.NodeInfo.OSImage
		// Talos sets OSImage to "Talos (v1.11.5)" or similar.
		if ver := parseTalosVersion(osImage); ver != "" {
			return ver, nil
		}
	}
	return "", nil
}

// parseTalosVersion extracts the version from an OS image string like "Talos (v1.11.5)".
func parseTalosVersion(osImage string) string {
	// Format: "Talos (v1.11.5)"
	if len(osImage) < 7 {
		return ""
	}
	start := -1
	for i, c := range osImage {
		if c == 'v' && i+1 < len(osImage) && osImage[i+1] >= '0' && osImage[i+1] <= '9' {
			start = i
			break
		}
	}
	if start < 0 {
		return ""
	}
	// Extract until non-version character.
	end := start + 1
	for end < len(osImage) {
		c := osImage[end]
		if (c >= '0' && c <= '9') || c == '.' {
			end++
		} else {
			break
		}
	}
	return osImage[start:end]
}

type NodeCondition struct {
	Type    string
	Status  string
	Message string
}

// ClusterSummary holds aggregated cluster stats.
type ClusterSummary struct {
	TotalNodes   int
	ReadyNodes   int
	NotReadyNodes int

	TotalPods    int
	RunningPods  int
	PendingPods  int
	FailedPods   int

	TotalCPU      resource.Quantity
	UsedCPU       resource.Quantity
	TotalMemory   resource.Quantity
	UsedMemory    resource.Quantity

	CPUPercent    float64
	MemoryPercent float64

	K8sVersion   string
	HasMetrics   bool
}

// GetNodes fetches all nodes with their status and resource info.
func (c *Client) GetNodes(ctx context.Context) ([]NodeInfo, error) {
	nodeList, err := c.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing nodes: %w", err)
	}

	// Fetch metrics if available.
	var nodeMetrics map[string]struct {
		cpu    resource.Quantity
		memory resource.Quantity
	}

	if c.metrics != nil {
		metricsList, err := c.metrics.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
		if err == nil {
			nodeMetrics = make(map[string]struct {
				cpu    resource.Quantity
				memory resource.Quantity
			})
			for _, m := range metricsList.Items {
				nodeMetrics[m.Name] = struct {
					cpu    resource.Quantity
					memory resource.Quantity
				}{
					cpu:    m.Usage[corev1.ResourceCPU],
					memory: m.Usage[corev1.ResourceMemory],
				}
			}
		}
	}

	var nodes []NodeInfo
	for _, node := range nodeList.Items {
		info := nodeInfoFromNode(&node)

		// Add metrics if available.
		if nodeMetrics != nil {
			if m, ok := nodeMetrics[node.Name]; ok {
				info.CPUUsage = m.cpu
				info.MemoryUsage = m.memory
				info.CPUPercent = quantityPercent(m.cpu, info.CPUCapacity)
				info.MemoryPercent = quantityPercent(m.memory, info.MemoryCapacity)
			}
		}

		nodes = append(nodes, info)
	}

	return nodes, nil
}

// GetClusterSummary returns an aggregated cluster overview.
func (c *Client) GetClusterSummary(ctx context.Context) (*ClusterSummary, error) {
	nodes, err := c.GetNodes(ctx)
	if err != nil {
		return nil, err
	}

	summary := &ClusterSummary{
		TotalNodes: len(nodes),
		HasMetrics: c.HasMetrics(ctx),
	}

	for _, n := range nodes {
		if n.Status == "Ready" {
			summary.ReadyNodes++
		} else {
			summary.NotReadyNodes++
		}
		summary.TotalCPU.Add(n.CPUCapacity)
		summary.TotalMemory.Add(n.MemoryCapacity)
		summary.UsedCPU.Add(n.CPUUsage)
		summary.UsedMemory.Add(n.MemoryUsage)
	}

	summary.CPUPercent = quantityPercent(summary.UsedCPU, summary.TotalCPU)
	summary.MemoryPercent = quantityPercent(summary.UsedMemory, summary.TotalMemory)

	// Fetch pod stats.
	podList, err := c.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err == nil {
		summary.TotalPods = len(podList.Items)
		for _, p := range podList.Items {
			switch p.Status.Phase {
			case corev1.PodRunning:
				summary.RunningPods++
			case corev1.PodPending:
				summary.PendingPods++
			case corev1.PodFailed:
				summary.FailedPods++
			}
		}
	}

	ver, err := c.ServerVersion()
	if err == nil {
		summary.K8sVersion = ver
	}

	return summary, nil
}

func nodeInfoFromNode(node *corev1.Node) NodeInfo {
	info := NodeInfo{
		Name:             node.Name,
		Status:           nodeStatus(node),
		Roles:            nodeRoles(node),
		Age:              time.Since(node.CreationTimestamp.Time),
		Version:          node.Status.NodeInfo.KubeletVersion,
		OSImage:          node.Status.NodeInfo.OSImage,
		KernelVersion:    node.Status.NodeInfo.KernelVersion,
		ContainerRuntime: node.Status.NodeInfo.ContainerRuntimeVersion,
		Unschedulable:    node.Spec.Unschedulable,
		TaintCount:       len(node.Spec.Taints),
	}

	// IPs
	for _, addr := range node.Status.Addresses {
		switch addr.Type {
		case corev1.NodeInternalIP:
			info.InternalIP = addr.Address
		case corev1.NodeExternalIP:
			info.ExternalIP = addr.Address
		}
	}

	// Labels
	info.InstanceType = node.Labels["node.kubernetes.io/instance-type"]
	info.Zone = node.Labels["topology.kubernetes.io/zone"]
	info.Arch = node.Labels["kubernetes.io/arch"]

	// Capacity
	info.CPUCapacity = node.Status.Capacity[corev1.ResourceCPU]
	info.MemoryCapacity = node.Status.Capacity[corev1.ResourceMemory]
	info.PodCapacity = node.Status.Capacity[corev1.ResourcePods]

	// Conditions
	for _, c := range node.Status.Conditions {
		info.Conditions = append(info.Conditions, NodeCondition{
			Type:    string(c.Type),
			Status:  string(c.Status),
			Message: c.Message,
		})
	}

	return info
}

func nodeStatus(node *corev1.Node) string {
	for _, c := range node.Status.Conditions {
		if c.Type == corev1.NodeReady {
			if c.Status == corev1.ConditionTrue {
				return "Ready"
			}
			return "NotReady"
		}
	}
	return "Unknown"
}

func nodeRoles(node *corev1.Node) []string {
	var roles []string
	for label := range node.Labels {
		if label == "node-role.kubernetes.io/control-plane" {
			roles = append(roles, "control-plane")
		} else if label == "node-role.kubernetes.io/worker" {
			roles = append(roles, "worker")
		}
	}
	if len(roles) == 0 {
		roles = append(roles, "worker")
	}
	return roles
}

func quantityPercent(used, capacity resource.Quantity) float64 {
	if capacity.IsZero() {
		return 0
	}
	return float64(used.MilliValue()) / float64(capacity.MilliValue()) * 100
}

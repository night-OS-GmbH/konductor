package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsv1beta1 "k8s.io/metrics/pkg/client/clientset/versioned"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/night-OS-GmbH/konductor/internal/operator/decision"
)

// MetricsCollector gathers cluster metrics from the Kubernetes API and the
// metrics-server (metrics.k8s.io/v1beta1). It produces the ClusterMetrics
// struct consumed by the decision engine.
type MetricsCollector struct {
	client        client.Client
	metricsClient metricsv1beta1.Interface
}

// NewMetricsCollector creates a new collector. The metricsClient is a direct
// client for metrics.k8s.io (not cached) because the metrics API doesn't support Watch.
func NewMetricsCollector(c client.Client, mc metricsv1beta1.Interface) *MetricsCollector {
	return &MetricsCollector{client: c, metricsClient: mc}
}

// Collect gathers CPU/memory utilization per node, pending pod counts, and
// aggregated cluster metrics. The poolLabels parameter filters which nodes
// belong to the pool being evaluated.
func (c *MetricsCollector) Collect(ctx context.Context, poolLabels map[string]string) (*decision.ClusterMetrics, error) {
	log := log.FromContext(ctx)

	// List all nodes, then filter to workers only.
	var nodeList corev1.NodeList
	listOpts := []client.ListOption{}
	if len(poolLabels) > 0 {
		listOpts = append(listOpts, client.MatchingLabels(poolLabels))
	}
	if err := c.client.List(ctx, &nodeList, listOpts...); err != nil {
		return nil, fmt.Errorf("listing nodes: %w", err)
	}

	// Build a lookup map. When pool labels are specified, include all matching
	// nodes (the label already scopes to the correct pool, which may be a
	// control-plane pool). Without pool labels, fall back to worker-only filtering.
	poolNodes := make(map[string]*corev1.Node, len(nodeList.Items))
	for i := range nodeList.Items {
		node := &nodeList.Items[i]
		if len(poolLabels) > 0 || isWorkerNode(node) {
			poolNodes[node.Name] = node
		}
	}

	// Fetch node metrics from the metrics API.
	nodeMetricsMap, err := c.fetchNodeMetrics(ctx, poolNodes)
	if err != nil {
		log.Error(err, "metrics API unavailable, utilization data will be zero")
		// Non-fatal: the engine can still work with pending pod data alone.
	}

	// Build per-node utilization.
	var (
		totalCPUPercent    float64
		totalMemoryPercent float64
		readyCount         int
		utilizations       []decision.NodeUtilization
	)

	for name, node := range poolNodes {
		cpuCapacity := node.Status.Capacity[corev1.ResourceCPU]
		memCapacity := node.Status.Capacity[corev1.ResourceMemory]

		nu := decision.NodeUtilization{
			NodeName:  name,
			Drainable: isWorkerNode(node) && !node.Spec.Unschedulable,
		}

		if m, ok := nodeMetricsMap[name]; ok {
			nu.CPUPercent = quantityPercent(m.cpuUsage, cpuCapacity)
			nu.MemoryPercent = quantityPercent(m.memUsage, memCapacity)
		}

		// Count pods on this node.
		nu.PodCount, err = c.countPodsOnNode(ctx, name)
		if err != nil {
			log.Error(err, "failed to count pods on node", "node", name)
		}

		if isNodeReady(node) {
			readyCount++
		}

		totalCPUPercent += nu.CPUPercent
		totalMemoryPercent += nu.MemoryPercent

		utilizations = append(utilizations, nu)
	}

	totalNodes := len(poolNodes)

	var avgCPU, avgMem float64
	if totalNodes > 0 {
		avgCPU = totalCPUPercent / float64(totalNodes)
		avgMem = totalMemoryPercent / float64(totalNodes)
	}

	// Count pending pods across the entire cluster (not just pool nodes).
	pendingPods, err := c.countPendingPods(ctx)
	if err != nil {
		log.Error(err, "failed to count pending pods")
	}

	return &decision.ClusterMetrics{
		AvgCPUPercent:    avgCPU,
		AvgMemoryPercent: avgMem,
		PendingPods:      pendingPods,
		NodeUtilizations: utilizations,
		TotalNodes:       totalNodes,
		ReadyNodes:       readyCount,
	}, nil
}

// nodeMetrics holds raw resource usage for a single node.
type nodeMetrics struct {
	cpuUsage resource.Quantity
	memUsage resource.Quantity
}

// fetchNodeMetrics retrieves resource usage from the metrics API for nodes
// that belong to the pool.
func (c *MetricsCollector) fetchNodeMetrics(ctx context.Context, poolNodes map[string]*corev1.Node) (map[string]nodeMetrics, error) {
	if c.metricsClient == nil {
		return nil, fmt.Errorf("metrics client not available")
	}

	metricsList, err := c.metricsClient.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing node metrics: %w", err)
	}

	result := make(map[string]nodeMetrics, len(metricsList.Items))
	for _, m := range metricsList.Items {
		if _, ok := poolNodes[m.Name]; !ok {
			continue
		}
		result[m.Name] = nodeMetrics{
			cpuUsage: m.Usage[corev1.ResourceCPU],
			memUsage: m.Usage[corev1.ResourceMemory],
		}
	}

	return result, nil
}

// countPendingPods returns the number of pods in Pending state with scheduling
// failure conditions (Unschedulable, Insufficient resources).
func (c *MetricsCollector) countPendingPods(ctx context.Context) (int, error) {
	var podList corev1.PodList
	if err := c.client.List(ctx, &podList, client.MatchingFields{"status.phase": "Pending"}); err != nil {
		// Field selectors may not be available with all cache configurations.
		// Fall back to listing all pods and filtering.
		if err := c.client.List(ctx, &podList); err != nil {
			return 0, fmt.Errorf("listing pods: %w", err)
		}
	}

	count := 0
	for i := range podList.Items {
		pod := &podList.Items[i]
		if pod.Status.Phase != corev1.PodPending {
			continue
		}

		// Only count pods that are pending due to scheduling failures, not
		// image pulls or init container work.
		if isSchedulingFailure(pod) {
			count++
		}
	}

	return count, nil
}

// countPodsOnNode returns the number of running pods on the specified node.
func (c *MetricsCollector) countPodsOnNode(ctx context.Context, nodeName string) (int, error) {
	var podList corev1.PodList
	if err := c.client.List(ctx, &podList); err != nil {
		return 0, fmt.Errorf("listing pods: %w", err)
	}

	count := 0
	for i := range podList.Items {
		pod := &podList.Items[i]
		if pod.Spec.NodeName == nodeName && pod.Status.Phase == corev1.PodRunning {
			count++
		}
	}

	return count, nil
}

// isSchedulingFailure returns true if a pending pod is stuck due to cluster
// resource constraints (not enough nodes/CPU/memory).
func isSchedulingFailure(pod *corev1.Pod) bool {
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse {
			reason := cond.Reason
			if reason == "Unschedulable" {
				return true
			}
			// Check the message for "Insufficient" (cpu, memory, etc.).
			if cond.Message != "" {
				msg := cond.Message
				if contains(msg, "Insufficient") || contains(msg, "insufficient") ||
					contains(msg, "didn't match Pod's node affinity") ||
					contains(msg, "node(s) had untolerated taint") {
					return true
				}
			}
		}
	}

	// Also check events-style reasons in the pod's status.
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason == "Unschedulable" {
			return true
		}
	}

	return false
}

// isWorkerNode returns true if the node is a worker (not a control-plane node).
func isWorkerNode(node *corev1.Node) bool {
	if _, ok := node.Labels["node-role.kubernetes.io/control-plane"]; ok {
		return false
	}
	if _, ok := node.Labels["node-role.kubernetes.io/master"]; ok {
		return false
	}
	return true
}

// quantityPercent computes the percentage of used relative to capacity.
func quantityPercent(used, capacity resource.Quantity) float64 {
	if capacity.IsZero() {
		return 0
	}
	return float64(used.MilliValue()) / float64(capacity.MilliValue()) * 100
}

// contains is a simple string containment check to avoid importing strings.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// isNodeReady checks whether a Kubernetes node has the Ready condition set to True.
// This is duplicated from the nodepool controller for package locality but both
// implementations are identical.
func isNodeReadyFromCollector(node *corev1.Node) bool {
	for _, c := range node.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// Ensure the standalone function is available within the same package.
// The isNodeReady in nodepool_controller.go is the canonical version used throughout.
var _ = metav1.Now // Ensure metav1 import is used.

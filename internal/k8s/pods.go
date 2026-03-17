package k8s

import (
	"context"
	"fmt"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PodInfo contains detailed information about a single pod.
type PodInfo struct {
	Name      string
	Namespace string
	Status    string // Display status: Running, CrashLoopBackOff, etc.
	Phase     string // Pod phase: Running, Pending, Failed, Succeeded
	Node      string
	IP        string
	Ready     string // "2/3"
	ReadyCount int
	TotalCount int
	Restarts  int32
	Age       time.Duration

	// Resource requests and limits (aggregated across all containers).
	CPURequest resource.Quantity
	CPULimit   resource.Quantity
	MemRequest resource.Quantity
	MemLimit   resource.Quantity

	// Actual usage from metrics API.
	CPUUsage resource.Quantity
	MemUsage resource.Quantity

	// Usage percentages relative to requests/limits.
	CPURequestPercent float64
	CPULimitPercent   float64
	MemRequestPercent float64
	MemLimitPercent   float64

	// Problem indicators.
	IsThrottled  bool // CPU usage > 90% of limit
	IsOOMRisk    bool // Memory usage > 85% of limit
	IsRestarting bool // Restarts > 3
	IsCrashLoop  bool
	IsNotReady   bool
	StatusReason string
}

// GetPods returns detailed information about all pods in the cluster.
func (c *Client) GetPods(ctx context.Context) ([]PodInfo, error) {
	podList, err := c.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list pods: %w", err)
	}

	// Fetch pod metrics if available.
	type podMetrics struct {
		cpu resource.Quantity
		mem resource.Quantity
	}
	metricsMap := make(map[string]podMetrics)
	if c.metrics != nil {
		pmList, err := c.metrics.MetricsV1beta1().PodMetricses("").List(ctx, metav1.ListOptions{})
		if err == nil {
			for _, pm := range pmList.Items {
				key := pm.Namespace + "/" + pm.Name
				var cpu, mem resource.Quantity
				for _, container := range pm.Containers {
					cpu.Add(*container.Usage.Cpu())
					mem.Add(*container.Usage.Memory())
				}
				metricsMap[key] = podMetrics{cpu: cpu, mem: mem}
			}
		}
	}

	pods := make([]PodInfo, 0, len(podList.Items))
	for i := range podList.Items {
		pod := &podList.Items[i]
		info := podInfoFromPod(pod)

		// Merge metrics data.
		key := pod.Namespace + "/" + pod.Name
		if m, ok := metricsMap[key]; ok {
			info.CPUUsage = m.cpu
			info.MemUsage = m.mem
			info.CPURequestPercent = quantityPercent(m.cpu, info.CPURequest)
			info.CPULimitPercent = quantityPercent(m.cpu, info.CPULimit)
			info.MemRequestPercent = quantityPercent(m.mem, info.MemRequest)
			info.MemLimitPercent = quantityPercent(m.mem, info.MemLimit)

			info.IsThrottled = info.CPULimitPercent > 90 && !info.CPULimit.IsZero()
			info.IsOOMRisk = info.MemLimitPercent > 85 && !info.MemLimit.IsZero()
		}

		pods = append(pods, info)
	}

	sort.Slice(pods, func(i, j int) bool {
		if pods[i].Namespace != pods[j].Namespace {
			return pods[i].Namespace < pods[j].Namespace
		}
		return pods[i].Name < pods[j].Name
	})

	return pods, nil
}

func podInfoFromPod(pod *corev1.Pod) PodInfo {
	info := PodInfo{
		Name:      pod.Name,
		Namespace: pod.Namespace,
		Phase:     string(pod.Status.Phase),
		Node:      pod.Spec.NodeName,
		IP:        pod.Status.PodIP,
		Age:       time.Since(pod.CreationTimestamp.Time),
	}

	// Aggregate resources across all containers.
	for _, c := range pod.Spec.Containers {
		info.TotalCount++
		info.CPURequest.Add(*c.Resources.Requests.Cpu())
		info.CPULimit.Add(*c.Resources.Limits.Cpu())
		info.MemRequest.Add(*c.Resources.Requests.Memory())
		info.MemLimit.Add(*c.Resources.Limits.Memory())
	}

	// Container statuses.
	var totalRestarts int32
	var readyCount int
	for _, cs := range pod.Status.ContainerStatuses {
		totalRestarts += cs.RestartCount
		if cs.Ready {
			readyCount++
		}
		if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
			info.IsCrashLoop = true
			info.StatusReason = "CrashLoopBackOff"
		}
		if cs.LastTerminationState.Terminated != nil && cs.LastTerminationState.Terminated.Reason == "OOMKilled" {
			info.StatusReason = "OOMKilled"
		}
	}
	info.Restarts = totalRestarts
	info.ReadyCount = readyCount
	info.Ready = fmt.Sprintf("%d/%d", readyCount, info.TotalCount)
	info.IsRestarting = totalRestarts > 3
	info.IsNotReady = readyCount < info.TotalCount && pod.Status.Phase == corev1.PodRunning

	info.Status = podStatus(pod)
	return info
}

func podStatus(pod *corev1.Pod) string {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
			return cs.State.Waiting.Reason
		}
		if cs.State.Terminated != nil && cs.State.Terminated.Reason != "" {
			return cs.State.Terminated.Reason
		}
	}
	for _, cs := range pod.Status.InitContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
			return "Init:" + cs.State.Waiting.Reason
		}
		if cs.State.Terminated != nil && cs.State.Terminated.Reason != "" {
			return "Init:" + cs.State.Terminated.Reason
		}
	}
	return string(pod.Status.Phase)
}

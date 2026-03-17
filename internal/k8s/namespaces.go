package k8s

import (
	"sort"

	"k8s.io/apimachinery/pkg/api/resource"
)

// NamespaceInfo contains aggregated resource information for a namespace.
type NamespaceInfo struct {
	Name        string
	PodCount    int
	RunningPods int
	PendingPods int
	FailedPods  int
	WarningPods int // CrashLoop, OOMRisk, high restarts, not ready

	CPUUsage   resource.Quantity
	MemUsage   resource.Quantity
	CPURequest resource.Quantity
	MemRequest resource.Quantity
}

// AggregateNamespaces groups pods by namespace and computes per-namespace stats.
func AggregateNamespaces(pods []PodInfo) []NamespaceInfo {
	nsMap := make(map[string]*NamespaceInfo)

	for _, pod := range pods {
		ns, ok := nsMap[pod.Namespace]
		if !ok {
			ns = &NamespaceInfo{Name: pod.Namespace}
			nsMap[pod.Namespace] = ns
		}
		ns.PodCount++

		switch pod.Phase {
		case "Running":
			ns.RunningPods++
		case "Pending":
			ns.PendingPods++
		case "Failed":
			ns.FailedPods++
		}

		if pod.IsCrashLoop || pod.IsOOMRisk || pod.IsRestarting || pod.IsNotReady {
			ns.WarningPods++
		}

		ns.CPUUsage.Add(pod.CPUUsage)
		ns.MemUsage.Add(pod.MemUsage)
		ns.CPURequest.Add(pod.CPURequest)
		ns.MemRequest.Add(pod.MemRequest)
	}

	namespaces := make([]NamespaceInfo, 0, len(nsMap))
	for _, ns := range nsMap {
		namespaces = append(namespaces, *ns)
	}

	// Sort by pod count descending.
	sort.Slice(namespaces, func(i, j int) bool {
		return namespaces[i].PodCount > namespaces[j].PodCount
	})

	return namespaces
}

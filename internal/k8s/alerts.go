package k8s

import (
	"fmt"
	"sort"
)

// AlertLevel represents the severity of an alert.
type AlertLevel int

const (
	AlertInfo AlertLevel = iota
	AlertWarning
	AlertCritical
)

// Alert represents a cluster health alert.
type Alert struct {
	Level     AlertLevel
	Resource  string
	Namespace string
	Message   string
}

// GenerateAlerts analyzes nodes and pods to produce health alerts.
func GenerateAlerts(nodes []NodeInfo, pods []PodInfo) []Alert {
	var alerts []Alert

	// Node alerts.
	for _, node := range nodes {
		if node.Status == "NotReady" {
			alerts = append(alerts, Alert{AlertCritical, "node/" + node.Name, "", "Node is NotReady"})
			continue
		}
		if node.CPUPercent > 90 {
			alerts = append(alerts, Alert{AlertCritical, "node/" + node.Name, "", fmt.Sprintf("CPU at %.0f%%", node.CPUPercent)})
		} else if node.CPUPercent > 80 {
			alerts = append(alerts, Alert{AlertWarning, "node/" + node.Name, "", fmt.Sprintf("CPU at %.0f%%", node.CPUPercent)})
		}
		if node.MemoryPercent > 90 {
			alerts = append(alerts, Alert{AlertCritical, "node/" + node.Name, "", fmt.Sprintf("Memory at %.0f%%", node.MemoryPercent)})
		} else if node.MemoryPercent > 85 {
			alerts = append(alerts, Alert{AlertWarning, "node/" + node.Name, "", fmt.Sprintf("Memory at %.0f%%", node.MemoryPercent)})
		}
	}

	// Pod alerts (individual).
	for _, pod := range pods {
		if pod.IsCrashLoop {
			alerts = append(alerts, Alert{AlertCritical, pod.Name, pod.Namespace, "CrashLoopBackOff"})
		} else if pod.IsOOMRisk {
			alerts = append(alerts, Alert{AlertWarning, pod.Name, pod.Namespace, fmt.Sprintf("Memory at %.0f%% of limit", pod.MemLimitPercent)})
		} else if pod.IsThrottled {
			alerts = append(alerts, Alert{AlertWarning, pod.Name, pod.Namespace, fmt.Sprintf("CPU throttled (%.0f%% of limit)", pod.CPULimitPercent)})
		} else if pod.IsRestarting {
			alerts = append(alerts, Alert{AlertWarning, pod.Name, pod.Namespace, fmt.Sprintf("%d restarts", pod.Restarts)})
		}
	}

	// Aggregate pending pods by namespace.
	pendingByNS := make(map[string]int)
	for _, pod := range pods {
		if pod.Phase == "Pending" {
			pendingByNS[pod.Namespace]++
		}
	}
	for ns, count := range pendingByNS {
		alerts = append(alerts, Alert{AlertInfo, fmt.Sprintf("%d pod(s) pending", count), ns, "Pods waiting to be scheduled"})
	}

	// Sort by severity (critical first).
	sort.Slice(alerts, func(i, j int) bool {
		return alerts[i].Level > alerts[j].Level
	})

	if len(alerts) > 15 {
		alerts = alerts[:15]
	}

	return alerts
}

package decision

import (
	"fmt"
	"sort"
)

// Action represents the type of scaling action to take.
type Action int

const (
	// NoAction means the cluster is within acceptable parameters.
	NoAction Action = iota
	// ScaleUp means nodes should be added.
	ScaleUp
	// ScaleDown means nodes should be removed.
	ScaleDown
)

// String returns a human-readable name for the action.
func (a Action) String() string {
	switch a {
	case NoAction:
		return "NoAction"
	case ScaleUp:
		return "ScaleUp"
	case ScaleDown:
		return "ScaleDown"
	default:
		return "Unknown"
	}
}

// Decision is the output of the decision engine: what to do, how many, and why.
type Decision struct {
	// Action is the scaling action to take.
	Action Action

	// Count is the number of nodes to add or remove.
	Count int

	// Reason is a human-readable explanation for the decision.
	Reason string

	// TargetNodes lists node names to remove (only relevant for ScaleDown).
	TargetNodes []string
}

// ClusterMetrics is a snapshot of current cluster resource utilization.
type ClusterMetrics struct {
	// AvgCPUPercent is the average CPU utilization across all nodes (0-100).
	AvgCPUPercent float64

	// AvgMemoryPercent is the average memory utilization across all nodes (0-100).
	AvgMemoryPercent float64

	// PendingPods is the number of pods in Pending state.
	PendingPods int

	// NodeUtilizations holds per-node utilization data.
	NodeUtilizations []NodeUtilization

	// TotalNodes is the total number of nodes in the pool.
	TotalNodes int

	// ReadyNodes is the number of nodes in Ready condition.
	ReadyNodes int
}

// NodeUtilization holds resource usage data for a single node.
type NodeUtilization struct {
	// NodeName is the Kubernetes node name.
	NodeName string

	// CPUPercent is the node's CPU utilization (0-100).
	CPUPercent float64

	// MemoryPercent is the node's memory utilization (0-100).
	MemoryPercent float64

	// PodCount is the number of pods running on this node.
	PodCount int

	// Drainable indicates whether this node can be safely drained and removed.
	Drainable bool
}

// ScalingConfig mirrors the CRD ScalingBehavior for the engine to consume.
type ScalingConfig struct {
	MinNodes        int32
	MaxNodes        int32
	ScaleUp         ScaleUpThresholds
	ScaleDown       ScaleDownThresholds
	CooldownSeconds int32
}

// ScaleUpThresholds holds the thresholds that trigger scale-up.
type ScaleUpThresholds struct {
	CPUThresholdPercent        int32
	MemoryThresholdPercent     int32
	PendingPodsThreshold       int32
	StabilizationWindowSeconds int32
	Step                       int32
}

// ScaleDownThresholds holds the thresholds that trigger scale-down.
type ScaleDownThresholds struct {
	CPUThresholdPercent        int32
	MemoryThresholdPercent     int32
	StabilizationWindowSeconds int32
	Step                       int32
}

// Engine evaluates cluster metrics against scaling configuration and
// produces a scaling decision. It uses hysteresis to prevent flapping.
type Engine struct {
	hysteresis *Hysteresis
}

// NewEngine creates a new decision Engine.
func NewEngine() *Engine {
	return &Engine{
		hysteresis: NewHysteresis(),
	}
}

// NewEngineWithHysteresis creates an Engine with a caller-provided Hysteresis,
// primarily useful for testing with a controlled clock.
func NewEngineWithHysteresis(h *Hysteresis) *Engine {
	return &Engine{
		hysteresis: h,
	}
}

// Evaluate examines the current cluster metrics against the scaling config
// and returns a Decision. The evaluation order is:
//
//  1. Cooldown check — if a recent scale action occurred, return NoAction.
//  2. Minimum enforcement — if nodes are below MinNodes, scale up immediately.
//  3. Pending pods — if pods are stuck pending above threshold, scale up.
//  4. Resource pressure — if avg CPU or memory exceeds threshold, scale up.
//  5. Underutilization — if nodes are idle long enough, scale down.
//
// All decisions respect min/max node bounds.
func (e *Engine) Evaluate(metrics ClusterMetrics, config ScalingConfig) Decision {
	// 1. Cooldown: block any action if we recently scaled.
	if e.hysteresis.InCooldown(config.CooldownSeconds) {
		return Decision{
			Action: NoAction,
			Reason: "cooldown active after recent scaling action",
		}
	}

	// 2. Minimum enforcement: if current nodes are below the configured minimum,
	// scale up immediately without waiting for threshold-based signals.
	if metrics.TotalNodes < int(config.MinNodes) {
		deficit := int(config.MinNodes) - metrics.TotalNodes
		count := e.clampScaleUp(deficit, metrics.TotalNodes, int(config.MaxNodes))
		if count > 0 {
			return Decision{
				Action: ScaleUp,
				Count:  count,
				Reason: fmt.Sprintf("current nodes (%d) below minimum (%d)", metrics.TotalNodes, config.MinNodes),
			}
		}
	}

	// 4. Pending pods check (highest priority for threshold-based scale-up).
	if config.ScaleUp.PendingPodsThreshold > 0 && metrics.PendingPods >= int(config.ScaleUp.PendingPodsThreshold) {
		// Track how long we have been seeing pending pods.
		e.hysteresis.TrackPressure(true)

		if e.hysteresis.PressureExceedsWindow(config.ScaleUp.StabilizationWindowSeconds) {
			count := e.clampScaleUp(int(config.ScaleUp.Step), metrics.TotalNodes, int(config.MaxNodes))
			if count > 0 {
				return Decision{
					Action: ScaleUp,
					Count:  count,
					Reason: fmt.Sprintf("%d pending pod(s) exceed threshold of %d", metrics.PendingPods, config.ScaleUp.PendingPodsThreshold),
				}
			}
		}

		return Decision{
			Action: NoAction,
			Reason: fmt.Sprintf("pending pods detected (%d), waiting for stabilization window", metrics.PendingPods),
		}
	}

	// 5. Resource pressure (CPU or memory above threshold).
	cpuExceeded := config.ScaleUp.CPUThresholdPercent > 0 && metrics.AvgCPUPercent >= float64(config.ScaleUp.CPUThresholdPercent)
	memExceeded := config.ScaleUp.MemoryThresholdPercent > 0 && metrics.AvgMemoryPercent >= float64(config.ScaleUp.MemoryThresholdPercent)

	if cpuExceeded || memExceeded {
		e.hysteresis.TrackPressure(true)

		if e.hysteresis.PressureExceedsWindow(config.ScaleUp.StabilizationWindowSeconds) {
			count := e.clampScaleUp(int(config.ScaleUp.Step), metrics.TotalNodes, int(config.MaxNodes))
			if count > 0 {
				reason := "resource pressure:"
				if cpuExceeded {
					reason += fmt.Sprintf(" CPU %.1f%% >= %d%%", metrics.AvgCPUPercent, config.ScaleUp.CPUThresholdPercent)
				}
				if memExceeded {
					if cpuExceeded {
						reason += ","
					}
					reason += fmt.Sprintf(" memory %.1f%% >= %d%%", metrics.AvgMemoryPercent, config.ScaleUp.MemoryThresholdPercent)
				}
				return Decision{
					Action: ScaleUp,
					Count:  count,
					Reason: reason,
				}
			}
		}

		return Decision{
			Action: NoAction,
			Reason: "resource pressure detected, waiting for stabilization window",
		}
	}

	// No scale-up pressure — reset pressure tracker.
	e.hysteresis.TrackPressure(false)

	// 6. Scale-down: find underutilized, drainable nodes.
	if config.ScaleDown.CPUThresholdPercent > 0 || config.ScaleDown.MemoryThresholdPercent > 0 {
		candidates := e.findScaleDownCandidates(metrics, config)
		if len(candidates) > 0 {
			// Limit by step size.
			step := int(config.ScaleDown.Step)
			if step <= 0 {
				step = 1
			}
			if len(candidates) > step {
				candidates = candidates[:step]
			}

			// Enforce minNodes.
			maxRemovable := metrics.TotalNodes - int(config.MinNodes)
			if maxRemovable <= 0 {
				return Decision{
					Action: NoAction,
					Reason: fmt.Sprintf("at minimum node count (%d)", config.MinNodes),
				}
			}
			if len(candidates) > maxRemovable {
				candidates = candidates[:maxRemovable]
			}

			names := make([]string, len(candidates))
			for i, c := range candidates {
				names[i] = c.NodeName
			}

			return Decision{
				Action:      ScaleDown,
				Count:       len(candidates),
				Reason:      fmt.Sprintf("%d node(s) underutilized beyond stabilization window", len(candidates)),
				TargetNodes: names,
			}
		}
	}

	return Decision{
		Action: NoAction,
		Reason: "cluster within acceptable parameters",
	}
}

// clampScaleUp returns the actual number of nodes to add, clamped by maxNodes.
func (e *Engine) clampScaleUp(step, currentNodes, maxNodes int) int {
	if step <= 0 {
		step = 1
	}
	available := maxNodes - currentNodes
	if available <= 0 {
		return 0
	}
	if step > available {
		return available
	}
	return step
}

// findScaleDownCandidates identifies nodes that have been underutilized long
// enough to be eligible for removal. Nodes are sorted by utilization ascending
// (least utilized first).
func (e *Engine) findScaleDownCandidates(metrics ClusterMetrics, config ScalingConfig) []NodeUtilization {
	cpuThresh := float64(config.ScaleDown.CPUThresholdPercent)
	memThresh := float64(config.ScaleDown.MemoryThresholdPercent)
	window := config.ScaleDown.StabilizationWindowSeconds

	var underutilized []NodeUtilization
	activeNodes := make(map[string]bool)

	for _, nu := range metrics.NodeUtilizations {
		activeNodes[nu.NodeName] = true

		if !nu.Drainable {
			continue
		}

		isUnderutilized := false
		if cpuThresh > 0 && memThresh > 0 {
			isUnderutilized = nu.CPUPercent < cpuThresh && nu.MemoryPercent < memThresh
		} else if cpuThresh > 0 {
			isUnderutilized = nu.CPUPercent < cpuThresh
		} else if memThresh > 0 {
			isUnderutilized = nu.MemoryPercent < memThresh
		}

		if isUnderutilized {
			e.hysteresis.TrackNodeUtilization(nu.NodeName, true)
			if e.hysteresis.NodeUnderutilizedLongEnough(nu.NodeName, window) {
				underutilized = append(underutilized, nu)
			}
		} else {
			e.hysteresis.TrackNodeUtilization(nu.NodeName, false)
		}
	}

	// Clean up hysteresis tracking for nodes that no longer exist.
	e.hysteresis.PruneNodes(activeNodes)

	// Sort by combined utilization ascending — remove least utilized first.
	sort.Slice(underutilized, func(i, j int) bool {
		iUtil := underutilized[i].CPUPercent + underutilized[i].MemoryPercent
		jUtil := underutilized[j].CPUPercent + underutilized[j].MemoryPercent
		return iUtil < jUtil
	})

	return underutilized
}

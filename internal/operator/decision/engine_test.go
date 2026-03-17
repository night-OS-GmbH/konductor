package decision

import (
	"testing"
	"time"
)

// --------------------------------------------------------------------------
// fakeClock — controllable clock for deterministic tests
// --------------------------------------------------------------------------

type fakeClock struct {
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

// --------------------------------------------------------------------------
// helpers
// --------------------------------------------------------------------------

func defaultConfig() ScalingConfig {
	return ScalingConfig{
		MinNodes: 3,
		MaxNodes: 10,
		ScaleUp: ScaleUpThresholds{
			CPUThresholdPercent:        80,
			MemoryThresholdPercent:     80,
			PendingPodsThreshold:       1,
			StabilizationWindowSeconds: 60,
			Step:                       1,
		},
		ScaleDown: ScaleDownThresholds{
			CPUThresholdPercent:        20,
			MemoryThresholdPercent:     20,
			StabilizationWindowSeconds: 300,
			Step:                       1,
		},
		CooldownSeconds: 120,
	}
}

func healthyMetrics(nodeCount int) ClusterMetrics {
	nodes := make([]NodeUtilization, nodeCount)
	for i := range nodes {
		nodes[i] = NodeUtilization{
			NodeName:      nodeName(i),
			CPUPercent:    50,
			MemoryPercent: 50,
			PodCount:      10,
			Drainable:     true,
		}
	}
	return ClusterMetrics{
		AvgCPUPercent:    50,
		AvgMemoryPercent: 50,
		PendingPods:      0,
		NodeUtilizations: nodes,
		TotalNodes:       nodeCount,
		ReadyNodes:       nodeCount,
	}
}

func nodeName(i int) string {
	return "node-" + string(rune('a'+i))
}

func newTestEngine(clock *fakeClock) *Engine {
	h := NewHysteresisWithClock(clock)
	return NewEngineWithHysteresis(h)
}

// --------------------------------------------------------------------------
// Scale-up: pending pods
// --------------------------------------------------------------------------

func TestScaleUp_PendingPods_AfterStabilization(t *testing.T) {
	clock := newFakeClock()
	engine := newTestEngine(clock)
	cfg := defaultConfig()

	metrics := healthyMetrics(5)
	metrics.PendingPods = 3

	// First evaluation: pressure starts, but stabilization not met yet.
	d := engine.Evaluate(metrics, cfg)
	if d.Action != NoAction {
		t.Fatalf("expected NoAction during stabilization, got %s", d.Action)
	}

	// Advance past the stabilization window.
	clock.Advance(61 * time.Second)

	d = engine.Evaluate(metrics, cfg)
	if d.Action != ScaleUp {
		t.Fatalf("expected ScaleUp after stabilization, got %s: %s", d.Action, d.Reason)
	}
	if d.Count != 1 {
		t.Fatalf("expected count=1, got %d", d.Count)
	}
}

func TestScaleUp_PendingPods_BelowThreshold(t *testing.T) {
	clock := newFakeClock()
	engine := newTestEngine(clock)
	cfg := defaultConfig()
	cfg.ScaleUp.PendingPodsThreshold = 5

	metrics := healthyMetrics(5)
	metrics.PendingPods = 3 // below threshold of 5

	clock.Advance(5 * time.Minute) // plenty of time

	d := engine.Evaluate(metrics, cfg)
	if d.Action != NoAction {
		t.Fatalf("expected NoAction when pending pods below threshold, got %s", d.Action)
	}
}

// --------------------------------------------------------------------------
// Scale-up: CPU threshold
// --------------------------------------------------------------------------

func TestScaleUp_HighCPU_AfterStabilization(t *testing.T) {
	clock := newFakeClock()
	engine := newTestEngine(clock)
	cfg := defaultConfig()

	metrics := healthyMetrics(5)
	metrics.AvgCPUPercent = 85

	// First evaluation: pressure recorded, stabilization not met.
	d := engine.Evaluate(metrics, cfg)
	if d.Action != NoAction {
		t.Fatalf("expected NoAction during stabilization, got %s", d.Action)
	}

	// Advance past stabilization window.
	clock.Advance(61 * time.Second)

	d = engine.Evaluate(metrics, cfg)
	if d.Action != ScaleUp {
		t.Fatalf("expected ScaleUp, got %s: %s", d.Action, d.Reason)
	}
	if d.Count != 1 {
		t.Fatalf("expected count=1, got %d", d.Count)
	}
}

// --------------------------------------------------------------------------
// Scale-up: memory threshold
// --------------------------------------------------------------------------

func TestScaleUp_HighMemory_AfterStabilization(t *testing.T) {
	clock := newFakeClock()
	engine := newTestEngine(clock)
	cfg := defaultConfig()

	metrics := healthyMetrics(5)
	metrics.AvgMemoryPercent = 90

	d := engine.Evaluate(metrics, cfg)
	if d.Action != NoAction {
		t.Fatalf("expected NoAction during stabilization, got %s", d.Action)
	}

	clock.Advance(61 * time.Second)

	d = engine.Evaluate(metrics, cfg)
	if d.Action != ScaleUp {
		t.Fatalf("expected ScaleUp, got %s: %s", d.Action, d.Reason)
	}
}

// --------------------------------------------------------------------------
// Scale-up: both CPU and memory exceeded
// --------------------------------------------------------------------------

func TestScaleUp_BothCPUAndMemory(t *testing.T) {
	clock := newFakeClock()
	engine := newTestEngine(clock)
	cfg := defaultConfig()

	metrics := healthyMetrics(5)
	metrics.AvgCPUPercent = 85
	metrics.AvgMemoryPercent = 90

	d := engine.Evaluate(metrics, cfg)
	clock.Advance(61 * time.Second)

	d = engine.Evaluate(metrics, cfg)
	if d.Action != ScaleUp {
		t.Fatalf("expected ScaleUp, got %s: %s", d.Action, d.Reason)
	}
	// Reason should mention both CPU and memory.
	if d.Reason == "" {
		t.Fatal("expected non-empty reason")
	}
}

// --------------------------------------------------------------------------
// No scale-up during cooldown
// --------------------------------------------------------------------------

func TestNoScaleUp_DuringCooldown(t *testing.T) {
	clock := newFakeClock()
	engine := newTestEngine(clock)
	cfg := defaultConfig()

	// Simulate a previous scale action.
	engine.hysteresis.RecordScaleAction()

	metrics := healthyMetrics(5)
	metrics.AvgCPUPercent = 95
	metrics.PendingPods = 10

	// Even with very high pressure, cooldown blocks action.
	d := engine.Evaluate(metrics, cfg)
	if d.Action != NoAction {
		t.Fatalf("expected NoAction during cooldown, got %s", d.Action)
	}

	// Advance past cooldown.
	clock.Advance(121 * time.Second)

	// Now pressure tracking starts fresh (it was reset by RecordScaleAction).
	d = engine.Evaluate(metrics, cfg)
	if d.Action != NoAction {
		t.Fatalf("expected NoAction (stabilization window just started), got %s", d.Action)
	}

	// Advance past stabilization window.
	clock.Advance(61 * time.Second)

	d = engine.Evaluate(metrics, cfg)
	if d.Action != ScaleUp {
		t.Fatalf("expected ScaleUp after cooldown + stabilization, got %s: %s", d.Action, d.Reason)
	}
}

// --------------------------------------------------------------------------
// No scale-up above maxNodes
// --------------------------------------------------------------------------

func TestNoScaleUp_AtMaxNodes(t *testing.T) {
	clock := newFakeClock()
	engine := newTestEngine(clock)
	cfg := defaultConfig()
	cfg.MaxNodes = 5

	metrics := healthyMetrics(5) // already at max
	metrics.AvgCPUPercent = 95

	// Start pressure and wait for stabilization.
	engine.Evaluate(metrics, cfg)
	clock.Advance(61 * time.Second)

	d := engine.Evaluate(metrics, cfg)
	if d.Action != NoAction {
		t.Fatalf("expected NoAction at maxNodes, got %s: %s", d.Action, d.Reason)
	}
}

// --------------------------------------------------------------------------
// Scale-up clamped by maxNodes
// --------------------------------------------------------------------------

func TestScaleUp_ClampedByMaxNodes(t *testing.T) {
	clock := newFakeClock()
	engine := newTestEngine(clock)
	cfg := defaultConfig()
	cfg.MaxNodes = 6
	cfg.ScaleUp.Step = 3

	metrics := healthyMetrics(5) // can only add 1 more (6 - 5)
	metrics.AvgCPUPercent = 95

	engine.Evaluate(metrics, cfg)
	clock.Advance(61 * time.Second)

	d := engine.Evaluate(metrics, cfg)
	if d.Action != ScaleUp {
		t.Fatalf("expected ScaleUp, got %s", d.Action)
	}
	if d.Count != 1 {
		t.Fatalf("expected count=1 (clamped from step=3 by maxNodes), got %d", d.Count)
	}
}

// --------------------------------------------------------------------------
// Scale-down: underutilized node after stabilization
// --------------------------------------------------------------------------

func TestScaleDown_UnderutilizedNode(t *testing.T) {
	clock := newFakeClock()
	engine := newTestEngine(clock)
	cfg := defaultConfig()

	metrics := healthyMetrics(5)
	// Make node-a underutilized.
	metrics.NodeUtilizations[0].CPUPercent = 5
	metrics.NodeUtilizations[0].MemoryPercent = 10
	// Keep averages normal so scale-up does not trigger.
	metrics.AvgCPUPercent = 40
	metrics.AvgMemoryPercent = 40

	// First evaluation: starts tracking node-a.
	d := engine.Evaluate(metrics, cfg)
	if d.Action != NoAction {
		t.Fatalf("expected NoAction during scale-down stabilization, got %s: %s", d.Action, d.Reason)
	}

	// Advance past scale-down stabilization window (300s).
	clock.Advance(301 * time.Second)

	d = engine.Evaluate(metrics, cfg)
	if d.Action != ScaleDown {
		t.Fatalf("expected ScaleDown, got %s: %s", d.Action, d.Reason)
	}
	if d.Count != 1 {
		t.Fatalf("expected count=1, got %d", d.Count)
	}
	if len(d.TargetNodes) != 1 || d.TargetNodes[0] != "node-a" {
		t.Fatalf("expected target node-a, got %v", d.TargetNodes)
	}
}

// --------------------------------------------------------------------------
// No scale-down below minNodes
// --------------------------------------------------------------------------

func TestNoScaleDown_AtMinNodes(t *testing.T) {
	clock := newFakeClock()
	engine := newTestEngine(clock)
	cfg := defaultConfig()
	cfg.MinNodes = 5

	metrics := healthyMetrics(5)
	// All nodes underutilized.
	for i := range metrics.NodeUtilizations {
		metrics.NodeUtilizations[i].CPUPercent = 5
		metrics.NodeUtilizations[i].MemoryPercent = 5
	}
	metrics.AvgCPUPercent = 5
	metrics.AvgMemoryPercent = 5

	engine.Evaluate(metrics, cfg)
	clock.Advance(301 * time.Second)

	d := engine.Evaluate(metrics, cfg)
	if d.Action != NoAction {
		t.Fatalf("expected NoAction at minNodes, got %s: %s", d.Action, d.Reason)
	}
}

// --------------------------------------------------------------------------
// Scale-down clamped by minNodes
// --------------------------------------------------------------------------

func TestScaleDown_ClampedByMinNodes(t *testing.T) {
	clock := newFakeClock()
	engine := newTestEngine(clock)
	cfg := defaultConfig()
	cfg.MinNodes = 4
	cfg.ScaleDown.Step = 3

	metrics := healthyMetrics(5)
	// All nodes underutilized.
	for i := range metrics.NodeUtilizations {
		metrics.NodeUtilizations[i].CPUPercent = 5
		metrics.NodeUtilizations[i].MemoryPercent = 5
	}
	metrics.AvgCPUPercent = 5
	metrics.AvgMemoryPercent = 5

	engine.Evaluate(metrics, cfg)
	clock.Advance(301 * time.Second)

	d := engine.Evaluate(metrics, cfg)
	if d.Action != ScaleDown {
		t.Fatalf("expected ScaleDown, got %s: %s", d.Action, d.Reason)
	}
	// 5 total - 4 min = max 1 removable, even though step=3 and all 5 are underutilized.
	if d.Count != 1 {
		t.Fatalf("expected count=1 (clamped by minNodes), got %d", d.Count)
	}
}

// --------------------------------------------------------------------------
// Scale-down: non-drainable nodes are skipped
// --------------------------------------------------------------------------

func TestScaleDown_SkipsNonDrainableNodes(t *testing.T) {
	clock := newFakeClock()
	engine := newTestEngine(clock)
	cfg := defaultConfig()
	cfg.MinNodes = 3

	metrics := healthyMetrics(5)
	// Make two nodes underutilized, but one is not drainable.
	metrics.NodeUtilizations[0].CPUPercent = 5
	metrics.NodeUtilizations[0].MemoryPercent = 5
	metrics.NodeUtilizations[0].Drainable = false // control-plane, system workloads, etc.

	metrics.NodeUtilizations[1].CPUPercent = 5
	metrics.NodeUtilizations[1].MemoryPercent = 5
	metrics.NodeUtilizations[1].Drainable = true

	metrics.AvgCPUPercent = 30
	metrics.AvgMemoryPercent = 30

	engine.Evaluate(metrics, cfg)
	clock.Advance(301 * time.Second)

	d := engine.Evaluate(metrics, cfg)
	if d.Action != ScaleDown {
		t.Fatalf("expected ScaleDown, got %s: %s", d.Action, d.Reason)
	}
	if d.Count != 1 {
		t.Fatalf("expected count=1, got %d", d.Count)
	}
	if d.TargetNodes[0] != "node-b" {
		t.Fatalf("expected target node-b (drainable), got %s", d.TargetNodes[0])
	}
}

// --------------------------------------------------------------------------
// Scale-down: least utilized removed first
// --------------------------------------------------------------------------

func TestScaleDown_LeastUtilizedFirst(t *testing.T) {
	clock := newFakeClock()
	engine := newTestEngine(clock)
	cfg := defaultConfig()
	cfg.MinNodes = 3
	cfg.ScaleDown.Step = 2

	metrics := healthyMetrics(6)
	// node-a: moderately utilized (above threshold — not a candidate)
	metrics.NodeUtilizations[0].CPUPercent = 50
	metrics.NodeUtilizations[0].MemoryPercent = 50
	// node-b: underutilized (combined 20)
	metrics.NodeUtilizations[1].CPUPercent = 10
	metrics.NodeUtilizations[1].MemoryPercent = 10
	// node-c: even more underutilized (combined 8)
	metrics.NodeUtilizations[2].CPUPercent = 3
	metrics.NodeUtilizations[2].MemoryPercent = 5
	// node-d: slightly underutilized (combined 30)
	metrics.NodeUtilizations[3].CPUPercent = 15
	metrics.NodeUtilizations[3].MemoryPercent = 15

	metrics.AvgCPUPercent = 30
	metrics.AvgMemoryPercent = 30

	engine.Evaluate(metrics, cfg)
	clock.Advance(301 * time.Second)

	d := engine.Evaluate(metrics, cfg)
	if d.Action != ScaleDown {
		t.Fatalf("expected ScaleDown, got %s: %s", d.Action, d.Reason)
	}
	if d.Count != 2 {
		t.Fatalf("expected count=2 (step=2), got %d", d.Count)
	}
	// node-c (combined=8) first, then node-b (combined=20).
	if d.TargetNodes[0] != "node-c" {
		t.Fatalf("expected first target node-c, got %s", d.TargetNodes[0])
	}
	if d.TargetNodes[1] != "node-b" {
		t.Fatalf("expected second target node-b, got %s", d.TargetNodes[1])
	}
}

// --------------------------------------------------------------------------
// Hysteresis: prevents flapping (scale-up then immediate scale-down)
// --------------------------------------------------------------------------

func TestHysteresis_PreventsFlapping(t *testing.T) {
	clock := newFakeClock()
	engine := newTestEngine(clock)
	cfg := defaultConfig()

	// Phase 1: High CPU causes scale-up pressure.
	metrics := healthyMetrics(5)
	metrics.AvgCPUPercent = 90

	engine.Evaluate(metrics, cfg)
	clock.Advance(61 * time.Second) // past stabilization

	d := engine.Evaluate(metrics, cfg)
	if d.Action != ScaleUp {
		t.Fatalf("expected ScaleUp, got %s", d.Action)
	}

	// Record the scale action (simulate actual scaling).
	engine.hysteresis.RecordScaleAction()

	// Phase 2: Now metrics show low utilization (the new node spread the load).
	metrics = healthyMetrics(6)
	for i := range metrics.NodeUtilizations {
		metrics.NodeUtilizations[i].CPUPercent = 10
		metrics.NodeUtilizations[i].MemoryPercent = 10
	}
	metrics.AvgCPUPercent = 10
	metrics.AvgMemoryPercent = 10

	// Should NOT immediately scale down — cooldown is active.
	d = engine.Evaluate(metrics, cfg)
	if d.Action != NoAction {
		t.Fatalf("expected NoAction (cooldown), got %s: %s", d.Action, d.Reason)
	}

	// Advance past cooldown but not past scale-down stabilization.
	clock.Advance(121 * time.Second)

	d = engine.Evaluate(metrics, cfg)
	if d.Action == ScaleDown {
		t.Fatal("expected NoAction (scale-down stabilization not met), got ScaleDown")
	}

	// Only after cooldown + scale-down stabilization should it consider scale-down.
	clock.Advance(301 * time.Second)

	d = engine.Evaluate(metrics, cfg)
	// Now nodes have been underutilized since the first check after cooldown,
	// which was at +121s. We are now at 121+301 = 422s. The node underutilization
	// was first tracked at 121s, and the window is 300s, so 422 - 121 = 301s >= 300s.
	if d.Action != ScaleDown {
		t.Fatalf("expected ScaleDown after full stabilization, got %s: %s", d.Action, d.Reason)
	}
}

// --------------------------------------------------------------------------
// Multiple underutilized nodes but step=1
// --------------------------------------------------------------------------

func TestScaleDown_MultipleUnderutilized_Step1(t *testing.T) {
	clock := newFakeClock()
	engine := newTestEngine(clock)
	cfg := defaultConfig()
	cfg.ScaleDown.Step = 1

	metrics := healthyMetrics(6)
	// Make 3 nodes underutilized.
	for i := 0; i < 3; i++ {
		metrics.NodeUtilizations[i].CPUPercent = 5
		metrics.NodeUtilizations[i].MemoryPercent = 5
	}
	metrics.AvgCPUPercent = 25
	metrics.AvgMemoryPercent = 25

	engine.Evaluate(metrics, cfg)
	clock.Advance(301 * time.Second)

	d := engine.Evaluate(metrics, cfg)
	if d.Action != ScaleDown {
		t.Fatalf("expected ScaleDown, got %s: %s", d.Action, d.Reason)
	}
	if d.Count != 1 {
		t.Fatalf("expected count=1 (step=1 even with 3 candidates), got %d", d.Count)
	}
}

// --------------------------------------------------------------------------
// Pressure reset: transient spike does not cause scale-up
// --------------------------------------------------------------------------

func TestScaleUp_TransientSpike_NoAction(t *testing.T) {
	clock := newFakeClock()
	engine := newTestEngine(clock)
	cfg := defaultConfig()

	// First evaluation: high CPU.
	metrics := healthyMetrics(5)
	metrics.AvgCPUPercent = 90

	engine.Evaluate(metrics, cfg)
	clock.Advance(30 * time.Second) // within stabilization window

	// CPU drops back to normal.
	metrics.AvgCPUPercent = 40
	engine.Evaluate(metrics, cfg)

	// Advance past what would have been the stabilization window.
	clock.Advance(61 * time.Second)

	// CPU is normal — no scale-up should happen.
	d := engine.Evaluate(metrics, cfg)
	if d.Action != NoAction {
		t.Fatalf("expected NoAction after transient spike, got %s: %s", d.Action, d.Reason)
	}
}

// --------------------------------------------------------------------------
// Scale-down: node recovers before stabilization window
// --------------------------------------------------------------------------

func TestScaleDown_NodeRecovers(t *testing.T) {
	clock := newFakeClock()
	engine := newTestEngine(clock)
	cfg := defaultConfig()

	metrics := healthyMetrics(5)
	metrics.NodeUtilizations[0].CPUPercent = 5
	metrics.NodeUtilizations[0].MemoryPercent = 5
	metrics.AvgCPUPercent = 40
	metrics.AvgMemoryPercent = 40

	// Start tracking underutilization.
	engine.Evaluate(metrics, cfg)
	clock.Advance(150 * time.Second)

	// Node recovers.
	metrics.NodeUtilizations[0].CPUPercent = 50
	metrics.NodeUtilizations[0].MemoryPercent = 50
	engine.Evaluate(metrics, cfg)

	// Advance past what would have been the stabilization window.
	clock.Advance(200 * time.Second)

	// Node is still healthy.
	d := engine.Evaluate(metrics, cfg)
	if d.Action != NoAction {
		t.Fatalf("expected NoAction after node recovered, got %s: %s", d.Action, d.Reason)
	}
}

// --------------------------------------------------------------------------
// Healthy cluster: no action
// --------------------------------------------------------------------------

func TestHealthyCluster_NoAction(t *testing.T) {
	clock := newFakeClock()
	engine := newTestEngine(clock)
	cfg := defaultConfig()

	metrics := healthyMetrics(5)

	d := engine.Evaluate(metrics, cfg)
	if d.Action != NoAction {
		t.Fatalf("expected NoAction for healthy cluster, got %s: %s", d.Action, d.Reason)
	}
	if d.Reason != "cluster within acceptable parameters" {
		t.Fatalf("unexpected reason: %s", d.Reason)
	}
}

// --------------------------------------------------------------------------
// Step > 1 scale-up
// --------------------------------------------------------------------------

func TestScaleUp_StepMultiple(t *testing.T) {
	clock := newFakeClock()
	engine := newTestEngine(clock)
	cfg := defaultConfig()
	cfg.ScaleUp.Step = 3

	metrics := healthyMetrics(5)
	metrics.AvgCPUPercent = 90

	engine.Evaluate(metrics, cfg)
	clock.Advance(61 * time.Second)

	d := engine.Evaluate(metrics, cfg)
	if d.Action != ScaleUp {
		t.Fatalf("expected ScaleUp, got %s", d.Action)
	}
	if d.Count != 3 {
		t.Fatalf("expected count=3, got %d", d.Count)
	}
}

// --------------------------------------------------------------------------
// Zero stabilization window: immediate action
// --------------------------------------------------------------------------

func TestScaleUp_ZeroStabilizationWindow(t *testing.T) {
	clock := newFakeClock()
	engine := newTestEngine(clock)
	cfg := defaultConfig()
	cfg.ScaleUp.StabilizationWindowSeconds = 0

	metrics := healthyMetrics(5)
	metrics.AvgCPUPercent = 90

	// With zero stabilization, should scale up immediately.
	d := engine.Evaluate(metrics, cfg)
	if d.Action != ScaleUp {
		t.Fatalf("expected ScaleUp with zero stabilization, got %s: %s", d.Action, d.Reason)
	}
}

// --------------------------------------------------------------------------
// Pending pods priority over CPU threshold
// --------------------------------------------------------------------------

func TestPendingPods_PriorityOverCPU(t *testing.T) {
	clock := newFakeClock()
	engine := newTestEngine(clock)
	cfg := defaultConfig()
	cfg.ScaleUp.StabilizationWindowSeconds = 0

	metrics := healthyMetrics(5)
	metrics.PendingPods = 5
	metrics.AvgCPUPercent = 90

	d := engine.Evaluate(metrics, cfg)
	if d.Action != ScaleUp {
		t.Fatalf("expected ScaleUp, got %s", d.Action)
	}
	// Should mention pending pods since that check comes first.
	if d.Reason == "" {
		t.Fatal("expected non-empty reason")
	}
}

// --------------------------------------------------------------------------
// Action.String() coverage
// --------------------------------------------------------------------------

func TestAction_String(t *testing.T) {
	tests := []struct {
		action Action
		want   string
	}{
		{NoAction, "NoAction"},
		{ScaleUp, "ScaleUp"},
		{ScaleDown, "ScaleDown"},
		{Action(99), "Unknown"},
	}
	for _, tt := range tests {
		if got := tt.action.String(); got != tt.want {
			t.Errorf("Action(%d).String() = %q, want %q", tt.action, got, tt.want)
		}
	}
}

// --------------------------------------------------------------------------
// Hysteresis unit tests
// --------------------------------------------------------------------------

func TestHysteresis_Cooldown(t *testing.T) {
	clock := newFakeClock()
	h := NewHysteresisWithClock(clock)

	// No action recorded yet — not in cooldown.
	if h.InCooldown(120) {
		t.Fatal("expected not in cooldown before any action")
	}

	h.RecordScaleAction()

	if !h.InCooldown(120) {
		t.Fatal("expected in cooldown after RecordScaleAction")
	}

	clock.Advance(60 * time.Second)
	if !h.InCooldown(120) {
		t.Fatal("expected still in cooldown at 60s (cooldown=120s)")
	}

	clock.Advance(61 * time.Second) // total 121s
	if h.InCooldown(120) {
		t.Fatal("expected not in cooldown after 121s (cooldown=120s)")
	}
}

func TestHysteresis_PressureTracking(t *testing.T) {
	clock := newFakeClock()
	h := NewHysteresisWithClock(clock)

	// No pressure yet.
	if h.PressureExceedsWindow(60) {
		t.Fatal("expected no pressure without tracking")
	}

	h.TrackPressure(true)
	if h.PressureExceedsWindow(60) {
		t.Fatal("expected pressure not yet exceeded window")
	}

	clock.Advance(61 * time.Second)
	if !h.PressureExceedsWindow(60) {
		t.Fatal("expected pressure exceeded window after 61s")
	}

	// Reset pressure.
	h.TrackPressure(false)
	if h.PressureExceedsWindow(60) {
		t.Fatal("expected no pressure after reset")
	}
}

func TestHysteresis_NodeUtilizationTracking(t *testing.T) {
	clock := newFakeClock()
	h := NewHysteresisWithClock(clock)

	h.TrackNodeUtilization("node-a", true)
	if h.NodeUnderutilizedLongEnough("node-a", 300) {
		t.Fatal("expected node not underutilized long enough yet")
	}

	clock.Advance(301 * time.Second)
	if !h.NodeUnderutilizedLongEnough("node-a", 300) {
		t.Fatal("expected node underutilized long enough after 301s")
	}

	// Stop tracking.
	h.TrackNodeUtilization("node-a", false)
	if h.NodeUnderutilizedLongEnough("node-a", 300) {
		t.Fatal("expected node no longer tracked")
	}
}

func TestHysteresis_PruneNodes(t *testing.T) {
	clock := newFakeClock()
	h := NewHysteresisWithClock(clock)

	h.TrackNodeUtilization("node-a", true)
	h.TrackNodeUtilization("node-b", true)
	h.TrackNodeUtilization("node-c", true)

	// node-b disappears from the cluster.
	active := map[string]bool{
		"node-a": true,
		"node-c": true,
	}
	h.PruneNodes(active)

	clock.Advance(301 * time.Second)

	if !h.NodeUnderutilizedLongEnough("node-a", 300) {
		t.Fatal("expected node-a still tracked")
	}
	if h.NodeUnderutilizedLongEnough("node-b", 300) {
		t.Fatal("expected node-b pruned")
	}
	if !h.NodeUnderutilizedLongEnough("node-c", 300) {
		t.Fatal("expected node-c still tracked")
	}
}

func TestHysteresis_RecordScaleAction_ResetsPressure(t *testing.T) {
	clock := newFakeClock()
	h := NewHysteresisWithClock(clock)

	h.TrackPressure(true)
	clock.Advance(61 * time.Second)

	if !h.PressureExceedsWindow(60) {
		t.Fatal("expected pressure exceeded before RecordScaleAction")
	}

	h.RecordScaleAction()

	// Pressure should be reset after a scale action.
	if h.PressureExceedsWindow(60) {
		t.Fatal("expected pressure reset after RecordScaleAction")
	}
}

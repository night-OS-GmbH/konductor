package decision

import (
	"sync"
	"time"
)

// Clock abstracts time so tests can inject a controllable clock.
type Clock interface {
	Now() time.Time
}

// realClock uses the actual system clock.
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// Hysteresis tracks timing state to prevent scaling flapping.
// It enforces cooldown periods, stabilization windows for scale-up pressure,
// and per-node underutilization durations for scale-down.
type Hysteresis struct {
	mu sync.Mutex

	clock Clock

	// lastScaleActionTime records when the most recent scaling action completed.
	lastScaleActionTime time.Time

	// scaleUpPressureStart records when sustained scale-up pressure was first detected.
	// Reset to zero when pressure disappears.
	scaleUpPressureStart time.Time

	// nodeUnderutilizedSince tracks per-node timestamps of when each node was
	// first observed to be underutilized. Removed when the node is no longer
	// underutilized or no longer exists.
	nodeUnderutilizedSince map[string]time.Time
}

// NewHysteresis creates a Hysteresis tracker using the real system clock.
func NewHysteresis() *Hysteresis {
	return &Hysteresis{
		clock:                  realClock{},
		nodeUnderutilizedSince: make(map[string]time.Time),
	}
}

// NewHysteresisWithClock creates a Hysteresis with a caller-provided clock
// for deterministic testing.
func NewHysteresisWithClock(clock Clock) *Hysteresis {
	return &Hysteresis{
		clock:                  clock,
		nodeUnderutilizedSince: make(map[string]time.Time),
	}
}

// InCooldown returns true if a scaling action occurred within the last
// cooldownSeconds seconds.
func (h *Hysteresis) InCooldown(cooldownSeconds int32) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.lastScaleActionTime.IsZero() {
		return false
	}

	cooldown := time.Duration(cooldownSeconds) * time.Second
	return h.clock.Now().Before(h.lastScaleActionTime.Add(cooldown))
}

// RecordScaleAction records that a scaling action just happened.
// This starts a new cooldown period.
func (h *Hysteresis) RecordScaleAction() {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.lastScaleActionTime = h.clock.Now()

	// Reset pressure tracking after a scale action since conditions changed.
	h.scaleUpPressureStart = time.Time{}
}

// TrackPressure updates the scale-up pressure tracker.
// Call with underPressure=true each evaluation cycle where thresholds are exceeded;
// call with underPressure=false to reset.
func (h *Hysteresis) TrackPressure(underPressure bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if underPressure {
		if h.scaleUpPressureStart.IsZero() {
			h.scaleUpPressureStart = h.clock.Now()
		}
	} else {
		h.scaleUpPressureStart = time.Time{}
	}
}

// PressureExceedsWindow returns true if scale-up pressure has been sustained
// for at least stabilizationWindowSeconds.
func (h *Hysteresis) PressureExceedsWindow(stabilizationWindowSeconds int32) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.scaleUpPressureStart.IsZero() {
		return false
	}

	window := time.Duration(stabilizationWindowSeconds) * time.Second
	return h.clock.Now().Sub(h.scaleUpPressureStart) >= window
}

// TrackNodeUtilization records whether a specific node is currently underutilized.
// If underutilized=true and the node is not yet tracked, it starts tracking.
// If underutilized=false, it removes the node from tracking.
func (h *Hysteresis) TrackNodeUtilization(nodeName string, underutilized bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if underutilized {
		if _, exists := h.nodeUnderutilizedSince[nodeName]; !exists {
			h.nodeUnderutilizedSince[nodeName] = h.clock.Now()
		}
	} else {
		delete(h.nodeUnderutilizedSince, nodeName)
	}
}

// NodeUnderutilizedLongEnough returns true if the given node has been tracked
// as underutilized for at least stabilizationWindowSeconds.
func (h *Hysteresis) NodeUnderutilizedLongEnough(nodeName string, stabilizationWindowSeconds int32) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	since, exists := h.nodeUnderutilizedSince[nodeName]
	if !exists {
		return false
	}

	window := time.Duration(stabilizationWindowSeconds) * time.Second
	return h.clock.Now().Sub(since) >= window
}

// PruneNodes removes underutilization tracking for nodes that are no longer
// present in the cluster (not in the activeNodes set).
func (h *Hysteresis) PruneNodes(activeNodes map[string]bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for name := range h.nodeUnderutilizedSince {
		if !activeNodes[name] {
			delete(h.nodeUnderutilizedSince, name)
		}
	}
}

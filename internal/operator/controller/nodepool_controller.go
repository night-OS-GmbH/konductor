package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	konductorv1alpha1 "github.com/night-OS-GmbH/konductor/api/v1alpha1"
	"github.com/night-OS-GmbH/konductor/internal/operator/decision"
	"github.com/night-OS-GmbH/konductor/internal/provider"
)

const (
	// nodePoolRequeueInterval is the default requeue interval for NodePool reconciliation.
	nodePoolRequeueInterval = 30 * time.Second

	// labelNodePool identifies which NodePool owns a NodeClaim.
	labelNodePool = "konductor.io/nodepool"

	// labelManagedBy marks resources as managed by Konductor.
	labelManagedBy = "app.kubernetes.io/managed-by"

	// nodeClaimFinalizer prevents premature deletion of NodeClaim resources.
	nodeClaimFinalizer = "konductor.io/node-cleanup"
)

// NodePoolReconciler reconciles NodePool custom resources. On each reconciliation
// cycle it collects cluster metrics, runs the decision engine, and creates or
// marks NodeClaims for scale-up/scale-down.
type NodePoolReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Engine   *decision.Engine
	Provider provider.Provider

	metricsCollector *MetricsCollector
}

// SetupNodePoolReconciler creates and registers the NodePool controller with the manager.
func SetupNodePoolReconciler(mgr manager.Manager, engine *decision.Engine, prov provider.Provider) error {
	r := &NodePoolReconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		Engine:           engine,
		Provider:         prov,
		metricsCollector: NewMetricsCollector(mgr.GetClient()),
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&konductorv1alpha1.NodePool{}).
		Owns(&konductorv1alpha1.NodeClaim{}).
		Named("nodepool").
		Complete(r)
}

// Reconcile is the core reconciliation loop for a single NodePool resource.
func (r *NodePoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// 1. Fetch the NodePool.
	var pool konductorv1alpha1.NodePool
	if err := r.Get(ctx, req.NamespacedName, &pool); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("NodePool deleted, nothing to reconcile")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("fetching NodePool: %w", err)
	}

	// 2. List NodeClaims owned by this pool.
	var claims konductorv1alpha1.NodeClaimList
	if err := r.List(ctx, &claims, client.InNamespace(pool.Namespace), client.MatchingLabels{
		labelNodePool: pool.Name,
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("listing NodeClaims: %w", err)
	}

	// Count claims by phase.
	var readyClaims, activeClaims, pendingClaims int32
	for i := range claims.Items {
		claim := &claims.Items[i]
		switch claim.Status.Phase {
		case konductorv1alpha1.NodeClaimPhaseReady:
			readyClaims++
			activeClaims++
		case konductorv1alpha1.NodeClaimPhasePending,
			konductorv1alpha1.NodeClaimPhaseProvisioning,
			konductorv1alpha1.NodeClaimPhaseBootstrapping:
			pendingClaims++
			activeClaims++
		case konductorv1alpha1.NodeClaimPhaseDraining,
			konductorv1alpha1.NodeClaimPhaseDeleting:
			// Node is going away, do not count as active.
		case konductorv1alpha1.NodeClaimPhaseFailed:
			// Failed claims are not active capacity.
		}
	}

	// 3. Collect cluster metrics for nodes matching this pool.
	poolLabels := map[string]string{labelNodePool: pool.Name}
	clusterMetrics, err := r.metricsCollector.Collect(ctx, poolLabels)
	if err != nil {
		log.Error(err, "failed to collect cluster metrics, using partial data")
		// Continue with whatever data we have; the engine handles zero-value metrics gracefully.
		if clusterMetrics == nil {
			clusterMetrics = &decision.ClusterMetrics{
				TotalNodes: int(activeClaims),
				ReadyNodes: int(readyClaims),
			}
		}
	}

	// Override with claim-based counts since they are more accurate for pool-scoped decisions.
	clusterMetrics.TotalNodes = int(activeClaims)
	clusterMetrics.ReadyNodes = int(readyClaims)

	// 4. Build the scaling config from the NodePool spec.
	scalingCfg := decision.ScalingConfig{
		MinNodes:        pool.Spec.MinNodes,
		MaxNodes:        pool.Spec.MaxNodes,
		CooldownSeconds: pool.Spec.Scaling.CooldownSeconds,
		ScaleUp: decision.ScaleUpThresholds{
			CPUThresholdPercent:        pool.Spec.Scaling.ScaleUp.CPUThresholdPercent,
			MemoryThresholdPercent:     pool.Spec.Scaling.ScaleUp.MemoryThresholdPercent,
			PendingPodsThreshold:       pool.Spec.Scaling.ScaleUp.PendingPodsThreshold,
			StabilizationWindowSeconds: pool.Spec.Scaling.ScaleUp.StabilizationWindowSeconds,
			Step:                       pool.Spec.Scaling.ScaleUp.Step,
		},
		ScaleDown: decision.ScaleDownThresholds{
			CPUThresholdPercent:        pool.Spec.Scaling.ScaleDown.CPUThresholdPercent,
			MemoryThresholdPercent:     pool.Spec.Scaling.ScaleDown.MemoryThresholdPercent,
			StabilizationWindowSeconds: pool.Spec.Scaling.ScaleDown.StabilizationWindowSeconds,
			Step:                       pool.Spec.Scaling.ScaleDown.Step,
		},
	}

	// 5. Run the decision engine.
	d := r.Engine.Evaluate(*clusterMetrics, scalingCfg)
	log.Info("decision engine result", "action", d.Action.String(), "count", d.Count, "reason", d.Reason)

	now := metav1.Now()

	// 6. Execute the decision (or just log it when paused).
	if pool.Spec.Scaling.Paused && d.Action != decision.NoAction {
		log.Info("scaling PAUSED — would execute but skipping",
			"action", d.Action.String(), "count", d.Count, "reason", d.Reason)
		pool.Status.Phase = konductorv1alpha1.NodePoolPhaseActive
		// Still update status so the TUI shows what would happen.
	} else {
		switch d.Action {
		case decision.ScaleUp:
			if err := r.scaleUp(ctx, &pool, d.Count); err != nil {
				return ctrl.Result{}, fmt.Errorf("scaling up: %w", err)
			}
			pool.Status.Phase = konductorv1alpha1.NodePoolPhaseScaling
			pool.Status.LastScaleTime = &now
			log.Info("scale-up initiated", "count", d.Count)

		case decision.ScaleDown:
			if err := r.scaleDown(ctx, &pool, &claims, d); err != nil {
				return ctrl.Result{}, fmt.Errorf("scaling down: %w", err)
			}
			pool.Status.Phase = konductorv1alpha1.NodePoolPhaseScaling
			pool.Status.LastScaleTime = &now
			log.Info("scale-down initiated", "count", d.Count, "targets", d.TargetNodes)

		case decision.NoAction:
			if pendingClaims > 0 {
				pool.Status.Phase = konductorv1alpha1.NodePoolPhaseScaling
			} else if readyClaims < pool.Spec.MinNodes {
				pool.Status.Phase = konductorv1alpha1.NodePoolPhaseDegraded
			} else {
				pool.Status.Phase = konductorv1alpha1.NodePoolPhaseActive
			}
		}
	}

	// 7. Update NodePool status.
	pool.Status.CurrentNodes = activeClaims
	pool.Status.DesiredNodes = computeDesired(activeClaims, d)
	pool.Status.ReadyNodes = readyClaims

	if err := r.Status().Update(ctx, &pool); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating NodePool status: %w", err)
	}

	return ctrl.Result{RequeueAfter: nodePoolRequeueInterval}, nil
}

// scaleUp creates new NodeClaim resources for the requested count.
func (r *NodePoolReconciler) scaleUp(ctx context.Context, pool *konductorv1alpha1.NodePool, count int) error {
	log := log.FromContext(ctx)

	for i := 0; i < count; i++ {
		claimName := fmt.Sprintf("%s-%s", pool.Name, generateSuffix())

		claim := &konductorv1alpha1.NodeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      claimName,
				Namespace: pool.Namespace,
				Labels: map[string]string{
					labelNodePool:  pool.Name,
					labelManagedBy: "konductor",
				},
			},
			Spec: konductorv1alpha1.NodeClaimSpec{
				NodePoolRef:    pool.Name,
				Provider:       pool.Spec.Provider,
				ProviderConfig: *pool.Spec.ProviderConfig.DeepCopy(),
				Talos:          pool.Spec.Talos,
				NodeTemplate:   *pool.Spec.NodeTemplate.DeepCopy(),
			},
		}

		// Set the NodePool as the owner so that deleting the pool cascades to claims.
		if err := controllerutil.SetControllerReference(pool, claim, r.Scheme); err != nil {
			return fmt.Errorf("setting owner reference on NodeClaim %s: %w", claimName, err)
		}

		// Add finalizer so the NodeClaim controller can clean up infrastructure.
		controllerutil.AddFinalizer(claim, nodeClaimFinalizer)

		claim.Status.Phase = konductorv1alpha1.NodeClaimPhasePending

		if err := r.Create(ctx, claim); err != nil {
			return fmt.Errorf("creating NodeClaim %s: %w", claimName, err)
		}

		log.Info("created NodeClaim", "name", claimName)
	}

	return nil
}

// scaleDown identifies NodeClaims to drain based on the decision engine's target nodes.
func (r *NodePoolReconciler) scaleDown(ctx context.Context, pool *konductorv1alpha1.NodePool, claims *konductorv1alpha1.NodeClaimList, d decision.Decision) error {
	log := log.FromContext(ctx)

	targetSet := make(map[string]bool, len(d.TargetNodes))
	for _, name := range d.TargetNodes {
		targetSet[name] = true
	}

	drained := 0
	for i := range claims.Items {
		if drained >= d.Count {
			break
		}

		claim := &claims.Items[i]

		// Only drain nodes that are Ready and match the target list.
		if claim.Status.Phase != konductorv1alpha1.NodeClaimPhaseReady {
			continue
		}

		// Match by K8s node name (set when the node joined the cluster).
		if len(targetSet) > 0 && !targetSet[claim.Status.NodeName] {
			continue
		}

		claim.Status.Phase = konductorv1alpha1.NodeClaimPhaseDraining
		if err := r.Status().Update(ctx, claim); err != nil {
			return fmt.Errorf("setting NodeClaim %s to Draining: %w", claim.Name, err)
		}

		log.Info("marked NodeClaim for draining", "name", claim.Name, "nodeName", claim.Status.NodeName)
		drained++
	}

	// If the engine did not provide specific targets (e.g. no per-node utilization data),
	// drain the oldest ready claims up to d.Count.
	if drained == 0 && len(targetSet) == 0 && d.Count > 0 {
		for i := range claims.Items {
			if drained >= d.Count {
				break
			}
			claim := &claims.Items[i]
			if claim.Status.Phase != konductorv1alpha1.NodeClaimPhaseReady {
				continue
			}

			claim.Status.Phase = konductorv1alpha1.NodeClaimPhaseDraining
			if err := r.Status().Update(ctx, claim); err != nil {
				return fmt.Errorf("setting NodeClaim %s to Draining: %w", claim.Name, err)
			}

			log.Info("marked NodeClaim for draining (no target list)", "name", claim.Name)
			drained++
		}
	}

	return nil
}

// computeDesired calculates the target node count after applying the decision.
func computeDesired(current int32, d decision.Decision) int32 {
	switch d.Action {
	case decision.ScaleUp:
		return current + int32(d.Count)
	case decision.ScaleDown:
		result := current - int32(d.Count)
		if result < 0 {
			return 0
		}
		return result
	default:
		return current
	}
}

// generateSuffix returns a short random-ish suffix for NodeClaim names based
// on the current time to avoid collisions without requiring a crypto RNG.
func generateSuffix() string {
	n := time.Now().UnixNano()
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	buf := make([]byte, 5)
	for i := range buf {
		buf[i] = chars[n%int64(len(chars))]
		n /= int64(len(chars))
	}
	return string(buf)
}

// ensureNodePoolLabel is a helper to look up the NodePool for a given claim.
// It is used by the NodeClaim controller to verify ownership.
func LookupNodePool(ctx context.Context, c client.Client, claim *konductorv1alpha1.NodeClaim) (*konductorv1alpha1.NodePool, error) {
	poolName := claim.Spec.NodePoolRef
	if poolName == "" {
		if label, ok := claim.Labels[labelNodePool]; ok {
			poolName = label
		} else {
			return nil, fmt.Errorf("NodeClaim %s has no nodePoolRef or %s label", claim.Name, labelNodePool)
		}
	}

	var pool konductorv1alpha1.NodePool
	if err := c.Get(ctx, types.NamespacedName{
		Namespace: claim.Namespace,
		Name:      poolName,
	}, &pool); err != nil {
		return nil, fmt.Errorf("fetching NodePool %s: %w", poolName, err)
	}

	return &pool, nil
}

// isNodeReady checks whether a Kubernetes node has the Ready condition set to True.
func isNodeReady(node *corev1.Node) bool {
	for _, c := range node.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

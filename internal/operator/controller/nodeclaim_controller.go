package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	policyv1 "k8s.io/api/policy/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	konductorv1alpha1 "github.com/night-OS-GmbH/konductor/api/v1alpha1"
	"github.com/night-OS-GmbH/konductor/internal/provider"
	"github.com/night-OS-GmbH/konductor/internal/talos"
)

const (
	// joiningTimeout is how long a node has to join the cluster before the claim
	// transitions to Failed.
	joiningTimeout = 5 * time.Minute

	// requeuePending is the requeue delay after creating infrastructure.
	requeuePending = 10 * time.Second

	// requeueProvisioning is the requeue delay while waiting for the server to start.
	requeueProvisioning = 10 * time.Second

	// requeueJoining is the requeue delay while waiting for the K8s node to appear.
	requeueJoining = 15 * time.Second

	// requeueDraining is the requeue delay while waiting for drain to complete.
	requeueDraining = 5 * time.Second

	// requeueDeleting is the requeue delay while waiting for deletion.
	requeueDeleting = 5 * time.Second

	// requeueReady is the requeue interval for periodic health checks on ready nodes.
	requeueReady = 60 * time.Second
)

// NodeClaimReconciler implements the state machine for individual node lifecycle
// management. Each NodeClaim progresses through phases:
// Pending -> Provisioning -> Joining -> Ready -> Draining -> Deleting -> Deleted.
type NodeClaimReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Provider    provider.Provider
	TalosClient *talos.Client
}

// SetupNodeClaimReconciler creates and registers the NodeClaim controller with the manager.
func SetupNodeClaimReconciler(mgr manager.Manager, prov provider.Provider, talosClient *talos.Client) error {
	r := &NodeClaimReconciler{
		Client:      mgr.GetClient(),
		Scheme:      mgr.GetScheme(),
		Provider:    prov,
		TalosClient: talosClient,
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&konductorv1alpha1.NodeClaim{}).
		Named("nodeclaim").
		Complete(r)
}

// Reconcile drives the NodeClaim state machine. The behavior depends on the
// current phase stored in claim.Status.Phase.
func (r *NodeClaimReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	var claim konductorv1alpha1.NodeClaim
	if err := r.Get(ctx, req.NamespacedName, &claim); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("fetching NodeClaim: %w", err)
	}

	// Handle deletion via finalizer.
	if !claim.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&claim, nodeClaimFinalizer) {
			if err := r.cleanupInfrastructure(ctx, &claim); err != nil {
				log.Error(err, "failed to clean up infrastructure during deletion")
				return ctrl.Result{RequeueAfter: requeueDeleting}, nil
			}
			controllerutil.RemoveFinalizer(&claim, nodeClaimFinalizer)
			if err := r.Update(ctx, &claim); err != nil {
				return ctrl.Result{}, fmt.Errorf("removing finalizer: %w", err)
			}
		}
		return ctrl.Result{}, nil
	}

	log.Info("reconciling NodeClaim", "phase", claim.Status.Phase, "providerID", claim.Status.ProviderID)

	switch claim.Status.Phase {
	case konductorv1alpha1.NodeClaimPhasePending, "":
		return r.reconcilePending(ctx, &claim)
	case konductorv1alpha1.NodeClaimPhaseProvisioning:
		return r.reconcileProvisioning(ctx, &claim)
	case konductorv1alpha1.NodeClaimPhaseBootstrapping:
		return r.reconcileJoining(ctx, &claim)
	case konductorv1alpha1.NodeClaimPhaseReady:
		return r.reconcileReady(ctx, &claim)
	case konductorv1alpha1.NodeClaimPhaseDraining:
		return r.reconcileDraining(ctx, &claim)
	case konductorv1alpha1.NodeClaimPhaseDeleting:
		return r.reconcileDeleting(ctx, &claim)
	case konductorv1alpha1.NodeClaimPhaseFailed:
		// Failed claims remain for inspection. No automatic retry.
		return ctrl.Result{}, nil
	default:
		log.Info("unknown phase, no action", "phase", claim.Status.Phase)
		return ctrl.Result{}, nil
	}
}

// reconcilePending handles the Pending phase: reads the Talos machine config
// from the referenced Secret, provisions infrastructure via the provider, and
// transitions to Provisioning.
func (r *NodeClaimReconciler) reconcilePending(ctx context.Context, claim *konductorv1alpha1.NodeClaim) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Read the Talos machine config from the referenced Secret.
	talosConfig, err := r.readTalosConfig(ctx, claim)
	if err != nil {
		return r.setFailed(ctx, claim, "ReadTalosConfig", err)
	}

	// Render the config with node-specific parameters.
	nodeName := claim.Name
	renderedConfig, err := talos.RenderWorkerConfig(talosConfig, talos.WorkerConfigParams{
		Hostname: nodeName,
	})
	if err != nil {
		return r.setFailed(ctx, claim, "RenderConfig", err)
	}

	// Build provider labels from the pool template and claim metadata.
	providerLabels := map[string]string{
		labelNodePool:  claim.Spec.NodePoolRef,
		labelManagedBy: "konductor",
	}
	for k, v := range claim.Spec.ProviderConfig.Labels {
		providerLabels[k] = v
	}

	// Provision the node.
	opts := provider.CreateNodeOpts{
		Name:               nodeName,
		ServerType:         claim.Spec.ProviderConfig.ServerType,
		Location:           claim.Spec.ProviderConfig.Location,
		Labels:             providerLabels,
		UserData:           renderedConfig,
		SSHKeyName:         claim.Spec.ProviderConfig.SSHKeyName,
		NetworkName:        claim.Spec.ProviderConfig.Network,
		PlacementGroupName: claim.Spec.ProviderConfig.PlacementGroup,
	}

	node, err := r.Provider.CreateNode(ctx, opts)
	if err != nil {
		return r.setFailed(ctx, claim, "CreateNode", err)
	}

	log.Info("infrastructure created", "providerID", node.ProviderID, "name", node.Name)

	// Transition to Provisioning.
	now := metav1.Now()
	claim.Status.Phase = konductorv1alpha1.NodeClaimPhaseProvisioning
	claim.Status.ProviderID = node.ProviderID
	claim.Status.CreatedAt = &now

	if node.InternalIP != "" || node.ExternalIP != "" {
		claim.Status.IPs = collectIPs(node)
	}

	if err := r.Status().Update(ctx, claim); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating status to Provisioning: %w", err)
	}

	return ctrl.Result{RequeueAfter: requeuePending}, nil
}

// reconcileProvisioning checks whether the provider node is running and
// transitions to Joining once it is.
func (r *NodeClaimReconciler) reconcileProvisioning(ctx context.Context, claim *konductorv1alpha1.NodeClaim) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	node, err := r.Provider.GetNode(ctx, claim.Status.ProviderID)
	if err != nil {
		log.Error(err, "failed to get provider node status")
		return ctrl.Result{RequeueAfter: requeueProvisioning}, nil
	}

	// Update IPs if they appeared.
	claim.Status.IPs = collectIPs(node)

	switch node.Status {
	case provider.NodeStatusRunning:
		log.Info("provider node is running, transitioning to Joining", "providerID", node.ProviderID)
		claim.Status.Phase = konductorv1alpha1.NodeClaimPhaseBootstrapping
		if err := r.Status().Update(ctx, claim); err != nil {
			return ctrl.Result{}, fmt.Errorf("updating status to Joining: %w", err)
		}
		return ctrl.Result{RequeueAfter: requeueJoining}, nil

	case provider.NodeStatusError:
		return r.setFailed(ctx, claim, "ProviderError", fmt.Errorf("provider reports node in error state"))

	default:
		log.Info("waiting for provider node", "status", node.Status)
		if err := r.Status().Update(ctx, claim); err != nil {
			return ctrl.Result{}, fmt.Errorf("updating IPs: %w", err)
		}
		return ctrl.Result{RequeueAfter: requeueProvisioning}, nil
	}
}

// reconcileJoining waits for the provisioned node to appear as a Kubernetes
// node in the cluster. It matches by provider ID or IP address.
func (r *NodeClaimReconciler) reconcileJoining(ctx context.Context, claim *konductorv1alpha1.NodeClaim) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Check for joining timeout.
	if claim.Status.CreatedAt != nil {
		elapsed := time.Since(claim.Status.CreatedAt.Time)
		if elapsed > joiningTimeout {
			return r.setFailed(ctx, claim, "JoinTimeout",
				fmt.Errorf("node did not join cluster within %s", joiningTimeout))
		}
	}

	// List all Kubernetes nodes to find the one matching this claim.
	var nodeList corev1.NodeList
	if err := r.List(ctx, &nodeList); err != nil {
		return ctrl.Result{}, fmt.Errorf("listing K8s nodes: %w", err)
	}

	for i := range nodeList.Items {
		k8sNode := &nodeList.Items[i]

		if r.nodeMatchesClaim(k8sNode, claim) {
			if isNodeReady(k8sNode) {
				now := metav1.Now()
				claim.Status.Phase = konductorv1alpha1.NodeClaimPhaseReady
				claim.Status.NodeName = k8sNode.Name
				claim.Status.ReadyAt = &now

				log.Info("node joined and is Ready", "k8sNode", k8sNode.Name)

				if err := r.Status().Update(ctx, claim); err != nil {
					return ctrl.Result{}, fmt.Errorf("updating status to Ready: %w", err)
				}
				return ctrl.Result{RequeueAfter: requeueReady}, nil
			}

			// Node exists but is not Ready yet. Keep waiting.
			claim.Status.NodeName = k8sNode.Name
			if err := r.Status().Update(ctx, claim); err != nil {
				return ctrl.Result{}, fmt.Errorf("updating node name: %w", err)
			}

			log.Info("node found but not Ready yet", "k8sNode", k8sNode.Name)
			return ctrl.Result{RequeueAfter: requeueJoining}, nil
		}
	}

	log.Info("waiting for K8s node to appear", "providerID", claim.Status.ProviderID, "ips", claim.Status.IPs)
	return ctrl.Result{RequeueAfter: requeueJoining}, nil
}

// reconcileReady verifies that the Kubernetes node still exists and is healthy.
func (r *NodeClaimReconciler) reconcileReady(ctx context.Context, claim *konductorv1alpha1.NodeClaim) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	if claim.Status.NodeName == "" {
		log.Info("Ready claim has no node name, returning to Joining")
		claim.Status.Phase = konductorv1alpha1.NodeClaimPhaseBootstrapping
		if err := r.Status().Update(ctx, claim); err != nil {
			return ctrl.Result{}, fmt.Errorf("reverting to Joining: %w", err)
		}
		return ctrl.Result{RequeueAfter: requeueJoining}, nil
	}

	var node corev1.Node
	if err := r.Get(ctx, client.ObjectKey{Name: claim.Status.NodeName}, &node); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("K8s node disappeared, marking claim as Failed", "nodeName", claim.Status.NodeName)
			return r.setFailed(ctx, claim, "NodeDisappeared",
				fmt.Errorf("kubernetes node %s no longer exists", claim.Status.NodeName))
		}
		return ctrl.Result{}, fmt.Errorf("checking node health: %w", err)
	}

	if !isNodeReady(&node) {
		log.Info("node is no longer Ready", "nodeName", node.Name)
		// Stay in Ready phase but set a condition. The node might recover.
		setCondition(claim, "NodeReady", metav1.ConditionFalse, "NodeNotReady", "Node is no longer reporting Ready")
		if err := r.Status().Update(ctx, claim); err != nil {
			return ctrl.Result{}, fmt.Errorf("updating conditions: %w", err)
		}
	} else {
		setCondition(claim, "NodeReady", metav1.ConditionTrue, "NodeReady", "Node is healthy")
		if err := r.Status().Update(ctx, claim); err != nil {
			return ctrl.Result{}, fmt.Errorf("updating conditions: %w", err)
		}
	}

	return ctrl.Result{RequeueAfter: requeueReady}, nil
}

// reconcileDraining cordons the node, evicts pods respecting PDBs, resets the
// Talos node, and transitions to Deleting.
func (r *NodeClaimReconciler) reconcileDraining(ctx context.Context, claim *konductorv1alpha1.NodeClaim) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	if claim.Status.NodeName == "" {
		// No K8s node to drain, skip straight to deleting.
		log.Info("no K8s node associated, skipping drain")
		claim.Status.Phase = konductorv1alpha1.NodeClaimPhaseDeleting
		if err := r.Status().Update(ctx, claim); err != nil {
			return ctrl.Result{}, fmt.Errorf("updating status to Deleting: %w", err)
		}
		return ctrl.Result{RequeueAfter: requeueDeleting}, nil
	}

	// Step 1: Cordon the node (mark unschedulable).
	var node corev1.Node
	if err := r.Get(ctx, client.ObjectKey{Name: claim.Status.NodeName}, &node); err != nil {
		if apierrors.IsNotFound(err) {
			// Node already gone, proceed to deletion.
			log.Info("K8s node already deleted, skipping to Deleting")
			claim.Status.Phase = konductorv1alpha1.NodeClaimPhaseDeleting
			if err := r.Status().Update(ctx, claim); err != nil {
				return ctrl.Result{}, fmt.Errorf("updating status to Deleting: %w", err)
			}
			return ctrl.Result{RequeueAfter: requeueDeleting}, nil
		}
		return ctrl.Result{}, fmt.Errorf("getting node for drain: %w", err)
	}

	if !node.Spec.Unschedulable {
		node.Spec.Unschedulable = true
		if err := r.Update(ctx, &node); err != nil {
			return ctrl.Result{}, fmt.Errorf("cordoning node %s: %w", node.Name, err)
		}
		log.Info("cordoned node", "nodeName", node.Name)
	}

	// Step 2: Evict pods (respecting PDBs).
	drained, err := r.evictPods(ctx, claim.Status.NodeName)
	if err != nil {
		log.Error(err, "error evicting pods, will retry")
		return ctrl.Result{RequeueAfter: requeueDraining}, nil
	}

	if !drained {
		log.Info("drain in progress, pods still running on node", "nodeName", claim.Status.NodeName)
		return ctrl.Result{RequeueAfter: requeueDraining}, nil
	}

	log.Info("node drained successfully", "nodeName", claim.Status.NodeName)

	// Step 3: Reset the Talos node to wipe state.
	if r.TalosClient != nil && len(claim.Status.IPs) > 0 {
		nodeIP := claim.Status.IPs[0]
		if err := r.TalosClient.ResetNode(ctx, nodeIP); err != nil {
			log.Error(err, "failed to reset Talos node, proceeding to delete anyway", "nodeIP", nodeIP)
			// Non-fatal: the server will be deleted anyway.
		} else {
			log.Info("Talos node reset", "nodeIP", nodeIP)
		}
	}

	// Transition to Deleting.
	claim.Status.Phase = konductorv1alpha1.NodeClaimPhaseDeleting
	if err := r.Status().Update(ctx, claim); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating status to Deleting: %w", err)
	}

	return ctrl.Result{RequeueAfter: requeueDeleting}, nil
}

// reconcileDeleting removes the Kubernetes node object and the provider infrastructure.
func (r *NodeClaimReconciler) reconcileDeleting(ctx context.Context, claim *konductorv1alpha1.NodeClaim) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Step 1: Delete the Kubernetes node object.
	if claim.Status.NodeName != "" {
		var node corev1.Node
		if err := r.Get(ctx, client.ObjectKey{Name: claim.Status.NodeName}, &node); err != nil {
			if !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("checking K8s node: %w", err)
			}
			// Already gone.
		} else {
			if err := r.Delete(ctx, &node); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("deleting K8s node %s: %w", node.Name, err)
			}
			log.Info("deleted K8s node", "nodeName", node.Name)
		}
	}

	// Step 2: Delete the provider infrastructure.
	if claim.Status.ProviderID != "" {
		if err := r.Provider.DeleteNode(ctx, claim.Status.ProviderID); err != nil {
			log.Error(err, "failed to delete provider node", "providerID", claim.Status.ProviderID)
			return ctrl.Result{RequeueAfter: requeueDeleting}, nil
		}
		log.Info("deleted provider node", "providerID", claim.Status.ProviderID)
	}

	// Step 3: Remove the finalizer so the NodeClaim object is garbage-collected.
	if controllerutil.ContainsFinalizer(claim, nodeClaimFinalizer) {
		controllerutil.RemoveFinalizer(claim, nodeClaimFinalizer)
		if err := r.Update(ctx, claim); err != nil {
			return ctrl.Result{}, fmt.Errorf("removing finalizer: %w", err)
		}
	}

	log.Info("NodeClaim cleanup complete", "name", claim.Name)
	return ctrl.Result{}, nil
}

// cleanupInfrastructure is called during object deletion (via finalizer) to
// ensure provider resources are released even if the state machine was interrupted.
func (r *NodeClaimReconciler) cleanupInfrastructure(ctx context.Context, claim *konductorv1alpha1.NodeClaim) error {
	log := log.FromContext(ctx)

	if claim.Status.ProviderID != "" {
		if err := r.Provider.DeleteNode(ctx, claim.Status.ProviderID); err != nil {
			return fmt.Errorf("deleting provider node %s: %w", claim.Status.ProviderID, err)
		}
		log.Info("cleaned up provider infrastructure", "providerID", claim.Status.ProviderID)
	}

	if claim.Status.NodeName != "" {
		var node corev1.Node
		if err := r.Get(ctx, client.ObjectKey{Name: claim.Status.NodeName}, &node); err == nil {
			if err := r.Delete(ctx, &node); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("deleting K8s node %s: %w", node.Name, err)
			}
			log.Info("cleaned up K8s node", "nodeName", node.Name)
		}
	}

	return nil
}

// evictPods evicts all non-system pods from the specified node, respecting PDBs.
// Returns true if all evictable pods have been evicted.
func (r *NodeClaimReconciler) evictPods(ctx context.Context, nodeName string) (bool, error) {
	log := log.FromContext(ctx)

	var podList corev1.PodList
	if err := r.List(ctx, &podList, client.MatchingFields{"spec.nodeName": nodeName}); err != nil {
		// Field selectors may not be indexed. Fall back to listing all pods and filtering.
		if err := r.List(ctx, &podList); err != nil {
			return false, fmt.Errorf("listing pods: %w", err)
		}
	}

	evictable := 0
	for i := range podList.Items {
		pod := &podList.Items[i]

		// Skip pods not on this node (in case we fell back to unfiltered list).
		if pod.Spec.NodeName != nodeName {
			continue
		}

		// Skip mirror pods (managed by kubelet, not evictable).
		if isMirrorPod(pod) {
			continue
		}

		// Skip DaemonSet pods (they will be recreated immediately).
		if isDaemonSetPod(pod) {
			continue
		}

		// Skip already-terminated pods.
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}

		evictable++

		eviction := &policyv1.Eviction{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pod.Name,
				Namespace: pod.Namespace,
			},
		}

		if err := r.SubResource("eviction").Create(ctx, pod, eviction); err != nil {
			if apierrors.IsNotFound(err) {
				// Pod already gone.
				evictable--
				continue
			}
			if apierrors.IsTooManyRequests(err) {
				// PDB is blocking eviction. Will retry on next reconciliation.
				log.Info("PDB blocking eviction", "pod", pod.Name, "namespace", pod.Namespace)
				continue
			}
			return false, fmt.Errorf("evicting pod %s/%s: %w", pod.Namespace, pod.Name, err)
		}

		log.V(1).Info("evicted pod", "pod", pod.Name, "namespace", pod.Namespace)
	}

	return evictable == 0, nil
}

// nodeMatchesClaim returns true if a Kubernetes node corresponds to this NodeClaim.
// Matching is done by provider ID (spec.providerID) or by IP address.
func (r *NodeClaimReconciler) nodeMatchesClaim(node *corev1.Node, claim *konductorv1alpha1.NodeClaim) bool {
	// Match by provider ID if both are set.
	if claim.Status.ProviderID != "" && node.Spec.ProviderID != "" {
		if node.Spec.ProviderID == claim.Status.ProviderID {
			return true
		}
	}

	// Match by node name matching claim name.
	if node.Name == claim.Name {
		return true
	}

	// Match by IP address.
	if len(claim.Status.IPs) > 0 {
		claimIPs := make(map[string]bool, len(claim.Status.IPs))
		for _, ip := range claim.Status.IPs {
			claimIPs[ip] = true
		}

		for _, addr := range node.Status.Addresses {
			if (addr.Type == corev1.NodeInternalIP || addr.Type == corev1.NodeExternalIP) && claimIPs[addr.Address] {
				return true
			}
		}
	}

	return false
}

// readTalosConfig reads the Talos machine configuration from the Secret
// referenced by the NodeClaim's spec.
func (r *NodeClaimReconciler) readTalosConfig(ctx context.Context, claim *konductorv1alpha1.NodeClaim) (string, error) {
	secretName := claim.Spec.Talos.ConfigSecretRef
	if secretName == "" {
		return "", fmt.Errorf("NodeClaim %s has no talos.configSecretRef", claim.Name)
	}

	var secret corev1.Secret
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: claim.Namespace,
		Name:      secretName,
	}, &secret); err != nil {
		return "", fmt.Errorf("reading Talos config secret %s: %w", secretName, err)
	}

	// Look for "worker.yaml" or "config" key in the secret data.
	for _, key := range []string{"talos-worker-config", "worker.yaml", "config", "machine-config"} {
		if data, ok := secret.Data[key]; ok {
			return string(data), nil
		}
	}

	// Fall back to the first key.
	for _, data := range secret.Data {
		return string(data), nil
	}

	return "", fmt.Errorf("secret %s has no data", secretName)
}

// setFailed transitions the NodeClaim to the Failed phase with the given reason and error.
func (r *NodeClaimReconciler) setFailed(ctx context.Context, claim *konductorv1alpha1.NodeClaim, reason string, err error) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	log.Error(err, "NodeClaim failed", "reason", reason)

	claim.Status.Phase = konductorv1alpha1.NodeClaimPhaseFailed
	claim.Status.FailureReason = reason
	claim.Status.FailureMessage = err.Error()

	setCondition(claim, "Ready", metav1.ConditionFalse, reason, err.Error())

	if updateErr := r.Status().Update(ctx, claim); updateErr != nil {
		return ctrl.Result{}, fmt.Errorf("updating status to Failed: %w", updateErr)
	}

	return ctrl.Result{}, nil
}

// collectIPs gathers IP addresses from a provider node into a string slice.
func collectIPs(node *provider.ProviderNode) []string {
	var ips []string
	if node.InternalIP != "" {
		ips = append(ips, node.InternalIP)
	}
	if node.ExternalIP != "" {
		ips = append(ips, node.ExternalIP)
	}
	return ips
}

// setCondition updates or appends a condition on the NodeClaim status.
func setCondition(claim *konductorv1alpha1.NodeClaim, condType string, status metav1.ConditionStatus, reason, message string) {
	now := metav1.Now()
	for i := range claim.Status.Conditions {
		if claim.Status.Conditions[i].Type == condType {
			claim.Status.Conditions[i].Status = status
			claim.Status.Conditions[i].Reason = reason
			claim.Status.Conditions[i].Message = message
			claim.Status.Conditions[i].LastTransitionTime = now
			return
		}
	}
	claim.Status.Conditions = append(claim.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: now,
	})
}

// isMirrorPod returns true if the pod is a static/mirror pod managed by kubelet.
func isMirrorPod(pod *corev1.Pod) bool {
	_, ok := pod.Annotations[corev1.MirrorPodAnnotationKey]
	return ok
}

// isDaemonSetPod returns true if the pod is owned by a DaemonSet.
func isDaemonSetPod(pod *corev1.Pod) bool {
	for _, ref := range pod.OwnerReferences {
		if ref.Kind == "DaemonSet" {
			return true
		}
	}
	return false
}

// isSystemNamespace returns true for namespaces that contain system-critical pods.
func isSystemNamespace(ns string) bool {
	return strings.HasPrefix(ns, "kube-") || ns == "konductor-system"
}

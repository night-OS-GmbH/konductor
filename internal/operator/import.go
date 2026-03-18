package operator

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

var (
	nodePoolGVR = schema.GroupVersionResource{
		Group:    "konductor.io",
		Version:  "v1alpha1",
		Resource: "nodepools",
	}
	nodeClaimGVR = schema.GroupVersionResource{
		Group:    "konductor.io",
		Version:  "v1alpha1",
		Resource: "nodeclaims",
	}
)

// DiscoveredNode represents an existing Kubernetes node discovered for import.
type DiscoveredNode struct {
	Name       string
	Role       string // "worker" or "control-plane"
	ServerType string // from label node.kubernetes.io/instance-type
	Location   string // from label topology.kubernetes.io/zone or region
	ProviderID string // from node.spec.providerID
	InternalIP string
	Ready      bool
}

// SuggestedPool groups discovered nodes that share the same role, server type,
// and location into a candidate NodePool for import.
type SuggestedPool struct {
	Name       string
	Role       string
	ServerType string
	Location   string
	Nodes      []DiscoveredNode
}

// DiscoverNodes lists all Kubernetes nodes and extracts their role, server type,
// location, provider ID, and readiness status.
func DiscoverNodes(ctx context.Context, clientset kubernetes.Interface) ([]DiscoveredNode, error) {
	nodeList, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing nodes: %w", err)
	}

	var discovered []DiscoveredNode
	for _, node := range nodeList.Items {
		d := DiscoveredNode{
			Name:       node.Name,
			ProviderID: node.Spec.ProviderID,
		}

		// Detect role from labels.
		labels := node.Labels
		if _, ok := labels["node-role.kubernetes.io/control-plane"]; ok {
			d.Role = "control-plane"
		} else {
			d.Role = "worker"
		}

		// Server type from Hetzner CCM label.
		d.ServerType = labels["node.kubernetes.io/instance-type"]

		// Location from topology label (zone or region).
		if zone, ok := labels["topology.kubernetes.io/zone"]; ok {
			d.Location = zone
		} else if region, ok := labels["topology.kubernetes.io/region"]; ok {
			d.Location = region
		}

		// Extract internal IP.
		for _, addr := range node.Status.Addresses {
			if addr.Type == corev1.NodeInternalIP {
				d.InternalIP = addr.Address
				break
			}
		}

		// Check readiness.
		for _, cond := range node.Status.Conditions {
			if cond.Type == corev1.NodeReady {
				d.Ready = cond.Status == corev1.ConditionTrue
				break
			}
		}

		discovered = append(discovered, d)
	}

	return discovered, nil
}

// SuggestPools groups discovered nodes by role + serverType + location and
// generates pool names like "workers-cpx31-nbg1" or "control-planes-cx33-nbg1".
func SuggestPools(nodes []DiscoveredNode) []SuggestedPool {
	type poolKey struct {
		role       string
		serverType string
		location   string
	}

	groups := make(map[poolKey][]DiscoveredNode)
	for _, n := range nodes {
		k := poolKey{
			role:       n.Role,
			serverType: n.ServerType,
			location:   n.Location,
		}
		groups[k] = append(groups[k], n)
	}

	// Sort keys for deterministic output.
	keys := make([]poolKey, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].role != keys[j].role {
			// control-plane first.
			return keys[i].role == "control-plane"
		}
		if keys[i].serverType != keys[j].serverType {
			return keys[i].serverType < keys[j].serverType
		}
		return keys[i].location < keys[j].location
	})

	var pools []SuggestedPool
	for _, k := range keys {
		poolNodes := groups[k]

		// Generate a descriptive pool name.
		var rolePart string
		if k.role == "control-plane" {
			rolePart = "control-planes"
		} else {
			rolePart = "workers"
		}

		parts := []string{rolePart}
		if k.serverType != "" {
			parts = append(parts, k.serverType)
		}
		if k.location != "" {
			parts = append(parts, k.location)
		}

		pools = append(pools, SuggestedPool{
			Name:       strings.Join(parts, "-"),
			Role:       k.role,
			ServerType: k.serverType,
			Location:   k.location,
			Nodes:      poolNodes,
		})
	}

	return pools
}

// ImportNodes creates a NodePool CR for the given pool, labels each node with
// the pool name, and creates a NodeClaim CR per node with status.phase=Ready.
// The NodePool is created with scaling.enabled=false as a safe default.
func ImportNodes(ctx context.Context, dynClient dynamic.Interface, clientset kubernetes.Interface, pool SuggestedPool) error {
	nodeCount := int64(len(pool.Nodes))

	// 1. Create the NodePool CR.
	nodePoolObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "konductor.io/v1alpha1",
			"kind":       "NodePool",
			"metadata": map[string]interface{}{
				"name": pool.Name,
			},
			"spec": map[string]interface{}{
				"role":     pool.Role,
				"provider": "hetzner",
				"providerConfig": map[string]interface{}{
					"serverType": pool.ServerType,
					"location":   pool.Location,
				},
				"minNodes": nodeCount,
				"maxNodes": nodeCount,
				"scaling": map[string]interface{}{
					"enabled":         false,
					"cooldownSeconds": int64(300),
					"scaleUp": map[string]interface{}{
						"cpuThresholdPercent":        int64(80),
						"memoryThresholdPercent":     int64(80),
						"pendingPodsThreshold":       int64(1),
						"stabilizationWindowSeconds": int64(60),
						"step":                       int64(1),
					},
					"scaleDown": map[string]interface{}{
						"cpuThresholdPercent":        int64(30),
						"memoryThresholdPercent":     int64(30),
						"stabilizationWindowSeconds": int64(600),
						"step":                       int64(1),
					},
				},
				"talos": map[string]interface{}{
					"configSecretRef": "talos-worker-config",
				},
			},
		},
	}

	_, err := dynClient.Resource(nodePoolGVR).Create(ctx, nodePoolObj, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("creating NodePool %q: %w", pool.Name, err)
	}

	// 2. Label each node and create a NodeClaim CR.
	for _, node := range pool.Nodes {
		// Label the K8s node with the pool reference.
		patchJSON := fmt.Sprintf(`{"metadata":{"labels":{"konductor.io/nodepool":%q}}}`, pool.Name)
		_, err := clientset.CoreV1().Nodes().Patch(
			ctx,
			node.Name,
			types.MergePatchType,
			[]byte(patchJSON),
			metav1.PatchOptions{},
		)
		if err != nil {
			return fmt.Errorf("labeling node %q: %w", node.Name, err)
		}

		// Create a NodeClaim CR representing this existing node.
		now := time.Now().UTC().Format(time.RFC3339)
		claimName := fmt.Sprintf("%s-%s", pool.Name, node.Name)
		// NodeClaim names must be DNS-safe; truncate if too long.
		if len(claimName) > 63 {
			claimName = claimName[:63]
		}

		claimObj := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "konductor.io/v1alpha1",
				"kind":       "NodeClaim",
				"metadata": map[string]interface{}{
					"name": claimName,
					"labels": map[string]interface{}{
						"konductor.io/nodepool": pool.Name,
					},
				},
				"spec": map[string]interface{}{
					"nodePoolRef": pool.Name,
					"provider":    "hetzner",
					"providerConfig": map[string]interface{}{
						"serverType": pool.ServerType,
						"location":   pool.Location,
					},
					"talos": map[string]interface{}{
						"configSecretRef": "talos-worker-config",
					},
				},
			},
		}

		_, err = dynClient.Resource(nodeClaimGVR).Create(ctx, claimObj, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("creating NodeClaim for node %q: %w", node.Name, err)
		}

		// Status must be set separately via the /status sub-resource.
		// Kubernetes ignores status fields during a normal Create.
		// Retry with fresh ResourceVersion in case the operator modified the object.
		for retry := 0; retry < 3; retry++ {
			fresh, getErr := dynClient.Resource(nodeClaimGVR).Get(ctx, claimName, metav1.GetOptions{})
			if getErr != nil {
				return fmt.Errorf("fetching NodeClaim for status update %q: %w", node.Name, getErr)
			}
			fresh.Object["status"] = map[string]interface{}{
				"phase":      "Ready",
				"providerID": node.ProviderID,
				"nodeName":   node.Name,
				"readyAt":    now,
				"createdAt":  now,
			}
			if node.InternalIP != "" {
				fresh.Object["status"].(map[string]interface{})["ips"] = []interface{}{node.InternalIP}
			}
			_, err = dynClient.Resource(nodeClaimGVR).UpdateStatus(ctx, fresh, metav1.UpdateOptions{})
			if err == nil {
				break
			}
			// Conflict — retry with fresh version.
			time.Sleep(200 * time.Millisecond)
		}
		if err != nil {
			return fmt.Errorf("setting NodeClaim status for node %q: %w", node.Name, err)
		}
	}

	return nil
}

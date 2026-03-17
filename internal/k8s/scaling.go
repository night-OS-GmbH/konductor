package k8s

import (
	"context"
	"encoding/json"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
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

// ScalingInfo holds the scaling state for TUI display.
type ScalingInfo struct {
	Installed bool
	Pool      *NodePoolInfo
	Claims    []NodeClaimInfo
}

type NodePoolInfo struct {
	Name            string
	Provider        string
	ServerType      string
	Location        string
	MinNodes        int32
	MaxNodes        int32
	CurrentNodes    int32
	DesiredNodes    int32
	ReadyNodes      int32
	Phase           string
	LastScaleTime   *time.Time
	CooldownSeconds int32
	ScaleUp         ScaleThresholds
	ScaleDown       ScaleThresholds
}

type ScaleThresholds struct {
	CPUPercent    int32
	MemoryPercent int32
	StabilizationSeconds int32
}

type NodeClaimInfo struct {
	Name       string
	Pool       string
	Phase      string
	ProviderID string
	NodeName   string
	IPs        []string
	CreatedAt  *time.Time
	ReadyAt    *time.Time
	Failure    string
}

// GetScalingInfo fetches NodePool and NodeClaim CRDs via dynamic client.
// Returns ScalingInfo with Installed=false if CRDs don't exist.
func (c *Client) GetScalingInfo(ctx context.Context) (*ScalingInfo, error) {
	dynClient, err := c.dynamicClient()
	if err != nil {
		return &ScalingInfo{Installed: false}, nil
	}

	// Try to list NodePools — if CRD doesn't exist, return not installed.
	poolList, err := dynClient.Resource(nodePoolGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return &ScalingInfo{Installed: false}, nil
	}

	info := &ScalingInfo{Installed: true}

	// Parse the first NodePool (typically there's only one).
	if len(poolList.Items) > 0 {
		raw := poolList.Items[0]
		pool := &NodePoolInfo{}
		pool.Name = raw.GetName()

		// Extract spec fields.
		if spec, ok := raw.Object["spec"].(map[string]interface{}); ok {
			pool.Provider, _ = spec["provider"].(string)
			if pc, ok := spec["providerConfig"].(map[string]interface{}); ok {
				pool.ServerType, _ = pc["serverType"].(string)
				pool.Location, _ = pc["location"].(string)
			}
			pool.MinNodes = jsonInt32(spec, "minNodes")
			pool.MaxNodes = jsonInt32(spec, "maxNodes")

			if scaling, ok := spec["scaling"].(map[string]interface{}); ok {
				pool.CooldownSeconds = jsonInt32(scaling, "cooldownSeconds")
				if su, ok := scaling["scaleUp"].(map[string]interface{}); ok {
					pool.ScaleUp.CPUPercent = jsonInt32(su, "cpuThresholdPercent")
					pool.ScaleUp.MemoryPercent = jsonInt32(su, "memoryThresholdPercent")
					pool.ScaleUp.StabilizationSeconds = jsonInt32(su, "stabilizationWindowSeconds")
				}
				if sd, ok := scaling["scaleDown"].(map[string]interface{}); ok {
					pool.ScaleDown.CPUPercent = jsonInt32(sd, "cpuThresholdPercent")
					pool.ScaleDown.MemoryPercent = jsonInt32(sd, "memoryThresholdPercent")
					pool.ScaleDown.StabilizationSeconds = jsonInt32(sd, "stabilizationWindowSeconds")
				}
			}
		}

		// Extract status fields.
		if status, ok := raw.Object["status"].(map[string]interface{}); ok {
			pool.CurrentNodes = jsonInt32(status, "currentNodes")
			pool.DesiredNodes = jsonInt32(status, "desiredNodes")
			pool.ReadyNodes = jsonInt32(status, "readyNodes")
			pool.Phase, _ = status["phase"].(string)
			if lst, ok := status["lastScaleTime"].(string); ok {
				if t, err := time.Parse(time.RFC3339, lst); err == nil {
					pool.LastScaleTime = &t
				}
			}
		}

		info.Pool = pool
	}

	// List NodeClaims.
	claimList, err := dynClient.Resource(nodeClaimGVR).List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, raw := range claimList.Items {
			claim := NodeClaimInfo{
				Name: raw.GetName(),
			}
			if spec, ok := raw.Object["spec"].(map[string]interface{}); ok {
				claim.Pool, _ = spec["nodePoolRef"].(string)
			}
			if status, ok := raw.Object["status"].(map[string]interface{}); ok {
				claim.Phase, _ = status["phase"].(string)
				claim.ProviderID, _ = status["providerID"].(string)
				claim.NodeName, _ = status["nodeName"].(string)
				if ips, ok := status["ips"].([]interface{}); ok {
					for _, ip := range ips {
						if s, ok := ip.(string); ok {
							claim.IPs = append(claim.IPs, s)
						}
					}
				}
				if cat, ok := status["createdAt"].(string); ok {
					if t, err := time.Parse(time.RFC3339, cat); err == nil {
						claim.CreatedAt = &t
					}
				}
				if rat, ok := status["readyAt"].(string); ok {
					if t, err := time.Parse(time.RFC3339, rat); err == nil {
						claim.ReadyAt = &t
					}
				}
				claim.Failure, _ = status["failureMessage"].(string)
			}
			info.Claims = append(info.Claims, claim)
		}
	}

	return info, nil
}

// dynamicClient creates a dynamic client reusing the same kubeconfig.
func (c *Client) dynamicClient() (dynamic.Interface, error) {
	loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: c.kubeconfigPath}
	overrides := &clientcmd.ConfigOverrides{CurrentContext: c.context}
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides)
	restCfg, err := cc.ClientConfig()
	if err != nil {
		return nil, err
	}
	return dynamic.NewForConfig(restCfg)
}

func jsonInt32(m map[string]interface{}, key string) int32 {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int32(n)
	case int64:
		return int32(n)
	case json.Number:
		i, _ := n.Int64()
		return int32(i)
	}
	return 0
}

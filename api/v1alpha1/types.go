package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// --------------------------------------------------------------------------
// NodePool — defines a pool of auto-scaled worker nodes
// --------------------------------------------------------------------------

// NodePool is the Schema for the nodepools API.
type NodePool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NodePoolSpec   `json:"spec"`
	Status NodePoolStatus `json:"status,omitempty"`
}

type NodePoolSpec struct {
	// Role defines the node role for this pool. Valid values are "worker"
	// (default) and "control-plane". Control-plane pools have additional
	// safety constraints (etcd quorum, maxUnavailable=1).
	Role string `json:"role,omitempty"`

	// Provider identifies the infrastructure provider (e.g. "hetzner").
	Provider string `json:"provider"`

	// ProviderConfig holds provider-specific server configuration.
	ProviderConfig ProviderConfig `json:"providerConfig"`

	// MinNodes is the minimum number of nodes the pool will maintain.
	MinNodes int32 `json:"minNodes"`

	// MaxNodes is the maximum number of nodes the pool is allowed to scale to.
	MaxNodes int32 `json:"maxNodes"`

	// Scaling defines scale-up/scale-down behavior and cooldown.
	Scaling ScalingBehavior `json:"scaling"`

	// Talos references the Talos machine configuration used to bootstrap new nodes.
	Talos TalosReference `json:"talos"`

	// NodeTemplate allows setting labels, annotations and taints on new nodes.
	NodeTemplate NodeTemplate `json:"nodeTemplate,omitempty"`
}

// ProviderConfig contains Hetzner-specific server provisioning settings.
type ProviderConfig struct {
	// ServerType is the Hetzner server type (e.g. "cpx31", "cax41").
	ServerType string `json:"serverType"`

	// Location is the Hetzner datacenter location (e.g. "fsn1", "nbg1").
	Location string `json:"location"`

	// Image is the OS image to use. Defaults to the latest Talos image if empty.
	Image string `json:"image,omitempty"`

	// Network is the Hetzner network name or ID to attach the server to.
	Network string `json:"network,omitempty"`

	// SSHKeyName is the name of the SSH key registered in Hetzner.
	SSHKeyName string `json:"sshKeyName,omitempty"`

	// PlacementGroup is the Hetzner placement group for anti-affinity.
	PlacementGroup string `json:"placementGroup,omitempty"`

	// Labels are additional labels applied to the Hetzner server resource.
	Labels map[string]string `json:"labels,omitempty"`
}

// ScalingBehavior defines thresholds, stabilization windows, and cooldown.
type ScalingBehavior struct {
	// Enabled activates autoscaling for this pool. When false (default),
	// the pool is static — the operator collects metrics and evaluates
	// decisions but will not create or delete nodes. Safe default for
	// imported pools and control-plane pools.
	Enabled bool `json:"enabled"`

	ScaleUp         ScaleUpConfig   `json:"scaleUp,omitempty"`
	ScaleDown       ScaleDownConfig `json:"scaleDown,omitempty"`
	CooldownSeconds int32           `json:"cooldownSeconds,omitempty"`
}

// ScaleUpConfig controls when and how aggressively the pool scales up.
type ScaleUpConfig struct {
	// CPUThresholdPercent triggers scale-up when average cluster CPU usage exceeds this value.
	CPUThresholdPercent int32 `json:"cpuThresholdPercent"`

	// MemoryThresholdPercent triggers scale-up when average cluster memory usage exceeds this value.
	MemoryThresholdPercent int32 `json:"memoryThresholdPercent"`

	// PendingPodsThreshold triggers scale-up when the number of pending pods exceeds this value.
	PendingPodsThreshold int32 `json:"pendingPodsThreshold"`

	// StabilizationWindowSeconds is how long the condition must persist before acting.
	StabilizationWindowSeconds int32 `json:"stabilizationWindowSeconds"`

	// Step is the number of nodes to add per scale-up action.
	Step int32 `json:"step"`
}

// ScaleDownConfig controls when and how the pool scales down.
type ScaleDownConfig struct {
	// CPUThresholdPercent triggers scale-down consideration when average CPU is below this value.
	CPUThresholdPercent int32 `json:"cpuThresholdPercent"`

	// MemoryThresholdPercent triggers scale-down consideration when average memory is below this value.
	MemoryThresholdPercent int32 `json:"memoryThresholdPercent"`

	// StabilizationWindowSeconds is how long a node must be underutilized before removal.
	StabilizationWindowSeconds int32 `json:"stabilizationWindowSeconds"`

	// Step is the maximum number of nodes to remove per scale-down action.
	Step int32 `json:"step"`
}

// TalosReference points to the Talos machine configuration used to bootstrap new nodes.
type TalosReference struct {
	// ConfigSecretRef is the name of the Secret containing the Talos machine config.
	ConfigSecretRef string `json:"configSecretRef"`

	// Version is the desired Talos version (e.g. "v1.9.3").
	Version string `json:"version,omitempty"`
}

// NodeTemplate defines metadata applied to Kubernetes Node objects created by this pool.
type NodeTemplate struct {
	// Labels to apply to created nodes.
	Labels map[string]string `json:"labels,omitempty"`

	// Annotations to apply to created nodes.
	Annotations map[string]string `json:"annotations,omitempty"`

	// Taints to apply to created nodes.
	Taints []Taint `json:"taints,omitempty"`
}

// Taint mirrors corev1.Taint but avoids pulling in the full core/v1 package.
type Taint struct {
	Key    string `json:"key"`
	Value  string `json:"value,omitempty"`
	Effect string `json:"effect"`
}

// NodePoolStatus describes the observed state of the NodePool.
type NodePoolStatus struct {
	// CurrentNodes is the actual number of nodes currently in the pool.
	CurrentNodes int32 `json:"currentNodes"`

	// DesiredNodes is the target number of nodes.
	DesiredNodes int32 `json:"desiredNodes"`

	// ReadyNodes is the number of nodes in Ready condition.
	ReadyNodes int32 `json:"readyNodes"`

	// Phase is the current lifecycle phase of the pool.
	Phase NodePoolPhase `json:"phase,omitempty"`

	// LastScaleTime records when the last scaling action occurred.
	LastScaleTime *metav1.Time `json:"lastScaleTime,omitempty"`

	// Conditions provide detailed status information.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// NodePoolPhase represents the lifecycle phase of a NodePool.
type NodePoolPhase string

const (
	NodePoolPhasePending  NodePoolPhase = "Pending"
	NodePoolPhaseActive   NodePoolPhase = "Active"
	NodePoolPhaseScaling  NodePoolPhase = "Scaling"
	NodePoolPhaseDegraded NodePoolPhase = "Degraded"
	NodePoolPhaseDeleting NodePoolPhase = "Deleting"
)

// NodePoolList contains a list of NodePool resources.
type NodePoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []NodePool `json:"items"`
}

// --------------------------------------------------------------------------
// NodeClaim — represents a single node provisioning request
// --------------------------------------------------------------------------

// NodeClaim is the Schema for the nodeclaims API. Each NodeClaim
// represents a request (and eventual fulfillment) of a single compute node.
type NodeClaim struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NodeClaimSpec   `json:"spec"`
	Status NodeClaimStatus `json:"status,omitempty"`
}

type NodeClaimSpec struct {
	// NodePoolRef is the name of the NodePool that owns this claim.
	NodePoolRef string `json:"nodePoolRef"`

	// Provider identifies the infrastructure provider.
	Provider string `json:"provider"`

	// ProviderConfig holds the provider-specific configuration for this node.
	ProviderConfig ProviderConfig `json:"providerConfig"`

	// Talos references the Talos machine configuration.
	Talos TalosReference `json:"talos"`

	// NodeTemplate defines metadata for the resulting Kubernetes node.
	NodeTemplate NodeTemplate `json:"nodeTemplate,omitempty"`
}

// NodeClaimStatus describes the observed state of a NodeClaim.
type NodeClaimStatus struct {
	// Phase is the current lifecycle phase of the claim.
	Phase NodeClaimPhase `json:"phase,omitempty"`

	// ProviderID is the cloud-provider identifier of the provisioned server.
	ProviderID string `json:"providerID,omitempty"`

	// NodeName is the Kubernetes node name once the node has joined the cluster.
	NodeName string `json:"nodeName,omitempty"`

	// IPs holds the assigned IP addresses.
	IPs []string `json:"ips,omitempty"`

	// CreatedAt records when the provider resource was created.
	CreatedAt *metav1.Time `json:"createdAt,omitempty"`

	// ReadyAt records when the node became Ready in the cluster.
	ReadyAt *metav1.Time `json:"readyAt,omitempty"`

	// FailureReason contains a machine-readable reason for failure.
	FailureReason string `json:"failureReason,omitempty"`

	// FailureMessage contains a human-readable description of the failure.
	FailureMessage string `json:"failureMessage,omitempty"`

	// Conditions provide detailed status information.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// NodeClaimPhase represents the lifecycle phase of a NodeClaim.
type NodeClaimPhase string

const (
	NodeClaimPhasePending      NodeClaimPhase = "Pending"
	NodeClaimPhaseProvisioning NodeClaimPhase = "Provisioning"
	NodeClaimPhaseBootstrapping NodeClaimPhase = "Bootstrapping"
	NodeClaimPhaseReady        NodeClaimPhase = "Ready"
	NodeClaimPhaseDraining     NodeClaimPhase = "Draining"
	NodeClaimPhaseDeleting     NodeClaimPhase = "Deleting"
	NodeClaimPhaseFailed       NodeClaimPhase = "Failed"
)

// NodeClaimList contains a list of NodeClaim resources.
type NodeClaimList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []NodeClaim `json:"items"`
}

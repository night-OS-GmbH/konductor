package components

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"github.com/night-OS-GmbH/konductor/internal/cluster"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

//go:embed manifests/hetzner-ccm.yaml
var hetznerCCMManifest string

// HetznerCCM manages the Hetzner Cloud Controller Manager component.
// The CCM integrates the cluster with Hetzner Cloud for:
//   - Load balancer provisioning
//   - Node metadata and lifecycle management
//   - Network route configuration
type HetznerCCM struct {
	clientset     kubernetes.Interface
	dynamicClient dynamic.Interface
}

// NewHetznerCCM creates a HetznerCCM component manager.
func NewHetznerCCM(clientset kubernetes.Interface, dynamicClient dynamic.Interface) *HetznerCCM {
	return &HetznerCCM{
		clientset:     clientset,
		dynamicClient: dynamicClient,
	}
}

func (h *HetznerCCM) Name() string {
	return "hetzner-ccm"
}

func (h *HetznerCCM) Description() string {
	return "Hetzner Cloud Controller Manager for load balancers, node lifecycle, and networking"
}

// Check inspects kube-system for the hcloud-cloud-controller-manager deployment.
func (h *HetznerCCM) Check(ctx context.Context) (*cluster.ComponentStatus, error) {
	recommended := cluster.RecommendedVersions["hetzner-ccm"]

	status := &cluster.ComponentStatus{
		Name:          h.Name(),
		Description:   h.Description(),
		LatestVersion: recommended,
		Installable:   true,
	}

	deploy, err := h.clientset.AppsV1().Deployments("kube-system").Get(ctx, "hcloud-cloud-controller-manager", metav1.GetOptions{})
	if err != nil {
		// Not found — not installed.
		return status, nil
	}

	status.Installed = true
	status.Healthy = deploy.Status.AvailableReplicas > 0

	// Extract version from container image tag.
	if len(deploy.Spec.Template.Spec.Containers) > 0 {
		image := deploy.Spec.Template.Spec.Containers[0].Image
		if idx := strings.LastIndex(image, ":"); idx != -1 {
			status.Version = image[idx+1:]
		}
	}

	status.NeedsUpdate = status.Version != "" && cluster.VersionOlderThan(status.Version, recommended)

	return status, nil
}

// Install deploys the Hetzner CCM into the cluster.
// Requires opts["hcloud_token"] to be set with a valid Hetzner Cloud API token.
// The token is stored in a Secret in kube-system before applying the CCM manifest.
func (h *HetznerCCM) Install(ctx context.Context, opts map[string]string) error {
	token := opts["hcloud_token"]
	if token == "" {
		return fmt.Errorf("hcloud_token is required to install Hetzner CCM")
	}

	// Ensure the hcloud secret exists in kube-system with the API token.
	if err := h.ensureHCloudSecret(ctx, token); err != nil {
		return fmt.Errorf("creating hcloud secret: %w", err)
	}

	// Apply the CCM manifest.
	return h.applyManifest(ctx, hetznerCCMManifest)
}

// Update reapplies the embedded CCM manifest. Server-side apply is idempotent.
func (h *HetznerCCM) Update(ctx context.Context) error {
	return h.applyManifest(ctx, hetznerCCMManifest)
}

// ensureHCloudSecret creates or updates the hcloud Secret in kube-system
// with the given API token. The CCM reads its credentials from this secret.
func (h *HetznerCCM) ensureHCloudSecret(ctx context.Context, token string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcloud",
			Namespace: "kube-system",
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"token":   token,
			"network": "", // Network name, set by CCM args or left empty for auto-detection.
		},
	}

	existing, err := h.clientset.CoreV1().Secrets("kube-system").Get(ctx, "hcloud", metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			_, err = h.clientset.CoreV1().Secrets("kube-system").Create(ctx, secret, metav1.CreateOptions{})
			return err
		}
		return err
	}

	// Update existing secret with new token.
	existing.StringData = secret.StringData
	_, err = h.clientset.CoreV1().Secrets("kube-system").Update(ctx, existing, metav1.UpdateOptions{})
	return err
}

func (h *HetznerCCM) applyManifest(ctx context.Context, yamlContent string) error {
	objects, err := decodeYAML(yamlContent)
	if err != nil {
		return fmt.Errorf("decoding hetzner-ccm manifest: %w", err)
	}

	for _, obj := range objects {
		gvr, err := gvrFromObject(obj)
		if err != nil {
			return fmt.Errorf("resolving GVR for %s/%s: %w", obj.GetKind(), obj.GetName(), err)
		}

		var resource dynamic.ResourceInterface
		if obj.GetNamespace() != "" {
			resource = h.dynamicClient.Resource(gvr).Namespace(obj.GetNamespace())
		} else {
			resource = h.dynamicClient.Resource(gvr)
		}

		data, err := obj.MarshalJSON()
		if err != nil {
			return fmt.Errorf("marshaling %s/%s: %w", obj.GetKind(), obj.GetName(), err)
		}

		_, err = resource.Patch(ctx, obj.GetName(), types.ApplyPatchType, data, metav1.PatchOptions{
			FieldManager: metricsServerFieldManager,
		})
		if err != nil {
			return fmt.Errorf("applying %s %q: %w", obj.GetKind(), obj.GetName(), err)
		}
	}

	return nil
}

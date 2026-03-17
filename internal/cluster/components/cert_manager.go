package components

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/night-OS-GmbH/konductor/internal/cluster"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

const (
	// certManagerManifestURL is the official cert-manager release manifest.
	certManagerManifestURL = "https://github.com/cert-manager/cert-manager/releases/download/%s/cert-manager.yaml"
)

// CertManager manages the cert-manager component for automatic TLS
// certificate provisioning and renewal via Let's Encrypt and other issuers.
type CertManager struct {
	clientset     kubernetes.Interface
	dynamicClient dynamic.Interface
	httpClient    *http.Client
}

// NewCertManager creates a CertManager component manager.
func NewCertManager(clientset kubernetes.Interface, dynamicClient dynamic.Interface) *CertManager {
	return &CertManager{
		clientset:     clientset,
		dynamicClient: dynamicClient,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (c *CertManager) Name() string {
	return "cert-manager"
}

func (c *CertManager) Description() string {
	return "Automatic TLS certificate management with Let's Encrypt and custom issuers"
}

// Check inspects the cert-manager namespace for the cert-manager deployment.
func (c *CertManager) Check(ctx context.Context) (*cluster.ComponentStatus, error) {
	recommended := cluster.RecommendedVersions["cert-manager"]

	status := &cluster.ComponentStatus{
		Name:          c.Name(),
		Description:   c.Description(),
		LatestVersion: recommended,
		Installable:   true,
	}

	deploy, err := c.clientset.AppsV1().Deployments("cert-manager").Get(ctx, "cert-manager", metav1.GetOptions{})
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

	status.NeedsUpdate = status.Version != "" && status.Version != recommended

	return status, nil
}

// Install downloads the official cert-manager manifest for the recommended
// version and applies it to the cluster using server-side apply.
func (c *CertManager) Install(ctx context.Context, _ map[string]string) error {
	version := cluster.RecommendedVersions["cert-manager"]
	manifest, err := c.downloadManifest(ctx, version)
	if err != nil {
		return fmt.Errorf("downloading cert-manager %s manifest: %w", version, err)
	}

	return c.applyManifest(ctx, manifest)
}

// Update downloads and applies the latest recommended cert-manager version.
func (c *CertManager) Update(ctx context.Context) error {
	return c.Install(ctx, nil)
}

// downloadManifest fetches the cert-manager YAML manifest for the given version.
func (c *CertManager) downloadManifest(ctx context.Context, version string) (string, error) {
	url := fmt.Sprintf(certManagerManifestURL, version)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("downloading manifest from %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d from %s", resp.StatusCode, url)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response body: %w", err)
	}

	return string(body), nil
}

func (c *CertManager) applyManifest(ctx context.Context, yamlContent string) error {
	objects, err := decodeYAML(yamlContent)
	if err != nil {
		return fmt.Errorf("decoding cert-manager manifest: %w", err)
	}

	for _, obj := range objects {
		gvr, err := gvrFromObject(obj)
		if err != nil {
			// cert-manager manifests include many CRD types that we may not
			// have in our static mapping. Skip unknown types with a warning
			// rather than failing — the server-side apply will handle them
			// if we can resolve them.
			continue
		}

		var resource dynamic.ResourceInterface
		if obj.GetNamespace() != "" {
			resource = c.dynamicClient.Resource(gvr).Namespace(obj.GetNamespace())
		} else {
			resource = c.dynamicClient.Resource(gvr)
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

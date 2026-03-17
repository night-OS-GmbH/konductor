package components

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"io"
	"strings"

	"github.com/night-OS-GmbH/konductor/internal/cluster"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

//go:embed manifests/metrics-server.yaml
var metricsServerManifest string

const (
	metricsServerFieldManager = "konductor-cluster"
)

// MetricsServer manages the metrics-server component in a Talos cluster.
// The embedded manifest includes Talos-specific args:
//   - --kubelet-insecure-tls (Talos lacks rotate-server-certificates by default)
//   - --kubelet-preferred-address-types=InternalIP (Talos hostnames not DNS-resolvable)
type MetricsServer struct {
	clientset     kubernetes.Interface
	dynamicClient dynamic.Interface
}

// NewMetricsServer creates a MetricsServer component manager.
func NewMetricsServer(clientset kubernetes.Interface, dynamicClient dynamic.Interface) *MetricsServer {
	return &MetricsServer{
		clientset:     clientset,
		dynamicClient: dynamicClient,
	}
}

func (m *MetricsServer) Name() string {
	return "metrics-server"
}

func (m *MetricsServer) Description() string {
	return "Cluster resource metrics (CPU/memory) for HPA and kubectl top"
}

// Check inspects kube-system for the metrics-server deployment and reports its status.
func (m *MetricsServer) Check(ctx context.Context) (*cluster.ComponentStatus, error) {
	recommended := cluster.RecommendedVersions["metrics-server"]

	status := &cluster.ComponentStatus{
		Name:          m.Name(),
		Description:   m.Description(),
		LatestVersion: recommended,
		Installable:   true,
	}

	deploy, err := m.clientset.AppsV1().Deployments("kube-system").Get(ctx, "metrics-server", metav1.GetOptions{})
	if err != nil {
		// Not found — not installed.
		return status, nil
	}

	status.Installed = true
	status.Healthy = deploy.Status.AvailableReplicas > 0

	// Extract version from the container image tag.
	if len(deploy.Spec.Template.Spec.Containers) > 0 {
		image := deploy.Spec.Template.Spec.Containers[0].Image
		if idx := strings.LastIndex(image, ":"); idx != -1 {
			status.Version = image[idx+1:]
		}
	}

	status.NeedsUpdate = status.Version != "" && cluster.VersionOlderThan(status.Version, recommended)

	return status, nil
}

// Install applies the embedded metrics-server manifest using server-side apply.
func (m *MetricsServer) Install(ctx context.Context, _ map[string]string) error {
	return m.applyManifest(ctx, metricsServerManifest)
}

// Update reapplies the embedded manifest. Server-side apply is idempotent,
// so this safely updates to the version embedded in the binary.
func (m *MetricsServer) Update(ctx context.Context) error {
	return m.applyManifest(ctx, metricsServerManifest)
}

func (m *MetricsServer) applyManifest(ctx context.Context, yamlContent string) error {
	objects, err := decodeYAML(yamlContent)
	if err != nil {
		return fmt.Errorf("decoding metrics-server manifest: %w", err)
	}

	for _, obj := range objects {
		gvr, err := gvrFromObject(obj)
		if err != nil {
			return fmt.Errorf("resolving GVR for %s/%s: %w", obj.GetKind(), obj.GetName(), err)
		}

		var resource dynamic.ResourceInterface
		if obj.GetNamespace() != "" {
			resource = m.dynamicClient.Resource(gvr).Namespace(obj.GetNamespace())
		} else {
			resource = m.dynamicClient.Resource(gvr)
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

// decodeYAML parses a multi-document YAML string into unstructured objects.
func decodeYAML(yamlContent string) ([]*unstructured.Unstructured, error) {
	var objects []*unstructured.Unstructured

	decoder := yamlutil.NewYAMLOrJSONDecoder(bytes.NewReader([]byte(yamlContent)), 4096)
	for {
		obj := &unstructured.Unstructured{}
		err := decoder.Decode(obj)
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("decoding YAML: %w", err)
		}
		// Skip empty documents.
		if obj.GetKind() == "" {
			continue
		}
		objects = append(objects, obj)
	}

	return objects, nil
}

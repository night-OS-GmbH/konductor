package components

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestGVRFromObject_KnownTypes(t *testing.T) {
	tests := []struct {
		apiVersion string
		kind       string
		wantRes    string
	}{
		{"v1", "Namespace", "namespaces"},
		{"v1", "Secret", "secrets"},
		{"v1", "ServiceAccount", "serviceaccounts"},
		{"v1", "Service", "services"},
		{"v1", "ConfigMap", "configmaps"},
		{"apps/v1", "Deployment", "deployments"},
		{"apps/v1", "DaemonSet", "daemonsets"},
		{"rbac.authorization.k8s.io/v1", "ClusterRole", "clusterroles"},
		{"rbac.authorization.k8s.io/v1", "ClusterRoleBinding", "clusterrolebindings"},
		{"rbac.authorization.k8s.io/v1", "Role", "roles"},
		{"rbac.authorization.k8s.io/v1", "RoleBinding", "rolebindings"},
		{"apiextensions.k8s.io/v1", "CustomResourceDefinition", "customresourcedefinitions"},
		{"admissionregistration.k8s.io/v1", "MutatingWebhookConfiguration", "mutatingwebhookconfigurations"},
		{"admissionregistration.k8s.io/v1", "ValidatingWebhookConfiguration", "validatingwebhookconfigurations"},
		{"apiregistration.k8s.io/v1", "APIService", "apiservices"},
	}

	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			obj := &unstructured.Unstructured{}
			obj.SetAPIVersion(tt.apiVersion)
			obj.SetKind(tt.kind)
			obj.SetName("test")

			gvr, err := gvrFromObject(obj)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gvr.Resource != tt.wantRes {
				t.Errorf("expected resource %q, got %q", tt.wantRes, gvr.Resource)
			}
		})
	}
}

func TestGVRFromObject_UnknownType(t *testing.T) {
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion("unknown.io/v1")
	obj.SetKind("UnknownKind")
	obj.SetName("test")

	_, err := gvrFromObject(obj)
	if err == nil {
		t.Fatal("expected error for unknown type, got nil")
	}
	if !strings.Contains(err.Error(), "unknown resource type") {
		t.Errorf("expected 'unknown resource type' error, got: %v", err)
	}
}

func TestDecodeYAML_MultiDocument(t *testing.T) {
	yaml := `---
apiVersion: v1
kind: Namespace
metadata:
  name: test-ns
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: test-sa
  namespace: test-ns
`
	objects, err := decodeYAML(yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(objects) != 2 {
		t.Fatalf("expected 2 objects, got %d", len(objects))
	}
	if objects[0].GetKind() != "Namespace" {
		t.Errorf("expected Namespace, got %s", objects[0].GetKind())
	}
	if objects[1].GetKind() != "ServiceAccount" {
		t.Errorf("expected ServiceAccount, got %s", objects[1].GetKind())
	}
}

func TestDecodeYAML_EmptyDocuments(t *testing.T) {
	yaml := `---
---
apiVersion: v1
kind: Namespace
metadata:
  name: test-ns
---
`
	objects, err := decodeYAML(yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(objects) != 1 {
		t.Fatalf("expected 1 object (skipping empties), got %d", len(objects))
	}
}

func TestDecodeYAML_MetricsServerManifest(t *testing.T) {
	// Verify the embedded metrics-server manifest can be parsed.
	objects, err := decodeYAML(metricsServerManifest)
	if err != nil {
		t.Fatalf("failed to decode metrics-server manifest: %v", err)
	}
	if len(objects) == 0 {
		t.Fatal("expected objects in metrics-server manifest, got 0")
	}

	// Verify we have the expected resource types.
	kinds := make(map[string]bool)
	for _, obj := range objects {
		kinds[obj.GetKind()] = true
	}

	expectedKinds := []string{"ServiceAccount", "ClusterRole", "ClusterRoleBinding", "Service", "Deployment", "APIService"}
	for _, k := range expectedKinds {
		if !kinds[k] {
			t.Errorf("expected kind %s in metrics-server manifest", k)
		}
	}
}

func TestDecodeYAML_HetznerCCMManifest(t *testing.T) {
	// Verify the embedded hetzner-ccm manifest can be parsed.
	objects, err := decodeYAML(hetznerCCMManifest)
	if err != nil {
		t.Fatalf("failed to decode hetzner-ccm manifest: %v", err)
	}
	if len(objects) == 0 {
		t.Fatal("expected objects in hetzner-ccm manifest, got 0")
	}

	// Verify we have the expected resource types.
	kinds := make(map[string]bool)
	for _, obj := range objects {
		kinds[obj.GetKind()] = true
	}

	expectedKinds := []string{"ServiceAccount", "ClusterRoleBinding", "Deployment"}
	for _, k := range expectedKinds {
		if !kinds[k] {
			t.Errorf("expected kind %s in hetzner-ccm manifest", k)
		}
	}
}

func TestMetricsServerManifest_TalosArgs(t *testing.T) {
	// Verify Talos-specific args are present in the metrics-server manifest.
	if !strings.Contains(metricsServerManifest, "--kubelet-insecure-tls") {
		t.Error("metrics-server manifest missing --kubelet-insecure-tls (required for Talos)")
	}
	if !strings.Contains(metricsServerManifest, "--kubelet-preferred-address-types=InternalIP") {
		t.Error("metrics-server manifest missing --kubelet-preferred-address-types=InternalIP (required for Talos)")
	}
}

func TestHetznerCCMManifest_CloudProvider(t *testing.T) {
	// Verify CCM has the --cloud-provider=hcloud flag.
	if !strings.Contains(hetznerCCMManifest, "--cloud-provider=hcloud") {
		t.Error("hetzner-ccm manifest missing --cloud-provider=hcloud")
	}
}

func TestHetznerCCMManifest_SecretRef(t *testing.T) {
	// Verify the CCM references the hcloud secret for the API token.
	if !strings.Contains(hetznerCCMManifest, "name: hcloud") {
		t.Error("hetzner-ccm manifest missing hcloud secret reference")
	}
}

package installer

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestRenderManifest_Namespace(t *testing.T) {
	data := map[string]string{
		"Namespace": "konductor-system",
	}
	rendered, err := renderManifest("manifests/namespace.yaml", data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(rendered, "name: konductor-system") {
		t.Errorf("expected namespace name in rendered output, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "kind: Namespace") {
		t.Errorf("expected Namespace kind in rendered output")
	}
}

func TestRenderManifest_Deployment(t *testing.T) {
	data := map[string]string{
		"Namespace":  "test-ns",
		"Image":      "ghcr.io/test/operator:v1.0.0",
		"SecretName": "test-secrets",
	}
	rendered, err := renderManifest("manifests/deployment.yaml", data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(rendered, "namespace: test-ns") {
		t.Errorf("expected namespace in deployment, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "image: ghcr.io/test/operator:v1.0.0") {
		t.Errorf("expected image in deployment, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "name: test-secrets") {
		t.Errorf("expected secret name in deployment, got:\n%s", rendered)
	}
}

func TestRenderManifest_Secrets(t *testing.T) {
	data := map[string]string{
		"Namespace":         "konductor-system",
		"SecretName":        "konductor-secrets",
		"HCloudTokenBase64": "dGVzdC10b2tlbg==",
		"TalosConfigBase64": "dGVzdC1jb25maWc=",
	}
	rendered, err := renderManifest("manifests/secrets.yaml", data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(rendered, "hcloud-token: dGVzdC10b2tlbg==") {
		t.Errorf("expected base64 hcloud token, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "talos-worker-config: dGVzdC1jb25maWc=") {
		t.Errorf("expected base64 talos config, got:\n%s", rendered)
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

func TestDecodeYAML_EmptyDocument(t *testing.T) {
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

func TestGVRFromObject_KnownTypes(t *testing.T) {
	tests := []struct {
		apiVersion string
		kind       string
		wantRes    string
	}{
		{"v1", "Namespace", "namespaces"},
		{"v1", "Secret", "secrets"},
		{"v1", "ServiceAccount", "serviceaccounts"},
		{"apps/v1", "Deployment", "deployments"},
		{"rbac.authorization.k8s.io/v1", "ClusterRole", "clusterroles"},
		{"rbac.authorization.k8s.io/v1", "ClusterRoleBinding", "clusterrolebindings"},
		{"apiextensions.k8s.io/v1", "CustomResourceDefinition", "customresourcedefinitions"},
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

func TestRenderManifest_CRDs(t *testing.T) {
	data := map[string]string{}

	// Verify CRD manifests render without errors (they have no template vars).
	for _, file := range []string{"manifests/crd-nodepool.yaml", "manifests/crd-nodeclaim.yaml"} {
		rendered, err := renderManifest(file, data)
		if err != nil {
			t.Fatalf("rendering %s: %v", file, err)
		}
		if !strings.Contains(rendered, "kind: CustomResourceDefinition") {
			t.Errorf("%s: expected CRD kind, got:\n%s", file, rendered[:200])
		}
		if !strings.Contains(rendered, "konductor.io") {
			t.Errorf("%s: expected konductor.io group", file)
		}
	}
}

func TestRenderManifest_RBAC(t *testing.T) {
	data := map[string]string{}
	rendered, err := renderManifest("manifests/rbac.yaml", data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// RBAC file contains ServiceAccount, ClusterRole, ClusterRoleBinding.
	if !strings.Contains(rendered, "kind: ServiceAccount") {
		t.Error("expected ServiceAccount in RBAC manifest")
	}
	if !strings.Contains(rendered, "kind: ClusterRole") {
		t.Error("expected ClusterRole in RBAC manifest")
	}
	if !strings.Contains(rendered, "kind: ClusterRoleBinding") {
		t.Error("expected ClusterRoleBinding in RBAC manifest")
	}
}

func TestAllManifestsEmbedded(t *testing.T) {
	expectedFiles := []string{
		"manifests/namespace.yaml",
		"manifests/crd-nodepool.yaml",
		"manifests/crd-nodeclaim.yaml",
		"manifests/rbac.yaml",
		"manifests/deployment.yaml",
		"manifests/secrets.yaml",
	}
	for _, file := range expectedFiles {
		data, err := manifests.ReadFile(file)
		if err != nil {
			t.Errorf("embedded file %s not found: %v", file, err)
			continue
		}
		if len(data) == 0 {
			t.Errorf("embedded file %s is empty", file)
		}
	}
}

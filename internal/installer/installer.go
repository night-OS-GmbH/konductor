// Package installer handles installation, uninstallation, and status checking
// of the Konductor operator in a Kubernetes cluster. It embeds YAML manifests
// and applies them using the Kubernetes dynamic client with server-side apply.
package installer

import (
	"bytes"
	"context"
	"embed"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"text/template"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

//go:embed manifests/*.yaml
var manifests embed.FS

const (
	// DefaultNamespace is the default namespace for the Konductor operator.
	DefaultNamespace = "konductor-system"

	// DefaultImage is the default operator container image.
	DefaultImage = "ghcr.io/night-os-gmbh/konductor-operator:main"

	// DefaultSecretName is the default name for the operator secrets.
	DefaultSecretName = "konductor-secrets"

	// fieldManager is the manager name used for server-side apply.
	fieldManager = "konductor-installer"
)

// InstallOptions configures the operator installation.
type InstallOptions struct {
	// Kubeconfig is the path to the kubeconfig file. Empty uses default.
	Kubeconfig string

	// Context is the kubeconfig context to use. Empty uses current.
	Context string

	// Namespace is the target namespace for the operator.
	Namespace string

	// Image is the operator container image reference.
	Image string

	// HCloudToken is the Hetzner Cloud API token.
	HCloudToken string

	// TalosConfig is the raw YAML content of the Talos worker machine config.
	TalosConfig string

	// DryRun prints rendered manifests instead of applying them.
	DryRun bool
}

// OperatorStatus describes the current state of the installed operator.
type OperatorStatus struct {
	// Installed indicates whether the operator namespace and deployment exist.
	Installed bool

	// Ready indicates whether the operator deployment has available replicas.
	Ready bool

	// Version is the operator image tag, extracted from the deployment spec.
	Version string

	// Namespace is the namespace where the operator is installed.
	Namespace string

	// NodePools is the count of NodePool custom resources in the cluster.
	NodePools int

	// NodeClaims is the count of NodeClaim custom resources in the cluster.
	NodeClaims int
}

// Installer manages the lifecycle of the Konductor operator in a cluster.
type Installer struct {
	dynamic   dynamic.Interface
	clientset kubernetes.Interface
	context   string
}

// NewInstaller creates a new Installer connected to the cluster specified
// by the given kubeconfig path and context.
func NewInstaller(kubeconfig, kubeContext string) (*Installer, error) {
	loadingRules := &clientcmd.ClientConfigLoadingRules{
		ExplicitPath: kubeconfig,
	}
	overrides := &clientcmd.ConfigOverrides{}
	if kubeContext != "" {
		overrides.CurrentContext = kubeContext
	}

	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides)

	restConfig, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("building rest config: %w", err)
	}
	restConfig.Timeout = 30 * time.Second

	dynClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("creating dynamic client: %w", err)
	}

	cs, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("creating kubernetes client: %w", err)
	}

	// Resolve the active context name for display purposes.
	rawConfig, err := clientConfig.RawConfig()
	if err != nil {
		return nil, fmt.Errorf("loading raw kubeconfig: %w", err)
	}
	activeContext := rawConfig.CurrentContext
	if kubeContext != "" {
		activeContext = kubeContext
	}

	return &Installer{
		dynamic:   dynClient,
		clientset: cs,
		context:   activeContext,
	}, nil
}

// ActiveContext returns the kubeconfig context this installer is connected to.
func (i *Installer) ActiveContext() string {
	return i.context
}

// Install deploys the Konductor operator to the cluster.
// It applies manifests in order: namespace, CRDs, RBAC, secrets, deployment.
func (i *Installer) Install(ctx context.Context, opts InstallOptions) error {
	if opts.Namespace == "" {
		opts.Namespace = DefaultNamespace
	}
	if opts.Image == "" {
		opts.Image = DefaultImage
	}

	templateData := map[string]string{
		"Namespace":         opts.Namespace,
		"Image":             opts.Image,
		"SecretName":        DefaultSecretName,
		"HCloudTokenBase64": base64.StdEncoding.EncodeToString([]byte(opts.HCloudToken)),
		"TalosConfigBase64": base64.StdEncoding.EncodeToString([]byte(opts.TalosConfig)),
	}

	// Ordered list of manifests to apply. Order matters: namespace first,
	// then CRDs (so we can create CR instances later), RBAC, secrets, deployment.
	manifestFiles := []string{
		"manifests/namespace.yaml",
		"manifests/crd-nodepool.yaml",
		"manifests/crd-nodeclaim.yaml",
		"manifests/rbac.yaml",
		"manifests/secrets.yaml",
		"manifests/deployment.yaml",
	}

	for _, file := range manifestFiles {
		rendered, err := renderManifest(file, templateData)
		if err != nil {
			return fmt.Errorf("rendering %s: %w", file, err)
		}

		if opts.DryRun {
			fmt.Printf("---\n# Source: %s\n%s\n", file, rendered)
			continue
		}

		if err := i.applyManifests(ctx, rendered); err != nil {
			return fmt.Errorf("applying %s: %w", file, err)
		}
	}

	return nil
}

// Uninstall removes the Konductor operator from the cluster.
// It deletes resources in reverse order: deployment, secrets, RBAC, CRDs, namespace.
func (i *Installer) Uninstall(ctx context.Context, namespace string) error {
	if namespace == "" {
		namespace = DefaultNamespace
	}

	templateData := map[string]string{
		"Namespace":         namespace,
		"Image":             DefaultImage,
		"SecretName":        DefaultSecretName,
		"HCloudTokenBase64": "placeholder",
		"TalosConfigBase64": "placeholder",
	}

	// Reverse order for deletion.
	manifestFiles := []string{
		"manifests/deployment.yaml",
		"manifests/secrets.yaml",
		"manifests/rbac.yaml",
		"manifests/crd-nodeclaim.yaml",
		"manifests/crd-nodepool.yaml",
		"manifests/namespace.yaml",
	}

	for _, file := range manifestFiles {
		rendered, err := renderManifest(file, templateData)
		if err != nil {
			return fmt.Errorf("rendering %s: %w", file, err)
		}

		if err := i.deleteManifests(ctx, rendered); err != nil {
			return fmt.Errorf("deleting %s: %w", file, err)
		}
	}

	return nil
}

// Status checks the current state of the Konductor operator installation.
func (i *Installer) Status(ctx context.Context, namespace string) (*OperatorStatus, error) {
	if namespace == "" {
		namespace = DefaultNamespace
	}

	status := &OperatorStatus{
		Namespace: namespace,
	}

	// Check if namespace exists.
	_, err := i.clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err != nil {
		// Namespace doesn't exist — not installed.
		return status, nil
	}

	// Check deployment.
	deploy, err := i.clientset.AppsV1().Deployments(namespace).Get(ctx, "konductor-operator", metav1.GetOptions{})
	if err != nil {
		// Namespace exists but no deployment.
		return status, nil
	}

	status.Installed = true
	status.Ready = deploy.Status.AvailableReplicas > 0

	// Extract version from image tag.
	if len(deploy.Spec.Template.Spec.Containers) > 0 {
		image := deploy.Spec.Template.Spec.Containers[0].Image
		if idx := strings.LastIndex(image, ":"); idx != -1 {
			status.Version = image[idx+1:]
		}
	}

	// Count NodePool CRDs.
	npGVR := schema.GroupVersionResource{
		Group:    "konductor.io",
		Version:  "v1alpha1",
		Resource: "nodepools",
	}
	npList, err := i.dynamic.Resource(npGVR).List(ctx, metav1.ListOptions{})
	if err == nil {
		status.NodePools = len(npList.Items)
	}

	// Count NodeClaim CRDs.
	ncGVR := schema.GroupVersionResource{
		Group:    "konductor.io",
		Version:  "v1alpha1",
		Resource: "nodeclaims",
	}
	ncList, err := i.dynamic.Resource(ncGVR).List(ctx, metav1.ListOptions{})
	if err == nil {
		status.NodeClaims = len(ncList.Items)
	}

	return status, nil
}

// renderManifest reads a manifest file from the embedded FS, executes it as a
// Go text/template with the provided data, and returns the rendered YAML.
func renderManifest(file string, data map[string]string) (string, error) {
	raw, err := manifests.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("reading embedded file %s: %w", file, err)
	}

	tmpl, err := template.New(file).Parse(string(raw))
	if err != nil {
		return "", fmt.Errorf("parsing template %s: %w", file, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("executing template %s: %w", file, err)
	}

	return buf.String(), nil
}

// applyManifests parses a multi-document YAML string into unstructured objects
// and applies each one using server-side apply.
func (i *Installer) applyManifests(ctx context.Context, yamlContent string) error {
	objects, err := decodeYAML(yamlContent)
	if err != nil {
		return err
	}

	for _, obj := range objects {
		gvr, err := gvrFromObject(obj)
		if err != nil {
			return fmt.Errorf("resolving GVR for %s/%s: %w", obj.GetKind(), obj.GetName(), err)
		}

		var resource dynamic.ResourceInterface
		if obj.GetNamespace() != "" {
			resource = i.dynamic.Resource(gvr).Namespace(obj.GetNamespace())
		} else {
			resource = i.dynamic.Resource(gvr)
		}

		// Server-side apply using Patch with ApplyPatchType.
		data, err := obj.MarshalJSON()
		if err != nil {
			return fmt.Errorf("marshaling %s/%s: %w", obj.GetKind(), obj.GetName(), err)
		}

		_, err = resource.Patch(ctx, obj.GetName(), types.ApplyPatchType, data, metav1.PatchOptions{
			FieldManager: fieldManager,
		})
		if err != nil {
			return fmt.Errorf("applying %s %q: %w", obj.GetKind(), obj.GetName(), err)
		}

		fmt.Printf("  applied %s/%s\n", strings.ToLower(obj.GetKind()), obj.GetName())
	}

	return nil
}

// deleteManifests parses a multi-document YAML and deletes each object.
// Errors on "not found" are silently ignored since the resource may already
// be gone.
func (i *Installer) deleteManifests(ctx context.Context, yamlContent string) error {
	objects, err := decodeYAML(yamlContent)
	if err != nil {
		return err
	}

	propagation := metav1.DeletePropagationForeground

	for _, obj := range objects {
		gvr, err := gvrFromObject(obj)
		if err != nil {
			return fmt.Errorf("resolving GVR for %s/%s: %w", obj.GetKind(), obj.GetName(), err)
		}

		var resource dynamic.ResourceInterface
		if obj.GetNamespace() != "" {
			resource = i.dynamic.Resource(gvr).Namespace(obj.GetNamespace())
		} else {
			resource = i.dynamic.Resource(gvr)
		}

		err = resource.Delete(ctx, obj.GetName(), metav1.DeleteOptions{
			PropagationPolicy: &propagation,
		})
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				fmt.Printf("  already removed %s/%s\n", strings.ToLower(obj.GetKind()), obj.GetName())
				continue
			}
			return fmt.Errorf("deleting %s %q: %w", obj.GetKind(), obj.GetName(), err)
		}

		fmt.Printf("  deleted %s/%s\n", strings.ToLower(obj.GetKind()), obj.GetName())
	}

	return nil
}

// decodeYAML parses a multi-document YAML string into a list of unstructured objects.
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

		// Skip empty documents (e.g. from "---" separators).
		if obj.GetKind() == "" {
			continue
		}

		objects = append(objects, obj)
	}

	return objects, nil
}

// gvrFromObject determines the GroupVersionResource for an unstructured object
// based on its apiVersion and kind. This is a static mapping that covers
// all the resource types used in Konductor manifests.
func gvrFromObject(obj *unstructured.Unstructured) (schema.GroupVersionResource, error) {
	gvk := obj.GroupVersionKind()

	// Static mapping for known types. This avoids needing a discovery client
	// and keeps the installer self-contained.
	knownResources := map[schema.GroupVersionKind]schema.GroupVersionResource{
		{Group: "", Version: "v1", Kind: "Namespace"}: {
			Group: "", Version: "v1", Resource: "namespaces",
		},
		{Group: "", Version: "v1", Kind: "Secret"}: {
			Group: "", Version: "v1", Resource: "secrets",
		},
		{Group: "", Version: "v1", Kind: "ServiceAccount"}: {
			Group: "", Version: "v1", Resource: "serviceaccounts",
		},
		{Group: "apps", Version: "v1", Kind: "Deployment"}: {
			Group: "apps", Version: "v1", Resource: "deployments",
		},
		{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRole"}: {
			Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles",
		},
		{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRoleBinding"}: {
			Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings",
		},
		{Group: "apiextensions.k8s.io", Version: "v1", Kind: "CustomResourceDefinition"}: {
			Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions",
		},
	}

	if gvr, ok := knownResources[gvk]; ok {
		return gvr, nil
	}

	return schema.GroupVersionResource{}, fmt.Errorf("unknown resource type: %s", gvk.String())
}

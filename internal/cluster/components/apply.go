package components

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// gvrFromObject determines the GroupVersionResource for an unstructured object
// based on its apiVersion and kind. This is a static mapping covering the
// resource types used by cluster component manifests.
//
// For components with complex manifests (like cert-manager) that include many
// CRD types, callers should handle the "unknown" error gracefully.
func gvrFromObject(obj *unstructured.Unstructured) (schema.GroupVersionResource, error) {
	gvk := obj.GroupVersionKind()

	knownResources := map[schema.GroupVersionKind]schema.GroupVersionResource{
		// Core v1
		{Group: "", Version: "v1", Kind: "Namespace"}: {
			Group: "", Version: "v1", Resource: "namespaces",
		},
		{Group: "", Version: "v1", Kind: "Secret"}: {
			Group: "", Version: "v1", Resource: "secrets",
		},
		{Group: "", Version: "v1", Kind: "ServiceAccount"}: {
			Group: "", Version: "v1", Resource: "serviceaccounts",
		},
		{Group: "", Version: "v1", Kind: "Service"}: {
			Group: "", Version: "v1", Resource: "services",
		},
		{Group: "", Version: "v1", Kind: "ConfigMap"}: {
			Group: "", Version: "v1", Resource: "configmaps",
		},

		// Apps v1
		{Group: "apps", Version: "v1", Kind: "Deployment"}: {
			Group: "apps", Version: "v1", Resource: "deployments",
		},
		{Group: "apps", Version: "v1", Kind: "DaemonSet"}: {
			Group: "apps", Version: "v1", Resource: "daemonsets",
		},

		// RBAC
		{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRole"}: {
			Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles",
		},
		{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRoleBinding"}: {
			Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings",
		},
		{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "Role"}: {
			Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "roles",
		},
		{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "RoleBinding"}: {
			Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "rolebindings",
		},

		// API Extensions
		{Group: "apiextensions.k8s.io", Version: "v1", Kind: "CustomResourceDefinition"}: {
			Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions",
		},

		// Admissionregistration
		{Group: "admissionregistration.k8s.io", Version: "v1", Kind: "MutatingWebhookConfiguration"}: {
			Group: "admissionregistration.k8s.io", Version: "v1", Resource: "mutatingwebhookconfigurations",
		},
		{Group: "admissionregistration.k8s.io", Version: "v1", Kind: "ValidatingWebhookConfiguration"}: {
			Group: "admissionregistration.k8s.io", Version: "v1", Resource: "validatingwebhookconfigurations",
		},

		// API Registration
		{Group: "apiregistration.k8s.io", Version: "v1", Kind: "APIService"}: {
			Group: "apiregistration.k8s.io", Version: "v1", Resource: "apiservices",
		},
	}

	if gvr, ok := knownResources[gvk]; ok {
		return gvr, nil
	}

	return schema.GroupVersionResource{}, fmt.Errorf("unknown resource type: %s", gvk.String())
}

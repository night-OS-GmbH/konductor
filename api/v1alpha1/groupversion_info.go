package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	// Group is the API group for Konductor CRDs.
	Group = "konductor.io"

	// Version is the API version.
	Version = "v1alpha1"
)

// SchemeGroupVersion is the GroupVersion for this API.
var SchemeGroupVersion = schema.GroupVersion{Group: Group, Version: Version}

// Resource returns the GroupResource for a given resource name.
func Resource(resource string) schema.GroupResource {
	return SchemeGroupVersion.WithResource(resource).GroupResource()
}

var (
	// SchemeBuilder collects functions that add things to a scheme.
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

	// AddToScheme applies all stored functions to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

// addKnownTypes registers the Konductor CRD types with the runtime scheme.
func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion,
		&NodePool{},
		&NodePoolList{},
		&NodeClaim{},
		&NodeClaimList{},
	)
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}

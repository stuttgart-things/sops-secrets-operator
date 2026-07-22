/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha2

// LocalObjectReference is a reference to an object in the same namespace.
//
// Deliberately has no namespace field: it also names source CRs, where
// crossing namespaces is not supported. Secret references that may cross
// namespaces use SecretReference instead.
type LocalObjectReference struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// SecretReference references a Secret, by default in the same namespace as
// the referring resource.
type SecretReference struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Namespace holding the Secret. Defaults to the namespace of the
	// referring resource.
	//
	// Reading a Secret from another namespace is refused unless the
	// operator was started with --secret-ref-namespaces listing that
	// namespace. Without that flag this field has no effect other than to
	// make the resource fail with an explicit error.
	// +optional
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace,omitempty"`
}

// SecretKeyRef references a key within a Secret, by default in the same
// namespace as the referring resource.
type SecretKeyRef struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`

	// Namespace holding the Secret. Defaults to the namespace of the
	// referring resource. Subject to --secret-ref-namespaces, as for
	// SecretReference.
	// +optional
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace,omitempty"`
}

// SourceKind identifies the kind of a source referenced by sourceRef.
// +kubebuilder:validation:Enum=GitRepository;ObjectSource
type SourceKind string

const (
	// SourceKindGitRepository names a GitRepository CR as the source.
	SourceKindGitRepository SourceKind = "GitRepository"
	// SourceKindObjectSource names an ObjectSource CR as the source
	// (HTTPS / S3-compatible). Introduced in v1alpha2; handled by a
	// later stage of #12.
	SourceKindObjectSource SourceKind = "ObjectSource"
)

// SourceKindRef identifies a source CR by kind and name within the CR's
// namespace. This replaces v1alpha1's RepositoryRef (which only ever
// pointed at a GitRepository).
type SourceKindRef struct {
	// +kubebuilder:validation:Required
	Kind SourceKind `json:"kind"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

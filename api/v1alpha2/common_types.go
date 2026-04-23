/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha2

// LocalObjectReference is a reference to an object in the same namespace.
type LocalObjectReference struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// SecretKeyRef references a key within a Secret in the same namespace.
type SecretKeyRef struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
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

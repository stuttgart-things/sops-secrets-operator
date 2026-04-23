/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha2

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ManifestTarget describes how to materialize a decrypted Secret manifest.
//
// Namespace is authoritative: whatever appears in the decrypted file's
// metadata.namespace is ignored and replaced with this value (or the
// CR's own namespace if this is empty).
type ManifestTarget struct {
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// +optional
	NameOverride string `json:"nameOverride,omitempty"`

	// +optional
	Adopt bool `json:"adopt,omitempty"`
}

// SopsSecretManifestSpec describes a SOPS-encrypted k8s Secret manifest
// whose decrypted content should be applied as-is.
type SopsSecretManifestSpec struct {
	// +kubebuilder:validation:Required
	Source SourceRef `json:"source"`

	// +kubebuilder:validation:Required
	Decryption DecryptionSpec `json:"decryption"`

	// +optional
	Target ManifestTarget `json:"target,omitempty"`
}

// SopsSecretManifestStatus is the observed state of the SopsSecretManifest.
type SopsSecretManifestStatus struct {
	// +optional
	LastAppliedHash string `json:"lastAppliedHash,omitempty"`

	// +optional
	LastSyncedCommit string `json:"lastSyncedCommit,omitempty"`

	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Source",type=string,JSONPath=".spec.source.sourceRef.name"
// +kubebuilder:printcolumn:name="Kind",type=string,JSONPath=".spec.source.sourceRef.kind"
// +kubebuilder:printcolumn:name="Path",type=string,JSONPath=".spec.source.path"
// +kubebuilder:printcolumn:name="Applied",type=string,JSONPath=".status.conditions[?(@.type==\"Applied\")].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// SopsSecretManifest decrypts a SOPS-encrypted Kubernetes Secret manifest
// and applies it to the cluster.
type SopsSecretManifest struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec SopsSecretManifestSpec `json:"spec"`

	// +optional
	Status SopsSecretManifestStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// SopsSecretManifestList contains a list of SopsSecretManifest.
type SopsSecretManifestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []SopsSecretManifest `json:"items"`
}

// Hub marks SopsSecretManifest v1alpha2 as the conversion hub.
func (*SopsSecretManifest) Hub() {}

func init() {
	SchemeBuilder.Register(&SopsSecretManifest{}, &SopsSecretManifestList{})
}

/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha2

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SourceRef identifies a file in a source CR (GitRepository or
// ObjectSource).
//
// Replaces v1alpha1's RepositoryRef-only shape. v1alpha1's
// source.repositoryRef.name is converted lossslessly into
// source.sourceRef{Kind: "GitRepository", Name}.
type SourceRef struct {
	// +kubebuilder:validation:Required
	SourceRef SourceKindRef `json:"sourceRef"`

	// Path within the source (git repo path or object key).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Path string `json:"path"`
}

// DecryptionSpec configures how the source file is decrypted.
type DecryptionSpec struct {
	// +kubebuilder:validation:Required
	KeyRef SecretKeyRef `json:"keyRef"`
}

// MappingTarget describes the target k8s Secret for a SopsSecret.
type MappingTarget struct {
	// +optional
	Name string `json:"name,omitempty"`

	// +optional
	Namespace string `json:"namespace,omitempty"`

	// +optional
	Type corev1.SecretType `json:"type,omitempty"`

	// +optional
	Adopt bool `json:"adopt,omitempty"`
}

// DataMapping maps one key in the decrypted flat YAML to one key in the
// target Secret's data.
type DataMapping struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	From string `json:"from"`
}

// SopsSecretSpec describes a mapping from a SOPS-encrypted flat key/value
// YAML file into a k8s Secret.
type SopsSecretSpec struct {
	// +kubebuilder:validation:Required
	Source SourceRef `json:"source"`

	// +kubebuilder:validation:Required
	Decryption DecryptionSpec `json:"decryption"`

	// +optional
	Target MappingTarget `json:"target,omitempty"`

	// +kubebuilder:validation:MinItems=1
	Data []DataMapping `json:"data"`
}

// SopsSecretStatus is the observed state of the SopsSecret.
type SopsSecretStatus struct {
	// +optional
	LastAppliedHash string `json:"lastAppliedHash,omitempty"`

	// +optional
	LastSyncedCommit string `json:"lastSyncedCommit,omitempty"`

	// LastProcessedReconcileToken is the value of the
	// sops.stuttgart-things.com/reconcile-requested annotation that was last
	// honored by the reconciler. When the live annotation differs, the next
	// reconcile re-runs the full pipeline regardless of cache state.
	// +optional
	LastProcessedReconcileToken string `json:"lastProcessedReconcileToken,omitempty"`

	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Condition types used by SopsSecret and SopsSecretManifest.
const (
	ConditionDecrypted = "Decrypted"
	ConditionApplied   = "Applied"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:printcolumn:name="Source",type=string,JSONPath=".spec.source.sourceRef.name"
// +kubebuilder:printcolumn:name="Kind",type=string,JSONPath=".spec.source.sourceRef.kind"
// +kubebuilder:printcolumn:name="Path",type=string,JSONPath=".spec.source.path"
// +kubebuilder:printcolumn:name="Applied",type=string,JSONPath=".status.conditions[?(@.type==\"Applied\")].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// SopsSecret decrypts a SOPS-encrypted flat key/value file and maps selected
// keys into a target Kubernetes Secret.
type SopsSecret struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec SopsSecretSpec `json:"spec"`

	// +optional
	Status SopsSecretStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// SopsSecretList contains a list of SopsSecret.
type SopsSecretList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []SopsSecret `json:"items"`
}

// Hub marks SopsSecret v1alpha2 as the conversion hub.
func (*SopsSecret) Hub() {}

func init() {
	SchemeBuilder.Register(&SopsSecret{}, &SopsSecretList{})
}

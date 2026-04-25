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

// InlineMode selects how the decrypted YAML is interpreted.
// +kubebuilder:validation:Enum=Mapping;Manifest
type InlineMode string

const (
	InlineModeMapping  InlineMode = "Mapping"
	InlineModeManifest InlineMode = "Manifest"
)

// InlineTarget describes the target k8s Secret for an InlineSopsSecret.
type InlineTarget struct {
	// +optional
	Name string `json:"name,omitempty"`

	// +optional
	Namespace string `json:"namespace,omitempty"`

	// +optional
	Type corev1.SecretType `json:"type,omitempty"`

	// +optional
	Adopt bool `json:"adopt,omitempty"`
}

// InlineSopsSecretSpec describes an inline SOPS-encrypted payload that is
// materialized into a target Kubernetes Secret.
//
// +kubebuilder:validation:XValidation:rule="self.mode != 'Mapping' || (has(self.data) && size(self.data) > 0)",message="spec.data is required and must be non-empty when mode=Mapping"
// +kubebuilder:validation:XValidation:rule="self.mode != 'Manifest' || !has(self.data) || size(self.data) == 0",message="spec.data must be empty when mode=Manifest"
type InlineSopsSecretSpec struct {
	// +kubebuilder:validation:Required
	Mode InlineMode `json:"mode"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	EncryptedYAML string `json:"encryptedYAML"`

	// +kubebuilder:validation:Required
	Decryption DecryptionSpec `json:"decryption"`

	// +optional
	Target InlineTarget `json:"target,omitempty"`

	// +optional
	Data []DataMapping `json:"data,omitempty"`
}

// InlineSopsSecretStatus is the observed state of the InlineSopsSecret.
type InlineSopsSecretStatus struct {
	// +optional
	LastAppliedHash string `json:"lastAppliedHash,omitempty"`

	// +optional
	LastProcessedReconcileToken string `json:"lastProcessedReconcileToken,omitempty"`

	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Mode",type=string,JSONPath=".spec.mode"
// +kubebuilder:printcolumn:name="Applied",type=string,JSONPath=".status.conditions[?(@.type==\"Applied\")].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// InlineSopsSecret materializes a target Kubernetes Secret from a
// SOPS-encrypted payload embedded directly in the CR.
type InlineSopsSecret struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec InlineSopsSecretSpec `json:"spec"`

	// +optional
	Status InlineSopsSecretStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// InlineSopsSecretList contains a list of InlineSopsSecret.
type InlineSopsSecretList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []InlineSopsSecret `json:"items"`
}

// Hub marks InlineSopsSecret v1alpha2 as the conversion hub.
func (*InlineSopsSecret) Hub() {}

func init() {
	SchemeBuilder.Register(&InlineSopsSecret{}, &InlineSopsSecretList{})
}

/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SourceRef identifies a file in a GitRepository.
type SourceRef struct {
	// +kubebuilder:validation:Required
	RepositoryRef LocalObjectReference `json:"repositoryRef"`

	// Path within the repository.
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
	// Defaults to metadata.name.
	// +optional
	Name string `json:"name,omitempty"`

	// Defaults to metadata.namespace.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// Defaults to Opaque.
	// +optional
	Type corev1.SecretType `json:"type,omitempty"`

	// When true, adopt a pre-existing Secret that is not already
	// managed by this operator. Defaults to false (refuse).
	// +optional
	Adopt bool `json:"adopt,omitempty"`
}

// DataMapping maps one key in the decrypted flat YAML to one key in the
// target Secret's data.
type DataMapping struct {
	// Key is the target k8s Secret data key.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`

	// From is the top-level key in the decrypted flat YAML file.
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
	// LastAppliedHash is the SHA-256 of the most recently applied target
	// Secret data (deterministic over the key-sorted key/value pairs).
	// +optional
	LastAppliedHash string `json:"lastAppliedHash,omitempty"`

	// LastSyncedCommit is the source repository commit at the last apply.
	// +optional
	LastSyncedCommit string `json:"lastSyncedCommit,omitempty"`

	// ObservedGeneration reflects the generation most recently reconciled.
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
// +kubebuilder:printcolumn:name="Source",type=string,JSONPath=".spec.source.repositoryRef.name"
// +kubebuilder:printcolumn:name="Path",type=string,JSONPath=".spec.source.path"
// +kubebuilder:printcolumn:name="Applied",type=string,JSONPath=".status.conditions[?(@.type==\"Applied\")].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// SopsSecret decrypts a SOPS-encrypted flat key/value file from a GitRepository
// and maps selected keys into a target Kubernetes Secret.
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

func init() {
	SchemeBuilder.Register(&SopsSecret{}, &SopsSecretList{})
}

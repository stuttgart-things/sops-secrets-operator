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

// InlineMode selects how the decrypted YAML is interpreted.
// +kubebuilder:validation:Enum=Mapping;Manifest
type InlineMode string

const (
	// InlineModeMapping parses the decrypted YAML as a flat map of scalars
	// and projects it into a target Secret via the Data mapping list —
	// same contract as SopsSecret.
	InlineModeMapping InlineMode = "Mapping"

	// InlineModeManifest parses the decrypted YAML as a whole core/v1
	// Secret manifest and applies it, same contract as SopsSecretManifest
	// (whitelist, namespace-authoritative, stringData normalization).
	InlineModeManifest InlineMode = "Manifest"
)

// InlineTarget describes the target k8s Secret for an InlineSopsSecret.
//
// Namespace is always authoritative: whatever appears inside the decrypted
// Secret manifest (Manifest mode) is ignored and replaced with this value
// (or the CR's own namespace if unset). Unlike SopsSecretManifest this is
// less load-bearing since the content was pasted into the CR by someone
// with namespace write access, but we keep the invariant for consistency.
type InlineTarget struct {
	// Name for the target Secret.
	//
	// In Mapping mode, defaults to the CR's metadata.name.
	// In Manifest mode, overrides the decrypted manifest's metadata.name
	// when set; falls back to the manifest's name otherwise.
	// +optional
	Name string `json:"name,omitempty"`

	// Namespace for the target Secret. Defaults to the CR's
	// metadata.namespace.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// Type for the target Secret.
	//
	// In Mapping mode, defaults to Opaque.
	// In Manifest mode, defaults to the decrypted manifest's type, falling
	// back to Opaque. Set here to override.
	// +optional
	Type corev1.SecretType `json:"type,omitempty"`

	// When true, adopt a pre-existing Secret that is not already managed
	// by this operator. Defaults to false (refuse).
	// +optional
	Adopt bool `json:"adopt,omitempty"`
}

// InlineSopsSecretSpec describes an inline SOPS-encrypted payload that is
// materialized into a target Kubernetes Secret.
//
// +kubebuilder:validation:XValidation:rule="self.mode != 'Mapping' || (has(self.data) && size(self.data) > 0)",message="spec.data is required and must be non-empty when mode=Mapping"
// +kubebuilder:validation:XValidation:rule="self.mode != 'Manifest' || !has(self.data) || size(self.data) == 0",message="spec.data must be empty when mode=Manifest"
type InlineSopsSecretSpec struct {
	// Mode selects how the decrypted content is interpreted.
	// +kubebuilder:validation:Required
	Mode InlineMode `json:"mode"`

	// EncryptedYAML is a SOPS-encrypted YAML document. Paste the complete
	// output of `sops --encrypt`. Its SOPS MAC is validated, so the string
	// must be preserved byte-for-byte.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	EncryptedYAML string `json:"encryptedYAML"`

	// +kubebuilder:validation:Required
	Decryption DecryptionSpec `json:"decryption"`

	// +optional
	Target InlineTarget `json:"target,omitempty"`

	// Data is the mapping list used in Mapping mode. Required when
	// mode=Mapping; must be empty when mode=Manifest.
	// +optional
	Data []DataMapping `json:"data,omitempty"`
}

// InlineSopsSecretStatus is the observed state of the InlineSopsSecret.
type InlineSopsSecretStatus struct {
	// LastAppliedHash is the SHA-256 of the most recently applied target
	// Secret.
	// +optional
	LastAppliedHash string `json:"lastAppliedHash,omitempty"`

	// LastProcessedReconcileToken records the last value of the
	// sops.stuttgart-things.com/reconcile-requested annotation that the
	// reconciler honored (forced re-decrypt + re-apply).
	// +optional
	LastProcessedReconcileToken string `json:"lastProcessedReconcileToken,omitempty"`

	// ObservedGeneration reflects the generation most recently reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:printcolumn:name="Mode",type=string,JSONPath=".spec.mode"
// +kubebuilder:printcolumn:name="Applied",type=string,JSONPath=".status.conditions[?(@.type==\"Applied\")].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// InlineSopsSecret materializes a target Kubernetes Secret from a
// SOPS-encrypted payload embedded directly in the CR (no Git, no object
// store). Access control is RBAC on the CR itself.
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

func init() {
	SchemeBuilder.Register(&InlineSopsSecret{}, &InlineSopsSecretList{})
}

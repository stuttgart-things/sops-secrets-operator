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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ManifestTarget describes how to materialize a decrypted Secret manifest.
//
// Namespace is authoritative: whatever appears in the decrypted file's
// metadata.namespace is ignored and replaced with this value (or the
// CR's own namespace if this is empty). This prevents a git-repo writer
// from implying write access to arbitrary cluster namespaces.
type ManifestTarget struct {
	// Defaults to metadata.namespace of the CR.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// NameOverride replaces the name from the decrypted manifest.
	// When empty, the manifest's metadata.name is used.
	// +optional
	NameOverride string `json:"nameOverride,omitempty"`

	// When true, adopt a pre-existing Secret that is not already
	// managed by this operator. Defaults to false (refuse).
	// +optional
	Adopt bool `json:"adopt,omitempty"`
}

// SopsSecretManifestSpec describes a SOPS-encrypted k8s Secret manifest
// whose decrypted content should be applied as-is (pass-through mode).
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
	// LastAppliedHash is the SHA-256 of the most recently applied target
	// Secret (type + data/stringData).
	// +optional
	LastAppliedHash string `json:"lastAppliedHash,omitempty"`

	// LastSyncedCommit is the source repository commit at the last apply.
	// +optional
	LastSyncedCommit string `json:"lastSyncedCommit,omitempty"`

	// LastProcessedReconcileToken mirrors SopsSecret's field.
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
// +kubebuilder:printcolumn:name="Source",type=string,JSONPath=".spec.source.repositoryRef.name"
// +kubebuilder:printcolumn:name="Path",type=string,JSONPath=".spec.source.path"
// +kubebuilder:printcolumn:name="Applied",type=string,JSONPath=".status.conditions[?(@.type==\"Applied\")].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// SopsSecretManifest decrypts a SOPS-encrypted Kubernetes Secret manifest
// from a GitRepository and applies it to the cluster.
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

func init() {
	SchemeBuilder.Register(&SopsSecretManifest{}, &SopsSecretManifestList{})
}

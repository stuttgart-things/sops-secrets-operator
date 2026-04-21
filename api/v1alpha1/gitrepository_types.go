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

// GitAuthType identifies the authentication method for a GitRepository.
// +kubebuilder:validation:Enum=basic;ssh
type GitAuthType string

const (
	GitAuthBasic GitAuthType = "basic"
	GitAuthSSH   GitAuthType = "ssh"
)

// GitAuth configures authentication to the remote repository.
//
// For "basic" the referenced Secret must carry `username` and `password`
// keys (password may be a personal access token).
//
// For "ssh" the referenced Secret must carry `privateKey` and `knownHosts`,
// and may optionally carry `passphrase` and `user`. Strict host-key
// checking is enforced; `knownHosts` is required.
type GitAuth struct {
	// +kubebuilder:validation:Required
	Type GitAuthType `json:"type"`

	// +kubebuilder:validation:Required
	SecretRef LocalObjectReference `json:"secretRef"`
}

// GitRepositorySpec describes a git repository to clone and keep synced.
type GitRepositorySpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	URL string `json:"url"`

	// Branch is fetched when Revision is empty. Defaults to "main".
	// +optional
	Branch string `json:"branch,omitempty"`

	// Revision pins to a commit SHA or tag. When non-empty it overrides Branch.
	// +optional
	Revision string `json:"revision,omitempty"`

	// Interval between reconciles. Defaults to 5m.
	// +optional
	Interval metav1.Duration `json:"interval,omitempty"`

	// +optional
	Auth *GitAuth `json:"auth,omitempty"`
}

// GitRepositoryStatus is the observed state of the GitRepository.
type GitRepositoryStatus struct {
	// LastSyncedCommit is the commit SHA at the last successful reconcile.
	// +optional
	LastSyncedCommit string `json:"lastSyncedCommit,omitempty"`

	// CacheReady is true when the local cache matches the configured ref.
	// +optional
	CacheReady bool `json:"cacheReady,omitempty"`

	// ObservedGeneration reflects the generation most recently reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Condition types used by GitRepository.
const (
	ConditionSourceReady  = "SourceReady"
	ConditionAuthResolved = "AuthResolved"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=".spec.url"
// +kubebuilder:printcolumn:name="Commit",type=string,JSONPath=".status.lastSyncedCommit"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type==\"SourceReady\")].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// GitRepository is a remote source of SOPS-encrypted files.
type GitRepository struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec GitRepositorySpec `json:"spec"`

	// +optional
	Status GitRepositoryStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// GitRepositoryList contains a list of GitRepository.
type GitRepositoryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GitRepository `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GitRepository{}, &GitRepositoryList{})
}

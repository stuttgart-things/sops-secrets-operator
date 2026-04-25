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

// ObjectAuthType identifies the authentication method for an ObjectSource.
// +kubebuilder:validation:Enum=none;bearer;basic;s3
type ObjectAuthType string

const (
	// ObjectAuthNone selects no authentication (anonymous HTTPS / public bucket).
	ObjectAuthNone ObjectAuthType = "none"
	// ObjectAuthBearer selects HTTP bearer-token authentication. The
	// referenced Secret must carry a "token" key.
	ObjectAuthBearer ObjectAuthType = "bearer"
	// ObjectAuthBasic selects HTTP basic authentication. The referenced
	// Secret must carry "username" and "password" keys.
	ObjectAuthBasic ObjectAuthType = "basic"
	// ObjectAuthS3 selects S3-compatible access keys. The referenced
	// Secret must carry "accessKey" and "secretKey" keys.
	ObjectAuthS3 ObjectAuthType = "s3"
)

// ObjectAuth configures authentication for an ObjectSource.
//
// For "none" the SecretRef must be omitted. For every other type a
// SecretRef is required with backend-appropriate keys.
type ObjectAuth struct {
	// +kubebuilder:validation:Required
	Type ObjectAuthType `json:"type"`

	// +optional
	SecretRef *LocalObjectReference `json:"secretRef,omitempty"`
}

// BucketSpec addresses an object in an S3-compatible bucket.
type BucketSpec struct {
	// Endpoint is the bucket endpoint (host[:port]), e.g. "s3.amazonaws.com"
	// or "minio.internal:9000". Must not include a scheme.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Endpoint string `json:"endpoint"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// +optional
	Region string `json:"region,omitempty"`

	// Insecure disables TLS when talking to the bucket endpoint.
	// Defaults to false.
	// +optional
	Insecure bool `json:"insecure,omitempty"`
}

// ObjectSourceSpec describes an HTTPS URL or S3-compatible bucket from
// which to fetch a single SOPS-encrypted file.
//
// Exactly one of `url` or `bucket` must be set.
//
// +kubebuilder:validation:XValidation:rule="(has(self.url) ? 1 : 0) + (has(self.bucket) ? 1 : 0) == 1",message="exactly one of spec.url or spec.bucket must be set"
type ObjectSourceSpec struct {
	// URL is an HTTPS URL to GET. Mutually exclusive with Bucket.
	// +optional
	URL string `json:"url,omitempty"`

	// Bucket configures an S3-compatible object to GET. Mutually
	// exclusive with URL. The object key is taken from the consumer's
	// source.path field.
	// +optional
	Bucket *BucketSpec `json:"bucket,omitempty"`

	// Interval between reconciles. Defaults to 5m.
	// +optional
	Interval metav1.Duration `json:"interval,omitempty"`

	// +optional
	Auth *ObjectAuth `json:"auth,omitempty"`
}

// ObjectSourceStatus is the observed state of the ObjectSource.
type ObjectSourceStatus struct {
	// LastSyncedETag is the ETag (or equivalent content identifier)
	// observed at the last successful reconcile.
	// +optional
	LastSyncedETag string `json:"lastSyncedETag,omitempty"`

	// LastSyncedAt is the wall-clock time of the last successful reconcile.
	// +optional
	LastSyncedAt *metav1.Time `json:"lastSyncedAt,omitempty"`

	// CacheReady is true when the local cache holds the object at LastSyncedETag.
	// +optional
	CacheReady bool `json:"cacheReady,omitempty"`

	// LastProcessedReconcileToken records the last value of the
	// sops.stuttgart-things.com/reconcile-requested annotation that the
	// reconciler honored (cache eviction + unconditional refetch).
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
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=".spec.url"
// +kubebuilder:printcolumn:name="Bucket",type=string,JSONPath=".spec.bucket.name"
// +kubebuilder:printcolumn:name="ETag",type=string,JSONPath=".status.lastSyncedETag"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type==\"SourceReady\")].status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// ObjectSource is a remote HTTPS or S3-compatible source of SOPS-encrypted files.
type ObjectSource struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec ObjectSourceSpec `json:"spec"`

	// +optional
	Status ObjectSourceStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ObjectSourceList contains a list of ObjectSource.
type ObjectSourceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ObjectSource `json:"items"`
}

// Hub marks ObjectSource v1alpha2 as the conversion hub. There is no
// v1alpha1 counterpart; Hub is implemented for symmetry with GitRepository
// so a future v1alphaN can convert cleanly.
func (*ObjectSource) Hub() {}

func init() {
	SchemeBuilder.Register(&ObjectSource{}, &ObjectSourceList{})
}

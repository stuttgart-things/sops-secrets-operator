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

// LocalObjectReference is a reference to an object in the same namespace.
//
// Deliberately has no namespace field: it also names GitRepository CRs via
// repositoryRef, where crossing namespaces is not supported. Secret
// references that may cross namespaces use SecretReference instead.
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

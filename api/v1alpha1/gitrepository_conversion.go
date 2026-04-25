/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1

import (
	"sigs.k8s.io/controller-runtime/pkg/conversion"

	v1alpha2 "github.com/stuttgart-things/sops-secrets-operator/api/v1alpha2"
)

// ConvertTo converts this GitRepository (v1alpha1) to the Hub version
// (v1alpha2). The shape is identical — this is a pure version bump.
func (src *GitRepository) ConvertTo(dstRaw conversion.Hub) error {
	dst := dstRaw.(*v1alpha2.GitRepository)
	dst.ObjectMeta = src.ObjectMeta
	dst.Spec = v1alpha2.GitRepositorySpec{
		URL:      src.Spec.URL,
		Branch:   src.Spec.Branch,
		Revision: src.Spec.Revision,
		Interval: src.Spec.Interval,
	}
	if src.Spec.Auth != nil {
		dst.Spec.Auth = &v1alpha2.GitAuth{
			Type:      v1alpha2.GitAuthType(src.Spec.Auth.Type),
			SecretRef: v1alpha2.LocalObjectReference{Name: src.Spec.Auth.SecretRef.Name},
		}
	}
	dst.Status = v1alpha2.GitRepositoryStatus{
		LastSyncedCommit:            src.Status.LastSyncedCommit,
		CacheReady:                  src.Status.CacheReady,
		LastProcessedReconcileToken: src.Status.LastProcessedReconcileToken,
		ObservedGeneration:          src.Status.ObservedGeneration,
		Conditions:                  src.Status.Conditions,
	}
	return nil
}

// ConvertFrom converts the Hub version (v1alpha2) back to this version
// (v1alpha1).
func (dst *GitRepository) ConvertFrom(srcRaw conversion.Hub) error {
	src := srcRaw.(*v1alpha2.GitRepository)
	dst.ObjectMeta = src.ObjectMeta
	dst.Spec = GitRepositorySpec{
		URL:      src.Spec.URL,
		Branch:   src.Spec.Branch,
		Revision: src.Spec.Revision,
		Interval: src.Spec.Interval,
	}
	if src.Spec.Auth != nil {
		dst.Spec.Auth = &GitAuth{
			Type:      GitAuthType(src.Spec.Auth.Type),
			SecretRef: LocalObjectReference{Name: src.Spec.Auth.SecretRef.Name},
		}
	}
	dst.Status = GitRepositoryStatus{
		LastSyncedCommit:            src.Status.LastSyncedCommit,
		CacheReady:                  src.Status.CacheReady,
		LastProcessedReconcileToken: src.Status.LastProcessedReconcileToken,
		ObservedGeneration:          src.Status.ObservedGeneration,
		Conditions:                  src.Status.Conditions,
	}
	return nil
}

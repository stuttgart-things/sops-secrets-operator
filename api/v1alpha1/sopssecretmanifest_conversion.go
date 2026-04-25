/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1

import (
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/conversion"

	v1alpha2 "github.com/stuttgart-things/sops-secrets-operator/api/v1alpha2"
)

// ConvertTo converts this SopsSecretManifest (v1alpha1) to the Hub version.
func (src *SopsSecretManifest) ConvertTo(dstRaw conversion.Hub) error {
	dst := dstRaw.(*v1alpha2.SopsSecretManifest)
	dst.ObjectMeta = src.ObjectMeta
	dst.Spec = v1alpha2.SopsSecretManifestSpec{
		Source: v1alpha2.SourceRef{
			SourceRef: v1alpha2.SourceKindRef{
				Kind: v1alpha2.SourceKindGitRepository,
				Name: src.Spec.Source.RepositoryRef.Name,
			},
			Path: src.Spec.Source.Path,
		},
		Decryption: v1alpha2.DecryptionSpec{
			KeyRef: v1alpha2.SecretKeyRef{
				Name: src.Spec.Decryption.KeyRef.Name,
				Key:  src.Spec.Decryption.KeyRef.Key,
			},
		},
		Target: v1alpha2.ManifestTarget{
			Namespace:    src.Spec.Target.Namespace,
			NameOverride: src.Spec.Target.NameOverride,
			Adopt:        src.Spec.Target.Adopt,
		},
	}
	dst.Status = v1alpha2.SopsSecretManifestStatus{
		LastAppliedHash:             src.Status.LastAppliedHash,
		LastSyncedCommit:            src.Status.LastSyncedCommit,
		LastProcessedReconcileToken: src.Status.LastProcessedReconcileToken,
		ObservedGeneration:          src.Status.ObservedGeneration,
		Conditions:                  src.Status.Conditions,
	}
	return nil
}

// ConvertFrom converts the Hub version back to v1alpha1. Rejects non-Git
// source kinds.
func (dst *SopsSecretManifest) ConvertFrom(srcRaw conversion.Hub) error {
	src := srcRaw.(*v1alpha2.SopsSecretManifest)
	if src.Spec.Source.SourceRef.Kind != "" &&
		src.Spec.Source.SourceRef.Kind != v1alpha2.SourceKindGitRepository {
		return fmt.Errorf("cannot convert SopsSecretManifest with source kind %q to v1alpha1 (only %q is representable)",
			src.Spec.Source.SourceRef.Kind, v1alpha2.SourceKindGitRepository)
	}
	dst.ObjectMeta = src.ObjectMeta
	dst.Spec = SopsSecretManifestSpec{
		Source: SourceRef{
			RepositoryRef: LocalObjectReference{Name: src.Spec.Source.SourceRef.Name},
			Path:          src.Spec.Source.Path,
		},
		Decryption: DecryptionSpec{
			KeyRef: SecretKeyRef{
				Name: src.Spec.Decryption.KeyRef.Name,
				Key:  src.Spec.Decryption.KeyRef.Key,
			},
		},
		Target: ManifestTarget{
			Namespace:    src.Spec.Target.Namespace,
			NameOverride: src.Spec.Target.NameOverride,
			Adopt:        src.Spec.Target.Adopt,
		},
	}
	dst.Status = SopsSecretManifestStatus{
		LastAppliedHash:             src.Status.LastAppliedHash,
		LastSyncedCommit:            src.Status.LastSyncedCommit,
		LastProcessedReconcileToken: src.Status.LastProcessedReconcileToken,
		ObservedGeneration:          src.Status.ObservedGeneration,
		Conditions:                  src.Status.Conditions,
	}
	return nil
}

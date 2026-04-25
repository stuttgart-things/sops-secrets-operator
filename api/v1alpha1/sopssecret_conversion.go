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

// ConvertTo converts this SopsSecret (v1alpha1) to the Hub version.
// v1alpha1's source.repositoryRef becomes source.sourceRef with
// kind=GitRepository, since v1alpha1 only ever referenced GitRepository.
func (src *SopsSecret) ConvertTo(dstRaw conversion.Hub) error {
	dst := dstRaw.(*v1alpha2.SopsSecret)
	dst.ObjectMeta = src.ObjectMeta
	dst.Spec = v1alpha2.SopsSecretSpec{
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
		Target: v1alpha2.MappingTarget{
			Name:      src.Spec.Target.Name,
			Namespace: src.Spec.Target.Namespace,
			Type:      src.Spec.Target.Type,
			Adopt:     src.Spec.Target.Adopt,
		},
		Data: convertDataMappingsTo(src.Spec.Data),
	}
	dst.Status = v1alpha2.SopsSecretStatus{
		LastAppliedHash:             src.Status.LastAppliedHash,
		LastSyncedCommit:            src.Status.LastSyncedCommit,
		LastProcessedReconcileToken: src.Status.LastProcessedReconcileToken,
		ObservedGeneration:          src.Status.ObservedGeneration,
		Conditions:                  src.Status.Conditions,
	}
	return nil
}

// ConvertFrom converts the Hub version back to v1alpha1. Rejects Hub
// objects whose sourceRef.kind isn't GitRepository, since v1alpha1 has
// no way to represent a non-Git source.
func (dst *SopsSecret) ConvertFrom(srcRaw conversion.Hub) error {
	src := srcRaw.(*v1alpha2.SopsSecret)
	if src.Spec.Source.SourceRef.Kind != "" &&
		src.Spec.Source.SourceRef.Kind != v1alpha2.SourceKindGitRepository {
		return fmt.Errorf("cannot convert SopsSecret with source kind %q to v1alpha1 (only %q is representable)",
			src.Spec.Source.SourceRef.Kind, v1alpha2.SourceKindGitRepository)
	}
	dst.ObjectMeta = src.ObjectMeta
	dst.Spec = SopsSecretSpec{
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
		Target: MappingTarget{
			Name:      src.Spec.Target.Name,
			Namespace: src.Spec.Target.Namespace,
			Type:      src.Spec.Target.Type,
			Adopt:     src.Spec.Target.Adopt,
		},
		Data: convertDataMappingsFrom(src.Spec.Data),
	}
	dst.Status = SopsSecretStatus{
		LastAppliedHash:             src.Status.LastAppliedHash,
		LastSyncedCommit:            src.Status.LastSyncedCommit,
		LastProcessedReconcileToken: src.Status.LastProcessedReconcileToken,
		ObservedGeneration:          src.Status.ObservedGeneration,
		Conditions:                  src.Status.Conditions,
	}
	return nil
}

func convertDataMappingsTo(in []DataMapping) []v1alpha2.DataMapping {
	if in == nil {
		return nil
	}
	out := make([]v1alpha2.DataMapping, len(in))
	for i, m := range in {
		out[i] = v1alpha2.DataMapping{Key: m.Key, From: m.From}
	}
	return out
}

func convertDataMappingsFrom(in []v1alpha2.DataMapping) []DataMapping {
	if in == nil {
		return nil
	}
	out := make([]DataMapping, len(in))
	for i, m := range in {
		out[i] = DataMapping{Key: m.Key, From: m.From}
	}
	return out
}

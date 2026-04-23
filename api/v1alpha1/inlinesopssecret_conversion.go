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

// ConvertTo converts this InlineSopsSecret (v1alpha1) to the Hub version.
// InlineSopsSecret has no source field, so conversion is a pure version bump.
func (src *InlineSopsSecret) ConvertTo(dstRaw conversion.Hub) error {
	dst := dstRaw.(*v1alpha2.InlineSopsSecret)
	dst.ObjectMeta = src.ObjectMeta
	dst.Spec = v1alpha2.InlineSopsSecretSpec{
		Mode:          v1alpha2.InlineMode(src.Spec.Mode),
		EncryptedYAML: src.Spec.EncryptedYAML,
		Decryption: v1alpha2.DecryptionSpec{
			KeyRef: v1alpha2.SecretKeyRef{
				Name: src.Spec.Decryption.KeyRef.Name,
				Key:  src.Spec.Decryption.KeyRef.Key,
			},
		},
		Target: v1alpha2.InlineTarget{
			Name:      src.Spec.Target.Name,
			Namespace: src.Spec.Target.Namespace,
			Type:      src.Spec.Target.Type,
			Adopt:     src.Spec.Target.Adopt,
		},
		Data: convertDataMappingsTo(src.Spec.Data),
	}
	dst.Status = v1alpha2.InlineSopsSecretStatus{
		LastAppliedHash:    src.Status.LastAppliedHash,
		ObservedGeneration: src.Status.ObservedGeneration,
		Conditions:         src.Status.Conditions,
	}
	return nil
}

// ConvertFrom converts the Hub version back to v1alpha1.
func (dst *InlineSopsSecret) ConvertFrom(srcRaw conversion.Hub) error {
	src := srcRaw.(*v1alpha2.InlineSopsSecret)
	dst.ObjectMeta = src.ObjectMeta
	dst.Spec = InlineSopsSecretSpec{
		Mode:          InlineMode(src.Spec.Mode),
		EncryptedYAML: src.Spec.EncryptedYAML,
		Decryption: DecryptionSpec{
			KeyRef: SecretKeyRef{
				Name: src.Spec.Decryption.KeyRef.Name,
				Key:  src.Spec.Decryption.KeyRef.Key,
			},
		},
		Target: InlineTarget{
			Name:      src.Spec.Target.Name,
			Namespace: src.Spec.Target.Namespace,
			Type:      src.Spec.Target.Type,
			Adopt:     src.Spec.Target.Adopt,
		},
		Data: convertDataMappingsFrom(src.Spec.Data),
	}
	dst.Status = InlineSopsSecretStatus{
		LastAppliedHash:    src.Status.LastAppliedHash,
		ObservedGeneration: src.Status.ObservedGeneration,
		Conditions:         src.Status.Conditions,
	}
	return nil
}

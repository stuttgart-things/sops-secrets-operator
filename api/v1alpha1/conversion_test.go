/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sopsv1alpha1 "github.com/stuttgart-things/sops-secrets-operator/api/v1alpha1"
	sopsv1alpha2 "github.com/stuttgart-things/sops-secrets-operator/api/v1alpha2"
)

func TestGitRepositoryConversion_Roundtrip(t *testing.T) {
	src := &sopsv1alpha1.GitRepository{
		ObjectMeta: metav1.ObjectMeta{Name: "my-repo", Namespace: "team-a"},
		Spec: sopsv1alpha1.GitRepositorySpec{
			URL:      "https://git.example/org/repo.git",
			Branch:   "main",
			Revision: "abc123",
			Interval: metav1.Duration{Duration: 5 * time.Minute},
			Auth: &sopsv1alpha1.GitAuth{
				Type:      sopsv1alpha1.GitAuthSSH,
				SecretRef: sopsv1alpha1.LocalObjectReference{Name: "git-creds"},
			},
		},
		Status: sopsv1alpha1.GitRepositoryStatus{
			LastSyncedCommit:   "abc123def",
			CacheReady:         true,
			ObservedGeneration: 7,
			Conditions: []metav1.Condition{
				{Type: sopsv1alpha1.ConditionSourceReady, Status: metav1.ConditionTrue, Reason: "Ready"},
			},
		},
	}

	hub := &sopsv1alpha2.GitRepository{}
	if err := src.ConvertTo(hub); err != nil {
		t.Fatalf("ConvertTo: %v", err)
	}
	back := &sopsv1alpha1.GitRepository{}
	if err := back.ConvertFrom(hub); err != nil {
		t.Fatalf("ConvertFrom: %v", err)
	}
	if diff := cmp.Diff(src, back); diff != "" {
		t.Errorf("round-trip mismatch (-want +got):\n%s", diff)
	}
}

func TestSopsSecretConversion_Roundtrip(t *testing.T) {
	src := &sopsv1alpha1.SopsSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "apps"},
		Spec: sopsv1alpha1.SopsSecretSpec{
			Source: sopsv1alpha1.SourceRef{
				RepositoryRef: sopsv1alpha1.LocalObjectReference{Name: "platform-secrets"},
				Path:          "creds.enc.yaml",
			},
			Decryption: sopsv1alpha1.DecryptionSpec{
				KeyRef: sopsv1alpha1.SecretKeyRef{Name: "age-key", Key: "age.agekey"},
			},
			Target: sopsv1alpha1.MappingTarget{
				Name:      "app-creds",
				Namespace: "apps",
				Type:      corev1.SecretTypeBasicAuth,
				Adopt:     true,
			},
			Data: []sopsv1alpha1.DataMapping{
				{Key: "USERNAME", From: "username"},
				{Key: "PASSWORD", From: "password"},
			},
		},
		Status: sopsv1alpha1.SopsSecretStatus{
			LastAppliedHash:    "deadbeef",
			LastSyncedCommit:   "abc",
			ObservedGeneration: 3,
		},
	}

	hub := &sopsv1alpha2.SopsSecret{}
	if err := src.ConvertTo(hub); err != nil {
		t.Fatalf("ConvertTo: %v", err)
	}
	if got, want := hub.Spec.Source.SourceRef.Kind, sopsv1alpha2.SourceKindGitRepository; got != want {
		t.Errorf("hub source kind = %q, want %q", got, want)
	}
	if got, want := hub.Spec.Source.SourceRef.Name, "platform-secrets"; got != want {
		t.Errorf("hub source name = %q, want %q", got, want)
	}

	back := &sopsv1alpha1.SopsSecret{}
	if err := back.ConvertFrom(hub); err != nil {
		t.Fatalf("ConvertFrom: %v", err)
	}
	if diff := cmp.Diff(src, back); diff != "" {
		t.Errorf("round-trip mismatch (-want +got):\n%s", diff)
	}
}

func TestSopsSecretConversion_FromHubRejectsObjectSource(t *testing.T) {
	hub := &sopsv1alpha2.SopsSecret{
		Spec: sopsv1alpha2.SopsSecretSpec{
			Source: sopsv1alpha2.SourceRef{
				SourceRef: sopsv1alpha2.SourceKindRef{
					Kind: sopsv1alpha2.SourceKindObjectSource,
					Name: "shared-bucket",
				},
				Path: "secrets.enc.yaml",
			},
		},
	}
	dst := &sopsv1alpha1.SopsSecret{}
	if err := dst.ConvertFrom(hub); err == nil {
		t.Fatalf("expected error converting ObjectSource-kind Hub down to v1alpha1; got nil")
	}
}

func TestSopsSecretManifestConversion_Roundtrip(t *testing.T) {
	src := &sopsv1alpha1.SopsSecretManifest{
		ObjectMeta: metav1.ObjectMeta{Name: "tls", Namespace: "apps"},
		Spec: sopsv1alpha1.SopsSecretManifestSpec{
			Source: sopsv1alpha1.SourceRef{
				RepositoryRef: sopsv1alpha1.LocalObjectReference{Name: "platform-secrets"},
				Path:          "tls.enc.yaml",
			},
			Decryption: sopsv1alpha1.DecryptionSpec{
				KeyRef: sopsv1alpha1.SecretKeyRef{Name: "age-key", Key: "age.agekey"},
			},
			Target: sopsv1alpha1.ManifestTarget{
				Namespace:    "apps",
				NameOverride: "tls-override",
				Adopt:        true,
			},
		},
	}
	hub := &sopsv1alpha2.SopsSecretManifest{}
	if err := src.ConvertTo(hub); err != nil {
		t.Fatalf("ConvertTo: %v", err)
	}
	back := &sopsv1alpha1.SopsSecretManifest{}
	if err := back.ConvertFrom(hub); err != nil {
		t.Fatalf("ConvertFrom: %v", err)
	}
	if diff := cmp.Diff(src, back); diff != "" {
		t.Errorf("round-trip mismatch (-want +got):\n%s", diff)
	}
}

func TestInlineSopsSecretConversion_Roundtrip(t *testing.T) {
	src := &sopsv1alpha1.InlineSopsSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "inline", Namespace: "apps"},
		Spec: sopsv1alpha1.InlineSopsSecretSpec{
			Mode:          sopsv1alpha1.InlineModeMapping,
			EncryptedYAML: "ENC[...]",
			Decryption: sopsv1alpha1.DecryptionSpec{
				KeyRef: sopsv1alpha1.SecretKeyRef{Name: "age-key", Key: "age.agekey"},
			},
			Target: sopsv1alpha1.InlineTarget{
				Name:      "inline-secret",
				Namespace: "apps",
				Type:      corev1.SecretTypeOpaque,
			},
			Data: []sopsv1alpha1.DataMapping{{Key: "TOKEN", From: "token"}},
		},
	}
	hub := &sopsv1alpha2.InlineSopsSecret{}
	if err := src.ConvertTo(hub); err != nil {
		t.Fatalf("ConvertTo: %v", err)
	}
	back := &sopsv1alpha1.InlineSopsSecret{}
	if err := back.ConvertFrom(hub); err != nil {
		t.Fatalf("ConvertFrom: %v", err)
	}
	if diff := cmp.Diff(src, back); diff != "" {
		t.Errorf("round-trip mismatch (-want +got):\n%s", diff)
	}
}

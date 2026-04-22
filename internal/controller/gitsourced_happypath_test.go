/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	"fmt"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	sopsv1alpha1 "github.com/stuttgart-things/sops-secrets-operator/api/v1alpha1"
	"github.com/stuttgart-things/sops-secrets-operator/internal/source"
	"github.com/stuttgart-things/sops-secrets-operator/internal/testutil"
)

// gitFixture is the setup produced by newGitFixture: a prepared local
// git repo, a populated Registry, and the resource names the caller
// needs to wire their CRs up to.
type gitFixture struct {
	registry  *source.Registry
	repoCRRef string // GitRepository CR name
	keyRef    string // age-key Secret name
}

var _ = Describe("Git-sourced happy paths (envtest)", func() {
	const namespace = "default"
	var counter int
	var uniq func(prefix string) string

	BeforeEach(func() {
		counter++
		uniq = func(prefix string) string { return fmt.Sprintf("%s-%d", prefix, counter) }
	})

	// newGitFixture encrypts `plaintext` into `filePath` inside a fresh local
	// git repo, runs the GitRepository reconciler twice (finalizer add +
	// auth/fetch) to populate the shared Registry, and returns everything
	// a SopsSecret/SopsSecretManifest reconcile needs.
	newGitFixture := func(prefix, filePath string, plaintext []byte) gitFixture {
		GinkgoHelper()
		age := testutil.GenerateAge(GinkgoT())
		ct := testutil.EncryptYAML(GinkgoT(), age.PublicKey, plaintext)

		repoDir := filepath.Join(GinkgoT().TempDir(), prefix+"-repo")
		localRepo, _ := testutil.InitGitRepo(GinkgoT(), repoDir, map[string][]byte{
			filePath: ct,
		})
		branch := testutil.DetectDefaultBranch(GinkgoT(), repoDir)

		keyRef := prefix + "-age-key"
		Expect(k8sClient.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: keyRef, Namespace: namespace},
			Data:       map[string][]byte{"age.agekey": []byte(age.PrivateKey)},
		})).To(Succeed())

		repoCRName := prefix + "-repo"
		Expect(k8sClient.Create(ctx, &sopsv1alpha1.GitRepository{
			ObjectMeta: metav1.ObjectMeta{Name: repoCRName, Namespace: namespace},
			Spec: sopsv1alpha1.GitRepositorySpec{
				URL:    localRepo.URL,
				Branch: branch,
			},
		})).To(Succeed())

		registry := source.NewRegistry()
		reconr := &GitRepositoryReconciler{
			Client:   k8sClient,
			Scheme:   k8sClient.Scheme(),
			Registry: registry,
		}
		for range 2 {
			_, err := reconr.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: namespace, Name: repoCRName},
			})
			Expect(err).NotTo(HaveOccurred())
		}
		gr := &sopsv1alpha1.GitRepository{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: repoCRName}, gr)).To(Succeed())
		Expect(gr.Status.CacheReady).To(BeTrue(), "GitRepository should be ready; conditions=%+v", gr.Status.Conditions)
		srcReady := conditionByType(gr.Status.Conditions, sopsv1alpha1.ConditionSourceReady)
		Expect(srcReady).NotTo(BeNil())
		Expect(srcReady.Status).To(Equal(metav1.ConditionTrue))
		Expect(gr.Status.LastSyncedCommit).NotTo(BeEmpty())

		return gitFixture{registry: registry, repoCRRef: repoCRName, keyRef: keyRef}
	}

	Context("SopsSecret Mapping mode end-to-end", func() {
		It("materializes a Secret with exactly the mapped keys, and updates on spec.data churn", func() {
			prefix := uniq("ss-e2e")
			plain := []byte("db_user: alice\ndb_password: s3cret\napi_token: xyz\n")
			fx := newGitFixture(prefix, "creds.enc.yaml", plain)

			cr := &sopsv1alpha1.SopsSecret{
				ObjectMeta: metav1.ObjectMeta{Name: prefix, Namespace: namespace},
				Spec: sopsv1alpha1.SopsSecretSpec{
					Source: sopsv1alpha1.SourceRef{
						RepositoryRef: sopsv1alpha1.LocalObjectReference{Name: fx.repoCRRef},
						Path:          "creds.enc.yaml",
					},
					Decryption: sopsv1alpha1.DecryptionSpec{
						KeyRef: sopsv1alpha1.SecretKeyRef{Name: fx.keyRef, Key: "age.agekey"},
					},
					Data: []sopsv1alpha1.DataMapping{
						{Key: "DB_USER", From: "db_user"},
						{Key: "DB_PASSWORD", From: "db_password"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			reconr := &SopsSecretReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Registry: fx.registry,
			}
			for range 2 {
				_, err := reconr.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Namespace: namespace, Name: prefix},
				})
				Expect(err).NotTo(HaveOccurred())
			}

			target := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: prefix}, target)).To(Succeed())
			Expect(target.Data).To(HaveKey("DB_USER"))
			Expect(target.Data).To(HaveKey("DB_PASSWORD"))
			Expect(target.Data).NotTo(HaveKey("API_TOKEN"))
			Expect(string(target.Data["DB_USER"])).To(Equal("alice"))
			Expect(string(target.Data["DB_PASSWORD"])).To(Equal("s3cret"))
			Expect(target.Annotations[SourceCommitAnnotation]).NotTo(BeEmpty())

			// Remove DB_PASSWORD, add API_TOKEN → next reconcile authoritatively
			// replaces the target Secret's data.
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: prefix}, cr)).To(Succeed())
			cr.Spec.Data = []sopsv1alpha1.DataMapping{
				{Key: "DB_USER", From: "db_user"},
				{Key: "API_TOKEN", From: "api_token"},
			}
			Expect(k8sClient.Update(ctx, cr)).To(Succeed())

			_, err := reconr.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: namespace, Name: prefix},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: prefix}, target)).To(Succeed())
			Expect(target.Data).To(HaveKey("DB_USER"))
			Expect(target.Data).To(HaveKey("API_TOKEN"))
			Expect(target.Data).NotTo(HaveKey("DB_PASSWORD"))
			Expect(string(target.Data["API_TOKEN"])).To(Equal("xyz"))
		})

		It("deletes the target Secret when the CR is deleted (finalizer)", func() {
			prefix := uniq("ss-fin")
			fx := newGitFixture(prefix, "c.enc.yaml", []byte("x: 1\n"))

			cr := &sopsv1alpha1.SopsSecret{
				ObjectMeta: metav1.ObjectMeta{Name: prefix, Namespace: namespace},
				Spec: sopsv1alpha1.SopsSecretSpec{
					Source: sopsv1alpha1.SourceRef{
						RepositoryRef: sopsv1alpha1.LocalObjectReference{Name: fx.repoCRRef},
						Path:          "c.enc.yaml",
					},
					Decryption: sopsv1alpha1.DecryptionSpec{
						KeyRef: sopsv1alpha1.SecretKeyRef{Name: fx.keyRef, Key: "age.agekey"},
					},
					Data: []sopsv1alpha1.DataMapping{{Key: "X", From: "x"}},
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			reconr := &SopsSecretReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Registry: fx.registry,
			}
			for range 2 {
				_, err := reconr.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Namespace: namespace, Name: prefix},
				})
				Expect(err).NotTo(HaveOccurred())
			}
			target := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: prefix}, target)).To(Succeed())

			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: prefix}, cr)).To(Succeed())
			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			_, err := reconr.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: namespace, Name: prefix},
			})
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: prefix}, target)
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected target Secret to be deleted, got err=%v", err)
		})
	})

	Context("SopsSecretManifest end-to-end", func() {
		It("applies the decrypted Secret with authoritative namespace and nameOverride", func() {
			prefix := uniq("sm-e2e")
			manifest := []byte(`apiVersion: v1
kind: Secret
metadata:
  name: manifest-name
  namespace: should-be-ignored
type: kubernetes.io/basic-auth
stringData:
  username: alice
  password: s3cret
`)
			fx := newGitFixture(prefix, "sec.enc.yaml", manifest)

			overrideName := prefix + "-override"
			cr := &sopsv1alpha1.SopsSecretManifest{
				ObjectMeta: metav1.ObjectMeta{Name: prefix, Namespace: namespace},
				Spec: sopsv1alpha1.SopsSecretManifestSpec{
					Source: sopsv1alpha1.SourceRef{
						RepositoryRef: sopsv1alpha1.LocalObjectReference{Name: fx.repoCRRef},
						Path:          "sec.enc.yaml",
					},
					Decryption: sopsv1alpha1.DecryptionSpec{
						KeyRef: sopsv1alpha1.SecretKeyRef{Name: fx.keyRef, Key: "age.agekey"},
					},
					Target: sopsv1alpha1.ManifestTarget{
						NameOverride: overrideName,
						// namespace defaults to CR's — "default" — NOT "should-be-ignored".
					},
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			reconr := &SopsSecretManifestReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Registry: fx.registry,
			}
			for range 2 {
				_, err := reconr.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Namespace: namespace, Name: prefix},
				})
				Expect(err).NotTo(HaveOccurred())
			}

			target := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: overrideName}, target)).To(Succeed())
			Expect(target.Namespace).To(Equal(namespace))
			Expect(target.Type).To(Equal(corev1.SecretType("kubernetes.io/basic-auth")))
			Expect(string(target.Data["username"])).To(Equal("alice"))
			Expect(string(target.Data["password"])).To(Equal("s3cret"))

			// The manifest's metadata.name was "manifest-name" — nameOverride
			// should win, so no Secret with that name should exist in the
			// CR's namespace. (envtest typically lacks a controller for
			// namespace creation; we skip checking the manifest's claimed
			// namespace, which `should-be-ignored` doesn't exist.)
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "manifest-name"}, &corev1.Secret{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})
})

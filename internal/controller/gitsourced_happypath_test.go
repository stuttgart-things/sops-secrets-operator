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
	sopsv1alpha2 "github.com/stuttgart-things/sops-secrets-operator/api/v1alpha2"
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

// gitSourceRef is shorthand for the v1alpha2 SourceRef pointing at a
// GitRepository — used to keep the per-test SopsSecret/SopsSecretManifest
// stanzas readable.
func gitSourceRef(name, path string) sopsv1alpha2.SourceRef {
	return sopsv1alpha2.SourceRef{
		SourceRef: sopsv1alpha2.SourceKindRef{
			Kind: sopsv1alpha2.SourceKindGitRepository,
			Name: name,
		},
		Path: path,
	}
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

			cr := &sopsv1alpha2.SopsSecret{
				ObjectMeta: metav1.ObjectMeta{Name: prefix, Namespace: namespace},
				Spec: sopsv1alpha2.SopsSecretSpec{
					Source: gitSourceRef(fx.repoCRRef, "creds.enc.yaml"),
					Decryption: sopsv1alpha2.DecryptionSpec{
						KeyRef: sopsv1alpha2.SecretKeyRef{Name: fx.keyRef, Key: "age.agekey"},
					},
					Data: []sopsv1alpha2.DataMapping{
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

			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: prefix}, cr)).To(Succeed())
			cr.Spec.Data = []sopsv1alpha2.DataMapping{
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

		It("refuses a pre-existing un-owned Secret, then adopts it when target.adopt=true", func() {
			prefix := uniq("ss-adopt")
			plain := []byte("db_user: alice\ndb_password: s3cret\n")
			fx := newGitFixture(prefix, "adopt.enc.yaml", plain)

			pre := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:        prefix,
					Namespace:   namespace,
					Labels:      map[string]string{"app": "legacy"},
					Annotations: map[string]string{"note": "hand-crafted"},
				},
				Data: map[string][]byte{
					"DB_USER":     []byte("stale-user"),
					"LEGACY_ONLY": []byte("keep-me"),
				},
			}
			Expect(k8sClient.Create(ctx, pre)).To(Succeed())

			cr := &sopsv1alpha2.SopsSecret{
				ObjectMeta: metav1.ObjectMeta{Name: prefix, Namespace: namespace},
				Spec: sopsv1alpha2.SopsSecretSpec{
					Source: gitSourceRef(fx.repoCRRef, "adopt.enc.yaml"),
					Decryption: sopsv1alpha2.DecryptionSpec{
						KeyRef: sopsv1alpha2.SecretKeyRef{Name: fx.keyRef, Key: "age.agekey"},
					},
					Data: []sopsv1alpha2.DataMapping{
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

			got := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: prefix}, got)).To(Succeed())
			Expect(got.Labels).NotTo(HaveKey(ManagedByLabel))
			Expect(got.Annotations).NotTo(HaveKey(OwnerAnnotation))
			Expect(string(got.Data["DB_USER"])).To(Equal("stale-user"))
			Expect(got.Data).To(HaveKey("LEGACY_ONLY"))

			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: prefix}, cr)).To(Succeed())
			applied := conditionByType(cr.Status.Conditions, sopsv1alpha2.ConditionApplied)
			Expect(applied).NotTo(BeNil())
			Expect(applied.Status).To(Equal(metav1.ConditionFalse))
			Expect(applied.Reason).To(Equal("ApplyFailed"))

			cr.Spec.Target.Adopt = true
			Expect(k8sClient.Update(ctx, cr)).To(Succeed())

			_, err := reconr.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: namespace, Name: prefix},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: prefix}, got)).To(Succeed())
			Expect(got.Labels[ManagedByLabel]).To(Equal(ManagedByValue))
			Expect(got.Annotations[OwnerAnnotation]).To(Equal(fmt.Sprintf("SopsSecret/%s/%s", namespace, prefix)))
			Expect(got.Annotations[ContentHashAnnotation]).NotTo(BeEmpty())
			Expect(string(got.Data["DB_USER"])).To(Equal("alice"))
			Expect(string(got.Data["DB_PASSWORD"])).To(Equal("s3cret"))
			Expect(got.Data).NotTo(HaveKey("LEGACY_ONLY"))
		})

		It("reverts drift when the target Secret is edited out of band", func() {
			prefix := uniq("ss-drift")
			plain := []byte("token: correct-horse\n")
			fx := newGitFixture(prefix, "drift.enc.yaml", plain)

			cr := &sopsv1alpha2.SopsSecret{
				ObjectMeta: metav1.ObjectMeta{Name: prefix, Namespace: namespace},
				Spec: sopsv1alpha2.SopsSecretSpec{
					Source: gitSourceRef(fx.repoCRRef, "drift.enc.yaml"),
					Decryption: sopsv1alpha2.DecryptionSpec{
						KeyRef: sopsv1alpha2.SecretKeyRef{Name: fx.keyRef, Key: "age.agekey"},
					},
					Data: []sopsv1alpha2.DataMapping{{Key: "TOKEN", From: "token"}},
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
			key := types.NamespacedName{Namespace: namespace, Name: prefix}
			Expect(k8sClient.Get(ctx, key, target)).To(Succeed())
			Expect(string(target.Data["TOKEN"])).To(Equal("correct-horse"))

			target.Data["TOKEN"] = []byte("tampered")
			target.Data["ROGUE"] = []byte("injected")
			Expect(k8sClient.Update(ctx, target)).To(Succeed())

			_, err := reconr.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, key, target)).To(Succeed())
			Expect(string(target.Data["TOKEN"])).To(Equal("correct-horse"))
			Expect(target.Data).NotTo(HaveKey("ROGUE"))
		})

		It("deletes the target Secret when the CR is deleted (finalizer)", func() {
			prefix := uniq("ss-fin")
			fx := newGitFixture(prefix, "c.enc.yaml", []byte("x: 1\n"))

			cr := &sopsv1alpha2.SopsSecret{
				ObjectMeta: metav1.ObjectMeta{Name: prefix, Namespace: namespace},
				Spec: sopsv1alpha2.SopsSecretSpec{
					Source: gitSourceRef(fx.repoCRRef, "c.enc.yaml"),
					Decryption: sopsv1alpha2.DecryptionSpec{
						KeyRef: sopsv1alpha2.SecretKeyRef{Name: fx.keyRef, Key: "age.agekey"},
					},
					Data: []sopsv1alpha2.DataMapping{{Key: "X", From: "x"}},
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
			cr := &sopsv1alpha2.SopsSecretManifest{
				ObjectMeta: metav1.ObjectMeta{Name: prefix, Namespace: namespace},
				Spec: sopsv1alpha2.SopsSecretManifestSpec{
					Source: gitSourceRef(fx.repoCRRef, "sec.enc.yaml"),
					Decryption: sopsv1alpha2.DecryptionSpec{
						KeyRef: sopsv1alpha2.SecretKeyRef{Name: fx.keyRef, Key: "age.agekey"},
					},
					Target: sopsv1alpha2.ManifestTarget{
						NameOverride: overrideName,
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

			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "manifest-name"}, &corev1.Secret{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})
})

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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	sopsv1alpha1 "github.com/stuttgart-things/sops-secrets-operator/api/v1alpha1"
	"github.com/stuttgart-things/sops-secrets-operator/internal/secretref"
	"github.com/stuttgart-things/sops-secrets-operator/internal/source"
	"github.com/stuttgart-things/sops-secrets-operator/internal/testutil"
)

// These specs cover the wiring, not the resolver: secretref's own unit
// tests decide precedence, and this file checks that the reconcilers
// actually consult it and report what they used. See #47 and #48.
var _ = Describe("Shared credentials (envtest)", func() {
	const namespace = "default"
	const otherNamespace = "platform-creds"

	var (
		counter int
		uniq    func(prefix string) string
	)

	BeforeEach(func() {
		counter++
		uniq = func(prefix string) string { return fmt.Sprintf("%s-%d", prefix, counter) }

		// A shared-credentials namespace, created once and left in place.
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: otherNamespace}}
		err := k8sClient.Create(ctx, ns)
		if err != nil {
			Expect(err.Error()).To(ContainSubstring("already exists"))
		}
	})

	condition := func(conds []metav1.Condition, t string) *metav1.Condition {
		for i := range conds {
			if conds[i].Type == t {
				return &conds[i]
			}
		}
		return nil
	}

	Describe("global age key (#47)", func() {
		// InlineSopsSecret needs no git source, so it isolates the key
		// resolution from everything else.
		reconcileInline := func(r *InlineSopsSecretReconciler, name string) *sopsv1alpha1.InlineSopsSecret {
			for range 2 {
				_, err := r.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Namespace: namespace, Name: name},
				})
				Expect(err).NotTo(HaveOccurred())
			}
			out := &sopsv1alpha1.InlineSopsSecret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, out)).To(Succeed())
			return out
		}

		It("decrypts a resource that omits decryption.keyRef", func() {
			prefix := uniq("global-age")

			// The shared key lives in the operator's namespace, and
			// nothing in the CR's namespace holds a copy.
			key := testutil.GenerateAge(GinkgoT())
			globalSecret := prefix + "-shared-age"
			Expect(k8sClient.Create(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: globalSecret, Namespace: otherNamespace},
				Data:       map[string][]byte{"age.agekey": []byte(key.PrivateKey)},
			})).To(Succeed())

			ct := testutil.EncryptYAML(GinkgoT(), key.PublicKey, []byte("db_password: s3cret\n"))

			cr := &sopsv1alpha1.InlineSopsSecret{
				ObjectMeta: metav1.ObjectMeta{Name: prefix, Namespace: namespace},
				Spec: sopsv1alpha1.InlineSopsSecretSpec{
					Mode:          sopsv1alpha1.InlineModeMapping,
					EncryptedYAML: string(ct),
					// No Decryption block at all.
					Target: sopsv1alpha1.InlineTarget{Name: prefix + "-out"},
					Data:   []sopsv1alpha1.DataMapping{{Key: "PASSWORD", From: "db_password"}},
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			r := &InlineSopsSecretReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				CredentialPolicy: CredentialPolicy{
					GlobalAgeKey: &secretref.Global{
						Namespace: otherNamespace,
						Name:      globalSecret,
						Key:       "age.agekey",
					},
				},
			}

			got := reconcileInline(r, prefix)
			dec := condition(got.Status.Conditions, sopsv1alpha1.ConditionDecrypted)
			Expect(dec).NotTo(BeNil())
			Expect(dec.Status).To(Equal(metav1.ConditionTrue))
			// The whole point of #47's observability note: a fallback must
			// be visible, not silent.
			Expect(dec.Message).To(ContainSubstring("global age key"))

			out := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: prefix + "-out"}, out)).To(Succeed())
			Expect(out.Data).To(HaveKeyWithValue("PASSWORD", []byte("s3cret")))
		})

		It("fails a resource that omits keyRef when no global key is configured", func() {
			prefix := uniq("no-age")
			key := testutil.GenerateAge(GinkgoT())
			ct := testutil.EncryptYAML(GinkgoT(), key.PublicKey, []byte("db_password: s3cret\n"))

			Expect(k8sClient.Create(ctx, &sopsv1alpha1.InlineSopsSecret{
				ObjectMeta: metav1.ObjectMeta{Name: prefix, Namespace: namespace},
				Spec: sopsv1alpha1.InlineSopsSecretSpec{
					Mode:          sopsv1alpha1.InlineModeMapping,
					EncryptedYAML: string(ct),
					Target:        sopsv1alpha1.InlineTarget{Name: prefix + "-out"},
					Data:          []sopsv1alpha1.DataMapping{{Key: "PASSWORD", From: "db_password"}},
				},
			})).To(Succeed())

			r := &InlineSopsSecretReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
			got := reconcileInline(r, prefix)

			dec := condition(got.Status.Conditions, sopsv1alpha1.ConditionDecrypted)
			Expect(dec).NotTo(BeNil())
			Expect(dec.Status).To(Equal(metav1.ConditionFalse))
			Expect(dec.Message).To(ContainSubstring("no operator-level default"))
		})
	})

	Describe("git auth (#48)", func() {
		reconcileGit := func(r *GitRepositoryReconciler, name string) *sopsv1alpha1.GitRepository {
			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: namespace, Name: name},
			})
			Expect(err).NotTo(HaveOccurred())
			out := &sopsv1alpha1.GitRepository{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, out)).To(Succeed())
			return out
		}

		// createBasicAuthSecret puts a usable basic-auth credential in the
		// shared-credentials namespace.
		createBasicAuthSecret := func(name string) {
			Expect(k8sClient.Create(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: otherNamespace},
				Data: map[string][]byte{
					"username": []byte("git"),
					"password": []byte("token"),
				},
			})).To(Succeed())
		}

		// createRepo makes a GitRepository whose auth block is supplied by
		// the caller. The URL is unreachable on purpose — these specs
		// assert on AuthResolved, which is set before any fetch.
		createRepo := func(name string, auth *sopsv1alpha1.GitAuth) {
			Expect(k8sClient.Create(ctx, &sopsv1alpha1.GitRepository{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec: sopsv1alpha1.GitRepositorySpec{
					URL:    "https://example.invalid/repo.git",
					Branch: "main",
					Auth:   auth,
				},
			})).To(Succeed())
		}

		newReconciler := func(p CredentialPolicy) *GitRepositoryReconciler {
			return &GitRepositoryReconciler{
				Client:           k8sClient,
				Scheme:           k8sClient.Scheme(),
				Registry:         source.NewRegistry(),
				CredentialPolicy: p,
			}
		}

		It("uses the global credential when auth omits secretRef", func() {
			prefix := uniq("gr-global-auth")
			createBasicAuthSecret(prefix + "-creds")
			createRepo(prefix, &sopsv1alpha1.GitAuth{Type: sopsv1alpha1.GitAuthBasic})

			got := reconcileGit(newReconciler(CredentialPolicy{
				GlobalGitAuth: &secretref.Global{Namespace: otherNamespace, Name: prefix + "-creds"},
			}), prefix)

			auth := condition(got.Status.Conditions, sopsv1alpha1.ConditionAuthResolved)
			Expect(auth).NotTo(BeNil())
			Expect(auth.Status).To(Equal(metav1.ConditionTrue))
			Expect(auth.Message).To(ContainSubstring("global credential"))
		})

		It("fails when auth omits secretRef and no global credential is configured", func() {
			prefix := uniq("gr-no-auth")
			createRepo(prefix, &sopsv1alpha1.GitAuth{Type: sopsv1alpha1.GitAuthBasic})

			got := reconcileGit(newReconciler(CredentialPolicy{}), prefix)

			auth := condition(got.Status.Conditions, sopsv1alpha1.ConditionAuthResolved)
			Expect(auth).NotTo(BeNil())
			Expect(auth.Status).To(Equal(metav1.ConditionFalse))
			Expect(auth.Message).To(ContainSubstring("--global-git-auth-secret"))
		})

		It("still clones unauthenticated when spec.auth is absent entirely", func() {
			// Regression guard: the global default must not turn a
			// deliberately anonymous clone into an authenticated one.
			prefix := uniq("gr-anon")
			createBasicAuthSecret(prefix + "-creds")
			createRepo(prefix, nil)

			got := reconcileGit(newReconciler(CredentialPolicy{
				GlobalGitAuth: &secretref.Global{Namespace: otherNamespace, Name: prefix + "-creds"},
			}), prefix)

			auth := condition(got.Status.Conditions, sopsv1alpha1.ConditionAuthResolved)
			Expect(auth).NotTo(BeNil())
			Expect(auth.Status).To(Equal(metav1.ConditionTrue))
			Expect(auth.Message).To(ContainSubstring("no authentication"))
		})

		It("refuses a cross-namespace secretRef by default", func() {
			prefix := uniq("gr-xns-denied")
			createBasicAuthSecret(prefix + "-creds")
			createRepo(prefix, &sopsv1alpha1.GitAuth{
				Type: sopsv1alpha1.GitAuthBasic,
				SecretRef: &sopsv1alpha1.SecretReference{
					Name:      prefix + "-creds",
					Namespace: otherNamespace,
				},
			})

			got := reconcileGit(newReconciler(CredentialPolicy{}), prefix)

			auth := condition(got.Status.Conditions, sopsv1alpha1.ConditionAuthResolved)
			Expect(auth).NotTo(BeNil())
			Expect(auth.Status).To(Equal(metav1.ConditionFalse))
			Expect(auth.Message).To(ContainSubstring("does not permit"))
		})

		It("allows a cross-namespace secretRef into a permitted namespace", func() {
			prefix := uniq("gr-xns-allowed")
			createBasicAuthSecret(prefix + "-creds")
			createRepo(prefix, &sopsv1alpha1.GitAuth{
				Type: sopsv1alpha1.GitAuthBasic,
				SecretRef: &sopsv1alpha1.SecretReference{
					Name:      prefix + "-creds",
					Namespace: otherNamespace,
				},
			})

			got := reconcileGit(newReconciler(CredentialPolicy{
				SecretRefs: secretref.NewResolver([]string{otherNamespace}),
			}), prefix)

			auth := condition(got.Status.Conditions, sopsv1alpha1.ConditionAuthResolved)
			Expect(auth).NotTo(BeNil())
			Expect(auth.Status).To(Equal(metav1.ConditionTrue))
			Expect(auth.Message).To(ContainSubstring("another namespace"))
		})
	})
})

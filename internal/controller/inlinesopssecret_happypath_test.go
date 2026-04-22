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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	sopsv1alpha1 "github.com/stuttgart-things/sops-secrets-operator/api/v1alpha1"
	"github.com/stuttgart-things/sops-secrets-operator/internal/testutil"
)

var _ = Describe("InlineSopsSecret happy path (envtest)", func() {
	const namespace = "default"
	var (
		counter int
		uniq    func(prefix string) string
		reconr  *InlineSopsSecretReconciler
	)

	BeforeEach(func() {
		counter++
		uniq = func(prefix string) string { return fmt.Sprintf("%s-%d", prefix, counter) }
		reconr = &InlineSopsSecretReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
	})

	// runReconcile runs reconcile until both finalizer-add + main work complete,
	// returning the final CR state. Two passes are enough: first adds the
	// finalizer + requeue, second does the work.
	runReconcile := func(name string) *sopsv1alpha1.InlineSopsSecret {
		for range 2 {
			_, err := reconr.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: namespace, Name: name},
			})
			Expect(err).NotTo(HaveOccurred())
		}
		out := &sopsv1alpha1.InlineSopsSecret{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, out)).To(Succeed())
		return out
	}

	// setupAgeKeySecret creates a Secret holding a fresh age private key in
	// `namespace` and returns (secretName, publicKey). Each spec gets its own
	// key Secret to avoid cross-spec interference.
	setupAgeKeySecret := func(namePrefix string) (string, string) {
		key := testutil.GenerateAge(GinkgoT())
		secName := namePrefix + "-age-key"
		Expect(k8sClient.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: secName, Namespace: namespace},
			Data:       map[string][]byte{"age.agekey": []byte(key.PrivateKey)},
		})).To(Succeed())
		return secName, key.PublicKey
	}

	Context("Mapping mode", func() {
		It("materializes a Secret with exactly the mapped keys", func() {
			prefix := uniq("is-map")
			keySecret, agePub := setupAgeKeySecret(prefix)

			ct := testutil.EncryptYAML(GinkgoT(), agePub, []byte(
				"db_password: s3cret\ndb_user: alice\napi_token: xyz\n",
			))

			cr := &sopsv1alpha1.InlineSopsSecret{
				ObjectMeta: metav1.ObjectMeta{Name: prefix, Namespace: namespace},
				Spec: sopsv1alpha1.InlineSopsSecretSpec{
					Mode:          sopsv1alpha1.InlineModeMapping,
					EncryptedYAML: string(ct),
					Decryption: sopsv1alpha1.DecryptionSpec{
						KeyRef: sopsv1alpha1.SecretKeyRef{Name: keySecret, Key: "age.agekey"},
					},
					Data: []sopsv1alpha1.DataMapping{
						{Key: "DB_USER", From: "db_user"},
						{Key: "DB_PASSWORD", From: "db_password"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			got := runReconcile(prefix)
			decrypted := conditionByType(got.Status.Conditions, sopsv1alpha1.ConditionDecrypted)
			Expect(decrypted).NotTo(BeNil())
			Expect(decrypted.Status).To(Equal(metav1.ConditionTrue))
			applied := conditionByType(got.Status.Conditions, sopsv1alpha1.ConditionApplied)
			Expect(applied).NotTo(BeNil())
			Expect(applied.Status).To(Equal(metav1.ConditionTrue))

			target := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: prefix}, target)).To(Succeed())
			Expect(target.Data).To(HaveKey("DB_USER"))
			Expect(target.Data).To(HaveKey("DB_PASSWORD"))
			// api_token was NOT mapped — must be absent (authoritative data).
			Expect(target.Data).NotTo(HaveKey("API_TOKEN"))
			Expect(target.Data).NotTo(HaveKey("api_token"))
			Expect(string(target.Data["DB_USER"])).To(Equal("alice"))
			Expect(string(target.Data["DB_PASSWORD"])).To(Equal("s3cret"))
			Expect(target.Labels).To(HaveKeyWithValue(ManagedByLabel, ManagedByValue))
			Expect(target.Annotations[OwnerAnnotation]).To(Equal(fmt.Sprintf("InlineSopsSecret/%s/%s", namespace, prefix)))
			Expect(target.Annotations[ContentHashAnnotation]).NotTo(BeEmpty())
		})

		It("reverts manual drift on the next reconcile", func() {
			prefix := uniq("is-drift")
			keySecret, agePub := setupAgeKeySecret(prefix)
			ct := testutil.EncryptYAML(GinkgoT(), agePub, []byte("token: original\n"))

			Expect(k8sClient.Create(ctx, &sopsv1alpha1.InlineSopsSecret{
				ObjectMeta: metav1.ObjectMeta{Name: prefix, Namespace: namespace},
				Spec: sopsv1alpha1.InlineSopsSecretSpec{
					Mode:          sopsv1alpha1.InlineModeMapping,
					EncryptedYAML: string(ct),
					Decryption: sopsv1alpha1.DecryptionSpec{
						KeyRef: sopsv1alpha1.SecretKeyRef{Name: keySecret, Key: "age.agekey"},
					},
					Data: []sopsv1alpha1.DataMapping{{Key: "TOKEN", From: "token"}},
				},
			})).To(Succeed())
			_ = runReconcile(prefix)

			// Hand-edit the target Secret: mutate value, add rogue key.
			target := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: prefix}, target)).To(Succeed())
			target.Data["TOKEN"] = []byte("tampered")
			target.Data["ROGUE"] = []byte("evil")
			Expect(k8sClient.Update(ctx, target)).To(Succeed())

			// Next reconcile must revert.
			_ = runReconcile(prefix)
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: prefix}, target)).To(Succeed())
			Expect(string(target.Data["TOKEN"])).To(Equal("original"))
			Expect(target.Data).NotTo(HaveKey("ROGUE"))
		})

		It("refuses to adopt an unmanaged pre-existing Secret, accepts with adopt=true", func() {
			prefix := uniq("is-adopt")
			keySecret, agePub := setupAgeKeySecret(prefix)
			ct := testutil.EncryptYAML(GinkgoT(), agePub, []byte("token: from-sops\n"))

			// Pre-existing unmanaged Secret that collides with the target.
			Expect(k8sClient.Create(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: prefix, Namespace: namespace},
				Data:       map[string][]byte{"EXISTING": []byte("existing-val")},
			})).To(Succeed())

			cr := &sopsv1alpha1.InlineSopsSecret{
				ObjectMeta: metav1.ObjectMeta{Name: prefix, Namespace: namespace},
				Spec: sopsv1alpha1.InlineSopsSecretSpec{
					Mode:          sopsv1alpha1.InlineModeMapping,
					EncryptedYAML: string(ct),
					Decryption: sopsv1alpha1.DecryptionSpec{
						KeyRef: sopsv1alpha1.SecretKeyRef{Name: keySecret, Key: "age.agekey"},
					},
					Data: []sopsv1alpha1.DataMapping{{Key: "TOKEN", From: "token"}},
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())
			got := runReconcile(prefix)
			applied := conditionByType(got.Status.Conditions, sopsv1alpha1.ConditionApplied)
			Expect(applied).NotTo(BeNil())
			Expect(applied.Status).To(Equal(metav1.ConditionFalse))
			Expect(applied.Reason).To(Equal("ApplyFailed"))
			Expect(applied.Message).To(ContainSubstring("target.adopt=true"))

			// Enable adoption and re-reconcile.
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: prefix}, cr)).To(Succeed())
			cr.Spec.Target.Adopt = true
			Expect(k8sClient.Update(ctx, cr)).To(Succeed())

			got = runReconcile(prefix)
			applied = conditionByType(got.Status.Conditions, sopsv1alpha1.ConditionApplied)
			Expect(applied).NotTo(BeNil())
			Expect(applied.Status).To(Equal(metav1.ConditionTrue))

			target := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: prefix}, target)).To(Succeed())
			// Adopted Secret: data is authoritative from CR, so EXISTING is gone.
			Expect(target.Data).NotTo(HaveKey("EXISTING"))
			Expect(string(target.Data["TOKEN"])).To(Equal("from-sops"))
			Expect(target.Labels).To(HaveKeyWithValue(ManagedByLabel, ManagedByValue))
		})

		It("deletes the target Secret when the CR is deleted", func() {
			prefix := uniq("is-fin")
			keySecret, agePub := setupAgeKeySecret(prefix)
			ct := testutil.EncryptYAML(GinkgoT(), agePub, []byte("token: x\n"))

			Expect(k8sClient.Create(ctx, &sopsv1alpha1.InlineSopsSecret{
				ObjectMeta: metav1.ObjectMeta{Name: prefix, Namespace: namespace},
				Spec: sopsv1alpha1.InlineSopsSecretSpec{
					Mode:          sopsv1alpha1.InlineModeMapping,
					EncryptedYAML: string(ct),
					Decryption: sopsv1alpha1.DecryptionSpec{
						KeyRef: sopsv1alpha1.SecretKeyRef{Name: keySecret, Key: "age.agekey"},
					},
					Data: []sopsv1alpha1.DataMapping{{Key: "TOKEN", From: "token"}},
				},
			})).To(Succeed())
			_ = runReconcile(prefix)

			target := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: prefix}, target)).To(Succeed())

			cr := &sopsv1alpha1.InlineSopsSecret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: prefix}, cr)).To(Succeed())
			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			// One more reconcile processes the finalizer.
			_, err := reconr.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: namespace, Name: prefix},
			})
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: prefix}, target)
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "target Secret should be deleted; got err=%v", err)
		})
	})

	Context("Manifest mode", func() {
		It("applies the decrypted Secret with authoritative namespace and type from manifest", func() {
			prefix := uniq("is-mf")
			keySecret, agePub := setupAgeKeySecret(prefix)

			manifest := []byte(`apiVersion: v1
kind: Secret
metadata:
  name: unused-name
  namespace: some-other-ns
type: kubernetes.io/basic-auth
stringData:
  username: alice
  password: s3cret
`)
			ct := testutil.EncryptYAML(GinkgoT(), agePub, manifest)

			targetName := prefix + "-target"
			cr := &sopsv1alpha1.InlineSopsSecret{
				ObjectMeta: metav1.ObjectMeta{Name: prefix, Namespace: namespace},
				Spec: sopsv1alpha1.InlineSopsSecretSpec{
					Mode:          sopsv1alpha1.InlineModeManifest,
					EncryptedYAML: string(ct),
					Decryption: sopsv1alpha1.DecryptionSpec{
						KeyRef: sopsv1alpha1.SecretKeyRef{Name: keySecret, Key: "age.agekey"},
					},
					Target: sopsv1alpha1.InlineTarget{
						Name: targetName, // override manifest name
						// namespace defaults to the CR namespace (authoritative),
						// NOT manifest's `some-other-ns`.
					},
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			got := runReconcile(prefix)
			applied := conditionByType(got.Status.Conditions, sopsv1alpha1.ConditionApplied)
			Expect(applied).NotTo(BeNil())
			Expect(applied.Status).To(Equal(metav1.ConditionTrue))

			target := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: targetName}, target)).To(Succeed())
			Expect(target.Type).To(Equal(corev1.SecretType("kubernetes.io/basic-auth")))
			Expect(string(target.Data["username"])).To(Equal("alice"))
			Expect(string(target.Data["password"])).To(Equal("s3cret"))

			// The manifest's `some-other-ns` is not where the Secret landed:
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: "some-other-ns", Name: targetName}, &corev1.Secret{})
			Expect(apierrors.IsNotFound(err) || err != nil).To(BeTrue())
		})
	})
})

// conditionByType is a small free helper that a few happy-path specs use.
// Named separately from the pointer-receiver `condition` helpers in the
// older test files so we don't collide with them.
func conditionByType(conds []metav1.Condition, t string) *metav1.Condition {
	for i := range conds {
		if conds[i].Type == t {
			return &conds[i]
		}
	}
	return nil
}

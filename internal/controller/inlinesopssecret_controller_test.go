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

package controller

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	sopsv1alpha1 "github.com/stuttgart-things/sops-secrets-operator/api/v1alpha1"
)

var _ = Describe("InlineSopsSecret Controller", func() {
	const namespace = "default"
	var counter int
	var uniqueName func(prefix string) string

	BeforeEach(func() {
		counter++
		uniqueName = func(prefix string) string { return fmt.Sprintf("%s-%d", prefix, counter) }
	})

	makeMapping := func(name string) *sopsv1alpha1.InlineSopsSecret {
		return &sopsv1alpha1.InlineSopsSecret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec: sopsv1alpha1.InlineSopsSecretSpec{
				Mode:          sopsv1alpha1.InlineModeMapping,
				EncryptedYAML: "not-real-ciphertext",
				Decryption: sopsv1alpha1.DecryptionSpec{
					KeyRef: sopsv1alpha1.SecretKeyRef{Name: "age-key", Key: "age.agekey"},
				},
				Data: []sopsv1alpha1.DataMapping{{Key: "K", From: "k"}},
			},
		}
	}

	reconcileOnce := func(name string) *sopsv1alpha1.InlineSopsSecret {
		r := &InlineSopsSecretReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		_, err := r.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Namespace: namespace, Name: name},
		})
		Expect(err).NotTo(HaveOccurred())

		out := &sopsv1alpha1.InlineSopsSecret{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, out)).To(Succeed())
		return out
	}

	condition := func(is *sopsv1alpha1.InlineSopsSecret, t string) *metav1.Condition {
		for i := range is.Status.Conditions {
			if is.Status.Conditions[i].Type == t {
				return &is.Status.Conditions[i]
			}
		}
		return nil
	}

	Context("first reconcile", func() {
		It("adds the finalizer and requeues", func() {
			name := uniqueName("is-finalizer")
			Expect(k8sClient.Create(ctx, makeMapping(name))).To(Succeed())

			_, _ = (&InlineSopsSecretReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}).Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: namespace, Name: name},
			})

			got := &sopsv1alpha1.InlineSopsSecret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, got)).To(Succeed())
			Expect(got.Finalizers).To(ContainElement(Finalizer))

			Expect(k8sClient.Delete(ctx, got)).To(Succeed())
		})
	})

	Context("when the age-key Secret is missing", func() {
		It("sets Decrypted=False with reason KeyResolveFailed", func() {
			name := uniqueName("is-no-key")
			Expect(k8sClient.Create(ctx, makeMapping(name))).To(Succeed())

			_ = reconcileOnce(name)    // finalizer
			got := reconcileOnce(name) // runs the decrypt path

			c := condition(got, sopsv1alpha1.ConditionDecrypted)
			Expect(c).NotTo(BeNil())
			Expect(c.Status).To(Equal(metav1.ConditionFalse))
			Expect(c.Reason).To(Equal("KeyResolveFailed"))
		})
	})

	Context("CRD schema validation", func() {
		It("rejects mode=Mapping with empty data", func() {
			name := uniqueName("is-bad-mapping")
			bad := &sopsv1alpha1.InlineSopsSecret{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec: sopsv1alpha1.InlineSopsSecretSpec{
					Mode:          sopsv1alpha1.InlineModeMapping,
					EncryptedYAML: "x",
					Decryption: sopsv1alpha1.DecryptionSpec{
						KeyRef: sopsv1alpha1.SecretKeyRef{Name: "k", Key: "age"},
					},
					Data: []sopsv1alpha1.DataMapping{},
				},
			}
			Expect(k8sClient.Create(ctx, bad)).NotTo(Succeed())
		})

		It("rejects mode=Manifest with non-empty data", func() {
			name := uniqueName("is-bad-manifest")
			bad := &sopsv1alpha1.InlineSopsSecret{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec: sopsv1alpha1.InlineSopsSecretSpec{
					Mode:          sopsv1alpha1.InlineModeManifest,
					EncryptedYAML: "x",
					Decryption: sopsv1alpha1.DecryptionSpec{
						KeyRef: sopsv1alpha1.SecretKeyRef{Name: "k", Key: "age"},
					},
					Data: []sopsv1alpha1.DataMapping{{Key: "K", From: "k"}},
				},
			}
			Expect(k8sClient.Create(ctx, bad)).NotTo(Succeed())
		})

		It("rejects empty encryptedYAML", func() {
			name := uniqueName("is-empty-ct")
			bad := &sopsv1alpha1.InlineSopsSecret{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec: sopsv1alpha1.InlineSopsSecretSpec{
					Mode:          sopsv1alpha1.InlineModeManifest,
					EncryptedYAML: "",
					Decryption: sopsv1alpha1.DecryptionSpec{
						KeyRef: sopsv1alpha1.SecretKeyRef{Name: "k", Key: "age"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, bad)).NotTo(Succeed())
		})
	})
})

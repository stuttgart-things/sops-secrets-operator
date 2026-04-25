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
	sopsv1alpha2 "github.com/stuttgart-things/sops-secrets-operator/api/v1alpha2"
	"github.com/stuttgart-things/sops-secrets-operator/internal/source"
)

var _ = Describe("SopsSecret Controller", func() {
	const namespace = "default"
	var counter int
	var uniqueName func(prefix string) string

	BeforeEach(func() {
		counter++
		uniqueName = func(prefix string) string { return fmt.Sprintf("%s-%d", prefix, counter) }
	})

	makeCR := func(name, repoRef string) *sopsv1alpha2.SopsSecret {
		return &sopsv1alpha2.SopsSecret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec: sopsv1alpha2.SopsSecretSpec{
				Source: sopsv1alpha2.SourceRef{
					SourceRef: sopsv1alpha2.SourceKindRef{
						Kind: sopsv1alpha2.SourceKindGitRepository,
						Name: repoRef,
					},
					Path: "secrets.enc.yaml",
				},
				Decryption: sopsv1alpha2.DecryptionSpec{
					KeyRef: sopsv1alpha2.SecretKeyRef{Name: "age-key", Key: "age.agekey"},
				},
				Data: []sopsv1alpha2.DataMapping{{Key: "DB_PASSWORD", From: "db_password"}},
			},
		}
	}

	reconcileOnce := func(name string) *sopsv1alpha2.SopsSecret {
		r := &SopsSecretReconciler{
			Client:   k8sClient,
			Scheme:   k8sClient.Scheme(),
			Registry: source.NewRegistry(),
		}
		_, err := r.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Namespace: namespace, Name: name},
		})
		Expect(err).NotTo(HaveOccurred())

		out := &sopsv1alpha2.SopsSecret{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, out)).To(Succeed())
		return out
	}

	condition := func(ss *sopsv1alpha2.SopsSecret, t string) *metav1.Condition {
		for i := range ss.Status.Conditions {
			if ss.Status.Conditions[i].Type == t {
				return &ss.Status.Conditions[i]
			}
		}
		return nil
	}

	Context("first reconcile", func() {
		It("adds the finalizer and requeues", func() {
			name := uniqueName("ss-finalizer")
			Expect(k8sClient.Create(ctx, makeCR(name, "nonexistent"))).To(Succeed())

			_, _ = (&SopsSecretReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Registry: source.NewRegistry(),
			}).Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: namespace, Name: name},
			})

			got := &sopsv1alpha2.SopsSecret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, got)).To(Succeed())
			Expect(got.Finalizers).To(ContainElement(Finalizer))

			Expect(k8sClient.Delete(ctx, got)).To(Succeed())
		})
	})

	Context("when the referenced GitRepository is missing", func() {
		It("sets SourceReady=False with reason SourceMissing", func() {
			name := uniqueName("ss-no-repo")
			Expect(k8sClient.Create(ctx, makeCR(name, "missing-repo"))).To(Succeed())

			_ = reconcileOnce(name)
			got := reconcileOnce(name)

			c := condition(got, sopsv1alpha2.ConditionSourceReady)
			Expect(c).NotTo(BeNil())
			Expect(c.Status).To(Equal(metav1.ConditionFalse))
			Expect(c.Reason).To(Equal("SourceMissing"))
		})
	})

	Context("when the GitRepository exists but is not Ready", func() {
		It("sets SourceReady=False with reason SourceNotReady", func() {
			name := uniqueName("ss-repo-not-ready")
			repoName := name + "-repo"

			Expect(k8sClient.Create(ctx, &sopsv1alpha1.GitRepository{
				ObjectMeta: metav1.ObjectMeta{Name: repoName, Namespace: namespace},
				Spec:       sopsv1alpha1.GitRepositorySpec{URL: "https://example.invalid/repo.git"},
			})).To(Succeed())

			Expect(k8sClient.Create(ctx, makeCR(name, repoName))).To(Succeed())
			_ = reconcileOnce(name)
			got := reconcileOnce(name)

			c := condition(got, sopsv1alpha2.ConditionSourceReady)
			Expect(c).NotTo(BeNil())
			Expect(c.Status).To(Equal(metav1.ConditionFalse))
			Expect(c.Reason).To(Equal("SourceNotReady"))
		})
	})

	Context("CRD schema validation", func() {
		It("rejects a SopsSecret with empty data slice", func() {
			name := uniqueName("ss-bad-schema")
			bad := &sopsv1alpha2.SopsSecret{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec: sopsv1alpha2.SopsSecretSpec{
					Source: sopsv1alpha2.SourceRef{
						SourceRef: sopsv1alpha2.SourceKindRef{
							Kind: sopsv1alpha2.SourceKindGitRepository,
							Name: "r",
						},
						Path: "x.yaml",
					},
					Decryption: sopsv1alpha2.DecryptionSpec{
						KeyRef: sopsv1alpha2.SecretKeyRef{Name: "k", Key: "age"},
					},
					Data: []sopsv1alpha2.DataMapping{},
				},
			}
			err := k8sClient.Create(ctx, bad)
			Expect(err).To(HaveOccurred())
		})
	})
})

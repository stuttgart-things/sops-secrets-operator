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
	"github.com/stuttgart-things/sops-secrets-operator/internal/source"
)

var _ = Describe("SopsSecretManifest Controller", func() {
	const namespace = "default"
	var counter int
	var uniqueName func(prefix string) string

	BeforeEach(func() {
		counter++
		uniqueName = func(prefix string) string { return fmt.Sprintf("%s-%d", prefix, counter) }
	})

	makeCR := func(name, repoRef string) *sopsv1alpha1.SopsSecretManifest {
		return &sopsv1alpha1.SopsSecretManifest{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec: sopsv1alpha1.SopsSecretManifestSpec{
				Source: sopsv1alpha1.SourceRef{
					RepositoryRef: sopsv1alpha1.LocalObjectReference{Name: repoRef},
					Path:          "secret.enc.yaml",
				},
				Decryption: sopsv1alpha1.DecryptionSpec{
					KeyRef: sopsv1alpha1.SecretKeyRef{Name: "age-key", Key: "age.agekey"},
				},
			},
		}
	}

	reconcileOnce := func(name string) *sopsv1alpha1.SopsSecretManifest {
		r := &SopsSecretManifestReconciler{
			Client:   k8sClient,
			Scheme:   k8sClient.Scheme(),
			Registry: source.NewRegistry(),
		}
		_, err := r.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Namespace: namespace, Name: name},
		})
		Expect(err).NotTo(HaveOccurred())

		out := &sopsv1alpha1.SopsSecretManifest{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, out)).To(Succeed())
		return out
	}

	condition := func(sm *sopsv1alpha1.SopsSecretManifest, t string) *metav1.Condition {
		for i := range sm.Status.Conditions {
			if sm.Status.Conditions[i].Type == t {
				return &sm.Status.Conditions[i]
			}
		}
		return nil
	}

	Context("first reconcile", func() {
		It("adds the finalizer", func() {
			name := uniqueName("sm-finalizer")
			Expect(k8sClient.Create(ctx, makeCR(name, "nonexistent"))).To(Succeed())

			_, _ = (&SopsSecretManifestReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Registry: source.NewRegistry(),
			}).Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: namespace, Name: name},
			})

			got := &sopsv1alpha1.SopsSecretManifest{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, got)).To(Succeed())
			Expect(got.Finalizers).To(ContainElement(Finalizer))

			Expect(k8sClient.Delete(ctx, got)).To(Succeed())
		})
	})

	Context("when the referenced GitRepository is missing", func() {
		It("sets SourceReady=False with reason SourceMissing", func() {
			name := uniqueName("sm-no-repo")
			Expect(k8sClient.Create(ctx, makeCR(name, "missing-repo"))).To(Succeed())

			_ = reconcileOnce(name) // finalizer add
			got := reconcileOnce(name)

			c := condition(got, sopsv1alpha1.ConditionSourceReady)
			Expect(c).NotTo(BeNil())
			Expect(c.Status).To(Equal(metav1.ConditionFalse))
			Expect(c.Reason).To(Equal("SourceMissing"))
		})
	})

	Context("when the GitRepository exists but is not Ready", func() {
		It("sets SourceReady=False with reason SourceNotReady", func() {
			name := uniqueName("sm-repo-not-ready")
			repoName := name + "-repo"

			Expect(k8sClient.Create(ctx, &sopsv1alpha1.GitRepository{
				ObjectMeta: metav1.ObjectMeta{Name: repoName, Namespace: namespace},
				Spec:       sopsv1alpha1.GitRepositorySpec{URL: "https://example.invalid/repo.git"},
			})).To(Succeed())

			Expect(k8sClient.Create(ctx, makeCR(name, repoName))).To(Succeed())
			_ = reconcileOnce(name)
			got := reconcileOnce(name)

			c := condition(got, sopsv1alpha1.ConditionSourceReady)
			Expect(c).NotTo(BeNil())
			Expect(c.Status).To(Equal(metav1.ConditionFalse))
			Expect(c.Reason).To(Equal("SourceNotReady"))
		})
	})
})

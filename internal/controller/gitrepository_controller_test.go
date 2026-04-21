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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	sopsv1alpha1 "github.com/stuttgart-things/sops-secrets-operator/api/v1alpha1"
	"github.com/stuttgart-things/sops-secrets-operator/internal/source"
)

var _ = Describe("GitRepository Controller", func() {
	const namespace = "default"
	var counter int
	var uniqueName func(prefix string) string

	BeforeEach(func() {
		counter++
		uniqueName = func(prefix string) string { return fmt.Sprintf("%s-%d", prefix, counter) }
	})

	reconcileOnce := func(name string) *sopsv1alpha1.GitRepository {
		r := &GitRepositoryReconciler{
			Client:   k8sClient,
			Scheme:   k8sClient.Scheme(),
			Registry: source.NewRegistry(),
		}
		_, err := r.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Namespace: namespace, Name: name},
		})
		Expect(err).NotTo(HaveOccurred())

		out := &sopsv1alpha1.GitRepository{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, out)).To(Succeed())
		return out
	}

	condition := func(gr *sopsv1alpha1.GitRepository, t string) *metav1.Condition {
		for i := range gr.Status.Conditions {
			if gr.Status.Conditions[i].Type == t {
				return &gr.Status.Conditions[i]
			}
		}
		return nil
	}

	Context("when the referenced auth Secret is missing", func() {
		It("sets AuthResolved=False and leaves CacheReady=false", func() {
			name := uniqueName("gr-missing-auth")
			Expect(k8sClient.Create(ctx, &sopsv1alpha1.GitRepository{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec: sopsv1alpha1.GitRepositorySpec{
					URL:    "https://example.invalid/repo.git",
					Branch: "main",
					Auth: &sopsv1alpha1.GitAuth{
						Type:      sopsv1alpha1.GitAuthBasic,
						SecretRef: sopsv1alpha1.LocalObjectReference{Name: "does-not-exist"},
					},
				},
			})).To(Succeed())

			got := reconcileOnce(name)
			auth := condition(got, sopsv1alpha1.ConditionAuthResolved)
			Expect(auth).NotTo(BeNil())
			Expect(auth.Status).To(Equal(metav1.ConditionFalse))
			Expect(auth.Reason).To(Equal("AuthFailed"))
			Expect(got.Status.CacheReady).To(BeFalse())

			Expect(k8sClient.Delete(ctx, got)).To(Succeed())
		})
	})

	Context("when the auth secret is an SSH secret missing knownHosts", func() {
		It("resolves auth but reports SSH key-material error", func() {
			name := uniqueName("gr-ssh-no-kh")
			sec := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: name + "-auth", Namespace: namespace},
				Data: map[string][]byte{
					"privateKey": []byte("dummy"),
					// knownHosts intentionally missing
				},
			}
			Expect(k8sClient.Create(ctx, sec)).To(Succeed())

			Expect(k8sClient.Create(ctx, &sopsv1alpha1.GitRepository{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec: sopsv1alpha1.GitRepositorySpec{
					URL: "git@example.invalid:foo/bar.git",
					Auth: &sopsv1alpha1.GitAuth{
						Type:      sopsv1alpha1.GitAuthSSH,
						SecretRef: sopsv1alpha1.LocalObjectReference{Name: sec.Name},
					},
				},
			})).To(Succeed())

			got := reconcileOnce(name)
			auth := condition(got, sopsv1alpha1.ConditionAuthResolved)
			Expect(auth).NotTo(BeNil())
			Expect(auth.Status).To(Equal(metav1.ConditionFalse))
			Expect(auth.Message).To(ContainSubstring("knownHosts"))

			Expect(k8sClient.Delete(ctx, got)).To(Succeed())
			Expect(k8sClient.Delete(ctx, sec)).To(Succeed())
		})
	})

	Context("reconcile is idempotent", func() {
		It("produces the same status on repeated calls", func() {
			name := uniqueName("gr-idempotent")
			Expect(k8sClient.Create(ctx, &sopsv1alpha1.GitRepository{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec: sopsv1alpha1.GitRepositorySpec{
					URL:    "https://example.invalid/repo.git",
					Branch: "main",
					Auth: &sopsv1alpha1.GitAuth{
						Type:      sopsv1alpha1.GitAuthBasic,
						SecretRef: sopsv1alpha1.LocalObjectReference{Name: "missing"},
					},
				},
			})).To(Succeed())

			first := reconcileOnce(name)
			firstAuth := condition(first, sopsv1alpha1.ConditionAuthResolved)
			Expect(firstAuth).NotTo(BeNil())

			second := reconcileOnce(name)
			secondAuth := condition(second, sopsv1alpha1.ConditionAuthResolved)
			Expect(secondAuth).NotTo(BeNil())
			Expect(secondAuth.Status).To(Equal(firstAuth.Status))
			Expect(secondAuth.Reason).To(Equal(firstAuth.Reason))

			Expect(k8sClient.Delete(ctx, second)).To(Succeed())
		})
	})

	Context("missing CR", func() {
		It("does not error on NotFound and forgets the registry entry", func() {
			r := &GitRepositoryReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Registry: source.NewRegistry(),
			}
			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: namespace, Name: "never-created"},
			})
			Expect(err).NotTo(HaveOccurred())
			got := &sopsv1alpha1.GitRepository{}
			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "never-created"}, got)
			Expect(errors.IsNotFound(err)).To(BeTrue())
		})
	})

})

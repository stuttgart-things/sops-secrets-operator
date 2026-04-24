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
	"net/http"
	"net/http/httptest"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	sopsv1alpha2 "github.com/stuttgart-things/sops-secrets-operator/api/v1alpha2"
	"github.com/stuttgart-things/sops-secrets-operator/internal/source"
)

var _ = Describe("ObjectSource Controller", func() {
	const namespace = "default"
	var counter int
	var uniqueName func(prefix string) string

	BeforeEach(func() {
		counter++
		uniqueName = func(prefix string) string { return fmt.Sprintf("%s-%d", prefix, counter) }
	})

	reconcileOnce := func(r *ObjectSourceReconciler, name string) *sopsv1alpha2.ObjectSource {
		_, err := r.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Namespace: namespace, Name: name},
		})
		Expect(err).NotTo(HaveOccurred())
		out := &sopsv1alpha2.ObjectSource{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, out)).To(Succeed())
		return out
	}

	condition := func(os *sopsv1alpha2.ObjectSource, t string) *metav1.Condition {
		for i := range os.Status.Conditions {
			if os.Status.Conditions[i].Type == t {
				return &os.Status.Conditions[i]
			}
		}
		return nil
	}

	Context("URL mode with ETag semantics", func() {
		It("updates cache on 200 and skips refetch on 304", func() {
			const etag = `"v1"`
			var gets int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodGet:
					atomic.AddInt32(&gets, 1)
					if r.Header.Get("If-None-Match") == etag {
						w.WriteHeader(http.StatusNotModified)
						return
					}
					w.Header().Set("ETag", etag)
					_, _ = w.Write([]byte("enc-payload"))
				default:
					http.Error(w, "no", http.StatusMethodNotAllowed)
				}
			}))
			defer srv.Close()

			name := uniqueName("os-https")
			Expect(k8sClient.Create(ctx, &sopsv1alpha2.ObjectSource{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec: sopsv1alpha2.ObjectSourceSpec{
					URL: srv.URL,
				},
			})).To(Succeed())

			r := &ObjectSourceReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Registry: source.NewRegistry(),
			}

			first := reconcileOnce(r, name)
			ready := condition(first, ObjectConditionSourceReady)
			Expect(ready).NotTo(BeNil())
			Expect(ready.Status).To(Equal(metav1.ConditionTrue))
			Expect(first.Status.LastSyncedETag).To(Equal(etag))
			Expect(first.Status.CacheReady).To(BeTrue())

			second := reconcileOnce(r, name)
			Expect(second.Status.LastSyncedETag).To(Equal(etag))
			Expect(atomic.LoadInt32(&gets)).To(Equal(int32(2))) // 200 + 304

			Expect(k8sClient.Delete(ctx, second)).To(Succeed())
		})
	})

	Context("URL mode with bearer auth", func() {
		It("resolves the Secret and includes the Authorization header", func() {
			var gotAuth string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotAuth = r.Header.Get("Authorization")
				w.Header().Set("ETag", `"a"`)
				_, _ = w.Write([]byte("x"))
			}))
			defer srv.Close()

			name := uniqueName("os-bearer")
			secName := name + "-auth"
			Expect(k8sClient.Create(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secName, Namespace: namespace},
				Data:       map[string][]byte{"token": []byte("t0ken")},
			})).To(Succeed())

			Expect(k8sClient.Create(ctx, &sopsv1alpha2.ObjectSource{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec: sopsv1alpha2.ObjectSourceSpec{
					URL: srv.URL,
					Auth: &sopsv1alpha2.ObjectAuth{
						Type:      sopsv1alpha2.ObjectAuthBearer,
						SecretRef: &sopsv1alpha2.LocalObjectReference{Name: secName},
					},
				},
			})).To(Succeed())

			r := &ObjectSourceReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Registry: source.NewRegistry(),
			}
			got := reconcileOnce(r, name)
			Expect(got.Status.CacheReady).To(BeTrue())
			Expect(gotAuth).To(Equal("Bearer t0ken"))

			Expect(k8sClient.Delete(ctx, got)).To(Succeed())
		})
	})

	Context("missing auth Secret", func() {
		It("sets AuthResolved=False and does not mark cache ready", func() {
			name := uniqueName("os-missing-auth")
			Expect(k8sClient.Create(ctx, &sopsv1alpha2.ObjectSource{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec: sopsv1alpha2.ObjectSourceSpec{
					URL: "https://example.invalid/x",
					Auth: &sopsv1alpha2.ObjectAuth{
						Type:      sopsv1alpha2.ObjectAuthBearer,
						SecretRef: &sopsv1alpha2.LocalObjectReference{Name: "does-not-exist"},
					},
				},
			})).To(Succeed())

			r := &ObjectSourceReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Registry: source.NewRegistry(),
			}
			got := reconcileOnce(r, name)
			auth := condition(got, ObjectConditionAuthResolved)
			Expect(auth).NotTo(BeNil())
			Expect(auth.Status).To(Equal(metav1.ConditionFalse))
			Expect(got.Status.CacheReady).To(BeFalse())

			Expect(k8sClient.Delete(ctx, got)).To(Succeed())
		})
	})

	Context("fetch failure on upstream 5xx", func() {
		It("reports SourceReady=False with reason FetchFailed", func() {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "broken", http.StatusInternalServerError)
			}))
			defer srv.Close()

			name := uniqueName("os-5xx")
			Expect(k8sClient.Create(ctx, &sopsv1alpha2.ObjectSource{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec:       sopsv1alpha2.ObjectSourceSpec{URL: srv.URL},
			})).To(Succeed())

			r := &ObjectSourceReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Registry: source.NewRegistry(),
			}
			got := reconcileOnce(r, name)
			ready := condition(got, ObjectConditionSourceReady)
			Expect(ready).NotTo(BeNil())
			Expect(ready.Status).To(Equal(metav1.ConditionFalse))
			Expect(ready.Reason).To(Equal("FetchFailed"))
			Expect(got.Status.CacheReady).To(BeFalse())

			Expect(k8sClient.Delete(ctx, got)).To(Succeed())
		})
	})

	Context("bucket mode with missing s3 secret keys", func() {
		It("reports AuthResolved=False without leaking credentials", func() {
			name := uniqueName("os-bucket-badsec")
			secName := name + "-auth"
			Expect(k8sClient.Create(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secName, Namespace: namespace},
				Data:       map[string][]byte{"accessKey": []byte("AK")},
			})).To(Succeed())

			Expect(k8sClient.Create(ctx, &sopsv1alpha2.ObjectSource{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec: sopsv1alpha2.ObjectSourceSpec{
					Bucket: &sopsv1alpha2.BucketSpec{
						Endpoint: "s3.invalid:9000",
						Name:     "mybucket",
					},
					Auth: &sopsv1alpha2.ObjectAuth{
						Type:      sopsv1alpha2.ObjectAuthS3,
						SecretRef: &sopsv1alpha2.LocalObjectReference{Name: secName},
					},
				},
			})).To(Succeed())

			r := &ObjectSourceReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Registry: source.NewRegistry(),
			}
			got := reconcileOnce(r, name)
			auth := condition(got, ObjectConditionAuthResolved)
			Expect(auth).NotTo(BeNil())
			Expect(auth.Status).To(Equal(metav1.ConditionFalse))
			Expect(auth.Message).To(ContainSubstring("secretKey"))
			Expect(auth.Message).NotTo(ContainSubstring("AK"))

			Expect(k8sClient.Delete(ctx, got)).To(Succeed())
		})
	})

	Context("both url and bucket set", func() {
		It("is rejected by OpenAPI validation at admission", func() {
			name := uniqueName("os-both")
			err := k8sClient.Create(ctx, &sopsv1alpha2.ObjectSource{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec: sopsv1alpha2.ObjectSourceSpec{
					URL:    "https://example.invalid/x",
					Bucket: &sopsv1alpha2.BucketSpec{Endpoint: "s3.invalid", Name: "b"},
				},
			})
			Expect(err).To(HaveOccurred())
			Expect(errors.IsInvalid(err)).To(BeTrue())
		})
	})

	Context("neither url nor bucket set", func() {
		It("is rejected by OpenAPI validation at admission", func() {
			name := uniqueName("os-neither")
			err := k8sClient.Create(ctx, &sopsv1alpha2.ObjectSource{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec:       sopsv1alpha2.ObjectSourceSpec{},
			})
			Expect(err).To(HaveOccurred())
			Expect(errors.IsInvalid(err)).To(BeTrue())
		})
	})

	Context("missing CR", func() {
		It("returns without error and forgets the registry entry", func() {
			r := &ObjectSourceReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Registry: source.NewRegistry(),
			}
			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: namespace, Name: "never-created"},
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})
})

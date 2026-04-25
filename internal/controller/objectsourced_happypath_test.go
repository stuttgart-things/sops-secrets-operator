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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	sopsv1alpha2 "github.com/stuttgart-things/sops-secrets-operator/api/v1alpha2"
	"github.com/stuttgart-things/sops-secrets-operator/internal/source"
	"github.com/stuttgart-things/sops-secrets-operator/internal/testutil"
)

// objectSourceFixture mirrors gitFixture but for an ObjectSource: a local
// httptest.Server is the upstream, the ObjectSource controller has been
// run end-to-end so the Registry holds the cached encrypted bytes, and
// the consumer (SopsSecret/SopsSecretManifest) reconciler reads back via
// Registry.ReadObject.
type objectSourceFixture struct {
	registry *source.Registry
	srcRef   string // ObjectSource CR name
	keyRef   string // age-key Secret name
	server   *httptest.Server
	gets     *int32
}

func objectSourceRef(name, path string) sopsv1alpha2.SourceRef {
	return sopsv1alpha2.SourceRef{
		SourceRef: sopsv1alpha2.SourceKindRef{
			Kind: sopsv1alpha2.SourceKindObjectSource,
			Name: name,
		},
		Path: path,
	}
}

var _ = Describe("ObjectSource-sourced happy paths (envtest)", func() {
	const namespace = "default"
	var counter int
	var uniq func(prefix string) string

	BeforeEach(func() {
		counter++
		uniq = func(prefix string) string { return fmt.Sprintf("%s-%d", prefix, counter) }
	})

	// newObjectSourceFixture spins up an httptest.Server that serves the
	// SOPS-encrypted bytes for `plaintext`, creates an ObjectSource pointing
	// at it, runs the ObjectSourceReconciler twice to populate the Registry,
	// and returns the bits a consumer test needs to wire up its CR.
	newObjectSourceFixture := func(prefix string, plaintext []byte) *objectSourceFixture {
		GinkgoHelper()
		age := testutil.GenerateAge(GinkgoT())
		ct := testutil.EncryptYAML(GinkgoT(), age.PublicKey, plaintext)

		const etag = `"v1"`
		var gets int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				http.Error(w, "no", http.StatusMethodNotAllowed)
				return
			}
			atomic.AddInt32(&gets, 1)
			if r.Header.Get("If-None-Match") == etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", etag)
			_, _ = w.Write(ct)
		}))

		keyRef := prefix + "-age-key"
		Expect(k8sClient.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: keyRef, Namespace: namespace},
			Data:       map[string][]byte{"age.agekey": []byte(age.PrivateKey)},
		})).To(Succeed())

		srcCRName := prefix + "-src"
		Expect(k8sClient.Create(ctx, &sopsv1alpha2.ObjectSource{
			ObjectMeta: metav1.ObjectMeta{Name: srcCRName, Namespace: namespace},
			Spec:       sopsv1alpha2.ObjectSourceSpec{URL: srv.URL},
		})).To(Succeed())

		registry := source.NewRegistry()
		osr := &ObjectSourceReconciler{
			Client:   k8sClient,
			Scheme:   k8sClient.Scheme(),
			Registry: registry,
		}
		_, err := osr.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Namespace: namespace, Name: srcCRName},
		})
		Expect(err).NotTo(HaveOccurred())

		got := &sopsv1alpha2.ObjectSource{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: srcCRName}, got)).To(Succeed())
		Expect(got.Status.CacheReady).To(BeTrue(), "ObjectSource should be ready; conditions=%+v", got.Status.Conditions)
		Expect(got.Status.LastSyncedETag).To(Equal(etag))

		return &objectSourceFixture{
			registry: registry,
			srcRef:   srcCRName,
			keyRef:   keyRef,
			server:   srv,
			gets:     &gets,
		}
	}

	Context("SopsSecret backed by an ObjectSource (URL mode)", func() {
		It("materializes a Secret with exactly the mapped keys", func() {
			prefix := uniq("ss-os-e2e")
			plain := []byte("db_user: alice\ndb_password: s3cret\napi_token: xyz\n")
			fx := newObjectSourceFixture(prefix, plain)
			defer fx.server.Close()

			cr := &sopsv1alpha2.SopsSecret{
				ObjectMeta: metav1.ObjectMeta{Name: prefix, Namespace: namespace},
				Spec: sopsv1alpha2.SopsSecretSpec{
					// path is unused for URL-mode ObjectSource but spec.source.path
					// is required by the schema; supply a placeholder.
					Source: objectSourceRef(fx.srcRef, "creds.enc.yaml"),
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

			r := &SopsSecretReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Registry: fx.registry,
			}
			for range 2 {
				_, err := r.Reconcile(ctx, reconcile.Request{
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
			// ETag is recorded as the "revision" in the source-commit annotation.
			Expect(target.Annotations[SourceCommitAnnotation]).To(Equal(`"v1"`))
		})

		It("reverts drift when the target Secret is edited out of band", func() {
			prefix := uniq("ss-os-drift")
			fx := newObjectSourceFixture(prefix, []byte("token: correct-horse\n"))
			defer fx.server.Close()

			cr := &sopsv1alpha2.SopsSecret{
				ObjectMeta: metav1.ObjectMeta{Name: prefix, Namespace: namespace},
				Spec: sopsv1alpha2.SopsSecretSpec{
					Source: objectSourceRef(fx.srcRef, "drift.enc.yaml"),
					Decryption: sopsv1alpha2.DecryptionSpec{
						KeyRef: sopsv1alpha2.SecretKeyRef{Name: fx.keyRef, Key: "age.agekey"},
					},
					Data: []sopsv1alpha2.DataMapping{{Key: "TOKEN", From: "token"}},
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			r := &SopsSecretReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Registry: fx.registry,
			}
			for range 2 {
				_, err := r.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Namespace: namespace, Name: prefix},
				})
				Expect(err).NotTo(HaveOccurred())
			}

			target := &corev1.Secret{}
			key := types.NamespacedName{Namespace: namespace, Name: prefix}
			Expect(k8sClient.Get(ctx, key, target)).To(Succeed())
			Expect(string(target.Data["TOKEN"])).To(Equal("correct-horse"))

			target.Data["TOKEN"] = []byte("tampered")
			Expect(k8sClient.Update(ctx, target)).To(Succeed())

			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, key, target)).To(Succeed())
			Expect(string(target.Data["TOKEN"])).To(Equal("correct-horse"))
		})
	})

	Context("SopsSecretManifest backed by an ObjectSource", func() {
		It("applies the decrypted manifest with authoritative namespace", func() {
			prefix := uniq("sm-os-e2e")
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
			fx := newObjectSourceFixture(prefix, manifest)
			defer fx.server.Close()

			overrideName := prefix + "-override"
			cr := &sopsv1alpha2.SopsSecretManifest{
				ObjectMeta: metav1.ObjectMeta{Name: prefix, Namespace: namespace},
				Spec: sopsv1alpha2.SopsSecretManifestSpec{
					Source: objectSourceRef(fx.srcRef, "sec.enc.yaml"),
					Decryption: sopsv1alpha2.DecryptionSpec{
						KeyRef: sopsv1alpha2.SecretKeyRef{Name: fx.keyRef, Key: "age.agekey"},
					},
					Target: sopsv1alpha2.ManifestTarget{NameOverride: overrideName},
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			r := &SopsSecretManifestReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Registry: fx.registry,
			}
			for range 2 {
				_, err := r.Reconcile(ctx, reconcile.Request{
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

	Context("force-sync annotation", func() {
		It("on the SopsSecret consumer: records the token after re-applying", func() {
			prefix := uniq("ss-os-force")
			fx := newObjectSourceFixture(prefix, []byte("token: t1\n"))
			defer fx.server.Close()

			cr := &sopsv1alpha2.SopsSecret{
				ObjectMeta: metav1.ObjectMeta{Name: prefix, Namespace: namespace},
				Spec: sopsv1alpha2.SopsSecretSpec{
					Source: objectSourceRef(fx.srcRef, "tok.enc.yaml"),
					Decryption: sopsv1alpha2.DecryptionSpec{
						KeyRef: sopsv1alpha2.SecretKeyRef{Name: fx.keyRef, Key: "age.agekey"},
					},
					Data: []sopsv1alpha2.DataMapping{{Key: "TOKEN", From: "token"}},
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			r := &SopsSecretReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Registry: fx.registry,
			}
			for range 2 {
				_, err := r.Reconcile(ctx, reconcile.Request{
					NamespacedName: types.NamespacedName{Namespace: namespace, Name: prefix},
				})
				Expect(err).NotTo(HaveOccurred())
			}

			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: prefix}, cr)).To(Succeed())
			Expect(cr.Status.LastProcessedReconcileToken).To(BeEmpty())

			cr.Annotations = map[string]string{ReconcileRequestAnnotation: "now-1"}
			Expect(k8sClient.Update(ctx, cr)).To(Succeed())

			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: namespace, Name: prefix},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: prefix}, cr)).To(Succeed())
			Expect(cr.Status.LastProcessedReconcileToken).To(Equal("now-1"))
		})

		It("on the ObjectSource: invalidates the cache and re-fetches upstream", func() {
			prefix := uniq("os-force-src")
			fx := newObjectSourceFixture(prefix, []byte("k: v\n"))
			defer fx.server.Close()
			before := atomic.LoadInt32(fx.gets)

			osr := &ObjectSourceReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Registry: fx.registry,
			}

			// A no-op reconcile: ETag matches, upstream returns 304.
			_, err := osr.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: namespace, Name: fx.srcRef},
			})
			Expect(err).NotTo(HaveOccurred())
			afterIdle := atomic.LoadInt32(fx.gets)
			Expect(afterIdle).To(Equal(before + 1))

			// Annotate to force re-fetch. The reconciler must drop the cached
			// ETag and issue an unconditional GET, which the upstream answers
			// with 200 (counted as a fresh hit).
			os := &sopsv1alpha2.ObjectSource{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: fx.srcRef}, os)).To(Succeed())
			os.Annotations = map[string]string{ReconcileRequestAnnotation: "force-1"}
			Expect(k8sClient.Update(ctx, os)).To(Succeed())

			_, err = osr.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: namespace, Name: fx.srcRef},
			})
			Expect(err).NotTo(HaveOccurred())

			afterForce := atomic.LoadInt32(fx.gets)
			Expect(afterForce).To(Equal(afterIdle + 1))

			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: fx.srcRef}, os)).To(Succeed())
			Expect(os.Status.LastProcessedReconcileToken).To(Equal("force-1"))
		})
	})
})

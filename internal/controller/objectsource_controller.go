/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	sopsv1alpha2 "github.com/stuttgart-things/sops-secrets-operator/api/v1alpha2"
	"github.com/stuttgart-things/sops-secrets-operator/internal/object"
	"github.com/stuttgart-things/sops-secrets-operator/internal/source"
)

const (
	// ObjectSourceAuthSecretIndex is a field index on ObjectSource pointing
	// at the name of its auth secret reference. Used to enqueue ObjectSources
	// when a referenced Secret changes.
	ObjectSourceAuthSecretIndex = ".spec.auth.secretRef.name"

	// ObjectConditionSourceReady is the source-ready condition type for
	// ObjectSource, mirroring GitRepository's naming.
	ObjectConditionSourceReady = "SourceReady"
	// ObjectConditionAuthResolved mirrors GitRepository's AuthResolved.
	ObjectConditionAuthResolved = "AuthResolved"
)

// ObjectSourceReconciler reconciles ObjectSource (v1alpha2) objects.
type ObjectSourceReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Registry *source.Registry
}

// +kubebuilder:rbac:groups=sops.stuttgart-things.com,resources=objectsources,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sops.stuttgart-things.com,resources=objectsources/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sops.stuttgart-things.com,resources=objectsources/finalizers,verbs=update

func (r *ObjectSourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("objectsource", req.NamespacedName)
	setStage, finish := trackReconcile("ObjectSource")
	defer finish()

	var os sopsv1alpha2.ObjectSource
	if err := r.Get(ctx, req.NamespacedName, &os); err != nil {
		if apierrors.IsNotFound(err) {
			r.Registry.ForgetObject(req.NamespacedName)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !os.DeletionTimestamp.IsZero() {
		r.Registry.ForgetObject(req.NamespacedName)
		return ctrl.Result{}, nil
	}

	// Validate spec oneOf. The CRD OpenAPI validation already blocks this
	// at admission time, but keep a defensive check for in-cluster edits
	// that could bypass validation (e.g. legacy clients).
	hasURL := os.Spec.URL != ""
	hasBucket := os.Spec.Bucket != nil
	if hasURL == hasBucket {
		err := fmt.Errorf("exactly one of spec.url or spec.bucket must be set")
		setStage(StageAuth)
		setObjectFailure(&os, ObjectConditionAuthResolved, "InvalidSpec", err.Error())
		if uerr := r.Status().Update(ctx, &os); uerr != nil {
			return ctrl.Result{}, uerr
		}
		return ctrl.Result{RequeueAfter: retryAfter}, nil
	}

	fetcher, mode, err := r.buildFetcher(ctx, &os)
	if err != nil {
		log.Error(err, "auth resolution failed")
		setStage(StageAuth)
		setObjectFailure(&os, ObjectConditionAuthResolved, "AuthFailed", err.Error())
		if uerr := r.Status().Update(ctx, &os); uerr != nil {
			return ctrl.Result{}, uerr
		}
		return ctrl.Result{RequeueAfter: retryAfter}, nil
	}
	setCondition(&os.Status.Conditions, ObjectConditionAuthResolved, metav1.ConditionTrue, "AuthOK", "auth resolved")

	etag, err := r.Registry.EnsureObjectCached(ctx, req.NamespacedName, mode, fetcher)
	if err != nil {
		log.Error(err, "fetch failed")
		setStage(StageFetch)
		setCondition(&os.Status.Conditions, ObjectConditionSourceReady, metav1.ConditionFalse, "FetchFailed", err.Error())
		os.Status.CacheReady = false
		if uerr := r.Status().Update(ctx, &os); uerr != nil {
			return ctrl.Result{}, uerr
		}
		return ctrl.Result{RequeueAfter: retryAfter}, nil
	}

	now := metav1.NewTime(time.Now())
	os.Status.LastSyncedETag = etag
	os.Status.LastSyncedAt = &now
	os.Status.CacheReady = true
	os.Status.ObservedGeneration = os.Generation

	msg := "cache ready"
	switch mode {
	case source.ObjectModeURL:
		if etag != "" {
			msg = "cache at " + etag
		}
	case source.ObjectModeBucket:
		msg = "bucket reachable"
	}
	setCondition(&os.Status.Conditions, ObjectConditionSourceReady, metav1.ConditionTrue, "Ready", msg)
	if err := r.Status().Update(ctx, &os); err != nil {
		return ctrl.Result{}, err
	}

	interval := os.Spec.Interval.Duration
	if interval == 0 {
		interval = defaultSyncInterval
	}
	return ctrl.Result{RequeueAfter: interval}, nil
}

// buildFetcher resolves the ObjectSource's auth Secret (if any) and
// returns an object.Fetcher plus the Registry cache mode. It never logs
// credential material.
func (r *ObjectSourceReconciler) buildFetcher(ctx context.Context, os *sopsv1alpha2.ObjectSource) (object.Fetcher, source.ObjectMode, error) {
	var sec *corev1.Secret
	if os.Spec.Auth != nil && os.Spec.Auth.Type != sopsv1alpha2.ObjectAuthNone {
		if os.Spec.Auth.SecretRef == nil {
			return nil, 0, fmt.Errorf("auth type %q requires secretRef", os.Spec.Auth.Type)
		}
		var s corev1.Secret
		if err := r.Get(ctx, client.ObjectKey{Namespace: os.Namespace, Name: os.Spec.Auth.SecretRef.Name}, &s); err != nil {
			return nil, 0, fmt.Errorf("get auth secret: %w", err)
		}
		sec = &s
	}

	if os.Spec.URL != "" {
		auth, err := resolveHTTPSAuth(os.Spec.Auth, sec)
		if err != nil {
			return nil, 0, err
		}
		return object.NewHTTPSFetcher(os.Spec.URL, auth), source.ObjectModeURL, nil
	}

	cfg := object.S3Config{
		Endpoint: os.Spec.Bucket.Endpoint,
		Bucket:   os.Spec.Bucket.Name,
		Region:   os.Spec.Bucket.Region,
		Insecure: os.Spec.Bucket.Insecure,
	}
	if os.Spec.Auth != nil && os.Spec.Auth.Type == sopsv1alpha2.ObjectAuthS3 {
		if len(sec.Data["accessKey"]) == 0 || len(sec.Data["secretKey"]) == 0 {
			return nil, 0, fmt.Errorf("auth secret %q: missing 'accessKey' or 'secretKey'", sec.Name)
		}
		cfg.AccessKey = string(sec.Data["accessKey"])
		cfg.SecretKey = string(sec.Data["secretKey"])
	} else if os.Spec.Auth != nil && os.Spec.Auth.Type != sopsv1alpha2.ObjectAuthNone {
		return nil, 0, fmt.Errorf("auth type %q is not valid for bucket mode (use s3 or none)", os.Spec.Auth.Type)
	}

	f, err := object.NewS3Fetcher(cfg)
	if err != nil {
		return nil, 0, err
	}
	return f, source.ObjectModeBucket, nil
}

func resolveHTTPSAuth(auth *sopsv1alpha2.ObjectAuth, sec *corev1.Secret) (object.HTTPSAuth, error) {
	if auth == nil || auth.Type == sopsv1alpha2.ObjectAuthNone {
		return object.HTTPSAuth{}, nil
	}
	switch auth.Type {
	case sopsv1alpha2.ObjectAuthBearer:
		if len(sec.Data["token"]) == 0 {
			return object.HTTPSAuth{}, fmt.Errorf("auth secret %q: missing 'token' key", sec.Name)
		}
		return object.HTTPSAuth{BearerToken: string(sec.Data["token"])}, nil
	case sopsv1alpha2.ObjectAuthBasic:
		if len(sec.Data["username"]) == 0 || len(sec.Data["password"]) == 0 {
			return object.HTTPSAuth{}, fmt.Errorf("auth secret %q: missing 'username' or 'password'", sec.Name)
		}
		return object.HTTPSAuth{
			Username: string(sec.Data["username"]),
			Password: string(sec.Data["password"]),
		}, nil
	case sopsv1alpha2.ObjectAuthS3:
		return object.HTTPSAuth{}, fmt.Errorf("auth type 's3' is only valid for bucket mode")
	default:
		return object.HTTPSAuth{}, fmt.Errorf("unknown auth type %q", auth.Type)
	}
}

func setObjectFailure(os *sopsv1alpha2.ObjectSource, condType, reason, msg string) {
	setCondition(&os.Status.Conditions, condType, metav1.ConditionFalse, reason, msg)
	setCondition(&os.Status.Conditions, ObjectConditionSourceReady, metav1.ConditionFalse, reason, "waiting for "+condType)
	os.Status.CacheReady = false
}

func (r *ObjectSourceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&sopsv1alpha2.ObjectSource{},
		ObjectSourceAuthSecretIndex,
		func(obj client.Object) []string {
			o := obj.(*sopsv1alpha2.ObjectSource)
			if o.Spec.Auth == nil || o.Spec.Auth.SecretRef == nil {
				return nil
			}
			return []string{o.Spec.Auth.SecretRef.Name}
		},
	); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&sopsv1alpha2.ObjectSource{}).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.mapSecretToObjectSources)).
		Named("objectsource").
		Complete(r)
}

func (r *ObjectSourceReconciler) mapSecretToObjectSources(ctx context.Context, obj client.Object) []reconcile.Request {
	var list sopsv1alpha2.ObjectSourceList
	if err := r.List(ctx, &list,
		client.InNamespace(obj.GetNamespace()),
		client.MatchingFields{ObjectSourceAuthSecretIndex: obj.GetName()},
	); err != nil {
		return nil
	}
	out := make([]reconcile.Request, 0, len(list.Items))
	for _, o := range list.Items {
		out = append(out, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&o)})
	}
	return out
}

/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	"time"

	"github.com/stuttgart-things/sops-secrets-operator/internal/metrics"
)

// Reconcile stage labels used on the sops_reconcile_errors_total counter.
const (
	StageAuth    = "auth"
	StageFetch   = "fetch"
	StageDecrypt = "decrypt"
	StageApply   = "apply"
)

// trackReconcile is called at the top of each Reconcile and returns a
// setter for the error stage plus a deferred finisher. Usage:
//
//	setStage, finish := trackReconcile("GitRepository")
//	defer finish()
//	// on failure path:
//	setStage(StageAuth)
//	return r.failStatus(...)
func trackReconcile(kind string) (setStage func(string), finish func()) {
	start := time.Now()
	var stage string
	return func(s string) { stage = s }, func() {
			result := metrics.ResultSuccess
			if stage != "" {
				result = metrics.ResultError
				metrics.ReconcileErrorsTotal.WithLabelValues(kind, stage).Inc()
			}
			metrics.ReconcileTotal.WithLabelValues(kind, result).Inc()
			metrics.ReconcileDurationSeconds.WithLabelValues(kind, result).Observe(time.Since(start).Seconds())
		}
}

// Labels and annotations set on every target Secret produced by this operator.
// They mark ownership (for adoption checks and finalizer cleanup) and carry
// the source-commit / content-hash needed for drift detection.
const (
	// ManagedByLabel marks a Secret as managed by this operator.
	ManagedByLabel = "sops.stuttgart-things.com/managed-by"
	// ManagedByValue is the operator name written to ManagedByLabel.
	ManagedByValue = "sops-secrets-operator"

	// OwnerAnnotation carries "<kind>/<namespace>/<name>" of the CR that owns
	// the Secret. Used for adoption-conflict detection across CR kinds.
	OwnerAnnotation = "sops.stuttgart-things.com/owner"
	// OwnerUIDAnnotation carries the owning CR's UID.
	OwnerUIDAnnotation = "sops.stuttgart-things.com/owner-uid"

	// ContentHashAnnotation holds a SHA-256 of the applied Secret data.
	ContentHashAnnotation = "sops.stuttgart-things.com/content-hash"
	// SourceCommitAnnotation holds the git commit the data was derived from.
	SourceCommitAnnotation = "sops.stuttgart-things.com/source-commit"

	// Finalizer is set on every SopsSecret / SopsSecretManifest so the
	// target Secret can be cleaned up on CR deletion.
	Finalizer = "sops.stuttgart-things.com/finalizer"
)

// FieldOwner is the server-side-apply / CreateOrUpdate field-manager name.
const FieldOwner = "sops-secrets-operator"

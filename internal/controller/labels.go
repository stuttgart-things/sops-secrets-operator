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

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/stuttgart-things/sops-secrets-operator/internal/metrics"
	"github.com/stuttgart-things/sops-secrets-operator/internal/tracing"
)

// Reconcile stage labels used on the sops_reconcile_errors_total counter
// and as child-span names in OTel traces.
const (
	StageAuth    = "auth"
	StageFetch   = "fetch"
	StageDecrypt = "decrypt"
	StageApply   = "apply"
)

// reconcileTracker collects per-reconcile metrics and spans. One is
// created at the top of each Reconcile via trackReconcile and finalized
// via Finish. Stage starts a child span scoped to one of the StageX
// constants; calling it again ends the previous child first.
//
// Spans never carry plaintext data or key material — only resource
// identifiers (namespace/name/kind/sourceRef) and post-decrypt
// fingerprints (commit SHA, content hash).
type reconcileTracker struct {
	metricKind string
	start      time.Time
	failStage  string

	rootSpan  trace.Span
	stageSpan trace.Span
}

// trackReconcile starts the metrics timer and the root span for a
// reconcile. Callers should:
//
//	ctx, t := trackReconcile(ctx, "SopsSecret", req.Namespace, req.Name)
//	defer t.Finish()
//	ctx = t.Stage(ctx, StageFetch) // before the fetch call
//	...
//	if err != nil { t.Fail(StageFetch, err); return ... }
//
// The returned context carries the root span; pass it down so any
// span-aware callees nest beneath it.
func trackReconcile(ctx context.Context, kind, namespace, name string) (context.Context, *reconcileTracker) {
	t := &reconcileTracker{
		metricKind: kind,
		start:      time.Now(),
	}
	ctx, t.rootSpan = tracing.Tracer().Start(ctx,
		fmt.Sprintf("%s.Reconcile", kind),
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(
			attribute.String("sops.kind", kind),
			attribute.String("sops.namespace", namespace),
			attribute.String("sops.name", name),
		),
	)
	return ctx, t
}

// Stage closes any open child span and starts a new one for the given
// stage. The returned context is scoped to the new child span.
func (t *reconcileTracker) Stage(ctx context.Context, stage string) context.Context {
	if t.stageSpan != nil {
		t.stageSpan.End()
		t.stageSpan = nil
	}
	ctx, t.stageSpan = tracing.Tracer().Start(ctx, "stage."+stage,
		trace.WithAttributes(attribute.String("sops.stage", stage)),
	)
	return ctx
}

// Fail marks the given stage as the failure point. The error is
// recorded on the active child span (if any) and the root span, both
// of which get codes.Error status. The metric label is set so Finish
// records sops_reconcile_errors_total{stage=...}.
func (t *reconcileTracker) Fail(stage string, err error) {
	t.failStage = stage
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if t.stageSpan != nil {
		if err != nil {
			t.stageSpan.RecordError(err)
		}
		t.stageSpan.SetStatus(codes.Error, msg)
	}
	if t.rootSpan != nil {
		if err != nil {
			t.rootSpan.RecordError(err)
		}
		t.rootSpan.SetStatus(codes.Error, msg)
	}
}

// SetSourceRef adds source-kind / source-name attributes to the root
// span once they are known. Safe to call before the first Stage.
func (t *reconcileTracker) SetSourceRef(kind, name string) {
	if t.rootSpan == nil {
		return
	}
	t.rootSpan.SetAttributes(
		attribute.String("sops.source.kind", kind),
		attribute.String("sops.source.name", name),
	)
}

// SetCommit annotates the root span with the source revision (commit
// SHA for git, ETag for object storage) once observed.
func (t *reconcileTracker) SetCommit(revision string) {
	if t.rootSpan == nil || revision == "" {
		return
	}
	t.rootSpan.SetAttributes(attribute.String("sops.commit", revision))
}

// SetContentHash annotates the root span with the post-decrypt content
// hash. The hash is a fingerprint, not plaintext.
func (t *reconcileTracker) SetContentHash(hash string) {
	if t.rootSpan == nil || hash == "" {
		return
	}
	t.rootSpan.SetAttributes(attribute.String("sops.content_hash", hash))
}

// Finish ends any open child span and the root span, then writes the
// reconcile metrics. Must be called via defer at the top of Reconcile.
func (t *reconcileTracker) Finish() {
	if t.stageSpan != nil {
		t.stageSpan.End()
		t.stageSpan = nil
	}
	if t.rootSpan != nil {
		t.rootSpan.End()
	}

	result := metrics.ResultSuccess
	if t.failStage != "" {
		result = metrics.ResultError
		metrics.ReconcileErrorsTotal.WithLabelValues(t.metricKind, t.failStage).Inc()
	}
	metrics.ReconcileTotal.WithLabelValues(t.metricKind, result).Inc()
	metrics.ReconcileDurationSeconds.WithLabelValues(t.metricKind, result).Observe(time.Since(t.start).Seconds())
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

	// ReconcileRequestAnnotation, when changed, makes the next reconcile
	// run the full pipeline regardless of cache state. The value is opaque
	// (timestamp / UUID / commit / etc.) — the reconciler only checks
	// whether it differs from status.lastProcessedReconcileToken.
	ReconcileRequestAnnotation = "sops.stuttgart-things.com/reconcile-requested"
)

// FieldOwner is the server-side-apply / CreateOrUpdate field-manager name.
const FieldOwner = "sops-secrets-operator"

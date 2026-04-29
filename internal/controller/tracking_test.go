/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package controller

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// installInMemoryTracer swaps the global tracer provider for one that
// records every emitted span into the returned exporter. The previous
// provider is restored on test teardown.
func installInMemoryTracer(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	prev := otel.GetTracerProvider()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prev)
	})
	return exp
}

func TestReconcileTracker_HappyPath_EmitsRootAndStageSpans(t *testing.T) {
	exp := installInMemoryTracer(t)

	ctx, tr := trackReconcile(context.Background(), "SopsSecret", "ns", "obj")
	tr.SetSourceRef("GitRepository", "src")
	ctx = tr.Stage(ctx, StageFetch)
	tr.SetCommit("abc1234")
	ctx = tr.Stage(ctx, StageDecrypt)
	tr.SetContentHash("hashvalue")
	_ = tr.Stage(ctx, StageApply)
	tr.Finish()

	spans := exp.GetSpans()
	if len(spans) != 4 {
		t.Fatalf("expected 4 spans (3 stages + root), got %d", len(spans))
	}

	// The root span ends last, so it sorts last in the in-memory exporter.
	root := spans[len(spans)-1]
	if root.Name != "SopsSecret.Reconcile" {
		t.Fatalf("expected root span SopsSecret.Reconcile, got %q", root.Name)
	}

	wantAttrs := map[string]string{
		"sops.kind":         "SopsSecret",
		"sops.namespace":    "ns",
		"sops.name":         "obj",
		"sops.source.kind":  "GitRepository",
		"sops.source.name":  "src",
		"sops.commit":       "abc1234",
		"sops.content_hash": "hashvalue",
	}
	got := map[string]string{}
	for _, a := range root.Attributes {
		got[string(a.Key)] = a.Value.AsString()
	}
	for k, v := range wantAttrs {
		if got[k] != v {
			t.Errorf("root span attr %q = %q; want %q", k, got[k], v)
		}
	}

	wantStages := []string{"stage." + StageFetch, "stage." + StageDecrypt, "stage." + StageApply}
	for i, name := range wantStages {
		if spans[i].Name != name {
			t.Errorf("span[%d] name = %q; want %q", i, spans[i].Name, name)
		}
	}
}

func TestReconcileTracker_FailMarksStageAndRootErrored(t *testing.T) {
	exp := installInMemoryTracer(t)

	ctx, tr := trackReconcile(context.Background(), "SopsSecret", "ns", "obj")
	ctx = tr.Stage(ctx, StageFetch)
	_ = tr.Stage(ctx, StageDecrypt)
	tr.Fail(StageDecrypt, errors.New("boom"))
	tr.Finish()

	spans := exp.GetSpans()
	if len(spans) != 3 {
		t.Fatalf("expected 3 spans, got %d", len(spans))
	}

	// Last span is the root; the second is the decrypt stage (because
	// Stage() ends the previous fetch span before opening decrypt).
	root := spans[len(spans)-1]
	if root.Status.Code != otelcodes.Error {
		t.Errorf("root span status = %v; want Error", root.Status.Code)
	}

	var decryptSpan *tracetest.SpanStub
	for i := range spans {
		if spans[i].Name == "stage."+StageDecrypt {
			decryptSpan = &spans[i]
			break
		}
	}
	if decryptSpan == nil {
		t.Fatalf("did not find stage.decrypt span")
	}
	if decryptSpan.Status.Code != otelcodes.Error {
		t.Errorf("decrypt span status = %v; want Error", decryptSpan.Status.Code)
	}
	if len(decryptSpan.Events) == 0 {
		t.Errorf("expected RecordError event on decrypt span, got none")
	}
}

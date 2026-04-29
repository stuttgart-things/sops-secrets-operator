/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package tracing wires an OTLP/gRPC trace exporter onto OpenTelemetry's
// global TracerProvider. It is opt-in: if no OTLP endpoint is configured
// in the environment, Setup installs a no-op provider so call sites that
// start spans are still safe.
package tracing

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// ServiceName is the value reported as service.name when no
// OTEL_SERVICE_NAME is set. It also feeds the Tracer name used by the
// controllers, so spans group cleanly in collectors.
const ServiceName = "sops-secrets-operator"

// ShutdownFunc flushes pending spans and tears down the exporter. It is
// safe to call on the no-op path; in that case it is a no-op too.
type ShutdownFunc func(context.Context) error

// Setup installs an OTLP/gRPC tracer provider as the global tracer
// provider when an endpoint is configured via the standard OTEL_*
// environment variables (OTEL_EXPORTER_OTLP_ENDPOINT,
// OTEL_EXPORTER_OTLP_TRACES_ENDPOINT, …). When no endpoint is set or
// OTEL_SDK_DISABLED=true, a noop provider is installed and Setup
// returns a no-op shutdown.
func Setup(ctx context.Context, version string) (ShutdownFunc, error) {
	if disabled() {
		otel.SetTracerProvider(noop.NewTracerProvider())
		return func(context.Context) error { return nil }, nil
	}

	exp, err := otlptracegrpc.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithProcess(),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(
			semconv.ServiceName(ServiceName),
			semconv.ServiceVersion(version),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("build resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	return tp.Shutdown, nil
}

// Tracer returns the controller-scoped tracer. Using a single name keeps
// instrumentation scope consistent in the collector.
func Tracer() trace.Tracer { return otel.Tracer(ServiceName) }

// disabled reports whether tracing should be skipped. We treat a missing
// endpoint as "off" so the operator runs unchanged when OTel is not in
// use, even though the SDK can technically default to localhost.
func disabled() bool {
	if v := os.Getenv("OTEL_SDK_DISABLED"); v == "true" {
		return true
	}
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" &&
		os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") == "" {
		return true
	}
	return false
}

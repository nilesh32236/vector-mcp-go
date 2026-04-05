// Package tracing provides OpenTelemetry tracing for vector-mcp-go.
// It exports spans to an OTLP endpoint (e.g. Jaeger, Tempo) when
// OTEL_EXPORTER_OTLP_ENDPOINT is set, otherwise falls back to a no-op tracer.
package tracing

import (
	"context"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const serviceName = "vector-mcp-go"

// Provider wraps the OTel TracerProvider and exposes a Shutdown hook.
type Provider struct {
	tp       trace.TracerProvider
	shutdown func(context.Context) error
}

// Init initialises the global OTel tracer.
// If OTEL_EXPORTER_OTLP_ENDPOINT is unset, a no-op tracer is used so the
// server works without any observability infrastructure.
func Init(ctx context.Context) (*Provider, error) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		p := noop.NewTracerProvider()
		otel.SetTracerProvider(p)
		return &Provider{tp: p, shutdown: func(context.Context) error { return nil }}, nil
	}

	exp, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint(endpoint),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		return nil, err
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(serviceName)),
	)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)

	return &Provider{tp: tp, shutdown: tp.Shutdown}, nil
}

// Shutdown flushes and stops the tracer provider.
func (p *Provider) Shutdown(ctx context.Context) error {
	return p.shutdown(ctx)
}

// Tracer returns a named tracer from the global provider.
func Tracer(name string) trace.Tracer {
	return otel.Tracer(name)
}

// StartSpan is a convenience wrapper that starts a span and returns the child context.
func StartSpan(ctx context.Context, tracerName, spanName string) (context.Context, trace.Span) {
	return Tracer(tracerName).Start(ctx, spanName)
}

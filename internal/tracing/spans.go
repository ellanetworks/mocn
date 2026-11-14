package tracing

import (
	"context"

	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

func DetachedRoot(ctx context.Context) context.Context {
	return trace.ContextWithSpanContext(context.WithoutCancel(ctx), trace.SpanContext{})
}

func SpanFailed(ctx context.Context, errType string) {
	FailSpan(trace.SpanFromContext(ctx), errType)
}

func FailSpan(span trace.Span, errType string) {
	span.SetAttributes(semconv.ErrorTypeKey.String(errType))
	span.SetStatus(codes.Error, errType)
}

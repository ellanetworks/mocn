package tracing

import (
	"context"
	"crypto/rand"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// TS 36.413 clause 9.2.1.4
type RANTraceID [8]byte

func (id RANTraceID) OTelTraceID() trace.TraceID {
	var t trace.TraceID

	copy(t[8:], id[:])

	return t
}

func (id RANTraceID) IsZero() bool {
	return id == RANTraceID{}
}

type pinnedTraceIDKey struct{}

func WithPinnedTraceID(ctx context.Context, id RANTraceID) context.Context {
	if id.IsZero() {
		return ctx
	}

	return context.WithValue(ctx, pinnedTraceIDKey{}, id.OTelTraceID())
}

func PinnedTraceID(ctx context.Context) (trace.TraceID, bool) {
	id, ok := ctx.Value(pinnedTraceIDKey{}).(trace.TraceID)

	return id, ok && id.IsValid()
}

func IDGenerator() sdktrace.IDGenerator {
	return pinnedIDGenerator{}
}

type pinnedIDGenerator struct{}

func (pinnedIDGenerator) NewIDs(ctx context.Context) (trace.TraceID, trace.SpanID) {
	if id, ok := PinnedTraceID(ctx); ok {
		return id, newSpanID()
	}

	var tid trace.TraceID

	for !tid.IsValid() {
		_, _ = rand.Read(tid[:])
	}

	return tid, newSpanID()
}

func (pinnedIDGenerator) NewSpanID(context.Context, trace.TraceID) trace.SpanID {
	return newSpanID()
}

func newSpanID() trace.SpanID {
	var sid trace.SpanID

	for !sid.IsValid() {
		_, _ = rand.Read(sid[:])
	}

	return sid
}

func IsRANTraceID(id trace.TraceID) bool {
	return id.IsValid() && [8]byte(id[:8]) == [8]byte{}
}

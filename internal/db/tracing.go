package db

import (
	"context"
	"errors"
	"strconv"

	"github.com/mattn/go-sqlite3"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const tracerName = "github.com/ellanetworks/mocn/internal/db"

const poolKey = attribute.Key("ella.mocn.db.pool")

type Option func(*DB)

func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(d *DB) {
		if tp != nil {
			d.tracer = tp.Tracer(tracerName, trace.WithSchemaURL(semconv.SchemaURL))
		}
	}
}

func noopTracer() trace.Tracer {
	return noop.NewTracerProvider().Tracer(tracerName)
}

func errorType(err error) attribute.KeyValue {
	var se sqlite3.Error

	switch {
	case errors.As(err, &se):
		return semconv.ErrorTypeKey.String(strconv.Itoa(int(se.ExtendedCode)))
	case errors.Is(err, ErrBusy):
		return semconv.ErrorTypeKey.String("busy")
	case errors.Is(err, context.DeadlineExceeded):
		return semconv.ErrorTypeKey.String("timeout")
	case errors.Is(err, context.Canceled):
		return semconv.ErrorTypeKey.String("canceled")
	}

	return semconv.ErrorType(err)
}

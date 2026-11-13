package db

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/mattn/go-sqlite3"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "github.com/ellanetworks/mocn/internal/db"

type Option func(*DB)

func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(d *DB) {
		if tp != nil {
			d.tracer = tp.Tracer(tracerName)
		}
	}
}

type conn struct {
	q      querier
	tracer trace.Tracer
}

func (c conn) untraced() conn {
	return conn{q: c.q}
}

func (d *DB) read() conn {
	return conn{q: d.reader(), tracer: d.tracer}
}

func (c conn) start(ctx context.Context, operation, table string) (context.Context, func(error)) {
	if c.tracer == nil {
		return ctx, func(error) {}
	}

	name := operation
	attrs := []attribute.KeyValue{semconv.DBSystemNameSQLite}

	if operation != "" {
		attrs = append(attrs, semconv.DBOperationName(operation))
	}

	if table != "" {
		attrs = append(attrs, semconv.DBCollectionName(table))

		if name != "" {
			name += " "
		}

		name += table
	}

	if name == "" {
		name = semconv.DBSystemNameSQLite.Value.AsString()
	}

	ctx, span := c.tracer.Start(ctx, name, trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attrs...))

	return ctx, func(err error) {
		if statementFailed(err) {
			span.SetAttributes(statementErrorAttributes(err)...)
			span.SetStatus(codes.Error, semconvErrorType(err))
		}

		span.End()
	}
}

func (c conn) exec(ctx context.Context, operation, table, query string, args ...any) (sql.Result, error) {
	ctx, end := c.start(ctx, operation, table)

	res, err := c.q.ExecContext(ctx, query, args...)
	end(err)

	return res, err
}

func (c conn) row(ctx context.Context, operation, table, query string, args ...any) scanner {
	return row{c: c, ctx: ctx, operation: operation, table: table, query: query, args: args}
}

type row struct {
	c                       conn
	ctx                     context.Context
	operation, table, query string
	args                    []any
}

func (r row) Scan(dest ...any) error {
	ctx, end := r.c.start(r.ctx, r.operation, r.table)

	err := r.c.q.QueryRowContext(ctx, r.query, r.args...).Scan(dest...)
	end(err)

	return err
}

func (c conn) each(ctx context.Context, table, query string, fn func(scanner) error, args ...any) (err error) {
	ctx, end := c.start(ctx, "SELECT", table)
	defer func() { end(err) }()

	rows, err := c.q.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}

	defer func() { _ = rows.Close() }()

	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}

	return rows.Err()
}

func statementFailed(err error) bool {
	if err == nil || errors.Is(err, sql.ErrNoRows) {
		return false
	}

	_, result := codeOf(err)

	return !result
}

func semconvErrorType(err error) string {
	var se sqlite3.Error
	if errors.As(err, &se) {
		return strconv.Itoa(int(se.ExtendedCode))
	}

	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	}

	return "_OTHER"
}

func statementErrorAttributes(err error) []attribute.KeyValue {
	attrs := []attribute.KeyValue{semconv.ErrorTypeKey.String(semconvErrorType(err))}

	var se sqlite3.Error
	if errors.As(err, &se) {
		attrs = append(attrs, semconv.DBResponseStatusCode(strconv.Itoa(int(se.ExtendedCode))))
	}

	return attrs
}

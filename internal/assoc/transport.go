package assoc

import (
	"context"
	"errors"
	"net"

	"github.com/ellanetworks/core/sctp"
	"github.com/ellanetworks/mocn/internal/metrics"
	"github.com/ellanetworks/mocn/internal/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	ErrTypeInvalid   = "invalid"
	ErrTypeNoSetup   = "no_setup"
	ErrTypeUnknownUE = "unknown_ue"
	ErrTypeError     = "error"
	errTypeClosed    = "association_closed"
	errTypeSendFail  = "_OTHER"
	AttrOperator     = "ellamocn.operator"
	attrSide         = "ellamocn.side"
)

type Sender interface {
	Send(ctx context.Context, msg []byte, stream uint16) error
}

type SpanInfo struct {
	Known  bool
	Type   string
	Class1 bool
	UE     []attribute.KeyValue
}

type Transport struct {
	ppid     uint32
	protocol string
	tracer   trace.Tracer
	inspect  func(msg []byte) SpanInfo
}

func newTransport(ppid uint32, protocol string, tracer trace.Tracer, inspect func(msg []byte) SpanInfo) *Transport {
	return &Transport{ppid: ppid, protocol: protocol, tracer: tracer, inspect: inspect}
}

func (t *Transport) Tracing() bool {
	return t.tracer != nil
}

func (t *Transport) StartReceive(ctx context.Context, side string, conn *sctp.SCTPConn, info SpanInfo, size int,
	operator string,
) (context.Context, trace.Span) {
	return t.start(ctx, true, side, conn, info, size, operator)
}

func (t *Transport) start(ctx context.Context, receive bool, side string, conn *sctp.SCTPConn, info SpanInfo,
	size int, operator string,
) (context.Context, trace.Span) {
	name := t.protocol + "/send"
	if receive {
		name = t.protocol + "/receive"
	}

	attrs := []attribute.KeyValue{
		attribute.String("network.protocol.name", t.protocol),
		attribute.String("network.transport", "sctp"),
		attribute.Int(t.protocol+".message_size", size),
		attribute.String(attrSide, side),
	}

	if conn != nil {
		if a := conn.RemoteAddr(); a != nil {
			attrs = append(attrs, attribute.String("network.peer.address", a.String()))
		}

		if a := conn.LocalAddr(); a != nil {
			attrs = append(attrs, attribute.String("network.local.address", a.String()))
		}
	}

	if info.Known {
		attrs = append(attrs, attribute.String(t.protocol+".message_type", info.Type))
	}

	if operator != "" {
		attrs = append(attrs, attribute.String(AttrOperator, operator))
	}

	attrs = append(attrs, info.UE...)

	return t.tracer.Start(ctx, name, trace.WithSpanKind(spanKind(info, receive)), trace.WithAttributes(attrs...))
}

// TS 36.413 clause 8.1
// TS 38.413 clause 8.1
func spanKind(info SpanInfo, receive bool) trace.SpanKind {
	switch {
	case info.Known && info.Class1 && receive:
		return trace.SpanKindServer
	case info.Known && info.Class1:
		return trace.SpanKindClient
	case receive:
		return trace.SpanKindConsumer
	default:
		return trace.SpanKindProducer
	}
}

func (t *Transport) Send(ctx context.Context, side string, conn *sctp.SCTPConn, operator string, msg []byte,
	stream uint16,
) error {
	if t.tracer == nil {
		return t.write(conn, msg, stream)
	}

	_, span := t.start(ctx, false, side, conn, t.inspect(msg), len(msg), operator)
	defer span.End()

	err := t.write(conn, msg, stream)
	if err != nil {
		errType := errTypeSendFail
		if errors.Is(err, net.ErrClosed) {
			errType = errTypeClosed
		}

		tracing.FailSpan(span, errType)
	}

	return err
}

func (t *Transport) write(conn *sctp.SCTPConn, msg []byte, stream uint16) error {
	if conn == nil {
		return net.ErrClosed
	}

	_, err := conn.WriteMsg(msg, &sctp.SndRcvInfo{Stream: stream, PPID: sctp.PPIDWireOrder(t.ppid)})

	return err
}

func (t *Transport) sendRadio(ctx context.Context, conn *sctp.SCTPConn, operator string, msg []byte, stream uint16) error {
	return t.Send(ctx, metrics.SideRadio, conn, operator, msg, stream)
}

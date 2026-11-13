package s1

import (
	"context"
	"errors"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/core/sctp"
	"github.com/ellanetworks/mocn/internal/gw"
	"github.com/ellanetworks/mocn/internal/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	tracerName      = "github.com/ellanetworks/mocn/internal/s1"
	spanReceive     = "s1ap/receive"
	spanSend        = "s1ap/send"
	attrSide        = "ellamocn.side"
	attrOperator    = "ellamocn.operator"
	attrENBUEID     = "s1ap.enb_ue_s1ap_id"
	attrMMEUEID     = "s1ap.mme_ue_s1ap_id"
	unknownMessage  = "UnknownMessage"
	errTypeInvalid  = "invalid"
	errTypeNoSetup  = "no_setup"
	errTypeUnknown  = "unknown_ue"
	errTypeError    = "error"
	errTypeClosed   = "association_closed"
	errTypeSendFail = "_OTHER"
)

type procedureMessages struct {
	class        int
	initiating   string
	successful   string
	unsuccessful string
}

// TS 36.413 clause 8.1
// TS 36.413 clause 9.3.2
var procedures = map[s1ap.ProcedureCode]procedureMessages{
	s1ap.ProcHandoverPreparation:                  {1, "HandoverRequired", "HandoverCommand", "HandoverPreparationFailure"},
	s1ap.ProcHandoverResourceAllocation:           {1, "HandoverRequest", "HandoverRequestAcknowledge", "HandoverFailure"},
	s1ap.ProcHandoverNotification:                 {2, "HandoverNotify", "", ""},
	s1ap.ProcPathSwitchRequest:                    {1, "PathSwitchRequest", "PathSwitchRequestAcknowledge", "PathSwitchRequestFailure"},
	s1ap.ProcHandoverCancel:                       {1, "HandoverCancel", "HandoverCancelAcknowledge", ""},
	s1ap.ProcERABSetup:                            {1, "E-RABSetupRequest", "E-RABSetupResponse", ""},
	s1ap.ProcERABModify:                           {1, "E-RABModifyRequest", "E-RABModifyResponse", ""},
	s1ap.ProcERABRelease:                          {1, "E-RABReleaseCommand", "E-RABReleaseResponse", ""},
	s1ap.ProcERABReleaseIndication:                {2, "E-RABReleaseIndication", "", ""},
	s1ap.ProcInitialContextSetup:                  {1, "InitialContextSetupRequest", "InitialContextSetupResponse", "InitialContextSetupFailure"},
	s1ap.ProcPaging:                               {2, "Paging", "", ""},
	s1ap.ProcDownlinkNASTransport:                 {2, "DownlinkNASTransport", "", ""},
	s1ap.ProcInitialUEMessage:                     {2, "InitialUEMessage", "", ""},
	s1ap.ProcUplinkNASTransport:                   {2, "UplinkNASTransport", "", ""},
	s1ap.ProcReset:                                {1, "Reset", "ResetAcknowledge", ""},
	s1ap.ProcErrorIndication:                      {2, "ErrorIndication", "", ""},
	s1ap.ProcNASNonDeliveryIndication:             {2, "NASNonDeliveryIndication", "", ""},
	s1ap.ProcS1Setup:                              {1, "S1SetupRequest", "S1SetupResponse", "S1SetupFailure"},
	s1ap.ProcUEContextReleaseRequest:              {2, "UEContextReleaseRequest", "", ""},
	s1ap.ProcDownlinkS1cdma2000tunnelling:         {2, "DownlinkS1cdma2000tunnelling", "", ""},
	s1ap.ProcUplinkS1cdma2000tunnelling:           {2, "UplinkS1cdma2000tunnelling", "", ""},
	s1ap.ProcUEContextModification:                {1, "UEContextModificationRequest", "UEContextModificationResponse", "UEContextModificationFailure"},
	s1ap.ProcUECapabilityInfoIndication:           {2, "UECapabilityInfoIndication", "", ""},
	s1ap.ProcUEContextRelease:                     {1, "UEContextReleaseCommand", "UEContextReleaseComplete", ""},
	s1ap.ProcENBStatusTransfer:                    {2, "ENBStatusTransfer", "", ""},
	s1ap.ProcMMEStatusTransfer:                    {2, "MMEStatusTransfer", "", ""},
	s1ap.ProcDeactivateTrace:                      {2, "DeactivateTrace", "", ""},
	s1ap.ProcTraceStart:                           {2, "TraceStart", "", ""},
	s1ap.ProcTraceFailureIndication:               {2, "TraceFailureIndication", "", ""},
	s1ap.ProcENBConfigurationUpdate:               {1, "ENBConfigurationUpdate", "ENBConfigurationUpdateAcknowledge", "ENBConfigurationUpdateFailure"},
	s1ap.ProcMMEConfigurationUpdate:               {1, "MMEConfigurationUpdate", "MMEConfigurationUpdateAcknowledge", "MMEConfigurationUpdateFailure"},
	s1ap.ProcLocationReportingControl:             {2, "LocationReportingControl", "", ""},
	s1ap.ProcLocationReportingFailureIndication:   {2, "LocationReportingFailureIndication", "", ""},
	s1ap.ProcLocationReport:                       {2, "LocationReport", "", ""},
	s1ap.ProcOverloadStart:                        {2, "OverloadStart", "", ""},
	s1ap.ProcOverloadStop:                         {2, "OverloadStop", "", ""},
	s1ap.ProcWriteReplaceWarning:                  {1, "WriteReplaceWarningRequest", "WriteReplaceWarningResponse", ""},
	s1ap.ProcENBDirectInformationTransfer:         {2, "ENBDirectInformationTransfer", "", ""},
	s1ap.ProcMMEDirectInformationTransfer:         {2, "MMEDirectInformationTransfer", "", ""},
	s1ap.ProcPrivateMessage:                       {2, "PrivateMessage", "", ""},
	s1ap.ProcENBConfigurationTransfer:             {2, "ENBConfigurationTransfer", "", ""},
	s1ap.ProcMMEConfigurationTransfer:             {2, "MMEConfigurationTransfer", "", ""},
	s1ap.ProcCellTrafficTrace:                     {2, "CellTrafficTrace", "", ""},
	s1ap.ProcKill:                                 {1, "KillRequest", "KillResponse", ""},
	s1ap.ProcDownlinkUEAssociatedLPPaTransport:    {2, "DownlinkUEAssociatedLPPaTransport", "", ""},
	s1ap.ProcUplinkUEAssociatedLPPaTransport:      {2, "UplinkUEAssociatedLPPaTransport", "", ""},
	s1ap.ProcDownlinkNonUEAssociatedLPPaTransport: {2, "DownlinkNonUEAssociatedLPPaTransport", "", ""},
	s1ap.ProcUplinkNonUEAssociatedLPPaTransport:   {2, "UplinkNonUEAssociatedLPPaTransport", "", ""},
	s1ap.ProcUERadioCapabilityMatch:               {1, "UERadioCapabilityMatchRequest", "UERadioCapabilityMatchResponse", ""},
	s1ap.ProcPWSRestartIndication:                 {2, "PWSRestartIndication", "", ""},
	s1ap.ProcERABModificationIndication:           {1, "E-RABModificationIndication", "E-RABModificationConfirm", ""},
	s1ap.ProcPWSFailureIndication:                 {2, "PWSFailureIndication", "", ""},
	s1ap.ProcRerouteNASRequest:                    {2, "RerouteNASRequest", "", ""},
	s1ap.ProcUEContextModificationIndication:      {1, "UEContextModificationIndication", "UEContextModificationConfirm", ""},
	s1ap.ProcConnectionEstablishmentIndication:    {2, "ConnectionEstablishmentIndication", "", ""},
	s1ap.ProcUEContextSuspend:                     {1, "UEContextSuspendRequest", "UEContextSuspendResponse", ""},
	s1ap.ProcUEContextResume:                      {1, "UEContextResumeRequest", "UEContextResumeResponse", "UEContextResumeFailure"},
	s1ap.ProcNASDeliveryIndication:                {2, "NASDeliveryIndication", "", ""},
	s1ap.ProcRetrieveUEInformation:                {2, "RetrieveUEInformation", "", ""},
	s1ap.ProcUEInformationTransfer:                {2, "UEInformationTransfer", "", ""},
	s1ap.ProcENBCPRelocationIndication:            {2, "ENBCPRelocationIndication", "", ""},
	s1ap.ProcMMECPRelocationIndication:            {2, "MMECPRelocationIndication", "", ""},
	s1ap.ProcSecondaryRATDataUsageReport:          {2, "SecondaryRATDataUsageReport", "", ""},
	s1ap.ProcUERadioCapabilityIDMapping:           {1, "UERadioCapabilityIDMappingRequest", "UERadioCapabilityIDMappingResponse", ""},
	s1ap.ProcHandoverSuccess:                      {2, "HandoverSuccess", "", ""},
	s1ap.ProcENBEarlyStatusTransfer:               {2, "ENBEarlyStatusTransfer", "", ""},
	s1ap.ProcMMEEarlyStatusTransfer:               {2, "MMEEarlyStatusTransfer", "", ""},
}

type msgInfo struct {
	proc    s1ap.ProcedureCode
	trigger s1ap.TriggeringMessage
	known   bool
	raw     *s1ap.RawPDU
	ids     ueIDs
}

func inspect(pdu s1ap.PDU, msg []byte) msgInfo {
	var m msgInfo

	switch p := pdu.(type) {
	case *s1ap.InitiatingMessage:
		m.proc, m.trigger, m.known = p.ProcedureCode, s1ap.TriggeringInitiatingMessage, true
	case *s1ap.SuccessfulOutcome:
		m.proc, m.trigger, m.known = p.ProcedureCode, s1ap.TriggeringSuccessfulOutcome, true
	case *s1ap.UnsuccessfulOutcome:
		m.proc, m.trigger, m.known = p.ProcedureCode, s1ap.TriggeringUnsuccessfulOutcome, true
	}

	if raw, err := s1ap.ParseRawPDU(msg); err == nil {
		m.raw, m.proc, m.trigger, m.known = raw, raw.ProcedureCode(), raw.Trigger(), true
		m.ids, _, _ = readUEIDs(raw)
	}

	return m
}

func (m msgInfo) messageType() string {
	p, ok := procedures[m.proc]
	if !ok {
		return unknownMessage
	}

	name := map[s1ap.TriggeringMessage]string{
		s1ap.TriggeringInitiatingMessage:   p.initiating,
		s1ap.TriggeringSuccessfulOutcome:   p.successful,
		s1ap.TriggeringUnsuccessfulOutcome: p.unsuccessful,
	}[m.trigger]

	if name == "" {
		return unknownMessage
	}

	return name
}

// TS 36.413 clause 8.1
func (m msgInfo) spanKind(receive bool) trace.SpanKind {
	switch {
	case m.known && procedures[m.proc].class == 1 && receive:
		return trace.SpanKindServer
	case m.known && procedures[m.proc].class == 1:
		return trace.SpanKindClient
	case receive:
		return trace.SpanKindConsumer
	default:
		return trace.SpanKindProducer
	}
}

// TS 36.413 clause 9.2.1.4
func traceActivationOf(p *s1ap.RawPDU) (tracing.RANTraceID, bool) {
	if p == nil || p.Trigger() != s1ap.TriggeringInitiatingMessage {
		return tracing.RANTraceID{}, false
	}

	var act s1ap.TraceActivation

	if ok, err := p.Get(s1ap.IDTraceActivation, &act); err != nil || !ok {
		return tracing.RANTraceID{}, false
	}

	return tracing.RANTraceID(act.EUTRANTraceID), true
}

// TS 36.413 clause 8.10.1
// TS 36.413 clause 8.10.3
func (u *ueContext) trace(p *s1ap.RawPDU) {
	if id, ok := traceActivationOf(p); ok {
		u.traceID = id
		return
	}

	if p.Trigger() != s1ap.TriggeringInitiatingMessage || p.ProcedureCode() != s1ap.ProcDeactivateTrace {
		return
	}

	var id s1ap.EUTRANTraceID

	if ok, err := p.Get(s1ap.IDEUTRANTraceID, &id); err == nil && ok && [6]byte(id[:6]) == [6]byte(u.traceID[:6]) {
		u.traceID = tracing.RANTraceID{}
	}
}

func (g *Gateway) startSpan(ctx context.Context, name, side string, conn *sctp.SCTPConn, info msgInfo, size int,
	operator string,
) (context.Context, trace.Span) {
	receive := name == spanReceive

	attrs := []attribute.KeyValue{
		attribute.String("network.protocol.name", "s1ap"),
		attribute.String("network.transport", "sctp"),
		attribute.Int("s1ap.message_size", size),
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

	if info.known {
		attrs = append(attrs, attribute.String("s1ap.message_type", info.messageType()))
	}

	if operator != "" {
		attrs = append(attrs, attribute.String(attrOperator, operator))
	}

	if info.ids.enb != nil {
		attrs = append(attrs, attribute.Int64(attrENBUEID, int64(*info.ids.enb)))
	}

	if info.ids.mme != nil {
		attrs = append(attrs, attribute.Int64(attrMMEUEID, int64(*info.ids.mme)))
	}

	return g.tracer.Start(ctx, name, trace.WithSpanKind(info.spanKind(receive)), trace.WithAttributes(attrs...))
}

func (g *Gateway) receiveFromRAN(ctx context.Context, n *RANAssociation, pdu s1ap.PDU, msg []byte) (context.Context, trace.Span) {
	if g.tracer == nil {
		return ctx, trace.SpanFromContext(ctx)
	}

	info := inspect(pdu, msg)
	operator := ""

	id, operatorID, ok := g.relay.ranUETrace(n, info.ids)
	if ok {
		operator = g.operatorName(operatorID)
	}

	return g.startSpan(tracing.WithPinnedTraceID(ctx, id), spanReceive, sideRadio, n.conn, info, len(msg), operator)
}

func (g *Gateway) receiveFromMME(ctx context.Context, a *MMEAssociation, pdu s1ap.PDU, msg []byte) (context.Context, trace.Span) {
	if g.tracer == nil {
		return ctx, trace.SpanFromContext(ctx)
	}

	info := inspect(pdu, msg)
	id := g.relay.mmeUETrace(a, info)

	return g.startSpan(tracing.WithPinnedTraceID(ctx, id), spanReceive, sideCore, a.conn, info, len(msg), a.client.operator())
}

func (g *Gateway) operatorName(id int64) string {
	g.mu.Lock()
	defer g.mu.Unlock()

	o, _ := g.operatorLocked(id)

	return o.name
}

func (r *relay) ranUETrace(n *RANAssociation, ids ueIDs) (tracing.RANTraceID, int64, bool) {
	if ids.mme == nil {
		return tracing.RANTraceID{}, 0, false
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	u := r.tracedRANUELocked(n, ids)
	if u == nil {
		return tracing.RANTraceID{}, 0, false
	}

	return u.traceID, u.operatorID, true
}

func (r *relay) tracedRANUELocked(n *RANAssociation, ids ueIDs) *ueContext {
	if ids.mme == nil {
		return nil
	}

	u := r.ues.byRANMMEID[*ids.mme]
	if u == nil || u.ran != n || u.mme == nil {
		return nil
	}

	return u
}

func (r *relay) mmeUETrace(a *MMEAssociation, info msgInfo) tracing.RANTraceID {
	r.mu.Lock()
	defer r.mu.Unlock()

	var u *ueContext

	switch {
	case info.ids.enb != nil:
		u = r.ues.byMMEENBID[*info.ids.enb]
	case info.ids.mme != nil:
		u = r.ues.byMMEID[mmeUEKey{a, *info.ids.mme}]
	}

	if u == nil || u.mme != a {
		return tracing.RANTraceID{}
	}

	if id, ok := traceActivationOf(info.raw); ok {
		return id
	}

	return u.traceID
}

func (g *Gateway) send(ctx context.Context, side string, conn *sctp.SCTPConn, operator string, msg []byte,
	stream uint16,
) error {
	if g == nil || g.tracer == nil {
		return gw.Send(conn, PPID, msg, stream)
	}

	_, span := g.startSpan(ctx, spanSend, side, conn, inspect(nil, msg), len(msg), operator)
	defer span.End()

	err := gw.Send(conn, PPID, msg, stream)
	if err != nil {
		errType := errTypeSendFail
		if errors.Is(err, gw.ErrClosed) {
			errType = errTypeClosed
		}

		tracing.FailSpan(span, errType)
	}

	return err
}

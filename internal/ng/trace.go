package ng

import (
	"context"
	"errors"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/core/sctp"
	"github.com/ellanetworks/mocn/internal/gw"
	"github.com/ellanetworks/mocn/internal/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	tracerName      = "github.com/ellanetworks/mocn/internal/ng"
	spanReceive     = "ngap/receive"
	spanSend        = "ngap/send"
	attrSide        = "ellamocn.side"
	attrOperator    = "ellamocn.operator"
	attrRANUEID     = "ngap.ran_ue_ngap_id"
	attrAMFUEID     = "ngap.amf_ue_ngap_id"
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

// TS 38.413 clause 8.1
// TS 38.413 clause 9.4.3
var procedures = map[ngap.ProcedureCode]procedureMessages{
	ngap.ProcAMFStatusIndication:                   {2, "AMFStatusIndication", "", ""},
	ngap.ProcCellTrafficTrace:                      {2, "CellTrafficTrace", "", ""},
	ngap.ProcAMFConfigurationUpdate:                {1, "AMFConfigurationUpdate", "AMFConfigurationUpdateAcknowledge", "AMFConfigurationUpdateFailure"},
	ngap.ProcDeactivateTrace:                       {2, "DeactivateTrace", "", ""},
	ngap.ProcDownlinkNASTransport:                  {2, "DownlinkNASTransport", "", ""},
	ngap.ProcDownlinkNonUEAssociatedNRPPaTransport: {2, "DownlinkNonUEAssociatedNRPPaTransport", "", ""},
	ngap.ProcDownlinkRANConfigurationTransfer:      {2, "DownlinkRANConfigurationTransfer", "", ""},
	ngap.ProcDownlinkRANStatusTransfer:             {2, "DownlinkRANStatusTransfer", "", ""},
	ngap.ProcDownlinkUEAssociatedNRPPaTransport:    {2, "DownlinkUEAssociatedNRPPaTransport", "", ""},
	ngap.ProcErrorIndication:                       {2, "ErrorIndication", "", ""},
	ngap.ProcHandoverCancel:                        {1, "HandoverCancel", "HandoverCancelAcknowledge", ""},
	ngap.ProcHandoverNotification:                  {2, "HandoverNotify", "", ""},
	ngap.ProcHandoverPreparation:                   {1, "HandoverRequired", "HandoverCommand", "HandoverPreparationFailure"},
	ngap.ProcHandoverResourceAllocation:            {1, "HandoverRequest", "HandoverRequestAcknowledge", "HandoverFailure"},
	ngap.ProcInitialContextSetup:                   {1, "InitialContextSetupRequest", "InitialContextSetupResponse", "InitialContextSetupFailure"},
	ngap.ProcInitialUEMessage:                      {2, "InitialUEMessage", "", ""},
	ngap.ProcLocationReportingControl:              {2, "LocationReportingControl", "", ""},
	ngap.ProcLocationReportingFailureIndication:    {2, "LocationReportingFailureIndication", "", ""},
	ngap.ProcLocationReport:                        {2, "LocationReport", "", ""},
	ngap.ProcNASNonDeliveryIndication:              {2, "NASNonDeliveryIndication", "", ""},
	ngap.ProcNGReset:                               {1, "NGReset", "NGResetAcknowledge", ""},
	ngap.ProcNGSetup:                               {1, "NGSetupRequest", "NGSetupResponse", "NGSetupFailure"},
	ngap.ProcOverloadStart:                         {2, "OverloadStart", "", ""},
	ngap.ProcOverloadStop:                          {2, "OverloadStop", "", ""},
	ngap.ProcPaging:                                {2, "Paging", "", ""},
	ngap.ProcPathSwitchRequest:                     {1, "PathSwitchRequest", "PathSwitchRequestAcknowledge", "PathSwitchRequestFailure"},
	ngap.ProcPDUSessionResourceModify:              {1, "PDUSessionResourceModifyRequest", "PDUSessionResourceModifyResponse", ""},
	ngap.ProcPDUSessionResourceModifyIndication:    {1, "PDUSessionResourceModifyIndication", "PDUSessionResourceModifyConfirm", ""},
	ngap.ProcPDUSessionResourceRelease:             {1, "PDUSessionResourceReleaseCommand", "PDUSessionResourceReleaseResponse", ""},
	ngap.ProcPDUSessionResourceSetup:               {1, "PDUSessionResourceSetupRequest", "PDUSessionResourceSetupResponse", ""},
	ngap.ProcPDUSessionResourceNotify:              {2, "PDUSessionResourceNotify", "", ""},
	ngap.ProcPrivateMessage:                        {2, "PrivateMessage", "", ""},
	ngap.ProcPWSCancel:                             {1, "PWSCancelRequest", "PWSCancelResponse", ""},
	ngap.ProcPWSFailureIndication:                  {2, "PWSFailureIndication", "", ""},
	ngap.ProcPWSRestartIndication:                  {2, "PWSRestartIndication", "", ""},
	ngap.ProcRANConfigurationUpdate:                {1, "RANConfigurationUpdate", "RANConfigurationUpdateAcknowledge", "RANConfigurationUpdateFailure"},
	ngap.ProcRerouteNASRequest:                     {2, "RerouteNASRequest", "", ""},
	ngap.ProcRRCInactiveTransitionReport:           {2, "RRCInactiveTransitionReport", "", ""},
	ngap.ProcTraceFailureIndication:                {2, "TraceFailureIndication", "", ""},
	ngap.ProcTraceStart:                            {2, "TraceStart", "", ""},
	ngap.ProcUEContextModification:                 {1, "UEContextModificationRequest", "UEContextModificationResponse", "UEContextModificationFailure"},
	ngap.ProcUEContextRelease:                      {1, "UEContextReleaseCommand", "UEContextReleaseComplete", ""},
	ngap.ProcUEContextReleaseRequest:               {2, "UEContextReleaseRequest", "", ""},
	ngap.ProcUERadioCapabilityCheck:                {1, "UERadioCapabilityCheckRequest", "UERadioCapabilityCheckResponse", ""},
	ngap.ProcUERadioCapabilityInfoIndication:       {2, "UERadioCapabilityInfoIndication", "", ""},
	ngap.ProcUETNLABindingRelease:                  {2, "UETNLABindingReleaseRequest", "", ""},
	ngap.ProcUplinkNASTransport:                    {2, "UplinkNASTransport", "", ""},
	ngap.ProcUplinkNonUEAssociatedNRPPaTransport:   {2, "UplinkNonUEAssociatedNRPPaTransport", "", ""},
	ngap.ProcUplinkRANConfigurationTransfer:        {2, "UplinkRANConfigurationTransfer", "", ""},
	ngap.ProcUplinkRANStatusTransfer:               {2, "UplinkRANStatusTransfer", "", ""},
	ngap.ProcUplinkUEAssociatedNRPPaTransport:      {2, "UplinkUEAssociatedNRPPaTransport", "", ""},
	ngap.ProcWriteReplaceWarning:                   {1, "WriteReplaceWarningRequest", "WriteReplaceWarningResponse", ""},
	ngap.ProcSecondaryRATDataUsageReport:           {2, "SecondaryRATDataUsageReport", "", ""},
	ngap.ProcUplinkRIMInformationTransfer:          {2, "UplinkRIMInformationTransfer", "", ""},
	ngap.ProcDownlinkRIMInformationTransfer:        {2, "DownlinkRIMInformationTransfer", "", ""},
	ngap.ProcRetrieveUEInformation:                 {2, "RetrieveUEInformation", "", ""},
	ngap.ProcUEInformationTransfer:                 {2, "UEInformationTransfer", "", ""},
	ngap.ProcRANCPRelocationIndication:             {2, "RANCPRelocationIndication", "", ""},
	ngap.ProcUEContextResume:                       {1, "UEContextResumeRequest", "UEContextResumeResponse", "UEContextResumeFailure"},
	ngap.ProcUEContextSuspend:                      {1, "UEContextSuspendRequest", "UEContextSuspendResponse", "UEContextSuspendFailure"},
	ngap.ProcUERadioCapabilityIDMapping:            {1, "UERadioCapabilityIDMappingRequest", "UERadioCapabilityIDMappingResponse", ""},
	ngap.ProcHandoverSuccess:                       {2, "HandoverSuccess", "", ""},
	ngap.ProcUplinkRANEarlyStatusTransfer:          {2, "UplinkRANEarlyStatusTransfer", "", ""},
	ngap.ProcDownlinkRANEarlyStatusTransfer:        {2, "DownlinkRANEarlyStatusTransfer", "", ""},
	ngap.ProcAMFCPRelocationIndication:             {2, "AMFCPRelocationIndication", "", ""},
	ngap.ProcConnectionEstablishmentIndication:     {2, "ConnectionEstablishmentIndication", "", ""},
	ngap.ProcBroadcastSessionModification:          {1, "BroadcastSessionModificationRequest", "BroadcastSessionModificationResponse", "BroadcastSessionModificationFailure"},
	ngap.ProcBroadcastSessionRelease:               {1, "BroadcastSessionReleaseRequest", "BroadcastSessionReleaseResponse", ""},
	ngap.ProcBroadcastSessionSetup:                 {1, "BroadcastSessionSetupRequest", "BroadcastSessionSetupResponse", "BroadcastSessionSetupFailure"},
	ngap.ProcDistributionSetup:                     {1, "DistributionSetupRequest", "DistributionSetupResponse", "DistributionSetupFailure"},
	ngap.ProcDistributionRelease:                   {1, "DistributionReleaseRequest", "DistributionReleaseResponse", ""},
	ngap.ProcMulticastSessionActivation:            {1, "MulticastSessionActivationRequest", "MulticastSessionActivationResponse", "MulticastSessionActivationFailure"},
	ngap.ProcMulticastSessionDeactivation:          {1, "MulticastSessionDeactivationRequest", "MulticastSessionDeactivationResponse", ""},
	ngap.ProcMulticastSessionUpdate:                {1, "MulticastSessionUpdateRequest", "MulticastSessionUpdateResponse", "MulticastSessionUpdateFailure"},
	ngap.ProcMulticastGroupPaging:                  {2, "MulticastGroupPaging", "", ""},
	ngap.ProcBroadcastSessionReleaseRequired:       {2, "BroadcastSessionReleaseRequired", "", ""},
	ngap.ProcTimingSynchronisationStatus:           {1, "TimingSynchronisationStatusRequest", "TimingSynchronisationStatusResponse", "TimingSynchronisationStatusFailure"},
	ngap.ProcTimingSynchronisationStatusReport:     {2, "TimingSynchronisationStatusReport", "", ""},
	ngap.ProcMTCommunicationHandling:               {1, "MTCommunicationHandlingRequest", "MTCommunicationHandlingResponse", "MTCommunicationHandlingFailure"},
	ngap.ProcRANPagingRequest:                      {2, "RANPagingRequest", "", ""},
	ngap.ProcBroadcastSessionTransport:             {1, "BroadcastSessionTransportRequest", "BroadcastSessionTransportResponse", "BroadcastSessionTransportFailure"},
}

type msgInfo struct {
	proc    ngap.ProcedureCode
	trigger ngap.TriggeringMessage
	known   bool
	raw     *ngap.RawPDU
	ids     ueIDs
}

func inspect(pdu ngap.PDU, msg []byte) msgInfo {
	var m msgInfo

	switch p := pdu.(type) {
	case *ngap.InitiatingMessage:
		m.proc, m.trigger, m.known = p.ProcedureCode, ngap.TriggeringInitiatingMessage, true
	case *ngap.SuccessfulOutcome:
		m.proc, m.trigger, m.known = p.ProcedureCode, ngap.TriggeringSuccessfulOutcome, true
	case *ngap.UnsuccessfulOutcome:
		m.proc, m.trigger, m.known = p.ProcedureCode, ngap.TriggeringUnsuccessfulOutcome, true
	}

	if raw, err := ngap.ParseRawPDU(msg); err == nil {
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

	name := map[ngap.TriggeringMessage]string{
		ngap.TriggeringInitiatingMessage:   p.initiating,
		ngap.TriggeringSuccessfulOutcome:   p.successful,
		ngap.TriggeringUnsuccessfulOutcome: p.unsuccessful,
	}[m.trigger]

	if name == "" {
		return unknownMessage
	}

	return name
}

// TS 38.413 clause 8.1
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

// TS 38.413 clause 9.3.1.14
func traceActivationOf(p *ngap.RawPDU) (tracing.RANTraceID, bool) {
	if p == nil || p.Trigger() != ngap.TriggeringInitiatingMessage {
		return tracing.RANTraceID{}, false
	}

	var act ngap.TraceActivation

	if ok, err := p.Get(ngap.IDTraceActivation, &act); err != nil || !ok {
		return tracing.RANTraceID{}, false
	}

	return tracing.RANTraceID(act.NGRANTraceID), true
}

// TS 38.413 clause 8.11.1
// TS 38.413 clause 8.11.3
func (u *ueContext) trace(p *ngap.RawPDU) {
	if id, ok := traceActivationOf(p); ok {
		u.traceID = id
		return
	}

	if p.Trigger() != ngap.TriggeringInitiatingMessage || p.ProcedureCode() != ngap.ProcDeactivateTrace {
		return
	}

	var id ngap.NGRANTraceID

	if ok, err := p.Get(ngap.IDNGRANTraceID, &id); err == nil && ok && [6]byte(id[:6]) == [6]byte(u.traceID[:6]) {
		u.traceID = tracing.RANTraceID{}
	}
}

func (g *Gateway) startSpan(ctx context.Context, name, side string, conn *sctp.SCTPConn, info msgInfo, size int,
	operator string,
) (context.Context, trace.Span) {
	receive := name == spanReceive

	attrs := []attribute.KeyValue{
		attribute.String("network.protocol.name", "ngap"),
		attribute.String("network.transport", "sctp"),
		attribute.Int("ngap.message_size", size),
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
		attrs = append(attrs, attribute.String("ngap.message_type", info.messageType()))
	}

	if operator != "" {
		attrs = append(attrs, attribute.String(attrOperator, operator))
	}

	if info.ids.ran != nil {
		attrs = append(attrs, attribute.Int64(attrRANUEID, int64(*info.ids.ran)))
	}

	if info.ids.amf != nil {
		attrs = append(attrs, attribute.Int64(attrAMFUEID, int64(*info.ids.amf)))
	}

	return g.tracer.Start(ctx, name, trace.WithSpanKind(info.spanKind(receive)), trace.WithAttributes(attrs...))
}

func (g *Gateway) receiveFromRAN(ctx context.Context, n *RANAssociation, pdu ngap.PDU, msg []byte) (context.Context, trace.Span) {
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

func (g *Gateway) receiveFromAMF(ctx context.Context, a *AMFAssociation, pdu ngap.PDU, msg []byte) (context.Context, trace.Span) {
	if g.tracer == nil {
		return ctx, trace.SpanFromContext(ctx)
	}

	info := inspect(pdu, msg)
	id := g.relay.amfUETrace(a, info)

	return g.startSpan(tracing.WithPinnedTraceID(ctx, id), spanReceive, sideCore, a.conn, info, len(msg), a.client.operator())
}

func (g *Gateway) operatorName(id int64) string {
	g.mu.Lock()
	defer g.mu.Unlock()

	o, _ := g.operatorLocked(id)

	return o.name
}

func (r *relay) ranUETrace(n *RANAssociation, ids ueIDs) (tracing.RANTraceID, int64, bool) {
	if ids.amf == nil {
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
	if ids.amf == nil {
		return nil
	}

	u := r.ues.byRANAMFID[*ids.amf]
	if u == nil || u.ran != n || u.amf == nil {
		return nil
	}

	return u
}

func (r *relay) amfUETrace(a *AMFAssociation, info msgInfo) tracing.RANTraceID {
	r.mu.Lock()
	defer r.mu.Unlock()

	var u *ueContext

	switch {
	case info.ids.ran != nil:
		u = r.ues.byAMFRANID[*info.ids.ran]
	case info.ids.amf != nil:
		u = r.ues.byAMFID[amfUEKey{a, *info.ids.amf}]
	}

	if u == nil || u.amf != a {
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

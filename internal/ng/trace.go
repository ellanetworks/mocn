package ng

import (
	"context"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/metrics"
	"github.com/ellanetworks/mocn/internal/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	tracerName     = "github.com/ellanetworks/mocn/internal/ng"
	protocolName   = "ngap"
	attrRANUEID    = "ngap.ran_ue_ngap_id"
	attrAMFUEID    = "ngap.amf_ue_ngap_id"
	unknownMessage = "UnknownMessage"
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
func (m msgInfo) span() assoc.SpanInfo {
	info := assoc.SpanInfo{Known: m.known, Class1: m.known && procedures[m.proc].class == 1}

	if m.known {
		info.Type = m.messageType()
	}

	if m.ids.ran != nil {
		info.UE = append(info.UE, attribute.Int64(attrRANUEID, int64(*m.ids.ran)))
	}

	if m.ids.amf != nil {
		info.UE = append(info.UE, attribute.Int64(attrAMFUEID, int64(*m.ids.amf)))
	}

	return info
}

func spanInfo(msg []byte) assoc.SpanInfo {
	return inspect(nil, msg).span()
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

func tracesUE(p *ngap.RawPDU) bool {
	return p.Trigger() == ngap.TriggeringInitiatingMessage &&
		(present(p, ngap.IDTraceActivation) || p.ProcedureCode() == ngap.ProcDeactivateTrace)
}

// TS 38.413 clause 8.11.1
// TS 38.413 clause 8.11.3
func traceUE(u *ueContext, p *ngap.RawPDU) {
	if id, ok := traceActivationOf(p); ok {
		u.TraceID = id
		return
	}

	if p.Trigger() != ngap.TriggeringInitiatingMessage || p.ProcedureCode() != ngap.ProcDeactivateTrace {
		return
	}

	var id ngap.NGRANTraceID

	if ok, err := p.Get(ngap.IDNGRANTraceID, &id); err == nil && ok && [6]byte(id[:6]) == [6]byte(u.TraceID[:6]) {
		u.TraceID = tracing.RANTraceID{}
	}
}

func (g *Gateway) receiveFromRAN(ctx context.Context, n *ranAssoc, pdu ngap.PDU, msg []byte) (context.Context, trace.Span) {
	tx := g.core.Transport()
	if !tx.Tracing() {
		return ctx, trace.SpanFromContext(ctx)
	}

	info := inspect(pdu, msg)
	operator := ""

	id, operatorID, ok := g.relay.ranUETrace(n, info.ids)
	if ok {
		operator = g.core.OperatorName(operatorID)
	}

	return tx.StartReceive(tracing.WithPinnedTraceID(ctx, id), metrics.SideRadio, n.Conn(), info.span(), len(msg), operator)
}

func (g *Gateway) receiveFromAMF(ctx context.Context, a *amfAssoc, pdu ngap.PDU, msg []byte) (context.Context, trace.Span) {
	tx := g.core.Transport()
	if !tx.Tracing() {
		return ctx, trace.SpanFromContext(ctx)
	}

	info := inspect(pdu, msg)
	id := g.relay.amfUETrace(a, info)

	return tx.StartReceive(tracing.WithPinnedTraceID(ctx, id), metrics.SideCore, a.Conn(), info.span(), len(msg), a.Operator())
}

func (r *relay) ranUETrace(n *ranAssoc, ids ueIDs) (tracing.RANTraceID, int64, bool) {
	if ids.amf == nil {
		return tracing.RANTraceID{}, 0, false
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	u := r.tracedRANUELocked(n, ids)
	if u == nil {
		return tracing.RANTraceID{}, 0, false
	}

	return u.TraceID, u.OperatorID, true
}

func (r *relay) tracedRANUELocked(n *ranAssoc, ids ueIDs) *ueContext {
	if ids.amf == nil {
		return nil
	}

	u := r.ues.ByRANCoreID(*ids.amf)
	if u == nil || u.RAN != n || u.Core == nil {
		return nil
	}

	return u
}

func (r *relay) amfUETrace(a *amfAssoc, info msgInfo) tracing.RANTraceID {
	r.mu.Lock()
	defer r.mu.Unlock()

	var u *ueContext

	switch {
	case info.ids.ran != nil:
		u = r.ues.ByCoreRANID(*info.ids.ran)
	case info.ids.amf != nil:
		u = r.ues.ByCoreID(a, *info.ids.amf)
	}

	if u == nil || u.Core != a {
		return tracing.RANTraceID{}
	}

	if id, ok := traceActivationOf(info.raw); ok {
		return id
	}

	return u.TraceID
}

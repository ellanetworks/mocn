package ng

import (
	"context"
	"errors"
	"log/slog"
	"slices"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/plmnid"
	"github.com/ellanetworks/mocn/internal/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var (
	causeSuccessfulHandover   = ngap.Cause{Group: ngap.CauseGroupRadioNetwork, Value: ngap.CauseRadioNetworkSuccessfulHandover}
	causeUnknownTargetID      = ngap.Cause{Group: ngap.CauseGroupRadioNetwork, Value: ngap.CauseRadioNetworkUnknownTargetID}
	errNoTransparentContainer = errors.New("no Source to Target Transparent Container")
)

// TS 38.413 clause 9.2.3.4
var handoverRequestOrder = []ngap.ProtocolIEID{
	ngap.IDAMFUENGAPID, ngap.IDHandoverType, ngap.IDCause, ngap.IDUEAggregateMaximumBitRate,
	ngap.IDCoreNetworkAssistanceInformationForInactive, ngap.IDUESecurityCapabilities, ngap.IDSecurityContext,
	ngap.IDNewSecurityContextInd, ngap.IDNASC, ngap.IDPDUSessionResourceSetupListHOReq, ngap.IDAllowedNSSAI,
	ngap.IDTraceActivation, ngap.IDMaskedIMEISV, ngap.IDSourceToTargetTransparentContainer, ngap.IDMobilityRestrictionList,
	ngap.IDLocationReportingRequestType, ngap.IDRRCInactiveTransitionReportRequest, ngap.IDGUAMI,
	ngap.IDRedirectionVoiceFallback, ngap.IDCNAssistedRANTuning, ngap.IDSRVCCOperationPossible, ngap.IDIABAuthorized,
	ngap.IDEnhancedCoverageRestriction, ngap.IDUEDifferentiationInfo, ngap.IDNRV2XServicesAuthorized,
	ngap.IDLTEV2XServicesAuthorized, ngap.IDNRUESidelinkAggregateMaximumBitrate,
	ngap.IDLTEUESidelinkAggregateMaximumBitrate, ngap.IDPC5QoSParameters, ngap.IDCEmodeBrestricted,
	ngap.IDUEUPCIoTSupport, ngap.IDManagementBasedMDTPLMNList, ngap.IDUERadioCapabilityID, ngap.IDExtendedConnectedTime,
	ngap.IDTimeSyncAssistanceInfo, ngap.IDUESliceMaximumBitRateList, ngap.IDFiveGProSeAuthorized,
	ngap.IDFiveGProSeUEPC5AggregateMaximumBitRate, ngap.IDFiveGProSePC5QoSParameters,
	ngap.IDAerialUEsubscriptionInformation, ngap.IDNRA2XServicesAuthorized, ngap.IDLTEA2XServicesAuthorized,
	ngap.IDNRA2XUEPC5AggregateMaximumBitRate, ngap.IDLTEA2XUEPC5AggregateMaximumBitRate, ngap.IDA2XPC5QoSParameters,
	ngap.IDMobileIABAuthorized, ngap.IDNoPDUSessionIndication, ngap.IDPartiallyAllowedNSSAI,
	ngap.IDSLPositioningRangingServiceInfo, ngap.IDAMFUENGAPID2, ngap.IDTargetRANNodeID,
}

// TS 38.413 clause 9.2.3.9
var pathSwitchRequestAcknowledgeOrder = []ngap.ProtocolIEID{
	ngap.IDAMFUENGAPID, ngap.IDRANUENGAPID, ngap.IDUESecurityCapabilities, ngap.IDSecurityContext,
	ngap.IDNewSecurityContextInd, ngap.IDPDUSessionResourceSwitchedList, ngap.IDPDUSessionResourceReleasedListPSAck,
	ngap.IDAllowedNSSAI, ngap.IDCoreNetworkAssistanceInformationForInactive, ngap.IDRRCInactiveTransitionReportRequest,
	ngap.IDCriticalityDiagnostics, ngap.IDRedirectionVoiceFallback, ngap.IDCNAssistedRANTuning,
	ngap.IDSRVCCOperationPossible, ngap.IDEnhancedCoverageRestriction, ngap.IDExtendedConnectedTime,
	ngap.IDUEDifferentiationInfo, ngap.IDNRV2XServicesAuthorized, ngap.IDLTEV2XServicesAuthorized,
	ngap.IDNRUESidelinkAggregateMaximumBitrate, ngap.IDLTEUESidelinkAggregateMaximumBitrate, ngap.IDPC5QoSParameters,
	ngap.IDCEmodeBrestricted, ngap.IDUEUPCIoTSupport, ngap.IDUERadioCapabilityID, ngap.IDManagementBasedMDTPLMNList,
	ngap.IDTimeSyncAssistanceInfo, ngap.IDFiveGProSeAuthorized, ngap.IDFiveGProSeUEPC5AggregateMaximumBitRate,
	ngap.IDFiveGProSePC5QoSParameters, ngap.IDManagementBasedMDTPLMNModificationList, ngap.IDIABAuthorized,
	ngap.IDAerialUEsubscriptionInformation, ngap.IDNRA2XServicesAuthorized, ngap.IDLTEA2XServicesAuthorized,
	ngap.IDNRA2XUEPC5AggregateMaximumBitRate, ngap.IDLTEA2XUEPC5AggregateMaximumBitRate, ngap.IDA2XPC5QoSParameters,
	ngap.IDMobileIABAuthorized, ngap.IDPartiallyAllowedNSSAI, ngap.IDSLPositioningRangingServiceInfo,
	ngap.IDAMFUENGAPID2,
}

func (r *relay) countHandover(proc, result string) {
	r.g.core.CountHandover(proc, result)
}

// TS 38.413 clause 8.4.2.2
// TS 38.413 clause 8.4.2.3
// TS 38.300 clause 4.10.2.2
// TS 38.300 clause 4.10.2.3
func (r *relay) handoverRequest(ctx context.Context, a *amfAssoc, log *slog.Logger, pdu ngap.PDU, msg []byte) {
	p, err := ngap.ParseRawPDU(msg)
	if err != nil {
		r.countHandover(assoc.HandoverRequest, assoc.HandoverInvalid)
		undecodable(ctx, a, sourceAMF, log, pdu, err)

		return
	}

	amfID, ok, err := p.AMFUENGAPID()
	if err != nil || !ok {
		r.countHandover(assoc.HandoverRequest, assoc.HandoverInvalid)
		terminate(ctx, a, sourceAMF, log, pdu, msg)

		return
	}

	log = log.With(slog.Uint64("amf_ue_ngap_id", uint64(amfID)))

	fail := func(result, reason string, cause ngap.Cause, err error) {
		r.countHandover(assoc.HandoverRequest, result)
		tracing.SpanFailed(ctx, result)
		log.WarnContext(ctx, reason, slog.Any("error", err))
		sendTo(ctx, a, log, "Handover Failure", &ngap.HandoverFailure{AMFUENGAPID: &amfID, Cause: &cause}, true)
	}

	g, err := assoc.GetIE[ngap.GUAMI](p.Get, ngap.IDGUAMI)
	if err != nil || g == nil {
		fail(assoc.HandoverInvalid, "HANDOVER REQUEST from AMF rejected: no valid GUAMI", causeAbstractReject, err)
		return
	}

	guami, err := guamiFrom(*g)
	if err != nil {
		fail(assoc.HandoverInvalid, "HANDOVER REQUEST from AMF rejected: invalid GUAMI", causeSemanticError, err)
		return
	}

	target, err := assoc.GetIE[ngap.GlobalRANNodeID](p.Get, ngap.IDTargetRANNodeID)
	if err != nil {
		fail(assoc.HandoverInvalid, "HANDOVER REQUEST from AMF rejected: invalid Target RAN Node ID", causeUnknownTargetID, err)
		return
	}

	var pick func([]*ranAssoc) *ranAssoc

	if target != nil {
		id, err := globalGNBIDFrom(*target)
		if err != nil {
			fail(assoc.HandoverNoRadio, "HANDOVER REQUEST from AMF rejected: the Target RAN Node ID is not a gNB", causeUnknownTargetID, err)
			return
		}

		log = log.With(slog.String("target", id.Ref()))
		pick = func(radios []*ranAssoc) *ranAssoc {
			if i := slices.IndexFunc(radios, func(c *ranAssoc) bool { return c.Setup.globalGNBID == id }); i >= 0 {
				return radios[i]
			}

			return nil
		}
	} else {
		cell, err := targetCell(p)
		if err != nil {
			fail(assoc.HandoverInvalid, "HANDOVER REQUEST from AMF rejected: no valid target cell", causeUnknownTargetID, err)
			return
		}

		if cell.NRCGI == nil {
			fail(assoc.HandoverNoRadio, "HANDOVER REQUEST from AMF rejected: the target cell is not an NR cell", causeUnknownTargetID, nil)
			return
		}

		plmn, err := plmnid.Decode(cell.NRCGI.PLMNIdentity)
		if err != nil {
			fail(assoc.HandoverInvalid, "HANDOVER REQUEST from AMF rejected: invalid target NR CGI", causeUnknownTargetID, err)
			return
		}

		nci := uint64(cell.NRCGI.NRCellIdentity)
		log = log.With(slog.String("target_plmn", plmn.String()), slog.Uint64("target_nr_cell_identity", nci))
		pick = func(radios []*ranAssoc) *ranAssoc { return servingCell(radios, plmn, nci) }
	}

	trace.SpanFromContext(ctx).SetAttributes(attribute.String(assoc.AttrOperator, a.Operator()))

	var (
		n  *ranAssoc
		id db.GlobalGNBID
	)

	r.g.core.View(func() {
		if n = pick(r.g.core.OperatorRadiosLocked(a.OperatorID())); n != nil {
			id = n.Setup.globalGNBID
		}
	})

	if n == nil {
		fail(assoc.HandoverNoRadio, "HANDOVER REQUEST from AMF rejected: no radio of the operator that is up is the target",
			causeUnknownTargetID, nil)

		return
	}

	r.mu.Lock()

	u := &ueContext{
		RAN: n, Core: a, OperatorID: a.OperatorID(), PLMN: guami.PLMN, Since: r.g.cfg.Now(),
		Ext: ueInfo{radio: id, guami: &guami, sets: r.sets[a]},
	}

	if err := r.ues.Add(u); err != nil {
		r.mu.Unlock()
		fail(assoc.HandoverError, "dropped HANDOVER REQUEST", causeHOFailure, err)

		return
	}

	r.ues.SetCoreID(u, amfID)
	traceUE(u, p)

	out, err := handoverRequestToRadio(p, u.RANCoreID, amfID)
	if err != nil {
		r.ues.Remove(u)
		r.mu.Unlock()
		fail(assoc.HandoverError, "failed to relay HANDOVER REQUEST", causeHOFailure, err)

		return
	}

	u.Ext.told = true
	ranAMFID := u.RANCoreID
	r.mu.Unlock()

	if !r.g.core.IsUp(n) {
		r.mu.Lock()
		r.ues.Remove(u)
		r.mu.Unlock()
		fail(assoc.HandoverNoRadio, "HANDOVER REQUEST from AMF rejected: the target radio left", causeUnknownTargetID, nil)

		return
	}

	if err := n.For(a.Operator()).Send(ctx, out, ueStream); err != nil {
		r.mu.Lock()
		removed := len(r.ues.Remove(u)) > 0
		r.mu.Unlock()

		if removed {
			fail(assoc.HandoverError, "failed to send HANDOVER REQUEST to radio", causeHOFailure, err)
		}

		return
	}

	r.countHandover(assoc.HandoverRequest, assoc.HandoverRelayed)
	log.InfoContext(ctx, "HANDOVER REQUEST relayed to radio", slog.Uint64("ran_amf_ue_ngap_id", uint64(ranAMFID)))
}

// TS 38.413 clause 8.4.2.2
// TS 38.413 clause 9.3.1.20
func targetCell(p *ngap.RawPDU) (ngap.NGRANCGI, error) {
	c, err := assoc.GetIE[ngap.SourceToTargetTransparentContainer](p.Get, ngap.IDSourceToTargetTransparentContainer)
	if err != nil {
		return ngap.NGRANCGI{}, err
	}

	if c == nil {
		return ngap.NGRANCGI{}, errNoTransparentContainer
	}

	t, err := ngap.ParseSourceNGRANNodeToTargetNGRANNodeTransparentContainer(*c)
	if err != nil {
		return ngap.NGRANCGI{}, err
	}

	return t.TargetCellID, nil
}

// TS 38.413 clause 9.3.1.6
// TS 38.413 clause 9.3.1.7
func servingCell(radios []*ranAssoc, plmn db.PLMN, nci uint64) *ranAssoc {
	var serving, exact []*ranAssoc

	for _, n := range radios {
		if !n.Setup.ownsCell(ngap.NRCellIdentity(nci)) {
			continue
		}

		serving = append(serving, n)

		if n.Setup.globalGNBID.PLMN == plmn {
			exact = append(exact, n)
		}
	}

	switch {
	case len(serving) == 1:
		return serving[0]
	case len(exact) == 1:
		return exact[0]
	}

	return nil
}

// TS 38.300 clause 4.10.2.3
// TS 38.413 clause 8.4.2.2
func handoverRequestToRadio(p *ngap.RawPDU, ranAMFID, amfID ngap.AMFUENGAPID) ([]byte, error) {
	if err := p.SetAMFUENGAPID(ranAMFID); err != nil {
		return nil, err
	}

	if err := setTypedIE(p, ngap.IDAMFUENGAPID2, amfID, handoverRequestOrder); err != nil {
		return nil, err
	}

	return p.Marshal()
}

// TS 38.413 clause 8.4.4.2
// TS 38.413 clause 8.4.4.3
// TS 38.300 clause 4.10.2.1
// TS 38.300 clause 4.10.2.3
// TS 23.502 clause 4.2.7.2.3
// TS 23.501 clause 5.9.9
func (r *relay) pathSwitchRequest(ctx context.Context, n *ranAssoc, log *slog.Logger, value, msg []byte) {
	req, err := ngap.ParsePathSwitchRequest(value)
	if err != nil {
		r.countHandover(assoc.PathSwitchRequest, assoc.HandoverInvalid)
		tracing.SpanFailed(ctx, assoc.ErrTypeInvalid)
		log.WarnContext(ctx, "rejected PATH SWITCH REQUEST from radio", slog.Any("error", err))
		indicateParseError(ctx, n, log, ngap.ProcPathSwitchRequest, err)

		return
	}

	log = log.With(slog.Uint64("ran_ue_ngap_id", uint64(req.RANUENGAPID)),
		slog.Uint64("source_amf_ue_ngap_id", uint64(req.SourceAMFUENGAPID)))

	reject := func(result, reason string, cause ngap.Cause, err error) {
		r.countHandover(assoc.PathSwitchRequest, result)
		tracing.SpanFailed(ctx, result)
		log.WarnContext(ctx, reason, slog.Any("error", err))
		rejectPathSwitch(ctx, n, log, req, cause)
	}

	p, err := ngap.ParseRawPDU(msg)
	if err != nil {
		reject(assoc.HandoverInvalid, "PATH SWITCH REQUEST from radio rejected", causeTransferSyntax, err)
		return
	}

	var source *GUAMI

	if g := req.SourceAMFGUAMI; g != nil {
		v, err := guamiFrom(*g)
		if err != nil {
			reject(assoc.HandoverInvalid, "PATH SWITCH REQUEST from radio rejected: invalid Source AMF GUAMI", causeSemanticError, err)
			return
		}

		source = &v
	}

	var located *db.PLMN

	if uli := req.UserLocationInformation; uli != nil && uli.Kind == ngap.UserLocationNR {
		if v, err := plmnid.Decode(uli.TAI.PLMNIdentity); err == nil {
			located = &v
		}
	}

	var plmn db.PLMN

	switch {
	case source != nil:
		plmn = source.PLMN
	case located != nil:
		plmn = *located
	default:
		reject(names.NoCore, "PATH SWITCH REQUEST from radio rejected: no Source AMF GUAMI and no NR TAI", causeTransportUnavailable, nil)
		return
	}

	var (
		op    assoc.Operator
		cands []amfCandidate
		found bool
		radio db.GlobalGNBID
	)

	r.g.core.View(func() {
		op, cands, found = r.g.selectionLocked(plmn)

		if n.Setup != nil {
			radio = n.Setup.globalGNBID
		}
	})

	if !found {
		reject(names.NoCore, "PATH SWITCH REQUEST from radio rejected: no operator serves the PLMN", causeUnknownPLMN, nil)
		return
	}

	trace.SpanFromContext(ctx).SetAttributes(attribute.String(assoc.AttrOperator, op.Name))

	cands = selectable(cands)
	b := binding{guami: source}

	if source == nil {
		b.sets = setsOfCandidates(cands)
		if len(b.sets) != 1 {
			reject(names.NoCore, "PATH SWITCH REQUEST from radio rejected: no Source AMF GUAMI and not exactly one AMF set of the operator is up",
				causeTransportUnavailable, nil)

			return
		}
	}

	amf := rebindAMF(cands, b, r.intN)
	if amf == nil {
		reject(names.NoCore, "PATH SWITCH REQUEST from radio rejected: no selectable AMF of the AMF set", causeTransportUnavailable, nil)
		return
	}

	if located != nil && slices.Contains(op.PLMNs, *located) {
		plmn = *located
	}

	r.mu.Lock()

	if old := r.ues.ByRANID(n, req.RANUENGAPID); old != nil {
		gone := r.ues.Remove(old)
		r.mu.Unlock()

		r.countHandover(assoc.PathSwitchRequest, assoc.HandoverInvalid)
		tracing.SpanFailed(ctx, assoc.HandoverInvalid)
		log.WarnContext(ctx, "PATH SWITCH REQUEST reuses an allocated RAN UE NGAP ID")
		sendUEErrorIndication(ctx, n, log, causeInconsistentRemoteID, nil, &req.RANUENGAPID)
		r.resetAMFs(ctx, gone, causeInconsistentRemoteID)

		return
	}

	u := &ueContext{
		RAN: n, RANID: req.RANUENGAPID, RANIDKnown: true, Core: amf, OperatorID: op.ID, PLMN: plmn, Since: r.g.cfg.Now(),
		Ext: ueInfo{
			radio: radio, guami: source, sets: r.sets[amf], switching: true, source: req.SourceAMFUENGAPID,
		},
	}

	r.updateLocation(u, p)

	if err := r.ues.Add(u); err != nil {
		r.mu.Unlock()
		reject(assoc.HandoverError, "dropped PATH SWITCH REQUEST", causeTransportUnavailable, err)

		return
	}

	out, err := rewrite(p, nil, &u.CoreRANID)
	if err != nil {
		r.ues.Remove(u)
		r.mu.Unlock()
		reject(assoc.HandoverError, "failed to relay PATH SWITCH REQUEST", causeTransportUnavailable, err)

		return
	}

	r.superviseLocked(ctx, u, log, "PATH SWITCH REQUEST")

	send := r.sendAMF
	r.mu.Unlock()

	if err := send(ctx, amf, out, ueStream); err != nil {
		r.mu.Lock()
		removed := len(r.ues.Remove(u)) > 0
		r.mu.Unlock()

		if removed {
			reject(assoc.HandoverError, "failed to send PATH SWITCH REQUEST to AMF", causeTransportUnavailable, err)
		}

		return
	}

	r.countHandover(assoc.PathSwitchRequest, assoc.HandoverRelayed)
	log.InfoContext(ctx, "PATH SWITCH REQUEST relayed", slog.String("operator", op.Name), slog.Any("amf", amf.Addrs()),
		slog.Uint64("amf_ran_ue_ngap_id", uint64(u.CoreRANID)))
}

// TS 23.501 clause 5.9.9
// TS 23.501 clause 5.21.2.2.1
func setsOfCandidates(cands []amfCandidate) []amfSet {
	var out []amfSet

	for _, c := range cands {
		for _, s := range setsOf(c.served) {
			if !slices.Contains(out, s) {
				out = append(out, s)
			}
		}
	}

	return out
}

// TS 38.413 clause 8.4.4.3
// TS 38.413 clause 9.3.4.20
func rejectPathSwitch(ctx context.Context, n *ranAssoc, log *slog.Logger, req *ngap.PathSwitchRequest, cause ngap.Cause) {
	transfer, err := (&ngap.PathSwitchRequestUnsuccessfulTransfer{Cause: cause}).Marshal()
	if err != nil {
		log.ErrorContext(ctx, "failed to marshal Path Switch Request Unsuccessful Transfer", slog.Any("error", err))
		return
	}

	released := make(ngap.PDUSessionResourceReleasedListPSFail, 0, len(req.PDUSessionResourceToBeSwitchedDLList))
	for _, item := range req.PDUSessionResourceToBeSwitchedDLList {
		released = append(released, ngap.PDUSessionResourceReleasedItemPSFail{PDUSessionID: item.PDUSessionID, Transfer: transfer})
	}

	sendTo(ctx, n, log, "Path Switch Request Failure", &ngap.PathSwitchRequestFailure{
		AMFUENGAPID: &req.SourceAMFUENGAPID, RANUENGAPID: &req.RANUENGAPID, PDUSessionResourceReleased: released,
	}, true)
}

package ng

import (
	"context"
	"log/slog"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/core/per"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/tracing"
)

func rewrite(p *ngap.RawPDU, amfID *ngap.AMFUENGAPID, ranID *ngap.RANUENGAPID) ([]byte, error) {
	if amfID != nil {
		if err := p.SetAMFUENGAPID(*amfID); err != nil {
			return nil, err
		}
	}

	if ranID != nil {
		if err := p.SetRANUENGAPID(*ranID); err != nil {
			return nil, err
		}
	}

	return p.Marshal()
}

type ueIDs struct {
	amf *ngap.AMFUENGAPID
	ran *ngap.RANUENGAPID
}

func readUEIDs(p *ngap.RawPDU) (ueIDs, *ngap.UENGAPIDs, error) {
	if p.Index(ngap.IDUENGAPIDs) >= 0 {
		v, _, err := p.UENGAPIDs()
		if err != nil {
			return ueIDs{}, nil, err
		}

		ids := ueIDs{amf: &v.AMFUENGAPID}
		if v.Pair {
			ids.ran = &v.RANUENGAPID
		}

		return ids, &v, nil
	}

	var ids ueIDs

	amfID, ok, err := p.AMFUENGAPID()
	if err != nil {
		return ueIDs{}, nil, err
	}

	if ok {
		ids.amf = &amfID
	}

	ranID, ok, err := p.RANUENGAPID()
	if err != nil {
		return ueIDs{}, nil, err
	}

	if ok {
		ids.ran = &ranID
	}

	return ids, nil, nil
}

// TS 38.300 clause 4.10.2.3
// TS 38.413 clause 10.6
func (r *relay) uplink(ctx context.Context, n *ranAssoc, log *slog.Logger, p *ngap.RawPDU) {
	proc := p.ProcedureCode()
	log = log.With(slog.String("procedure", proc.String()))

	ids, _, err := readUEIDs(p)
	if err != nil {
		tracing.SpanFailed(ctx, assoc.ErrTypeInvalid)
		log.WarnContext(ctx, "dropped NGAP message from radio with invalid UE NGAP IDs", slog.Any("error", err))

		return
	}

	last := p.Trigger() == ngap.TriggeringSuccessfulOutcome && proc == ngap.ProcUEContextRelease

	r.mu.Lock()

	u, cause := r.ranUE(n, ids)
	if u == nil {
		traced := r.tracedRANUELocked(n, ids)
		gone := r.ues.Remove(r.ues.OnRAN(n, ids.amf, ids.ran)...)
		r.mu.Unlock()

		var peer assoc.Sender = n
		if traced != nil {
			peer = n.For(r.g.core.OperatorName(traced.OperatorID))
		}

		tracing.SpanFailed(ctx, assoc.ErrTypeUnknownUE)
		log.WarnContext(ctx, "NGAP message from radio for an unknown UE", slog.String("cause", cause.String()),
			slog.Any("amf_ue_ngap_id", ids.amf), slog.Any("ran_ue_ngap_id", ids.ran))

		if !last {
			sendUEErrorIndication(ctx, peer, log, cause, ids.amf, ids.ran)
		}

		r.resetAMFs(ctx, gone, cause)

		return
	}

	if u.Core == nil {
		if last {
			r.ues.Remove(u)
		}

		r.mu.Unlock()

		if last {
			log.InfoContext(ctx, "UE context released", slog.Uint64("amf_ue_ngap_id", uint64(u.RANCoreID)))
		} else {
			log.DebugContext(ctx, "dropped NGAP message from radio for a UE being released")
		}

		return
	}

	if proc == ngap.ProcUEContextReleaseRequest && p.Index(ngap.IDGWContextReleaseIndication) >= 0 {
		r.ues.Remove(u)
		r.mu.Unlock()

		log.InfoContext(ctx, "UE context released: UE Context Release Request with GW Context Release Indication",
			slog.Uint64("amf_ue_ngap_id", uint64(u.RANCoreID)))

		return
	}

	if !u.CoreIDKnown {
		r.mu.Unlock()
		log.InfoContext(ctx, "dropped NGAP message from radio for a UE whose INITIAL UE MESSAGE was rerouted")

		return
	}

	if u.Ext.unbound || u.Core.Lost() {
		r.mu.Unlock()

		if !r.rebind(ctx, log, u) {
			return
		}

		r.mu.Lock()

		if r.ues.ByRANCoreID(u.RANCoreID) != u || u.Ext.unbound || u.Core.Lost() {
			r.mu.Unlock()
			log.InfoContext(ctx, "dropped NGAP message from radio for a UE released or unbound meanwhile")

			return
		}
	}

	r.updateLocation(u, p)

	amfID, amfRANID, amf := u.CoreID, u.CoreRANID, u.Core
	r.mu.Unlock()

	out, err := rewrite(p, &amfID, &amfRANID)
	if err != nil {
		tracing.SpanFailed(ctx, assoc.ErrTypeError)
		log.WarnContext(ctx, "failed to relay NGAP message to AMF", slog.Any("error", err))

		return
	}

	if last {
		r.mu.Lock()
		r.ues.Remove(u)
		r.mu.Unlock()
	}

	log.DebugContext(ctx, "relayed NGAP message to AMF", slog.Uint64("amf_ran_ue_ngap_id", uint64(amfRANID)))

	if err := amf.Send(ctx, out, ueStream); err != nil {
		log.WarnContext(ctx, "failed to send NGAP message to AMF", slog.Any("error", err))
	}
}

func (r *relay) ranUE(n *ranAssoc, ids ueIDs) (*ueContext, ngap.Cause) {
	if ids.amf == nil {
		return nil, causeUnknownLocalID
	}

	u := r.ues.ByRANCoreID(*ids.amf)
	if u == nil || u.RAN != n || (u.Core != nil && !u.Ext.told) {
		return nil, causeUnknownLocalID
	}

	if ids.ran == nil || *ids.ran != u.RANID {
		return nil, causeInconsistentRemoteID
	}

	return u, ngap.Cause{}
}

// TS 38.300 clause 4.10.2.3
// TS 38.413 clause 10.6
func (r *relay) downlink(ctx context.Context, a *amfAssoc, log *slog.Logger, p *ngap.RawPDU) {
	log = log.With(slog.String("procedure", p.ProcedureCode().String()))

	ids, pair, err := readUEIDs(p)
	if err != nil {
		tracing.SpanFailed(ctx, assoc.ErrTypeInvalid)
		log.WarnContext(ctx, "dropped NGAP message from AMF with invalid UE NGAP IDs", slog.Any("error", err))

		return
	}

	var newID *ngap.AMFUENGAPID

	if p.Index(ngap.IDNewAMFUENGAPID) >= 0 {
		id, _, err := p.NewAMFUENGAPID()
		if err != nil {
			tracing.SpanFailed(ctx, assoc.ErrTypeInvalid)
			log.WarnContext(ctx, "dropped NGAP message from AMF with an invalid New AMF UE NGAP ID", slog.Any("error", err))

			return
		}

		newID = &id
	}

	r.mu.Lock()

	u, others, cause := r.amfUE(a, ids)
	if u == nil {
		gone := r.ues.Remove(append(others, r.ues.OnCore(a, ids.amf, ids.ran)...)...)
		r.mu.Unlock()
		r.unknownFromAMF(ctx, a, log, ids, gone, cause)

		return
	}

	if u.Core != a {
		log.InfoContext(ctx, "UE-associated logical NG-connection moved to another AMF of the AMF set",
			slog.Any("from", u.Core.Addrs()))
		r.ues.Rebind(u, a)
		u.Ext.sets = r.sets[a]
	}

	u.Ext.unbound, u.Ext.backup = false, amfName{}
	storeGUAMI(u, p)

	if !u.CoreIDKnown || u.CoreID != *ids.amf {
		r.ues.SetCoreID(u, *ids.amf)
	}

	if newID != nil {
		r.ues.SetCoreID(u, *newID)
	}

	ranAMFID, ranID, ran, told, amfID := u.RANCoreID, u.RANID, u.RAN, u.Ext.told, u.CoreID
	r.mu.Unlock()

	if newID != nil {
		err = p.SetNewAMFUENGAPID(ranAMFID)
	}

	if err == nil && p.ProcedureCode() == ngap.ProcInitialContextSetup && p.Trigger() == ngap.TriggeringInitiatingMessage {
		err = setTypedIE(p, ngap.IDAMFUENGAPID2, amfID, initialContextSetupRequestOrder)
	}

	var out []byte

	switch {
	case err != nil:
	case pair != nil:
		pair.AMFUENGAPID, pair.RANUENGAPID = ranAMFID, ranID
		if err = p.SetUENGAPIDs(*pair); err == nil {
			out, err = p.Marshal()
		}
	default:
		out, err = rewrite(p, &ranAMFID, &ranID)
	}

	if err != nil {
		tracing.SpanFailed(ctx, assoc.ErrTypeError)
		log.WarnContext(ctx, "failed to relay NGAP message to radio", slog.Any("error", err))

		return
	}

	if !told || tracesUE(p) {
		r.mu.Lock()
		traceUE(u, p)
		u.Ext.told = true
		r.mu.Unlock()
	}

	log.DebugContext(ctx, "relayed NGAP message to radio", slog.Uint64("amf_ue_ngap_id", uint64(ranAMFID)))

	if err := ran.For(a.Operator()).Send(ctx, out, ueStream); err != nil {
		log.WarnContext(ctx, "failed to send NGAP message to radio", slog.Any("error", err))
	}
}

func (r *relay) unknownFromAMF(ctx context.Context, a *amfAssoc, log *slog.Logger, ids ueIDs, gone []*ueContext,
	cause ngap.Cause,
) {
	tracing.SpanFailed(ctx, assoc.ErrTypeUnknownUE)
	log.WarnContext(ctx, "NGAP message from AMF for an unknown UE", slog.String("cause", cause.String()),
		slog.Any("amf_ue_ngap_id", ids.amf), slog.Any("ran_ue_ngap_id", ids.ran))
	sendUEErrorIndication(ctx, a, log, cause, ids.amf, ids.ran)
	r.resetRANs(ctx, a.Operator(), gone, cause)
}

// TS 23.502 clause 4.2.7.2.3
// TS 23.502 clause 4.2.7.2.4
// TS 23.501 clause 5.21.2.2.1
func (r *relay) sameSet(u *ueContext, a *amfAssoc) bool {
	return u.OperatorID == a.OperatorID() && sharesSet(u.Ext.sets, r.sets[a])
}

func (r *relay) amfUE(a *amfAssoc, ids ueIDs) (*ueContext, []*ueContext, ngap.Cause) {
	if ids.ran == nil {
		if ids.amf == nil {
			return nil, nil, causeUnknownLocalID
		}

		if u := r.ues.ByCoreID(a, *ids.amf); u != nil {
			return u, nil, ngap.Cause{}
		}

		moved := r.ues.Where(func(u *ueContext) bool {
			return u.Core != a && u.CoreIDKnown && u.CoreID == *ids.amf && r.sameSet(u, a)
		})
		if len(moved) == 1 {
			return moved[0], nil, ngap.Cause{}
		}

		return nil, nil, causeUnknownLocalID
	}

	u := r.ues.ByCoreRANID(*ids.ran)
	if u == nil || u.Core == nil {
		return nil, nil, causeUnknownLocalID
	}

	if ids.amf == nil {
		return nil, nil, causeInconsistentRemoteID
	}

	if u.Core != a {
		if !r.sameSet(u, a) {
			return nil, nil, causeUnknownLocalID
		}

		if other := r.ues.ByCoreID(a, *ids.amf); other != nil {
			return nil, []*ueContext{u, other}, causeInconsistentRemoteID
		}

		return u, nil, ngap.Cause{}
	}

	if u.CoreIDKnown {
		if u.CoreID != *ids.amf {
			return nil, nil, causeInconsistentRemoteID
		}

		return u, nil, ngap.Cause{}
	}

	if other := r.ues.ByCoreID(a, *ids.amf); other != nil {
		return nil, []*ueContext{u, other}, causeInconsistentRemoteID
	}

	return u, nil, ngap.Cause{}
}

// TS 38.413 clause 9.2.2.1
var initialContextSetupRequestOrder = []ngap.ProtocolIEID{
	ngap.IDAMFUENGAPID, ngap.IDRANUENGAPID, ngap.IDOldAMF, ngap.IDUEAggregateMaximumBitRate,
	ngap.IDCoreNetworkAssistanceInformationForInactive, ngap.IDGUAMI, ngap.IDPDUSessionResourceSetupListCxtReq,
	ngap.IDAllowedNSSAI, ngap.IDUESecurityCapabilities, ngap.IDSecurityKey, ngap.IDTraceActivation,
	ngap.IDMobilityRestrictionList, ngap.IDUERadioCapability, ngap.IDIndexToRFSP, ngap.IDMaskedIMEISV, ngap.IDNASPDU,
	ngap.IDEmergencyFallbackIndicator, ngap.IDRRCInactiveTransitionReportRequest, ngap.IDUERadioCapabilityForPaging,
	ngap.IDRedirectionVoiceFallback, ngap.IDLocationReportingRequestType, ngap.IDCNAssistedRANTuning,
	ngap.IDSRVCCOperationPossible, ngap.IDIABAuthorized, ngap.IDEnhancedCoverageRestriction, ngap.IDExtendedConnectedTime,
	ngap.IDUEDifferentiationInfo, ngap.IDNRV2XServicesAuthorized, ngap.IDLTEV2XServicesAuthorized,
	ngap.IDNRUESidelinkAggregateMaximumBitrate, ngap.IDLTEUESidelinkAggregateMaximumBitrate, ngap.IDPC5QoSParameters,
	ngap.IDCEmodeBrestricted, ngap.IDUEUPCIoTSupport, ngap.IDRGLevelWirelineAccessCharacteristics,
	ngap.IDManagementBasedMDTPLMNList, ngap.IDUERadioCapabilityID, ngap.IDTimeSyncAssistanceInfo, ngap.IDQMCConfigInfo,
	ngap.IDTargetNSSAIInformation, ngap.IDUESliceMaximumBitRateList, ngap.IDFiveGProSeAuthorized,
	ngap.IDFiveGProSeUEPC5AggregateMaximumBitRate, ngap.IDFiveGProSePC5QoSParameters,
	ngap.IDNetworkControlledRepeaterAuthorized, ngap.IDAerialUEsubscriptionInformation, ngap.IDNRA2XServicesAuthorized,
	ngap.IDLTEA2XServicesAuthorized, ngap.IDNRA2XUEPC5AggregateMaximumBitRate, ngap.IDLTEA2XUEPC5AggregateMaximumBitRate,
	ngap.IDA2XPC5QoSParameters, ngap.IDMobileIABAuthorized, ngap.IDPartiallyAllowedNSSAI,
	ngap.IDSLPositioningRangingServiceInfo, ngap.IDExtendedOldAMF, ngap.IDAMFUENGAPID2,
}

// TS 38.300 clause 4.10.2.3
// TS 38.413 clause 8.3.1.2
func setTypedIE(p *ngap.RawPDU, id ngap.ProtocolIEID, v per.Marshaler, order []ngap.ProtocolIEID) error {
	b, err := per.Marshal(v, per.Aligned)
	if err != nil {
		return err
	}

	return setIE(p, ngap.RawIE{ID: id, Criticality: ngap.CriticalityIgnore, Value: b}, order)
}

package s1

import (
	"context"
	"errors"
	"log/slog"

	"github.com/ellanetworks/core/per"
	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/tracing"
)

func rewrite(p *s1ap.RawPDU, mmeID *s1ap.MMEUES1APID, enbID *s1ap.ENBUES1APID) ([]byte, error) {
	if mmeID != nil {
		if err := p.SetMMEUES1APID(*mmeID); err != nil {
			return nil, err
		}
	}

	if enbID != nil {
		if err := p.SetENBUES1APID(*enbID); err != nil {
			return nil, err
		}
	}

	return p.Marshal()
}

var errUnknownUEIDs = errors.New("UE S1AP IDs in an unknown alternative")

type ueIDs struct {
	mme *s1ap.MMEUES1APID
	enb *s1ap.ENBUES1APID
}

func readUEIDs(p *s1ap.RawPDU) (ueIDs, *s1ap.UES1APIDs, error) {
	if present(p, s1ap.IDUES1APIDs) {
		v, _, err := p.UES1APIDs()
		if err != nil {
			return ueIDs{}, nil, err
		}

		mmeID, ok := v.MMEID()
		if !ok {
			return ueIDs{}, nil, errUnknownUEIDs
		}

		ids := ueIDs{mme: &mmeID}
		if enbID, ok := v.ENBID(); ok {
			ids.enb = &enbID
		}

		return ids, &v, nil
	}

	var ids ueIDs

	mmeID, ok, err := p.MMEUES1APID()
	if err != nil {
		return ueIDs{}, nil, err
	}

	if ok {
		ids.mme = &mmeID
	}

	enbID, ok, err := p.ENBUES1APID()
	if err != nil {
		return ueIDs{}, nil, err
	}

	if ok {
		ids.enb = &enbID
	}

	return ids, nil, nil
}

// TS 36.300 clause 4.6.2
// TS 36.413 clause 10.6
func (r *relay) uplink(ctx context.Context, n *ranAssoc, log *slog.Logger, p *s1ap.RawPDU) {
	proc := p.ProcedureCode()
	log = log.With(slog.String("procedure", proc.String()))

	ids, _, err := readUEIDs(p)
	if err != nil {
		tracing.SpanFailed(ctx, assoc.ErrTypeInvalid)
		log.WarnContext(ctx, "dropped S1AP message from radio with invalid UE S1AP IDs", slog.Any("error", err))

		return
	}

	last := (p.Trigger() == s1ap.TriggeringSuccessfulOutcome && proc == s1ap.ProcUEContextRelease) ||
		(p.Trigger() == s1ap.TriggeringUnsuccessfulOutcome && proc == s1ap.ProcHandoverResourceAllocation)

	r.mu.Lock()

	u, cause := r.ranUE(n, ids)
	if u == nil {
		traced := r.tracedRANUELocked(n, ids)
		gone := r.ues.Remove(r.ues.OnRAN(n, ids.mme, ids.enb)...)
		r.mu.Unlock()

		var peer assoc.Sender = n
		if traced != nil {
			peer = n.For(r.g.core.OperatorName(traced.OperatorID))
		}

		tracing.SpanFailed(ctx, assoc.ErrTypeUnknownUE)
		log.WarnContext(ctx, "S1AP message from radio for an unknown UE", slog.String("cause", cause.String()),
			slog.Any("mme_ue_s1ap_id", ids.mme), slog.Any("enb_ue_s1ap_id", ids.enb))

		if !last {
			sendUEErrorIndication(ctx, peer, log, cause, ids.mme, ids.enb)
		}

		r.resetMMEs(ctx, gone, cause)

		return
	}

	if u.Core == nil {
		if last {
			r.ues.Remove(u)
		}

		r.mu.Unlock()

		if last {
			log.InfoContext(ctx, "UE context released", slog.Uint64("mme_ue_s1ap_id", uint64(u.RANCoreID)))
		} else {
			log.DebugContext(ctx, "dropped S1AP message from radio for a UE being released")
		}

		return
	}

	if !u.RANIDKnown && ids.enb != nil && !r.ues.SetRANID(u, *ids.enb) {
		gone := r.ues.Remove(u, r.ues.ByRANID(n, *ids.enb))
		r.mu.Unlock()

		tracing.SpanFailed(ctx, assoc.ErrTypeUnknownUE)
		log.WarnContext(ctx, "S1AP message from radio with an eNB UE S1AP ID of another UE",
			slog.Any("mme_ue_s1ap_id", ids.mme), slog.Any("enb_ue_s1ap_id", ids.enb))
		sendUEErrorIndication(ctx, n, log, causeUnknownPair, ids.mme, ids.enb)
		r.resetMMEs(ctx, gone, causeUnknownPair)

		return
	}

	if proc == s1ap.ProcUEContextReleaseRequest && present(p, s1ap.IDGWContextReleaseIndication) {
		r.ues.Remove(u)
		r.mu.Unlock()

		log.InfoContext(ctx, "UE context released: UE Context Release Request with GW Context Release Indication",
			slog.Uint64("mme_ue_s1ap_id", uint64(u.RANCoreID)))

		return
	}

	if !u.CoreIDKnown {
		r.mu.Unlock()
		log.InfoContext(ctx, "dropped S1AP message from radio for a UE whose INITIAL UE MESSAGE was rerouted")

		return
	}

	r.updateLocation(u, p)

	if err := r.tnlUplinkLocked(u, p); err != nil {
		r.mu.Unlock()
		r.abort(ctx, log, u, proc, err)

		return
	}

	mmeID, mmeENBID, mme := u.CoreID, u.CoreRANID, u.Core
	r.mu.Unlock()

	enbID := &mmeENBID
	if ids.enb == nil {
		enbID = nil
	}

	out, err := rewrite(p, &mmeID, enbID)
	if err != nil {
		tracing.SpanFailed(ctx, assoc.ErrTypeError)
		log.WarnContext(ctx, "failed to relay S1AP message to MME", slog.Any("error", err))

		return
	}

	if last {
		r.mu.Lock()
		r.ues.Remove(u)
		r.mu.Unlock()
	}

	log.DebugContext(ctx, "relayed S1AP message to MME", slog.Uint64("mme_enb_ue_s1ap_id", uint64(mmeENBID)))

	if err := mme.SendWait(ctx, out, ueStream); err != nil {
		log.WarnContext(ctx, "failed to send S1AP message to MME", slog.Any("error", err))
	}
}

func (r *relay) ranUE(n *ranAssoc, ids ueIDs) (*ueContext, s1ap.Cause) {
	if ids.mme == nil {
		return nil, causeUnknownMMEUEID
	}

	u := r.ues.ByRANCoreID(*ids.mme)
	if u == nil || u.RAN != n || (u.Core != nil && !u.Ext.told) {
		return nil, causeUnknownMMEUEID
	}

	if !u.RANIDKnown {
		return u, s1ap.Cause{}
	}

	if ids.enb == nil || *ids.enb != u.RANID {
		return nil, causeUnknownPair
	}

	return u, s1ap.Cause{}
}

// TS 36.300 clause 4.6.2
// TS 36.413 clause 10.6
func (r *relay) downlink(ctx context.Context, a *mmeAssoc, log *slog.Logger, p *s1ap.RawPDU) {
	log = log.With(slog.String("procedure", p.ProcedureCode().String()))

	ids, pair, err := readUEIDs(p)
	if err != nil {
		tracing.SpanFailed(ctx, assoc.ErrTypeInvalid)
		log.WarnContext(ctx, "dropped S1AP message from MME with invalid UE S1AP IDs", slog.Any("error", err))

		return
	}

	r.mu.Lock()

	u, others, cause := r.mmeUE(a, ids)
	if u == nil {
		gone := r.ues.Remove(append(others, r.ues.OnCore(a, ids.mme, ids.enb)...)...)
		r.mu.Unlock()

		tracing.SpanFailed(ctx, assoc.ErrTypeUnknownUE)
		log.WarnContext(ctx, "S1AP message from MME for an unknown UE", slog.String("cause", cause.String()),
			slog.Any("mme_ue_s1ap_id", ids.mme), slog.Any("enb_ue_s1ap_id", ids.enb))
		sendUEErrorIndication(ctx, a, log, cause, ids.mme, ids.enb)
		r.resetRANs(ctx, a.Operator(), gone, cause)

		return
	}

	switched := u.Ext.switching && p.ProcedureCode() == s1ap.ProcPathSwitchRequest &&
		p.Trigger() == s1ap.TriggeringSuccessfulOutcome
	failed := u.Ext.switching && p.ProcedureCode() == s1ap.ProcPathSwitchRequest &&
		p.Trigger() == s1ap.TriggeringUnsuccessfulOutcome

	if !u.CoreIDKnown && !failed {
		r.ues.SetCoreID(u, *ids.mme)
	}

	var stale []*ueContext

	if switched {
		u.Ext.switching = false
		stale = r.supersededLocked(u, a)
	}

	if err := r.tnlDownlinkLocked(u, p, stale); err != nil {
		r.mu.Unlock()
		r.abort(ctx, log, u, p.ProcedureCode(), err)

		for _, o := range stale {
			r.releaseRadioSide(ctx, o.RAN, o.RAN.Logger(r.log), o, causeSuccessfulHandover)
		}

		return
	}

	ranMMEID, enbID, ran, told, mmeID, plmn := u.RANCoreID, u.RANID, u.RAN, u.Ext.told, u.CoreID, u.PLMN
	r.mu.Unlock()

	var out []byte

	if p.ProcedureCode() == s1ap.ProcInitialContextSetup && p.Trigger() == s1ap.TriggeringInitiatingMessage {
		err = r.informServingMME(p, a, plmn, mmeID, initialContextSetupRequestOrder)
	}

	if err == nil && switched {
		err = setTypedIE(p, s1ap.IDMMEUES1APID2, mmeID, pathSwitchRequestAcknowledgeOrder)
	}

	switch {
	case err != nil:
	case pair != nil:
		if pair.UES1APIDPair != nil {
			pair.UES1APIDPair.MMEUES1APID, pair.UES1APIDPair.ENBUES1APID = ranMMEID, enbID
		} else {
			pair.MMEUES1APID = &ranMMEID
		}

		if err = p.SetUES1APIDs(*pair); err == nil {
			out, err = p.Marshal()
		}
	default:
		out, err = rewrite(p, &ranMMEID, &enbID)
	}

	if err != nil {
		tracing.SpanFailed(ctx, assoc.ErrTypeError)
		log.WarnContext(ctx, "failed to relay S1AP message to radio", slog.Any("error", err))

		return
	}

	switch {
	case failed:
		r.mu.Lock()
		r.ues.Remove(u)
		r.mu.Unlock()
	case !told || tracesUE(p):
		r.mu.Lock()
		traceUE(u, p)
		u.Ext.told = true
		r.mu.Unlock()
	}

	log.DebugContext(ctx, "relayed S1AP message to radio", slog.Uint64("mme_ue_s1ap_id", uint64(ranMMEID)))

	if err := ran.For(a.Operator()).Send(ctx, out, ueStream); err != nil {
		log.WarnContext(ctx, "failed to send S1AP message to radio", slog.Any("error", err))
	}

	for _, o := range stale {
		r.releaseRadioSide(ctx, o.RAN, o.RAN.Logger(r.log), o, causeSuccessfulHandover)
	}
}

// TS 36.413 clause 8.3.3.1
// TS 36.413 clause 8.4.4.2
func (r *relay) supersededLocked(u *ueContext, a *mmeAssoc) []*ueContext {
	stale := r.ues.Where(func(o *ueContext) bool {
		return o != u && o.Core == a && o.CoreIDKnown && o.CoreID == u.Ext.source
	})

	for _, o := range stale {
		r.ues.Detach(o)
	}

	return stale
}

func (r *relay) mmeUE(a *mmeAssoc, ids ueIDs) (*ueContext, []*ueContext, s1ap.Cause) {
	if ids.enb == nil {
		if ids.mme != nil {
			if u := r.ues.ByCoreID(a, *ids.mme); u != nil {
				return u, nil, s1ap.Cause{}
			}

			return nil, nil, causeUnknownMMEUEID
		}

		return nil, nil, causeUnknownENBUEID
	}

	u := r.ues.ByCoreRANID(*ids.enb)
	if u == nil || u.Core != a {
		return nil, nil, causeUnknownENBUEID
	}

	if ids.mme == nil {
		return nil, nil, causeUnknownPair
	}

	if u.CoreIDKnown {
		if u.CoreID != *ids.mme {
			return nil, nil, causeUnknownPair
		}

		return u, nil, s1ap.Cause{}
	}

	if other := r.ues.ByCoreID(a, *ids.mme); other != nil && !u.Ext.switching {
		return nil, []*ueContext{u, other}, causeUnknownMMEUEID
	}

	return u, nil, s1ap.Cause{}
}

const (
	idCSGMembershipStatus s1ap.ProtocolIEID = 146
	idRegisteredLAI       s1ap.ProtocolIEID = 159
)

// TS 36.413 clause 9.1.4.1
var initialContextSetupRequestOrder = []s1ap.ProtocolIEID{
	s1ap.IDMMEUES1APID, s1ap.IDENBUES1APID, 66, 24, 107, 73, 25, 41, 74, 106, 108, 124, idCSGMembershipStatus,
	idRegisteredLAI, s1ap.IDGUMMEI, s1ap.IDMMEUES1APID2, 165, 177, 187, 192, 196, 195, 241, 240, 248, 251, 269, 271, 277,
	283, 278, 299, 301, 306, 307, 308, 314,
}

// TS 36.300 clause 4.6.2
// TS 36.413 clause 8.3.1.2
// TS 36.413 clause 9.1.4.1
func (r *relay) informServingMME(p *s1ap.RawPDU, a *mmeAssoc, plmn db.PLMN, mmeID s1ap.MMEUES1APID,
	order []s1ap.ProtocolIEID,
) error {
	if gummei, ok := r.g.servingGUMMEI(a, plmn); ok && !present(p, s1ap.IDGUMMEI) {
		g, err := gummeiTo(gummei)
		if err != nil {
			return err
		}

		if err := setTypedIE(p, s1ap.IDGUMMEI, &g, order); err != nil {
			return err
		}
	}

	return setTypedIE(p, s1ap.IDMMEUES1APID2, mmeID, order)
}

func setTypedIE(p *s1ap.RawPDU, id s1ap.ProtocolIEID, v per.Marshaler, order []s1ap.ProtocolIEID) error {
	b, err := per.Marshal(v, per.Aligned)
	if err != nil {
		return err
	}

	return setIE(p, s1ap.RawIE{ID: id, Criticality: s1ap.CriticalityIgnore, Value: b}, order)
}

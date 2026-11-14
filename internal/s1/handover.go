package s1

import (
	"context"
	"errors"
	"log/slog"
	"slices"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/plmnid"
	"github.com/ellanetworks/mocn/internal/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var (
	causeSuccessfulHandover   = s1ap.Cause{Group: s1ap.CauseGroupRadioNetwork, Value: s1ap.CauseRadioNetworkSuccessfulHandover}
	causeUnknownTargetID      = s1ap.Cause{Group: s1ap.CauseGroupRadioNetwork, Value: s1ap.CauseRadioNetworkUnknownTargetID}
	errNoTransparentContainer = errors.New("no Source to Target Transparent Container")
)

// TS 36.413 clause 9.1.5.4
var handoverRequestOrder = []s1ap.ProtocolIEID{
	s1ap.IDMMEUES1APID, s1ap.IDHandoverType, s1ap.IDCause, s1ap.IDUEAggregateMaximumBitrate, s1ap.IDERABToBeSetupListHOReq,
	s1ap.IDSourceToTargetTransparentContainer, s1ap.IDUESecurityCapabilities, s1ap.IDHandoverRestrictionList,
	s1ap.IDTraceActivation, s1ap.IDRequestType, 124, s1ap.IDSecurityContext, s1ap.IDNASSecurityParameterstoEUTRAN, idCSGID,
	idCSGMembershipStatus, s1ap.IDGUMMEI, s1ap.IDMMEUES1APID2, 165, 177, 192, 196, 195, 241, 240, 248, 251, 269, 271, 277,
	283, 278, 299, 301, 306, 307, 308, 314,
}

const cellIdentityBits = 28

// TS 36.413 clause 9.1.5.9
var pathSwitchRequestAcknowledgeOrder = []s1ap.ProtocolIEID{
	s1ap.IDMMEUES1APID, s1ap.IDENBUES1APID, s1ap.IDUEAggregateMaximumBitrate, s1ap.IDERABToBeSwitchedULList,
	s1ap.IDERABToBeReleasedList, s1ap.IDSecurityContext, s1ap.IDCriticalityDiagnostics, s1ap.IDMMEUES1APID2,
	idCSGMembershipStatus, 195, 241, 240, 248, 251, 269, 271, 277, 283, 278, s1ap.IDHandoverRestrictionList, 299, 306,
	307, 308, 314, s1ap.IDUESecurityCapabilities, 341,
}

func (r *relay) countHandover(proc, result string) {
	r.g.core.CountHandover(proc, result)
}

// TS 36.413 clause 8.4.2.2
// TS 36.413 clause 8.4.2.3
// TS 36.300 clause 4.6.2
func (r *relay) handoverRequest(ctx context.Context, a *mmeAssoc, log *slog.Logger, pdu s1ap.PDU, msg []byte) {
	p, err := s1ap.ParseRawPDU(msg)
	if err != nil {
		r.countHandover(assoc.HandoverRequest, assoc.HandoverInvalid)
		undecodable(ctx, a, sourceMME, log, pdu, err)

		return
	}

	mmeID, ok, err := p.MMEUES1APID()
	if err != nil || !ok {
		r.countHandover(assoc.HandoverRequest, assoc.HandoverInvalid)
		terminate(ctx, a, sourceMME, log, pdu, msg)

		return
	}

	log = log.With(slog.Uint64("mme_ue_s1ap_id", uint64(mmeID)))

	fail := func(result, reason string, cause s1ap.Cause, err error) {
		r.countHandover(assoc.HandoverRequest, result)
		tracing.SpanFailed(ctx, result)
		log.WarnContext(ctx, reason, slog.Any("error", err))
		sendTo(ctx, a, log, "Handover Failure", &s1ap.HandoverFailure{MMEUES1APID: &mmeID, Cause: &cause}, true)
	}

	cell, err := targetCell(p)
	if err != nil {
		fail(assoc.HandoverInvalid, "HANDOVER REQUEST from MME rejected: no valid target cell", causeUnknownTargetID, err)
		return
	}

	cellPLMN, err := plmnid.Decode(cell.PLMNIdentity)
	if err != nil {
		fail(assoc.HandoverInvalid, "HANDOVER REQUEST from MME rejected: invalid target E-UTRAN CGI", causeUnknownTargetID, err)
		return
	}

	log = log.With(slog.String("target_plmn", cellPLMN.String()), slog.Uint64("target_cell_identity", uint64(cell.CellID)))
	trace.SpanFromContext(ctx).SetAttributes(attribute.String(assoc.AttrOperator, a.Operator()))

	var (
		n     *ranAssoc
		radio db.GlobalENBID
		op    assoc.Operator
	)

	r.g.core.View(func() {
		op, _ = r.g.core.OperatorLocked(a.OperatorID())
		if n = servingCell(r.g.core.OperatorRadiosLocked(a.OperatorID()), cellPLMN, cell.CellID); n != nil {
			radio = n.Setup.globalENBID
		}
	})

	if n == nil || len(op.PLMNs) == 0 {
		fail(assoc.HandoverNoRadio, "HANDOVER REQUEST from MME rejected: no radio of the operator that is up serves the target cell",
			causeUnknownTargetID, nil)

		return
	}

	plmn := servingPLMN(p, op.PLMNs, cellPLMN)

	r.mu.Lock()

	u := &ueContext{
		RAN: n, Core: a, OperatorID: a.OperatorID(), PLMN: plmn, Since: r.g.cfg.Now(),
		Ext: ueInfo{radio: radio, ecgi: &ECGI{PLMN: cellPLMN, CellID: cell.CellID}},
	}

	if err := r.ues.Add(u); err != nil {
		r.mu.Unlock()
		fail(assoc.HandoverError, "dropped HANDOVER REQUEST", causeHOFailure, err)

		return
	}

	r.ues.SetCoreID(u, mmeID)
	traceUE(u, p)

	ranMMEID := u.RANCoreID
	r.mu.Unlock()

	out, err := r.handoverRequestToRadio(p, a, plmn, ranMMEID, mmeID)
	if err != nil {
		r.mu.Lock()
		r.ues.Remove(u)
		r.mu.Unlock()
		fail(assoc.HandoverError, "failed to relay HANDOVER REQUEST", causeHOFailure, err)

		return
	}

	r.mu.Lock()
	u.Ext.told = true
	r.mu.Unlock()

	if !r.g.core.IsUp(n) {
		r.mu.Lock()
		r.ues.Remove(u)
		r.mu.Unlock()
		fail(assoc.HandoverNoRadio, "HANDOVER REQUEST from MME rejected: the target radio left", causeUnknownTargetID, nil)

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
	log.InfoContext(ctx, "HANDOVER REQUEST relayed to radio", slog.String("target", radio.Ref()),
		slog.Uint64("ran_mme_ue_s1ap_id", uint64(ranMMEID)))
}

// TS 36.300 clause 4.6.2
// TS 36.413 clause 8.4.2.2
func (r *relay) handoverRequestToRadio(p *s1ap.RawPDU, a *mmeAssoc, plmn db.PLMN, ranMMEID, mmeID s1ap.MMEUES1APID,
) ([]byte, error) {
	if err := p.SetMMEUES1APID(ranMMEID); err != nil {
		return nil, err
	}

	if err := r.informServingMME(p, a, plmn, mmeID, handoverRequestOrder); err != nil {
		return nil, err
	}

	return p.Marshal()
}

// TS 36.413 clause 8.4.2.2
// TS 36.413 clause 9.2.1.56
func targetCell(p *s1ap.RawPDU) (s1ap.EUTRANCGI, error) {
	c, err := assoc.GetIE[s1ap.TransparentContainer](p.Get, s1ap.IDSourceToTargetTransparentContainer)
	if err != nil {
		return s1ap.EUTRANCGI{}, err
	}

	if c == nil {
		return s1ap.EUTRANCGI{}, errNoTransparentContainer
	}

	t, err := s1ap.ParseSourceeNBToTargeteNBTransparentContainer(*c)
	if err != nil {
		return s1ap.EUTRANCGI{}, err
	}

	return t.TargetCellID, nil
}

// TS 36.413 clause 8.4.2.4
// TS 36.413 clause 9.2.1.22
func servingPLMN(p *s1ap.RawPDU, plmns []db.PLMN, cell db.PLMN) db.PLMN {
	if l, err := assoc.GetIE[s1ap.HandoverRestrictionList](p.Get, s1ap.IDHandoverRestrictionList); err == nil && l != nil {
		if v, err := plmnid.Decode(l.ServingPLMN); err == nil && slices.Contains(plmns, v) {
			return v
		}
	}

	if slices.Contains(plmns, cell) {
		return cell
	}

	return plmns[0]
}

// TS 36.413 clause 9.2.1.37
var enbIDBits = map[db.ENBIDType]uint{
	db.ENBIDMacro: 20, db.ENBIDHome: 28, db.ENBIDShortMacro: 18, db.ENBIDLongMacro: 21,
}

// TS 36.413 clause 9.2.1.37
// TS 36.413 clause 9.2.1.38
func servingCell(radios []*ranAssoc, plmn db.PLMN, cellID uint32) *ranAssoc {
	var serving, exact []*ranAssoc

	for _, n := range radios {
		if !n.Setup.ownsCell(cellID) {
			continue
		}

		serving = append(serving, n)

		if n.Setup.globalENBID.PLMN == plmn {
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

// TS 36.413 clause 8.4.4.2
// TS 36.413 clause 8.4.4.3
// TS 36.300 clause 4.6.2
func (r *relay) pathSwitchRequest(ctx context.Context, n *ranAssoc, log *slog.Logger, pdu s1ap.PDU, msg []byte) {
	p, err := s1ap.ParseRawPDU(msg)
	if err != nil {
		r.countHandover(assoc.PathSwitchRequest, assoc.HandoverInvalid)
		undecodable(ctx, n, sourceRadio, log, pdu, err)

		return
	}

	enbID, okENB, errENB := p.ENBUES1APID()
	mmeID, okMME, errMME := p.SourceMMEUES1APID()

	if errENB != nil || errMME != nil || !okENB || !okMME {
		r.countHandover(assoc.PathSwitchRequest, assoc.HandoverInvalid)
		terminate(ctx, n, sourceRadio, log, pdu, msg)

		return
	}

	log = log.With(slog.Uint64("enb_ue_s1ap_id", uint64(enbID)), slog.Uint64("source_mme_ue_s1ap_id", uint64(mmeID)))

	reject := func(result, reason string, cause s1ap.Cause, err error) {
		r.countHandover(assoc.PathSwitchRequest, result)
		tracing.SpanFailed(ctx, result)
		log.WarnContext(ctx, reason, slog.Any("error", err))
		sendTo(ctx, n, log, "Path Switch Request Failure", &s1ap.PathSwitchRequestFailure{
			MMEUES1APID: &mmeID, ENBUES1APID: &enbID, Cause: &cause,
		}, true)
	}

	g, err := assoc.GetIE[s1ap.GUMMEI](p.Get, s1ap.IDSourceMMEGUMMEI)
	if err != nil {
		reject(assoc.HandoverInvalid, "PATH SWITCH REQUEST from radio rejected: invalid Source MME GUMMEI", causeSemanticError, err)
		return
	}

	if g == nil {
		reject(names.NoCore, "PATH SWITCH REQUEST from radio rejected: no Source MME GUMMEI", causeTransportUnavailable, nil)
		return
	}

	gummeiPLMN, err := plmnid.Decode(g.PLMNIdentity)
	if err != nil {
		reject(assoc.HandoverInvalid, "PATH SWITCH REQUEST from radio rejected: invalid Source MME GUMMEI", causeSemanticError, err)
		return
	}

	var (
		op    assoc.Operator
		cands []mmeCandidate
		found bool
		radio db.GlobalENBID
	)

	r.g.core.View(func() {
		op, cands, found = r.g.selectionLocked(gummeiPLMN)

		if n.Setup != nil {
			radio = n.Setup.globalENBID
		}
	})

	if !found {
		reject(names.NoCore, "PATH SWITCH REQUEST from radio rejected: no operator serves the PLMN of the Source MME GUMMEI",
			causeUnknownPLMN, nil)

		return
	}

	trace.SpanFromContext(ctx).SetAttributes(attribute.String(assoc.AttrOperator, op.Name))

	group := uint16(g.MMEGroupID[0])<<8 | uint16(g.MMEGroupID[1])

	mme := findServing(selectable(cands), gummeiPLMN, &group, uint8(g.MMECode))
	if mme == nil {
		reject(names.NoCore, "PATH SWITCH REQUEST from radio rejected: no selectable MME serves the Source MME GUMMEI",
			causeTransportUnavailable, nil)

		return
	}

	plmn := gummeiPLMN

	if tai, err := assoc.GetIE[s1ap.TAI](p.Get, s1ap.IDTAI); err == nil && tai != nil {
		if v, err := plmnid.Decode(tai.PLMNIdentity); err == nil && slices.Contains(op.PLMNs, v) {
			plmn = v
		}
	}

	r.mu.Lock()

	if old := r.ues.ByRANID(n, enbID); old != nil {
		gone := r.ues.Remove(old)
		r.mu.Unlock()

		r.countHandover(assoc.PathSwitchRequest, assoc.HandoverInvalid)
		tracing.SpanFailed(ctx, assoc.HandoverInvalid)
		log.WarnContext(ctx, "PATH SWITCH REQUEST reuses an allocated eNB UE S1AP ID")
		sendUEErrorIndication(ctx, n, log, causeUnknownENBUEID, nil, &enbID)
		r.resetMMEs(ctx, gone, causeUnknownENBUEID)

		return
	}

	u := &ueContext{
		RAN: n, RANID: enbID, RANIDKnown: true, Core: mme, OperatorID: op.ID, PLMN: plmn, Since: r.g.cfg.Now(),
		Ext: ueInfo{radio: radio, switching: true, source: mmeID},
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

	send := r.sendMME
	r.mu.Unlock()

	if err := send(ctx, mme, out, ueStream); err != nil {
		r.mu.Lock()
		removed := len(r.ues.Remove(u)) > 0
		r.mu.Unlock()

		if removed {
			reject(assoc.HandoverError, "failed to send PATH SWITCH REQUEST to MME", causeTransportUnavailable, err)
		}

		return
	}

	r.countHandover(assoc.PathSwitchRequest, assoc.HandoverRelayed)
	log.InfoContext(ctx, "PATH SWITCH REQUEST relayed", slog.String("operator", op.Name), slog.Any("mme", mme.Addrs()),
		slog.Uint64("mme_enb_ue_s1ap_id", uint64(u.CoreRANID)))
}

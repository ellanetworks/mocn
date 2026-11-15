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
)

// TS 36.413 clause 8.6.2.5
// TS 36.300 clause 4.6.2
// TS 36.300 clause 19.2.2.6
// TS 23.401 clause 5.19.1
func (r *relay) rerouteNASRequest(ctx context.Context, a *mmeAssoc, log *slog.Logger, value, msg []byte) {
	req, err := s1ap.ParseRerouteNASRequest(value)
	if err != nil {
		tracing.SpanFailed(ctx, assoc.ErrTypeInvalid)
		log.WarnContext(ctx, "rejected Reroute NAS Request from MME", slog.Any("error", err))

		indicateParseError(ctx, a, log, s1ap.ProcRerouteNASRequest, err)

		return
	}

	group := uint16(req.MMEGroupID[0])<<8 | uint16(req.MMEGroupID[1])
	log = log.With(slog.Uint64("mme_enb_ue_s1ap_id", uint64(req.ENBUES1APID)), slog.Uint64("mme_group_id", uint64(group)))

	r.mu.Lock()

	u, _, cause := r.mmeUE(a, ueIDs{mme: req.MMEUES1APID, enb: &req.ENBUES1APID})
	if req.MMEUES1APID == nil {
		u = r.ues.ByCoreRANID(req.ENBUES1APID)
	}

	if u != nil && u.Core != a {
		u, cause = nil, causeUnknownENBUEID
	}

	if u == nil {
		gone := r.ues.Remove(r.ues.OnCore(a, req.MMEUES1APID, &req.ENBUES1APID)...)
		r.mu.Unlock()

		tracing.SpanFailed(ctx, assoc.ErrTypeUnknownUE)
		log.WarnContext(ctx, "Reroute NAS Request from MME for an unknown UE", slog.String("cause", cause.String()))
		sendUEErrorIndication(ctx, a, log, cause, req.MMEUES1APID, &req.ENBUES1APID)
		r.resetRANs(ctx, a.Operator(), gone, cause)

		return
	}

	operatorID, plmn, enbID := u.OperatorID, u.PLMN, u.CoreRANID
	r.mu.Unlock()

	var cands []mmeCandidate

	r.g.core.View(func() { cands = r.g.candidatesLocked(operatorID) })

	target := rerouteTarget(selectable(cands), plmn, group, req.AdditionalGUTI, r.intN)

	out, err := reroutedInitialUEMessage(msg, req, enbID)

	r.mu.Lock()

	if r.ues.ByCoreRANID(req.ENBUES1APID) != u {
		r.mu.Unlock()
		return
	}

	if target != nil && target.Lost() {
		target = nil
	}

	if target == nil && !a.Lost() {
		target = a
	}

	if target == nil || err != nil {
		gone := r.ues.Remove(u)
		r.mu.Unlock()

		reason := "INITIAL UE MESSAGE not rerouted: no selectable MME in the MME group and the requesting MME is lost"
		if err != nil {
			reason = "INITIAL UE MESSAGE not rerouted: invalid Reroute NAS Request"
		}

		tracing.SpanFailed(ctx, names.NoCore)
		log.WarnContext(ctx, reason, slog.Any("error", err))
		r.resetRANs(ctx, a.Operator(), gone, causeTransportUnavailable)

		return
	}

	r.ues.Rebind(u, target)
	r.superviseLocked(ctx, u, log, "INITIAL UE MESSAGE")

	send := r.sendMME
	r.mu.Unlock()

	if err := send(ctx, target, out, ueStream); err != nil {
		log.WarnContext(ctx, "failed to send the rerouted INITIAL UE MESSAGE to MME", slog.Any("error", err))
		return
	}

	log.InfoContext(ctx, "INITIAL UE MESSAGE rerouted", slog.Any("mme", target.Addrs()))
}

// TS 23.401 clause 5.19.1
func rerouteTarget(cands []mmeCandidate, plmn db.PLMN, group uint16, guti *s1ap.AdditionalGUTI,
	intN func(int) int,
) *mmeAssoc {
	var valid []mmeCandidate

	for _, c := range cands {
		if slices.ContainsFunc(c.served, func(s ServedGUMMEIs) bool {
			return slices.Contains(s.PLMNs, plmn) && slices.Contains(s.GroupIDs, group)
		}) {
			valid = append(valid, c)
		}
	}

	if guti != nil {
		if gp, err := plmnid.Decode(guti.GUMMEI.PLMNIdentity); err == nil {
			gg := uint16(guti.GUMMEI.MMEGroupID[0])<<8 | uint16(guti.GUMMEI.MMEGroupID[1])
			if a := findServing(valid, gp, &gg, uint8(guti.GUMMEI.MMECode)); a != nil {
				return a
			}
		}
	}

	if a := weighted(valid, func(c mmeCandidate) bool { return !c.overloaded }, intN); a != nil {
		return a
	}

	return weighted(valid, func(mmeCandidate) bool { return true }, intN)
}

const (
	idCSGID                                   s1ap.ProtocolIEID = 127
	idCellAccessMode                          s1ap.ProtocolIEID = 145
	idGWTransportLayerAddress                 s1ap.ProtocolIEID = 155
	idRelayNodeIndicator                      s1ap.ProtocolIEID = 160
	idGUMMEIType                              s1ap.ProtocolIEID = 170
	idTunnelInformationForBBF                 s1ap.ProtocolIEID = 176
	idSIPTOLGWTransportLayerAddress           s1ap.ProtocolIEID = 184
	idLHNID                                   s1ap.ProtocolIEID = 186
	idCEModeBSupportIndicator                 s1ap.ProtocolIEID = 242
	idDCNID                                   s1ap.ProtocolIEID = 246
	idCoverageLevel                           s1ap.ProtocolIEID = 250
	idUEApplicationLayerMeasurementCapability s1ap.ProtocolIEID = 263
	idEDTSession                              s1ap.ProtocolIEID = 281
	idIABNodeIndication                       s1ap.ProtocolIEID = 302
	idLTENTNTAIInformation                    s1ap.ProtocolIEID = 339
)

// TS 36.413 clause 9.1.7.1
var initialUEMessageOrder = []s1ap.ProtocolIEID{
	s1ap.IDENBUES1APID, s1ap.IDNASPDU, s1ap.IDTAI, s1ap.IDEUTRANCGI, s1ap.IDRRCEstablishmentCause, s1ap.IDSTMSI,
	idCSGID, s1ap.IDGUMMEI, idCellAccessMode, idGWTransportLayerAddress, idRelayNodeIndicator, idGUMMEIType,
	idTunnelInformationForBBF, idSIPTOLGWTransportLayerAddress, idLHNID, s1ap.IDMMEGroupID, s1ap.IDUEUsageType,
	idCEModeBSupportIndicator, idDCNID, idCoverageLevel, idUEApplicationLayerMeasurementCapability, idEDTSession,
	idIABNodeIndication, idLTENTNTAIInformation,
}

// TS 36.413 clause 8.6.2.5
// TS 23.401 clause 5.19.1
func reroutedInitialUEMessage(msg []byte, req *s1ap.RerouteNASRequest, enbID s1ap.ENBUES1APID) ([]byte, error) {
	reroute, err := s1ap.ParseRawPDU(msg)
	if err != nil {
		return nil, err
	}

	p, err := s1ap.ParseRawPDU([]byte(req.S1Message))
	if err != nil {
		return nil, err
	}

	if p.ProcedureCode() != s1ap.ProcInitialUEMessage || p.Trigger() != s1ap.TriggeringInitiatingMessage {
		return nil, errors.New("the S1 Message is not an INITIAL UE MESSAGE")
	}

	if err := p.SetENBUES1APID(enbID); err != nil {
		return nil, err
	}

	for _, id := range []s1ap.ProtocolIEID{s1ap.IDMMEGroupID, s1ap.IDUEUsageType} {
		i, err := reroute.Index(id)
		if err != nil {
			return nil, err
		}

		if i < 0 {
			continue
		}

		ie, err := reroute.IE(i)
		if err != nil {
			return nil, err
		}

		ie.Criticality = s1ap.CriticalityIgnore

		if err := setIE(p, ie, initialUEMessageOrder); err != nil {
			return nil, err
		}
	}

	return p.Marshal()
}

func setIE(p *s1ap.RawPDU, ie s1ap.RawIE, order []s1ap.ProtocolIEID) error {
	i, err := p.Index(ie.ID)
	if err != nil {
		return err
	}

	if i >= 0 {
		return p.SetValue(i, ie.Value)
	}

	rank := slices.Index(order, ie.ID)
	at := p.Len()

	if rank >= 0 {
		for i := range p.Len() {
			f, err := p.IE(i)
			if err != nil {
				return err
			}

			if r := slices.Index(order, f.ID); r < 0 || r > rank {
				at = i
				break
			}
		}
	}

	return p.Insert(at, ie)
}

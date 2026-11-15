package ng

import (
	"context"
	"errors"
	"log/slog"
	"slices"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/tracing"
)

// TS 38.413 clause 8.6.5
// TS 38.300 clause 4.10.2.3
// TS 23.502 clause 4.2.2.2.3
func (r *relay) rerouteNASRequest(ctx context.Context, a *amfAssoc, log *slog.Logger, value, msg []byte) {
	req, err := ngap.ParseRerouteNASRequest(value)
	if err != nil {
		tracing.SpanFailed(ctx, assoc.ErrTypeInvalid)
		log.WarnContext(ctx, "rejected Reroute NAS Request from AMF", slog.Any("error", err))

		indicateParseError(ctx, a, log, ngap.ProcRerouteNASRequest, err)

		return
	}

	log = log.With(slog.Uint64("amf_ran_ue_ngap_id", uint64(req.RANUENGAPID)), slog.Uint64("amf_set_id", uint64(req.AMFSetID)))

	r.mu.Lock()

	u, _, cause := r.amfUE(a, ueIDs{amf: req.AMFUENGAPID, ran: &req.RANUENGAPID})
	if req.AMFUENGAPID == nil {
		u = r.ues.ByCoreRANID(req.RANUENGAPID)
	}

	if u != nil && u.Core != a {
		u, cause = nil, causeUnknownLocalID
	}

	if u == nil {
		gone := r.ues.Remove(r.ues.OnCore(a, req.AMFUENGAPID, &req.RANUENGAPID)...)
		r.mu.Unlock()

		tracing.SpanFailed(ctx, assoc.ErrTypeUnknownUE)
		log.WarnContext(ctx, "Reroute NAS Request from AMF for an unknown UE", slog.String("cause", cause.String()))
		sendUEErrorIndication(ctx, a, log, cause, req.AMFUENGAPID, &req.RANUENGAPID)
		r.resetRANs(ctx, a.Operator(), gone, cause)

		return
	}

	operatorID, plmn := u.OperatorID, u.PLMN
	r.mu.Unlock()

	var cands []amfCandidate

	r.g.core.View(func() { cands = r.g.candidatesLocked(operatorID) })

	target, _ := selectAMF(selectable(cands), ueIdentity{plmn: plmn, set: ngap.Ptr(uint16(req.AMFSetID))}, r.intN)

	out, err := reroutedInitialUEMessage(msg, req)
	if err == nil {
		err = checkPartiallyAllowedNSSAI(req)
	}

	r.mu.Lock()

	if r.ues.ByCoreRANID(req.RANUENGAPID) != u {
		r.mu.Unlock()
		return
	}

	if target != nil && target.Lost() {
		target = nil
	}

	if target == nil || err != nil {
		gone := r.ues.Remove(u)
		r.mu.Unlock()

		reason := "INITIAL UE MESSAGE not rerouted: no selectable AMF in the AMF Set"
		if err != nil {
			reason = "INITIAL UE MESSAGE not rerouted: invalid Reroute NAS Request"
		}

		tracing.SpanFailed(ctx, names.NoCore)
		log.WarnContext(ctx, reason, slog.Any("error", err))
		r.resetRANs(ctx, a.Operator(), gone, causeTransportUnavailable)

		return
	}

	r.ues.Rebind(u, target)
	u.Ext.sets = r.sets[target]
	r.superviseLocked(ctx, u, log, "INITIAL UE MESSAGE")

	send := r.sendAMF
	r.mu.Unlock()

	if err := send(ctx, target, out, ueStream); err != nil {
		log.WarnContext(ctx, "failed to send the rerouted INITIAL UE MESSAGE to AMF", slog.Any("error", err))
		return
	}

	log.InfoContext(ctx, "INITIAL UE MESSAGE rerouted", slog.Any("amf", target.Addrs()))
}

// TS 38.413 clause 8.6.5.3
func checkPartiallyAllowedNSSAI(req *ngap.RerouteNASRequest) error {
	if len(req.PartiallyAllowedNSSAI) == 0 {
		return nil
	}

	if len(req.AllowedNSSAI)+len(req.PartiallyAllowedNSSAI) > maxnoofAllowedSNSSAIs {
		return errors.New("more than eight S-NSSAIs in the Allowed NSSAI and the Partially Allowed NSSAI")
	}

	for _, p := range req.PartiallyAllowedNSSAI {
		if slices.ContainsFunc(req.AllowedNSSAI, func(a ngap.AllowedNSSAIItem) bool {
			return snssaiFrom(a.SNSSAI) == snssaiFrom(p.SNSSAI)
		}) {
			return errors.New("an S-NSSAI of the Partially Allowed NSSAI is also in the Allowed NSSAI")
		}
	}

	return nil
}

// TS 38.413 clause 9.2.5.1
var initialUEMessageOrder = []ngap.ProtocolIEID{
	ngap.IDRANUENGAPID, ngap.IDNASPDU, ngap.IDUserLocationInformation, ngap.IDRRCEstablishmentCause, ngap.IDFiveGSTMSI,
	ngap.IDAMFSetID, ngap.IDUEContextRequest, ngap.IDAllowedNSSAI, ngap.IDSourceToTargetAMFInformationReroute,
	ngap.IDSelectedPLMNIdentity, ngap.IDIABNodeIndication, ngap.IDCEmodeBSupportIndicator, ngap.IDLTEMIndication,
	ngap.IDEDTSession, ngap.IDAuthenticatedIndication, ngap.IDNPNAccessInformation, ngap.IDRedCapIndication,
	ngap.IDSelectedNID, ngap.IDMobileIABNodeIndication, ngap.IDPartiallyAllowedNSSAI, ngap.IDERedCapIndication,
	ngap.IDAUN3DeviceAccessInfo, ngap.IDGUAMI, ngap.IDGUAMIType, ngap.IDRequestedNSSAI,
}

// TS 38.413 clause 8.6.5.2
const maxnoofAllowedSNSSAIs = 8

func reroutedInitialUEMessage(msg []byte, req *ngap.RerouteNASRequest) ([]byte, error) {
	reroute, err := ngap.ParseRawPDU(msg)
	if err != nil {
		return nil, err
	}

	p, err := ngap.ParseRawPDU([]byte(req.NGAPMessage))
	if err != nil {
		return nil, err
	}

	if p.ProcedureCode() != ngap.ProcInitialUEMessage || p.Trigger() != ngap.TriggeringInitiatingMessage {
		return nil, errors.New("the NGAP Message is not an INITIAL UE MESSAGE")
	}

	for _, id := range []ngap.ProtocolIEID{
		ngap.IDAMFSetID, ngap.IDAllowedNSSAI, ngap.IDSourceToTargetAMFInformationReroute, ngap.IDPartiallyAllowedNSSAI,
	} {
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

		if id == ngap.IDAMFSetID {
			ie.Criticality = ngap.CriticalityIgnore
		}

		if err := setIE(p, ie, initialUEMessageOrder); err != nil {
			return nil, err
		}
	}

	return p.Marshal()
}

func setIE(p *ngap.RawPDU, ie ngap.RawIE, order []ngap.ProtocolIEID) error {
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

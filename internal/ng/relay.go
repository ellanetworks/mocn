package ng

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"slices"
	"sync"
	"time"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/core/per"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/gw"
	"github.com/ellanetworks/mocn/internal/metrics"
	"github.com/ellanetworks/mocn/internal/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type sender interface {
	Send(ctx context.Context, msg []byte, stream uint16) error
}

const (
	ueStream                    = 1
	maxnoofNGConnectionsToReset = 65536
	initialUERelayed            = "relayed"
	initialUEUnknownPLMN        = "unknown_plmn"
	initialUENoAMF              = "no_amf"
	initialUERejected           = "rejected"
	initialUEInvalid            = "invalid"
	initialUEError              = "error"
	sourceRadio                 = "radio"
	sourceAMF                   = "AMF"
)

var (
	causeUnknownLocalID       = ngap.Cause{Group: ngap.CauseGroupRadioNetwork, Value: ngap.CauseRadioNetworkUnknownLocalUENGAPID}
	causeInconsistentRemoteID = ngap.Cause{Group: ngap.CauseGroupRadioNetwork, Value: ngap.CauseRadioNetworkInconsistentRemoteUEID}
	causeNGRANRelease         = ngap.Cause{Group: ngap.CauseGroupRadioNetwork, Value: ngap.CauseRadioNetworkReleaseDueToNGRANGeneratedReason}
	causeRadioUnspec          = ngap.Cause{Group: ngap.CauseGroupRadioNetwork, Value: ngap.CauseRadioNetworkUnspecified}
)

var initialUEResults = []string{
	initialUERelayed, initialUEUnknownPLMN, initialUENoAMF, initialUERejected, initialUEInvalid, initialUEError,
}

type relay struct {
	g       *Gateway
	log     *slog.Logger
	intN    func(int) int
	metrics *metrics.Metrics

	mu      sync.Mutex
	ues     ueTable
	sendAMF func(context.Context, *AMFAssociation, []byte, uint16) error
}

func newRelay(g *Gateway, intN func(int) int) *relay {
	if intN == nil {
		intN = rand.IntN
	}

	return &relay{g: g, log: g.log, intN: intN, ues: newUETable(), sendAMF: sendAMF, metrics: g.cfg.Metrics}
}

func sendAMF(ctx context.Context, a *AMFAssociation, msg []byte, stream uint16) error {
	return a.Send(ctx, msg, stream)
}

func ranLog(log *slog.Logger, n *RANAssociation) *slog.Logger {
	return log.With(slog.Any("tnl_addresses", n.addrs))
}

func amfLog(log *slog.Logger, a *AMFAssociation) *slog.Logger {
	return log.With(slog.Int64("operator_id", a.client.operatorID), slog.Any("amf", a.client.addrs))
}

// TS 38.300 clause 4.10.2.3
func (r *relay) fromRAN(ctx context.Context, from *RANAssociation, pdu ngap.PDU, msg []byte) {
	log := ranLog(r.log, from)

	if m, ok := pdu.(*ngap.InitiatingMessage); ok {
		switch m.ProcedureCode {
		case ngap.ProcInitialUEMessage:
			r.initialUEMessage(ctx, from, log, msg)
			return
		case ngap.ProcNGReset:
			r.resetFromRAN(ctx, from, log, m.Value)
			return
		case ngap.ProcErrorIndication:
			r.errorIndicationFromRAN(ctx, from, log, m.Value, msg)
			return
		case ngap.ProcPathSwitchRequest:
			terminate(ctx, sourceRadio, log, pdu)
			return
		}
	}

	if m, ok := pdu.(*ngap.SuccessfulOutcome); ok && m.ProcedureCode == ngap.ProcNGReset {
		log.DebugContext(ctx, "NG Reset Acknowledge from radio")
		return
	}

	p, ok := uePDU(ctx, log, sourceRadio, msg)
	if !ok {
		terminate(ctx, sourceRadio, log, pdu)
		return
	}

	r.uplink(ctx, from, log, p)
}

// TS 38.300 clause 4.10.2.3
func (r *relay) fromAMF(ctx context.Context, from *AMFAssociation, pdu ngap.PDU, msg []byte) {
	log := amfLog(r.log, from)

	if m, ok := pdu.(*ngap.InitiatingMessage); ok {
		switch m.ProcedureCode {
		case ngap.ProcNGReset:
			r.resetFromAMF(ctx, from, log, m.Value)
			return
		case ngap.ProcErrorIndication:
			r.errorIndicationFromAMF(ctx, from, log, m.Value, msg)
			return
		case ngap.ProcHandoverResourceAllocation:
			terminate(ctx, sourceAMF, log, pdu)
			return
		case ngap.ProcPaging:
			r.paging(ctx, from, log, m.Value, msg)
			return
		case ngap.ProcAMFStatusIndication:
			r.g.amfStatusIndication(ctx, from, log, m.Value, msg)
			return
		case ngap.ProcOverloadStart:
			r.g.overloadStart(ctx, from, log, m.Value)
			return
		case ngap.ProcOverloadStop:
			r.g.overloadStop(ctx, from, log)
			return
		case ngap.ProcRerouteNASRequest:
			r.rerouteNASRequest(ctx, from, log, m.Value, msg)
			return
		}
	}

	if m, ok := pdu.(*ngap.SuccessfulOutcome); ok && m.ProcedureCode == ngap.ProcNGReset {
		log.DebugContext(ctx, "NG Reset Acknowledge from AMF")
		return
	}

	p, ok := uePDU(ctx, log, sourceAMF, msg)
	if !ok {
		terminate(ctx, sourceAMF, log, pdu)
		return
	}

	r.downlink(ctx, from, log, p)
}

func uePDU(ctx context.Context, log *slog.Logger, from string, msg []byte) (*ngap.RawPDU, bool) {
	p, err := ngap.ParseRawPDU(msg)
	if err != nil {
		tracing.SpanFailed(ctx, errTypeInvalid)
		log.WarnContext(ctx, "undecodable NGAP message from "+from, slog.Any("error", err))

		return nil, false
	}

	if p.Index(ngap.IDAMFUENGAPID) < 0 && p.Index(ngap.IDRANUENGAPID) < 0 && p.Index(ngap.IDUENGAPIDs) < 0 {
		return nil, false
	}

	return p, true
}

func (r *relay) ranLost(ctx context.Context, from *RANAssociation, cause ngap.Cause) {
	r.mu.Lock()
	gone := r.ues.remove(r.ues.where(func(u *ueContext) bool { return u.ran == from })...)
	r.mu.Unlock()

	if len(gone) > 0 {
		ranLog(r.log, from).InfoContext(ctx, "radio UE contexts released", slog.Int("ues", len(gone)),
			slog.String("cause", cause.String()))
	}

	r.resetAMFs(ctx, gone, cause)
}

func (r *relay) amfLost(ctx context.Context, from *AMFAssociation) {
	r.mu.Lock()
	from.lost = true
	gone := r.ues.remove(r.ues.where(func(u *ueContext) bool { return u.amf == from })...)
	r.mu.Unlock()

	if len(gone) > 0 {
		amfLog(r.log, from).InfoContext(ctx, "AMF lost, UE contexts released", slog.Int("ues", len(gone)))
	}

	r.resetRANs(ctx, from.client.operator(), gone, causeTransportUnavailable)
}

func getIE[T any, P interface {
	*T
	per.Unmarshaler
}](p *ngap.RawPDU, id ngap.ProtocolIEID,
) (*T, error) {
	var v T

	ok, err := p.Get(id, P(&v))
	if err != nil || !ok {
		return nil, err
	}

	return &v, nil
}

func identityOf(ctx context.Context, log *slog.Logger, p *ngap.RawPDU, plmn db.PLMN) ueIdentity {
	id := ueIdentity{plmn: plmn}

	if g, err := getIE[ngap.GUAMI](p, ngap.IDGUAMI); err != nil {
		log.WarnContext(ctx, "ignored invalid GUAMI in INITIAL UE MESSAGE", slog.Any("error", err))
	} else if g != nil {
		if v, err := guamiFrom(*g); err == nil {
			id.guami = &v
		}
	}

	if s, err := getIE[ngap.FiveGSTMSI](p, ngap.IDFiveGSTMSI); err != nil {
		log.WarnContext(ctx, "ignored invalid 5G-S-TMSI in INITIAL UE MESSAGE", slog.Any("error", err))
	} else if s != nil {
		id.stmsi = &fiveGSTMSI{setID: uint16(s.AMFSetID), pointer: uint8(s.AMFPointer)}
	}

	return id
}

// TS 38.413 clause 8.6.1
// TS 38.300 clause 4.10.2.3
func (r *relay) initialUEMessage(ctx context.Context, n *RANAssociation, log *slog.Logger, msg []byte) {
	p, err := ngap.ParseRawPDU(msg)
	if err != nil {
		r.rejectInitialUE(ctx, log, initialUEInvalid, "undecodable INITIAL UE MESSAGE", err)
		return
	}

	ranID, hasRANID, err := p.RANUENGAPID()
	if err != nil || !hasRANID {
		r.rejectInitialUE(ctx, log, initialUEInvalid, "INITIAL UE MESSAGE without a valid RAN UE NGAP ID", err)
		return
	}

	uli, err := getIE[ngap.UserLocationInformation](p, ngap.IDUserLocationInformation)
	if err != nil || uli == nil || uli.Kind != ngap.UserLocationNR {
		r.rejectInitialUE(ctx, log, initialUEInvalid, "INITIAL UE MESSAGE without a valid NR User Location Information", err)
		return
	}

	plmn, err := gw.DecodePLMN(uli.TAI.PLMNIdentity)
	if err != nil {
		r.rejectInitialUE(ctx, log, initialUEInvalid, "INITIAL UE MESSAGE with an invalid PLMN", err)
		return
	}

	id := identityOf(ctx, log, p, plmn)

	r.g.mu.Lock()
	op, cands, found := r.g.selectionLocked(plmn)

	var radio db.GlobalGNBID
	if n.setup != nil {
		radio = n.setup.globalGNBID
	}

	r.g.mu.Unlock()

	log = log.With(slog.Uint64("ran_ue_ngap_id", uint64(ranID)), slog.String("plmn", plmn.String()))

	r.mu.Lock()

	if old := r.ues.byRANID[ranUEKey{n, ranID}]; old != nil {
		gone := r.ues.remove(old)
		r.mu.Unlock()

		r.countInitialUE(initialUERejected)
		tracing.SpanFailed(ctx, initialUERejected)
		log.WarnContext(ctx, "INITIAL UE MESSAGE reuses an allocated RAN UE NGAP ID")
		sendUEErrorIndication(ctx, n, log, causeInconsistentRemoteID, nil, &ranID)
		r.resetAMFs(ctx, gone, causeInconsistentRemoteID)

		return
	}

	if !found {
		r.countInitialUE(initialUEUnknownPLMN)
		tracing.SpanFailed(ctx, initialUEUnknownPLMN)
		log.WarnContext(ctx, "INITIAL UE MESSAGE not routed: no operator serves the PLMN")
		r.releaseUnrouted(ctx, n, log, ranID, causeUnknownPLMN)

		return
	}

	trace.SpanFromContext(ctx).SetAttributes(attribute.String(attrOperator, op.name))

	amf, rule := selectAMF(selectable(cands), id, r.intN)
	if amf == nil {
		r.countInitialUE(initialUENoAMF)
		tracing.SpanFailed(ctx, initialUENoAMF)
		log.WarnContext(ctx, "INITIAL UE MESSAGE not routed: no selectable AMF of the operator", slog.String("operator", op.name))
		r.releaseUnrouted(ctx, n, log, ranID, causeTransportUnavailable)

		return
	}

	u := &ueContext{ran: n, ranID: ranID, amf: amf, operatorID: op.id, plmn: plmn, radio: radio, since: r.g.cfg.Now()}

	r.updateLocation(u, p)

	if err := r.ues.add(u); err != nil {
		r.mu.Unlock()
		r.rejectInitialUE(ctx, log, initialUEError, "dropped INITIAL UE MESSAGE", err)

		return
	}

	out, err := rewrite(p, nil, &u.amfRANID)
	if err != nil {
		r.ues.remove(u)
		r.mu.Unlock()
		r.rejectInitialUE(ctx, log, initialUEError, "failed to relay INITIAL UE MESSAGE", err)

		return
	}

	send := r.sendAMF
	r.mu.Unlock()

	if err := send(ctx, amf, out, ueStream); err != nil {
		r.countInitialUE(initialUEError)
		tracing.SpanFailed(ctx, initialUEError)
		log.WarnContext(ctx, "failed to send INITIAL UE MESSAGE to AMF", slog.Any("error", err))

		r.mu.Lock()

		if len(r.ues.remove(u)) == 0 {
			r.mu.Unlock()
			return
		}

		r.releaseUnrouted(ctx, n, log, ranID, causeTransportUnavailable)

		return
	}

	r.countInitialUE(initialUERelayed)
	log.InfoContext(ctx, "INITIAL UE MESSAGE relayed", slog.String("operator", op.name), slog.Any("amf", amf.client.addrs),
		slog.String("selected_by", rule), slog.Uint64("amf_ran_ue_ngap_id", uint64(u.amfRANID)))
}

func selectable(cands []amfCandidate) []amfCandidate {
	return slices.DeleteFunc(cands, func(c amfCandidate) bool { return c.assoc.lost })
}

func (r *relay) countInitialUE(result string) {
	r.metrics.InitialUE.WithLabelValues(interfaceNG, result).Inc()
}

// TS 38.413 clause 8.3.3
// TS 38.413 clause 8.7.4
func (r *relay) releaseUnrouted(ctx context.Context, n *RANAssociation, log *slog.Logger, ranID ngap.RANUENGAPID,
	cause ngap.Cause,
) {
	u := &ueContext{ran: n, ranID: ranID}

	if err := r.ues.add(u); err != nil {
		r.mu.Unlock()
		log.WarnContext(ctx, "failed to release the UE", slog.Any("error", err))

		return
	}

	out, err := (&ngap.UEContextReleaseCommand{
		UENGAPIDs: ngap.UENGAPIDs{AMFUENGAPID: u.ranAMFID, RANUENGAPID: ranID, Pair: true},
		Cause:     &cause,
	}).Marshal()
	if err != nil {
		r.ues.remove(u)
		r.mu.Unlock()
		log.ErrorContext(ctx, "failed to marshal UE Context Release Command", slog.Any("error", err))

		return
	}

	log = log.With(slog.Uint64("amf_ue_ngap_id", uint64(u.ranAMFID)))
	u.release = time.AfterFunc(r.g.cfg.ResponseTimeout, func() {
		r.mu.Lock()
		pending := r.ues.byRANAMFID[u.ranAMFID] == u
		r.mu.Unlock()

		if !pending {
			return
		}

		log.WarnContext(ctx, "no UE Context Release Complete from radio, UE-associated logical NG-connection reset")
		sendResets(ctx, n, log, sourceRadio, causeRadioUnspec,
			[]ngap.UEAssociatedLogicalNGConnectionItem{{AMFUENGAPID: ngap.Ptr(u.ranAMFID), RANUENGAPID: ngap.Ptr(ranID)}})

		r.mu.Lock()
		r.ues.remove(u)
		r.mu.Unlock()
	})
	r.mu.Unlock()

	if err := n.Send(ctx, out, ueStream); err != nil {
		r.mu.Lock()
		r.ues.remove(u)
		r.mu.Unlock()
		log.WarnContext(ctx, "failed to send UE Context Release Command", slog.Any("error", err))

		return
	}

	log.InfoContext(ctx, "UE Context Release Command sent to radio", slog.String("cause", cause.String()))
}

func (r *relay) rejectInitialUE(ctx context.Context, log *slog.Logger, result, reason string, err error) {
	r.countInitialUE(result)
	tracing.SpanFailed(ctx, result)
	log.WarnContext(ctx, reason, slog.Any("error", err))
}

// TS 38.413 clause 9.3.1.16
func (r *relay) updateLocation(u *ueContext, p *ngap.RawPDU) {
	uli, err := getIE[ngap.UserLocationInformation](p, ngap.IDUserLocationInformation)
	if err != nil || uli == nil || uli.Kind != ngap.UserLocationNR {
		return
	}

	if plmn, err := gw.DecodePLMN(uli.TAI.PLMNIdentity); err == nil {
		u.tai = &TAI{PLMN: plmn, TAC: uint32(uli.TAI.TAC)}
	}

	if plmn, err := gw.DecodePLMN(uli.PLMNIdentity); err == nil {
		u.nrcgi = &NRCGI{PLMN: plmn, CellID: uli.CellIdentity}
	}
}

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
func (r *relay) uplink(ctx context.Context, n *RANAssociation, log *slog.Logger, p *ngap.RawPDU) {
	proc := p.ProcedureCode()
	log = log.With(slog.String("procedure", proc.String()))

	ids, _, err := readUEIDs(p)
	if err != nil {
		tracing.SpanFailed(ctx, errTypeInvalid)
		log.WarnContext(ctx, "dropped NGAP message from radio with invalid UE NGAP IDs", slog.Any("error", err))

		return
	}

	last := p.Trigger() == ngap.TriggeringSuccessfulOutcome && proc == ngap.ProcUEContextRelease

	r.mu.Lock()

	u, cause := r.ranUE(n, ids)
	if u == nil {
		traced := r.tracedRANUELocked(n, ids)
		gone := r.ues.remove(r.ues.onRAN(n, ids.amf, ids.ran)...)
		r.mu.Unlock()

		var peer sender = n
		if traced != nil {
			peer = operatorRAN{n: n, operator: r.g.operatorName(traced.operatorID)}
		}

		tracing.SpanFailed(ctx, errTypeUnknown)
		log.WarnContext(ctx, "NGAP message from radio for an unknown UE", slog.String("cause", cause.String()),
			slog.Any("amf_ue_ngap_id", ids.amf), slog.Any("ran_ue_ngap_id", ids.ran))

		if !last {
			sendUEErrorIndication(ctx, peer, log, cause, ids.amf, ids.ran)
		}

		r.resetAMFs(ctx, gone, cause)

		return
	}

	if u.amf == nil {
		if last {
			r.ues.remove(u)
		}

		r.mu.Unlock()

		if last {
			log.InfoContext(ctx, "UE context released", slog.Uint64("amf_ue_ngap_id", uint64(u.ranAMFID)))
		} else {
			log.DebugContext(ctx, "dropped NGAP message from radio for a UE being released")
		}

		return
	}

	if proc == ngap.ProcUEContextReleaseRequest && p.Index(idGWContextReleaseIndication) >= 0 {
		r.mu.Unlock()
		log.InfoContext(ctx, "ignored UE Context Release Request with GW Context Release Indication: the UE is served by a radio")

		return
	}

	if !u.amfIDKnown {
		r.mu.Unlock()
		log.InfoContext(ctx, "dropped NGAP message from radio for a UE whose INITIAL UE MESSAGE was rerouted")

		return
	}

	r.updateLocation(u, p)

	out, err := rewrite(p, &u.amfID, &u.amfRANID)
	if err != nil {
		r.mu.Unlock()
		tracing.SpanFailed(ctx, errTypeError)
		log.WarnContext(ctx, "failed to relay NGAP message to AMF", slog.Any("error", err))

		return
	}

	if last {
		r.ues.remove(u)
	}

	amf := u.amf
	r.mu.Unlock()

	log.DebugContext(ctx, "relayed NGAP message to AMF", slog.Uint64("amf_ran_ue_ngap_id", uint64(u.amfRANID)))

	if err := amf.Send(ctx, out, ueStream); err != nil {
		log.WarnContext(ctx, "failed to send NGAP message to AMF", slog.Any("error", err))
	}
}

func (r *relay) ranUE(n *RANAssociation, ids ueIDs) (*ueContext, ngap.Cause) {
	if ids.amf == nil {
		return nil, causeUnknownLocalID
	}

	u := r.ues.byRANAMFID[*ids.amf]
	if u == nil || u.ran != n || (u.amf != nil && !u.told) {
		return nil, causeUnknownLocalID
	}

	if ids.ran == nil || *ids.ran != u.ranID {
		return nil, causeInconsistentRemoteID
	}

	return u, ngap.Cause{}
}

// TS 38.413 clause 8.3.1.2
// TS 38.413 clause 8.6.2.2
func rebinds(p *ngap.RawPDU) bool {
	return p.Trigger() == ngap.TriggeringInitiatingMessage &&
		(p.ProcedureCode() == ngap.ProcDownlinkNASTransport || p.ProcedureCode() == ngap.ProcInitialContextSetup)
}

// TS 38.300 clause 4.10.2.3
// TS 38.413 clause 10.6
func (r *relay) downlink(ctx context.Context, a *AMFAssociation, log *slog.Logger, p *ngap.RawPDU) {
	log = log.With(slog.String("procedure", p.ProcedureCode().String()))

	ids, pair, err := readUEIDs(p)
	if err != nil {
		tracing.SpanFailed(ctx, errTypeInvalid)
		log.WarnContext(ctx, "dropped NGAP message from AMF with invalid UE NGAP IDs", slog.Any("error", err))

		return
	}

	var newID *ngap.AMFUENGAPID

	if p.Index(ngap.IDNewAMFUENGAPID) >= 0 {
		id, _, err := p.NewAMFUENGAPID()
		if err != nil {
			tracing.SpanFailed(ctx, errTypeInvalid)
			log.WarnContext(ctx, "dropped NGAP message from AMF with an invalid New AMF UE NGAP ID", slog.Any("error", err))

			return
		}

		newID = &id
	}

	r.mu.Lock()

	u, others, cause := r.amfUE(a, ids, rebinds(p))
	if u == nil {
		gone := r.ues.remove(append(others, r.ues.onAMF(a, ids.amf, ids.ran)...)...)
		r.mu.Unlock()

		tracing.SpanFailed(ctx, errTypeUnknown)
		log.WarnContext(ctx, "NGAP message from AMF for an unknown UE", slog.String("cause", cause.String()),
			slog.Any("amf_ue_ngap_id", ids.amf), slog.Any("ran_ue_ngap_id", ids.ran))
		sendUEErrorIndication(ctx, a, log, cause, ids.amf, ids.ran)
		r.resetRANs(ctx, a.client.operator(), gone, cause)

		return
	}

	if u.amf != a {
		log.InfoContext(ctx, "UE-associated logical NG-connection moved to another AMF of the operator",
			slog.Any("from", u.amf.client.addrs))
		r.ues.rebind(u, a)
	}

	if !u.amfIDKnown || u.amfID != *ids.amf {
		r.ues.setAMFID(u, *ids.amf)
	}

	if newID != nil {
		r.ues.setAMFID(u, *newID)
		err = p.SetNewAMFUENGAPID(u.ranAMFID)
	}

	var out []byte

	switch {
	case err != nil:
	case pair != nil:
		pair.AMFUENGAPID, pair.RANUENGAPID = u.ranAMFID, u.ranID
		if err = p.SetUENGAPIDs(*pair); err == nil {
			out, err = p.Marshal()
		}
	default:
		out, err = rewrite(p, &u.ranAMFID, &u.ranID)
	}

	if err == nil {
		u.trace(p)
		u.told = true
	}

	ran := u.ran
	r.mu.Unlock()

	if err != nil {
		tracing.SpanFailed(ctx, errTypeError)
		log.WarnContext(ctx, "failed to relay NGAP message to radio", slog.Any("error", err))

		return
	}

	log.DebugContext(ctx, "relayed NGAP message to radio", slog.Uint64("amf_ue_ngap_id", uint64(u.ranAMFID)))

	if err := (operatorRAN{n: ran, operator: a.client.operator()}).Send(ctx, out, ueStream); err != nil {
		log.WarnContext(ctx, "failed to send NGAP message to radio", slog.Any("error", err))
	}
}

func (r *relay) amfUE(a *AMFAssociation, ids ueIDs, rebind bool) (*ueContext, []*ueContext, ngap.Cause) {
	if ids.ran == nil {
		if ids.amf != nil {
			if u := r.ues.byAMFID[amfUEKey{a, *ids.amf}]; u != nil {
				return u, nil, ngap.Cause{}
			}
		}

		return nil, nil, causeUnknownLocalID
	}

	u := r.ues.byAMFRANID[*ids.ran]
	if u == nil || u.amf == nil {
		return nil, nil, causeUnknownLocalID
	}

	if ids.amf == nil {
		return nil, nil, causeInconsistentRemoteID
	}

	if u.amf != a {
		if !rebind || u.operatorID != a.client.operatorID {
			return nil, nil, causeUnknownLocalID
		}

		if other := r.ues.byAMFID[amfUEKey{a, *ids.amf}]; other != nil {
			return nil, []*ueContext{u, other}, causeInconsistentRemoteID
		}

		return u, nil, ngap.Cause{}
	}

	if u.amfIDKnown {
		if u.amfID != *ids.amf {
			return nil, nil, causeInconsistentRemoteID
		}

		return u, nil, ngap.Cause{}
	}

	if other := r.ues.byAMFID[amfUEKey{a, *ids.amf}]; other != nil {
		return nil, []*ueContext{u, other}, causeInconsistentRemoteID
	}

	return u, nil, ngap.Cause{}
}

// TS 38.413 clause 8.6.5
// TS 38.300 clause 4.10.2.3
// TS 23.502 clause 4.2.2.2.3
func (r *relay) rerouteNASRequest(ctx context.Context, a *AMFAssociation, log *slog.Logger, value, msg []byte) {
	req, err := ngap.ParseRerouteNASRequest(value)
	if err != nil {
		tracing.SpanFailed(ctx, errTypeInvalid)
		log.WarnContext(ctx, "rejected Reroute NAS Request from AMF", slog.Any("error", err))

		if ase, ok := errors.AsType[*ngap.AbstractSyntaxError](err); ok {
			amfID, ranID := ase.UEIDs()
			sendUEErrorIndication(ctx, a, log, ase.Cause, amfID, ranID)
		}

		return
	}

	log = log.With(slog.Uint64("amf_ran_ue_ngap_id", uint64(req.RANUENGAPID)), slog.Uint64("amf_set_id", uint64(req.AMFSetID)))

	r.mu.Lock()

	u, _, cause := r.amfUE(a, ueIDs{amf: req.AMFUENGAPID, ran: &req.RANUENGAPID}, false)
	if req.AMFUENGAPID == nil {
		u = r.ues.byAMFRANID[req.RANUENGAPID]
		if u == nil || u.amf != a {
			u, cause = nil, causeUnknownLocalID
		}
	}

	if u == nil {
		gone := r.ues.remove(r.ues.onAMF(a, req.AMFUENGAPID, &req.RANUENGAPID)...)
		r.mu.Unlock()

		tracing.SpanFailed(ctx, errTypeUnknown)
		log.WarnContext(ctx, "Reroute NAS Request from AMF for an unknown UE", slog.String("cause", cause.String()))
		sendUEErrorIndication(ctx, a, log, cause, req.AMFUENGAPID, &req.RANUENGAPID)
		r.resetRANs(ctx, a.client.operator(), gone, cause)

		return
	}

	r.mu.Unlock()

	r.g.mu.Lock()
	cands := r.g.candidatesLocked(u.operatorID)
	r.g.mu.Unlock()

	target, _ := selectAMF(selectable(cands), ueIdentity{plmn: u.plmn, set: ngap.Ptr(uint16(req.AMFSetID))}, r.intN)

	out, err := reroutedInitialUEMessage(msg, req)

	r.mu.Lock()

	if r.ues.byAMFRANID[req.RANUENGAPID] != u {
		r.mu.Unlock()
		return
	}

	if target == nil || err != nil {
		gone := r.ues.remove(u)
		r.mu.Unlock()

		tracing.SpanFailed(ctx, initialUENoAMF)
		log.WarnContext(ctx, "INITIAL UE MESSAGE not rerouted: no selectable AMF in the AMF Set", slog.Any("error", err))
		r.resetRANs(ctx, a.client.operator(), gone, causeTransportUnavailable)

		return
	}

	r.ues.rebind(u, target)

	send := r.sendAMF
	r.mu.Unlock()

	if err := send(ctx, target, out, ueStream); err != nil {
		log.WarnContext(ctx, "failed to send the rerouted INITIAL UE MESSAGE to AMF", slog.Any("error", err))
		return
	}

	log.InfoContext(ctx, "INITIAL UE MESSAGE rerouted", slog.Any("amf", target.client.addrs))
}

// TS 38.413 clause 9.2.5.1
var initialUEMessageOrder = []ngap.ProtocolIEID{
	ngap.IDRANUENGAPID, ngap.IDNASPDU, ngap.IDUserLocationInformation, ngap.IDRRCEstablishmentCause, ngap.IDFiveGSTMSI,
	ngap.IDAMFSetID, ngap.IDUEContextRequest, ngap.IDAllowedNSSAI, ngap.IDSourceToTargetAMFInformationReroute, 174, 201, 224,
	225, 227, 245, 259, 333, 371, 402, ngap.IDPartiallyAllowedNSSAI, 427, 440, ngap.IDGUAMI, 176, 454,
}

const idGWContextReleaseIndication ngap.ProtocolIEID = 452

// TS 38.413 clause 8.6.5.2
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
		i := reroute.Index(id)
		if i < 0 {
			continue
		}

		ie, err := reroute.IE(i)
		if err != nil {
			return nil, err
		}

		if err := setIE(p, ie); err != nil {
			return nil, err
		}
	}

	return p.Marshal()
}

func setIE(p *ngap.RawPDU, ie ngap.RawIE) error {
	if i := p.Index(ie.ID); i >= 0 {
		return p.SetValue(i, ie.Value)
	}

	rank := slices.Index(initialUEMessageOrder, ie.ID)
	at := p.Len()

	if rank >= 0 {
		for i := range p.Len() {
			f, err := p.IE(i)
			if err != nil {
				return err
			}

			if r := slices.Index(initialUEMessageOrder, f.ID); r < 0 || r > rank {
				at = i
				break
			}
		}
	}

	return p.Insert(at, ie)
}

// TS 38.413 clause 8.7.5
// TS 38.413 clause 10.6
func (r *relay) errorIndicationFromRAN(ctx context.Context, n *RANAssociation, log *slog.Logger, value, msg []byte) {
	ind, err := ngap.ParseErrorIndication(value)
	if err != nil || ind.AMFUENGAPID == nil || ind.RANUENGAPID == nil {
		logErrorIndication(ctx, sourceRadio, log, value)
		return
	}

	r.mu.Lock()

	u, _ := r.ranUE(n, ueIDs{amf: ind.AMFUENGAPID, ran: ind.RANUENGAPID})
	if u == nil {
		r.mu.Unlock()
		logErrorIndication(ctx, sourceRadio, log, value)

		return
	}

	if u.amf == nil || !u.amfIDKnown {
		if releasesConnection(ind.Cause) {
			r.ues.remove(u)
		}

		r.mu.Unlock()
		logErrorIndication(ctx, sourceRadio, log, value)

		return
	}

	out, err := rewriteRaw(msg, &u.amfID, &u.amfRANID)
	if err == nil && releasesConnection(ind.Cause) {
		r.ues.remove(u)
	}

	amf := u.amf
	r.mu.Unlock()

	if err != nil {
		log.WarnContext(ctx, "failed to relay Error Indication to AMF", slog.Any("error", err))
		return
	}

	log.InfoContext(ctx, "relayed Error Indication to AMF", slog.String("cause", causeText(ind.Cause)))

	if err := amf.Send(ctx, out, ueStream); err != nil {
		log.WarnContext(ctx, "failed to send Error Indication to AMF", slog.Any("error", err))
	}
}

// TS 38.413 clause 8.7.5
// TS 38.413 clause 10.6
func (r *relay) errorIndicationFromAMF(ctx context.Context, a *AMFAssociation, log *slog.Logger, value, msg []byte) {
	ind, err := ngap.ParseErrorIndication(value)
	if err != nil || ind.AMFUENGAPID == nil || ind.RANUENGAPID == nil {
		logErrorIndication(ctx, sourceAMF, log, value)
		return
	}

	r.mu.Lock()

	u := r.ues.byAMFRANID[*ind.RANUENGAPID]
	if u == nil || u.amf != a || !u.amfIDKnown || u.amfID != *ind.AMFUENGAPID {
		r.mu.Unlock()
		logErrorIndication(ctx, sourceAMF, log, value)

		return
	}

	out, err := rewriteRaw(msg, &u.ranAMFID, &u.ranID)
	if err == nil && releasesConnection(ind.Cause) {
		r.ues.remove(u)
	}

	ran := u.ran
	r.mu.Unlock()

	if err != nil {
		log.WarnContext(ctx, "failed to relay Error Indication to radio", slog.Any("error", err))
		return
	}

	log.InfoContext(ctx, "relayed Error Indication to radio", slog.String("cause", causeText(ind.Cause)))

	if err := (operatorRAN{n: ran, operator: a.client.operator()}).Send(ctx, out, ueStream); err != nil {
		log.WarnContext(ctx, "failed to send Error Indication to radio", slog.Any("error", err))
	}
}

func rewriteRaw(msg []byte, amfID *ngap.AMFUENGAPID, ranID *ngap.RANUENGAPID) ([]byte, error) {
	p, err := ngap.ParseRawPDU(msg)
	if err != nil {
		return nil, err
	}

	return rewrite(p, amfID, ranID)
}

// TS 38.413 clause 10.6
func releasesConnection(c *ngap.Cause) bool {
	return c != nil && !c.Extended && c.Group == ngap.CauseGroupRadioNetwork &&
		(c.Value == causeUnknownLocalID.Value || c.Value == causeInconsistentRemoteID.Value)
}

// TS 38.413 clause 8.7.4.2.2
// TS 38.300 clause 4.10.2.3
func (r *relay) resetFromRAN(ctx context.Context, n *RANAssociation, log *slog.Logger, value []byte) {
	reset, ok := parseReset(ctx, n, sourceRadio, log, value)
	if !ok {
		return
	}

	r.mu.Lock()

	var gone []*ueContext

	if reset.ResetType.All {
		gone = r.ues.remove(r.ues.where(func(u *ueContext) bool { return u.ran == n })...)
	} else {
		for _, item := range reset.ResetType.Part {
			gone = append(gone, r.ues.remove(r.ues.onRAN(n, item.AMFUENGAPID, item.RANUENGAPID)...)...)
		}
	}

	r.mu.Unlock()

	acknowledgeReset(ctx, n, sourceRadio, log, reset)
	r.resetAMFs(ctx, gone, causeOr(reset.Cause))
}

// TS 38.413 clause 8.7.4.2.1
// TS 38.300 clause 4.10.2.3
func (r *relay) resetFromAMF(ctx context.Context, a *AMFAssociation, log *slog.Logger, value []byte) {
	reset, ok := parseReset(ctx, a, sourceAMF, log, value)
	if !ok {
		return
	}

	r.mu.Lock()

	var gone []*ueContext

	if reset.ResetType.All {
		gone = r.ues.remove(r.ues.where(func(u *ueContext) bool { return u.amf == a })...)
	} else {
		for _, item := range reset.ResetType.Part {
			gone = append(gone, r.ues.remove(r.ues.onAMF(a, item.AMFUENGAPID, item.RANUENGAPID)...)...)
		}
	}

	r.mu.Unlock()

	acknowledgeReset(ctx, a, sourceAMF, log, reset)
	r.resetRANs(ctx, a.client.operator(), gone, causeOr(reset.Cause))
}

func causeOr(c *ngap.Cause) ngap.Cause {
	if c == nil {
		return causeMiscUnspecified
	}

	return *c
}

// TS 38.413 clause 8.7.4.2.2
func (r *relay) resetAMFs(ctx context.Context, ues []*ueContext, cause ngap.Cause) {
	groups := map[*AMFAssociation][]ngap.UEAssociatedLogicalNGConnectionItem{}

	var order []*AMFAssociation

	for _, u := range ues {
		if u.amf == nil {
			continue
		}

		item := ngap.UEAssociatedLogicalNGConnectionItem{RANUENGAPID: ngap.Ptr(u.amfRANID)}
		if u.amfIDKnown {
			item.AMFUENGAPID = ngap.Ptr(u.amfID)
		}

		if _, ok := groups[u.amf]; !ok {
			order = append(order, u.amf)
		}

		groups[u.amf] = append(groups[u.amf], item)
	}

	for _, a := range order {
		sendResets(ctx, a, amfLog(r.log, a), sourceAMF, cause, groups[a])
	}
}

// TS 38.413 clause 8.7.4.2.1
func (r *relay) resetRANs(ctx context.Context, operator string, ues []*ueContext, cause ngap.Cause) {
	groups := map[*RANAssociation][]ngap.UEAssociatedLogicalNGConnectionItem{}

	var order []*RANAssociation

	for _, u := range ues {
		item := ngap.UEAssociatedLogicalNGConnectionItem{RANUENGAPID: ngap.Ptr(u.ranID)}
		if u.amfIDKnown {
			item.AMFUENGAPID = ngap.Ptr(u.ranAMFID)
		}

		if _, ok := groups[u.ran]; !ok {
			order = append(order, u.ran)
		}

		groups[u.ran] = append(groups[u.ran], item)
	}

	for _, n := range order {
		sendResets(ctx, operatorRAN{n: n, operator: operator}, ranLog(r.log, n), sourceRadio, cause, groups[n])
	}
}

func sendResets(ctx context.Context, peer sender, log *slog.Logger, to string, cause ngap.Cause,
	items []ngap.UEAssociatedLogicalNGConnectionItem,
) {
	for chunk := range slices.Chunk(items, maxnoofNGConnectionsToReset) {
		out, err := (&ngap.NGReset{Cause: &cause, ResetType: ngap.ResetType{Part: chunk}}).Marshal()
		if err != nil {
			log.ErrorContext(ctx, "failed to marshal NG Reset", slog.Any("error", err))
			return
		}

		if err := peer.Send(ctx, out, nonUEStream); err != nil {
			log.WarnContext(ctx, "failed to send NG Reset to "+to, slog.Any("error", err))
			return
		}

		log.InfoContext(ctx, "NG Reset sent to "+to, slog.String("cause", cause.String()), slog.Int("connections", len(chunk)))
	}
}

func (r *relay) snapshot() []UE {
	r.mu.Lock()
	ues := r.ues.where(func(u *ueContext) bool { return u.amf != nil })

	out := make([]UE, 0, len(ues))
	for _, u := range ues {
		out = append(out, u.snapshot())
	}

	r.mu.Unlock()

	return out
}

func (r *relay) countByOperator() map[int64]int {
	r.mu.Lock()
	defer r.mu.Unlock()

	counts := map[int64]int{}

	for _, u := range r.ues.byRANAMFID {
		if u.amf == nil {
			continue
		}

		counts[u.operatorID]++
	}

	return counts
}

// TS 38.300 clause 4.10.2.3
func terminate(ctx context.Context, from string, log *slog.Logger, pdu ngap.PDU) {
	log.InfoContext(ctx, "dropped NGAP message from "+from, slog.String("procedure", procedureOf(pdu).String()))
}

func parseReset(ctx context.Context, peer sender, from string, log *slog.Logger, value []byte) (*ngap.NGReset, bool) {
	reset, err := ngap.ParseNGReset(value)
	if err != nil {
		tracing.SpanFailed(ctx, errTypeInvalid)
		log.WarnContext(ctx, "rejected NG Reset from "+from, slog.Any("error", err))

		if ase, ok := errors.AsType[*ngap.AbstractSyntaxError](err); ok {
			sendErrorIndication(ctx, peer, log, ase.Cause, ase.ErrorIndicationDiagnostics())
		}

		return nil, false
	}

	return reset, true
}

// TS 38.413 clause 8.7.4
func acknowledgeReset(ctx context.Context, peer sender, from string, log *slog.Logger, reset *ngap.NGReset) {
	out, err := (&ngap.NGResetAcknowledge{
		ConnectionList:         reset.ResetType.Part,
		CriticalityDiagnostics: responseDiagnostics(ngap.ProcNGReset, reset.Diagnostics()),
	}).Marshal()
	if err != nil {
		log.ErrorContext(ctx, "failed to marshal NG Reset Acknowledge", slog.Any("error", err))
		return
	}

	if err := peer.Send(ctx, out, nonUEStream); err != nil {
		log.WarnContext(ctx, "failed to send NG Reset Acknowledge", slog.Any("error", err))
		return
	}

	log.InfoContext(ctx, "NG Reset from "+from+" acknowledged", slog.String("cause", causeText(reset.Cause)),
		slog.Bool("all", reset.ResetType.All), slog.Int("connections", len(reset.ResetType.Part)))
}

// TS 38.413 clause 10.3.4.2
func sendErrorIndication(ctx context.Context, peer sender, log *slog.Logger, cause ngap.Cause,
	diag ngap.CriticalityDiagnostics,
) {
	out, err := (&ngap.ErrorIndication{Cause: &cause, CriticalityDiagnostics: &diag}).Marshal()
	if err != nil {
		log.ErrorContext(ctx, "failed to marshal Error Indication", slog.Any("error", err))
		return
	}

	if err := peer.Send(ctx, out, nonUEStream); err != nil {
		log.WarnContext(ctx, "failed to send Error Indication", slog.Any("error", err))
	}
}

// TS 38.413 clause 8.7.5.2
func sendUEErrorIndication(ctx context.Context, peer sender, log *slog.Logger, cause ngap.Cause,
	amfID *ngap.AMFUENGAPID, ranID *ngap.RANUENGAPID,
) {
	out, err := (&ngap.ErrorIndication{AMFUENGAPID: amfID, RANUENGAPID: ranID, Cause: &cause}).Marshal()
	if err != nil {
		log.ErrorContext(ctx, "failed to marshal Error Indication", slog.Any("error", err))
		return
	}

	if err := peer.Send(ctx, out, ueStream); err != nil {
		log.WarnContext(ctx, "failed to send Error Indication", slog.Any("error", err))
	}
}

// TS 38.413 clause 8.7.5
func logErrorIndication(ctx context.Context, from string, log *slog.Logger, value []byte) {
	ind, err := ngap.ParseErrorIndication(value)
	if err != nil {
		log.WarnContext(ctx, "undecodable Error Indication from "+from, slog.Any("error", err))
		return
	}

	log.WarnContext(ctx, "Error Indication from "+from, slog.String("cause", causeText(ind.Cause)),
		slog.Bool("criticality_diagnostics", ind.CriticalityDiagnostics != nil),
		slog.Bool("ue_associated", ind.AMFUENGAPID != nil || ind.RANUENGAPID != nil))
}

func procedureOf(pdu ngap.PDU) ngap.ProcedureCode {
	switch p := pdu.(type) {
	case *ngap.InitiatingMessage:
		return p.ProcedureCode
	case *ngap.SuccessfulOutcome:
		return p.ProcedureCode
	case *ngap.UnsuccessfulOutcome:
		return p.ProcedureCode
	}

	return 0
}

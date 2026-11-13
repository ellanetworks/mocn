package s1

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"slices"
	"sync"
	"time"

	"github.com/ellanetworks/core/per"
	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type Relay interface {
	FromRAN(ctx context.Context, from *RANAssociation, pdu s1ap.PDU, msg []byte)
	FromMME(ctx context.Context, from *MMEAssociation, pdu s1ap.PDU, msg []byte)
	RANLost(ctx context.Context, from *RANAssociation, cause s1ap.Cause)
	MMELost(ctx context.Context, from *MMEAssociation)
}

type sender interface {
	Send(ctx context.Context, msg []byte, stream uint16) error
}

const (
	ueStream                              = 1
	maxnoofIndividualS1ConnectionsToReset = 256
	idGWContextReleaseIndication          = s1ap.ProtocolIEID(164)
	initialUERelayed                      = "relayed"
	initialUEUnknownPLMN                  = "unknown_plmn"
	initialUENoMME                        = "no_mme"
	initialUERejected                     = "rejected"
	initialUEInvalid                      = "invalid"
	initialUEError                        = "error"
	sourceRadio                           = "radio"
	sourceMME                             = "MME"
)

var (
	causeUnknownMMEUEID = s1ap.Cause{Group: s1ap.CauseGroupRadioNetwork, Value: s1ap.CauseRadioNetworkUnknownMMEUES1APID}
	causeUnknownENBUEID = s1ap.Cause{Group: s1ap.CauseGroupRadioNetwork, Value: s1ap.CauseRadioNetworkUnknownENBUES1APID}
	causeUnknownPair    = s1ap.Cause{Group: s1ap.CauseGroupRadioNetwork, Value: s1ap.CauseRadioNetworkUnknownPairUES1APID}
	causeEUTRANRelease  = s1ap.Cause{Group: s1ap.CauseGroupRadioNetwork, Value: s1ap.CauseRadioNetworkReleaseDueToEUTRANGeneratedReason}
	causeRadioUnspec    = s1ap.Cause{Group: s1ap.CauseGroupRadioNetwork, Value: s1ap.CauseRadioNetworkUnspecified}
)

var initialUEResults = []string{
	initialUERelayed, initialUEUnknownPLMN, initialUENoMME, initialUERejected, initialUEInvalid, initialUEError,
}

type relay struct {
	g         *Gateway
	log       *slog.Logger
	intN      func(int) int
	initialUE *prometheus.CounterVec
	paged     *prometheus.CounterVec

	mu      sync.Mutex
	ues     ueTable
	sendMME func(context.Context, *MMEAssociation, []byte, uint16) error
}

func newRelay(g *Gateway, intN func(int) int) *relay {
	if intN == nil {
		intN = rand.IntN
	}

	r := &relay{
		g: g, log: g.log, intN: intN, ues: newUETable(), sendMME: sendMME,
		initialUE: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ellamocn_initial_ue_messages_total",
			Help: "INITIAL UE MESSAGEs received from radios, by interface (s1) and result " +
				"(relayed: sent to an MME; unknown_plmn: no operator serves the PLMN; " +
				"no_mme: no selectable MME of the operator; " +
				"rejected: reuses an allocated eNB UE S1AP ID, an Error Indication is sent; " +
				"invalid: undecodable or missing a mandatory IE; error: internal failure or failed send to the MME).",
		}, []string{"interface", "result"}),
	}

	for _, res := range initialUEResults {
		r.initialUE.WithLabelValues(interfaceS1, res)
	}

	r.paged = newPagingCounter()

	return r
}

func sendMME(ctx context.Context, a *MMEAssociation, msg []byte, stream uint16) error {
	return a.Send(ctx, msg, stream)
}

func ranLog(log *slog.Logger, n *RANAssociation) *slog.Logger {
	return log.With(slog.Any("tnl_addresses", n.addrs))
}

func mmeLog(log *slog.Logger, a *MMEAssociation) *slog.Logger {
	return log.With(slog.Int64("operator_id", a.client.operatorID), slog.Any("mme", a.client.addrs))
}

func (r *relay) FromRAN(ctx context.Context, from *RANAssociation, pdu s1ap.PDU, msg []byte) {
	log := ranLog(r.log, from)

	if m, ok := pdu.(*s1ap.InitiatingMessage); ok {
		switch m.ProcedureCode {
		case s1ap.ProcInitialUEMessage:
			r.initialUEMessage(ctx, from, log, msg)
			return
		case s1ap.ProcReset:
			r.resetFromRAN(ctx, from, log, m.Value)
			return
		case s1ap.ProcErrorIndication:
			r.errorIndicationFromRAN(ctx, from, log, m.Value, msg)
			return
		case s1ap.ProcPathSwitchRequest:
			terminate(ctx, sourceRadio, log, pdu)
			return
		}
	}

	if m, ok := pdu.(*s1ap.SuccessfulOutcome); ok && m.ProcedureCode == s1ap.ProcReset {
		log.DebugContext(ctx, "Reset Acknowledge from radio")
		return
	}

	p, ok := uePDU(ctx, log, sourceRadio, msg)
	if !ok {
		terminate(ctx, sourceRadio, log, pdu)
		return
	}

	r.uplink(ctx, from, log, p)
}

func (r *relay) FromMME(ctx context.Context, from *MMEAssociation, pdu s1ap.PDU, msg []byte) {
	log := mmeLog(r.log, from)

	if m, ok := pdu.(*s1ap.InitiatingMessage); ok {
		switch m.ProcedureCode {
		case s1ap.ProcReset:
			r.resetFromMME(ctx, from, log, m.Value)
			return
		case s1ap.ProcErrorIndication:
			r.errorIndicationFromMME(ctx, from, log, m.Value, msg)
			return
		case s1ap.ProcHandoverResourceAllocation:
			terminate(ctx, sourceMME, log, pdu)
			return
		case s1ap.ProcPaging:
			r.paging(ctx, from, log, m.Value, msg)
			return
		case s1ap.ProcOverloadStart:
			r.g.overloadStart(ctx, from, log, m.Value)
			return
		case s1ap.ProcOverloadStop:
			r.g.overloadStop(ctx, from, log, m.Value)
			return
		}
	}

	if m, ok := pdu.(*s1ap.SuccessfulOutcome); ok && m.ProcedureCode == s1ap.ProcReset {
		log.DebugContext(ctx, "Reset Acknowledge from MME")
		return
	}

	p, ok := uePDU(ctx, log, sourceMME, msg)
	if !ok {
		terminate(ctx, sourceMME, log, pdu)
		return
	}

	r.downlink(ctx, from, log, p)
}

func uePDU(ctx context.Context, log *slog.Logger, from string, msg []byte) (*s1ap.RawPDU, bool) {
	p, err := s1ap.ParseRawPDU(msg)
	if err != nil {
		spanFailed(ctx, errTypeInvalid)
		log.WarnContext(ctx, "undecodable S1AP message from "+from, slog.Any("error", err))

		return nil, false
	}

	if p.Index(s1ap.IDMMEUES1APID) < 0 && p.Index(s1ap.IDENBUES1APID) < 0 && p.Index(s1ap.IDUES1APIDs) < 0 {
		return nil, false
	}

	return p, true
}

func (r *relay) RANLost(ctx context.Context, from *RANAssociation, cause s1ap.Cause) {
	r.mu.Lock()
	gone := r.ues.remove(r.ues.where(func(u *ueContext) bool { return u.ran == from })...)
	r.mu.Unlock()

	if len(gone) > 0 {
		ranLog(r.log, from).InfoContext(ctx, "radio UE contexts released", slog.Int("ues", len(gone)),
			slog.String("cause", cause.String()))
	}

	r.resetMMEs(ctx, gone, cause)
}

func (r *relay) MMELost(ctx context.Context, from *MMEAssociation) {
	r.mu.Lock()
	from.lost = true
	gone := r.ues.remove(r.ues.where(func(u *ueContext) bool { return u.mme == from })...)
	r.mu.Unlock()

	if len(gone) > 0 {
		mmeLog(r.log, from).InfoContext(ctx, "MME lost, UE contexts released", slog.Int("ues", len(gone)))
	}

	r.resetRANs(ctx, from.client.operator(), gone, causeTransportUnavailable)
}

func getIE[T any, P interface {
	*T
	per.Unmarshaler
}](p *s1ap.RawPDU, id s1ap.ProtocolIEID,
) (*T, error) {
	var v T

	ok, err := p.Get(id, P(&v))
	if err != nil || !ok {
		return nil, err
	}

	return &v, nil
}

// TS 36.413 clause 8.6.2.1
func (r *relay) initialUEMessage(ctx context.Context, n *RANAssociation, log *slog.Logger, msg []byte) {
	p, err := s1ap.ParseRawPDU(msg)
	if err != nil {
		r.rejectInitialUE(ctx, log, initialUEInvalid, "undecodable INITIAL UE MESSAGE", err)
		return
	}

	enbID, hasENBID, err := p.ENBUES1APID()
	if err != nil || !hasENBID {
		r.rejectInitialUE(ctx, log, initialUEInvalid, "INITIAL UE MESSAGE without a valid eNB UE S1AP ID", err)
		return
	}

	tai, err := getIE[s1ap.TAI](p, s1ap.IDTAI)
	if err != nil || tai == nil {
		r.rejectInitialUE(ctx, log, initialUEInvalid, "INITIAL UE MESSAGE without a valid TAI", err)
		return
	}

	plmn, err := decodePLMN(tai.PLMNIdentity)
	if err != nil {
		r.rejectInitialUE(ctx, log, initialUEInvalid, "INITIAL UE MESSAGE with an invalid PLMN", err)
		return
	}

	id := ueIdentity{plmn: plmn}

	if id.gummei, err = getIE[s1ap.GUMMEI](p, s1ap.IDGUMMEI); err != nil {
		log.WarnContext(ctx, "ignored invalid GUMMEI in INITIAL UE MESSAGE", slog.Any("error", err))
	}

	if id.stmsi, err = getIE[s1ap.STMSI](p, s1ap.IDSTMSI); err != nil {
		log.WarnContext(ctx, "ignored invalid S-TMSI in INITIAL UE MESSAGE", slog.Any("error", err))
	}

	r.g.mu.Lock()
	op, cands, found := r.g.selectionLocked(plmn)

	var radio db.GlobalENBID
	if n.setup != nil {
		radio = n.setup.globalENBID
	}

	r.g.mu.Unlock()

	log = log.With(slog.Uint64("enb_ue_s1ap_id", uint64(enbID)), slog.String("plmn", plmn.String()))

	r.mu.Lock()

	if old := r.ues.byRANENBID[ranUEKey{n, enbID}]; old != nil {
		gone := r.ues.remove(old)
		r.mu.Unlock()

		r.initialUE.WithLabelValues(interfaceS1, initialUERejected).Inc()
		spanFailed(ctx, initialUERejected)
		log.WarnContext(ctx, "INITIAL UE MESSAGE reuses an allocated eNB UE S1AP ID")
		sendUEErrorIndication(ctx, n, log, causeUnknownENBUEID, nil, &enbID)
		r.resetMMEs(ctx, gone, causeUnknownENBUEID)

		return
	}

	if !found {
		r.initialUE.WithLabelValues(interfaceS1, initialUEUnknownPLMN).Inc()
		spanFailed(ctx, initialUEUnknownPLMN)
		log.WarnContext(ctx, "INITIAL UE MESSAGE not routed: no operator serves the PLMN")
		r.releaseUnrouted(ctx, n, log, enbID, causeUnknownPLMN)

		return
	}

	trace.SpanFromContext(ctx).SetAttributes(attribute.String(attrOperator, op.name))

	mme, rule := selectMME(slices.DeleteFunc(cands, func(c mmeCandidate) bool { return c.assoc.lost }), id, r.intN)
	if mme == nil {
		r.initialUE.WithLabelValues(interfaceS1, initialUENoMME).Inc()
		spanFailed(ctx, initialUENoMME)
		log.WarnContext(ctx, "INITIAL UE MESSAGE not routed: no selectable MME of the operator", slog.String("operator", op.name))
		r.releaseUnrouted(ctx, n, log, enbID, causeTransportUnavailable)

		return
	}

	u := &ueContext{
		ran: n, enbID: enbID, mme: mme, operatorID: op.id, plmn: plmn, radio: radio, since: r.g.cfg.Now(),
		tai: &TAI{PLMN: plmn, TAC: uint16(tai.TAC)},
	}

	r.updateLocation(u, p)

	if err := r.ues.add(u); err != nil {
		r.mu.Unlock()
		r.rejectInitialUE(ctx, log, initialUEError, "dropped INITIAL UE MESSAGE", err)

		return
	}

	out, err := rewrite(p, nil, &u.mmeENBID)
	if err != nil {
		r.ues.remove(u)
		r.mu.Unlock()
		r.rejectInitialUE(ctx, log, initialUEError, "failed to relay INITIAL UE MESSAGE", err)

		return
	}

	send := r.sendMME
	r.mu.Unlock()

	if err := send(ctx, mme, out, ueStream); err != nil {
		r.initialUE.WithLabelValues(interfaceS1, initialUEError).Inc()
		spanFailed(ctx, initialUEError)
		log.WarnContext(ctx, "failed to send INITIAL UE MESSAGE to MME", slog.Any("error", err))

		r.mu.Lock()

		if len(r.ues.remove(u)) == 0 {
			r.mu.Unlock()
			return
		}

		r.releaseUnrouted(ctx, n, log, enbID, causeTransportUnavailable)

		return
	}

	r.initialUE.WithLabelValues(interfaceS1, initialUERelayed).Inc()
	log.InfoContext(ctx, "INITIAL UE MESSAGE relayed", slog.String("operator", op.name), slog.Any("mme", mme.client.addrs),
		slog.String("selected_by", rule), slog.Uint64("mme_enb_ue_s1ap_id", uint64(u.mmeENBID)))
}

// TS 36.413 clause 8.3.3
// TS 36.413 clause 8.7.1
func (r *relay) releaseUnrouted(ctx context.Context, n *RANAssociation, log *slog.Logger, enbID s1ap.ENBUES1APID,
	cause s1ap.Cause,
) {
	u := &ueContext{ran: n, enbID: enbID}

	if err := r.ues.add(u); err != nil {
		r.mu.Unlock()
		log.WarnContext(ctx, "failed to release the UE", slog.Any("error", err))

		return
	}

	out, err := (&s1ap.UEContextReleaseCommand{
		UES1APIDs: s1ap.UES1APIDs{MMEUES1APID: u.ranMMEID, ENBUES1APID: enbID, Pair: true},
		Cause:     &cause,
	}).Marshal()
	if err != nil {
		r.ues.remove(u)
		r.mu.Unlock()
		log.ErrorContext(ctx, "failed to marshal UE Context Release Command", slog.Any("error", err))

		return
	}

	log = log.With(slog.Uint64("mme_ue_s1ap_id", uint64(u.ranMMEID)))
	u.release = time.AfterFunc(r.g.cfg.ResponseTimeout, func() {
		r.mu.Lock()
		pending := r.ues.byRANMMEID[u.ranMMEID] == u
		r.mu.Unlock()

		if !pending {
			return
		}

		log.WarnContext(ctx, "no UE Context Release Complete from radio, UE-associated logical S1-connection reset")
		sendResets(ctx, n, log, sourceRadio, causeRadioUnspec,
			[]s1ap.UEAssociatedLogicalS1ConnectionItem{{MMEUES1APID: s1ap.Ptr(u.ranMMEID), ENBUES1APID: s1ap.Ptr(enbID)}})

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
	r.initialUE.WithLabelValues(interfaceS1, result).Inc()
	spanFailed(ctx, result)
	log.WarnContext(ctx, reason, slog.Any("error", err))
}

func (r *relay) updateLocation(u *ueContext, p *s1ap.RawPDU) {
	if tai, err := getIE[s1ap.TAI](p, s1ap.IDTAI); err == nil && tai != nil {
		if plmn, err := decodePLMN(tai.PLMNIdentity); err == nil {
			u.tai = &TAI{PLMN: plmn, TAC: uint16(tai.TAC)}
		}
	}

	if cgi, err := getIE[s1ap.EUTRANCGI](p, s1ap.IDEUTRANCGI); err == nil && cgi != nil {
		if plmn, err := decodePLMN(cgi.PLMNIdentity); err == nil {
			u.ecgi = &ECGI{PLMN: plmn, CellID: cgi.CellID}
		}
	}
}

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

type ueIDs struct {
	mme *s1ap.MMEUES1APID
	enb *s1ap.ENBUES1APID
}

func readUEIDs(p *s1ap.RawPDU) (ueIDs, *s1ap.UES1APIDs, error) {
	if p.Index(s1ap.IDUES1APIDs) >= 0 {
		v, _, err := p.UES1APIDs()
		if err != nil {
			return ueIDs{}, nil, err
		}

		ids := ueIDs{mme: &v.MMEUES1APID}
		if v.Pair {
			ids.enb = &v.ENBUES1APID
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
func (r *relay) uplink(ctx context.Context, n *RANAssociation, log *slog.Logger, p *s1ap.RawPDU) {
	proc := p.ProcedureCode()
	log = log.With(slog.String("procedure", proc.String()))

	ids, _, err := readUEIDs(p)
	if err != nil {
		spanFailed(ctx, errTypeInvalid)
		log.WarnContext(ctx, "dropped S1AP message from radio with invalid UE S1AP IDs", slog.Any("error", err))

		return
	}

	last := p.Trigger() == s1ap.TriggeringSuccessfulOutcome && proc == s1ap.ProcUEContextRelease

	r.mu.Lock()

	u, cause := r.ranUE(n, ids)
	if u == nil {
		traced := r.tracedRANUELocked(n, ids)
		gone := r.ues.remove(r.ues.onRAN(n, ids.mme, ids.enb)...)
		r.mu.Unlock()

		var peer sender = n
		if traced != nil {
			peer = operatorRAN{n: n, operator: r.g.operatorName(traced.operatorID)}
		}

		spanFailed(ctx, errTypeUnknown)
		log.WarnContext(ctx, "S1AP message from radio for an unknown UE", slog.String("cause", cause.String()),
			slog.Any("mme_ue_s1ap_id", ids.mme), slog.Any("enb_ue_s1ap_id", ids.enb))

		if !last {
			sendUEErrorIndication(ctx, peer, log, cause, ids.mme, ids.enb)
		}

		r.resetMMEs(ctx, gone, cause)

		return
	}

	if u.mme == nil {
		if last {
			r.ues.remove(u)
		}

		r.mu.Unlock()

		if last {
			log.InfoContext(ctx, "UE context released", slog.Uint64("mme_ue_s1ap_id", uint64(u.ranMMEID)))
		} else {
			log.DebugContext(ctx, "dropped S1AP message from radio for a UE being released")
		}

		return
	}

	if proc == s1ap.ProcUEContextReleaseRequest && p.Index(idGWContextReleaseIndication) >= 0 {
		r.mu.Unlock()
		log.InfoContext(ctx, "ignored UE Context Release Request with GW Context Release Indication: the UE is served by a radio")

		return
	}

	r.updateLocation(u, p)

	out, err := rewrite(p, &u.mmeID, &u.mmeENBID)
	if err != nil {
		r.mu.Unlock()
		spanFailed(ctx, errTypeError)
		log.WarnContext(ctx, "failed to relay S1AP message to MME", slog.Any("error", err))

		return
	}

	if last {
		r.ues.remove(u)
	}

	mme := u.mme
	r.mu.Unlock()

	log.DebugContext(ctx, "relayed S1AP message to MME", slog.Uint64("mme_enb_ue_s1ap_id", uint64(u.mmeENBID)))

	if err := mme.Send(ctx, out, ueStream); err != nil {
		log.WarnContext(ctx, "failed to send S1AP message to MME", slog.Any("error", err))
	}
}

func (r *relay) ranUE(n *RANAssociation, ids ueIDs) (*ueContext, s1ap.Cause) {
	if ids.mme == nil {
		return nil, causeUnknownMMEUEID
	}

	u := r.ues.byRANMMEID[*ids.mme]
	if u == nil || u.ran != n || (u.mme != nil && !u.mmeIDKnown) {
		return nil, causeUnknownMMEUEID
	}

	if ids.enb == nil || *ids.enb != u.enbID {
		return nil, causeUnknownPair
	}

	return u, s1ap.Cause{}
}

// TS 36.300 clause 4.6.2
// TS 36.413 clause 10.6
func (r *relay) downlink(ctx context.Context, a *MMEAssociation, log *slog.Logger, p *s1ap.RawPDU) {
	log = log.With(slog.String("procedure", p.ProcedureCode().String()))

	ids, pair, err := readUEIDs(p)
	if err != nil {
		spanFailed(ctx, errTypeInvalid)
		log.WarnContext(ctx, "dropped S1AP message from MME with invalid UE S1AP IDs", slog.Any("error", err))

		return
	}

	r.mu.Lock()

	u, others, cause := r.mmeUE(a, ids)
	if u == nil {
		gone := r.ues.remove(append(others, r.ues.onMME(a, ids.mme, ids.enb)...)...)
		r.mu.Unlock()

		spanFailed(ctx, errTypeUnknown)
		log.WarnContext(ctx, "S1AP message from MME for an unknown UE", slog.String("cause", cause.String()),
			slog.Any("mme_ue_s1ap_id", ids.mme), slog.Any("enb_ue_s1ap_id", ids.enb))
		sendUEErrorIndication(ctx, a, log, cause, ids.mme, ids.enb)
		r.resetRANs(ctx, a.client.operator(), gone, cause)

		return
	}

	if !u.mmeIDKnown {
		r.ues.setMMEID(u, *ids.mme)
	}

	var out []byte

	if pair != nil {
		pair.MMEUES1APID, pair.ENBUES1APID = u.ranMMEID, u.enbID
		if err = p.SetUES1APIDs(*pair); err == nil {
			out, err = p.Marshal()
		}
	} else {
		out, err = rewrite(p, &u.ranMMEID, &u.enbID)
	}

	if err == nil {
		u.trace(p)
	}

	ran := u.ran
	r.mu.Unlock()

	if err != nil {
		spanFailed(ctx, errTypeError)
		log.WarnContext(ctx, "failed to relay S1AP message to radio", slog.Any("error", err))

		return
	}

	log.DebugContext(ctx, "relayed S1AP message to radio", slog.Uint64("mme_ue_s1ap_id", uint64(u.ranMMEID)))

	if err := (operatorRAN{n: ran, operator: a.client.operator()}).Send(ctx, out, ueStream); err != nil {
		log.WarnContext(ctx, "failed to send S1AP message to radio", slog.Any("error", err))
	}
}

func (r *relay) mmeUE(a *MMEAssociation, ids ueIDs) (*ueContext, []*ueContext, s1ap.Cause) {
	if ids.enb == nil {
		if ids.mme != nil {
			if u := r.ues.byMMEID[mmeUEKey{a, *ids.mme}]; u != nil {
				return u, nil, s1ap.Cause{}
			}

			return nil, nil, causeUnknownMMEUEID
		}

		return nil, nil, causeUnknownENBUEID
	}

	u := r.ues.byMMEENBID[*ids.enb]
	if u == nil || u.mme != a {
		return nil, nil, causeUnknownENBUEID
	}

	if ids.mme == nil {
		return nil, nil, causeUnknownPair
	}

	if u.mmeIDKnown {
		if u.mmeID != *ids.mme {
			return nil, nil, causeUnknownPair
		}

		return u, nil, s1ap.Cause{}
	}

	if other := r.ues.byMMEID[mmeUEKey{a, *ids.mme}]; other != nil {
		return nil, []*ueContext{u, other}, causeUnknownMMEUEID
	}

	return u, nil, s1ap.Cause{}
}

// TS 36.413 clause 8.7.2
// TS 36.413 clause 10.6
func (r *relay) errorIndicationFromRAN(ctx context.Context, n *RANAssociation, log *slog.Logger, value, msg []byte) {
	ind, err := s1ap.ParseErrorIndication(value)
	if err != nil || ind.MMEUES1APID == nil || ind.ENBUES1APID == nil {
		logErrorIndication(ctx, sourceRadio, log, value)
		return
	}

	r.mu.Lock()

	u, _ := r.ranUE(n, ueIDs{mme: ind.MMEUES1APID, enb: ind.ENBUES1APID})
	if u == nil {
		r.mu.Unlock()
		logErrorIndication(ctx, sourceRadio, log, value)

		return
	}

	if u.mme == nil {
		if releasesConnection(ind.Cause) {
			r.ues.remove(u)
		}

		r.mu.Unlock()
		logErrorIndication(ctx, sourceRadio, log, value)

		return
	}

	out, err := rewriteRaw(msg, &u.mmeID, &u.mmeENBID)
	if err == nil && releasesConnection(ind.Cause) {
		r.ues.remove(u)
	}

	mme := u.mme
	r.mu.Unlock()

	if err != nil {
		log.WarnContext(ctx, "failed to relay Error Indication to MME", slog.Any("error", err))
		return
	}

	log.InfoContext(ctx, "relayed Error Indication to MME", slog.String("cause", causeText(ind.Cause)))

	if err := mme.Send(ctx, out, ueStream); err != nil {
		log.WarnContext(ctx, "failed to send Error Indication to MME", slog.Any("error", err))
	}
}

// TS 36.413 clause 8.7.2
// TS 36.413 clause 10.6
func (r *relay) errorIndicationFromMME(ctx context.Context, a *MMEAssociation, log *slog.Logger, value, msg []byte) {
	ind, err := s1ap.ParseErrorIndication(value)
	if err != nil || ind.MMEUES1APID == nil || ind.ENBUES1APID == nil {
		logErrorIndication(ctx, sourceMME, log, value)
		return
	}

	r.mu.Lock()

	u := r.ues.byMMEENBID[*ind.ENBUES1APID]
	if u == nil || u.mme != a || !u.mmeIDKnown || u.mmeID != *ind.MMEUES1APID {
		r.mu.Unlock()
		logErrorIndication(ctx, sourceMME, log, value)

		return
	}

	out, err := rewriteRaw(msg, &u.ranMMEID, &u.enbID)
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

func rewriteRaw(msg []byte, mmeID *s1ap.MMEUES1APID, enbID *s1ap.ENBUES1APID) ([]byte, error) {
	p, err := s1ap.ParseRawPDU(msg)
	if err != nil {
		return nil, err
	}

	return rewrite(p, mmeID, enbID)
}

// TS 36.413 clause 10.6
func releasesConnection(c *s1ap.Cause) bool {
	return c != nil && !c.Extended && c.Group == s1ap.CauseGroupRadioNetwork &&
		(c.Value == causeUnknownMMEUEID.Value || c.Value == causeUnknownENBUEID.Value || c.Value == causeUnknownPair.Value)
}

// TS 36.413 clause 8.7.1.2.2
// TS 36.300 clause 4.6.2
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
			gone = append(gone, r.ues.remove(r.ues.onRAN(n, item.MMEUES1APID, item.ENBUES1APID)...)...)
		}
	}

	r.mu.Unlock()

	acknowledgeReset(ctx, n, sourceRadio, log, reset)
	r.resetMMEs(ctx, gone, causeOr(reset.Cause))
}

// TS 36.413 clause 8.7.1.2.1
// TS 36.300 clause 4.6.2
func (r *relay) resetFromMME(ctx context.Context, a *MMEAssociation, log *slog.Logger, value []byte) {
	reset, ok := parseReset(ctx, a, sourceMME, log, value)
	if !ok {
		return
	}

	r.mu.Lock()

	var gone []*ueContext

	if reset.ResetType.All {
		gone = r.ues.remove(r.ues.where(func(u *ueContext) bool { return u.mme == a })...)
	} else {
		for _, item := range reset.ResetType.Part {
			gone = append(gone, r.ues.remove(r.ues.onMME(a, item.MMEUES1APID, item.ENBUES1APID)...)...)
		}
	}

	r.mu.Unlock()

	acknowledgeReset(ctx, a, sourceMME, log, reset)
	r.resetRANs(ctx, a.client.operator(), gone, causeOr(reset.Cause))
}

func causeOr(c *s1ap.Cause) s1ap.Cause {
	if c == nil {
		return causeMiscUnspecified
	}

	return *c
}

// TS 36.413 clause 8.7.1.2.2
func (r *relay) resetMMEs(ctx context.Context, ues []*ueContext, cause s1ap.Cause) {
	groups := map[*MMEAssociation][]s1ap.UEAssociatedLogicalS1ConnectionItem{}

	var order []*MMEAssociation

	for _, u := range ues {
		if u.mme == nil {
			continue
		}

		item := s1ap.UEAssociatedLogicalS1ConnectionItem{ENBUES1APID: s1ap.Ptr(u.mmeENBID)}
		if u.mmeIDKnown {
			item.MMEUES1APID = s1ap.Ptr(u.mmeID)
		}

		if _, ok := groups[u.mme]; !ok {
			order = append(order, u.mme)
		}

		groups[u.mme] = append(groups[u.mme], item)
	}

	for _, a := range order {
		sendResets(ctx, a, mmeLog(r.log, a), sourceMME, cause, groups[a])
	}
}

// TS 36.413 clause 8.7.1.2.1
func (r *relay) resetRANs(ctx context.Context, operator string, ues []*ueContext, cause s1ap.Cause) {
	groups := map[*RANAssociation][]s1ap.UEAssociatedLogicalS1ConnectionItem{}

	var order []*RANAssociation

	for _, u := range ues {
		item := s1ap.UEAssociatedLogicalS1ConnectionItem{ENBUES1APID: s1ap.Ptr(u.enbID)}
		if u.mmeIDKnown {
			item.MMEUES1APID = s1ap.Ptr(u.ranMMEID)
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

func sendResets(ctx context.Context, peer sender, log *slog.Logger, to string, cause s1ap.Cause,
	items []s1ap.UEAssociatedLogicalS1ConnectionItem,
) {
	for chunk := range slices.Chunk(items, maxnoofIndividualS1ConnectionsToReset) {
		out, err := (&s1ap.Reset{Cause: &cause, ResetType: s1ap.ResetType{Part: chunk}}).Marshal()
		if err != nil {
			log.ErrorContext(ctx, "failed to marshal Reset", slog.Any("error", err))
			return
		}

		if err := peer.Send(ctx, out, nonUEStream); err != nil {
			log.WarnContext(ctx, "failed to send Reset to "+to, slog.Any("error", err))
			return
		}

		log.InfoContext(ctx, "Reset sent to "+to, slog.String("cause", cause.String()), slog.Int("connections", len(chunk)))
	}
}

func (r *relay) snapshot() []UE {
	r.mu.Lock()
	ues := r.ues.where(func(u *ueContext) bool { return u.mme != nil })

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

	for _, u := range r.ues.byRANMMEID {
		if u.mme == nil {
			continue
		}

		counts[u.operatorID]++
	}

	return counts
}

// TS 36.300 clause 4.6.2
func terminate(ctx context.Context, from string, log *slog.Logger, pdu s1ap.PDU) {
	log.InfoContext(ctx, "dropped S1AP message from "+from, slog.String("procedure", procedureOf(pdu).String()))
}

func parseReset(ctx context.Context, peer sender, from string, log *slog.Logger, value []byte) (*s1ap.Reset, bool) {
	reset, err := s1ap.ParseReset(value)
	if err != nil {
		spanFailed(ctx, errTypeInvalid)
		log.WarnContext(ctx, "rejected Reset from "+from, slog.Any("error", err))

		if ase, ok := errors.AsType[*s1ap.AbstractSyntaxError](err); ok {
			sendErrorIndication(ctx, peer, log, ase.Cause, ase.ErrorIndicationDiagnostics())
		}

		return nil, false
	}

	return reset, true
}

// TS 36.413 clause 8.7.1
func acknowledgeReset(ctx context.Context, peer sender, from string, log *slog.Logger, reset *s1ap.Reset) {
	out, err := (&s1ap.ResetAcknowledge{
		ConnectionList:         reset.ResetType.Part,
		CriticalityDiagnostics: responseDiagnostics(s1ap.ProcReset, reset.Diagnostics()),
	}).Marshal()
	if err != nil {
		log.ErrorContext(ctx, "failed to marshal Reset Acknowledge", slog.Any("error", err))
		return
	}

	if err := peer.Send(ctx, out, nonUEStream); err != nil {
		log.WarnContext(ctx, "failed to send Reset Acknowledge", slog.Any("error", err))
		return
	}

	log.InfoContext(ctx, "Reset from "+from+" acknowledged", slog.String("cause", causeText(reset.Cause)),
		slog.Bool("all", reset.ResetType.All), slog.Int("connections", len(reset.ResetType.Part)))
}

// TS 36.413 clause 10.3.4.2
func sendErrorIndication(ctx context.Context, peer sender, log *slog.Logger, cause s1ap.Cause,
	diag s1ap.CriticalityDiagnostics,
) {
	out, err := (&s1ap.ErrorIndication{Cause: &cause, CriticalityDiagnostics: &diag}).Marshal()
	if err != nil {
		log.ErrorContext(ctx, "failed to marshal Error Indication", slog.Any("error", err))
		return
	}

	if err := peer.Send(ctx, out, nonUEStream); err != nil {
		log.WarnContext(ctx, "failed to send Error Indication", slog.Any("error", err))
	}
}

// TS 36.413 clause 8.7.2.2
func sendUEErrorIndication(ctx context.Context, peer sender, log *slog.Logger, cause s1ap.Cause,
	mmeID *s1ap.MMEUES1APID, enbID *s1ap.ENBUES1APID,
) {
	out, err := (&s1ap.ErrorIndication{MMEUES1APID: mmeID, ENBUES1APID: enbID, Cause: &cause}).Marshal()
	if err != nil {
		log.ErrorContext(ctx, "failed to marshal Error Indication", slog.Any("error", err))
		return
	}

	if err := peer.Send(ctx, out, ueStream); err != nil {
		log.WarnContext(ctx, "failed to send Error Indication", slog.Any("error", err))
	}
}

// TS 36.413 clause 8.7.2
func logErrorIndication(ctx context.Context, from string, log *slog.Logger, value []byte) {
	ind, err := s1ap.ParseErrorIndication(value)
	if err != nil {
		log.WarnContext(ctx, "undecodable Error Indication from "+from, slog.Any("error", err))
		return
	}

	log.WarnContext(ctx, "Error Indication from "+from, slog.String("cause", causeText(ind.Cause)),
		slog.Bool("criticality_diagnostics", ind.CriticalityDiagnostics != nil),
		slog.Bool("ue_associated", ind.MMEUES1APID != nil || ind.ENBUES1APID != nil))
}

func procedureOf(pdu s1ap.PDU) s1ap.ProcedureCode {
	switch p := pdu.(type) {
	case *s1ap.InitiatingMessage:
		return p.ProcedureCode
	case *s1ap.SuccessfulOutcome:
		return p.ProcedureCode
	case *s1ap.UnsuccessfulOutcome:
		return p.ProcedureCode
	}

	return 0
}

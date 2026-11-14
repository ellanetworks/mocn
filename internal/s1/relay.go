package s1

import (
	"context"
	"log/slog"
	"sync"

	"github.com/ellanetworks/core/s1ap"
)

const (
	ueStream                              = 1
	maxnoofIndividualS1ConnectionsToReset = 256
	sourceRadio                           = "radio"
	sourceMME                             = "MME"
)

var (
	causeUnknownMMEUEID = s1ap.Cause{Group: s1ap.CauseGroupRadioNetwork, Value: s1ap.CauseRadioNetworkUnknownMMEUES1APID}
	causeUnknownENBUEID = s1ap.Cause{Group: s1ap.CauseGroupRadioNetwork, Value: s1ap.CauseRadioNetworkUnknownENBUES1APID}
	causeUnknownPair    = s1ap.Cause{Group: s1ap.CauseGroupRadioNetwork, Value: s1ap.CauseRadioNetworkUnknownPairUES1APID}
	causeEUTRANRelease  = s1ap.Cause{Group: s1ap.CauseGroupRadioNetwork, Value: s1ap.CauseRadioNetworkReleaseDueToEUTRANGeneratedReason}
	causeRadioUnspec    = s1ap.Cause{Group: s1ap.CauseGroupRadioNetwork, Value: s1ap.CauseRadioNetworkUnspecified}
	causeHOFailure      = s1ap.Cause{Group: s1ap.CauseGroupRadioNetwork, Value: s1ap.CauseRadioNetworkHOFailureInTarget}
)

type relay struct {
	g    *Gateway
	log  *slog.Logger
	intN func(int) int

	mu      sync.Mutex
	ues     *ueTable
	sendMME func(context.Context, *mmeAssoc, []byte, uint16) error
}

func newRelay(g *Gateway) *relay {
	return &relay{g: g, log: g.log, intN: g.cfg.IntN, ues: newUETable(), sendMME: sendMME}
}

func (r *relay) countInitialUE(result string) {
	r.g.core.CountInitialUE(result)
}

func sendMME(ctx context.Context, a *mmeAssoc, msg []byte, stream uint16) error {
	return a.Send(ctx, msg, stream)
}

func (r *relay) fromRAN(ctx context.Context, from *ranAssoc, pdu s1ap.PDU, msg []byte) {
	log := from.Logger(r.log)

	if m, ok := pdu.(*s1ap.InitiatingMessage); ok {
		switch m.ProcedureCode {
		case s1ap.ProcInitialUEMessage:
			r.initialUEMessage(ctx, from, log, m.Value, msg)
			return
		case s1ap.ProcReset:
			r.resetFromRAN(ctx, from, log, m.Value)
			return
		case s1ap.ProcErrorIndication:
			r.errorIndicationFromRAN(ctx, from, log, m.Value, msg)
			return
		case s1ap.ProcPathSwitchRequest:
			r.pathSwitchRequest(ctx, from, log, pdu, msg)
			return
		}
	}

	if m, ok := pdu.(*s1ap.SuccessfulOutcome); ok && m.ProcedureCode == s1ap.ProcReset {
		log.DebugContext(ctx, "Reset Acknowledge from radio")
		return
	}

	p, err := s1ap.ParseRawPDU(msg)
	if err != nil {
		undecodable(ctx, from, sourceRadio, log, pdu, err)
		return
	}

	if !ueAssociated(p) {
		terminate(ctx, from, sourceRadio, log, pdu, msg)
		return
	}

	r.uplink(ctx, from, log, p)
}

func (r *relay) fromMME(ctx context.Context, from *mmeAssoc, pdu s1ap.PDU, msg []byte) {
	log := from.Logger(r.log)

	if m, ok := pdu.(*s1ap.InitiatingMessage); ok {
		switch m.ProcedureCode {
		case s1ap.ProcReset:
			r.resetFromMME(ctx, from, log, m.Value)
			return
		case s1ap.ProcErrorIndication:
			r.errorIndicationFromMME(ctx, from, log, m.Value, msg)
			return
		case s1ap.ProcHandoverResourceAllocation:
			r.handoverRequest(ctx, from, log, pdu, msg)
			return
		case s1ap.ProcRerouteNASRequest:
			r.rerouteNASRequest(ctx, from, log, m.Value, msg)
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

	p, err := s1ap.ParseRawPDU(msg)
	if err != nil {
		undecodable(ctx, from, sourceMME, log, pdu, err)
		return
	}

	if !ueAssociated(p) {
		terminate(ctx, from, sourceMME, log, pdu, msg)
		return
	}

	r.downlink(ctx, from, log, p)
}

func ueAssociated(p *s1ap.RawPDU) bool {
	return p.Index(s1ap.IDMMEUES1APID) >= 0 || p.Index(s1ap.IDENBUES1APID) >= 0 || p.Index(s1ap.IDUES1APIDs) >= 0
}

func (r *relay) ranLost(ctx context.Context, from *ranAssoc, cause s1ap.Cause) {
	r.mu.Lock()
	gone := r.ues.Remove(r.ues.Where(func(u *ueContext) bool { return u.RAN == from })...)
	r.mu.Unlock()

	if len(gone) > 0 {
		from.Logger(r.log).InfoContext(ctx, "radio UE contexts released", slog.Int("ues", len(gone)),
			slog.String("cause", cause.String()))
	}

	r.resetMMEs(ctx, gone, cause)
}

func (r *relay) mmeLost(ctx context.Context, from *mmeAssoc) {
	r.mu.Lock()
	from.MarkLost()

	gone := r.ues.Remove(r.ues.Where(func(u *ueContext) bool { return u.Core == from })...)
	r.mu.Unlock()

	if len(gone) > 0 {
		from.Logger(r.log).InfoContext(ctx, "MME lost, UE contexts released", slog.Int("ues", len(gone)))
	}

	r.resetRANs(ctx, from.Operator(), gone, causeTransportUnavailable)
}

func (r *relay) snapshot() []UE {
	r.mu.Lock()
	ues := r.ues.Where(func(u *ueContext) bool { return u.Core != nil })

	out := make([]UE, 0, len(ues))
	for _, u := range ues {
		out = append(out, snapshotUE(u))
	}

	r.mu.Unlock()

	return out
}

func (r *relay) countByOperator() map[int64]int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.ues.CountByOperator()
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

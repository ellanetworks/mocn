package s1

import (
	"context"
	"log/slog"
	"sync"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/gtpu"
	"github.com/ellanetworks/mocn/internal/metrics"
)

const (
	ueStream                              = 1
	maxnoofIndividualS1ConnectionsToReset = 256
	sourceRadio                           = "radio"
	sourceMME                             = "MME"
)

var (
	causeUnknownMMEUEID = s1ap.CauseRadioNetworkUnknownMMEUES1APID.Cause()
	causeUnknownENBUEID = s1ap.CauseRadioNetworkUnknownENBUES1APID.Cause()
	causeUnknownPair    = s1ap.CauseRadioNetworkUnknownPairUES1APID.Cause()
	causeEUTRANRelease  = s1ap.CauseRadioNetworkReleaseDueToEUTRANGeneratedReason.Cause()
	causeRadioUnspec    = s1ap.CauseRadioNetworkUnspecified.Cause()
	causeHOFailure      = s1ap.CauseRadioNetworkHOFailureInTarget.Cause()
)

type relay struct {
	g    *Gateway
	log  *slog.Logger
	intN func(int) int

	plane *gtpu.Plane

	mu      sync.Mutex
	ues     *ueTable
	sendMME func(context.Context, *mmeAssoc, []byte, uint16) error
}

func newRelay(g *Gateway) *relay {
	r := &relay{g: g, log: g.log, intN: g.cfg.IntN, plane: g.cfg.Plane, ues: newUETable(), sendMME: sendMME}

	if r.plane != nil {
		r.ues.OnRemove(func(u *ueContext) { r.plane.ReleaseAll(&u.Ext.tunnels) })

		labels := make([]string, 0, len(tunnelProcedures))
		for _, proc := range tunnelProcedures {
			labels = append(labels, procedureLabel(proc))
		}

		r.plane.TunnelProcedures(metrics.InterfaceS1, labels...)
	}

	return r
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
		case s1ap.ProcPWSRestartIndication, s1ap.ProcPWSFailureIndication:
			r.g.pwsIndication(ctx, from, log, m.ProcedureCode, m.Value, msg)
			return
		}
	}

	if m, ok := pdu.(*s1ap.SuccessfulOutcome); ok {
		switch m.ProcedureCode {
		case s1ap.ProcReset:
			log.DebugContext(ctx, "Reset Acknowledge from radio")
			return
		case s1ap.ProcWriteReplaceWarning:
			r.g.writeReplaceWarningResponse(ctx, from, log, m.Value, msg)
			return
		case s1ap.ProcKill:
			r.g.killResponse(ctx, from, log, m.Value, msg)
			return
		}
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
		case s1ap.ProcWriteReplaceWarning:
			r.g.writeReplaceWarning(ctx, from, log, m.Value, msg)
			return
		case s1ap.ProcKill:
			r.g.kill(ctx, from, log, m.Value, msg)
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
	return present(p, s1ap.IDMMEUES1APID) || present(p, s1ap.IDENBUES1APID) || present(p, s1ap.IDUES1APIDs)
}

func present(p *s1ap.RawPDU, id s1ap.ProtocolIEID) bool {
	for range p.All(id) {
		return true
	}

	return false
}

func (r *relay) ranLost(ctx context.Context, from *ranAssoc, cause s1ap.Cause) {
	r.g.warningsLost(from)

	r.mu.Lock()
	gone := r.ues.Remove(r.ues.Where(func(u *ueContext) bool { return u.RAN == from })...)
	r.mu.Unlock()

	if ref := from.Ref(); r.plane != nil && ref != "" {
		r.plane.ForgetRadio(ref)
	}

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
		s := snapshotUE(u)
		if r.plane != nil {
			s.Tunnels = r.plane.Describe(&u.Ext.tunnels)
		}

		out = append(out, s)
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

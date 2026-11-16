package ng

import (
	"context"
	"log/slog"
	"sync"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/gtpu"
	"github.com/ellanetworks/mocn/internal/metrics"
)

const (
	ueStream                    = 1
	maxnoofNGConnectionsToReset = 65536
	sourceRadio                 = "radio"
	sourceAMF                   = "AMF"
)

var (
	causeUnknownLocalID       = ngap.CauseRadioNetworkUnknownLocalUENGAPID.Cause()
	causeInconsistentRemoteID = ngap.CauseRadioNetworkInconsistentRemoteUEID.Cause()
	causeNGRANRelease         = ngap.CauseRadioNetworkReleaseDueToNGRANGeneratedReason.Cause()
	causeRadioUnspec          = ngap.CauseRadioNetworkUnspecified.Cause()
	causeHOFailure            = ngap.CauseRadioNetworkHOFailureInTarget.Cause()
)

type relay struct {
	g     *Gateway
	log   *slog.Logger
	intN  func(int) int
	plane *gtpu.Plane

	mu      sync.Mutex
	ues     *ueTable
	sets    map[*amfAssoc][]amfSet
	sendAMF func(context.Context, *amfAssoc, []byte, uint16) error
}

func newRelay(g *Gateway) *relay {
	r := &relay{
		g: g, log: g.log, intN: g.cfg.IntN, plane: g.cfg.Plane, ues: newUETable(), sets: map[*amfAssoc][]amfSet{},
		sendAMF: sendAMF,
	}

	if r.plane != nil {
		r.ues.OnRemove(func(u *ueContext) { r.plane.ReleaseAll(&u.Ext.tunnels) })

		labels := make([]string, 0, len(tunnelProcedures))
		for _, proc := range tunnelProcedures {
			labels = append(labels, procedureLabel(proc))
		}

		r.plane.TunnelProcedures(metrics.InterfaceNG, labels...)
	}

	return r
}

func sendAMF(ctx context.Context, a *amfAssoc, msg []byte, stream uint16) error {
	return a.SendWait(ctx, msg, stream)
}

// TS 38.300 clause 4.10.2.3
func (r *relay) fromRAN(ctx context.Context, from *ranAssoc, pdu ngap.PDU, msg []byte) {
	log := from.Logger(r.log)

	if m, ok := pdu.(*ngap.InitiatingMessage); ok {
		switch m.ProcedureCode {
		case ngap.ProcInitialUEMessage:
			r.initialUEMessage(ctx, from, log, m.Value, msg)
			return
		case ngap.ProcNGReset:
			r.resetFromRAN(ctx, from, log, m.Value)
			return
		case ngap.ProcErrorIndication:
			r.errorIndicationFromRAN(ctx, from, log, m.Value, msg)
			return
		case ngap.ProcPathSwitchRequest:
			r.pathSwitchRequest(ctx, from, log, m.Value, msg)
			return
		case ngap.ProcPWSRestartIndication, ngap.ProcPWSFailureIndication:
			r.g.pwsIndication(ctx, from, log, m.ProcedureCode, m.Value, msg)
			return
		}
	}

	if m, ok := pdu.(*ngap.SuccessfulOutcome); ok {
		switch m.ProcedureCode {
		case ngap.ProcNGReset:
			log.DebugContext(ctx, "NG Reset Acknowledge from radio")
			return
		case ngap.ProcWriteReplaceWarning:
			r.g.writeReplaceWarningResponse(ctx, from, log, m.Value, msg)
			return
		case ngap.ProcPWSCancel:
			r.g.pwsCancelResponse(ctx, from, log, m.Value, msg)
			return
		}
	}

	p, err := ngap.ParseRawPDU(msg)
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

// TS 38.300 clause 4.10.2.3
func (r *relay) fromAMF(ctx context.Context, from *amfAssoc, pdu ngap.PDU, msg []byte) {
	log := from.Logger(r.log)

	if m, ok := pdu.(*ngap.InitiatingMessage); ok {
		switch m.ProcedureCode {
		case ngap.ProcNGReset:
			r.resetFromAMF(ctx, from, log, m.Value)
			return
		case ngap.ProcErrorIndication:
			r.errorIndicationFromAMF(ctx, from, log, m.Value, msg)
			return
		case ngap.ProcHandoverResourceAllocation:
			r.handoverRequest(ctx, from, log, pdu, msg)
			return
		case ngap.ProcPaging:
			r.paging(ctx, from, log, m.Value, msg)
			return
		case ngap.ProcAMFStatusIndication:
			r.g.amfStatusIndication(ctx, from, log, m.Value)
			return
		case ngap.ProcOverloadStart:
			r.g.overloadStart(ctx, from, log, m.Value)
			return
		case ngap.ProcOverloadStop:
			r.g.overloadStop(ctx, from, log, m.Value)
			return
		case ngap.ProcUETNLABindingRelease:
			r.tnlaBindingRelease(ctx, from, log, m.Value)
			return
		case ngap.ProcRerouteNASRequest:
			r.rerouteNASRequest(ctx, from, log, m.Value, msg)
			return
		case ngap.ProcWriteReplaceWarning:
			r.g.writeReplaceWarning(ctx, from, log, m.Value, msg)
			return
		case ngap.ProcPWSCancel:
			r.g.pwsCancel(ctx, from, log, m.Value, msg)
			return
		}
	}

	if m, ok := pdu.(*ngap.SuccessfulOutcome); ok && m.ProcedureCode == ngap.ProcNGReset {
		log.DebugContext(ctx, "NG Reset Acknowledge from AMF")
		return
	}

	p, err := ngap.ParseRawPDU(msg)
	if err != nil {
		undecodable(ctx, from, sourceAMF, log, pdu, err)
		return
	}

	if !ueAssociated(p) {
		terminate(ctx, from, sourceAMF, log, pdu, msg)
		return
	}

	r.downlink(ctx, from, log, p)
}

func ueAssociated(p *ngap.RawPDU) bool {
	return present(p, ngap.IDAMFUENGAPID) || present(p, ngap.IDRANUENGAPID) || present(p, ngap.IDUENGAPIDs)
}

func present(p *ngap.RawPDU, id ngap.ProtocolIEID) bool {
	for range p.All(id) {
		return true
	}

	return false
}

func (r *relay) ranLost(ctx context.Context, from *ranAssoc, cause ngap.Cause) {
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

	r.resetAMFs(ctx, gone, cause)
}

func (r *relay) countInitialUE(result string) {
	r.g.core.CountInitialUE(result)
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

package ng

import (
	"context"
	"errors"
	"log/slog"
	"slices"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/gw"
	"github.com/ellanetworks/mocn/internal/tracing"
)

const (
	pagingRelayed = "relayed"
	pagingNoRadio = "no_radio"
	pagingInvalid = "invalid"
)

var pagingResults = []string{pagingRelayed, pagingNoRadio, pagingInvalid}

// TS 38.413 clause 8.5.1
func (g *Gateway) pagingTargetsLocked(operatorID int64, list []ngap.TAI) []*RANAssociation {
	o, ok := g.operatorLocked(operatorID)
	if !ok {
		return nil
	}

	var tais []TAI

	for _, t := range list {
		if p, err := gw.DecodePLMN(t.PLMNIdentity); err == nil && slices.Contains(o.plmns, p) {
			tais = append(tais, TAI{PLMN: p, TAC: uint32(t.TAC)})
		}
	}

	var out []*RANAssociation

	for _, n := range g.ranNodes {
		if n.up && n.setup != nil && servesAnyTAI(n.setup.tas, tais) {
			out = append(out, n)
		}
	}

	return out
}

func servesAnyTAI(tas []TA, tais []TAI) bool {
	for _, ta := range tas {
		for _, tai := range tais {
			if ta.TAC == tai.TAC && slices.ContainsFunc(ta.BroadcastPLMNs, func(b BroadcastPLMN) bool { return b.PLMN == tai.PLMN }) {
				return true
			}
		}
	}

	return false
}

// TS 38.413 clause 8.5.1
// TS 38.300 clause 4.10.2.3
func (r *relay) paging(ctx context.Context, a *AMFAssociation, log *slog.Logger, value, msg []byte) {
	p, err := ngap.ParsePaging(value)
	if err != nil {
		r.metrics.Paging.WithLabelValues(interfaceNG, pagingInvalid).Inc()
		tracing.SpanFailed(ctx, pagingInvalid)
		log.WarnContext(ctx, "rejected Paging from AMF", slog.Any("error", err))

		if ase, ok := errors.AsType[*ngap.AbstractSyntaxError](err); ok {
			sendErrorIndication(ctx, a, log, ase.Cause, ase.ErrorIndicationDiagnostics())
		}

		return
	}

	r.g.mu.Lock()
	radios := r.g.pagingTargetsLocked(a.client.operatorID, p.TAIListForPaging)
	r.g.mu.Unlock()

	if len(radios) == 0 {
		r.metrics.Paging.WithLabelValues(interfaceNG, pagingNoRadio).Inc()
		tracing.SpanFailed(ctx, pagingNoRadio)
		log.DebugContext(ctx, "Paging from AMF not relayed: no radio serves a TAI of the list", slog.Int("tais", len(p.TAIListForPaging)))

		return
	}

	r.metrics.Paging.WithLabelValues(interfaceNG, pagingRelayed).Inc()
	log.DebugContext(ctx, "Paging from AMF relayed to radios", slog.Int("radios", len(radios)))
	sendRadios(ctx, radios, a.client.operator(), log, "Paging", msg)
}

func sendRadios(ctx context.Context, radios []*RANAssociation, operator string, log *slog.Logger, what string,
	out []byte,
) {
	for _, n := range radios {
		if err := (operatorRAN{n: n, operator: operator}).Send(ctx, out, nonUEStream); err != nil {
			ranLog(log, n).WarnContext(ctx, "failed to send "+what+" to radio", slog.Any("error", err))
		}
	}
}

func (g *Gateway) operatorRadiosLocked(operatorID int64) []*RANAssociation {
	o, ok := g.operatorLocked(operatorID)
	if !ok {
		return nil
	}

	var out []*RANAssociation

	for _, n := range g.ranNodes {
		if n.up && n.setup != nil && broadcasts(n.setup.tas, o.plmns) {
			out = append(out, n)
		}
	}

	return out
}

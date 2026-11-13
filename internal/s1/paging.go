package s1

import (
	"context"
	"errors"
	"log/slog"
	"slices"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/gw"
	"github.com/ellanetworks/mocn/internal/tracing"
)

const (
	pagingRelayed = "relayed"
	pagingNoRadio = "no_radio"
	pagingInvalid = "invalid"
)

var pagingResults = []string{pagingRelayed, pagingNoRadio, pagingInvalid}

// TS 36.413 clause 8.5.2
// TS 36.300 clause 4.7.4
func (g *Gateway) pagingTargetsLocked(operatorID int64, list []s1ap.TAI) []*RANAssociation {
	o, ok := g.operatorLocked(operatorID)
	if !ok {
		return nil
	}

	var tais []TAI

	for _, t := range list {
		if p, err := gw.DecodePLMN(t.PLMNIdentity); err == nil && slices.Contains(o.plmns, p) {
			tais = append(tais, TAI{PLMN: p, TAC: uint16(t.TAC)})
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
			if ta.TAC == tai.TAC && slices.Contains(ta.BroadcastPLMNs, tai.PLMN) {
				return true
			}
		}
	}

	return false
}

// TS 36.413 clause 8.5
// TS 36.300 clause 4.7.4
func (r *relay) paging(ctx context.Context, a *MMEAssociation, log *slog.Logger, value, msg []byte) {
	p, err := s1ap.ParsePaging(value)
	if err != nil {
		r.metrics.Paging.WithLabelValues(interfaceS1, pagingInvalid).Inc()
		tracing.SpanFailed(ctx, pagingInvalid)
		log.WarnContext(ctx, "rejected Paging from MME", slog.Any("error", err))

		if ase, ok := errors.AsType[*s1ap.AbstractSyntaxError](err); ok {
			sendErrorIndication(ctx, a, log, ase.Cause, ase.ErrorIndicationDiagnostics())
		}

		return
	}

	r.g.mu.Lock()
	radios := r.g.pagingTargetsLocked(a.client.operatorID, p.TAIList)
	r.g.mu.Unlock()

	if len(radios) == 0 {
		r.metrics.Paging.WithLabelValues(interfaceS1, pagingNoRadio).Inc()
		tracing.SpanFailed(ctx, pagingNoRadio)
		log.DebugContext(ctx, "Paging from MME not relayed: no radio serves a TAI of the list", slog.Int("tais", len(p.TAIList)))

		return
	}

	r.metrics.Paging.WithLabelValues(interfaceS1, pagingRelayed).Inc()
	log.DebugContext(ctx, "Paging from MME relayed to radios", slog.Int("radios", len(radios)))
	sendRadios(ctx, radios, a.client.operator(), log, "Paging", msg)
}

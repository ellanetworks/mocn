package s1

import (
	"context"
	"errors"
	"log/slog"
	"slices"

	"github.com/ellanetworks/core/s1ap"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	pagingRelayed = "relayed"
	pagingNoRadio = "no_radio"
	pagingInvalid = "invalid"
)

var pagingResults = []string{pagingRelayed, pagingNoRadio, pagingInvalid}

func newPagingCounter() *prometheus.CounterVec {
	c := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ellamocn_paging_messages_total",
		Help: "PAGING messages received from MMEs, by interface (s1) and result " +
			"(relayed: sent to the radios that serve a TAI of the List of TAIs in one of the operator's PLMNs; " +
			"no_radio: no radio that is up serves such a TAI; invalid: undecodable).",
	}, []string{"interface", "result"})

	for _, res := range pagingResults {
		c.WithLabelValues(interfaceS1, res)
	}

	return c
}

// TS 36.413 clause 8.5.2
// TS 36.300 clause 4.7.4
func (g *Gateway) pagingTargetsLocked(operatorID int64, list []s1ap.TAI) []*RANAssociation {
	o, ok := g.operatorLocked(operatorID)
	if !ok {
		return nil
	}

	var tais []TAI

	for _, t := range list {
		if p, err := decodePLMN(t.PLMNIdentity); err == nil && slices.Contains(o.plmns, p) {
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
		r.paged.WithLabelValues(interfaceS1, pagingInvalid).Inc()
		spanFailed(ctx, pagingInvalid)
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
		r.paged.WithLabelValues(interfaceS1, pagingNoRadio).Inc()
		spanFailed(ctx, pagingNoRadio)
		log.DebugContext(ctx, "Paging from MME not relayed: no radio serves a TAI of the list", slog.Int("tais", len(p.TAIList)))

		return
	}

	r.paged.WithLabelValues(interfaceS1, pagingRelayed).Inc()
	log.DebugContext(ctx, "Paging from MME relayed to radios", slog.Int("radios", len(radios)))
	sendRadios(ctx, radios, log, "Paging", msg)
}

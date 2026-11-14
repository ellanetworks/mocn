package s1

import (
	"context"
	"log/slog"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/plmnid"
	"github.com/ellanetworks/mocn/internal/tracing"
)

// TS 36.413 clause 8.5
// TS 36.300 clause 4.7.4
func (r *relay) paging(ctx context.Context, a *mmeAssoc, log *slog.Logger, value, msg []byte) {
	p, err := s1ap.ParsePaging(value)
	if err != nil {
		r.g.core.CountPaging(assoc.PagingInvalid)
		tracing.SpanFailed(ctx, assoc.PagingInvalid)
		log.WarnContext(ctx, "rejected Paging from MME", slog.Any("error", err))

		indicateParseError(ctx, a, log, s1ap.ProcPaging, err)

		return
	}

	tais := make([]assoc.TAI, 0, len(p.TAIList))

	for _, t := range p.TAIList {
		if id, err := plmnid.Decode(t.PLMNIdentity); err == nil {
			tais = append(tais, assoc.TAI{PLMN: id, TAC: uint32(t.TAC)})
		}
	}

	r.g.core.Page(ctx, a, log, tais, len(p.TAIList), msg)
}

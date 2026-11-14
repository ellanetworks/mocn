package ng

import (
	"context"
	"log/slog"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/plmnid"
	"github.com/ellanetworks/mocn/internal/tracing"
)

// TS 38.413 clause 8.5.1
// TS 38.300 clause 4.10.2.3
func (r *relay) paging(ctx context.Context, a *amfAssoc, log *slog.Logger, value, msg []byte) {
	p, err := ngap.ParsePaging(value)
	if err != nil {
		r.g.core.CountPaging(assoc.PagingInvalid)
		tracing.SpanFailed(ctx, assoc.PagingInvalid)
		log.WarnContext(ctx, "rejected Paging from AMF", slog.Any("error", err))

		indicateParseError(ctx, a, log, ngap.ProcPaging, err)

		return
	}

	tais := make([]assoc.TAI, 0, len(p.TAIListForPaging))

	for _, t := range p.TAIListForPaging {
		if id, err := plmnid.Decode(t.PLMNIdentity); err == nil {
			tais = append(tais, assoc.TAI{PLMN: id, TAC: uint32(t.TAC)})
		}
	}

	r.g.core.Page(ctx, a, log, tais, len(p.TAIListForPaging), msg)
}

package s1

import (
	"context"
	"log/slog"
	"slices"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/tracing"
)

// TS 36.413 clause 8.7.1.2.2
// TS 36.300 clause 4.6.2
func (r *relay) resetFromRAN(ctx context.Context, n *ranAssoc, log *slog.Logger, value []byte) {
	reset, ok := parseReset(ctx, n, sourceRadio, log, value)
	if !ok {
		return
	}

	r.mu.Lock()

	var gone []*ueContext

	if reset.ResetType.All {
		gone = r.ues.Remove(r.ues.Where(func(u *ueContext) bool { return u.RAN == n })...)
	} else {
		for _, item := range reset.ResetType.Part {
			gone = append(gone, r.ues.Remove(r.ues.OnRAN(n, item.MMEUES1APID, item.ENBUES1APID)...)...)
		}
	}

	r.mu.Unlock()

	acknowledgeReset(ctx, n, sourceRadio, log, reset)
	r.resetMMEs(ctx, gone, causeOr(reset.Cause))
}

// TS 36.413 clause 8.7.1.2.1
// TS 36.300 clause 4.6.2
func (r *relay) resetFromMME(ctx context.Context, a *mmeAssoc, log *slog.Logger, value []byte) {
	reset, ok := parseReset(ctx, a, sourceMME, log, value)
	if !ok {
		return
	}

	r.mu.Lock()

	var gone []*ueContext

	if reset.ResetType.All {
		gone = r.ues.Remove(r.ues.Where(func(u *ueContext) bool { return u.Core == a })...)
	} else {
		for _, item := range reset.ResetType.Part {
			gone = append(gone, r.ues.Remove(r.ues.OnCore(a, item.MMEUES1APID, item.ENBUES1APID)...)...)
		}
	}

	r.mu.Unlock()

	acknowledgeReset(ctx, a, sourceMME, log, reset)
	r.resetRANs(ctx, a.Operator(), gone, causeOr(reset.Cause))
}

func causeOr(c *s1ap.Cause) s1ap.Cause {
	if c == nil {
		return causeMiscUnspecified
	}

	return *c
}

// TS 36.413 clause 8.7.1.2.2
func (r *relay) resetMMEs(ctx context.Context, ues []*ueContext, cause s1ap.Cause) {
	groups := map[*mmeAssoc][]s1ap.UEAssociatedLogicalS1ConnectionItem{}

	var order []*mmeAssoc

	for _, u := range ues {
		if u.Core == nil || u.Core.Lost() {
			continue
		}

		item := s1ap.UEAssociatedLogicalS1ConnectionItem{ENBUES1APID: s1ap.Ptr(u.CoreRANID)}
		if u.CoreIDKnown {
			item.MMEUES1APID = s1ap.Ptr(u.CoreID)
		}

		if _, ok := groups[u.Core]; !ok {
			order = append(order, u.Core)
		}

		groups[u.Core] = append(groups[u.Core], item)
	}

	for _, a := range order {
		sendResets(ctx, a, a.Logger(r.log), sourceMME, cause, groups[a])
	}
}

// TS 36.413 clause 8.7.1.2.1
func (r *relay) resetRANs(ctx context.Context, operator string, ues []*ueContext, cause s1ap.Cause) {
	groups := map[*ranAssoc][]s1ap.UEAssociatedLogicalS1ConnectionItem{}

	var order []*ranAssoc

	for _, u := range ues {
		item := s1ap.UEAssociatedLogicalS1ConnectionItem{ENBUES1APID: s1ap.Ptr(u.RANID)}
		if u.CoreIDKnown {
			item.MMEUES1APID = s1ap.Ptr(u.RANCoreID)
		}

		if _, ok := groups[u.RAN]; !ok {
			order = append(order, u.RAN)
		}

		groups[u.RAN] = append(groups[u.RAN], item)
	}

	for _, n := range order {
		sendResets(ctx, n.For(operator), n.Logger(r.log), sourceRadio, cause, groups[n])
	}
}

func sendResets(ctx context.Context, peer assoc.Sender, log *slog.Logger, to string, cause s1ap.Cause,
	items []s1ap.UEAssociatedLogicalS1ConnectionItem,
) {
	for chunk := range slices.Chunk(items, maxnoofIndividualS1ConnectionsToReset) {
		out, err := (&s1ap.Reset{Cause: &cause, ResetType: s1ap.ResetType{Part: chunk}}).Marshal()
		if err != nil {
			log.ErrorContext(ctx, "failed to marshal Reset", slog.Any("error", err))
			return
		}

		if err := peer.Send(ctx, out, assoc.NonUEStream); err != nil {
			log.WarnContext(ctx, "failed to send Reset to "+to, slog.Any("error", err))
			return
		}

		log.InfoContext(ctx, "Reset sent to "+to, slog.String("cause", cause.String()), slog.Int("connections", len(chunk)))
	}
}

func parseReset(ctx context.Context, peer assoc.Sender, from string, log *slog.Logger, value []byte) (*s1ap.Reset, bool) {
	reset, err := s1ap.ParseReset(value)
	if err != nil {
		tracing.SpanFailed(ctx, assoc.ErrTypeInvalid)
		log.WarnContext(ctx, "rejected Reset from "+from, slog.Any("error", err))

		indicateParseError(ctx, peer, log, s1ap.ProcReset, err)

		return nil, false
	}

	return reset, true
}

// TS 36.413 clause 8.7.1
func acknowledgeReset(ctx context.Context, peer assoc.Sender, from string, log *slog.Logger, reset *s1ap.Reset) {
	out, err := (&s1ap.ResetAcknowledge{
		ConnectionList:         reset.ResetType.Part,
		CriticalityDiagnostics: responseDiagnostics(s1ap.ProcReset, reset.Diagnostics()),
	}).Marshal()
	if err != nil {
		log.ErrorContext(ctx, "failed to marshal Reset Acknowledge", slog.Any("error", err))
		return
	}

	if err := peer.Send(ctx, out, assoc.NonUEStream); err != nil {
		log.WarnContext(ctx, "failed to send Reset Acknowledge", slog.Any("error", err))
		return
	}

	log.InfoContext(ctx, "Reset from "+from+" acknowledged", slog.String("cause", causeText(reset.Cause)),
		slog.Bool("all", reset.ResetType.All), slog.Int("connections", len(reset.ResetType.Part)))
}

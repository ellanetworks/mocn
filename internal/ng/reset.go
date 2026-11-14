package ng

import (
	"context"
	"log/slog"
	"slices"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/tracing"
)

// TS 38.413 clause 8.7.4.2.2
// TS 38.300 clause 4.10.2.3
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
			gone = append(gone, r.ues.Remove(r.ues.OnRAN(n, item.AMFUENGAPID, item.RANUENGAPID)...)...)
		}
	}

	r.mu.Unlock()

	acknowledgeReset(ctx, n, sourceRadio, log, reset)
	r.resetAMFs(ctx, gone, causeOr(reset.Cause))
}

// TS 38.413 clause 8.7.4.2.1
// TS 38.300 clause 4.10.2.3
func (r *relay) resetFromAMF(ctx context.Context, a *amfAssoc, log *slog.Logger, value []byte) {
	reset, ok := parseReset(ctx, a, sourceAMF, log, value)
	if !ok {
		return
	}

	r.mu.Lock()

	var gone []*ueContext

	if reset.ResetType.All {
		gone = r.ues.Remove(r.ues.Where(func(u *ueContext) bool { return u.Core == a })...)
	} else {
		for _, item := range reset.ResetType.Part {
			gone = append(gone, r.ues.Remove(r.ues.OnCore(a, item.AMFUENGAPID, item.RANUENGAPID)...)...)
		}
	}

	r.mu.Unlock()

	acknowledgeReset(ctx, a, sourceAMF, log, reset)
	r.resetRANs(ctx, a.Operator(), gone, causeOr(reset.Cause))
}

func causeOr(c *ngap.Cause) ngap.Cause {
	if c == nil {
		return causeMiscUnspecified
	}

	return *c
}

// TS 38.413 clause 8.7.4.2.2
// TS 23.502 clause 4.2.7.2.3
// TS 23.502 clause 4.2.7.3
// TS 23.501 clause 5.9.9
// TS 23.501 clause 5.21.2.3
func (r *relay) resetAMFs(ctx context.Context, ues []*ueContext, cause ngap.Cause) {
	groups := map[*amfAssoc][]ngap.UEAssociatedLogicalNGConnectionItem{}

	var order []*amfAssoc

	for _, u := range ues {
		if u.Core == nil {
			continue
		}

		to := u.Core
		if u.CoreIDKnown && (u.Ext.unbound || to.Lost()) {
			to = r.rebindTarget(u.OperatorID, r.bindingOf(u))
		}

		if to == nil || to.Lost() {
			continue
		}

		item := ngap.UEAssociatedLogicalNGConnectionItem{RANUENGAPID: ngap.Ptr(u.CoreRANID)}
		if u.CoreIDKnown {
			item.AMFUENGAPID = ngap.Ptr(u.CoreID)
		}

		if _, ok := groups[to]; !ok {
			order = append(order, to)
		}

		groups[to] = append(groups[to], item)
	}

	for _, a := range order {
		sendResets(ctx, a, a.Logger(r.log), sourceAMF, cause, groups[a])
	}
}

// TS 38.413 clause 8.7.4.2.1
func (r *relay) resetRANs(ctx context.Context, operator string, ues []*ueContext, cause ngap.Cause) {
	groups := map[*ranAssoc][]ngap.UEAssociatedLogicalNGConnectionItem{}

	var order []*ranAssoc

	for _, u := range ues {
		item := ngap.UEAssociatedLogicalNGConnectionItem{RANUENGAPID: ngap.Ptr(u.RANID)}
		if u.CoreIDKnown {
			item.AMFUENGAPID = ngap.Ptr(u.RANCoreID)
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

func sendResets(ctx context.Context, peer assoc.Sender, log *slog.Logger, to string, cause ngap.Cause,
	items []ngap.UEAssociatedLogicalNGConnectionItem,
) {
	for chunk := range slices.Chunk(items, maxnoofNGConnectionsToReset) {
		out, err := (&ngap.NGReset{Cause: &cause, ResetType: ngap.ResetType{Part: chunk}}).Marshal()
		if err != nil {
			log.ErrorContext(ctx, "failed to marshal NG Reset", slog.Any("error", err))
			return
		}

		if err := peer.Send(ctx, out, assoc.NonUEStream); err != nil {
			log.WarnContext(ctx, "failed to send NG Reset to "+to, slog.Any("error", err))
			return
		}

		log.InfoContext(ctx, "NG Reset sent to "+to, slog.String("cause", cause.String()), slog.Int("connections", len(chunk)))
	}
}

func parseReset(ctx context.Context, peer assoc.Sender, from string, log *slog.Logger, value []byte) (*ngap.NGReset, bool) {
	reset, err := ngap.ParseNGReset(value)
	if err != nil {
		tracing.SpanFailed(ctx, assoc.ErrTypeInvalid)
		log.WarnContext(ctx, "rejected NG Reset from "+from, slog.Any("error", err))

		indicateParseError(ctx, peer, log, ngap.ProcNGReset, err)

		return nil, false
	}

	return reset, true
}

// TS 38.413 clause 8.7.4
func acknowledgeReset(ctx context.Context, peer assoc.Sender, from string, log *slog.Logger, reset *ngap.NGReset) {
	out, err := (&ngap.NGResetAcknowledge{
		ConnectionList:         reset.ResetType.Part,
		CriticalityDiagnostics: responseDiagnostics(ngap.ProcNGReset, reset.Diagnostics()),
	}).Marshal()
	if err != nil {
		log.ErrorContext(ctx, "failed to marshal NG Reset Acknowledge", slog.Any("error", err))
		return
	}

	if err := peer.Send(ctx, out, assoc.NonUEStream); err != nil {
		log.WarnContext(ctx, "failed to send NG Reset Acknowledge", slog.Any("error", err))
		return
	}

	log.InfoContext(ctx, "NG Reset from "+from+" acknowledged", slog.String("cause", causeText(reset.Cause)),
		slog.Bool("all", reset.ResetType.All), slog.Int("connections", len(reset.ResetType.Part)))
}

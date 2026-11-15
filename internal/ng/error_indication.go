package ng

import (
	"context"
	"log/slog"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/assoc"
)

// TS 38.413 clause 8.7.5
// TS 38.413 clause 10.6
func (r *relay) errorIndicationFromRAN(ctx context.Context, n *ranAssoc, log *slog.Logger, value, msg []byte) {
	ind, err := ngap.ParseErrorIndication(value)
	if err != nil || ind.AMFUENGAPID == nil || ind.RANUENGAPID == nil {
		logErrorIndication(ctx, sourceRadio, log, value)
		return
	}

	r.mu.Lock()

	u, _ := r.ranUE(n, ueIDs{amf: ind.AMFUENGAPID, ran: ind.RANUENGAPID})
	if u == nil {
		r.mu.Unlock()
		logErrorIndication(ctx, sourceRadio, log, value)

		return
	}

	if u.Core == nil || !u.CoreIDKnown {
		if releasesConnection(ind.Cause) {
			r.ues.Remove(u)
		}

		r.mu.Unlock()
		logErrorIndication(ctx, sourceRadio, log, value)

		return
	}

	out, err := rewriteRaw(msg, &u.CoreID, &u.CoreRANID)
	if err == nil && releasesConnection(ind.Cause) {
		r.ues.Remove(u)
	}

	amf := u.Core
	r.mu.Unlock()

	if err != nil {
		log.WarnContext(ctx, "failed to relay Error Indication to AMF", slog.Any("error", err))
		return
	}

	log.InfoContext(ctx, "relayed Error Indication to AMF", slog.String("cause", causeText(ind.Cause)))

	if err := amf.Send(ctx, out, ueStream); err != nil {
		log.WarnContext(ctx, "failed to send Error Indication to AMF", slog.Any("error", err))
	}
}

// TS 38.413 clause 8.7.5
// TS 38.413 clause 10.6
func (r *relay) errorIndicationFromAMF(ctx context.Context, a *amfAssoc, log *slog.Logger, value, msg []byte) {
	ind, err := ngap.ParseErrorIndication(value)
	if err == nil && ind.AMFUENGAPID == nil && ind.RANUENGAPID != nil {
		r.releaseOnErrorIndication(ctx, a, log, *ind.RANUENGAPID, ind.Cause)
		return
	}

	if err == nil && ind.AMFUENGAPID != nil && ind.RANUENGAPID == nil && releasesConnection(ind.Cause) {
		r.releaseOnAMFErrorIndication(ctx, a, log, *ind.AMFUENGAPID, ind.Cause)
		return
	}

	if err != nil || ind.AMFUENGAPID == nil || ind.RANUENGAPID == nil {
		logErrorIndication(ctx, sourceAMF, log, value)
		return
	}

	r.mu.Lock()

	u := r.ues.ByCoreRANID(*ind.RANUENGAPID)
	if u == nil || u.Core != a || !u.CoreIDKnown || u.CoreID != *ind.AMFUENGAPID {
		r.mu.Unlock()
		logErrorIndication(ctx, sourceAMF, log, value)

		return
	}

	out, err := rewriteRaw(msg, &u.RANCoreID, &u.RANID)
	if err == nil && releasesConnection(ind.Cause) {
		r.ues.Remove(u)
	}

	ran := u.RAN
	r.mu.Unlock()

	if err != nil {
		log.WarnContext(ctx, "failed to relay Error Indication to radio", slog.Any("error", err))
		return
	}

	log.InfoContext(ctx, "relayed Error Indication to radio", slog.String("cause", causeText(ind.Cause)))

	if err := ran.For(a.Operator()).Send(ctx, out, ueStream); err != nil {
		log.WarnContext(ctx, "failed to send Error Indication to radio", slog.Any("error", err))
	}
}

// TS 38.413 clause 10.6
func (r *relay) releaseOnErrorIndication(ctx context.Context, a *amfAssoc, log *slog.Logger, ranID ngap.RANUENGAPID,
	cause *ngap.Cause,
) {
	log = log.With(slog.Uint64("ran_ue_ngap_id", uint64(ranID)), slog.String("cause", causeText(cause)))

	r.mu.Lock()

	u := r.ues.ByCoreRANID(ranID)
	if u == nil || u.Core != a {
		r.mu.Unlock()
		log.WarnContext(ctx, "Error Indication from AMF for an unknown RAN UE NGAP ID")

		return
	}

	gone := r.ues.Remove(u)
	r.mu.Unlock()

	log.WarnContext(ctx, "Error Indication from AMF with only the RAN UE NGAP ID, UE context released")
	r.resetRANs(ctx, a.Operator(), gone, causeOr(cause))
}

// TS 38.413 clause 10.6
func (r *relay) releaseOnAMFErrorIndication(ctx context.Context, a *amfAssoc, log *slog.Logger, amfID ngap.AMFUENGAPID,
	cause *ngap.Cause,
) {
	log = log.With(slog.Uint64("amf_ue_ngap_id", uint64(amfID)), slog.String("cause", causeText(cause)))

	r.mu.Lock()
	gone := r.ues.Remove(r.ues.ByCoreID(a, amfID))
	r.mu.Unlock()

	if len(gone) == 0 {
		log.WarnContext(ctx, "Error Indication from AMF for an unknown AMF UE NGAP ID")
		return
	}

	log.WarnContext(ctx, "Error Indication from AMF with only the AMF UE NGAP ID, UE context released")
	r.resetRANs(ctx, a.Operator(), gone, *cause)
}

func rewriteRaw(msg []byte, amfID *ngap.AMFUENGAPID, ranID *ngap.RANUENGAPID) ([]byte, error) {
	p, err := ngap.ParseRawPDU(msg)
	if err != nil {
		return nil, err
	}

	return rewrite(p, amfID, ranID)
}

// TS 38.413 clause 10.6
func releasesConnection(c *ngap.Cause) bool {
	return c != nil && (c.Equal(causeUnknownLocalID) || c.Equal(causeInconsistentRemoteID))
}

// TS 38.413 clause 8.7.5.2
func sendUEErrorIndication(ctx context.Context, peer assoc.Sender, log *slog.Logger, cause ngap.Cause,
	amfID *ngap.AMFUENGAPID, ranID *ngap.RANUENGAPID,
) {
	indicate(ctx, peer, log, ueIDs{amf: amfID, ran: ranID}, cause, nil)
}

// TS 38.413 clause 8.7.5
func logErrorIndication(ctx context.Context, from string, log *slog.Logger, value []byte) {
	ind, err := ngap.ParseErrorIndication(value)
	if err != nil {
		log.WarnContext(ctx, "undecodable Error Indication from "+from, slog.Any("error", err))
		return
	}

	log.WarnContext(ctx, "Error Indication from "+from, slog.String("cause", causeText(ind.Cause)),
		slog.Bool("criticality_diagnostics", ind.CriticalityDiagnostics != nil),
		slog.Bool("ue_associated", ind.AMFUENGAPID != nil || ind.RANUENGAPID != nil))
}

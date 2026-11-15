package s1

import (
	"context"
	"log/slog"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/assoc"
)

// TS 36.413 clause 8.7.2
// TS 36.413 clause 10.6
func (r *relay) errorIndicationFromRAN(ctx context.Context, n *ranAssoc, log *slog.Logger, value, msg []byte) {
	ind, err := s1ap.ParseErrorIndication(value)
	if err != nil || ind.MMEUES1APID == nil || ind.ENBUES1APID == nil {
		logErrorIndication(ctx, sourceRadio, log, value)
		return
	}

	r.mu.Lock()

	u, _ := r.ranUE(n, ueIDs{mme: ind.MMEUES1APID, enb: ind.ENBUES1APID})
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

	mme := u.Core
	r.mu.Unlock()

	if err != nil {
		log.WarnContext(ctx, "failed to relay Error Indication to MME", slog.Any("error", err))
		return
	}

	log.InfoContext(ctx, "relayed Error Indication to MME", slog.String("cause", causeText(ind.Cause)))

	if err := mme.Send(ctx, out, ueStream); err != nil {
		log.WarnContext(ctx, "failed to send Error Indication to MME", slog.Any("error", err))
	}
}

// TS 36.413 clause 8.7.2
// TS 36.413 clause 10.6
func (r *relay) errorIndicationFromMME(ctx context.Context, a *mmeAssoc, log *slog.Logger, value, msg []byte) {
	ind, err := s1ap.ParseErrorIndication(value)
	if err == nil && ind.MMEUES1APID == nil && ind.ENBUES1APID != nil {
		r.releaseOnErrorIndication(ctx, a, log, *ind.ENBUES1APID, ind.Cause)
		return
	}

	if err == nil && ind.MMEUES1APID != nil && ind.ENBUES1APID == nil && releasesConnection(ind.Cause) {
		r.releaseOnMMEErrorIndication(ctx, a, log, *ind.MMEUES1APID, ind.Cause)
		return
	}

	if err != nil || ind.MMEUES1APID == nil || ind.ENBUES1APID == nil {
		logErrorIndication(ctx, sourceMME, log, value)
		return
	}

	r.mu.Lock()

	u := r.ues.ByCoreRANID(*ind.ENBUES1APID)
	if u == nil || u.Core != a || !u.CoreIDKnown || u.CoreID != *ind.MMEUES1APID {
		r.mu.Unlock()
		logErrorIndication(ctx, sourceMME, log, value)

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

// TS 36.413 clause 10.6
func (r *relay) releaseOnErrorIndication(ctx context.Context, a *mmeAssoc, log *slog.Logger, enbID s1ap.ENBUES1APID,
	cause *s1ap.Cause,
) {
	log = log.With(slog.Uint64("enb_ue_s1ap_id", uint64(enbID)), slog.String("cause", causeText(cause)))

	r.mu.Lock()

	u := r.ues.ByCoreRANID(enbID)
	if u == nil || u.Core != a {
		r.mu.Unlock()
		log.WarnContext(ctx, "Error Indication from MME for an unknown eNB UE S1AP ID")

		return
	}

	gone := r.ues.Remove(u)
	r.mu.Unlock()

	log.WarnContext(ctx, "Error Indication from MME with only the eNB UE S1AP ID, UE context released")
	r.resetRANs(ctx, a.Operator(), gone, causeOr(cause))
}

// TS 36.413 clause 10.6
func (r *relay) releaseOnMMEErrorIndication(ctx context.Context, a *mmeAssoc, log *slog.Logger, mmeID s1ap.MMEUES1APID,
	cause *s1ap.Cause,
) {
	log = log.With(slog.Uint64("mme_ue_s1ap_id", uint64(mmeID)), slog.String("cause", causeText(cause)))

	r.mu.Lock()
	gone := r.ues.Remove(r.ues.ByCoreID(a, mmeID))
	r.mu.Unlock()

	if len(gone) == 0 {
		log.WarnContext(ctx, "Error Indication from MME for an unknown MME UE S1AP ID")
		return
	}

	log.WarnContext(ctx, "Error Indication from MME with only the MME UE S1AP ID, UE context released")
	r.resetRANs(ctx, a.Operator(), gone, *cause)
}

func rewriteRaw(msg []byte, mmeID *s1ap.MMEUES1APID, enbID *s1ap.ENBUES1APID) ([]byte, error) {
	p, err := s1ap.ParseRawPDU(msg)
	if err != nil {
		return nil, err
	}

	return rewrite(p, mmeID, enbID)
}

// TS 36.413 clause 10.6
func releasesConnection(c *s1ap.Cause) bool {
	return c != nil && (c.Equal(causeUnknownMMEUEID) || c.Equal(causeUnknownENBUEID) || c.Equal(causeUnknownPair))
}

// TS 36.413 clause 8.7.2.2
func sendUEErrorIndication(ctx context.Context, peer assoc.Sender, log *slog.Logger, cause s1ap.Cause,
	mmeID *s1ap.MMEUES1APID, enbID *s1ap.ENBUES1APID,
) {
	indicate(ctx, peer, log, ueIDs{mme: mmeID, enb: enbID}, cause, nil)
}

// TS 36.413 clause 8.7.2
func logErrorIndication(ctx context.Context, from string, log *slog.Logger, value []byte) {
	ind, err := s1ap.ParseErrorIndication(value)
	if err != nil {
		log.WarnContext(ctx, "undecodable Error Indication from "+from, slog.Any("error", err))
		return
	}

	log.WarnContext(ctx, "Error Indication from "+from, slog.String("cause", causeText(ind.Cause)),
		slog.Bool("criticality_diagnostics", ind.CriticalityDiagnostics != nil),
		slog.Bool("ue_associated", ind.MMEUES1APID != nil || ind.ENBUES1APID != nil))
}

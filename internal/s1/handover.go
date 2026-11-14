package s1

import (
	"context"
	"log/slog"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/tracing"
)

// TS 36.413 clause 8.4.2.3
// TS 36.413 clause 10.4
func (r *relay) handoverRequest(ctx context.Context, a *mmeAssoc, log *slog.Logger, pdu s1ap.PDU, msg []byte) {
	p, err := s1ap.ParseRawPDU(msg)
	if err != nil {
		undecodable(ctx, a, sourceMME, log, pdu, err)
		return
	}

	mmeID, ok, err := p.MMEUES1APID()
	if err != nil || !ok {
		terminate(ctx, a, sourceMME, log, pdu, msg)
		return
	}

	tracing.SpanFailed(ctx, errTypeUnsupported)
	log.WarnContext(ctx, "HANDOVER REQUEST from MME rejected: handover into the gateway is not supported",
		slog.Uint64("mme_ue_s1ap_id", uint64(mmeID)))
	sendTo(ctx, a, log, "Handover Failure",
		&s1ap.HandoverFailure{MMEUES1APID: &mmeID, Cause: &causeHOFailure}, true)
}

// TS 36.413 clause 8.4.4.3
// TS 36.413 clause 10.4
func (r *relay) pathSwitchRequest(ctx context.Context, n *ranAssoc, log *slog.Logger, pdu s1ap.PDU, msg []byte) {
	p, err := s1ap.ParseRawPDU(msg)
	if err != nil {
		undecodable(ctx, n, sourceRadio, log, pdu, err)
		return
	}

	enbID, okENB, errENB := p.ENBUES1APID()
	mmeID, okMME, errMME := p.SourceMMEUES1APID()

	if errENB != nil || errMME != nil || !okENB || !okMME {
		terminate(ctx, n, sourceRadio, log, pdu, msg)
		return
	}

	tracing.SpanFailed(ctx, errTypeUnsupported)
	log.WarnContext(ctx, "PATH SWITCH REQUEST from radio rejected: path switch through the gateway is not supported",
		slog.Uint64("enb_ue_s1ap_id", uint64(enbID)), slog.Uint64("mme_ue_s1ap_id", uint64(mmeID)))
	sendTo(ctx, n, log, "Path Switch Request Failure", &s1ap.PathSwitchRequestFailure{
		MMEUES1APID: &mmeID, ENBUES1APID: &enbID, Cause: &causeNotCompatibleWithState,
	}, true)
}

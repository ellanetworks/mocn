package ng

import (
	"context"
	"log/slog"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/tracing"
)

// TS 38.413 clause 8.4.2.3
// TS 38.413 clause 10.4
func (r *relay) handoverRequest(ctx context.Context, a *amfAssoc, log *slog.Logger, pdu ngap.PDU, msg []byte) {
	p, err := ngap.ParseRawPDU(msg)
	if err != nil {
		undecodable(ctx, a, sourceAMF, log, pdu, err)
		return
	}

	amfID, ok, err := p.AMFUENGAPID()
	if err != nil || !ok {
		terminate(ctx, a, sourceAMF, log, pdu, msg)
		return
	}

	tracing.SpanFailed(ctx, errTypeUnsupported)
	log.WarnContext(ctx, "HANDOVER REQUEST from AMF rejected: handover into the gateway is not supported",
		slog.Uint64("amf_ue_ngap_id", uint64(amfID)))
	sendTo(ctx, a, log, "Handover Failure",
		&ngap.HandoverFailure{AMFUENGAPID: &amfID, Cause: &causeHOFailure}, true)
}

// TS 38.413 clause 8.4.4.3
// TS 38.413 clause 9.3.4.20
// TS 38.413 clause 10.4
func (r *relay) pathSwitchRequest(ctx context.Context, n *ranAssoc, log *slog.Logger, value []byte) {
	req, err := ngap.ParsePathSwitchRequest(value)
	if err != nil {
		tracing.SpanFailed(ctx, assoc.ErrTypeInvalid)
		log.WarnContext(ctx, "rejected PATH SWITCH REQUEST from radio", slog.Any("error", err))
		indicateParseError(ctx, n, log, ngap.ProcPathSwitchRequest, err)

		return
	}

	transfer, err := (&ngap.PathSwitchRequestUnsuccessfulTransfer{Cause: causeNotCompatibleWithState}).Marshal()
	if err != nil {
		log.ErrorContext(ctx, "failed to marshal Path Switch Request Unsuccessful Transfer", slog.Any("error", err))
		return
	}

	released := make(ngap.PDUSessionResourceReleasedListPSFail, 0, len(req.PDUSessionResourceToBeSwitchedDLList))
	for _, item := range req.PDUSessionResourceToBeSwitchedDLList {
		released = append(released, ngap.PDUSessionResourceReleasedItemPSFail{PDUSessionID: item.PDUSessionID, Transfer: transfer})
	}

	tracing.SpanFailed(ctx, errTypeUnsupported)
	log.WarnContext(ctx, "PATH SWITCH REQUEST from radio rejected: path switch through the gateway is not supported",
		slog.Uint64("ran_ue_ngap_id", uint64(req.RANUENGAPID)), slog.Uint64("amf_ue_ngap_id", uint64(req.SourceAMFUENGAPID)))
	sendTo(ctx, n, log, "Path Switch Request Failure", &ngap.PathSwitchRequestFailure{
		AMFUENGAPID: &req.SourceAMFUENGAPID, RANUENGAPID: &req.RANUENGAPID, PDUSessionResourceReleased: released,
	}, true)
}

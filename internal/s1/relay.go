package s1

import (
	"context"
	"log/slog"

	"github.com/ellanetworks/core/s1ap"
)

type Relay interface {
	FromRAN(ctx context.Context, from *RANAssociation, pdu s1ap.PDU)
	FromMME(ctx context.Context, from *MMEAssociation, pdu s1ap.PDU)
	RANLost(from *RANAssociation)
	MMELost(from *MMEAssociation)
}

type dropRelay struct {
	log *slog.Logger
}

func (r dropRelay) FromRAN(ctx context.Context, from *RANAssociation, pdu s1ap.PDU) {
	r.log.DebugContext(ctx, "dropped S1AP message from RAN node", slog.String("procedure", procedureOf(pdu).String()),
		slog.Any("tnl_addresses", from.addrs))
}

func (r dropRelay) FromMME(ctx context.Context, from *MMEAssociation, pdu s1ap.PDU) {
	r.log.DebugContext(ctx, "dropped S1AP message from MME", slog.String("procedure", procedureOf(pdu).String()),
		slog.Any("tnl_addresses", from.client.addrs))
}

func (dropRelay) RANLost(*RANAssociation) {}

func (dropRelay) MMELost(*MMEAssociation) {}

func procedureOf(pdu s1ap.PDU) s1ap.ProcedureCode {
	switch p := pdu.(type) {
	case *s1ap.InitiatingMessage:
		return p.ProcedureCode
	case *s1ap.SuccessfulOutcome:
		return p.ProcedureCode
	case *s1ap.UnsuccessfulOutcome:
		return p.ProcedureCode
	}

	return 0
}

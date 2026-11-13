package s1

import (
	"context"
	"errors"
	"log/slog"

	"github.com/ellanetworks/core/s1ap"
)

type Relay interface {
	FromRAN(ctx context.Context, from *RANAssociation, pdu s1ap.PDU)
	FromMME(ctx context.Context, from *MMEAssociation, pdu s1ap.PDU)
	RANLost(from *RANAssociation)
	MMELost(from *MMEAssociation)
}

type sender interface {
	Send(msg []byte, stream uint16) error
}

type localRelay struct {
	log *slog.Logger
}

func (r localRelay) FromRAN(ctx context.Context, from *RANAssociation, pdu s1ap.PDU) {
	r.terminate(ctx, from, "radio", r.log.With(slog.Any("tnl_addresses", from.addrs)), pdu)
}

func (r localRelay) FromMME(ctx context.Context, from *MMEAssociation, pdu s1ap.PDU) {
	r.terminate(ctx, from, "MME", r.log.With(slog.Int64("operator_id", from.client.operatorID),
		slog.Any("tnl_addresses", from.client.addrs)), pdu)
}

func (localRelay) RANLost(*RANAssociation) {}

func (localRelay) MMELost(*MMEAssociation) {}

// TS 36.300 clause 4.6.2
func (r localRelay) terminate(ctx context.Context, peer sender, from string, log *slog.Logger, pdu s1ap.PDU) {
	if m, ok := pdu.(*s1ap.InitiatingMessage); ok {
		switch m.ProcedureCode {
		case s1ap.ProcReset:
			acknowledgeReset(ctx, peer, from, log, m.Value)
			return
		case s1ap.ProcErrorIndication:
			logErrorIndication(ctx, from, log, m.Value)
			return
		}
	}

	log.InfoContext(ctx, "dropped S1AP message from "+from, slog.String("procedure", procedureOf(pdu).String()))
}

// TS 36.413 clause 8.7.1
func acknowledgeReset(ctx context.Context, peer sender, from string, log *slog.Logger, value []byte) {
	reset, err := s1ap.ParseReset(value)
	if err != nil {
		log.WarnContext(ctx, "rejected Reset from "+from, slog.Any("error", err))

		if ase, ok := errors.AsType[*s1ap.AbstractSyntaxError](err); ok {
			sendErrorIndication(ctx, peer, log, ase.Cause, ase.ErrorIndicationDiagnostics())
		}

		return
	}

	out, err := (&s1ap.ResetAcknowledge{
		ConnectionList:         reset.ResetType.Part,
		CriticalityDiagnostics: responseDiagnostics(s1ap.ProcReset, reset.Diagnostics()),
	}).Marshal()
	if err != nil {
		log.ErrorContext(ctx, "failed to marshal Reset Acknowledge", slog.Any("error", err))
		return
	}

	if err := peer.Send(out, nonUEStream); err != nil {
		log.WarnContext(ctx, "failed to send Reset Acknowledge", slog.Any("error", err))
		return
	}

	log.InfoContext(ctx, "Reset from "+from+" acknowledged", slog.String("cause", causeText(reset.Cause)),
		slog.Bool("all", reset.ResetType.All), slog.Int("connections", len(reset.ResetType.Part)))
}

// TS 36.413 clause 10.3.4.2
func sendErrorIndication(ctx context.Context, peer sender, log *slog.Logger, cause s1ap.Cause,
	diag s1ap.CriticalityDiagnostics,
) {
	out, err := (&s1ap.ErrorIndication{Cause: &cause, CriticalityDiagnostics: &diag}).Marshal()
	if err != nil {
		log.ErrorContext(ctx, "failed to marshal Error Indication", slog.Any("error", err))
		return
	}

	if err := peer.Send(out, nonUEStream); err != nil {
		log.WarnContext(ctx, "failed to send Error Indication", slog.Any("error", err))
	}
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

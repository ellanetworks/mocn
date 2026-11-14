package s1

import (
	"context"
	"errors"
	"log/slog"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/tracing"
)

const errTypeUnsupported = "unsupported"

var (
	causeTransferSyntax = s1ap.Cause{Group: s1ap.CauseGroupProtocol, Value: s1ap.CauseProtocolTransferSyntaxError}
	causeAbstractReject = s1ap.Cause{Group: s1ap.CauseGroupProtocol, Value: s1ap.CauseProtocolAbstractSyntaxErrorReject}
	causeAbstractNotify = s1ap.Cause{
		Group: s1ap.CauseGroupProtocol, Value: s1ap.CauseProtocolAbstractSyntaxErrorIgnoreAndNotify,
	}
)

type sendable interface {
	Marshal() ([]byte, error)
}

func triggerOf(pdu s1ap.PDU) s1ap.TriggeringMessage {
	switch pdu.(type) {
	case *s1ap.SuccessfulOutcome:
		return s1ap.TriggeringSuccessfulOutcome
	case *s1ap.UnsuccessfulOutcome:
		return s1ap.TriggeringUnsuccessfulOutcome
	}

	return s1ap.TriggeringInitiatingMessage
}

func idsOf(msg []byte) ueIDs {
	p, err := s1ap.ParseRawPDU(msg)
	if err != nil {
		return ueIDs{}
	}

	ids, _, _ := readUEIDs(p)

	return ids
}

func diagnosticsOf(pdu s1ap.PDU, crit *s1ap.Criticality) *s1ap.CriticalityDiagnostics {
	proc, trigger := procedureOf(pdu), triggerOf(pdu)

	return &s1ap.CriticalityDiagnostics{ProcedureCode: &proc, TriggeringMessage: &trigger, ProcedureCriticality: crit}
}

func sendTo(ctx context.Context, peer assoc.Sender, log *slog.Logger, what string, msg sendable, ues bool) {
	out, err := msg.Marshal()
	if err != nil {
		log.ErrorContext(ctx, "failed to marshal "+what, slog.Any("error", err))
		return
	}

	stream := uint16(assoc.NonUEStream)
	if ues {
		stream = ueStream
	}

	if err := peer.Send(ctx, out, stream); err != nil {
		log.WarnContext(ctx, "failed to send "+what, slog.Any("error", err))
	}
}

// TS 36.413 clause 8.7.2.2
func indicate(ctx context.Context, peer assoc.Sender, log *slog.Logger, ids ueIDs, cause s1ap.Cause,
	diag *s1ap.CriticalityDiagnostics,
) {
	sendTo(ctx, peer, log, "Error Indication", &s1ap.ErrorIndication{
		MMEUES1APID: ids.mme, ENBUES1APID: ids.enb, Cause: &cause, CriticalityDiagnostics: diag,
	}, ids.mme != nil || ids.enb != nil)
}

// TS 36.413 clause 10.2
// TS 36.413 clause 10.5
func indicateTransferSyntax(ctx context.Context, peer assoc.Sender, log *slog.Logger, pdu s1ap.PDU) {
	var diag *s1ap.CriticalityDiagnostics

	if pdu != nil {
		if _, ok := pdu.(*s1ap.InitiatingMessage); ok && procedureOf(pdu) == s1ap.ProcErrorIndication {
			return
		}

		diag = diagnosticsOf(pdu, nil)
	}

	indicate(ctx, peer, log, ueIDs{}, causeTransferSyntax, diag)
}

func undecodable(ctx context.Context, peer assoc.Sender, from string, log *slog.Logger, pdu s1ap.PDU, err error) {
	tracing.SpanFailed(ctx, assoc.ErrTypeInvalid)
	log.WarnContext(ctx, "undecodable S1AP message from "+from, slog.Any("error", err))
	indicateTransferSyntax(ctx, peer, log, pdu)
}

// TS 36.413 clause 10.2
// TS 36.413 clause 10.3.4.2
// TS 36.413 clause 10.3.5
// TS 36.413 clause 10.3.6
func indicateParseError(ctx context.Context, peer assoc.Sender, log *slog.Logger, proc s1ap.ProcedureCode, err error) {
	if ase, ok := errors.AsType[*s1ap.AbstractSyntaxError](err); ok {
		mmeID, enbID := ase.UEIDs()
		diag := ase.ErrorIndicationDiagnostics()
		indicate(ctx, peer, log, ueIDs{mme: mmeID, enb: enbID}, ase.Cause, &diag)

		return
	}

	indicateTransferSyntax(ctx, peer, log, &s1ap.InitiatingMessage{ProcedureCode: proc})
}

func codecError(err error) bool {
	_, ase := errors.AsType[*s1ap.AbstractSyntaxError](err)
	_, tse := errors.AsType[*s1ap.TransferSyntaxError](err)

	return ase || tse
}

func failureCause(err error) (s1ap.Cause, *s1ap.CriticalityDiagnostics) {
	if ase, ok := errors.AsType[*s1ap.AbstractSyntaxError](err); ok {
		diag := ase.OutcomeDiagnostics()
		return ase.Cause, &diag
	}

	return causeTransferSyntax, nil
}

// TS 36.413 clause 10.3.2
// TS 36.413 clause 10.3.4.1
// TS 36.300 clause 4.6.2
func terminate(ctx context.Context, peer assoc.Sender, from string, log *slog.Logger, pdu s1ap.PDU, msg []byte) {
	proc := procedureOf(pdu)

	m, ok := pdu.(*s1ap.InitiatingMessage)
	if !ok || m.Criticality == s1ap.CriticalityIgnore {
		log.InfoContext(ctx, "dropped S1AP message from "+from, slog.String("procedure", proc.String()))
		return
	}

	cause := causeAbstractReject
	if m.Criticality == s1ap.CriticalityNotify {
		cause = causeAbstractNotify
	}

	tracing.SpanFailed(ctx, errTypeUnsupported)
	log.WarnContext(ctx, "unsupported S1AP procedure from "+from+", Error Indication sent",
		slog.String("procedure", proc.String()), slog.String("criticality", m.Criticality.String()))
	indicate(ctx, peer, log, idsOf(msg), cause, diagnosticsOf(pdu, &m.Criticality))
}

// TS 36.413 clause 10.4
// TS 36.413 clause 10.5
func notCompatible(ctx context.Context, peer assoc.Sender, from string, log *slog.Logger, pdu s1ap.PDU, msg []byte) {
	proc := procedureOf(pdu)
	log = log.With(slog.String("procedure", proc.String()))

	tracing.SpanFailed(ctx, assoc.ErrTypeNoSetup)

	m, ok := pdu.(*s1ap.InitiatingMessage)
	if !ok {
		log.DebugContext(ctx, "dropped S1AP message from "+from+" before S1 Setup")
		return
	}

	if proc == s1ap.ProcErrorIndication {
		logErrorIndication(ctx, from, log, m.Value)
		return
	}

	ids := idsOf(msg)

	if out := failureFor(proc, ids, causeNotCompatibleWithState); out != nil {
		log.WarnContext(ctx, "S1AP request from "+from+" before S1 Setup rejected")
		sendTo(ctx, peer, log, "failure message", out, ids.mme != nil || ids.enb != nil)

		return
	}

	log.WarnContext(ctx, "S1AP message from "+from+" before S1 Setup, Error Indication sent")
	indicate(ctx, peer, log, ids, causeNotCompatibleWithState, diagnosticsOf(pdu, &m.Criticality))
}

// TS 36.413 clause 10.4
func failureFor(proc s1ap.ProcedureCode, ids ueIDs, cause s1ap.Cause) sendable {
	switch proc {
	case s1ap.ProcHandoverPreparation:
		if ids.mme != nil && ids.enb != nil {
			return &s1ap.HandoverPreparationFailure{MMEUES1APID: ids.mme, ENBUES1APID: ids.enb, Cause: &cause}
		}
	case s1ap.ProcHandoverResourceAllocation:
		if ids.mme != nil {
			return &s1ap.HandoverFailure{MMEUES1APID: ids.mme, Cause: &cause}
		}
	case s1ap.ProcInitialContextSetup:
		if ids.mme != nil && ids.enb != nil {
			return &s1ap.InitialContextSetupFailure{MMEUES1APID: ids.mme, ENBUES1APID: ids.enb, Cause: &cause}
		}
	case s1ap.ProcUEContextModification:
		if ids.mme != nil && ids.enb != nil {
			return &s1ap.UEContextModificationFailure{MMEUES1APID: ids.mme, ENBUES1APID: ids.enb, Cause: &cause}
		}
	case s1ap.ProcENBConfigurationUpdate:
		return &s1ap.ENBConfigurationUpdateFailure{Cause: &cause}
	case s1ap.ProcMMEConfigurationUpdate:
		return &s1ap.MMEConfigurationUpdateFailure{Cause: &cause}
	}

	return nil
}

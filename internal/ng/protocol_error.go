package ng

import (
	"context"
	"errors"
	"log/slog"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/tracing"
)

const errTypeUnsupported = "unsupported"

var (
	causeTransferSyntax = ngap.CauseProtocolTransferSyntaxError.Cause()
	causeAbstractReject = ngap.CauseProtocolAbstractSyntaxErrorReject.Cause()
	causeAbstractNotify = ngap.CauseProtocolAbstractSyntaxErrorIgnoreAndNotify.Cause()
)

type sendable interface {
	Marshal() ([]byte, error)
}

func triggerOf(pdu ngap.PDU) ngap.TriggeringMessage {
	switch pdu.(type) {
	case *ngap.SuccessfulOutcome:
		return ngap.TriggeringSuccessfulOutcome
	case *ngap.UnsuccessfulOutcome:
		return ngap.TriggeringUnsuccessfulOutcome
	}

	return ngap.TriggeringInitiatingMessage
}

func idsOf(msg []byte) ueIDs {
	p, err := ngap.ParseRawPDU(msg)
	if err != nil {
		return ueIDs{}
	}

	ids, _, _ := readUEIDs(p)

	return ids
}

func diagnosticsOf(pdu ngap.PDU, crit *ngap.Criticality) *ngap.CriticalityDiagnostics {
	proc, trigger := procedureOf(pdu), triggerOf(pdu)

	return &ngap.CriticalityDiagnostics{ProcedureCode: &proc, TriggeringMessage: &trigger, ProcedureCriticality: crit}
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

// TS 38.413 clause 8.7.2.2
func indicate(ctx context.Context, peer assoc.Sender, log *slog.Logger, ids ueIDs, cause ngap.Cause,
	diag *ngap.CriticalityDiagnostics,
) {
	sendTo(ctx, peer, log, "Error Indication", &ngap.ErrorIndication{
		AMFUENGAPID: ids.amf, RANUENGAPID: ids.ran, Cause: &cause, CriticalityDiagnostics: diag,
	}, ids.amf != nil || ids.ran != nil)
}

// TS 38.413 clause 10.2
// TS 38.413 clause 10.5
func indicateTransferSyntax(ctx context.Context, peer assoc.Sender, log *slog.Logger, pdu ngap.PDU) {
	var diag *ngap.CriticalityDiagnostics

	if pdu != nil {
		if _, ok := pdu.(*ngap.InitiatingMessage); ok && procedureOf(pdu) == ngap.ProcErrorIndication {
			return
		}

		diag = diagnosticsOf(pdu, nil)
	}

	indicate(ctx, peer, log, ueIDs{}, causeTransferSyntax, diag)
}

// TS 38.413 clause 10.3.4.2
// TS 38.413 clause 10.3.5
func indicateNotify(ctx context.Context, peer assoc.Sender, log *slog.Logger, proc ngap.ProcedureCode,
	trigger ngap.TriggeringMessage, d ngap.Diagnostics,
) {
	indicate(ctx, peer, log, ueIDs{}, causeAbstractNotify, &ngap.CriticalityDiagnostics{
		ProcedureCode: &proc, TriggeringMessage: &trigger, ProcedureCriticality: ngap.Ptr(ngap.ProcedureCriticality(proc)),
		IEsCriticalityDiagnostics: d.Report(),
	})
}

func undecodable(ctx context.Context, peer assoc.Sender, from string, log *slog.Logger, pdu ngap.PDU, err error) {
	tracing.SpanFailed(ctx, assoc.ErrTypeInvalid)
	log.WarnContext(ctx, "undecodable NGAP message from "+from, slog.Any("error", err))
	indicateTransferSyntax(ctx, peer, log, pdu)
}

// TS 38.413 clause 10.2
// TS 38.413 clause 10.3.4.2
// TS 38.413 clause 10.3.5
// TS 38.413 clause 10.3.6
func indicateParseError(ctx context.Context, peer assoc.Sender, log *slog.Logger, proc ngap.ProcedureCode, err error) {
	if ase, ok := errors.AsType[*ngap.AbstractSyntaxError](err); ok {
		amfID, ranID := ase.UEIDs()
		diag := ase.ErrorIndicationDiagnostics()
		indicate(ctx, peer, log, ueIDs{amf: amfID, ran: ranID}, ase.Cause, &diag)

		return
	}

	indicateTransferSyntax(ctx, peer, log, &ngap.InitiatingMessage{ProcedureCode: proc})
}

func codecError(err error) bool {
	_, ase := errors.AsType[*ngap.AbstractSyntaxError](err)
	_, tse := errors.AsType[*ngap.TransferSyntaxError](err)

	return ase || tse
}

func failureCause(err error) (ngap.Cause, *ngap.CriticalityDiagnostics) {
	if ase, ok := errors.AsType[*ngap.AbstractSyntaxError](err); ok {
		diag := ase.OutcomeDiagnostics()
		return ase.Cause, &diag
	}

	return causeTransferSyntax, nil
}

// TS 38.413 clause 10.3.2
// TS 38.413 clause 10.3.4.1
// TS 38.300 clause 4.10.2.3
func terminate(ctx context.Context, peer assoc.Sender, from string, log *slog.Logger, pdu ngap.PDU, msg []byte) {
	proc := procedureOf(pdu)

	m, ok := pdu.(*ngap.InitiatingMessage)
	if !ok || m.Criticality == ngap.CriticalityIgnore {
		log.InfoContext(ctx, "dropped NGAP message from "+from, slog.String("procedure", proc.String()))
		return
	}

	cause := causeAbstractReject
	if m.Criticality == ngap.CriticalityNotify {
		cause = causeAbstractNotify
	}

	tracing.SpanFailed(ctx, errTypeUnsupported)
	log.WarnContext(ctx, "unsupported NGAP procedure from "+from+", Error Indication sent",
		slog.String("procedure", proc.String()), slog.String("criticality", m.Criticality.String()))
	indicate(ctx, peer, log, idsOf(msg), cause, diagnosticsOf(pdu, &m.Criticality))
}

// TS 38.413 clause 10.4
// TS 38.413 clause 10.5
func notCompatible(ctx context.Context, peer assoc.Sender, from string, log *slog.Logger, pdu ngap.PDU, msg []byte) {
	proc := procedureOf(pdu)
	log = log.With(slog.String("procedure", proc.String()))

	tracing.SpanFailed(ctx, assoc.ErrTypeNoSetup)

	m, ok := pdu.(*ngap.InitiatingMessage)
	if !ok {
		log.DebugContext(ctx, "dropped NGAP message from "+from+" before NG Setup")
		return
	}

	if proc == ngap.ProcErrorIndication {
		logErrorIndication(ctx, from, log, m.Value)
		return
	}

	ids := idsOf(msg)

	if out := failureFor(proc, ids, causeNotCompatibleWithState); out != nil {
		log.WarnContext(ctx, "NGAP request from "+from+" before NG Setup rejected")
		sendTo(ctx, peer, log, "failure message", out, ids.amf != nil || ids.ran != nil)

		return
	}

	log.WarnContext(ctx, "NGAP message from "+from+" before NG Setup, Error Indication sent")
	indicate(ctx, peer, log, ids, causeNotCompatibleWithState, diagnosticsOf(pdu, &m.Criticality))
}

// TS 38.413 clause 10.4
func failureFor(proc ngap.ProcedureCode, ids ueIDs, cause ngap.Cause) sendable {
	switch proc {
	case ngap.ProcHandoverPreparation:
		if ids.amf != nil && ids.ran != nil {
			return &ngap.HandoverPreparationFailure{AMFUENGAPID: ids.amf, RANUENGAPID: ids.ran, Cause: &cause}
		}
	case ngap.ProcHandoverResourceAllocation:
		if ids.amf != nil {
			return &ngap.HandoverFailure{AMFUENGAPID: ids.amf, Cause: &cause}
		}
	case ngap.ProcInitialContextSetup:
		if ids.amf != nil && ids.ran != nil {
			return &ngap.InitialContextSetupFailure{AMFUENGAPID: ids.amf, RANUENGAPID: ids.ran, Cause: &cause}
		}
	case ngap.ProcUEContextModification:
		if ids.amf != nil && ids.ran != nil {
			return &ngap.UEContextModificationFailure{AMFUENGAPID: ids.amf, RANUENGAPID: ids.ran, Cause: &cause}
		}
	case ngap.ProcRANConfigurationUpdate:
		return &ngap.RANConfigurationUpdateFailure{Cause: &cause}
	case ngap.ProcAMFConfigurationUpdate:
		return &ngap.AMFConfigurationUpdateFailure{Cause: &cause}
	}

	return nil
}

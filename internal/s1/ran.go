package s1

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/tracing"
)

var (
	causeUnknownPLMN            = s1ap.CauseMiscUnknownPLMN.Cause()
	causeMiscUnspecified        = s1ap.CauseMiscUnspecified.Cause()
	causeSemanticError          = s1ap.CauseProtocolSemanticError.Cause()
	causeNotCompatibleWithState = s1ap.CauseProtocolMessageNotCompatible.Cause()
	causeTransportUnavailable   = s1ap.CauseTransportResourceUnavailable.Cause()
)

const noMMETimeToWait = s1ap.TimeToWaitV5s

func ranNodeLocked(n *ranAssoc) RANNode {
	r := RANNode{
		State:          n.StateLocked(),
		ConnectedSince: n.ConnectedAt(),
		TNLAddresses:   n.TNLAddresses(),
		SupportedTAs:   []TA{},
	}

	s := n.Setup

	if f := n.Failure; r.State == RANNodeSetupFailed {
		r.SetupFailure = &SetupFailure{Cause: f.Cause, Reason: f.Reason, At: f.At}
		s = f.Request
	}

	if s != nil {
		id := s.globalENBID
		r.GlobalENBID = &id
		r.Name = s.name
		r.SupportedTAs = slices.Clone(s.tas)
		r.DefaultPagingDRX = pagingDRXFrames(s.drx)
	}

	return r
}

func (p protocol) RadioLost(ctx context.Context, n *ranAssoc) {
	p.relay.ranLost(ctx, n, causeTransportUnavailable)
}

func (p protocol) HandleRadio(ctx context.Context, n *ranAssoc, msg []byte) {
	pdu, err := s1ap.Unmarshal(msg)

	ctx, span := p.receiveFromRAN(ctx, n, pdu, msg)
	defer span.End()

	if err != nil {
		undecodable(ctx, n, sourceRadio, n.Logger(p.log), nil, err)
		return
	}

	switch m := pdu.(type) {
	case *s1ap.InitiatingMessage:
		switch m.ProcedureCode {
		case s1ap.ProcS1Setup:
			p.handleS1Setup(ctx, n, m.Value)
			return
		case s1ap.ProcENBConfigurationUpdate:
			p.handleENBConfigurationUpdate(ctx, n, m.Value)
			return
		}
	case *s1ap.SuccessfulOutcome:
		if m.ProcedureCode == s1ap.ProcMMEConfigurationUpdate {
			p.core.RadioUpdateAcknowledged(ctx, n)
			return
		}
	case *s1ap.UnsuccessfulOutcome:
		if m.ProcedureCode == s1ap.ProcMMEConfigurationUpdate {
			p.handleMMEConfigurationUpdateFailure(ctx, n, m.Value)
			return
		}
	}

	if !p.core.IsUp(n) {
		notCompatible(ctx, n, sourceRadio, n.Logger(p.log), pdu, msg)
		return
	}

	p.relay.fromRAN(ctx, n, pdu, msg)
}

func ranSetupFrom(req *s1ap.S1SetupRequest) (ranSetup, error) {
	id, err := globalENBIDFrom(req.GlobalENBID)
	if err != nil {
		return ranSetup{}, err
	}

	tas, err := tasFrom(req.SupportedTAs)
	if err != nil {
		return ranSetup{}, err
	}

	if req.DefaultPagingDRX == nil {
		return ranSetup{}, errors.New("missing Default Paging DRX")
	}

	s := ranSetup{globalENBID: id, tas: tas, drx: *req.DefaultPagingDRX}
	if req.ENBName != nil {
		s.name = *req.ENBName
	}

	return s, nil
}

// TS 36.413 clause 8.7.3
func (p protocol) handleS1Setup(ctx context.Context, n *ranAssoc, value []byte) {
	if p.core.TakeDown(n) {
		p.relay.ranLost(ctx, n, causeEUTRANRelease)
	}

	req, err := s1ap.ParseS1SetupRequest(value)
	if err != nil {
		cause, diag := failureCause(err)
		p.rejectS1Setup(ctx, n, nil, assoc.SetupInvalid, cause, nil, diag, err.Error())

		return
	}

	setup, err := ranSetupFrom(req)
	if err != nil {
		p.rejectS1Setup(ctx, n, nil, assoc.SetupInvalid, causeSemanticError, nil, nil, err.Error())
		return
	}

	serving, err := p.core.BeginSetup(ctx, n, setup)

	switch {
	case errors.Is(err, assoc.ErrNodeInUse):
		p.rejectS1Setup(ctx, n, &setup, assoc.SetupInvalid, causeSemanticError, nil, nil,
			"the Global eNB ID "+setup.Ref()+" is used by another radio")

		return
	case err != nil:
		p.rejectS1Setup(ctx, n, &setup, assoc.SetupUnknownPLMN, causeUnknownPLMN, nil, nil, err.Error())
		return
	}

	n.Logger(p.log).InfoContext(ctx, "S1 Setup Request", slog.String("enb_name", setup.name))

	accept := func(served servedGUMMEIs) error { return p.acceptS1Setup(ctx, n, served, req.Diagnostics()) }
	failed := func(info mmeInfo) bool { return info.cause != nil }

	switch outcome, failures := p.core.AwaitCore(ctx, n, serving, accept, failed); outcome {
	case assoc.AwaitCoreFailed:
		cause, ttw := setupFailure(failures)
		p.rejectS1Setup(ctx, n, &setup, names.CoreFailure, cause, ttw, nil, "the operator's MMEs refused the setup: "+causeLabel(cause))
	case assoc.AwaitNoCore:
		p.rejectS1Setup(ctx, n, &setup, names.NoCore, causeTransportUnavailable, s1ap.Ptr(noMMETimeToWait), nil,
			"no MME of the serving operators is up")
	case assoc.AwaitDone:
	}
}

func setupFailure(failures []mmeInfo) (s1ap.Cause, *s1ap.TimeToWait) {
	cause := *failures[0].cause

	var ttw *s1ap.TimeToWait

	for _, f := range failures {
		if f.ttw != nil && (ttw == nil || *f.ttw > *ttw) {
			ttw = f.ttw
		}
	}

	return cause, ttw
}

// TS 36.413 clause 9.1.8.5
func (p protocol) acceptS1Setup(ctx context.Context, n *ranAssoc, served servedGUMMEIs, d s1ap.Diagnostics) error {
	gummeis, err := gummeisTo(served)
	if err != nil {
		return fmt.Errorf("encode Served GUMMEIs: %w", err)
	}

	out, err := (&s1ap.S1SetupResponse{
		MMEName:                s1ap.Ptr(assoc.NodeName),
		ServedGUMMEIs:          gummeis,
		RelativeMMECapacity:    s1ap.Ptr(relativeMMECapacity),
		CriticalityDiagnostics: responseDiagnostics(s1ap.ProcS1Setup, d),
	}).Marshal()
	if err != nil {
		return fmt.Errorf("marshal S1 Setup Response: %w", err)
	}

	if err := n.Send(ctx, out, assoc.NonUEStream); err != nil {
		return fmt.Errorf("send S1 Setup Response: %w", err)
	}

	return nil
}

func (p protocol) RadioUpLocked(ctx context.Context, n *ranAssoc) {
	n.Logger(p.log).InfoContext(ctx, "radio S1 Setup accepted", slog.String("enb_name", n.Setup.name))

	p.replayOverloadLocked(ctx, n, nil)
}

func (p protocol) rejectS1Setup(ctx context.Context, n *ranAssoc, req *ranSetup, result string, cause s1ap.Cause,
	ttw *s1ap.TimeToWait, diag *s1ap.CriticalityDiagnostics, reason string,
) {
	p.core.SetupRejected(ctx, n, result, assoc.RadioSetupFailure[ranSetup]{
		Request: req, Cause: causeLabel(cause), Reason: reason,
	})

	n.Logger(p.log).WarnContext(ctx, "radio S1 Setup rejected", slog.String("cause", cause.String()),
		slog.String("reason", reason))

	out, err := (&s1ap.S1SetupFailure{Cause: &cause, TimeToWait: ttw, CriticalityDiagnostics: diag}).Marshal()
	if err != nil {
		p.log.ErrorContext(ctx, "failed to marshal S1 Setup Failure", slog.Any("error", err))
		return
	}

	if err := n.Send(ctx, out, assoc.NonUEStream); err != nil {
		p.log.WarnContext(ctx, "failed to send S1 Setup Failure", slog.Any("error", err))
	}
}

// TS 36.413 clause 8.7.4
// TS 36.413 clause 8.7.6
func (p protocol) handleENBConfigurationUpdate(ctx context.Context, n *ranAssoc, value []byte) {
	if !p.core.IsUp(n) {
		p.rejectENBConfigurationUpdate(ctx, n, assoc.ErrTypeNoSetup, causeNotCompatibleWithState, nil, "no S1 Setup")
		return
	}

	upd, err := s1ap.ParseENBConfigurationUpdate(value)
	if err != nil {
		cause, diag := failureCause(err)
		p.rejectENBConfigurationUpdate(ctx, n, assoc.ErrTypeInvalid, cause, diag, err.Error())

		return
	}

	var tas []TA

	if len(upd.SupportedTAs) > 0 {
		if tas, err = tasFrom(upd.SupportedTAs); err != nil {
			p.rejectENBConfigurationUpdate(ctx, n, assoc.ErrTypeInvalid, causeSemanticError, nil, err.Error())
			return
		}
	}

	out, err := (&s1ap.ENBConfigurationUpdateAcknowledge{
		CriticalityDiagnostics: responseDiagnostics(s1ap.ProcENBConfigurationUpdate, upd.Diagnostics()),
	}).Marshal()
	if err != nil {
		p.log.ErrorContext(ctx, "failed to marshal eNB Configuration Update Acknowledge", slog.Any("error", err))
		return
	}

	var name string

	update := func(s ranSetup) (ranSetup, bool) {
		if upd.ENBName != nil {
			s.name = *upd.ENBName
		}

		if tas != nil {
			s.tas = tas
		}

		if upd.DefaultPagingDRX != nil {
			s.drx = *upd.DefaultPagingDRX
		}

		name = s.name

		return s, tas != nil
	}

	commit := func(added []int64) {
		if err := n.Send(ctx, out, assoc.NonUEStream); err != nil {
			n.Logger(p.log).WarnContext(ctx, "failed to send eNB Configuration Update Acknowledge", slog.Any("error", err))
		}

		if len(added) > 0 {
			p.replayOverloadLocked(ctx, n, added)
		}
	}

	switch err := p.core.Reconfigure(ctx, n, update, commit); {
	case errors.Is(err, assoc.ErrNoOperator):
		p.rejectENBConfigurationUpdate(ctx, n, assoc.SetupUnknownPLMN, causeUnknownPLMN, nil, err.Error())
	case err != nil:
		p.rejectENBConfigurationUpdate(ctx, n, assoc.ErrTypeNoSetup, causeNotCompatibleWithState, nil, err.Error())
	default:
		n.Logger(p.log).InfoContext(ctx, "radio configuration updated", slog.String("enb_name", name))
	}
}

func (p protocol) rejectENBConfigurationUpdate(ctx context.Context, n *ranAssoc, errType string,
	cause s1ap.Cause, diag *s1ap.CriticalityDiagnostics, reason string,
) {
	tracing.SpanFailed(ctx, errType)

	n.Logger(p.log).WarnContext(ctx, "radio eNB Configuration Update rejected", slog.String("cause", cause.String()),
		slog.String("reason", reason))

	out, err := (&s1ap.ENBConfigurationUpdateFailure{Cause: &cause, CriticalityDiagnostics: diag}).Marshal()
	if err != nil {
		p.log.ErrorContext(ctx, "failed to marshal eNB Configuration Update Failure", slog.Any("error", err))
		return
	}

	if err := n.Send(ctx, out, assoc.NonUEStream); err != nil {
		p.log.WarnContext(ctx, "failed to send eNB Configuration Update Failure", slog.Any("error", err))
	}
}

// TS 36.413 clause 8.7.5
// TS 36.413 clause 9.1.8.10
func (p protocol) RadioConfigUpdate(ctx context.Context, served servedGUMMEIs) []byte {
	gummeis, err := gummeisTo(served)
	if err != nil {
		p.log.ErrorContext(ctx, "failed to encode Served GUMMEIs", slog.Any("error", err))
		return nil
	}

	out, err := (&s1ap.MMEConfigurationUpdate{MMEName: s1ap.Ptr(assoc.NodeName), ServedGUMMEIs: gummeis}).Marshal()
	if err != nil {
		p.log.ErrorContext(ctx, "failed to marshal MME Configuration Update", slog.Any("error", err))
		return nil
	}

	return out
}

func (protocol) RadioNotifyLocked(_ context.Context, _ *ranAssoc, acked, _ servedGUMMEIs) servedGUMMEIs {
	return acked
}

func (p protocol) handleMMEConfigurationUpdateFailure(ctx context.Context, n *ranAssoc, value []byte) {
	fail, err := s1ap.ParseMMEConfigurationUpdateFailure(value)

	var wait *time.Duration

	if err == nil {
		wait = waitOf(fail.TimeToWait)
	}

	cause := "undecodable"
	if err == nil {
		cause = causeText(fail.Cause)
	} else {
		tracing.SpanFailed(ctx, assoc.ErrTypeInvalid)
	}

	n.Logger(p.log).WarnContext(ctx, "radio rejected MME Configuration Update", slog.String("cause", cause))

	p.core.RadioUpdateRejected(ctx, n, wait)
}

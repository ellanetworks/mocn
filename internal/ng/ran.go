package ng

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/tracing"
)

var (
	causeUnknownPLMN            = ngap.CauseMiscUnknownPLMNOrSNPN.Cause()
	causeMiscUnspecified        = ngap.CauseMiscUnspecified.Cause()
	causeSemanticError          = ngap.CauseProtocolSemanticError.Cause()
	causeNotCompatibleWithState = ngap.CauseProtocolMessageNotCompatible.Cause()
	causeTransportUnavailable   = ngap.CauseTransportResourceUnavailable.Cause()
	causeTransportUnspecified   = ngap.CauseTransportUnspecified.Cause()
)

const noAMFTimeToWait = ngap.TimeToWaitV5s

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
		id := s.globalGNBID
		r.GlobalGNBID = &id
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
	pdu, err := ngap.Unmarshal(msg)

	ctx, span := p.receiveFromRAN(ctx, n, pdu, msg)
	defer span.End()

	if err != nil {
		undecodable(ctx, n, sourceRadio, n.Logger(p.log), nil, err)
		return
	}

	switch m := pdu.(type) {
	case *ngap.InitiatingMessage:
		switch m.ProcedureCode {
		case ngap.ProcNGSetup:
			p.handleNGSetup(ctx, n, m.Value)
			return
		case ngap.ProcRANConfigurationUpdate:
			p.handleRANConfigurationUpdate(ctx, n, m.Value)
			return
		}
	case *ngap.SuccessfulOutcome:
		if m.ProcedureCode == ngap.ProcAMFConfigurationUpdate {
			p.core.RadioUpdateAcknowledged(ctx, n)
			return
		}
	case *ngap.UnsuccessfulOutcome:
		if m.ProcedureCode == ngap.ProcAMFConfigurationUpdate {
			p.handleAMFConfigurationUpdateFailure(ctx, n, m.Value)
			return
		}
	}

	if !p.core.IsUp(n) {
		notCompatible(ctx, n, sourceRadio, n.Logger(p.log), pdu, msg)
		return
	}

	p.relay.fromRAN(ctx, n, pdu, msg)
}

func ranSetupFrom(req *ngap.NGSetupRequest) (ranSetup, error) {
	id, err := globalGNBIDFrom(req.GlobalRANNodeID)
	if err != nil {
		return ranSetup{}, err
	}

	tas, err := tasFrom(req.SupportedTAList)
	if err != nil {
		return ranSetup{}, err
	}

	if req.DefaultPagingDRX == nil {
		return ranSetup{}, errors.New("missing Default Paging DRX")
	}

	s := ranSetup{globalGNBID: id, tas: tas, drx: *req.DefaultPagingDRX}
	if req.RANNodeName != nil {
		s.name = *req.RANNodeName
	}

	return s, nil
}

// TS 38.413 clause 8.7.1
// TS 38.300 clause 4.10.2.3
func (p protocol) handleNGSetup(ctx context.Context, n *ranAssoc, value []byte) {
	if p.core.TakeDown(n) {
		p.relay.ranLost(ctx, n, causeNGRANRelease)
	}

	req, err := ngap.ParseNGSetupRequest(value)
	if err != nil {
		cause, diag := failureCause(err)
		p.rejectNGSetup(ctx, n, nil, assoc.SetupInvalid, cause, nil, diag, err.Error())

		return
	}

	setup, err := ranSetupFrom(req)
	if err != nil {
		p.rejectNGSetup(ctx, n, nil, assoc.SetupInvalid, causeSemanticError, nil, nil, err.Error())
		return
	}

	serving, err := p.core.BeginSetup(ctx, n, setup)

	switch {
	case errors.Is(err, assoc.ErrNodeInUse):
		p.rejectNGSetup(ctx, n, &setup, assoc.SetupInvalid, causeSemanticError, nil, nil,
			"the Global gNB ID "+setup.Ref()+" is used by another radio")

		return
	case err != nil:
		p.rejectNGSetup(ctx, n, &setup, assoc.SetupUnknownPLMN, causeUnknownPLMN, nil, nil, err.Error())
		return
	}

	n.Logger(p.log).InfoContext(ctx, "NG Setup Request", slog.String("ran_node_name", setup.name))

	accept := func(cfg amfConfig) error { return p.acceptNGSetup(ctx, n, cfg, req.Diagnostics()) }
	failed := func(info amfInfo) bool { return info.cause != nil }

	switch outcome, failures := p.core.AwaitCore(ctx, n, serving, accept, failed); outcome {
	case assoc.AwaitCoreFailed:
		cause, ttw := setupFailure(failures)
		p.rejectNGSetup(ctx, n, &setup, names.CoreFailure, cause, ttw, nil, "the operator's AMFs refused the setup: "+causeLabel(cause))
	case assoc.AwaitNoCore:
		p.rejectNGSetup(ctx, n, &setup, names.NoCore, causeTransportUnavailable, ngap.Ptr(noAMFTimeToWait), nil,
			"no AMF of the serving operators is up")
	case assoc.AwaitDone:
	}
}

func setupFailure(failures []amfInfo) (ngap.Cause, *ngap.TimeToWait) {
	cause := *failures[0].cause

	var ttw *ngap.TimeToWait

	for _, f := range failures {
		if f.ttw != nil && (ttw == nil || *f.ttw > *ttw) {
			ttw = f.ttw
		}
	}

	return cause, ttw
}

func (p protocol) acceptNGSetup(ctx context.Context, n *ranAssoc, cfg amfConfig, d ngap.Diagnostics) error {
	out, err := ngSetupResponse(cfg, responseDiagnostics(ngap.ProcNGSetup, d))
	if err != nil {
		return err
	}

	if err := n.Send(ctx, out, assoc.NonUEStream); err != nil {
		return fmt.Errorf("send NG Setup Response: %w", err)
	}

	return nil
}

func (p protocol) RadioUpLocked(ctx context.Context, n *ranAssoc) {
	n.Logger(p.log).InfoContext(ctx, "radio NG Setup accepted", slog.String("ran_node_name", n.Setup.name))

	p.replayOverloadLocked(ctx, n, nil)
}

// TS 38.413 clause 9.2.6.2
func ngSetupResponse(cfg amfConfig, diag *ngap.CriticalityDiagnostics) ([]byte, error) {
	served, err := servedGUAMIsTo(cfg.served)
	if err != nil {
		return nil, err
	}

	support, err := plmnSupportTo(cfg.support)
	if err != nil {
		return nil, err
	}

	out, err := (&ngap.NGSetupResponse{
		AMFName:                assoc.NodeName,
		ServedGUAMIList:        served,
		RelativeAMFCapacity:    ngap.Ptr(relativeAMFCapacity),
		PLMNSupportList:        support,
		CriticalityDiagnostics: diag,
	}).Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal NG Setup Response: %w", err)
	}

	return out, nil
}

func (p protocol) rejectNGSetup(ctx context.Context, n *ranAssoc, req *ranSetup, result string, cause ngap.Cause,
	ttw *ngap.TimeToWait, diag *ngap.CriticalityDiagnostics, reason string,
) {
	p.core.SetupRejected(ctx, n, result, assoc.RadioSetupFailure[ranSetup]{
		Request: req, Cause: causeLabel(cause), Reason: reason,
	})

	n.Logger(p.log).WarnContext(ctx, "radio NG Setup rejected", slog.String("cause", cause.String()),
		slog.String("reason", reason))

	out, err := (&ngap.NGSetupFailure{Cause: &cause, TimeToWait: ttw, CriticalityDiagnostics: diag}).Marshal()
	if err != nil {
		p.log.ErrorContext(ctx, "failed to marshal NG Setup Failure", slog.Any("error", err))
		return
	}

	if err := n.Send(ctx, out, assoc.NonUEStream); err != nil {
		p.log.WarnContext(ctx, "failed to send NG Setup Failure", slog.Any("error", err))
	}
}

// TS 38.413 clause 8.7.2
// TS 38.413 clause 8.7.7
func (p protocol) handleRANConfigurationUpdate(ctx context.Context, n *ranAssoc, value []byte) {
	if !p.core.IsUp(n) {
		p.rejectRANConfigurationUpdate(ctx, n, assoc.ErrTypeNoSetup, causeNotCompatibleWithState, nil, "no NG Setup")
		return
	}

	upd, err := ngap.ParseRANConfigurationUpdate(value)
	if err != nil {
		cause, diag := failureCause(err)
		p.rejectRANConfigurationUpdate(ctx, n, assoc.ErrTypeInvalid, cause, diag, err.Error())

		return
	}

	var tas []TA

	if len(upd.SupportedTAList) > 0 {
		if tas, err = tasFrom(upd.SupportedTAList); err != nil {
			p.rejectRANConfigurationUpdate(ctx, n, assoc.ErrTypeInvalid, causeSemanticError, nil, err.Error())
			return
		}
	}

	out, err := (&ngap.RANConfigurationUpdateAcknowledge{
		CriticalityDiagnostics: responseDiagnostics(ngap.ProcRANConfigurationUpdate, upd.Diagnostics()),
	}).Marshal()
	if err != nil {
		p.log.ErrorContext(ctx, "failed to marshal RAN Configuration Update Acknowledge", slog.Any("error", err))
		return
	}

	var name string

	update := func(s ranSetup) (ranSetup, bool) {
		if upd.RANNodeName != nil {
			s.name = *upd.RANNodeName
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
			n.Logger(p.log).WarnContext(ctx, "failed to send RAN Configuration Update Acknowledge", slog.Any("error", err))
		}

		if len(added) > 0 {
			p.replayOverloadLocked(ctx, n, added)
		}
	}

	switch err := p.core.Reconfigure(ctx, n, update, commit); {
	case errors.Is(err, assoc.ErrNoOperator):
		p.rejectRANConfigurationUpdate(ctx, n, assoc.SetupUnknownPLMN, causeUnknownPLMN, nil, err.Error())
	case err != nil:
		p.rejectRANConfigurationUpdate(ctx, n, assoc.ErrTypeNoSetup, causeNotCompatibleWithState, nil, err.Error())
	default:
		n.Logger(p.log).InfoContext(ctx, "radio configuration updated", slog.String("ran_node_name", name))
	}
}

func (p protocol) rejectRANConfigurationUpdate(ctx context.Context, n *ranAssoc, errType string,
	cause ngap.Cause, diag *ngap.CriticalityDiagnostics, reason string,
) {
	tracing.SpanFailed(ctx, errType)

	n.Logger(p.log).WarnContext(ctx, "radio RAN Configuration Update rejected", slog.String("cause", cause.String()),
		slog.String("reason", reason))

	out, err := (&ngap.RANConfigurationUpdateFailure{Cause: &cause, CriticalityDiagnostics: diag}).Marshal()
	if err != nil {
		p.log.ErrorContext(ctx, "failed to marshal RAN Configuration Update Failure", slog.Any("error", err))
		return
	}

	if err := n.Send(ctx, out, assoc.NonUEStream); err != nil {
		p.log.WarnContext(ctx, "failed to send RAN Configuration Update Failure", slog.Any("error", err))
	}
}

// TS 38.413 clause 9.2.6.7
func buildAMFConfigurationUpdate(cfg amfConfig) ([]byte, error) {
	served, err := servedGUAMIsTo(cfg.served)
	if err != nil {
		return nil, err
	}

	support, err := plmnSupportTo(cfg.support)
	if err != nil {
		return nil, err
	}

	out, err := (&ngap.AMFConfigurationUpdate{
		AMFName: ngap.Ptr(assoc.NodeName), ServedGUAMIList: served, PLMNSupportList: support,
	}).Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal AMF Configuration Update: %w", err)
	}

	return out, nil
}

// TS 38.413 clause 8.7.3
func (p protocol) RadioConfigUpdate(ctx context.Context, cfg amfConfig) []byte {
	out, err := buildAMFConfigurationUpdate(cfg)
	if err != nil {
		p.log.ErrorContext(ctx, "failed to build AMF Configuration Update", slog.Any("error", err))
		return nil
	}

	return out
}

func (p protocol) handleAMFConfigurationUpdateFailure(ctx context.Context, n *ranAssoc, value []byte) {
	fail, err := ngap.ParseAMFConfigurationUpdateFailure(value)

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

	n.Logger(p.log).WarnContext(ctx, "radio rejected AMF Configuration Update", slog.String("cause", cause))

	p.core.RadioUpdateRejected(ctx, n, wait)
}

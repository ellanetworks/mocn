package s1

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/tracing"
)

type mmeInfo struct {
	name     string
	served   []ServedGUMMEIs
	capacity *uint8
	overload []Overload
	cause    *s1ap.Cause
	ttw      *s1ap.TimeToWait
}

func mmeStatusLocked(c *mmeClient) MMEStatus {
	return MMEStatus{
		State:               c.State,
		Since:               c.Since,
		PrimaryTNLAddress:   c.PrimaryAddressLocked(),
		MMEName:             c.Info.name,
		ServedGUMMEIs:       slices.Clone(c.Info.served),
		RelativeMMECapacity: c.Info.capacity,
		Overload:            slices.Clone(c.Info.overload),
	}
}

func (protocol) Overloaded(info mmeInfo) bool {
	return len(info.overload) > 0
}

func (p protocol) CoreLostLocked(c *mmeClient, a *mmeAssoc) func(ctx context.Context) {
	cleared := c.Info.overload
	radios := p.core.ClientRadiosLocked(c)

	return func(ctx context.Context) {
		p.overloadCleared(ctx, radios, c.Operator(), c.Logger(), cleared)
		p.relay.mmeLost(ctx, a)
	}
}

// TS 36.413 clause 9.1.8.4
func s1SetupRequest(enbID db.GlobalENBID, want enbConfig) ([]byte, error) {
	id, err := globalENBIDTo(enbID)
	if err != nil {
		return nil, err
	}

	tas, err := tasTo(want.tas)
	if err != nil {
		return nil, err
	}

	out, err := (&s1ap.S1SetupRequest{
		GlobalENBID: id, ENBName: s1ap.Ptr(assoc.NodeName), SupportedTAs: tas, DefaultPagingDRX: s1ap.Ptr(want.drx),
	}).Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal S1 Setup Request: %w", err)
	}

	return out, nil
}

// TS 36.413 clause 8.7.4
// TS 36.413 clause 9.1.8.7
func (protocol) ConfigurationUpdate(want, acked enbConfig) ([]byte, error) {
	upd := &s1ap.ENBConfigurationUpdate{ENBName: s1ap.Ptr(assoc.NodeName)}

	if !tasEqual(want.tas, acked.tas) {
		tas, err := tasTo(want.tas)
		if err != nil {
			return nil, err
		}

		upd.SupportedTAs = tas
	}

	if want.drx != acked.drx {
		upd.DefaultPagingDRX = s1ap.Ptr(want.drx)
	}

	out, err := upd.Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal eNB Configuration Update: %w", err)
	}

	return out, nil
}

func (p protocol) HandleCore(ctx context.Context, s *mmeSession, msg []byte) error {
	pdu, err := s1ap.Unmarshal(msg)

	ctx, span := p.receiveFromMME(ctx, s.Core(), pdu, msg)
	defer span.End()

	log := s.Client().Logger()

	if err != nil {
		undecodable(ctx, s.Core(), sourceMME, log, nil, err)
		return nil
	}

	switch m := pdu.(type) {
	case *s1ap.SuccessfulOutcome:
		switch m.ProcedureCode {
		case s1ap.ProcS1Setup:
			return p.handleS1SetupResponse(ctx, s, m.Value)
		case s1ap.ProcENBConfigurationUpdate:
			s.UpdateAcknowledged()
			return nil
		}
	case *s1ap.UnsuccessfulOutcome:
		switch m.ProcedureCode {
		case s1ap.ProcS1Setup:
			return p.handleS1SetupFailure(ctx, s, m.Value)
		case s1ap.ProcENBConfigurationUpdate:
			p.handleENBConfigurationUpdateFailure(ctx, s, m.Value)
			return nil
		}
	case *s1ap.InitiatingMessage:
		if s.SetupDone() && m.ProcedureCode == s1ap.ProcMMEConfigurationUpdate {
			p.handleMMEConfigurationUpdate(ctx, s, m.Value)
			return nil
		}
	}

	if !s.SetupDone() {
		notCompatible(ctx, s.Core(), sourceMME, log, pdu, msg)
		return nil
	}

	p.relay.fromMME(ctx, s.Core(), pdu, msg)

	return nil
}

func (protocol) handleS1SetupResponse(ctx context.Context, s *mmeSession, value []byte) error {
	if !s.AwaitingSetup() {
		return nil
	}

	resp, err := s1ap.ParseS1SetupResponse(value)
	if err != nil {
		return s.SetupInvalid(ctx, "S1 Setup Response", err)
	}

	served, err := gummeisFrom(resp.ServedGUMMEIs)
	if err != nil {
		return s.SetupInvalid(ctx, "S1 Setup Response", err)
	}

	var name string
	if resp.MMEName != nil {
		name = *resp.MMEName
	}

	s.SetupAccepted(ctx, func(info *mmeInfo) {
		info.name, info.served, info.capacity = name, served, resp.RelativeMMECapacity
		info.cause, info.ttw = nil, nil
	})

	s.Client().Logger().InfoContext(ctx, "MME S1 Setup succeeded", slog.String("mme_name", name))

	return nil
}

func (protocol) handleS1SetupFailure(ctx context.Context, s *mmeSession, value []byte) error {
	if !s.AwaitingSetup() {
		return nil
	}

	s.EndSetup(assoc.SetupFailure)

	fail, err := s1ap.ParseS1SetupFailure(value)
	if err != nil {
		tracing.SpanFailed(ctx, assoc.ErrTypeInvalid)
		return fmt.Errorf("S1 Setup Failure: %w", err)
	}

	retry := s.RetryAfter(waitOf(fail.TimeToWait))

	s.SetupRejected(ctx, func(info *mmeInfo) { info.cause, info.ttw = fail.Cause, fail.TimeToWait })

	s.Client().Logger().WarnContext(ctx, "MME rejected S1 Setup", slog.String("cause", causeText(fail.Cause)),
		slog.Duration("retry_in", retry))

	return nil
}

func (protocol) handleENBConfigurationUpdateFailure(ctx context.Context, s *mmeSession, value []byte) {
	if !s.AwaitingUpdate() {
		return
	}

	var wait *time.Duration

	cause := "undecodable"

	if fail, err := s1ap.ParseENBConfigurationUpdateFailure(value); err == nil {
		cause, wait = causeText(fail.Cause), waitOf(fail.TimeToWait)
	} else {
		tracing.SpanFailed(ctx, assoc.ErrTypeInvalid)
	}

	retry := s.UpdateRejected(wait)

	s.Client().Logger().WarnContext(ctx, "MME rejected eNB Configuration Update", slog.String("cause", cause),
		slog.Duration("retry_in", retry))
}

// TS 36.413 clause 8.7.5
// TS 36.413 clause 8.7.7
// TS 36.300 clause 4.6.2
func (p protocol) handleMMEConfigurationUpdate(ctx context.Context, s *mmeSession, value []byte) {
	log := s.Client().Logger()
	upd, err := s1ap.ParseMMEConfigurationUpdate(value)

	var served []ServedGUMMEIs

	if err == nil && len(upd.ServedGUMMEIs) > 0 {
		served, err = gummeisFrom(upd.ServedGUMMEIs)
	}

	if err != nil {
		cause := causeSemanticError

		var diag *s1ap.CriticalityDiagnostics

		if codecError(err) {
			cause, diag = failureCause(err)
		}

		tracing.SpanFailed(ctx, assoc.ErrTypeInvalid)
		log.WarnContext(ctx, "rejected MME Configuration Update", slog.Any("error", err))

		out, err := (&s1ap.MMEConfigurationUpdateFailure{Cause: &cause, CriticalityDiagnostics: diag}).Marshal()
		if err != nil {
			log.ErrorContext(ctx, "failed to marshal MME Configuration Update Failure", slog.Any("error", err))
			return
		}

		if err := s.Core().Send(ctx, out, assoc.NonUEStream); err != nil {
			log.WarnContext(ctx, "failed to send MME Configuration Update Failure", slog.Any("error", err))
		}

		return
	}

	out, err := (&s1ap.MMEConfigurationUpdateAcknowledge{
		CriticalityDiagnostics: responseDiagnostics(s1ap.ProcMMEConfigurationUpdate, upd.Diagnostics()),
	}).Marshal()
	if err != nil {
		log.ErrorContext(ctx, "failed to marshal MME Configuration Update Acknowledge", slog.Any("error", err))
		return
	}

	var (
		ended  []GUMMEI
		radios []*ranAssoc
	)

	s.Update(ctx, func(c *mmeClient) {
		if upd.MMEName != nil {
			c.Info.name = *upd.MMEName
		}

		if served != nil {
			c.Info.served = served
			o, _ := p.core.OperatorLocked(c.OperatorID())
			still := mmeGUMMEIs(served, o.PLMNs)
			ended = endOverload(c, func(id GUMMEI) bool { return !slices.Contains(still, id) })
			radios = p.core.OperatorRadiosLocked(c.OperatorID())
		}

		if upd.RelativeMMECapacity != nil {
			c.Info.capacity = upd.RelativeMMECapacity
		}
	})

	if err := s.Core().Send(ctx, out, assoc.NonUEStream); err != nil {
		log.WarnContext(ctx, "failed to send MME Configuration Update Acknowledge", slog.Any("error", err))
	}

	log.InfoContext(ctx, "MME configuration updated")

	if len(ended) > 0 && len(radios) > 0 {
		log.InfoContext(ctx, "overload of GUMMEIs the MME no longer serves ended, Overload Stop sent to radios",
			slog.Int("gummeis", len(ended)), slog.Int("radios", len(radios)))
		p.sendOverloadStop(ctx, radios, s.Core().Operator(), log, ended)
	}
}

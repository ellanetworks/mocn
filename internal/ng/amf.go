package ng

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/tracing"
)

type amfInfo struct {
	name        amfName
	served      []GUAMI
	types       map[GUAMI]ngap.GUAMIType
	support     []PLMNSupport
	capacity    *uint8
	unavailable []GUAMI
	backups     map[GUAMI]amfName
	overload    []Overload
	cause       *ngap.Cause
	ttw         *ngap.TimeToWait
}

func amfStatusLocked(c *amfClient) AMFStatus {
	s := AMFStatus{
		State:               c.State,
		Since:               c.Since,
		PrimaryTNLAddress:   c.PrimaryAddressLocked(),
		AMFName:             c.Info.name.String(),
		ServedGUAMIs:        slices.Clone(c.Info.served),
		PLMNSupport:         slices.Clone(c.Info.support),
		RelativeAMFCapacity: c.Info.capacity,
		UnavailableGUAMIs:   slices.Clone(c.Info.unavailable),
	}

	for _, o := range c.Info.overload {
		o.GUAMIs, o.Slices = slices.Clone(o.GUAMIs), slices.Clone(o.Slices)
		s.Overload = append(s.Overload, o)
	}

	return s
}

func (protocol) Overloaded(info amfInfo) bool {
	return len(info.overload) > 0
}

func (p protocol) CoreLostLocked(c *amfClient, a *amfAssoc) func(ctx context.Context) {
	cleared := c.Info.overload
	backups := c.Info.backups
	radios := p.core.ClientRadiosLocked(c)

	return func(ctx context.Context) {
		p.overloadCleared(ctx, radios, c.Operator(), c.Logger(), cleared)
		p.relay.amfLost(ctx, a, backups)
	}
}

// TS 38.413 clause 8.7.1.2
// TS 38.413 clause 9.2.6.1
func ngSetupRequest(gnbID db.GlobalGNBID, want gnbConfig) ([]byte, error) {
	id, err := globalGNBIDTo(gnbID)
	if err != nil {
		return nil, err
	}

	tas, err := tasTo(want.tas)
	if err != nil {
		return nil, err
	}

	out, err := (&ngap.NGSetupRequest{
		GlobalRANNodeID: id, RANNodeName: ngap.Ptr(assoc.NodeName), SupportedTAList: tas, DefaultPagingDRX: ngap.Ptr(want.drx),
		UERetentionInformation: ngap.Ptr(ngap.UERetentionUesRetained),
	}).Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal NG Setup Request: %w", err)
	}

	return out, nil
}

// TS 38.413 clause 8.7.2
// TS 38.413 clause 9.2.6.4
func (protocol) ConfigurationUpdate(want, acked gnbConfig) ([]byte, error) {
	upd := &ngap.RANConfigurationUpdate{RANNodeName: ngap.Ptr(assoc.NodeName)}

	if !tasEqual(want.tas, acked.tas) {
		tas, err := tasTo(want.tas)
		if err != nil {
			return nil, err
		}

		upd.SupportedTAList = tas
	}

	if want.drx != acked.drx {
		upd.DefaultPagingDRX = ngap.Ptr(want.drx)
	}

	out, err := upd.Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal RAN Configuration Update: %w", err)
	}

	return out, nil
}

func (p protocol) HandleCore(ctx context.Context, s *amfSession, msg []byte) error {
	pdu, err := ngap.Unmarshal(msg)

	ctx, span := p.receiveFromAMF(ctx, s.Core(), pdu, msg)
	defer span.End()

	log := s.Client().Logger()

	if err != nil {
		undecodable(ctx, s.Core(), sourceAMF, log, nil, err)
		return nil
	}

	switch m := pdu.(type) {
	case *ngap.SuccessfulOutcome:
		switch m.ProcedureCode {
		case ngap.ProcNGSetup:
			return p.handleNGSetupResponse(ctx, s, m.Value)
		case ngap.ProcRANConfigurationUpdate:
			s.UpdateAcknowledged()
			return nil
		}
	case *ngap.UnsuccessfulOutcome:
		switch m.ProcedureCode {
		case ngap.ProcNGSetup:
			return p.handleNGSetupFailure(ctx, s, m.Value)
		case ngap.ProcRANConfigurationUpdate:
			p.handleRANConfigurationUpdateFailure(ctx, s, m.Value)
			return nil
		}
	case *ngap.InitiatingMessage:
		if s.SetupDone() && m.ProcedureCode == ngap.ProcAMFConfigurationUpdate {
			p.handleAMFConfigurationUpdate(ctx, s, m.Value)
			return nil
		}
	}

	if !s.SetupDone() {
		notCompatible(ctx, s.Core(), sourceAMF, log, pdu, msg)
		return nil
	}

	p.relay.fromAMF(ctx, s.Core(), pdu, msg)

	return nil
}

// TS 38.413 clause 8.7.1.1
// TS 38.413 clause 8.7.1.2
func (p protocol) handleNGSetupResponse(ctx context.Context, s *amfSession, value []byte) error {
	if !s.AwaitingSetup() {
		return nil
	}

	resp, err := ngap.ParseNGSetupResponse(value)
	if err != nil {
		return s.SetupInvalid(ctx, "NG Setup Response", err)
	}

	served, err := servedGUAMIsFrom(resp.ServedGUAMIList)
	if err != nil {
		return s.SetupInvalid(ctx, "NG Setup Response", err)
	}

	support, err := plmnSupportFrom(resp.PLMNSupportList)
	if err != nil {
		return s.SetupInvalid(ctx, "NG Setup Response", err)
	}

	name := amfNameFrom(&resp.AMFName, resp.ExtendedAMFName)

	retained := resp.UERetentionInformation != nil && *resp.UERetentionInformation == ngap.UERetentionUesRetained
	if !retained {
		p.relay.notRetained(ctx, s.Core())
	}

	p.relay.setSets(s.Core(), served.guamis)

	s.SetupAccepted(ctx, func(info *amfInfo) {
		*info = amfInfo{
			name: name, served: served.guamis, types: served.types, backups: served.backups, support: support,
			capacity: resp.RelativeAMFCapacity,
		}
	})

	s.Client().Logger().InfoContext(ctx, "AMF NG Setup succeeded", slog.String("amf_name", name.String()),
		slog.Bool("ues_retained", retained))

	return nil
}

func (protocol) handleNGSetupFailure(ctx context.Context, s *amfSession, value []byte) error {
	if !s.AwaitingSetup() {
		return nil
	}

	s.EndSetup(assoc.SetupFailure)

	fail, err := ngap.ParseNGSetupFailure(value)
	if err != nil {
		tracing.SpanFailed(ctx, assoc.ErrTypeInvalid)
		return fmt.Errorf("NG Setup Failure: %w", err)
	}

	retry := s.RetryAfter(waitOf(fail.TimeToWait))

	s.SetupRejected(ctx, func(info *amfInfo) { info.cause, info.ttw = fail.Cause, fail.TimeToWait })

	s.Client().Logger().WarnContext(ctx, "AMF rejected NG Setup", slog.String("cause", causeText(fail.Cause)),
		slog.Duration("retry_in", retry))

	return nil
}

func (protocol) handleRANConfigurationUpdateFailure(ctx context.Context, s *amfSession, value []byte) {
	if !s.AwaitingUpdate() {
		return
	}

	var wait *time.Duration

	cause := "undecodable"

	if fail, err := ngap.ParseRANConfigurationUpdateFailure(value); err == nil {
		cause, wait = causeText(fail.Cause), waitOf(fail.TimeToWait)
	} else {
		tracing.SpanFailed(ctx, assoc.ErrTypeInvalid)
	}

	retry := s.UpdateRejected(wait)

	s.Client().Logger().WarnContext(ctx, "AMF rejected RAN Configuration Update", slog.String("cause", cause),
		slog.Duration("retry_in", retry))
}

// TS 38.413 clause 8.7.3
// TS 38.413 clause 8.7.8
// TS 38.300 clause 4.10.2.3
// TS 23.501 clause 5.21.2.2.1
func (p protocol) handleAMFConfigurationUpdate(ctx context.Context, s *amfSession, value []byte) {
	log := s.Client().Logger()
	upd, err := ngap.ParseAMFConfigurationUpdate(value)

	var (
		served  *servedGUAMIs
		support []PLMNSupport
	)

	if err == nil && len(upd.ServedGUAMIList) > 0 {
		var v servedGUAMIs
		if v, err = servedGUAMIsFrom(upd.ServedGUAMIList); err == nil {
			served = &v
		}
	}

	if err == nil && len(upd.PLMNSupportList) > 0 {
		support, err = plmnSupportFrom(upd.PLMNSupportList)
	}

	if err != nil {
		cause := causeSemanticError

		var diag *ngap.CriticalityDiagnostics

		if codecError(err) {
			cause, diag = failureCause(err)
		}

		tracing.SpanFailed(ctx, assoc.ErrTypeInvalid)
		log.WarnContext(ctx, "rejected AMF Configuration Update", slog.Any("error", err))

		out, err := (&ngap.AMFConfigurationUpdateFailure{Cause: &cause, CriticalityDiagnostics: diag}).Marshal()
		if err != nil {
			log.ErrorContext(ctx, "failed to marshal AMF Configuration Update Failure", slog.Any("error", err))
			return
		}

		if err := s.Core().Send(ctx, out, assoc.NonUEStream); err != nil {
			log.WarnContext(ctx, "failed to send AMF Configuration Update Failure", slog.Any("error", err))
		}

		return
	}

	failed := tnlAssociationsNotSetUp(upd.AMFTNLAssociationToAddList)

	out, err := (&ngap.AMFConfigurationUpdateAcknowledge{
		AMFTNLAssociationFailedToSetupList: failed,
		CriticalityDiagnostics:             responseDiagnostics(ngap.ProcAMFConfigurationUpdate, upd.Diagnostics()),
	}).Marshal()
	if err != nil {
		log.ErrorContext(ctx, "failed to marshal AMF Configuration Update Acknowledge", slog.Any("error", err))
		return
	}

	var (
		ended  []GUAMI
		radios []*ranAssoc
	)

	s.Update(ctx, func(c *amfClient) {
		if upd.AMFName != nil || upd.ExtendedAMFName != nil {
			c.Info.name = amfNameFrom(upd.AMFName, upd.ExtendedAMFName)
		}

		if served != nil {
			c.Info.served, c.Info.types, c.Info.backups, c.Info.unavailable = served.guamis, served.types, served.backups, nil
			c.Info.overload, ended = withoutGUAMIs(c.Info.overload, func(g GUAMI) bool {
				return !slices.Contains(served.guamis, g)
			})
			radios = p.core.OperatorRadiosLocked(c.OperatorID())
		}

		if support != nil {
			c.Info.support = support
		}

		if upd.RelativeAMFCapacity != nil {
			c.Info.capacity = upd.RelativeAMFCapacity
		}
	})

	if served != nil {
		p.relay.setSets(s.Core(), served.guamis)
	}

	if err := s.Core().Send(ctx, out, assoc.NonUEStream); err != nil {
		log.WarnContext(ctx, "failed to send AMF Configuration Update Acknowledge", slog.Any("error", err))
	}

	if len(failed) > 0 {
		log.InfoContext(ctx, "AMF TNL associations to add not set up", slog.Int("tnl_associations", len(failed)))
	}

	log.InfoContext(ctx, "AMF configuration updated")

	if len(ended) > 0 && len(radios) > 0 {
		log.InfoContext(ctx, "overload of GUAMIs the AMF no longer serves ended, Overload Stop sent to radios",
			slog.Int("guamis", len(ended)), slog.Int("radios", len(radios)))
		p.sendOverloadStop(ctx, radios, s.Core().Operator(), log, ended)
	}
}

func tnlAssociationsNotSetUp(add ngap.AMFTNLAssociationToAddList) ngap.TNLAssociationList {
	out := make(ngap.TNLAssociationList, 0, len(add))

	for _, item := range add {
		out = append(out, ngap.TNLAssociationItem{TNLAssociationAddress: item.AMFTNLAssociationAddress, Cause: causeTransportUnspecified})
	}

	return out
}

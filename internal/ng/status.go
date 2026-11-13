package ng

import (
	"context"
	"errors"
	"log/slog"
	"slices"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/tracing"
)

// TS 38.413 clause 9.3.1.105
var overloadActions = map[ngap.OverloadAction]OverloadAction{
	ngap.OverloadActionRejectNonEmergencyMODT:                      OverloadRejectNonEmergencyMODT,
	ngap.OverloadActionRejectRRCCRSignalling:                       OverloadRejectRRCCRSignalling,
	ngap.OverloadActionPermitEmergencySessionsAndMTServicesOnly:    OverloadPermitEmergencyAndMTOnly,
	ngap.OverloadActionPermitHighPrioritySessionsAndMTServicesOnly: OverloadPermitHighPriorityAndMTOnly,
}

// TS 38.413 clause 8.7.6
// TS 38.300 clause 4.10.2.3
// TS 23.501 clause 5.21.2.2.1
func (g *Gateway) amfStatusIndication(ctx context.Context, a *AMFAssociation, log *slog.Logger, value, msg []byte) {
	ind, err := ngap.ParseAMFStatusIndication(value)

	var unavailable []GUAMI

	if err == nil {
		for _, item := range ind.UnavailableGUAMIList {
			var v GUAMI
			if v, err = guamiFrom(item.GUAMI); err != nil {
				break
			}

			unavailable = append(unavailable, v)
		}
	}

	if err != nil {
		tracing.SpanFailed(ctx, errTypeInvalid)
		log.WarnContext(ctx, "rejected AMF Status Indication from AMF", slog.Any("error", err))

		if ase, ok := errors.AsType[*ngap.AbstractSyntaxError](err); ok {
			sendErrorIndication(ctx, a, log, ase.Cause, ase.ErrorIndicationDiagnostics())
		}

		return
	}

	g.mu.Lock()

	c := a.client
	if c.assoc != a {
		g.mu.Unlock()
		return
	}

	for _, v := range unavailable {
		if !slices.Contains(c.status.UnavailableGUAMIs, v) {
			c.status.UnavailableGUAMIs = append(c.status.UnavailableGUAMIs, v)
		}
	}

	radios := g.operatorRadiosLocked(c.operatorID)
	g.mu.Unlock()

	log.InfoContext(ctx, "AMF Status Indication from AMF relayed to radios", slog.Int("guamis", len(unavailable)),
		slog.Int("radios", len(radios)))
	sendRadios(ctx, radios, c.operator(), log, "AMF Status Indication", msg)
}

func overloadResponseFrom(r *ngap.OverloadResponse) *OverloadAction {
	if r == nil {
		return nil
	}

	if a, ok := overloadActions[r.OverloadAction]; ok {
		return &a
	}

	return nil
}

func loadReduction(t *ngap.TrafficLoadReductionIndication) *uint8 {
	if t == nil {
		return nil
	}

	return new(uint8(*t))
}

// TS 38.413 clause 8.7.7
// TS 38.413 clause 9.2.6.14
func (g *Gateway) overloadStart(ctx context.Context, a *AMFAssociation, log *slog.Logger, value []byte) {
	start, err := ngap.ParseOverloadStart(value)
	if err != nil {
		tracing.SpanFailed(ctx, errTypeInvalid)
		log.WarnContext(ctx, "rejected Overload Start from AMF", slog.Any("error", err))

		if ase, ok := errors.AsType[*ngap.AbstractSyntaxError](err); ok {
			sendErrorIndication(ctx, a, log, ase.Cause, ase.ErrorIndicationDiagnostics())
		}

		return
	}

	o := &Overload{
		Action:                         overloadResponseFrom(start.AMFOverloadResponse),
		TrafficLoadReductionIndication: loadReduction(start.AMFTrafficLoadReductionIndication),
	}

	for _, item := range start.OverloadStartNSSAIList {
		s := SliceOverload{
			Action:                         overloadResponseFrom(item.SliceOverloadResponse),
			TrafficLoadReductionIndication: loadReduction(item.SliceTrafficLoadReductionIndication),
		}

		for _, sl := range item.SliceOverloadList {
			s.SNSSAIs = append(s.SNSSAIs, snssaiFrom(sl.SNSSAI))
		}

		o.Slices = append(o.Slices, s)
	}

	g.mu.Lock()

	if a.client.assoc == a {
		a.client.status.Overload = o
	}

	g.mu.Unlock()

	log.InfoContext(ctx, "Overload Start from AMF recorded", slog.Any("overload_action", o.Action),
		slog.Int("slices", len(o.Slices)))
}

// TS 38.413 clause 8.7.8
func (g *Gateway) overloadStop(ctx context.Context, a *AMFAssociation, log *slog.Logger) {
	g.mu.Lock()

	ongoing := a.client.assoc == a && a.client.status.Overload != nil
	if ongoing {
		a.client.status.Overload = nil
	}

	g.mu.Unlock()

	if !ongoing {
		log.InfoContext(ctx, "Overload Stop from AMF ignored: no ongoing overload action")
		return
	}

	log.InfoContext(ctx, "Overload Stop from AMF recorded")
}

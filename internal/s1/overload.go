package s1

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/gw"
	"github.com/ellanetworks/mocn/internal/tracing"
)

const (
	procOverloadStart = "overload_start"
	procOverloadStop  = "overload_stop"
	overloadRelayed   = "relayed"
	overloadNoRadio   = "no_radio"
	overloadIgnored   = "ignored"
	overloadInvalid   = "invalid"
	maxnoofMMECs      = 256
)

var overloadResults = map[string][]string{
	procOverloadStart: {overloadRelayed, overloadNoRadio, overloadInvalid},
	procOverloadStop:  {overloadRelayed, overloadNoRadio, overloadIgnored, overloadInvalid},
}

// TS 36.413 clause 9.2.3.20
var overloadActions = map[s1ap.OverloadAction]OverloadAction{
	s1ap.OverloadActionRejectNonEmergencyMODT:                                                         OverloadRejectNonEmergencyMODT,
	s1ap.OverloadActionRejectRRCCRSignalling:                                                          OverloadRejectRRCCRSignalling,
	s1ap.OverloadActionPermitEmergencySessionsAndMobileTerminatedServicesOnly:                         OverloadPermitEmergencyAndMTOnly,
	s1ap.OverloadActionPermitHighPrioritySessionsAndMobileTerminatedServicesOnly:                      OverloadPermitHighPriorityAndMTOnly,
	s1ap.OverloadActionRejectDelayTolerantAccess:                                                      OverloadRejectDelayTolerantAccess,
	s1ap.OverloadActionPermitHighPrioritySessionsAndExceptionReportingAndMobileTerminatedServicesOnly: OverloadPermitHighPriorityExceptionReportAndMT,
	s1ap.OverloadActionNotAcceptMODataOrDelayTolerantAccessFromCPCIoT:                                 OverloadNotAcceptMODataOrDelayTolerantFromCPCIoT,
}

// TS 36.413 clause 9.2.3.9
func gummeiFrom(g s1ap.GUMMEI) (GUMMEI, error) {
	plmn, err := gw.DecodePLMN(g.PLMNIdentity)
	if err != nil {
		return GUMMEI{}, err
	}

	return GUMMEI{PLMN: plmn, MMEGroupID: uint16(g.MMEGroupID[0])<<8 | uint16(g.MMEGroupID[1]), MMECode: uint8(g.MMECode)}, nil
}

// TS 36.413 clause 9.2.3.9
func gummeiTo(g GUMMEI) (s1ap.GUMMEI, error) {
	plmn, err := gw.EncodePLMN[s1ap.PLMNIdentity](g.PLMN)
	if err != nil {
		return s1ap.GUMMEI{}, err
	}

	return s1ap.GUMMEI{
		PLMNIdentity: plmn, MMEGroupID: s1ap.MMEGroupID{byte(g.MMEGroupID >> 8), byte(g.MMEGroupID)},
		MMECode: s1ap.MMECode(g.MMECode),
	}, nil
}

func gummeiListFrom(in []s1ap.GUMMEI) ([]GUMMEI, error) {
	out := make([]GUMMEI, 0, len(in))

	for _, g := range in {
		id, err := gummeiFrom(g)
		if err != nil {
			return nil, err
		}

		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}

	return out, nil
}

func gummeiListTo(in []GUMMEI) ([]s1ap.GUMMEI, error) {
	out := make([]s1ap.GUMMEI, 0, len(in))

	for _, g := range in {
		id, err := gummeiTo(g)
		if err != nil {
			return nil, err
		}

		out = append(out, id)
	}

	return out, nil
}

// TS 36.413 clause 9.1.8.5
// TS 36.300 clause 4.6.2
func mmeGUMMEIs(served []ServedGUMMEIs) []GUMMEI {
	if len(served) == 0 {
		return nil
	}

	var out []GUMMEI

	lte := served[0]

	for _, p := range lte.PLMNs {
		for _, group := range lte.GroupIDs {
			for _, code := range lte.MMECodes {
				if len(out) == maxnoofMMECs {
					return out
				}

				out = append(out, GUMMEI{PLMN: p, MMEGroupID: group, MMECode: code})
			}
		}
	}

	return out
}

func (g *Gateway) operatorRadiosLocked(operatorID int64) []*RANAssociation {
	o, ok := g.operatorLocked(operatorID)
	if !ok {
		return nil
	}

	var out []*RANAssociation

	for _, n := range g.ranNodes {
		if n.up && n.setup != nil && broadcasts(n.setup.tas, o.plmns) {
			out = append(out, n)
		}
	}

	return out
}

func (g *Gateway) countOverload(proc, result string) {
	g.cfg.Metrics.Overload.WithLabelValues(interfaceS1, proc, result).Inc()
}

func (g *Gateway) rejectOverload(ctx context.Context, a *MMEAssociation, log *slog.Logger, proc string, err error) {
	g.countOverload(proc, overloadInvalid)
	tracing.SpanFailed(ctx, overloadInvalid)
	log.WarnContext(ctx, "rejected "+proc+" from MME", slog.Any("error", err))

	if ase, ok := errors.AsType[*s1ap.AbstractSyntaxError](err); ok {
		sendErrorIndication(ctx, a, log, ase.Cause, ase.ErrorIndicationDiagnostics())
	}
}

// TS 36.413 clause 8.7.6
// TS 36.300 clause 4.6.2
// TS 36.300 clause 19.2.2.12.1
func (g *Gateway) overloadStart(ctx context.Context, a *MMEAssociation, log *slog.Logger, value []byte) {
	start, err := s1ap.ParseOverloadStart(value)
	if err != nil {
		g.rejectOverload(ctx, a, log, procOverloadStart, err)
		return
	}

	gummeis, err := gummeiListFrom(start.GUMMEIList)
	if err != nil {
		g.rejectOverload(ctx, a, log, procOverloadStart, err)
		return
	}

	action := overloadActions[start.OverloadResponse.OverloadAction]

	g.mu.Lock()

	c := a.client
	if c.assoc != a {
		g.mu.Unlock()
		return
	}

	if len(gummeis) == 0 {
		gummeis = mmeGUMMEIs(c.status.ServedGUMMEIs)
	}

	for _, id := range gummeis {
		entry := Overload{GUMMEI: id, Action: action, TrafficLoadReductionIndication: start.TrafficLoadReductionIndication}

		if i := slices.IndexFunc(c.status.Overload, func(o Overload) bool { return o.GUMMEI == id }); i >= 0 {
			c.status.Overload[i] = entry
		} else {
			c.status.Overload = append(c.status.Overload, entry)
		}
	}

	radios := g.operatorRadiosLocked(c.operatorID)
	g.mu.Unlock()

	list, err := gummeiListTo(gummeis)
	if err != nil || len(list) == 0 {
		g.rejectOverload(ctx, a, log, procOverloadStart, errors.Join(errors.New("no GUMMEI of the MME"), err))
		return
	}

	out, err := (&s1ap.OverloadStart{
		OverloadResponse: start.OverloadResponse, GUMMEIList: list,
		TrafficLoadReductionIndication: start.TrafficLoadReductionIndication,
	}).Marshal()
	if err != nil {
		g.countOverload(procOverloadStart, overloadInvalid)
		tracing.SpanFailed(ctx, overloadInvalid)
		log.ErrorContext(ctx, "failed to marshal Overload Start", slog.Any("error", err))

		return
	}

	log = log.With(slog.String("overload_action", string(action)), slog.Int("gummeis", len(list)))

	if len(radios) == 0 {
		g.countOverload(procOverloadStart, overloadNoRadio)
		tracing.SpanFailed(ctx, overloadNoRadio)
		log.InfoContext(ctx, "Overload Start from MME, no radio of the operator is up")

		return
	}

	g.countOverload(procOverloadStart, overloadRelayed)
	log.InfoContext(ctx, "Overload Start from MME relayed to radios", slog.Int("radios", len(radios)))
	sendRadios(ctx, radios, a.client.operator(), log, "Overload Start", out)
}

func overloadActionTo(a OverloadAction) (s1ap.OverloadAction, bool) {
	for k, v := range overloadActions {
		if v == a {
			return k, true
		}
	}

	return 0, false
}

func overloadStarts(entries []Overload) ([]*s1ap.OverloadStart, error) {
	type group struct {
		action  s1ap.OverloadAction
		tlri    *uint8
		gummeis []GUMMEI
	}

	var groups []*group

	for _, e := range entries {
		action, ok := overloadActionTo(e.Action)
		if !ok {
			return nil, errors.New("unknown overload action " + string(e.Action))
		}

		i := slices.IndexFunc(groups, func(gr *group) bool {
			return gr.action == action && len(gr.gummeis) < maxnoofMMECs &&
				(gr.tlri == nil) == (e.TrafficLoadReductionIndication == nil) &&
				(gr.tlri == nil || *gr.tlri == *e.TrafficLoadReductionIndication)
		})
		if i < 0 {
			groups = append(groups, &group{action: action, tlri: e.TrafficLoadReductionIndication})
			i = len(groups) - 1
		}

		groups[i].gummeis = append(groups[i].gummeis, e.GUMMEI)
	}

	out := make([]*s1ap.OverloadStart, 0, len(groups))

	for _, gr := range groups {
		list, err := gummeiListTo(gr.gummeis)
		if err != nil {
			return nil, err
		}

		out = append(out, &s1ap.OverloadStart{
			OverloadResponse: s1ap.OverloadResponse{OverloadAction: gr.action}, GUMMEIList: list,
			TrafficLoadReductionIndication: gr.tlri,
		})
	}

	return out, nil
}

// TS 36.413 clause 8.7.3.1
// TS 36.413 clause 8.7.6
func (g *Gateway) replayOverloadLocked(ctx context.Context, n *RANAssociation) {
	for _, key := range slices.Sorted(maps.Keys(g.mmes)) {
		c := g.mmes[key]
		if len(c.status.Overload) == 0 {
			continue
		}

		o, ok := g.operatorLocked(c.operatorID)
		if !ok || !broadcasts(n.setup.tas, o.plmns) {
			continue
		}

		log := ranLog(c.logger(), n)

		starts, err := overloadStarts(c.status.Overload)
		if err != nil {
			log.ErrorContext(ctx, "failed to build Overload Start", slog.Any("error", err))
			continue
		}

		for _, start := range starts {
			out, err := start.Marshal()
			if err != nil {
				log.ErrorContext(ctx, "failed to marshal Overload Start", slog.Any("error", err))
				continue
			}

			if err := (operatorRAN{n: n, operator: o.name}).Send(ctx, out, nonUEStream); err != nil {
				log.WarnContext(ctx, "failed to send Overload Start to radio", slog.Any("error", err))
				continue
			}

			g.countOverload(procOverloadStart, overloadRelayed)
			log.InfoContext(ctx, "ongoing MME overload sent to radio after S1 Setup",
				slog.Int("gummeis", len(start.GUMMEIList)))
		}
	}
}

// TS 36.413 clause 8.7.7
// TS 36.300 clause 4.6.2
// TS 36.300 clause 19.2.2.12.2
func (g *Gateway) overloadStop(ctx context.Context, a *MMEAssociation, log *slog.Logger, value []byte) {
	stop, err := s1ap.ParseOverloadStop(value)
	if err != nil {
		g.rejectOverload(ctx, a, log, procOverloadStop, err)
		return
	}

	gummeis, err := gummeiListFrom(stop.GUMMEIList)
	if err != nil {
		g.rejectOverload(ctx, a, log, procOverloadStop, err)
		return
	}

	g.mu.Lock()

	c := a.client
	if c.assoc != a {
		g.mu.Unlock()
		return
	}

	var ended []GUMMEI

	c.status.Overload = slices.DeleteFunc(c.status.Overload, func(o Overload) bool {
		if len(gummeis) == 0 || slices.Contains(gummeis, o.GUMMEI) {
			ended = append(ended, o.GUMMEI)
			return true
		}

		return false
	})

	radios := g.operatorRadiosLocked(c.operatorID)
	g.mu.Unlock()

	if len(ended) == 0 {
		g.countOverload(procOverloadStop, overloadIgnored)
		log.InfoContext(ctx, "Overload Stop from MME ignored: no ongoing overload action")

		return
	}

	if len(radios) == 0 {
		g.countOverload(procOverloadStop, overloadNoRadio)
		tracing.SpanFailed(ctx, overloadNoRadio)
		log.InfoContext(ctx, "Overload Stop from MME, no radio of the operator is up", slog.Int("gummeis", len(ended)))

		return
	}

	g.countOverload(procOverloadStop, overloadRelayed)
	log.InfoContext(ctx, "Overload Stop from MME relayed to radios", slog.Int("gummeis", len(ended)),
		slog.Int("radios", len(radios)))
	g.sendOverloadStop(ctx, radios, a.client.operator(), log, ended)
}

// TS 36.413 clause 8.7.3.1
func (g *Gateway) overloadCleared(ctx context.Context, radios []*RANAssociation, operator string, log *slog.Logger,
	cleared []Overload,
) {
	if len(cleared) == 0 || len(radios) == 0 {
		return
	}

	ended := make([]GUMMEI, 0, len(cleared))
	for _, o := range cleared {
		ended = append(ended, o.GUMMEI)
	}

	log.InfoContext(ctx, "MME overload state cleared, Overload Stop sent to radios", slog.Int("gummeis", len(ended)),
		slog.Int("radios", len(radios)))
	g.sendOverloadStop(ctx, radios, operator, log, ended)
}

func (g *Gateway) sendOverloadStop(ctx context.Context, radios []*RANAssociation, operator string, log *slog.Logger,
	ended []GUMMEI,
) {
	list, err := gummeiListTo(ended)
	if err != nil {
		log.ErrorContext(ctx, "failed to encode the GUMMEI List", slog.Any("error", err))
		return
	}

	out, err := (&s1ap.OverloadStop{GUMMEIList: list}).Marshal()
	if err != nil {
		log.ErrorContext(ctx, "failed to marshal Overload Stop", slog.Any("error", err))
		return
	}

	sendRadios(ctx, radios, operator, log, "Overload Stop", out)
}

func sendRadios(ctx context.Context, radios []*RANAssociation, operator string, log *slog.Logger, what string,
	out []byte,
) {
	for _, n := range radios {
		if err := (operatorRAN{n: n, operator: operator}).Send(ctx, out, nonUEStream); err != nil {
			ranLog(log, n).WarnContext(ctx, "failed to send "+what+" to radio", slog.Any("error", err))
		}
	}
}

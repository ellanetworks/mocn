package s1

import (
	"context"
	"errors"
	"log/slog"
	"slices"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/plmnid"
	"github.com/ellanetworks/mocn/internal/tracing"
)

const maxnoofMMECs = 256

var overloadProcedures = map[string]s1ap.ProcedureCode{
	assoc.OverloadStart: s1ap.ProcOverloadStart,
	assoc.OverloadStop:  s1ap.ProcOverloadStop,
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
	plmn, err := plmnid.Decode(g.PLMNIdentity)
	if err != nil {
		return GUMMEI{}, err
	}

	return GUMMEI{PLMN: plmn, MMEGroupID: uint16(g.MMEGroupID[0])<<8 | uint16(g.MMEGroupID[1]), MMECode: uint8(g.MMECode)}, nil
}

// TS 36.413 clause 9.2.3.9
func gummeiTo(g GUMMEI) (s1ap.GUMMEI, error) {
	plmn, err := plmnid.Encode[s1ap.PLMNIdentity](g.PLMN)
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
func mmeGUMMEIs(served []ServedGUMMEIs, plmns []db.PLMN) []GUMMEI {
	if len(served) == 0 {
		return nil
	}

	var out []GUMMEI

	lte := served[0]

	for _, p := range lte.PLMNs {
		if !slices.Contains(plmns, p) {
			continue
		}

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

func (g *Gateway) rejectOverload(ctx context.Context, a *mmeAssoc, log *slog.Logger, proc string, err error) {
	g.core.CountOverload(proc, assoc.OverloadInvalid)
	tracing.SpanFailed(ctx, assoc.OverloadInvalid)
	log.WarnContext(ctx, "rejected "+proc+" from MME", slog.Any("error", err))

	if codecError(err) {
		indicateParseError(ctx, a, log, overloadProcedures[proc], err)
	}
}

// TS 36.413 clause 8.7.6
// TS 36.300 clause 4.6.2
// TS 36.300 clause 19.2.2.12.1
func (g *Gateway) overloadStart(ctx context.Context, a *mmeAssoc, log *slog.Logger, value []byte) {
	start, err := s1ap.ParseOverloadStart(value)
	if err != nil {
		g.rejectOverload(ctx, a, log, assoc.OverloadStart, err)
		return
	}

	gummeis, err := gummeiListFrom(start.GUMMEIList)
	if err != nil {
		g.rejectOverload(ctx, a, log, assoc.OverloadStart, err)
		return
	}

	action := overloadActions[*start.OverloadResponse.OverloadAction]

	var radios []*ranAssoc

	if !g.core.UpdateCore(ctx, a, func(c *mmeClient) bool {
		if len(gummeis) == 0 {
			o, _ := g.core.OperatorLocked(c.OperatorID())
			gummeis = mmeGUMMEIs(c.Info.served, o.PLMNs)
		}

		for _, id := range gummeis {
			entry := Overload{GUMMEI: id, Action: action, TrafficLoadReductionIndication: start.TrafficLoadReductionIndication}

			if i := slices.IndexFunc(c.Info.overload, func(o Overload) bool { return o.GUMMEI == id }); i >= 0 {
				c.Info.overload[i] = entry
			} else {
				c.Info.overload = append(c.Info.overload, entry)
			}
		}

		radios = g.core.OperatorRadiosLocked(c.OperatorID())

		return false
	}) {
		return
	}

	list, err := gummeiListTo(gummeis)
	if err != nil || len(list) == 0 {
		g.rejectOverload(ctx, a, log, assoc.OverloadStart, errors.Join(errors.New("no GUMMEI of the MME"), err))
		return
	}

	out, err := (&s1ap.OverloadStart{
		OverloadResponse: start.OverloadResponse, GUMMEIList: list,
		TrafficLoadReductionIndication: start.TrafficLoadReductionIndication,
	}).Marshal()
	if err != nil {
		g.core.CountOverload(assoc.OverloadStart, assoc.OverloadInvalid)
		tracing.SpanFailed(ctx, assoc.OverloadInvalid)
		log.ErrorContext(ctx, "failed to marshal Overload Start", slog.Any("error", err))

		return
	}

	log = log.With(slog.String("overload_action", string(action)), slog.Int("gummeis", len(list)))

	if len(radios) == 0 {
		g.core.CountOverload(assoc.OverloadStart, assoc.OverloadNoRadio)
		tracing.SpanFailed(ctx, assoc.OverloadNoRadio)
		log.InfoContext(ctx, "Overload Start from MME, no radio of the operator is up")

		return
	}

	g.core.CountOverload(assoc.OverloadStart, assoc.OverloadRelayed)
	log.InfoContext(ctx, "Overload Start from MME relayed to radios", slog.Int("radios", len(radios)))
	assoc.SendRadios(ctx, radios, a.Operator(), log, "Overload Start", out)
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
			OverloadResponse: s1ap.OverloadResponse{OverloadAction: s1ap.Ptr(gr.action)}, GUMMEIList: list,
			TrafficLoadReductionIndication: gr.tlri,
		})
	}

	return out, nil
}

// TS 36.413 clause 8.7.3.1
// TS 36.413 clause 8.7.4
// TS 36.413 clause 8.7.6
func (g *Gateway) replayOverloadLocked(ctx context.Context, n *ranAssoc, operators []int64) {
	for c := range g.core.ClientsLocked() {
		if len(c.Info.overload) == 0 || (operators != nil && !slices.Contains(operators, c.OperatorID())) {
			continue
		}

		o, ok := g.core.OperatorLocked(c.OperatorID())
		if !ok || !n.Setup.Broadcasts(o.PLMNs) {
			continue
		}

		log := n.Logger(c.Logger())

		starts, err := overloadStarts(c.Info.overload)
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

			if err := n.For(o.Name).Send(ctx, out, assoc.NonUEStream); err != nil {
				log.WarnContext(ctx, "failed to send Overload Start to radio", slog.Any("error", err))
				continue
			}

			g.core.CountOverload(assoc.OverloadStart, assoc.OverloadRelayed)
			log.InfoContext(ctx, "ongoing MME overload sent to radio", slog.Int("gummeis", len(start.GUMMEIList)))
		}
	}
}

// TS 36.413 clause 8.7.7
// TS 36.300 clause 4.6.2
// TS 36.300 clause 19.2.2.12.2
func (g *Gateway) overloadStop(ctx context.Context, a *mmeAssoc, log *slog.Logger, value []byte) {
	stop, err := s1ap.ParseOverloadStop(value)
	if err != nil {
		g.rejectOverload(ctx, a, log, assoc.OverloadStop, err)
		return
	}

	gummeis, err := gummeiListFrom(stop.GUMMEIList)
	if err != nil {
		g.rejectOverload(ctx, a, log, assoc.OverloadStop, err)
		return
	}

	var (
		ended  []GUMMEI
		radios []*ranAssoc
	)

	if !g.core.UpdateCore(ctx, a, func(c *mmeClient) bool {
		ended = endOverload(c, func(id GUMMEI) bool { return len(gummeis) == 0 || slices.Contains(gummeis, id) })
		radios = g.core.OperatorRadiosLocked(c.OperatorID())

		return false
	}) {
		return
	}

	if len(ended) == 0 {
		g.core.CountOverload(assoc.OverloadStop, assoc.OverloadIgnored)
		log.InfoContext(ctx, "Overload Stop from MME ignored: no ongoing overload action")

		return
	}

	if len(radios) == 0 {
		g.core.CountOverload(assoc.OverloadStop, assoc.OverloadNoRadio)
		tracing.SpanFailed(ctx, assoc.OverloadNoRadio)
		log.InfoContext(ctx, "Overload Stop from MME, no radio of the operator is up", slog.Int("gummeis", len(ended)))

		return
	}

	g.core.CountOverload(assoc.OverloadStop, assoc.OverloadRelayed)
	log.InfoContext(ctx, "Overload Stop from MME relayed to radios", slog.Int("gummeis", len(ended)),
		slog.Int("radios", len(radios)))
	g.sendOverloadStop(ctx, radios, a.Operator(), log, ended)
}

func endOverload(c *mmeClient, ends func(GUMMEI) bool) []GUMMEI {
	var ended []GUMMEI

	c.Info.overload = slices.DeleteFunc(c.Info.overload, func(o Overload) bool {
		if ends(o.GUMMEI) {
			ended = append(ended, o.GUMMEI)
			return true
		}

		return false
	})

	return ended
}

// TS 36.413 clause 8.7.3.1
func (g *Gateway) overloadCleared(ctx context.Context, radios []*ranAssoc, operator string, log *slog.Logger,
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

func (g *Gateway) sendOverloadStop(ctx context.Context, radios []*ranAssoc, operator string, log *slog.Logger,
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

	assoc.SendRadios(ctx, radios, operator, log, "Overload Stop", out)
}

// TS 36.300 clause 4.6.2
// TS 36.413 clause 9.1.4.1
func (g *Gateway) servingGUMMEI(a *mmeAssoc, plmn db.PLMN) (id GUMMEI, ok bool) {
	g.core.View(func() {
		for c := range g.core.ClientsLocked() {
			if c.Assoc != a {
				continue
			}

			for _, s := range c.Info.served {
				if slices.Contains(s.PLMNs, plmn) && len(s.GroupIDs) > 0 && len(s.MMECodes) > 0 {
					id, ok = GUMMEI{PLMN: plmn, MMEGroupID: s.GroupIDs[0], MMECode: s.MMECodes[0]}, true
					return
				}
			}
		}
	})

	return id, ok
}

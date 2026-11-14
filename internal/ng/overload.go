package ng

import (
	"context"
	"errors"
	"log/slog"
	"slices"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/tracing"
)

const maxnoofGUAMIs = 1024

var overloadProcedures = map[string]ngap.ProcedureCode{
	assoc.OverloadStart: ngap.ProcOverloadStart,
	assoc.OverloadStop:  ngap.ProcOverloadStop,
}

// TS 38.413 clause 9.3.1.105
var overloadActions = map[ngap.OverloadAction]OverloadAction{
	ngap.OverloadActionRejectNonEmergencyMODT:                      OverloadRejectNonEmergencyMODT,
	ngap.OverloadActionRejectRRCCRSignalling:                       OverloadRejectRRCCRSignalling,
	ngap.OverloadActionPermitEmergencySessionsAndMTServicesOnly:    OverloadPermitEmergencyAndMTOnly,
	ngap.OverloadActionPermitHighPrioritySessionsAndMTServicesOnly: OverloadPermitHighPriorityAndMTOnly,
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

func overloadResponseTo(a *OverloadAction) *ngap.OverloadResponse {
	if a == nil {
		return nil
	}

	for k, v := range overloadActions {
		if v == *a {
			return &ngap.OverloadResponse{OverloadAction: k}
		}
	}

	return nil
}

func loadReduction(t *ngap.TrafficLoadReductionIndication) *uint8 {
	if t == nil {
		return nil
	}

	return new(uint8(*t))
}

func loadReductionTo(t *uint8) *ngap.TrafficLoadReductionIndication {
	if t == nil {
		return nil
	}

	return new(ngap.TrafficLoadReductionIndication(*t))
}

func guamiListFrom(in ngap.GUAMIList) ([]GUAMI, error) {
	out := make([]GUAMI, 0, len(in))

	for _, item := range in {
		g, err := guamiFrom(item.GUAMI)
		if err != nil {
			return nil, err
		}

		if !slices.Contains(out, g) {
			out = append(out, g)
		}
	}

	return out, nil
}

func guamiListTo(in []GUAMI) (ngap.GUAMIList, error) {
	out := make(ngap.GUAMIList, 0, len(in))

	for _, g := range in {
		v, err := guamiTo(g)
		if err != nil {
			return nil, err
		}

		out = append(out, ngap.GUAMIListItem{GUAMI: v})
	}

	return out, nil
}

// TS 38.413 clause 9.2.6.14
func overloadFrom(start *ngap.OverloadStart) Overload {
	o := Overload{
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

	return o
}

// TS 38.413 clause 9.2.6.14
func overloadStartsTo(o Overload) ([]*ngap.OverloadStart, error) {
	var nssai ngap.OverloadStartNSSAIList

	for _, s := range o.Slices {
		item := ngap.OverloadStartNSSAIItem{
			SliceOverloadResponse:               overloadResponseTo(s.Action),
			SliceTrafficLoadReductionIndication: loadReductionTo(s.TrafficLoadReductionIndication),
		}

		for _, sn := range s.SNSSAIs {
			v, err := snssaiTo(sn)
			if err != nil {
				return nil, err
			}

			item.SliceOverloadList = append(item.SliceOverloadList, ngap.SliceOverloadItem{SNSSAI: v})
		}

		nssai = append(nssai, item)
	}

	var out []*ngap.OverloadStart

	for chunk := range slices.Chunk(o.GUAMIs, maxnoofGUAMIs) {
		list, err := guamiListTo(chunk)
		if err != nil {
			return nil, err
		}

		out = append(out, &ngap.OverloadStart{
			AMFOverloadResponse:               overloadResponseTo(o.Action),
			AMFTrafficLoadReductionIndication: loadReductionTo(o.TrafficLoadReductionIndication),
			OverloadStartNSSAIList:            nssai,
			GUAMIList:                         list,
		})
	}

	return out, nil
}

// TS 38.300 clause 4.10.2.3
func operatorGUAMIs(served []GUAMI, plmns []db.PLMN) []GUAMI {
	var out []GUAMI

	for _, g := range served {
		if slices.Contains(plmns, g.PLMN) && !slices.Contains(out, g) {
			out = append(out, g)
		}
	}

	return out
}

func withoutGUAMIs(groups []Overload, gone func(GUAMI) bool) ([]Overload, []GUAMI) {
	var (
		out   []Overload
		ended []GUAMI
	)

	for _, o := range groups {
		kept := slices.DeleteFunc(slices.Clone(o.GUAMIs), func(g GUAMI) bool {
			if gone(g) {
				ended = append(ended, g)
				return true
			}

			return false
		})

		if len(kept) > 0 {
			o.GUAMIs = kept
			out = append(out, o)
		}
	}

	return out, ended
}

func (g *Gateway) rejectOverload(ctx context.Context, a *amfAssoc, log *slog.Logger, proc string, err error) {
	g.core.CountOverload(proc, assoc.OverloadInvalid)
	tracing.SpanFailed(ctx, assoc.OverloadInvalid)
	log.WarnContext(ctx, "rejected "+proc+" from AMF", slog.Any("error", err))

	if codecError(err) {
		indicateParseError(ctx, a, log, overloadProcedures[proc], err)
	}
}

// TS 38.413 clause 8.7.7
// TS 38.300 clause 4.10.2.3
func (g *Gateway) overloadStart(ctx context.Context, a *amfAssoc, log *slog.Logger, value []byte) {
	start, err := ngap.ParseOverloadStart(value)
	if err != nil {
		g.rejectOverload(ctx, a, log, assoc.OverloadStart, err)
		return
	}

	guamis, err := guamiListFrom(start.GUAMIList)
	if err != nil {
		g.rejectOverload(ctx, a, log, assoc.OverloadStart, err)
		return
	}

	o := overloadFrom(start)

	var radios []*ranAssoc

	if !g.core.UpdateCore(ctx, a, func(c *amfClient) bool {
		if len(guamis) == 0 {
			op, _ := g.core.OperatorLocked(c.OperatorID())
			guamis = operatorGUAMIs(c.Info.served, op.PLMNs)
		}

		if len(guamis) == 0 {
			return false
		}

		o.GUAMIs = guamis
		kept, _ := withoutGUAMIs(c.Info.overload, func(v GUAMI) bool { return slices.Contains(guamis, v) })
		c.Info.overload = append(kept, o)
		radios = g.core.OperatorRadiosLocked(c.OperatorID())

		return false
	}) {
		return
	}

	if len(guamis) == 0 {
		g.rejectOverload(ctx, a, log, assoc.OverloadStart, errors.New("no GUAMI of the AMF"))
		return
	}

	starts, err := overloadStartsTo(o)
	if err != nil {
		g.core.CountOverload(assoc.OverloadStart, assoc.OverloadInvalid)
		tracing.SpanFailed(ctx, assoc.OverloadInvalid)
		log.ErrorContext(ctx, "failed to build Overload Start", slog.Any("error", err))

		return
	}

	log = log.With(slog.Any("overload_action", o.Action), slog.Int("guamis", len(guamis)), slog.Int("slices", len(o.Slices)))

	if len(radios) == 0 {
		g.core.CountOverload(assoc.OverloadStart, assoc.OverloadNoRadio)
		tracing.SpanFailed(ctx, assoc.OverloadNoRadio)
		log.InfoContext(ctx, "Overload Start from AMF, no radio of the operator is up")

		return
	}

	g.core.CountOverload(assoc.OverloadStart, assoc.OverloadRelayed)
	log.InfoContext(ctx, "Overload Start from AMF relayed to radios", slog.Int("radios", len(radios)))

	for _, s := range starts {
		s.OverloadStartNSSAIList = start.OverloadStartNSSAIList
		g.sendOverload(ctx, radios, a.Operator(), log, "Overload Start", s)
	}
}

// TS 38.413 clause 8.7.1.1
// TS 38.413 clause 8.7.2
// TS 38.413 clause 8.7.7
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

		for _, group := range c.Info.overload {
			starts, err := overloadStartsTo(group)
			if err != nil {
				log.ErrorContext(ctx, "failed to build Overload Start", slog.Any("error", err))
				continue
			}

			for _, s := range starts {
				g.sendOverload(ctx, []*ranAssoc{n}, o.Name, log, "Overload Start", s)
				g.core.CountOverload(assoc.OverloadStart, assoc.OverloadRelayed)
			}

			log.InfoContext(ctx, "ongoing AMF overload sent to radio", slog.Int("guamis", len(group.GUAMIs)))
		}
	}
}

// TS 38.413 clause 8.7.8
// TS 38.300 clause 4.10.2.3
func (g *Gateway) overloadStop(ctx context.Context, a *amfAssoc, log *slog.Logger, value []byte) {
	stop, err := ngap.ParseOverloadStop(value)
	if err != nil {
		g.rejectOverload(ctx, a, log, assoc.OverloadStop, err)
		return
	}

	guamis, err := guamiListFrom(stop.GUAMIList)
	if err != nil {
		g.rejectOverload(ctx, a, log, assoc.OverloadStop, err)
		return
	}

	var (
		ended  []GUAMI
		radios []*ranAssoc
	)

	if !g.core.UpdateCore(ctx, a, func(c *amfClient) bool {
		c.Info.overload, ended = withoutGUAMIs(c.Info.overload, func(v GUAMI) bool {
			return len(guamis) == 0 || slices.Contains(guamis, v)
		})
		radios = g.core.OperatorRadiosLocked(c.OperatorID())

		return false
	}) {
		return
	}

	if len(ended) == 0 {
		g.core.CountOverload(assoc.OverloadStop, assoc.OverloadIgnored)
		log.InfoContext(ctx, "Overload Stop from AMF ignored: no ongoing overload action")

		return
	}

	if len(radios) == 0 {
		g.core.CountOverload(assoc.OverloadStop, assoc.OverloadNoRadio)
		tracing.SpanFailed(ctx, assoc.OverloadNoRadio)
		log.InfoContext(ctx, "Overload Stop from AMF, no radio of the operator is up", slog.Int("guamis", len(ended)))

		return
	}

	g.core.CountOverload(assoc.OverloadStop, assoc.OverloadRelayed)
	log.InfoContext(ctx, "Overload Stop from AMF relayed to radios", slog.Int("guamis", len(ended)),
		slog.Int("radios", len(radios)))
	g.sendOverloadStop(ctx, radios, a.Operator(), log, ended)
}

// TS 38.413 clause 8.7.1.1
func (g *Gateway) overloadCleared(ctx context.Context, radios []*ranAssoc, operator string, log *slog.Logger,
	cleared []Overload,
) {
	var ended []GUAMI

	for _, o := range cleared {
		ended = append(ended, o.GUAMIs...)
	}

	if len(ended) == 0 || len(radios) == 0 {
		return
	}

	log.InfoContext(ctx, "AMF overload state cleared, Overload Stop sent to radios", slog.Int("guamis", len(ended)),
		slog.Int("radios", len(radios)))
	g.sendOverloadStop(ctx, radios, operator, log, ended)
}

func (g *Gateway) sendOverloadStop(ctx context.Context, radios []*ranAssoc, operator string, log *slog.Logger,
	ended []GUAMI,
) {
	for chunk := range slices.Chunk(ended, maxnoofGUAMIs) {
		list, err := guamiListTo(chunk)
		if err != nil {
			log.ErrorContext(ctx, "failed to encode the GUAMI List", slog.Any("error", err))
			return
		}

		g.sendOverload(ctx, radios, operator, log, "Overload Stop", &ngap.OverloadStop{GUAMIList: list})
	}
}

func (g *Gateway) sendOverload(ctx context.Context, radios []*ranAssoc, operator string, log *slog.Logger, what string,
	msg sendable,
) {
	out, err := msg.Marshal()
	if err != nil {
		log.ErrorContext(ctx, "failed to marshal "+what, slog.Any("error", err))
		return
	}

	assoc.SendRadios(ctx, radios, operator, log, what, out)
}

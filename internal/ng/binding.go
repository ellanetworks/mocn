package ng

import (
	"context"
	"log/slog"
	"slices"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/tracing"
)

func (r *relay) setSets(a *amfAssoc, served []GUAMI) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !a.Lost() {
		r.sets[a] = setsOf(served)
	}
}

// TS 23.502 clause 4.2.7.3
// TS 23.501 clause 5.21.2.3
func (r *relay) amfLost(ctx context.Context, from *amfAssoc, backups map[GUAMI]amfName) {
	r.mu.Lock()
	from.MarkLost()
	delete(r.sets, from)

	var (
		drop []*ueContext
		kept int
	)

	for _, u := range r.ues.Where(func(u *ueContext) bool { return u.Core == from }) {
		if !u.CoreIDKnown {
			drop = append(drop, u)
			continue
		}

		u.Ext.unbound = true
		if u.Ext.guami != nil {
			u.Ext.backup = backups[*u.Ext.guami]
		}

		kept++
	}

	gone := r.ues.Remove(drop...)
	r.mu.Unlock()

	log := from.Logger(r.log)

	if kept > 0 {
		log.InfoContext(ctx, "AMF lost, NGAP UE-TNLA-bindings released", slog.Int("ues", kept))
	}

	if len(gone) > 0 {
		log.InfoContext(ctx, "AMF lost, UE contexts awaiting its first answer released", slog.Int("ues", len(gone)))
	}

	r.resetRANs(ctx, from.Operator(), gone, causeTransportUnavailable)
}

// TS 23.502 clause 4.2.7.3
// TS 23.501 clause 5.21.2.2.1
func (r *relay) unbind(ctx context.Context, a *amfAssoc, guamis []GUAMI, drained bool) {
	r.mu.Lock()

	released := 0

	for _, u := range r.ues.Where(func(u *ueContext) bool { return u.Core == a && u.CoreIDKnown && !u.Ext.unbound }) {
		g := u.Ext.guami
		if (g != nil && slices.Contains(guamis, *g)) || (g == nil && drained) {
			u.Ext.unbound = true
			released++
		}
	}

	r.mu.Unlock()

	if released > 0 {
		a.Logger(r.log).InfoContext(ctx, "GUAMIs unavailable, NGAP UE-TNLA-bindings released", slog.Int("ues", released))
	}
}

// TS 38.413 clause 8.13.1
// TS 23.502 clause 4.2.7.2.5
func (r *relay) tnlaBindingRelease(ctx context.Context, a *amfAssoc, log *slog.Logger, value []byte) {
	log = log.With(slog.String("procedure", ngap.ProcUETNLABindingRelease.String()))

	req, err := ngap.ParseUETNLABindingReleaseRequest(value)
	if err != nil {
		tracing.SpanFailed(ctx, assoc.ErrTypeInvalid)
		log.WarnContext(ctx, "rejected UE TNLA Binding Release Request from AMF", slog.Any("error", err))

		indicateParseError(ctx, a, log, ngap.ProcUETNLABindingRelease, err)

		return
	}

	ids := ueIDs{amf: &req.AMFUENGAPID, ran: &req.RANUENGAPID}

	r.mu.Lock()

	u, others, cause := r.amfUE(a, ids)
	if u == nil {
		gone := r.ues.Remove(append(others, r.ues.OnCore(a, ids.amf, ids.ran)...)...)
		r.mu.Unlock()
		r.unknownFromAMF(ctx, a, log, ids, gone, cause)

		return
	}

	u.Ext.unbound = true
	r.mu.Unlock()

	log.InfoContext(ctx, "NGAP UE-TNLA-binding released by AMF", slog.Uint64("amf_ran_ue_ngap_id", uint64(u.CoreRANID)))
}

// TS 38.413 clause 8.7.1.1
// TS 38.413 clause 8.7.1.2
func (r *relay) notRetained(ctx context.Context, a *amfAssoc) {
	r.mu.Lock()
	gone := r.ues.Remove(r.ues.Where(func(u *ueContext) bool { return u.Core != nil && u.Core != a && u.Core.Peer == a.Peer })...)
	r.mu.Unlock()

	if len(gone) == 0 {
		return
	}

	a.Logger(r.log).InfoContext(ctx, "AMF did not retain the UE contexts at NG Setup, UE contexts released",
		slog.Int("ues", len(gone)))
	r.resetRANs(ctx, a.Operator(), gone, causeMiscUnspecified)
}

func (r *relay) bindingOf(u *ueContext) binding {
	return binding{current: u.Core, guami: u.Ext.guami, backup: u.Ext.backup, sets: u.Ext.sets}
}

func (r *relay) rebindTarget(operatorID int64, b binding) *amfAssoc {
	var cands []amfCandidate

	r.g.core.View(func() { cands = selectable(r.g.candidatesLocked(operatorID)) })

	return rebindAMF(cands, b, r.intN)
}

// TS 23.502 clause 4.2.7.2.3
// TS 23.501 clause 5.21.2.2.1
// TS 23.501 clause 5.21.2.3
func (r *relay) rebind(ctx context.Context, log *slog.Logger, u *ueContext) bool {
	r.mu.Lock()

	if r.ues.ByRANCoreID(u.RANCoreID) != u {
		r.mu.Unlock()
		return false
	}

	if !u.Ext.unbound && !u.Core.Lost() {
		r.mu.Unlock()
		return true
	}

	b, operatorID, id := r.bindingOf(u), u.OperatorID, u.CoreID
	r.mu.Unlock()

	target := r.rebindTarget(operatorID, b)

	r.mu.Lock()

	if r.ues.ByRANCoreID(u.RANCoreID) != u {
		r.mu.Unlock()
		return false
	}

	if !u.Ext.unbound && !u.Core.Lost() {
		r.mu.Unlock()
		return true
	}

	if target != nil && target.Lost() {
		target = nil
	}

	if target == nil || (r.ues.ByCoreID(target, id) != nil && r.ues.ByCoreID(target, id) != u) {
		gone := r.ues.Remove(u)
		r.mu.Unlock()

		log.WarnContext(ctx, "no AMF of the AMF set to bind the UE to, UE context released")
		r.resetAMFs(ctx, gone, causeTransportUnavailable)
		r.resetRANs(ctx, b.current.Operator(), gone, causeTransportUnavailable)

		return false
	}

	if target != u.Core {
		r.ues.Rebind(u, target)
		r.ues.SetCoreID(u, id)
	}

	u.Ext.sets, u.Ext.unbound, u.Ext.backup = r.sets[target], false, amfName{}
	r.mu.Unlock()

	log.InfoContext(ctx, "NGAP UE-TNLA-binding created", slog.Any("amf", target.Addrs()), slog.Any("from", b.current.Addrs()))

	return true
}

// TS 38.413 clause 8.3.1.2
// TS 38.413 clause 8.3.4.2
// TS 23.502 clause 4.2.7.2.3
func storeGUAMI(u *ueContext, p *ngap.RawPDU) {
	if p.Trigger() != ngap.TriggeringInitiatingMessage {
		return
	}

	var id ngap.ProtocolIEID

	switch p.ProcedureCode() {
	case ngap.ProcInitialContextSetup:
		id = ngap.IDGUAMI
	case ngap.ProcUEContextModification:
		id = ngap.IDNewGUAMI
	default:
		return
	}

	if g, err := assoc.GetIE[ngap.GUAMI](p.Get, id); err == nil && g != nil {
		if v, err := guamiFrom(*g); err == nil {
			u.Ext.guami = &v
		}
	}
}

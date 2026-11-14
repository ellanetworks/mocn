package ng

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/plmnid"
	"github.com/ellanetworks/mocn/internal/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// TS 38.413 clause 8.6.1.2
// TS 38.300 clause 4.10.2.3
func identityOf(ctx context.Context, log *slog.Logger, p *ngap.RawPDU, plmn db.PLMN) ueIdentity {
	id := ueIdentity{plmn: plmn}

	if g, err := assoc.GetIE[ngap.GUAMI](p.Get, ngap.IDGUAMI); err != nil {
		log.WarnContext(ctx, "ignored invalid GUAMI in INITIAL UE MESSAGE", slog.Any("error", err))
	} else if g != nil {
		if v, err := guamiFrom(*g); err == nil {
			id.guami = &v
		}
	}

	if t, err := assoc.GetIE[ngap.GUAMIType](p.Get, ngap.IDGUAMIType); err != nil {
		log.WarnContext(ctx, "ignored invalid GUAMI Type in INITIAL UE MESSAGE", slog.Any("error", err))
	} else {
		id.guamiType = t
	}

	if n, err := assoc.GetIE[ngap.RequestedNSSAI](p.Get, ngap.IDRequestedNSSAI); err != nil {
		log.WarnContext(ctx, "ignored invalid Requested NSSAI in INITIAL UE MESSAGE", slog.Any("error", err))
	} else if n != nil {
		for _, item := range *n {
			id.nssai = append(id.nssai, snssaiFrom(item.SNSSAI))
		}
	}

	if s, err := assoc.GetIE[ngap.FiveGSTMSI](p.Get, ngap.IDFiveGSTMSI); err != nil {
		log.WarnContext(ctx, "ignored invalid 5G-S-TMSI in INITIAL UE MESSAGE", slog.Any("error", err))
	} else if s != nil {
		id.stmsi = &fiveGSTMSI{setID: uint16(s.AMFSetID), pointer: uint8(s.AMFPointer)}
	}

	return id
}

// TS 38.413 clause 10.3.5
func missingIEs(err error) bool {
	ase, ok := errors.AsType[*ngap.AbstractSyntaxError](err)
	if !ok || len(ase.IEs) == 0 {
		return false
	}

	for _, ie := range ase.IEs {
		if ie.TypeOfError != ngap.TypeOfErrorMissing {
			return false
		}
	}

	return true
}

// TS 38.413 clause 8.6.1
// TS 38.413 clause 10.2
// TS 38.413 clause 10.3.5
// TS 38.413 clause 10.4
// TS 38.300 clause 4.10.2.3
func (r *relay) initialUEMessage(ctx context.Context, n *ranAssoc, log *slog.Logger, value, msg []byte) {
	invalid := func(reason string, err error) {
		r.rejectInitialUE(ctx, log, assoc.InitialUEInvalid, reason, err)

		if _, perr := ngap.ParseInitialUEMessage(value); missingIEs(perr) {
			indicateParseError(ctx, n, log, ngap.ProcInitialUEMessage, perr)
			return
		}

		indicateTransferSyntax(ctx, n, log, &ngap.InitiatingMessage{ProcedureCode: ngap.ProcInitialUEMessage})
	}

	p, err := ngap.ParseRawPDU(msg)
	if err != nil {
		invalid("undecodable INITIAL UE MESSAGE", err)
		return
	}

	ranID, hasRANID, err := p.RANUENGAPID()
	if err != nil || !hasRANID {
		invalid("INITIAL UE MESSAGE without a valid RAN UE NGAP ID", err)
		return
	}

	uli, err := assoc.GetIE[ngap.UserLocationInformation](p.Get, ngap.IDUserLocationInformation)
	if err != nil || uli == nil {
		invalid("INITIAL UE MESSAGE without a valid User Location Information", err)
		return
	}

	if _, err := ngap.ParseInitialUEMessage(value); missingIEs(err) {
		r.rejectInitialUE(ctx, log, assoc.InitialUEInvalid, "INITIAL UE MESSAGE without a mandatory IE", err)
		indicateParseError(ctx, n, log, ngap.ProcInitialUEMessage, err)

		return
	}

	semantic := func(reason string, err error) {
		r.rejectInitialUE(ctx, log, assoc.InitialUEInvalid, reason, err)
		indicate(ctx, n, log, ueIDs{ran: &ranID}, causeSemanticError, diagnosticsOf(&ngap.InitiatingMessage{
			ProcedureCode: ngap.ProcInitialUEMessage,
		}, nil))
	}

	if uli.Kind != ngap.UserLocationNR {
		semantic("INITIAL UE MESSAGE without an NR User Location Information", nil)
		return
	}

	plmn, err := plmnid.Decode(uli.TAI.PLMNIdentity)
	if err != nil {
		semantic("INITIAL UE MESSAGE with an invalid PLMN", err)
		return
	}

	id := identityOf(ctx, log, p, plmn)

	var (
		op    assoc.Operator
		cands []amfCandidate
		found bool
		radio db.GlobalGNBID
	)

	r.g.core.View(func() {
		op, cands, found = r.g.selectionLocked(plmn)

		if n.Setup != nil {
			radio = n.Setup.globalGNBID
		}
	})

	log = log.With(slog.Uint64("ran_ue_ngap_id", uint64(ranID)), slog.String("plmn", plmn.String()))

	r.mu.Lock()

	if old := r.ues.ByRANID(n, ranID); old != nil {
		gone := r.ues.Remove(old)
		r.mu.Unlock()

		r.countInitialUE(assoc.InitialUERejected)
		tracing.SpanFailed(ctx, assoc.InitialUERejected)
		log.WarnContext(ctx, "INITIAL UE MESSAGE reuses an allocated RAN UE NGAP ID")
		sendUEErrorIndication(ctx, n, log, causeInconsistentRemoteID, nil, &ranID)
		r.resetAMFs(ctx, gone, causeInconsistentRemoteID)

		return
	}

	if !found {
		r.mu.Unlock()
		r.countInitialUE(assoc.InitialUEUnknownPLMN)
		tracing.SpanFailed(ctx, assoc.InitialUEUnknownPLMN)
		log.WarnContext(ctx, "INITIAL UE MESSAGE not routed: no operator serves the PLMN")
		r.releaseUnrouted(ctx, n, log, ranID, causeUnknownPLMN)

		return
	}

	trace.SpanFromContext(ctx).SetAttributes(attribute.String(assoc.AttrOperator, op.Name))

	amf, rule := selectAMF(selectable(cands), id, r.intN)
	if amf == nil {
		r.mu.Unlock()
		r.countInitialUE(names.NoCore)
		tracing.SpanFailed(ctx, names.NoCore)
		log.WarnContext(ctx, "INITIAL UE MESSAGE not routed: no selectable AMF of the operator", slog.String("operator", op.Name))
		r.releaseUnrouted(ctx, n, log, ranID, causeTransportUnavailable)

		return
	}

	u := &ueContext{
		RAN: n, RANID: ranID, RANIDKnown: true, Core: amf, OperatorID: op.ID, PLMN: plmn, Since: r.g.cfg.Now(),
		Ext: ueInfo{radio: radio, sets: r.sets[amf]},
	}

	r.updateLocation(u, p)

	if err := r.ues.Add(u); err != nil {
		r.mu.Unlock()
		r.rejectInitialUE(ctx, log, assoc.InitialUEError, "dropped INITIAL UE MESSAGE", err)

		return
	}

	out, err := rewrite(p, nil, &u.CoreRANID)
	if err != nil {
		r.ues.Remove(u)
		r.mu.Unlock()
		r.rejectInitialUE(ctx, log, assoc.InitialUEError, "failed to relay INITIAL UE MESSAGE", err)

		return
	}

	r.superviseLocked(ctx, u, log, "INITIAL UE MESSAGE")

	send := r.sendAMF
	r.mu.Unlock()

	if err := send(ctx, amf, out, ueStream); err != nil {
		r.countInitialUE(assoc.InitialUEError)
		tracing.SpanFailed(ctx, assoc.InitialUEError)
		log.WarnContext(ctx, "failed to send INITIAL UE MESSAGE to AMF", slog.Any("error", err))

		r.mu.Lock()
		removed := len(r.ues.Remove(u)) > 0
		r.mu.Unlock()

		if removed {
			r.releaseUnrouted(ctx, n, log, ranID, causeTransportUnavailable)
		}

		return
	}

	r.countInitialUE(assoc.InitialUERelayed)
	log.InfoContext(ctx, "INITIAL UE MESSAGE relayed", slog.String("operator", op.Name), slog.Any("amf", amf.Addrs()),
		slog.String("selected_by", rule), slog.Uint64("amf_ran_ue_ngap_id", uint64(u.CoreRANID)))
}

func selectable(cands []amfCandidate) []amfCandidate {
	return slices.DeleteFunc(cands, func(c amfCandidate) bool { return c.assoc.Lost() })
}

// TS 38.413 clause 8.3.3
// TS 38.413 clause 8.7.4
func (r *relay) releaseUnrouted(ctx context.Context, n *ranAssoc, log *slog.Logger, ranID ngap.RANUENGAPID,
	cause ngap.Cause,
) {
	u := &ueContext{RAN: n, RANID: ranID, RANIDKnown: true}

	r.mu.Lock()

	if err := r.ues.Add(u); err != nil {
		r.mu.Unlock()
		log.WarnContext(ctx, "failed to release the UE", slog.Any("error", err))

		return
	}

	r.mu.Unlock()

	r.releaseRadioSide(ctx, n, log, u, cause)
}

// TS 38.413 clause 8.3.3
func (r *relay) releaseRadioSide(ctx context.Context, n *ranAssoc, log *slog.Logger, u *ueContext, cause ngap.Cause) {
	r.mu.Lock()
	ranID := u.RANID
	r.mu.Unlock()

	out, err := (&ngap.UEContextReleaseCommand{
		UENGAPIDs: ngap.UENGAPIDs{AMFUENGAPID: u.RANCoreID, RANUENGAPID: ranID, Pair: true},
		Cause:     &cause,
	}).Marshal()
	if err != nil {
		r.mu.Lock()
		r.ues.Remove(u)
		r.mu.Unlock()
		log.ErrorContext(ctx, "failed to marshal UE Context Release Command", slog.Any("error", err))

		return
	}

	log = log.With(slog.Uint64("amf_ue_ngap_id", uint64(u.RANCoreID)))
	timerCtx := tracing.DetachedRoot(ctx)

	r.mu.Lock()
	u.Release = time.AfterFunc(r.g.cfg.ResponseTimeout, func() {
		r.mu.Lock()
		pending := r.ues.ByRANCoreID(u.RANCoreID) == u
		r.mu.Unlock()

		if !pending {
			return
		}

		log.WarnContext(timerCtx, "no UE Context Release Complete from radio, UE-associated logical NG-connection reset")
		sendResets(timerCtx, n, log, sourceRadio, causeRadioUnspec,
			[]ngap.UEAssociatedLogicalNGConnectionItem{{AMFUENGAPID: ngap.Ptr(u.RANCoreID), RANUENGAPID: ngap.Ptr(ranID)}})

		r.mu.Lock()
		r.ues.Remove(u)
		r.mu.Unlock()
	})
	r.mu.Unlock()

	if err := n.Send(ctx, out, ueStream); err != nil {
		r.mu.Lock()
		r.ues.Remove(u)
		r.mu.Unlock()
		log.WarnContext(ctx, "failed to send UE Context Release Command", slog.Any("error", err))

		return
	}

	log.InfoContext(ctx, "UE Context Release Command sent to radio", slog.String("cause", cause.String()))
}

func (r *relay) superviseLocked(ctx context.Context, u *ueContext, log *slog.Logger, request string) {
	if u.Release != nil {
		u.Release.Stop()
	}

	ctx = tracing.DetachedRoot(ctx)
	u.Release = time.AfterFunc(r.g.cfg.ResponseTimeout, func() {
		r.mu.Lock()

		if r.ues.ByRANCoreID(u.RANCoreID) != u || u.CoreIDKnown {
			r.mu.Unlock()
			return
		}

		gone := r.ues.Remove(u)
		r.mu.Unlock()

		log.WarnContext(ctx, "no answer from AMF to "+request+", UE context released",
			slog.Uint64("amf_ran_ue_ngap_id", uint64(u.CoreRANID)))
		r.resetAMFs(ctx, gone, causeRadioUnspec)
		r.resetRANs(ctx, u.Core.Operator(), gone, causeRadioUnspec)
	})
}

func (r *relay) rejectInitialUE(ctx context.Context, log *slog.Logger, result, reason string, err error) {
	r.countInitialUE(result)
	tracing.SpanFailed(ctx, result)
	log.WarnContext(ctx, reason, slog.Any("error", err))
}

// TS 38.413 clause 9.3.1.16
func (r *relay) updateLocation(u *ueContext, p *ngap.RawPDU) {
	uli, err := assoc.GetIE[ngap.UserLocationInformation](p.Get, ngap.IDUserLocationInformation)
	if err != nil || uli == nil || uli.Kind != ngap.UserLocationNR {
		return
	}

	if plmn, err := plmnid.Decode(uli.TAI.PLMNIdentity); err == nil {
		u.Ext.tai = &TAI{PLMN: plmn, TAC: uint32(uli.TAI.TAC)}
	}

	if plmn, err := plmnid.Decode(uli.PLMNIdentity); err == nil {
		u.Ext.nrcgi = &NRCGI{PLMN: plmn, CellID: uli.CellIdentity}
	}
}

package s1

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/plmnid"
	"github.com/ellanetworks/mocn/internal/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

func identityOf(ctx context.Context, log *slog.Logger, p *s1ap.RawPDU, plmn db.PLMN) ueIdentity {
	id := ueIdentity{plmn: plmn}

	var err error

	if id.gummei, err = assoc.GetIE[s1ap.GUMMEI](p.Get, s1ap.IDGUMMEI); err != nil {
		log.WarnContext(ctx, "ignored invalid GUMMEI in INITIAL UE MESSAGE", slog.Any("error", err))
	}

	if id.stmsi, err = assoc.GetIE[s1ap.STMSI](p.Get, s1ap.IDSTMSI); err != nil {
		log.WarnContext(ctx, "ignored invalid S-TMSI in INITIAL UE MESSAGE", slog.Any("error", err))
	}

	return id
}

// TS 36.413 clause 10.3.5
func missingIEs(err error) bool {
	ase, ok := errors.AsType[*s1ap.AbstractSyntaxError](err)
	if !ok || len(ase.IEs) == 0 {
		return false
	}

	for _, ie := range ase.IEs {
		if ie.TypeOfError != s1ap.TypeOfErrorMissing {
			return false
		}
	}

	return true
}

// TS 36.413 clause 8.6.2.1
// TS 36.413 clause 10.2
// TS 36.413 clause 10.3.5
func (r *relay) initialUEMessage(ctx context.Context, n *ranAssoc, log *slog.Logger, value, msg []byte) {
	invalid := func(reason string, err error) {
		r.rejectInitialUE(ctx, log, assoc.InitialUEInvalid, reason, err)

		if _, perr := s1ap.ParseInitialUEMessage(value); missingIEs(perr) {
			indicateParseError(ctx, n, log, s1ap.ProcInitialUEMessage, perr)
			return
		}

		indicateTransferSyntax(ctx, n, log, &s1ap.InitiatingMessage{ProcedureCode: s1ap.ProcInitialUEMessage})
	}

	p, err := s1ap.ParseRawPDU(msg)
	if err != nil {
		invalid("undecodable INITIAL UE MESSAGE", err)
		return
	}

	enbID, hasENBID, err := p.ENBUES1APID()
	if err != nil || !hasENBID {
		invalid("INITIAL UE MESSAGE without a valid eNB UE S1AP ID", err)
		return
	}

	tai, err := assoc.GetIE[s1ap.TAI](p.Get, s1ap.IDTAI)
	if err != nil || tai == nil {
		invalid("INITIAL UE MESSAGE without a valid TAI", err)
		return
	}

	if _, err := s1ap.ParseInitialUEMessage(value); missingIEs(err) {
		r.rejectInitialUE(ctx, log, assoc.InitialUEInvalid, "INITIAL UE MESSAGE without a mandatory IE", err)
		indicateParseError(ctx, n, log, s1ap.ProcInitialUEMessage, err)

		return
	}

	plmn, err := plmnid.Decode(tai.PLMNIdentity)
	if err != nil {
		r.rejectInitialUE(ctx, log, assoc.InitialUEInvalid, "INITIAL UE MESSAGE with an invalid PLMN", err)
		indicate(ctx, n, log, ueIDs{enb: &enbID}, causeSemanticError, diagnosticsOf(&s1ap.InitiatingMessage{
			ProcedureCode: s1ap.ProcInitialUEMessage,
		}, nil))

		return
	}

	id := identityOf(ctx, log, p, plmn)

	var (
		op    assoc.Operator
		cands []mmeCandidate
		found bool
		radio db.GlobalENBID
	)

	r.g.core.View(func() {
		op, cands, found = r.g.selectionLocked(plmn)

		if n.Setup != nil {
			radio = n.Setup.globalENBID
		}
	})

	log = log.With(slog.Uint64("enb_ue_s1ap_id", uint64(enbID)), slog.String("plmn", plmn.String()))

	r.mu.Lock()

	if old := r.ues.ByRANID(n, enbID); old != nil {
		gone := r.ues.Remove(old)
		r.mu.Unlock()

		r.countInitialUE(assoc.InitialUERejected)
		tracing.SpanFailed(ctx, assoc.InitialUERejected)
		log.WarnContext(ctx, "INITIAL UE MESSAGE reuses an allocated eNB UE S1AP ID")
		sendUEErrorIndication(ctx, n, log, causeUnknownENBUEID, nil, &enbID)
		r.resetMMEs(ctx, gone, causeUnknownENBUEID)

		return
	}

	if !found {
		r.mu.Unlock()
		r.countInitialUE(assoc.InitialUEUnknownPLMN)
		tracing.SpanFailed(ctx, assoc.InitialUEUnknownPLMN)
		log.WarnContext(ctx, "INITIAL UE MESSAGE not routed: no operator serves the PLMN")
		r.releaseUnrouted(ctx, n, log, enbID, causeUnknownPLMN)

		return
	}

	trace.SpanFromContext(ctx).SetAttributes(attribute.String(assoc.AttrOperator, op.Name))

	mme, rule := selectMME(selectable(cands), id, r.intN)
	if mme == nil {
		r.mu.Unlock()
		r.countInitialUE(names.NoCore)
		tracing.SpanFailed(ctx, names.NoCore)
		log.WarnContext(ctx, "INITIAL UE MESSAGE not routed: no selectable MME of the operator", slog.String("operator", op.Name))
		r.releaseUnrouted(ctx, n, log, enbID, causeTransportUnavailable)

		return
	}

	u := &ueContext{
		RAN: n, RANID: enbID, Core: mme, OperatorID: op.ID, PLMN: plmn, Since: r.g.cfg.Now(),
		Ext: ueInfo{radio: radio, tai: &TAI{PLMN: plmn, TAC: uint16(tai.TAC)}},
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

	r.superviseLocked(ctx, u, log)

	send := r.sendMME
	r.mu.Unlock()

	if err := send(ctx, mme, out, ueStream); err != nil {
		r.countInitialUE(assoc.InitialUEError)
		tracing.SpanFailed(ctx, assoc.InitialUEError)
		log.WarnContext(ctx, "failed to send INITIAL UE MESSAGE to MME", slog.Any("error", err))

		r.mu.Lock()
		removed := len(r.ues.Remove(u)) > 0
		r.mu.Unlock()

		if removed {
			r.releaseUnrouted(ctx, n, log, enbID, causeTransportUnavailable)
		}

		return
	}

	r.countInitialUE(assoc.InitialUERelayed)
	log.InfoContext(ctx, "INITIAL UE MESSAGE relayed", slog.String("operator", op.Name), slog.Any("mme", mme.Addrs()),
		slog.String("selected_by", rule), slog.Uint64("mme_enb_ue_s1ap_id", uint64(u.CoreRANID)))
}

func selectable(cands []mmeCandidate) []mmeCandidate {
	return slices.DeleteFunc(cands, func(c mmeCandidate) bool { return c.assoc.Lost() })
}

// TS 36.413 clause 8.3.3
// TS 36.413 clause 8.7.1
func (r *relay) releaseUnrouted(ctx context.Context, n *ranAssoc, log *slog.Logger, enbID s1ap.ENBUES1APID,
	cause s1ap.Cause,
) {
	u := &ueContext{RAN: n, RANID: enbID}

	r.mu.Lock()

	if err := r.ues.Add(u); err != nil {
		r.mu.Unlock()
		log.WarnContext(ctx, "failed to release the UE", slog.Any("error", err))

		return
	}

	out, err := (&s1ap.UEContextReleaseCommand{
		UES1APIDs: s1ap.UES1APIDs{MMEUES1APID: u.RANCoreID, ENBUES1APID: enbID, Pair: true},
		Cause:     &cause,
	}).Marshal()
	if err != nil {
		r.ues.Remove(u)
		r.mu.Unlock()
		log.ErrorContext(ctx, "failed to marshal UE Context Release Command", slog.Any("error", err))

		return
	}

	log = log.With(slog.Uint64("mme_ue_s1ap_id", uint64(u.RANCoreID)))
	timerCtx := tracing.DetachedRoot(ctx)
	u.Release = time.AfterFunc(r.g.cfg.ResponseTimeout, func() {
		r.mu.Lock()
		pending := r.ues.ByRANCoreID(u.RANCoreID) == u
		r.mu.Unlock()

		if !pending {
			return
		}

		log.WarnContext(timerCtx, "no UE Context Release Complete from radio, UE-associated logical S1-connection reset")
		sendResets(timerCtx, n, log, sourceRadio, causeRadioUnspec,
			[]s1ap.UEAssociatedLogicalS1ConnectionItem{{MMEUES1APID: s1ap.Ptr(u.RANCoreID), ENBUES1APID: s1ap.Ptr(enbID)}})

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

func (r *relay) superviseLocked(ctx context.Context, u *ueContext, log *slog.Logger) {
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

		log.WarnContext(ctx, "no answer from MME to INITIAL UE MESSAGE, UE context released",
			slog.Uint64("mme_enb_ue_s1ap_id", uint64(u.CoreRANID)))
		r.resetMMEs(ctx, gone, causeRadioUnspec)
		r.resetRANs(ctx, u.Core.Operator(), gone, causeRadioUnspec)
	})
}

func (r *relay) rejectInitialUE(ctx context.Context, log *slog.Logger, result, reason string, err error) {
	r.countInitialUE(result)
	tracing.SpanFailed(ctx, result)
	log.WarnContext(ctx, reason, slog.Any("error", err))
}

func (r *relay) updateLocation(u *ueContext, p *s1ap.RawPDU) {
	if tai, err := assoc.GetIE[s1ap.TAI](p.Get, s1ap.IDTAI); err == nil && tai != nil {
		if plmn, err := plmnid.Decode(tai.PLMNIdentity); err == nil {
			u.Ext.tai = &TAI{PLMN: plmn, TAC: uint16(tai.TAC)}
		}
	}

	if cgi, err := assoc.GetIE[s1ap.EUTRANCGI](p.Get, s1ap.IDEUTRANCGI); err == nil && cgi != nil {
		if plmn, err := plmnid.Decode(cgi.PLMNIdentity); err == nil {
			u.Ext.ecgi = &ECGI{PLMN: plmn, CellID: cgi.CellID}
		}
	}
}

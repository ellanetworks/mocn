package s1

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/plmnid"
	"github.com/ellanetworks/mocn/internal/tracing"
)

type warningKey struct {
	id     s1ap.MessageIdentifier
	serial s1ap.SerialNumber
}

type (
	completedAreas = assoc.Areas[s1ap.TAI, s1ap.EmergencyAreaID, s1ap.CellIDBroadcastItem]
	cancelledAreas = assoc.Areas[s1ap.TAI, s1ap.EmergencyAreaID, s1ap.CellIDCancelledItem]
	writes         = assoc.Warnings[warningKey, *s1ap.BroadcastCompletedAreaList]
	kills          = assoc.Warnings[warningKey, *s1ap.BroadcastCancelledAreaList]
)

var pwsIndications = map[s1ap.ProcedureCode]string{
	s1ap.ProcPWSRestartIndication: assoc.PWSRestartIndication,
	s1ap.ProcPWSFailureIndication: assoc.PWSFailureIndication,
}

// TS 36.413 clause 9.2.1.37
// TS 36.413 clause 9.2.1.38
func (s ranSetup) ownsCellID(cellID uint32) bool {
	bits, ok := enbIDBits[s.globalENBID.Type]

	return ok && cellID>>(cellIdentityBits-bits) == s.globalENBID.ID
}

// TS 36.413 clause 9.2.1.38
// TS 36.300 clause 8.2
func (s ranSetup) ownsCell(c s1ap.EUTRANCGI) bool {
	plmn, err := plmnid.Decode(c.PLMNIdentity)

	return err == nil && s.ownsCellID(uint32(c.CellID)) && (plmn == s.globalENBID.PLMN || s.Broadcasts([]db.PLMN{plmn}))
}

// TS 36.413 clause 8.12.1.2
// TS 36.413 clause 9.2.1.46
func warningCovers(l *s1ap.WarningAreaList) func(ranSetup) bool {
	switch {
	case l == nil || l.Ext != nil || l.EmergencyAreaIDList != nil:
		return func(ranSetup) bool { return true }
	case l.CellIDList != nil:
		return func(s ranSetup) bool {
			return slices.ContainsFunc(*l.CellIDList, s.ownsCell)
		}
	}

	listed := assoc.Items(l.TrackingAreaListforWarning)
	tais := make([]assoc.TAI, 0, len(listed))

	for _, t := range listed {
		if id, err := plmnid.Decode(t.PLMNIdentity); err == nil {
			tais = append(tais, assoc.TAI{PLMN: id, TAC: uint32(t.TAC)})
		}
	}

	return func(s ranSetup) bool { return slices.ContainsFunc(tais, s.Serves) }
}

func (g *Gateway) rejectWarning(ctx context.Context, a *mmeAssoc, log *slog.Logger, proc string,
	code s1ap.ProcedureCode, err error,
) {
	g.core.CountPWS(proc, assoc.PWSInvalid)
	tracing.SpanFailed(ctx, assoc.PWSInvalid)
	log.WarnContext(ctx, "rejected "+code.String()+" from MME", slog.Any("error", err))
	indicateParseError(ctx, a, log, code, err)
}

// TS 36.413 clause 8.12.1
// TS 36.300 clause 4.6.2
func (g *Gateway) writeReplaceWarning(ctx context.Context, a *mmeAssoc, log *slog.Logger, value, msg []byte) {
	req, err := s1ap.ParseWriteReplaceWarningRequest(value)
	if err != nil {
		g.rejectWarning(ctx, a, log, assoc.PWSWriteReplaceWarning, s1ap.ProcWriteReplaceWarning, err)
		return
	}

	diag := responseDiagnostics(s1ap.ProcWriteReplaceWarning, req.Diagnostics())

	assoc.Warn(ctx, g.core, g.writes, a, log, assoc.Warning[ranSetup, warningKey, *s1ap.BroadcastCompletedAreaList]{
		Procedure: assoc.PWSWriteReplaceWarning, What: "Write-Replace Warning Request",
		Key: warningKey{req.MessageIdentifier, req.SerialNumber}, Covers: warningCovers(req.WarningAreaList), Msg: msg,
		Answer: func(ctx context.Context, got []*s1ap.BroadcastCompletedAreaList) {
			sendTo(ctx, a, log, "Write-Replace Warning Response", &s1ap.WriteReplaceWarningResponse{
				MessageIdentifier: req.MessageIdentifier, SerialNumber: req.SerialNumber,
				BroadcastCompletedAreaList: completedAreaList(got), CriticalityDiagnostics: diag,
			}, false)
		},
	})
}

// TS 36.413 clause 8.12.2
// TS 36.300 clause 4.6.2
func (g *Gateway) kill(ctx context.Context, a *mmeAssoc, log *slog.Logger, value, msg []byte) {
	req, err := s1ap.ParseKillRequest(value)
	if err != nil {
		g.rejectWarning(ctx, a, log, names.Kill, s1ap.ProcKill, err)
		return
	}

	diag := responseDiagnostics(s1ap.ProcKill, req.Diagnostics())

	assoc.Warn(ctx, g.core, g.kills, a, log, assoc.Warning[ranSetup, warningKey, *s1ap.BroadcastCancelledAreaList]{
		Procedure: names.Kill, What: "Kill Request",
		Key: warningKey{req.MessageIdentifier, req.SerialNumber}, Covers: warningCovers(req.WarningAreaList), Msg: msg,
		Answer: func(ctx context.Context, got []*s1ap.BroadcastCancelledAreaList) {
			sendTo(ctx, a, log, "Kill Response", &s1ap.KillResponse{
				MessageIdentifier: req.MessageIdentifier, SerialNumber: req.SerialNumber,
				BroadcastCancelledAreaList: cancelledAreaList(got), CriticalityDiagnostics: diag,
			}, false)
		},
	})
}

func warningKeyOf(msg []byte) (warningKey, bool) {
	p, err := s1ap.ParseRawPDU(msg)
	if err != nil {
		return warningKey{}, false
	}

	var k warningKey

	okID, errID := p.Get(s1ap.IDMessageIdentifier, &k.id)
	okSerial, errSerial := p.Get(s1ap.IDSerialNumber, &k.serial)

	return k, okID && okSerial && errID == nil && errSerial == nil
}

// TS 36.413 clause 10.2
// TS 36.413 clause 10.3.4.2
// TS 36.413 clause 10.3.5
// TS 36.413 clause 10.3.6
func warningAnswer[T any](ctx context.Context, w *assoc.Warnings[warningKey, T], n *ranAssoc, log *slog.Logger,
	proc s1ap.ProcedureCode, msg []byte, key warningKey, got T, d s1ap.Diagnostics, err error,
) {
	if err != nil {
		tracing.SpanFailed(ctx, assoc.ErrTypeInvalid)
		log.WarnContext(ctx, "invalid "+proc.String()+" response from radio", slog.Any("error", err))

		if _, ase := errors.AsType[*s1ap.AbstractSyntaxError](err); !ase {
			indicateTransferSyntax(ctx, n, log, &s1ap.SuccessfulOutcome{ProcedureCode: proc})
		}

		var (
			ok   bool
			none T
		)

		if key, ok = warningKeyOf(msg); !ok {
			return
		}

		got = none
	} else if d.ReportRequired() {
		indicateNotify(ctx, n, log, proc, s1ap.TriggeringSuccessfulOutcome, d)
	}

	if !w.Answer(n.Conn(), key, got) {
		log.InfoContext(ctx, "dropped "+proc.String()+" response from radio: no request awaits it")
	}
}

// TS 36.413 clause 8.12.1.2
func (g *Gateway) writeReplaceWarningResponse(ctx context.Context, n *ranAssoc, log *slog.Logger, value, msg []byte) {
	var (
		key  warningKey
		list *s1ap.BroadcastCompletedAreaList
		d    s1ap.Diagnostics
	)

	resp, err := s1ap.ParseWriteReplaceWarningResponse(value)
	if err == nil {
		key, list, d = warningKey{resp.MessageIdentifier, resp.SerialNumber}, resp.BroadcastCompletedAreaList, resp.Diagnostics()
	}

	warningAnswer(ctx, g.writes, n, log, s1ap.ProcWriteReplaceWarning, msg, key, list, d, err)
}

// TS 36.413 clause 8.12.2.2
func (g *Gateway) killResponse(ctx context.Context, n *ranAssoc, log *slog.Logger, value, msg []byte) {
	var (
		key  warningKey
		list *s1ap.BroadcastCancelledAreaList
		d    s1ap.Diagnostics
	)

	resp, err := s1ap.ParseKillResponse(value)
	if err == nil {
		key, list, d = warningKey{resp.MessageIdentifier, resp.SerialNumber}, resp.BroadcastCancelledAreaList, resp.Diagnostics()
	}

	warningAnswer(ctx, g.kills, n, log, s1ap.ProcKill, msg, key, list, d, err)
}

func (g *Gateway) warningsLost(n *ranAssoc) {
	g.writes.RadioLost(n.Conn())
	g.kills.RadioLost(n.Conn())
}

// TS 36.413 clause 9.2.1.54
func completedAreaList(got []*s1ap.BroadcastCompletedAreaList) *s1ap.BroadcastCompletedAreaList {
	var a completedAreas

	for _, l := range got {
		if l == nil {
			continue
		}

		a.AddCells(assoc.Items(l.CellIDBroadcast)...)

		for _, t := range assoc.Items(l.TAIBroadcast) {
			a.AddTAI(t.TAI, assoc.Convert(t.CompletedCellInTAI, func(c s1ap.CompletedCellInTAIItem) s1ap.CellIDBroadcastItem {
				return s1ap.CellIDBroadcastItem(c)
			}))
		}

		for _, e := range assoc.Items(l.EmergencyAreaIDBroadcast) {
			a.AddEAI(e.EmergencyAreaID, assoc.Convert(e.CompletedCellInEAI, func(c s1ap.CompletedCellInEAIItem) s1ap.CellIDBroadcastItem {
				return s1ap.CellIDBroadcastItem(c)
			}))
		}
	}

	if a.Empty() {
		return nil
	}

	a = a.OneKind()

	return &s1ap.BroadcastCompletedAreaList{
		CellIDBroadcast: assoc.NonEmpty(s1ap.CellIDBroadcast(a.Cells)),
		TAIBroadcast: assoc.NonEmpty(s1ap.TAIBroadcast(assoc.Convert(a.TAIs, func(t assoc.Area[s1ap.TAI, s1ap.CellIDBroadcastItem]) s1ap.TAIBroadcastItem {
			return s1ap.TAIBroadcastItem{
				TAI: t.ID, CompletedCellInTAI: assoc.Convert(t.Cells, func(c s1ap.CellIDBroadcastItem) s1ap.CompletedCellInTAIItem {
					return s1ap.CompletedCellInTAIItem(c)
				}),
			}
		}))),
		EmergencyAreaIDBroadcast: assoc.NonEmpty(s1ap.EmergencyAreaIDBroadcast(assoc.Convert(a.EAIs,
			func(e assoc.Area[s1ap.EmergencyAreaID, s1ap.CellIDBroadcastItem]) s1ap.EmergencyAreaIDBroadcastItem {
				return s1ap.EmergencyAreaIDBroadcastItem{
					EmergencyAreaID: e.ID,
					CompletedCellInEAI: assoc.Convert(e.Cells, func(c s1ap.CellIDBroadcastItem) s1ap.CompletedCellInEAIItem {
						return s1ap.CompletedCellInEAIItem(c)
					}),
				}
			}))),
	}
}

// TS 36.413 clause 9.2.1.70
func cancelledAreaList(got []*s1ap.BroadcastCancelledAreaList) *s1ap.BroadcastCancelledAreaList {
	var a cancelledAreas

	for _, l := range got {
		if l == nil {
			continue
		}

		a.AddCells(assoc.Items(l.CellIDCancelled)...)

		for _, t := range assoc.Items(l.TAICancelled) {
			a.AddTAI(t.TAI, assoc.Convert(t.CancelledCellInTAI, func(c s1ap.CancelledCellInTAIItem) s1ap.CellIDCancelledItem {
				return s1ap.CellIDCancelledItem(c)
			}))
		}

		for _, e := range assoc.Items(l.EmergencyAreaIDCancelled) {
			a.AddEAI(e.EmergencyAreaID, assoc.Convert(e.CancelledCellInEAI, func(c s1ap.CancelledCellInEAIItem) s1ap.CellIDCancelledItem {
				return s1ap.CellIDCancelledItem(c)
			}))
		}
	}

	if a.Empty() {
		return nil
	}

	a = a.OneKind()

	return &s1ap.BroadcastCancelledAreaList{
		CellIDCancelled: assoc.NonEmpty(s1ap.CellIDCancelled(a.Cells)),
		TAICancelled: assoc.NonEmpty(s1ap.TAICancelled(assoc.Convert(a.TAIs, func(t assoc.Area[s1ap.TAI, s1ap.CellIDCancelledItem]) s1ap.TAICancelledItem {
			return s1ap.TAICancelledItem{
				TAI: t.ID, CancelledCellInTAI: assoc.Convert(t.Cells, func(c s1ap.CellIDCancelledItem) s1ap.CancelledCellInTAIItem {
					return s1ap.CancelledCellInTAIItem(c)
				}),
			}
		}))),
		EmergencyAreaIDCancelled: assoc.NonEmpty(s1ap.EmergencyAreaIDCancelled(assoc.Convert(a.EAIs,
			func(e assoc.Area[s1ap.EmergencyAreaID, s1ap.CellIDCancelledItem]) s1ap.EmergencyAreaIDCancelledItem {
				return s1ap.EmergencyAreaIDCancelledItem{
					EmergencyAreaID: e.ID,
					CancelledCellInEAI: assoc.Convert(e.Cells, func(c s1ap.CellIDCancelledItem) s1ap.CancelledCellInEAIItem {
						return s1ap.CancelledCellInEAIItem(c)
					}),
				}
			}))),
	}
}

// TS 36.413 clause 8.12.3
// TS 36.413 clause 8.12.4
// TS 36.413 clause 10.3.4.2
// TS 36.413 clause 10.3.5
// TS 36.300 clause 4.6.2
// TS 23.007 clause 15A.1.2
func (g *Gateway) pwsIndication(ctx context.Context, n *ranAssoc, log *slog.Logger, code s1ap.ProcedureCode,
	value, msg []byte,
) {
	proc := pwsIndications[code]

	var (
		cells []string
		d     s1ap.Diagnostics
		err   error
	)

	if code == s1ap.ProcPWSRestartIndication {
		var ind *s1ap.PWSRestartIndication
		if ind, err = s1ap.ParsePWSRestartIndication(value); err == nil {
			d = ind.Diagnostics()
			for _, c := range ind.ECGIListForRestart {
				cells = append(cells, fmt.Sprintf("%x/%07x", c.PLMNIdentity, c.CellID))
			}
		}
	} else {
		var ind *s1ap.PWSFailureIndication
		if ind, err = s1ap.ParsePWSFailureIndication(value); err == nil {
			d = ind.Diagnostics()
		}
	}

	var p *s1ap.RawPDU
	if err == nil {
		p, err = s1ap.ParseRawPDU(msg)
	}

	if err != nil {
		g.core.CountPWS(proc, assoc.PWSInvalid)
		tracing.SpanFailed(ctx, assoc.PWSInvalid)
		log.WarnContext(ctx, "rejected "+code.String()+" from radio", slog.Any("error", err))
		indicateParseError(ctx, n, log, code, err)

		return
	}

	g.core.PWSIndication(ctx, n, log, proc, code.String(), cells, func(o assoc.Operator) ([]byte, error) {
		id, err := globalENBIDTo(*o.GlobalENBID)
		if err != nil {
			return nil, err
		}

		if err := p.Set(s1ap.IDGlobalENBID, &id); err != nil {
			return nil, err
		}

		return p.Marshal()
	})

	if d.ReportRequired() {
		indicateNotify(ctx, n, log, code, s1ap.TriggeringInitiatingMessage, d)
	}
}

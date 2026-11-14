package ng

import (
	"context"
	"log/slog"
	"slices"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/plmnid"
	"github.com/ellanetworks/mocn/internal/tracing"
)

type warningKey struct {
	id     ngap.MessageIdentifier
	serial ngap.SerialNumber
}

type (
	completedEUTRA = assoc.Areas[ngap.TAI, ngap.EmergencyAreaID, ngap.CellIDBroadcastEUTRAItem]
	completedNR    = assoc.Areas[ngap.TAI, ngap.EmergencyAreaID, ngap.CellIDBroadcastNRItem]
	cancelledEUTRA = assoc.Areas[ngap.TAI, ngap.EmergencyAreaID, ngap.CellIDCancelledEUTRAItem]
	cancelledNR    = assoc.Areas[ngap.TAI, ngap.EmergencyAreaID, ngap.CellIDCancelledNRItem]
	writes         = assoc.Warnings[warningKey, *ngap.BroadcastCompletedAreaList]
	cancels        = assoc.Warnings[warningKey, *ngap.BroadcastCancelledAreaList]
)

var pwsIndications = map[ngap.ProcedureCode]string{
	ngap.ProcPWSRestartIndication: assoc.PWSRestartIndication,
	ngap.ProcPWSFailureIndication: assoc.PWSFailureIndication,
}

// TS 38.413 clause 9.3.1.6
// TS 38.413 clause 9.3.1.7
func (s ranSetup) ownsCell(nci ngap.NRCellIdentity) bool {
	id := s.globalGNBID

	return id.Length > 0 && id.Length <= ngap.NRCellIdentityBits && uint64(nci)>>(ngap.NRCellIdentityBits-id.Length) == uint64(id.ID)
}

// TS 38.413 clause 8.9.1.2
// TS 38.413 clause 9.3.1.37
func warningCovers(l *ngap.WarningAreaList) func(ranSetup) bool {
	switch {
	case l == nil || len(l.EmergencyAreaIDs) > 0:
		return func(ranSetup) bool { return true }
	case len(l.NRCGIs) > 0:
		return func(s ranSetup) bool {
			return slices.ContainsFunc(l.NRCGIs, func(c ngap.NRCGI) bool { return s.ownsCell(c.NRCellIdentity) })
		}
	}

	tais := make([]assoc.TAI, 0, len(l.TAIs))

	for _, t := range l.TAIs {
		if id, err := plmnid.Decode(t.PLMNIdentity); err == nil {
			tais = append(tais, assoc.TAI{PLMN: id, TAC: uint32(t.TAC)})
		}
	}

	return func(s ranSetup) bool { return slices.ContainsFunc(tais, s.Serves) }
}

func (g *Gateway) rejectWarning(ctx context.Context, a *amfAssoc, log *slog.Logger, proc string,
	code ngap.ProcedureCode, err error,
) {
	g.core.CountPWS(proc, assoc.PWSInvalid)
	tracing.SpanFailed(ctx, assoc.PWSInvalid)
	log.WarnContext(ctx, "rejected "+code.String()+" from AMF", slog.Any("error", err))
	indicateParseError(ctx, a, log, code, err)
}

// TS 38.413 clause 8.9.1
// TS 38.300 clause 4.10.2.3
func (g *Gateway) writeReplaceWarning(ctx context.Context, a *amfAssoc, log *slog.Logger, value, msg []byte) {
	req, err := ngap.ParseWriteReplaceWarningRequest(value)
	if err != nil {
		g.rejectWarning(ctx, a, log, assoc.PWSWriteReplaceWarning, ngap.ProcWriteReplaceWarning, err)
		return
	}

	diag := responseDiagnostics(ngap.ProcWriteReplaceWarning, req.Diagnostics())

	assoc.Warn(ctx, g.core, g.writes, a, log, assoc.Warning[ranSetup, warningKey, *ngap.BroadcastCompletedAreaList]{
		Procedure: assoc.PWSWriteReplaceWarning, What: "Write-Replace Warning Request",
		Key: warningKey{req.MessageIdentifier, req.SerialNumber}, Covers: warningCovers(req.WarningAreaList), Msg: msg,
		Answer: func(ctx context.Context, got []*ngap.BroadcastCompletedAreaList) {
			sendTo(ctx, a, log, "Write-Replace Warning Response", &ngap.WriteReplaceWarningResponse{
				MessageIdentifier: req.MessageIdentifier, SerialNumber: req.SerialNumber,
				BroadcastCompletedAreaList: completedAreaList(got), CriticalityDiagnostics: diag,
			}, false)
		},
	})
}

// TS 38.413 clause 8.9.2
// TS 38.300 clause 4.10.2.3
func (g *Gateway) pwsCancel(ctx context.Context, a *amfAssoc, log *slog.Logger, value, msg []byte) {
	req, err := ngap.ParsePWSCancelRequest(value)
	if err != nil {
		g.rejectWarning(ctx, a, log, names.Kill, ngap.ProcPWSCancel, err)
		return
	}

	diag := responseDiagnostics(ngap.ProcPWSCancel, req.Diagnostics())

	assoc.Warn(ctx, g.core, g.cancels, a, log, assoc.Warning[ranSetup, warningKey, *ngap.BroadcastCancelledAreaList]{
		Procedure: names.Kill, What: "PWS Cancel Request",
		Key: warningKey{req.MessageIdentifier, req.SerialNumber}, Covers: warningCovers(req.WarningAreaList), Msg: msg,
		Answer: func(ctx context.Context, got []*ngap.BroadcastCancelledAreaList) {
			sendTo(ctx, a, log, "PWS Cancel Response", &ngap.PWSCancelResponse{
				MessageIdentifier: req.MessageIdentifier, SerialNumber: req.SerialNumber,
				BroadcastCancelledAreaList: cancelledAreaList(got), CriticalityDiagnostics: diag,
			}, false)
		},
	})
}

// TS 38.413 clause 8.9.1.2
func (g *Gateway) writeReplaceWarningResponse(ctx context.Context, n *ranAssoc, log *slog.Logger, value []byte) {
	resp, err := ngap.ParseWriteReplaceWarningResponse(value)
	if err != nil {
		tracing.SpanFailed(ctx, assoc.ErrTypeInvalid)
		log.WarnContext(ctx, "dropped invalid Write-Replace Warning Response from radio", slog.Any("error", err))

		return
	}

	if !g.writes.Answer(n.Conn(), warningKey{resp.MessageIdentifier, resp.SerialNumber}, resp.BroadcastCompletedAreaList) {
		log.InfoContext(ctx, "dropped Write-Replace Warning Response from radio: no request awaits it")
	}
}

// TS 38.413 clause 8.9.2.2
func (g *Gateway) pwsCancelResponse(ctx context.Context, n *ranAssoc, log *slog.Logger, value []byte) {
	resp, err := ngap.ParsePWSCancelResponse(value)
	if err != nil {
		tracing.SpanFailed(ctx, assoc.ErrTypeInvalid)
		log.WarnContext(ctx, "dropped invalid PWS Cancel Response from radio", slog.Any("error", err))

		return
	}

	if !g.cancels.Answer(n.Conn(), warningKey{resp.MessageIdentifier, resp.SerialNumber}, resp.BroadcastCancelledAreaList) {
		log.InfoContext(ctx, "dropped PWS Cancel Response from radio: no request awaits it")
	}
}

func (g *Gateway) warningsLost(n *ranAssoc) {
	g.core.ForgetRestarts(n)
	g.writes.RadioLost(n.Conn())
	g.cancels.RadioLost(n.Conn())
}

// TS 38.413 clause 9.3.1.43
func completedAreaList(got []*ngap.BroadcastCompletedAreaList) *ngap.BroadcastCompletedAreaList {
	var (
		eutra completedEUTRA
		nr    completedNR
	)

	for _, l := range got {
		if l == nil {
			continue
		}

		eutra.AddCells(l.CellIDBroadcastEUTRA...)
		nr.AddCells(l.CellIDBroadcastNR...)

		for _, t := range l.TAIBroadcastEUTRA {
			eutra.AddTAI(t.TAI, assoc.Convert(t.CompletedCellsInTAI, func(c ngap.CompletedCellsInTAIEUTRAItem) ngap.CellIDBroadcastEUTRAItem {
				return ngap.CellIDBroadcastEUTRAItem(c)
			}))
		}

		for _, e := range l.EmergencyAreaIDBroadcastEUTRA {
			eutra.AddEAI(e.EmergencyAreaID, assoc.Convert(e.CompletedCellsInEAI, func(c ngap.CompletedCellsInEAIEUTRAItem) ngap.CellIDBroadcastEUTRAItem {
				return ngap.CellIDBroadcastEUTRAItem(c)
			}))
		}

		for _, t := range l.TAIBroadcastNR {
			nr.AddTAI(t.TAI, assoc.Convert(t.CompletedCellsInTAI, func(c ngap.CompletedCellsInTAINRItem) ngap.CellIDBroadcastNRItem {
				return ngap.CellIDBroadcastNRItem(c)
			}))
		}

		for _, e := range l.EmergencyAreaIDBroadcastNR {
			nr.AddEAI(e.EmergencyAreaID, assoc.Convert(e.CompletedCellsInEAI, func(c ngap.CompletedCellsInEAINRItem) ngap.CellIDBroadcastNRItem {
				return ngap.CellIDBroadcastNRItem(c)
			}))
		}
	}

	if !nr.Empty() {
		nr = nr.OneKind()

		return &ngap.BroadcastCompletedAreaList{
			CellIDBroadcastNR: nr.Cells,
			TAIBroadcastNR: assoc.Convert(nr.TAIs, func(t assoc.Area[ngap.TAI, ngap.CellIDBroadcastNRItem]) ngap.TAIBroadcastNRItem {
				return ngap.TAIBroadcastNRItem{
					TAI: t.ID, CompletedCellsInTAI: assoc.Convert(t.Cells, func(c ngap.CellIDBroadcastNRItem) ngap.CompletedCellsInTAINRItem {
						return ngap.CompletedCellsInTAINRItem(c)
					}),
				}
			}),
			EmergencyAreaIDBroadcastNR: assoc.Convert(nr.EAIs,
				func(e assoc.Area[ngap.EmergencyAreaID, ngap.CellIDBroadcastNRItem]) ngap.EmergencyAreaIDBroadcastNRItem {
					return ngap.EmergencyAreaIDBroadcastNRItem{
						EmergencyAreaID: e.ID,
						CompletedCellsInEAI: assoc.Convert(e.Cells, func(c ngap.CellIDBroadcastNRItem) ngap.CompletedCellsInEAINRItem {
							return ngap.CompletedCellsInEAINRItem(c)
						}),
					}
				}),
		}
	}

	if eutra.Empty() {
		return nil
	}

	eutra = eutra.OneKind()

	return &ngap.BroadcastCompletedAreaList{
		CellIDBroadcastEUTRA: eutra.Cells,
		TAIBroadcastEUTRA: assoc.Convert(eutra.TAIs, func(t assoc.Area[ngap.TAI, ngap.CellIDBroadcastEUTRAItem]) ngap.TAIBroadcastEUTRAItem {
			return ngap.TAIBroadcastEUTRAItem{
				TAI: t.ID, CompletedCellsInTAI: assoc.Convert(t.Cells, func(c ngap.CellIDBroadcastEUTRAItem) ngap.CompletedCellsInTAIEUTRAItem {
					return ngap.CompletedCellsInTAIEUTRAItem(c)
				}),
			}
		}),
		EmergencyAreaIDBroadcastEUTRA: assoc.Convert(eutra.EAIs,
			func(e assoc.Area[ngap.EmergencyAreaID, ngap.CellIDBroadcastEUTRAItem]) ngap.EmergencyAreaIDBroadcastEUTRAItem {
				return ngap.EmergencyAreaIDBroadcastEUTRAItem{
					EmergencyAreaID: e.ID,
					CompletedCellsInEAI: assoc.Convert(e.Cells, func(c ngap.CellIDBroadcastEUTRAItem) ngap.CompletedCellsInEAIEUTRAItem {
						return ngap.CompletedCellsInEAIEUTRAItem(c)
					}),
				}
			}),
	}
}

// TS 38.413 clause 9.3.1.44
func cancelledAreaList(got []*ngap.BroadcastCancelledAreaList) *ngap.BroadcastCancelledAreaList {
	var (
		eutra cancelledEUTRA
		nr    cancelledNR
	)

	for _, l := range got {
		if l == nil {
			continue
		}

		eutra.AddCells(l.CellIDCancelledEUTRA...)
		nr.AddCells(l.CellIDCancelledNR...)

		for _, t := range l.TAICancelledEUTRA {
			eutra.AddTAI(t.TAI, assoc.Convert(t.CancelledCellsInTAI, func(c ngap.CancelledCellsInTAIEUTRAItem) ngap.CellIDCancelledEUTRAItem {
				return ngap.CellIDCancelledEUTRAItem(c)
			}))
		}

		for _, e := range l.EmergencyAreaIDCancelledEUTRA {
			eutra.AddEAI(e.EmergencyAreaID, assoc.Convert(e.CancelledCellsInEAI, func(c ngap.CancelledCellsInEAIEUTRAItem) ngap.CellIDCancelledEUTRAItem {
				return ngap.CellIDCancelledEUTRAItem(c)
			}))
		}

		for _, t := range l.TAICancelledNR {
			nr.AddTAI(t.TAI, assoc.Convert(t.CancelledCellsInTAI, func(c ngap.CancelledCellsInTAINRItem) ngap.CellIDCancelledNRItem {
				return ngap.CellIDCancelledNRItem(c)
			}))
		}

		for _, e := range l.EmergencyAreaIDCancelledNR {
			nr.AddEAI(e.EmergencyAreaID, assoc.Convert(e.CancelledCellsInEAI, func(c ngap.CancelledCellsInEAINRItem) ngap.CellIDCancelledNRItem {
				return ngap.CellIDCancelledNRItem(c)
			}))
		}
	}

	if !nr.Empty() {
		nr = nr.OneKind()

		return &ngap.BroadcastCancelledAreaList{
			CellIDCancelledNR: nr.Cells,
			TAICancelledNR: assoc.Convert(nr.TAIs, func(t assoc.Area[ngap.TAI, ngap.CellIDCancelledNRItem]) ngap.TAICancelledNRItem {
				return ngap.TAICancelledNRItem{
					TAI: t.ID, CancelledCellsInTAI: assoc.Convert(t.Cells, func(c ngap.CellIDCancelledNRItem) ngap.CancelledCellsInTAINRItem {
						return ngap.CancelledCellsInTAINRItem(c)
					}),
				}
			}),
			EmergencyAreaIDCancelledNR: assoc.Convert(nr.EAIs,
				func(e assoc.Area[ngap.EmergencyAreaID, ngap.CellIDCancelledNRItem]) ngap.EmergencyAreaIDCancelledNRItem {
					return ngap.EmergencyAreaIDCancelledNRItem{
						EmergencyAreaID: e.ID,
						CancelledCellsInEAI: assoc.Convert(e.Cells, func(c ngap.CellIDCancelledNRItem) ngap.CancelledCellsInEAINRItem {
							return ngap.CancelledCellsInEAINRItem(c)
						}),
					}
				}),
		}
	}

	if eutra.Empty() {
		return nil
	}

	eutra = eutra.OneKind()

	return &ngap.BroadcastCancelledAreaList{
		CellIDCancelledEUTRA: eutra.Cells,
		TAICancelledEUTRA: assoc.Convert(eutra.TAIs, func(t assoc.Area[ngap.TAI, ngap.CellIDCancelledEUTRAItem]) ngap.TAICancelledEUTRAItem {
			return ngap.TAICancelledEUTRAItem{
				TAI: t.ID, CancelledCellsInTAI: assoc.Convert(t.Cells, func(c ngap.CellIDCancelledEUTRAItem) ngap.CancelledCellsInTAIEUTRAItem {
					return ngap.CancelledCellsInTAIEUTRAItem(c)
				}),
			}
		}),
		EmergencyAreaIDCancelledEUTRA: assoc.Convert(eutra.EAIs,
			func(e assoc.Area[ngap.EmergencyAreaID, ngap.CellIDCancelledEUTRAItem]) ngap.EmergencyAreaIDCancelledEUTRAItem {
				return ngap.EmergencyAreaIDCancelledEUTRAItem{
					EmergencyAreaID: e.ID,
					CancelledCellsInEAI: assoc.Convert(e.Cells, func(c ngap.CellIDCancelledEUTRAItem) ngap.CancelledCellsInEAIEUTRAItem {
						return ngap.CancelledCellsInEAIEUTRAItem(c)
					}),
				}
			}),
	}
}

// TS 38.413 clause 8.9.3
// TS 38.413 clause 8.9.4
// TS 38.300 clause 4.10.2.3
// TS 23.007 clause 15A.1.2
func (g *Gateway) pwsIndication(ctx context.Context, n *ranAssoc, log *slog.Logger, code ngap.ProcedureCode,
	value, msg []byte,
) {
	proc := pwsIndications[code]

	var err error
	if code == ngap.ProcPWSRestartIndication {
		_, err = ngap.ParsePWSRestartIndication(value)
	} else {
		_, err = ngap.ParsePWSFailureIndication(value)
	}

	var p *ngap.RawPDU
	if err == nil {
		p, err = ngap.ParseRawPDU(msg)
	}

	if err != nil {
		g.core.CountPWS(proc, assoc.PWSInvalid)
		tracing.SpanFailed(ctx, assoc.PWSInvalid)
		log.WarnContext(ctx, "rejected "+code.String()+" from radio", slog.Any("error", err))
		indicateParseError(ctx, n, log, code, err)

		return
	}

	g.core.PWSIndication(ctx, n, log, proc, code.String(), msg, func(o assoc.Operator) ([]byte, error) {
		id, err := globalGNBIDTo(*o.GlobalGNBID)
		if err != nil {
			return nil, err
		}

		if err := p.Set(ngap.IDGlobalRANNodeID, id); err != nil {
			return nil, err
		}

		return p.Marshal()
	})
}

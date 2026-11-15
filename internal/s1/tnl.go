package s1

import (
	"context"
	"errors"
	"log/slog"
	"slices"

	"github.com/ellanetworks/core/per"
	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/gtpu"
	"github.com/ellanetworks/mocn/internal/metrics"
	"github.com/ellanetworks/mocn/internal/tnl"
	"github.com/ellanetworks/mocn/internal/tracing"
)

var (
	causeTransportResource = s1ap.CauseTransportResourceUnavailable.Cause()
	errNoTNLPath           = errors.New("no user-plane path for the UE")
)

func (r *relay) tnlPath(u *ueContext) (gtpu.Path, bool) {
	if r.plane == nil || u.Core == nil || u.RAN == nil {
		return gtpu.Path{}, false
	}

	if u.Ext.path != nil && u.Ext.tunnels.Len() > 0 {
		return *u.Ext.path, true
	}

	path, ok := r.plane.Path(u.OperatorID, u.Ext.radio.Ref())
	if ok {
		u.Ext.path = &path
	}

	return path, ok
}

func editIE[T any, PT interface {
	*T
	per.Marshaler
	per.Unmarshaler
}](p *s1ap.RawPDU, id s1ap.ProtocolIEID, edit func(*T) error,
) error {
	i, err := p.Index(id)
	if err != nil || i < 0 {
		return err
	}

	ie, err := p.IE(i)
	if err != nil {
		return err
	}

	v := PT(new(T))
	if err := per.Unmarshal(ie.Value, v, per.Aligned); err != nil {
		return err
	}

	if err := edit((*T)(v)); err != nil {
		return err
	}

	b, err := per.Marshal(v, per.Aligned)
	if err != nil {
		return err
	}

	return p.SetValue(i, b)
}

func readIE[T any, PT interface {
	*T
	per.Unmarshaler
}](p *s1ap.RawPDU, id s1ap.ProtocolIEID) (*T, error) {
	i, err := p.Index(id)
	if err != nil {
		return nil, err
	}

	if i < 0 {
		return nil, nil //nolint:nilnil
	}

	ie, err := p.IE(i)
	if err != nil {
		return nil, err
	}

	v := PT(new(T))
	if err := per.Unmarshal(ie.Value, v, per.Aligned); err != nil {
		return nil, err
	}

	return (*T)(v), nil
}

func strip(p *s1ap.RawPDU, ids ...s1ap.ProtocolIEID) error {
	for _, id := range ids {
		var at []int

		for i := range p.All(id) {
			at = append(at, i)
		}

		for _, i := range slices.Backward(at) {
			if err := p.Remove(i); err != nil {
				return err
			}
		}
	}

	return nil
}

type binder struct {
	r    *relay
	path gtpu.Path
	s    *gtpu.Session
	proc s1ap.ProcedureCode
}

func (b binder) bind(bearer s1ap.ERABID, kind gtpu.Kind, pending bool, to gtpu.Side, tla *s1ap.TransportLayerAddress,
	teid *s1ap.GTPTEID,
) error {
	e, err := b.r.plane.Bind(b.s, gtpu.Binding{
		Path: b.path, Proc: int(b.proc), Bearer: int(bearer), Slot: gtpu.Slot{Kind: kind}, Pending: pending,
	}, to, tnl.Octets(*tla), uint32(*teid))
	if err != nil {
		return err
	}

	addr, err := tnl.Address(e.Addr)
	if err != nil {
		return err
	}

	*tla, *teid = addr, s1ap.GTPTEID(e.TEID)

	return nil
}

func (b binder) bindOptional(bearer s1ap.ERABID, kind gtpu.Kind, to gtpu.Side, tla **s1ap.TransportLayerAddress,
	teid **s1ap.GTPTEID,
) error {
	if *teid == nil && *tla == nil {
		return nil
	}

	if *teid == nil || *tla == nil {
		return gtpu.ErrInvalidAddress
	}

	return b.bind(bearer, kind, false, to, *tla, *teid)
}

func lossless[T any, PT interface {
	*T
	per.Marshaler
}](items []T) error {
	for i := range items {
		if err := tnl.Lossless(PT(&items[i])); err != nil {
			return err
		}
	}

	return nil
}

// TS 36.300 clause 4.6.2
// TS 36.413 clause 8.2.1.2
// TS 36.413 clause 8.2.2.2
// TS 36.413 clause 8.3.1.2
// TS 36.413 clause 8.4.2.2
// TS 36.413 clause 8.4.4.2
// TS 36.413 clause 9.2.1.3
func (r *relay) tnlDownlinkLocked(u *ueContext, p *s1ap.RawPDU, stale []*ueContext) error {
	path, ok := r.tnlPath(u)
	if !ok {
		return nil
	}

	proc, trig := p.ProcedureCode(), p.Trigger()
	b := binder{r: r, path: path, s: &u.Ext.tunnels, proc: proc}

	switch {
	case proc == s1ap.ProcInitialContextSetup && trig == s1ap.TriggeringInitiatingMessage:
		return editIE(p, s1ap.IDERABToBeSetupListCtxtSUReq, func(l *s1ap.ERABToBeSetupListCtxtSUReq) error {
			if err := lossless(*l); err != nil {
				return err
			}

			for i := range *l {
				it := &(*l)[i]
				if err := b.bind(it.ERABID, gtpu.KindUL, true, gtpu.Core, &it.TransportLayerAddress, &it.GTPTEID); err != nil {
					return err
				}
			}

			return nil
		})
	case proc == s1ap.ProcERABSetup && trig == s1ap.TriggeringInitiatingMessage:
		return editIE(p, s1ap.IDERABToBeSetupListBearerSUReq, func(l *s1ap.ERABToBeSetupListBearerSUReq) error {
			if err := lossless(*l); err != nil {
				return err
			}

			for i := range *l {
				it := &(*l)[i]
				if err := b.bind(it.ERABID, gtpu.KindUL, true, gtpu.Core, &it.TransportLayerAddress, &it.GTPTEID); err != nil {
					return err
				}
			}

			return nil
		})
	case proc == s1ap.ProcERABModify && trig == s1ap.TriggeringInitiatingMessage:
		return editIE(p, s1ap.IDERABToBeModifiedListBearerModReq, func(l *s1ap.ERABToBeModifiedListBearerModReq) error {
			if err := lossless(*l); err != nil {
				return err
			}

			for i := range *l {
				it := &(*l)[i]
				if it.TransportInformation == nil {
					continue
				}

				ti := it.TransportInformation
				if err := b.bind(it.ERABID, gtpu.KindUL, true, gtpu.Core, &ti.TransportLayerAddress, &ti.ULGTPTEID); err != nil {
					return err
				}
			}

			return nil
		})
	case proc == s1ap.ProcHandoverPreparation && trig == s1ap.TriggeringSuccessfulOutcome:
		return editIE(p, s1ap.IDERABSubjecttoDataForwardingList, func(l *s1ap.ERABSubjecttoDataForwardingList) error {
			if err := lossless(*l); err != nil {
				return err
			}

			for i := range *l {
				it := &(*l)[i]
				if err := b.bindOptional(it.ERABID, gtpu.KindForwardDL, gtpu.Core, &it.DLTransportLayerAddr, &it.DLGTPTEID); err != nil {
					return err
				}

				if err := b.bindOptional(it.ERABID, gtpu.KindForwardUL, gtpu.Core, &it.ULTransportLayerAddr, &it.ULGTPTEID); err != nil {
					return err
				}
			}

			return nil
		})
	case proc == s1ap.ProcPathSwitchRequest && trig == s1ap.TriggeringSuccessfulOutcome:
		return r.pathSwitchAcknowledgeLocked(b, p, stale)
	case proc == s1ap.ProcERABModificationIndication && trig == s1ap.TriggeringSuccessfulOutcome:
		return r.settleListed(b, p, s1ap.IDERABModifyListBearerModConf)
	case proc == s1ap.ProcDownlinkS1cdma2000tunnelling:
		return strip(p, s1ap.IDERABSubjecttoDataForwardingList)
	}

	return nil
}

// TS 36.413 clause 8.4.4.2
func (r *relay) pathSwitchAcknowledgeLocked(b binder, p *s1ap.RawPDU, stale []*ueContext) error {
	released := map[int]bool{}

	ack, err := parseValue(p, s1ap.ParsePathSwitchRequestAcknowledge)
	if err != nil {
		return err
	}

	for _, it := range ack.ERABToBeReleased {
		released[int(it.ERABID)] = true
	}

	r.plane.Settle(b.s, int(s1ap.ProcPathSwitchRequest), func(bearer int) bool { return !released[bearer] })

	for _, o := range stale {
		r.plane.Adopt(&o.Ext.tunnels, b.s, b.path, func(ref gtpu.Ref) bool {
			return ref.Slot.Kind.Uplink() && !ref.Pending && slices.ContainsFunc(b.s.Refs(), func(x gtpu.Ref) bool {
				return x.Bearer == ref.Bearer
			})
		})
	}

	return editIE(p, s1ap.IDERABToBeSwitchedULList, func(l *s1ap.ERABToBeSwitchedULList) error {
		if err := lossless(*l); err != nil {
			return err
		}

		for i := range *l {
			it := &(*l)[i]
			if err := b.bind(it.ERABID, gtpu.KindUL, false, gtpu.Core, &it.TransportLayerAddress, &it.GTPTEID); err != nil {
				return err
			}
		}

		return nil
	})
}

func parseValue[T any](p *s1ap.RawPDU, parse func([]byte) (*T, error)) (*T, error) {
	b, err := p.Marshal()
	if err != nil {
		return nil, err
	}

	pdu, err := s1ap.Unmarshal(b)
	if err != nil {
		return nil, err
	}

	switch m := pdu.(type) {
	case *s1ap.InitiatingMessage:
		return parse(m.Value)
	case *s1ap.SuccessfulOutcome:
		return parse(m.Value)
	case *s1ap.UnsuccessfulOutcome:
		return parse(m.Value)
	}

	return nil, errNoTNLPath
}

func (r *relay) settleListed(b binder, p *s1ap.RawPDU, id s1ap.ProtocolIEID) error {
	accepted := map[int]bool{}

	var released []int

	switch id {
	case s1ap.IDERABModifyListBearerModConf:
		conf, err := parseValue(p, s1ap.ParseERABModificationConfirm)
		if err != nil {
			return err
		}

		for _, e := range conf.ModifiedERABs {
			accepted[int(e)] = true
		}

		b.proc = s1ap.ProcERABModificationIndication

		for _, it := range conf.ToBeReleased {
			released = append(released, int(it.ERABID))
		}
	case s1ap.IDERABModifyListBearerModRes:
		res, err := parseValue(p, s1ap.ParseERABModifyResponse)
		if err != nil {
			return err
		}

		for _, it := range res.ERABModify {
			accepted[int(it.ERABID)] = true
		}
	}

	r.plane.Settle(b.s, int(b.proc), func(bearer int) bool { return accepted[bearer] })
	r.plane.ReleaseBearers(b.s, released...)

	return nil
}

// TS 36.300 clause 4.6.2
// TS 36.413 clause 8.2.1.2
// TS 36.413 clause 8.2.2.2
// TS 36.413 clause 8.2.3.2
// TS 36.413 clause 8.2.4.2
// TS 36.413 clause 8.3.1.2
// TS 36.413 clause 8.4.2.2
// TS 36.413 clause 8.4.5.2
// TS 36.413 clause 8.4.6.2
func (r *relay) tnlUplinkLocked(u *ueContext, p *s1ap.RawPDU) error {
	path, ok := r.tnlPath(u)
	if !ok {
		return nil
	}

	proc, trig := p.ProcedureCode(), p.Trigger()
	b := binder{r: r, path: path, s: &u.Ext.tunnels, proc: proc}

	switch {
	case proc == s1ap.ProcInitialContextSetup && trig == s1ap.TriggeringSuccessfulOutcome:
		return setupResponse[s1ap.ERABSetupListCtxtSURes](b, p, s1ap.IDERABSetupListCtxtSURes,
			func(it *s1ap.ERABSetupItemCtxtSURes) (s1ap.ERABID, *s1ap.TransportLayerAddress, *s1ap.GTPTEID) {
				return it.ERABID, &it.TransportLayerAddress, &it.GTPTEID
			})
	case proc == s1ap.ProcERABSetup && trig == s1ap.TriggeringSuccessfulOutcome:
		return setupResponse[s1ap.ERABSetupListBearerSURes](b, p, s1ap.IDERABSetupListBearerSURes,
			func(it *s1ap.ERABSetupItemBearerSURes) (s1ap.ERABID, *s1ap.TransportLayerAddress, *s1ap.GTPTEID) {
				return it.ERABID, &it.TransportLayerAddress, &it.GTPTEID
			})
	case proc == s1ap.ProcERABModify && trig == s1ap.TriggeringSuccessfulOutcome:
		return r.settleListed(b, p, s1ap.IDERABModifyListBearerModRes)
	case trig == s1ap.TriggeringUnsuccessfulOutcome &&
		(proc == s1ap.ProcInitialContextSetup || proc == s1ap.ProcHandoverResourceAllocation):
		r.plane.Settle(b.s, int(proc), func(int) bool { return false })
	case proc == s1ap.ProcHandoverResourceAllocation && trig == s1ap.TriggeringSuccessfulOutcome:
		return editIE(p, s1ap.IDERABAdmittedList, func(l *s1ap.ERABAdmittedList) error {
			if err := lossless(*l); err != nil {
				return err
			}

			r.plane.Settle(b.s, int(proc), func(bearer int) bool {
				return slices.ContainsFunc(*l, func(it s1ap.ERABAdmittedItem) bool { return int(it.ERABID) == bearer })
			})

			for i := range *l {
				it := &(*l)[i]
				if err := b.bind(it.ERABID, gtpu.KindDL, false, gtpu.RAN, &it.TransportLayerAddress, &it.GTPTEID); err != nil {
					return err
				}

				if err := b.bindOptional(it.ERABID, gtpu.KindForwardDL, gtpu.RAN, &it.DLTransportLayerAddr, &it.DLGTPTEID); err != nil {
					return err
				}

				if err := b.bindOptional(it.ERABID, gtpu.KindForwardUL, gtpu.RAN, &it.ULTransportLayerAddr, &it.ULGTPTEID); err != nil {
					return err
				}
			}

			return nil
		})
	case proc == s1ap.ProcERABModificationIndication && trig == s1ap.TriggeringInitiatingMessage:
		if err := strip(p, idTunnelInformationForBBF); err != nil {
			return err
		}

		if err := editIE(p, s1ap.IDERABToBeModifiedListBearerModInd, func(l *s1ap.ERABToBeModifiedListBearerModInd) error {
			return bindDownlink(b, true, *l, func(it *s1ap.ERABToBeModifiedItemBearerModInd) (s1ap.ERABID, *s1ap.TransportLayerAddress, *s1ap.GTPTEID) {
				return it.ERABID, &it.TransportLayerAddress, &it.DLGTPTEID
			})
		}); err != nil {
			return err
		}

		return editIE(p, s1ap.IDERABNotToBeModifiedListBearerModInd, func(l *s1ap.ERABNotToBeModifiedListBearerModInd) error {
			return bindDownlink(b, false, *l, func(it *s1ap.ERABNotToBeModifiedItemBearerModInd) (s1ap.ERABID, *s1ap.TransportLayerAddress, *s1ap.GTPTEID) {
				return it.ERABID, &it.TransportLayerAddress, &it.DLGTPTEID
			})
		})
	case proc == s1ap.ProcERABRelease && trig == s1ap.TriggeringSuccessfulOutcome:
		res, err := parseValue(p, s1ap.ParseERABReleaseResponse)
		if err != nil {
			return err
		}

		var ids []int
		for _, it := range res.ERABReleased {
			ids = append(ids, int(it.ERABID))
		}

		r.plane.ReleaseBearers(b.s, ids...)
	case proc == s1ap.ProcERABReleaseIndication:
		ind, err := parseValue(p, s1ap.ParseERABReleaseIndication)
		if err != nil {
			return err
		}

		var ids []int
		for _, it := range ind.ERABReleased {
			ids = append(ids, int(it.ERABID))
		}

		r.plane.ReleaseBearers(b.s, ids...)
	case proc == s1ap.ProcHandoverCancel:
		r.plane.Release(b.s, func(ref gtpu.Ref) bool { return !ref.Slot.Kind.Forwarding() })
	case proc == s1ap.ProcHandoverNotification:
		return strip(p, idTunnelInformationForBBF)
	case proc == s1ap.ProcUplinkNASTransport:
		return strip(p, idGWTransportLayerAddress, idSIPTOLGWTransportLayerAddress)
	}

	return nil
}

// TS 36.300 clause 4.6.2
// TS 36.413 clause 8.4.2.2
func (r *relay) tnlHandoverRequestLocked(u *ueContext, p *s1ap.RawPDU) error {
	path, ok := r.tnlPath(u)
	if !ok {
		return nil
	}

	b := binder{r: r, path: path, s: &u.Ext.tunnels, proc: s1ap.ProcHandoverResourceAllocation}

	return editIE(p, s1ap.IDERABToBeSetupListHOReq, func(l *s1ap.ERABToBeSetupListHOReq) error {
		if err := lossless(*l); err != nil {
			return err
		}

		for i := range *l {
			it := &(*l)[i]
			if err := b.bind(it.ERABID, gtpu.KindUL, true, gtpu.Core, &it.TransportLayerAddress, &it.GTPTEID); err != nil {
				return err
			}
		}

		return nil
	})
}

// TS 36.300 clause 4.6.2
// TS 36.413 clause 8.4.4.2
func (r *relay) tnlPathSwitchRequestLocked(u *ueContext, p *s1ap.RawPDU) error {
	path, ok := r.tnlPath(u)
	if !ok {
		return nil
	}

	if err := strip(p, idTunnelInformationForBBF); err != nil {
		return err
	}

	b := binder{r: r, path: path, s: &u.Ext.tunnels, proc: s1ap.ProcPathSwitchRequest}

	return editIE(p, s1ap.IDERABToBeSwitchedDLList, func(l *s1ap.ERABToBeSwitchedDLList) error {
		if err := lossless(*l); err != nil {
			return err
		}

		for i := range *l {
			it := &(*l)[i]
			if err := b.bind(it.ERABID, gtpu.KindDL, true, gtpu.RAN, &it.TransportLayerAddress, &it.GTPTEID); err != nil {
				return err
			}
		}

		return nil
	})
}

// TS 36.300 clause 4.6.2
func (r *relay) tnlInitialUELocked(u *ueContext, p *s1ap.RawPDU) error {
	if _, ok := r.tnlPath(u); !ok {
		return nil
	}

	return strip(p, idGWTransportLayerAddress, idSIPTOLGWTransportLayerAddress, idTunnelInformationForBBF)
}

func bindDownlink[T any, PT interface {
	*T
	per.Marshaler
}](b binder, pending bool, items []T, fields func(*T) (s1ap.ERABID, *s1ap.TransportLayerAddress, *s1ap.GTPTEID),
) error {
	if err := lossless[T, PT](items); err != nil {
		return err
	}

	for i := range items {
		e, tla, teid := fields(&items[i])
		if err := b.bind(e, gtpu.KindDL, pending, gtpu.RAN, tla, teid); err != nil {
			return err
		}
	}

	return nil
}

func setupResponse[L ~[]T, T any, PT interface {
	*T
	per.Marshaler
}, PL interface {
	*L
	per.Marshaler
	per.Unmarshaler
}](b binder, p *s1ap.RawPDU, id s1ap.ProtocolIEID, fields func(*T) (s1ap.ERABID, *s1ap.TransportLayerAddress, *s1ap.GTPTEID),
) error {
	l, err := readIE[L, PL](p, id)
	if err != nil {
		return err
	}

	accepted := map[int]bool{}

	if l != nil {
		for i := range *l {
			e, _, _ := fields(&(*l)[i])
			accepted[int(e)] = true
		}
	}

	b.r.plane.Settle(b.s, int(b.proc), func(bearer int) bool { return accepted[bearer] })

	return editIE[L, PL](p, id, func(l *L) error {
		return bindDownlink[T, PT](b, false, *l, fields)
	})
}

var tunnelProcedures = []s1ap.ProcedureCode{
	s1ap.ProcInitialContextSetup, s1ap.ProcERABSetup, s1ap.ProcERABModify, s1ap.ProcHandoverPreparation,
	s1ap.ProcHandoverResourceAllocation, s1ap.ProcPathSwitchRequest, s1ap.ProcERABModificationIndication,
}

func procedureLabel(p s1ap.ProcedureCode) string {
	name, _ := s1ap.ProcedureCodeName(p)

	return metrics.ProcedureLabel(name)
}

func (r *relay) abort(ctx context.Context, log *slog.Logger, u *ueContext, proc s1ap.ProcedureCode, err error) {
	r.mu.Lock()
	gone := r.ues.Remove(u)
	r.mu.Unlock()

	r.plane.TunnelSetupFailed(u.OperatorID, metrics.InterfaceS1, procedureLabel(proc), err)
	tracing.SpanFailed(ctx, assoc.ErrTypeError)
	log.WarnContext(ctx, "UE released: the user-plane transport could not be relayed", slog.Any("error", err))
	r.resetMMEs(ctx, gone, causeTransportResource)

	if u.Core != nil {
		r.resetRANs(ctx, u.Core.Operator(), gone, causeTransportResource)
	}
}

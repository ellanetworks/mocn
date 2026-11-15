package ng

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/core/per"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/gtpu"
	"github.com/ellanetworks/mocn/internal/metrics"
	"github.com/ellanetworks/mocn/internal/tnl"
	"github.com/ellanetworks/mocn/internal/tracing"
)

var causeTransportResource = ngap.CauseTransportResourceUnavailable.Cause()

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

func readIE[T any, PT interface {
	*T
	per.Unmarshaler
}](p *ngap.RawPDU, id ngap.ProtocolIEID) (*T, error) {
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

func editIE[T any, PT interface {
	*T
	per.Marshaler
	per.Unmarshaler
}](p *ngap.RawPDU, id ngap.ProtocolIEID, edit func(*T) error,
) error {
	v, err := readIE[T, PT](p, id)
	if err != nil || v == nil {
		return err
	}

	if err := edit(v); err != nil {
		return err
	}

	b, err := per.Marshal(PT(v), per.Aligned)
	if err != nil {
		return err
	}

	i, err := p.Index(id)
	if err != nil {
		return err
	}

	return p.SetValue(i, b)
}

type sessionField[T any] func(*T) (ngap.PDUSessionID, *ngap.TransferContainer)

func sessions[L ~[]T, T any, PL interface {
	*L
	per.Unmarshaler
}](p *ngap.RawPDU, id ngap.ProtocolIEID, field sessionField[T]) (map[int]bool, error) {
	l, err := readIE[L, PL](p, id)
	if err != nil {
		return nil, err
	}

	out := map[int]bool{}

	if l != nil {
		for i := range *l {
			s, _ := field(&(*l)[i])
			out[int(s)] = true
		}
	}

	return out, nil
}

func editTransfers[L ~[]T, T any, PL interface {
	*L
	per.Marshaler
	per.Unmarshaler
}, PT interface {
	*T
	per.Marshaler
}](p *ngap.RawPDU, id ngap.ProtocolIEID, field sessionField[T], strict bool,
	edit func(session int, tc *ngap.TransferContainer) error,
) error {
	return editIE[L, PL](p, id, func(l *L) error {
		for i := range *l {
			it := &(*l)[i]
			if strict {
				if err := tnl.Lossless(PT(it)); err != nil {
					return err
				}
			}

			s, tc := field(it)
			if err := edit(int(s), tc); err != nil {
				return err
			}
		}

		return nil
	})
}

type binder struct {
	r       *relay
	path    gtpu.Path
	s       *gtpu.Session
	proc    ngap.ProcedureCode
	session int
	pending bool
}

func gtpTunnel(u *ngap.UPTransportLayerInformation) (*ngap.GTPTunnel, error) {
	if u.GTPTunnel == nil {
		return nil, fmt.Errorf("%w: %T", tnl.ErrUnknownExtension, u)
	}

	return u.GTPTunnel, nil
}

func rewritten(g *ngap.GTPTunnel, e gtpu.Endpoint) error {
	addr, err := tnl.Address(e.Addr)
	if err != nil {
		return err
	}

	g.TransportLayerAddress, g.GTPTEID = addr, ngap.GTPTEID(e.TEID)

	return nil
}

func (b binder) up(kind gtpu.Kind, idx int, to gtpu.Side, u *ngap.UPTransportLayerInformation) error {
	g, err := gtpTunnel(u)
	if err != nil {
		return err
	}

	e, err := b.r.plane.Bind(b.s, gtpu.Binding{
		Path: b.path, Proc: int(b.proc), Bearer: b.session, Slot: gtpu.Slot{Kind: kind, Index: idx}, Pending: b.pending,
	}, to, tnl.Octets(g.TransportLayerAddress), uint32(g.GTPTEID))
	if err != nil {
		return err
	}

	return rewritten(g, e)
}

func (b binder) upOpt(kind gtpu.Kind, idx int, to gtpu.Side, u *ngap.UPTransportLayerInformation) error {
	if u == nil {
		return nil
	}

	return b.up(kind, idx, to, u)
}

func (b binder) reverse(from gtpu.Side, u *ngap.UPTransportLayerInformation) error {
	g, err := gtpTunnel(u)
	if err != nil {
		return err
	}

	e, err := b.r.plane.Reversed(b.s, from, tnl.Octets(g.TransportLayerAddress), uint32(g.GTPTEID))
	if err != nil {
		return err
	}

	return rewritten(g, e)
}

func (b binder) reverseOpt(from gtpu.Side, u *ngap.UPTransportLayerInformation) error {
	if u == nil {
		return nil
	}

	return b.reverse(from, u)
}

func (b binder) identified(from gtpu.Side, u *ngap.UPTransportLayerInformation) (gtpu.Ref, error) {
	g, err := gtpTunnel(u)
	if err != nil {
		return gtpu.Ref{}, err
	}

	teid := uint32(g.GTPTEID)

	for _, r := range b.s.Refs() {
		if r.TEID == teid && r.Ingress == from && !r.Pending {
			if err := b.reverse(from, u); err != nil {
				return gtpu.Ref{}, err
			}

			return r, nil
		}
	}

	return gtpu.Ref{}, gtpu.ErrUnknownEndpoint
}

func uplinkSlot(dl gtpu.Ref) gtpu.Slot {
	switch dl.Slot.Kind {
	case gtpu.KindDL:
		return gtpu.Slot{Kind: gtpu.KindUL}
	case gtpu.KindDLRedundant:
		return gtpu.Slot{Kind: gtpu.KindULRedundant}
	case gtpu.KindDLAdditionalRedundant, gtpu.KindDLAdditionalRedundantPair:
		return gtpu.Slot{Kind: gtpu.KindULAdditionalRedundantPair, Index: -int(dl.TEID)}
	}

	return gtpu.Slot{Kind: gtpu.KindULAdditionalPair, Index: -int(dl.TEID)}
}

// TS 38.413 clause 8.2.3.2
func (b binder) replaceUplink(dl, ul *ngap.UPTransportLayerInformation) error {
	if dl == nil {
		if ul != nil {
			return gtpu.ErrUnknownEndpoint
		}

		return nil
	}

	ref, err := b.identified(gtpu.Core, dl)
	if err != nil {
		return err
	}

	slot := uplinkSlot(ref)

	return b.upOpt(slot.Kind, slot.Index, gtpu.Core, ul)
}

// TS 38.413 clause 9.3.1.271
func (b binder) relayed(u *ngap.UPTransportLayerInformation) error {
	g, err := gtpTunnel(u)
	if err != nil {
		return err
	}

	v4, v6 := gtpu.Addresses(tnl.Octets(g.TransportLayerAddress))
	teid := uint32(g.GTPTEID)

	for _, r := range b.s.Refs() {
		if r.Ingress != gtpu.RAN || r.Superseded || r.Peer.TEID != teid || (r.Peer.Addr != v4 && r.Peer.Addr != v6) {
			continue
		}

		if t, ok := b.r.plane.Get(r.TEID); ok {
			return rewritten(g, t.Endpoint())
		}
	}

	return gtpu.ErrUnknownEndpoint
}

func (b binder) qos(kind gtpu.Kind, idx int, to gtpu.Side, q *ngap.QosFlowPerTNLInformation) error {
	if q == nil {
		return nil
	}

	return b.up(kind, idx, to, &q.UPTransportLayerInformation)
}

func (b binder) qosList(kind gtpu.Kind, to gtpu.Side, l ngap.QosFlowPerTNLInformationList) error {
	for i := range l {
		if err := b.qos(kind, i, to, &l[i].QosFlowPerTNLInformation); err != nil {
			return err
		}
	}

	return nil
}

func (b binder) upList(kind gtpu.Kind, to gtpu.Side, l ngap.UPTransportLayerInformationList) error {
	for i := range l {
		if err := b.up(kind, i, to, &l[i].NGUUPTNLInformation); err != nil {
			return err
		}
	}

	return nil
}

func (b binder) pairs(kind gtpu.Kind, newSide gtpu.Side, l ngap.UPTransportLayerInformationPairList) error {
	for i := range l {
		ul, dl := &l[i].ULNGUUPTNLInformation, &l[i].DLNGUUPTNLInformation
		if newSide == gtpu.Core {
			if err := b.up(kind, i, gtpu.Core, ul); err != nil {
				return err
			}

			if err := b.reverse(gtpu.Core, dl); err != nil {
				return err
			}

			continue
		}

		if err := b.reverse(gtpu.RAN, ul); err != nil {
			return err
		}

		if err := b.up(kind, i, gtpu.RAN, dl); err != nil {
			return err
		}
	}

	return nil
}

func decodeTransfer[T any](tc ngap.TransferContainer, decode func(ngap.TransferContainer) (*T, error)) (*T, error) {
	t, err := decode(tc)
	if err != nil {
		return nil, err
	}

	if err := tnl.Lossless(t); err != nil {
		return nil, err
	}

	return t, nil
}

// TS 38.413 clause 9.3.4.1
func (b binder) setupRequestTransfer(tc *ngap.TransferContainer) error {
	t, err := decodeTransfer(*tc, ngap.DecodePDUSessionResourceSetupRequestTransfer)
	if err != nil {
		return err
	}

	if err := b.up(gtpu.KindUL, 0, gtpu.Core, &t.ULNGUUPTNLInformation); err != nil {
		return err
	}

	if err := b.upList(gtpu.KindULAdditional, gtpu.Core, t.AdditionalULNGUUPTNLInformation); err != nil {
		return err
	}

	if err := b.upOpt(gtpu.KindULRedundant, 0, gtpu.Core, t.RedundantULNGUUPTNLInformation); err != nil {
		return err
	}

	if err := b.upList(gtpu.KindULAdditionalRedundant, gtpu.Core, t.AdditionalRedundantULNGUUPTNLInformation); err != nil {
		return err
	}

	*tc, err = t.Marshal()

	return err
}

// TS 38.413 clause 9.3.4.2
func (b binder) setupResponseTransfer(tc *ngap.TransferContainer) error {
	t, err := decodeTransfer(*tc, ngap.DecodePDUSessionResourceSetupResponseTransfer)
	if err != nil {
		return err
	}

	if err := b.qos(gtpu.KindDL, 0, gtpu.RAN, &t.DLQosFlowPerTNLInformation); err != nil {
		return err
	}

	if err := b.qosList(gtpu.KindDLAdditional, gtpu.RAN, t.AdditionalDLQosFlowPerTNLInformation); err != nil {
		return err
	}

	if err := b.qos(gtpu.KindDLRedundant, 0, gtpu.RAN, t.RedundantDLQosFlowPerTNLInformation); err != nil {
		return err
	}

	if err := b.qosList(gtpu.KindDLAdditionalRedundant, gtpu.RAN, t.AdditionalRedundantDLQosFlowPerTNLInformation); err != nil {
		return err
	}

	*tc, err = t.Marshal()

	return err
}

// TS 38.413 clause 8.2.3.2
// TS 38.413 clause 9.3.1.271
// TS 38.413 clause 9.3.4.3
func (b binder) modifyRequestTransfer(tc *ngap.TransferContainer) error {
	t, err := decodeTransfer(*tc, ngap.DecodePDUSessionResourceModifyRequestTransfer)
	if err != nil {
		return err
	}

	for i := range t.ULNGUUPTNLModify {
		it := &t.ULNGUUPTNLModify[i]
		if err := b.replaceUplink(&it.DLNGUUPTNLInformation, &it.ULNGUUPTNLInformation); err != nil {
			return err
		}

		if err := b.replaceUplink(it.RedundantDLNGUUPTNLInformation, it.RedundantULNGUUPTNLInformation); err != nil {
			return err
		}
	}

	for i := range t.QosFlowAddOrModifyRequest {
		it := &t.QosFlowAddOrModifyRequest[i]
		if err := b.upOpt(gtpu.KindULQosFlow, int(it.QosFlowIdentifier), gtpu.Core, it.ULNGUUPTNLInformation); err != nil {
			return err
		}
	}

	if err := b.upList(gtpu.KindULAdditional, gtpu.Core, t.AdditionalULNGUUPTNLInformation); err != nil {
		return err
	}

	if err := b.upList(gtpu.KindULAdditionalRedundant, gtpu.Core, t.AdditionalRedundantULNGUUPTNLInformation); err != nil {
		return err
	}

	if err := b.upOpt(gtpu.KindULRedundant, 0, gtpu.Core, t.RedundantULNGUUPTNLInformation); err != nil {
		return err
	}

	if f := t.UserPlaneFailureIndication; f != nil {
		if err := b.relayed(&f.ULNGUUPTNLInformation); err != nil {
			return err
		}

		if err := b.reverse(gtpu.Core, &f.DLNGUUPTNLInformation); err != nil {
			return err
		}
	}

	*tc, err = t.Marshal()

	return err
}

// TS 38.413 clause 8.2.3.2
// TS 38.413 clause 9.3.4.4
func (b binder) modifyResponseTransfer(tc *ngap.TransferContainer) error {
	t, err := decodeTransfer(*tc, ngap.DecodePDUSessionResourceModifyResponseTransfer)
	if err != nil {
		return err
	}

	if err := b.upOpt(gtpu.KindDL, 0, gtpu.RAN, t.DLNGUUPTNLInformation); err != nil {
		return err
	}

	if err := b.reverseOpt(gtpu.RAN, t.ULNGUUPTNLInformation); err != nil {
		return err
	}

	for i := range t.QosFlowAddOrModifyResponse {
		it := &t.QosFlowAddOrModifyResponse[i]
		if err := b.upOpt(gtpu.KindDLQosFlow, int(it.QosFlowIdentifier), gtpu.RAN, it.DLNGUUPTNLInformation); err != nil {
			return err
		}
	}

	if err := b.qosList(gtpu.KindDLAdditional, gtpu.RAN, t.AdditionalDLQosFlowPerTNLInformation); err != nil {
		return err
	}

	if err := b.pairs(gtpu.KindDLAdditionalPair, gtpu.RAN, t.AdditionalNGUUPTNLInformation); err != nil {
		return err
	}

	if err := b.upOpt(gtpu.KindDLRedundant, 0, gtpu.RAN, t.RedundantDLNGUUPTNLInformation); err != nil {
		return err
	}

	if err := b.reverseOpt(gtpu.RAN, t.RedundantULNGUUPTNLInformation); err != nil {
		return err
	}

	if err := b.qosList(gtpu.KindDLAdditionalRedundant, gtpu.RAN, t.AdditionalRedundantDLQosFlowPerTNLInformation); err != nil {
		return err
	}

	if err := b.pairs(gtpu.KindDLAdditionalRedundantPair, gtpu.RAN, t.AdditionalRedundantNGUUPTNLInformation); err != nil {
		return err
	}

	*tc, err = t.Marshal()

	return err
}

// TS 38.413 clause 9.3.4.5
func (b binder) modifyIndicationTransfer(tc *ngap.TransferContainer) error {
	t, err := decodeTransfer(*tc, ngap.DecodePDUSessionResourceModifyIndicationTransfer)
	if err != nil {
		return err
	}

	if err := b.qos(gtpu.KindDL, 0, gtpu.RAN, &t.DLQosFlowPerTNLInformation); err != nil {
		return err
	}

	if err := b.qosList(gtpu.KindDLAdditional, gtpu.RAN, t.AdditionalDLQosFlowPerTNLInformation); err != nil {
		return err
	}

	if err := b.qos(gtpu.KindDLRedundant, 0, gtpu.RAN, t.RedundantDLQosFlowPerTNLInformation); err != nil {
		return err
	}

	if err := b.qosList(gtpu.KindDLAdditionalRedundant, gtpu.RAN, t.AdditionalRedundantDLQosFlowPerTNLInformation); err != nil {
		return err
	}

	*tc, err = t.Marshal()

	return err
}

// TS 38.413 clause 9.3.4.6
func (b binder) modifyConfirmTransfer(tc *ngap.TransferContainer) error {
	t, err := decodeTransfer(*tc, ngap.DecodePDUSessionResourceModifyConfirmTransfer)
	if err != nil {
		return err
	}

	if err := b.up(gtpu.KindUL, 0, gtpu.Core, &t.ULNGUUPTNLInformation); err != nil {
		return err
	}

	if err := b.pairs(gtpu.KindULAdditionalPair, gtpu.Core, t.AdditionalNGUUPTNLInformation); err != nil {
		return err
	}

	if err := b.upOpt(gtpu.KindULRedundant, 0, gtpu.Core, t.RedundantULNGUUPTNLInformation); err != nil {
		return err
	}

	if err := b.pairs(gtpu.KindULAdditionalRedundantPair, gtpu.Core, t.AdditionalRedundantNGUUPTNLInformation); err != nil {
		return err
	}

	*tc, err = t.Marshal()

	return err
}

// TS 38.413 clause 8.4.4.2
// TS 38.413 clause 9.3.4.8
func (b binder) pathSwitchRequestTransfer(tc *ngap.TransferContainer) error {
	t, err := decodeTransfer(*tc, ngap.DecodePathSwitchRequestTransfer)
	if err != nil {
		return err
	}

	if err := b.up(gtpu.KindDL, 0, gtpu.RAN, &t.DLNGUUPTNLInformation); err != nil {
		return err
	}

	if err := b.qosList(gtpu.KindDLAdditional, gtpu.RAN, t.AdditionalDLQosFlowPerTNLInformation); err != nil {
		return err
	}

	if err := b.upOpt(gtpu.KindDLRedundant, 0, gtpu.RAN, t.RedundantDLNGUUPTNLInformation); err != nil {
		return err
	}

	if err := b.qosList(gtpu.KindDLAdditionalRedundant, gtpu.RAN, t.AdditionalRedundantDLQosFlowPerTNLInformation); err != nil {
		return err
	}

	t.DLNGUTNLInformationReused, t.RedundantDLNGUTNLInformationReused = nil, nil

	*tc, err = t.Marshal()

	return err
}

// TS 38.413 clause 9.3.4.9
func (b binder) pathSwitchRequestAcknowledgeTransfer(tc *ngap.TransferContainer) error {
	t, err := decodeTransfer(*tc, ngap.DecodePathSwitchRequestAcknowledgeTransfer)
	if err != nil {
		return err
	}

	if err := b.upOpt(gtpu.KindUL, 0, gtpu.Core, t.ULNGUUPTNLInformation); err != nil {
		return err
	}

	if err := b.pairs(gtpu.KindULAdditionalPair, gtpu.Core, t.AdditionalNGUUPTNLInformation); err != nil {
		return err
	}

	if err := b.upOpt(gtpu.KindULRedundant, 0, gtpu.Core, t.RedundantULNGUUPTNLInformation); err != nil {
		return err
	}

	if err := b.pairs(gtpu.KindULAdditionalRedundantPair, gtpu.Core, t.AdditionalRedundantNGUUPTNLInformation); err != nil {
		return err
	}

	*tc, err = t.Marshal()

	return err
}

func (b binder) forwardingDRBs(to gtpu.Side, l ngap.DataForwardingResponseDRBList) error {
	for i := range l {
		it := &l[i]
		if err := b.upOpt(gtpu.KindForwardDLDRB, int(it.DRBID), to, it.DLForwardingUPTNLInformation); err != nil {
			return err
		}

		if err := b.upOpt(gtpu.KindForwardULDRB, int(it.DRBID), to, it.ULForwardingUPTNLInformation); err != nil {
			return err
		}
	}

	return nil
}

func (b binder) forwardingERABs(to gtpu.Side, l ngap.DataForwardingResponseERABList) error {
	for i := range l {
		it := &l[i]
		if err := b.up(gtpu.KindForwardDLERAB, int(it.ERABID), to, &it.DLForwardingUPTNLInformation); err != nil {
			return err
		}
	}

	return nil
}

// TS 38.413 clause 9.3.4.11
func (b binder) handoverRequestAcknowledgeTransfer(tc *ngap.TransferContainer) error {
	t, err := decodeTransfer(*tc, ngap.DecodeHandoverRequestAcknowledgeTransfer)
	if err != nil {
		return err
	}

	if err := b.up(gtpu.KindDL, 0, gtpu.RAN, &t.DLNGUUPTNLInformation); err != nil {
		return err
	}

	if err := b.upOpt(gtpu.KindForwardDL, 0, gtpu.RAN, t.DLForwardingUPTNLInformation); err != nil {
		return err
	}

	if err := b.forwardingDRBs(gtpu.RAN, t.DataForwardingResponseDRB); err != nil {
		return err
	}

	for i := range t.AdditionalDLUPTNLInformationForHO {
		it := &t.AdditionalDLUPTNLInformationForHO[i]
		if err := b.up(gtpu.KindDLAdditional, i, gtpu.RAN, &it.AdditionalDLNGUUPTNLInformation); err != nil {
			return err
		}

		if err := b.upOpt(gtpu.KindForwardDLAdditional, i, gtpu.RAN, it.AdditionalDLForwardingUPTNLInformation); err != nil {
			return err
		}

		if err := b.upOpt(gtpu.KindDLAdditionalRedundant, i, gtpu.RAN, it.AdditionalRedundantDLNGUUPTNLInformation); err != nil {
			return err
		}
	}

	if err := b.upOpt(gtpu.KindForwardUL, 0, gtpu.RAN, t.ULForwardingUPTNLInformation); err != nil {
		return err
	}

	if err := b.upList(gtpu.KindForwardULAdditional, gtpu.RAN, t.AdditionalULForwardingUPTNLInformation); err != nil {
		return err
	}

	if err := b.forwardingERABs(gtpu.RAN, t.DataForwardingResponseERAB); err != nil {
		return err
	}

	if err := b.upOpt(gtpu.KindDLRedundant, 0, gtpu.RAN, t.RedundantDLNGUUPTNLInformation); err != nil {
		return err
	}

	*tc, err = t.Marshal()

	return err
}

// TS 38.413 clause 9.3.4.10
func (b binder) handoverCommandTransfer(tc *ngap.TransferContainer) error {
	t, err := decodeTransfer(*tc, ngap.DecodeHandoverCommandTransfer)
	if err != nil {
		return err
	}

	if err := b.upOpt(gtpu.KindForwardDL, 0, gtpu.Core, t.DLForwardingUPTNLInformation); err != nil {
		return err
	}

	if err := b.forwardingDRBs(gtpu.Core, t.DataForwardingResponseDRB); err != nil {
		return err
	}

	if err := b.qosList(gtpu.KindForwardDLAdditional, gtpu.Core, t.AdditionalDLForwardingUPTNLInformation); err != nil {
		return err
	}

	if err := b.upOpt(gtpu.KindForwardUL, 0, gtpu.Core, t.ULForwardingUPTNLInformation); err != nil {
		return err
	}

	if err := b.upList(gtpu.KindForwardULAdditional, gtpu.Core, t.AdditionalULForwardingUPTNLInformation); err != nil {
		return err
	}

	if err := b.forwardingERABs(gtpu.Core, t.DataForwardingResponseERAB); err != nil {
		return err
	}

	*tc, err = t.Marshal()

	return err
}

func (r *relay) binder(u *ueContext, proc ngap.ProcedureCode) (binder, bool) {
	path, ok := r.tnlPath(u)
	if !ok {
		return binder{}, false
	}

	return binder{r: r, path: path, s: &u.Ext.tunnels, proc: proc}, true
}

func (b binder) each(pending bool, edit func(binder, *ngap.TransferContainer) error) func(int, *ngap.TransferContainer) error {
	return func(session int, tc *ngap.TransferContainer) error {
		b.session, b.pending = session, pending

		return edit(b, tc)
	}
}

func (b binder) settle(accepted map[int]bool) {
	b.r.plane.Settle(b.s, int(b.proc), func(session int) bool { return accepted[session] })
}

func setupSURes(it *ngap.PDUSessionResourceSetupItemSURes) (ngap.PDUSessionID, *ngap.TransferContainer) {
	return it.PDUSessionID, &it.Transfer
}

func setupCxtRes(it *ngap.PDUSessionResourceSetupItemCxtRes) (ngap.PDUSessionID, *ngap.TransferContainer) {
	return it.PDUSessionID, &it.Transfer
}

func modRes(it *ngap.PDUSessionResourceModifyItemModRes) (ngap.PDUSessionID, *ngap.TransferContainer) {
	return it.PDUSessionID, &it.Transfer
}

func modCfm(it *ngap.PDUSessionResourceModifyItemModCfm) (ngap.PDUSessionID, *ngap.TransferContainer) {
	return it.PDUSessionID, &it.Transfer
}

func admitted(it *ngap.PDUSessionResourceAdmittedItem) (ngap.PDUSessionID, *ngap.TransferContainer) {
	return it.PDUSessionID, &it.Transfer
}

func switched(it *ngap.PDUSessionResourceSwitchedItem) (ngap.PDUSessionID, *ngap.TransferContainer) {
	return it.PDUSessionID, &it.Transfer
}

func releasedRelRes(it *ngap.PDUSessionResourceReleasedItemRelRes) (ngap.PDUSessionID, *ngap.TransferContainer) {
	return it.PDUSessionID, &it.Transfer
}

func releasedNot(it *ngap.PDUSessionResourceReleasedItemNot) (ngap.PDUSessionID, *ngap.TransferContainer) {
	return it.PDUSessionID, &it.Transfer
}

// TS 38.300 clause 4.10.1
// TS 38.413 clause 8.2.1.2
// TS 38.413 clause 8.2.3.2
// TS 38.413 clause 8.3.1.2
// TS 38.413 clause 8.4.1.2
// TS 38.413 clause 8.4.4.2
// TS 38.413 clause 8.2.5.2
func (r *relay) tnlDownlinkLocked(u *ueContext, p *ngap.RawPDU, stale []*ueContext) error {
	proc, trig := p.ProcedureCode(), p.Trigger()

	b, ok := r.binder(u, proc)
	if !ok {
		return nil
	}

	switch {
	case proc == ngap.ProcInitialContextSetup && trig == ngap.TriggeringInitiatingMessage:
		return editTransfers[ngap.PDUSessionResourceSetupListCxtReq](p, ngap.IDPDUSessionResourceSetupListCxtReq,
			func(it *ngap.PDUSessionResourceSetupItemCxtReq) (ngap.PDUSessionID, *ngap.TransferContainer) {
				return it.PDUSessionID, &it.Transfer
			}, false, b.each(true, binder.setupRequestTransfer))
	case proc == ngap.ProcPDUSessionResourceSetup && trig == ngap.TriggeringInitiatingMessage:
		return editTransfers[ngap.PDUSessionResourceSetupListSUReq](p, ngap.IDPDUSessionResourceSetupListSUReq,
			func(it *ngap.PDUSessionResourceSetupItemSUReq) (ngap.PDUSessionID, *ngap.TransferContainer) {
				return it.PDUSessionID, &it.Transfer
			}, false, b.each(true, binder.setupRequestTransfer))
	case proc == ngap.ProcPDUSessionResourceModify && trig == ngap.TriggeringInitiatingMessage:
		return editTransfers[ngap.PDUSessionResourceModifyListModReq](p, ngap.IDPDUSessionResourceModifyListModReq,
			func(it *ngap.PDUSessionResourceModifyItemModReq) (ngap.PDUSessionID, *ngap.TransferContainer) {
				return it.PDUSessionID, &it.Transfer
			}, false, b.each(true, binder.modifyRequestTransfer))
	case proc == ngap.ProcPDUSessionResourceModifyIndication && trig == ngap.TriggeringSuccessfulOutcome:
		accepted, err := sessions[ngap.PDUSessionResourceModifyListModCfm](p, ngap.IDPDUSessionResourceModifyListModCfm, modCfm)
		if err != nil {
			return err
		}

		b.settle(accepted)

		return editTransfers[ngap.PDUSessionResourceModifyListModCfm](p, ngap.IDPDUSessionResourceModifyListModCfm, modCfm,
			true, b.each(false, binder.modifyConfirmTransfer))
	case proc == ngap.ProcPathSwitchRequest && trig == ngap.TriggeringSuccessfulOutcome:
		accepted, err := sessions[ngap.PDUSessionResourceSwitchedList](p, ngap.IDPDUSessionResourceSwitchedList, switched)
		if err != nil {
			return err
		}

		b.settle(accepted)

		for _, o := range stale {
			r.plane.Adopt(&o.Ext.tunnels, b.s, b.path, func(ref gtpu.Ref) bool {
				return ref.Slot.Kind.Uplink() && !ref.Pending && accepted[ref.Bearer]
			})
		}

		return editTransfers[ngap.PDUSessionResourceSwitchedList](p, ngap.IDPDUSessionResourceSwitchedList, switched, true,
			b.each(false, binder.pathSwitchRequestAcknowledgeTransfer))
	case proc == ngap.ProcHandoverPreparation && trig == ngap.TriggeringSuccessfulOutcome:
		return editTransfers[ngap.PDUSessionResourceHandoverList](p, ngap.IDPDUSessionResourceHandoverList,
			func(it *ngap.PDUSessionResourceHandoverItem) (ngap.PDUSessionID, *ngap.TransferContainer) {
				return it.PDUSessionID, &it.Transfer
			}, true, b.each(false, binder.handoverCommandTransfer))
	}

	return nil
}

// TS 38.300 clause 4.10.1
// TS 38.413 clause 8.2.1.2
// TS 38.413 clause 8.2.2.2
// TS 38.413 clause 8.2.3.2
// TS 38.413 clause 8.2.4.2
// TS 38.413 clause 8.2.5.2
// TS 38.413 clause 8.3.1.2
// TS 38.413 clause 8.4.2.2
// TS 38.413 clause 8.4.6.2
func (r *relay) tnlUplinkLocked(u *ueContext, p *ngap.RawPDU) error {
	proc, trig := p.ProcedureCode(), p.Trigger()

	b, ok := r.binder(u, proc)
	if !ok {
		return nil
	}

	switch {
	case proc == ngap.ProcInitialContextSetup && trig == ngap.TriggeringSuccessfulOutcome:
		accepted, err := sessions[ngap.PDUSessionResourceSetupListCxtRes](p, ngap.IDPDUSessionResourceSetupListCxtRes, setupCxtRes)
		if err != nil {
			return err
		}

		b.settle(accepted)

		return editTransfers[ngap.PDUSessionResourceSetupListCxtRes](p, ngap.IDPDUSessionResourceSetupListCxtRes, setupCxtRes,
			true, b.each(false, binder.setupResponseTransfer))
	case proc == ngap.ProcPDUSessionResourceSetup && trig == ngap.TriggeringSuccessfulOutcome:
		accepted, err := sessions[ngap.PDUSessionResourceSetupListSURes](p, ngap.IDPDUSessionResourceSetupListSURes, setupSURes)
		if err != nil {
			return err
		}

		b.settle(accepted)

		return editTransfers[ngap.PDUSessionResourceSetupListSURes](p, ngap.IDPDUSessionResourceSetupListSURes, setupSURes,
			true, b.each(false, binder.setupResponseTransfer))
	case proc == ngap.ProcPDUSessionResourceModify && trig == ngap.TriggeringSuccessfulOutcome:
		accepted, err := sessions[ngap.PDUSessionResourceModifyListModRes](p, ngap.IDPDUSessionResourceModifyListModRes, modRes)
		if err != nil {
			return err
		}

		b.settle(accepted)

		return editTransfers[ngap.PDUSessionResourceModifyListModRes](p, ngap.IDPDUSessionResourceModifyListModRes, modRes,
			true, b.each(false, binder.modifyResponseTransfer))
	case proc == ngap.ProcPDUSessionResourceModifyIndication && trig == ngap.TriggeringInitiatingMessage:
		return editTransfers[ngap.PDUSessionResourceModifyListModInd](p, ngap.IDPDUSessionResourceModifyListModInd,
			func(it *ngap.PDUSessionResourceModifyItemModInd) (ngap.PDUSessionID, *ngap.TransferContainer) {
				return it.PDUSessionID, &it.Transfer
			}, true, b.each(true, binder.modifyIndicationTransfer))
	case proc == ngap.ProcHandoverResourceAllocation && trig == ngap.TriggeringSuccessfulOutcome:
		accepted, err := sessions[ngap.PDUSessionResourceAdmittedList](p, ngap.IDPDUSessionResourceAdmittedList, admitted)
		if err != nil {
			return err
		}

		b.settle(accepted)

		return editTransfers[ngap.PDUSessionResourceAdmittedList](p, ngap.IDPDUSessionResourceAdmittedList, admitted, true,
			b.each(false, binder.handoverRequestAcknowledgeTransfer))
	case trig == ngap.TriggeringUnsuccessfulOutcome &&
		(proc == ngap.ProcInitialContextSetup || proc == ngap.ProcHandoverResourceAllocation):
		b.settle(nil)
	case proc == ngap.ProcPDUSessionResourceRelease && trig == ngap.TriggeringSuccessfulOutcome:
		released, err := sessions[ngap.PDUSessionResourceReleasedListRelRes](p, ngap.IDPDUSessionResourceReleasedListRelRes,
			releasedRelRes)
		if err != nil {
			return err
		}

		r.releaseSessions(b.s, released)
	case proc == ngap.ProcPDUSessionResourceNotify:
		released, err := sessions[ngap.PDUSessionResourceReleasedListNot](p, ngap.IDPDUSessionResourceReleasedListNot, releasedNot)
		if err != nil {
			return err
		}

		r.releaseSessions(b.s, released)
	case proc == ngap.ProcHandoverCancel:
		r.plane.Release(b.s, func(ref gtpu.Ref) bool { return !ref.Slot.Kind.Forwarding() })
	}

	return nil
}

func (r *relay) releaseSessions(s *gtpu.Session, released map[int]bool) {
	r.plane.Release(s, func(ref gtpu.Ref) bool { return !released[ref.Bearer] })
}

// TS 38.300 clause 4.10.1
// TS 38.413 clause 8.4.2.2
func (r *relay) tnlHandoverRequestLocked(u *ueContext, p *ngap.RawPDU) error {
	b, ok := r.binder(u, ngap.ProcHandoverResourceAllocation)
	if !ok {
		return nil
	}

	return editTransfers[ngap.PDUSessionResourceSetupListHOReq](p, ngap.IDPDUSessionResourceSetupListHOReq,
		func(it *ngap.PDUSessionResourceSetupItemHOReq) (ngap.PDUSessionID, *ngap.TransferContainer) {
			return it.PDUSessionID, &it.Transfer
		}, false, b.each(true, binder.setupRequestTransfer))
}

// TS 38.300 clause 4.10.1
// TS 38.413 clause 8.4.4.2
func (r *relay) tnlPathSwitchRequestLocked(u *ueContext, p *ngap.RawPDU) error {
	b, ok := r.binder(u, ngap.ProcPathSwitchRequest)
	if !ok {
		return nil
	}

	return editTransfers[ngap.PDUSessionResourceToBeSwitchedDLList](p, ngap.IDPDUSessionResourceToBeSwitchedDLList,
		func(it *ngap.PDUSessionResourceToBeSwitchedDLItem) (ngap.PDUSessionID, *ngap.TransferContainer) {
			return it.PDUSessionID, &it.Transfer
		}, true, b.each(true, binder.pathSwitchRequestTransfer))
}

var tunnelProcedures = []ngap.ProcedureCode{
	ngap.ProcInitialContextSetup, ngap.ProcPDUSessionResourceSetup, ngap.ProcPDUSessionResourceModify,
	ngap.ProcPDUSessionResourceModifyIndication, ngap.ProcHandoverPreparation, ngap.ProcHandoverResourceAllocation,
	ngap.ProcPathSwitchRequest,
}

func procedureLabel(p ngap.ProcedureCode) string {
	name, _ := ngap.ProcedureCodeName(p)

	return metrics.ProcedureLabel(name)
}

func (r *relay) abort(ctx context.Context, log *slog.Logger, u *ueContext, proc ngap.ProcedureCode, err error) {
	r.mu.Lock()
	gone := r.ues.Remove(u)
	r.mu.Unlock()

	r.plane.TunnelSetupFailed(u.OperatorID, metrics.InterfaceNG, procedureLabel(proc), err)
	tracing.SpanFailed(ctx, assoc.ErrTypeError)
	log.WarnContext(ctx, "UE released: the user-plane transport could not be relayed", slog.Any("error", err))
	r.resetAMFs(ctx, gone, causeTransportResource)

	if u.Core != nil {
		r.resetRANs(ctx, u.Core.Operator(), gone, causeTransportResource)
	}
}

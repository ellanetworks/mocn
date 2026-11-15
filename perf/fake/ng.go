package fake

import (
	"fmt"
	"net/netip"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/ng"
	"github.com/ellanetworks/mocn/internal/plmnid"
	"github.com/ellanetworks/mocn/internal/tnl"
)

var (
	ngNAS   = ngap.NASPDU{0x7e, 0x00, 0x41, 0x79, 0x00, 0x0d, 0x01, 0x00, 0xf1, 0x10, 0xca, 0xfe, 0xba, 0xbe}
	ngSlice = ngap.SNSSAI{SST: 1, SD: &ngap.SD{0x01, 0x02, 0x03}}
)

type ngRadio struct {
	plmn ngap.PLMNIdentity
}

func newNGRadio(p db.PLMN) (*ngRadio, error) {
	id, err := plmnid.Encode[ngap.PLMNIdentity](p)
	if err != nil {
		return nil, err
	}

	return &ngRadio{plmn: id}, nil
}

func (r *ngRadio) port() int    { return 38412 }
func (r *ngRadio) ppid() uint32 { return ng.PPID }

func (r *ngRadio) setup(radio int) ([]byte, error) {
	name := fmt.Sprintf("perf-gnb-%d", radio)
	drx := ngap.PagingDRXv128

	return (&ngap.NGSetupRequest{
		GlobalRANNodeID: ngap.GlobalRANNodeID{GlobalGNBID: &ngap.GlobalGNBID{
			PLMNIdentity: r.plmn, GNBID: ngap.GNBID{GNBID: &ngap.GNBIDValue{Value: uint32(radio + 1), Bits: 24}},
		}},
		RANNodeName: &name,
		SupportedTAList: ngap.SupportedTAList{{TAC: 1, BroadcastPLMNList: []ngap.BroadcastPLMNItem{{
			PLMNIdentity: r.plmn, TAISliceSupportList: ngap.SliceSupportList{{SNSSAI: ngSlice}},
		}}}},
		DefaultPagingDRX: &drx,
	}).Marshal()
}

func (r *ngRadio) setupResult(raw []byte) (bool, error) {
	pdu, err := ngap.Unmarshal(raw)
	if err != nil {
		return false, err
	}

	switch m := pdu.(type) {
	case *ngap.SuccessfulOutcome:
		return m.ProcedureCode == ngap.ProcNGSetup, nil
	case *ngap.UnsuccessfulOutcome:
		if m.ProcedureCode == ngap.ProcNGSetup {
			return false, errSetupRejected
		}
	}

	return false, nil
}

func (r *ngRadio) initialUE(radio int, ue uint32) ([]byte, error) {
	return (&ngap.InitialUEMessage{
		RANUENGAPID: ngap.RANUENGAPID(ue),
		NASPDU:      ngNAS,
		UserLocationInformation: ngap.UserLocationInformation{NR: &ngap.UserLocationInformationNR{
			NRCGI: ngap.NRCGI{PLMNIdentity: r.plmn, NRCellIdentity: ngap.NRCellIdentity(radio+1) << 12},
			TAI:   ngap.TAI{PLMNIdentity: r.plmn, TAC: 1},
		}},
		RRCEstablishmentCause: new(ngap.RRCEstablishmentCause(3)),
	}).Marshal()
}

func (r *ngRadio) handle(raw []byte, local netip.Addr, teid uint32) (reply []byte, s Session, ue uint32, ok bool,
	err error,
) {
	pdu, err := ngap.Unmarshal(raw)
	if err != nil {
		return nil, Session{}, 0, false, err
	}

	m, isInit := pdu.(*ngap.InitiatingMessage)
	if !isInit || m.ProcedureCode != ngap.ProcPDUSessionResourceSetup {
		return nil, Session{}, 0, false, nil
	}

	req, err := ngap.ParsePDUSessionResourceSetupRequest(m.Value)
	if err != nil {
		return nil, Session{}, 0, false, err
	}

	if len(req.PDUSessionResourceSetup) == 0 {
		return nil, Session{}, 0, false, errNoBearer
	}

	item := req.PDUSessionResourceSetup[0]

	tr, err := ngap.ParsePDUSessionResourceSetupRequestTransfer(item.Transfer)
	if err != nil {
		return nil, Session{}, 0, false, err
	}

	ul := tr.ULNGUUPTNLInformation.GTPTunnel
	if ul == nil {
		return nil, Session{}, 0, false, errNoBearer
	}

	peer, err := address(tnl.Octets(ul.TransportLayerAddress))
	if err != nil {
		return nil, Session{}, 0, false, err
	}

	tla, err := tnl.Address(local)
	if err != nil {
		return nil, Session{}, 0, false, err
	}

	transfer, err := (&ngap.PDUSessionResourceSetupResponseTransfer{
		DLQosFlowPerTNLInformation: ngap.QosFlowPerTNLInformation{
			UPTransportLayerInformation: ngap.UPTransportLayerInformation{GTPTunnel: &ngap.GTPTunnel{
				TransportLayerAddress: tla, GTPTEID: ngap.GTPTEID(teid),
			}},
			AssociatedQosFlowList: ngap.AssociatedQosFlowList{{QosFlowIdentifier: 1}},
		},
	}).Marshal()
	if err != nil {
		return nil, Session{}, 0, false, err
	}

	reply, err = (&ngap.PDUSessionResourceSetupResponse{
		AMFUENGAPID: new(req.AMFUENGAPID),
		RANUENGAPID: new(req.RANUENGAPID),
		PDUSessionResourceSetup: ngap.PDUSessionResourceSetupListSURes{{
			PDUSessionID: item.PDUSessionID, Transfer: transfer,
		}},
	}).Marshal()

	s = Session{Local: local, LocalTEID: teid, Peer: peer, PeerTEID: uint32(ul.GTPTEID)}

	return reply, s, uint32(req.RANUENGAPID), true, err
}

type ngCore struct {
	plmn ngap.PLMNIdentity
	ues  *ueTable
}

func newNGCore(p db.PLMN, ues *ueTable) (*ngCore, error) {
	id, err := plmnid.Encode[ngap.PLMNIdentity](p)
	if err != nil {
		return nil, err
	}

	return &ngCore{plmn: id, ues: ues}, nil
}

func (c *ngCore) port() int    { return 38412 }
func (c *ngCore) ppid() uint32 { return ng.PPID }

func (c *ngCore) handle(raw []byte) ([][]byte, error) {
	pdu, err := ngap.Unmarshal(raw)
	if err != nil {
		return nil, err
	}

	switch m := pdu.(type) {
	case *ngap.InitiatingMessage:
		switch m.ProcedureCode {
		case ngap.ProcNGSetup:
			return c.setup()
		case ngap.ProcInitialUEMessage:
			return c.initialUE(m.Value)
		}
	case *ngap.SuccessfulOutcome:
		if m.ProcedureCode == ngap.ProcPDUSessionResourceSetup {
			return nil, c.sessionSetUp(m.Value)
		}
	}

	return nil, nil
}

func (c *ngCore) setup() ([][]byte, error) {
	b, err := (&ngap.NGSetupResponse{
		AMFName: "perf-amf",
		ServedGUAMIList: ngap.ServedGUAMIList{{GUAMI: ngap.GUAMI{
			PLMNIdentity: c.plmn, AMFRegionID: 2, AMFSetID: 1, AMFPointer: 0,
		}}},
		RelativeAMFCapacity: new(uint8(255)),
		PLMNSupportList: ngap.PLMNSupportList{{
			PLMNIdentity: c.plmn, SliceSupportList: ngap.SliceSupportList{{SNSSAI: ngSlice}},
		}},
	}).Marshal()

	return [][]byte{b}, err
}

func (c *ngCore) initialUE(value []byte) ([][]byte, error) {
	msg, err := ngap.ParseInitialUEMessage(value)
	if err != nil {
		return nil, err
	}

	id, gw := c.ues.open()

	tla, err := tnl.Address(gw.Addr)
	if err != nil {
		return nil, err
	}

	nas, err := (&ngap.DownlinkNASTransport{
		AMFUENGAPID: ngap.AMFUENGAPID(id), RANUENGAPID: msg.RANUENGAPID, NASPDU: ngNAS,
	}).Marshal()
	if err != nil {
		return nil, err
	}

	transfer, err := (&ngap.PDUSessionResourceSetupRequestTransfer{
		ULNGUUPTNLInformation: ngap.UPTransportLayerInformation{GTPTunnel: &ngap.GTPTunnel{
			TransportLayerAddress: tla, GTPTEID: ngap.GTPTEID(gw.TEID),
		}},
		PDUSessionType: ngap.PDUSessionTypeIPv4,
		QosFlowSetupRequest: ngap.QosFlowSetupRequestList{{
			QosFlowIdentifier: 1,
			QosFlowLevelQosParameters: ngap.QosFlowLevelQosParameters{
				QosCharacteristics:             ngap.QosCharacteristics{NonDynamic5QI: &ngap.NonDynamic5QIDescriptor{FiveQI: 9}},
				AllocationAndRetentionPriority: ngap.AllocationAndRetentionPriority{PriorityLevelARP: 1},
			},
		}},
	}).Marshal()
	if err != nil {
		return nil, err
	}

	setup, err := (&ngap.PDUSessionResourceSetupRequest{
		AMFUENGAPID: ngap.AMFUENGAPID(id),
		RANUENGAPID: msg.RANUENGAPID,
		PDUSessionResourceSetup: ngap.PDUSessionResourceSetupListSUReq{{
			PDUSessionID: 1, SNSSAI: ngSlice, Transfer: transfer,
		}},
	}).Marshal()

	return [][]byte{nas, setup}, err
}

func (c *ngCore) sessionSetUp(value []byte) error {
	resp, err := ngap.ParsePDUSessionResourceSetupResponse(value)
	if err != nil {
		return err
	}

	if resp.AMFUENGAPID == nil || len(resp.PDUSessionResourceSetup) == 0 {
		return errNoBearer
	}

	tr, err := ngap.ParsePDUSessionResourceSetupResponseTransfer(resp.PDUSessionResourceSetup[0].Transfer)
	if err != nil {
		return err
	}

	dl := tr.DLQosFlowPerTNLInformation.UPTransportLayerInformation.GTPTunnel
	if dl == nil {
		return errNoBearer
	}

	peer, err := address(tnl.Octets(dl.TransportLayerAddress))
	if err != nil {
		return err
	}

	return c.ues.established(uint64(*resp.AMFUENGAPID), peer, uint32(dl.GTPTEID))
}

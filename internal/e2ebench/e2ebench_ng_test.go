package e2ebench

import (
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"testing"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/core/per/ap"
	"github.com/ellanetworks/mocn/internal/ng"
)

type ngPLMN = ngap.PLMNIdentity

const (
	ngPPID     = ng.PPID
	ngUEStream = 1
	pduSession = 1
)

var (
	ngNAS          = ngap.NASPDU{0x7e, 0x00, 0x41, 0x79, 0x00, 0x0d, 0x01, 0x00, 0xf1, 0x10, 0xca, 0xfe, 0xba, 0xbe}
	ngSlice        = ngap.SliceSupportItem{SNSSAI: ngap.SNSSAI{SST: 1, SD: &ngap.SD{0x01, 0x02, 0x03}}}
	ngReleaseCause = ngap.CauseRadioNetworkUserInactivity.Cause()
)

func ngTunnel(a netip.Addr, teid uint32) ngap.UPTransportLayerInformation {
	return ngap.UPTransportLayerInformation{GTPTunnel: &ngap.GTPTunnel{
		TransportLayerAddress: ap.TransportLayerAddressFromOctets(a.AsSlice()), GTPTEID: ngap.GTPTEID(teid),
	}}
}

func ngValue(raw []byte) ([]byte, error) {
	pdu, err := ngap.Unmarshal(raw)
	if err != nil {
		return nil, err
	}

	switch m := pdu.(type) {
	case *ngap.InitiatingMessage:
		return m.Value, nil
	case *ngap.SuccessfulOutcome:
		return m.Value, nil
	case *ngap.UnsuccessfulOutcome:
		return m.Value, nil
	}

	return nil, errors.New("NGAP PDU without a value")
}

func expectNG(raw []byte, proc ngap.ProcedureCode) error {
	p, err := ngap.ParseRawPDU(raw)
	if err != nil {
		return err
	}

	if p.Trigger() != ngap.TriggeringInitiatingMessage || p.ProcedureCode() != proc {
		return fmt.Errorf("got NGAP %v %v, want initiating %v", p.Trigger(), p.ProcedureCode(), proc)
	}

	return nil
}

func ngGUAMI(plmn ngPLMN) ngap.GUAMI {
	return ngap.GUAMI{PLMNIdentity: plmn, AMFRegionID: 2, AMFSetID: 1, AMFPointer: 0}
}

type amf struct {
	coreNode
	m    *benchMOCN
	op   int
	plmn ngPLMN
	next uint64

	mu  sync.Mutex
	ues map[uint64]uint32
}

func (c *amf) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return len(c.ues)
}

func (c *amf) ranID(id ngap.AMFUENGAPID) (ngap.RANUENGAPID, error) {
	c.mu.Lock()
	ranID, ok := c.ues[uint64(id)]
	c.mu.Unlock()

	if !ok {
		return 0, fmt.Errorf("AMF of operator %d: unknown AMF UE NGAP ID %d", c.op+1, id)
	}

	return ngap.RANUENGAPID(ranID), nil
}

func (c *amf) ids(p *ngap.RawPDU) (ngap.AMFUENGAPID, ngap.RANUENGAPID, error) {
	id, ok, err := p.AMFUENGAPID()
	if err != nil || !ok {
		return 0, 0, fmt.Errorf("NGAP %v without an AMF UE NGAP ID: %w", p.ProcedureCode(), err)
	}

	ranID, err := c.ranID(id)

	return id, ranID, err
}

func (c *amf) page(token uint32) error {
	n, err := c.node()
	if err != nil {
		return err
	}

	return n.send(&ngap.Paging{
		UEPagingIdentity: &ngap.UEPagingIdentity{FiveGSTMSI: &ngap.FiveGSTMSI{AMFSetID: 1, AMFPointer: 0, FiveGTMSI: ngap.FiveGTMSI(token)}},
		TAIListForPaging: ngap.TAIListForPaging{{TAI: ngap.TAI{PLMNIdentity: c.plmn, TAC: 1}}},
	}, 0)
}

func (c *amf) handle(n node, raw []byte) error {
	p, err := ngap.ParseRawPDU(raw)
	if err != nil {
		return err
	}

	trig, proc := p.Trigger(), p.ProcedureCode()

	switch {
	case trig == ngap.TriggeringInitiatingMessage && proc == ngap.ProcNGSetup:
		return n.send(&ngap.NGSetupResponse{
			AMFName:             fmt.Sprintf("amf%d", c.op+1),
			RelativeAMFCapacity: ngap.Ptr(uint8(255)),
			ServedGUAMIList:     ngap.ServedGUAMIList{{GUAMI: ngGUAMI(c.plmn)}},
			PLMNSupportList:     ngap.PLMNSupportList{{PLMNIdentity: c.plmn, SliceSupportList: ngap.SliceSupportList{ngSlice}}},
		}, 0)
	case trig == ngap.TriggeringInitiatingMessage && proc == ngap.ProcRANConfigurationUpdate:
		return n.send(&ngap.RANConfigurationUpdateAcknowledge{}, 0)
	case trig == ngap.TriggeringInitiatingMessage && proc == ngap.ProcInitialUEMessage:
		return c.initialUE(n, p)
	case trig == ngap.TriggeringSuccessfulOutcome && proc == ngap.ProcInitialContextSetup:
		return c.downlinkNAS(n, p)
	case trig == ngap.TriggeringInitiatingMessage && proc == ngap.ProcUplinkNASTransport:
		return c.setUpSession(n, p)
	case trig == ngap.TriggeringSuccessfulOutcome && proc == ngap.ProcPDUSessionResourceSetup:
		return c.sessionSetUp(n, raw)
	case trig == ngap.TriggeringInitiatingMessage && proc == ngap.ProcUEContextReleaseRequest:
		return c.releaseRequested(n, p)
	case trig == ngap.TriggeringSuccessfulOutcome && proc == ngap.ProcUEContextRelease:
		return c.released(p)
	}

	return fmt.Errorf("AMF of operator %d: unexpected NGAP %v %v", c.op+1, trig, proc)
}

func (c *amf) initialUE(n node, p *ngap.RawPDU) error {
	ranID, ok, err := p.RANUENGAPID()
	if err != nil || !ok {
		return fmt.Errorf("INITIAL UE MESSAGE without a RAN UE NGAP ID: %w", err)
	}

	var uli ngap.UserLocationInformation
	if ok, err := p.Get(ngap.IDUserLocationInformation, &uli); err != nil || !ok || uli.NR == nil {
		return fmt.Errorf("INITIAL UE MESSAGE without an NR User Location Information: %w", err)
	}

	if uli.NR.TAI.PLMNIdentity != c.plmn {
		return fmt.Errorf("INITIAL UE MESSAGE for PLMN %x routed to the AMF of operator %d", uli.NR.TAI.PLMNIdentity, c.op+1)
	}

	c.next++
	id := c.next

	c.mu.Lock()
	c.ues[id] = uint32(ranID)
	c.mu.Unlock()

	return n.send(&ngap.InitialContextSetupRequest{
		AMFUENGAPID:  ngap.AMFUENGAPID(id),
		RANUENGAPID:  ranID,
		GUAMI:        ngGUAMI(c.plmn),
		AllowedNSSAI: ngap.AllowedNSSAI{{SNSSAI: ngSlice.SNSSAI}},
		UESecurityCapabilities: ngap.UESecurityCapabilities{
			NREncryptionAlgorithms:          ngap.SecurityAlgorithmsFrom(0xe000),
			NRIntegrityProtectionAlgorithms: ngap.SecurityAlgorithmsFrom(0xe000),
		},
		SecurityKey: securityKey,
		NASPDU:      &ngNAS,
	}, ngUEStream)
}

func (c *amf) downlinkNAS(n node, p *ngap.RawPDU) error {
	id, ranID, err := c.ids(p)
	if err != nil {
		return err
	}

	return n.send(&ngap.DownlinkNASTransport{AMFUENGAPID: id, RANUENGAPID: ranID, NASPDU: ngNAS}, ngUEStream)
}

func (c *amf) setUpSession(n node, p *ngap.RawPDU) error {
	id, ranID, err := c.ids(p)
	if err != nil {
		return err
	}

	transfer, err := (&ngap.PDUSessionResourceSetupRequestTransfer{
		ULNGUUPTNLInformation: ngTunnel(coreUPs[c.op], uint32(id)),
		PDUSessionType:        ngap.PDUSessionTypeIPv4,
		QosFlowSetupRequest: ngap.QosFlowSetupRequestList{{
			QosFlowIdentifier: 1,
			QosFlowLevelQosParameters: ngap.QosFlowLevelQosParameters{
				QosCharacteristics:             ngap.QosCharacteristics{NonDynamic5QI: &ngap.NonDynamic5QIDescriptor{FiveQI: 9}},
				AllocationAndRetentionPriority: ngap.AllocationAndRetentionPriority{PriorityLevelARP: 1},
			},
		}},
	}).Marshal()
	if err != nil {
		return err
	}

	return n.send(&ngap.PDUSessionResourceSetupRequest{
		AMFUENGAPID: id, RANUENGAPID: ranID,
		PDUSessionResourceSetup: ngap.PDUSessionResourceSetupListSUReq{{
			PDUSessionID: pduSession, SNSSAI: ngSlice.SNSSAI, Transfer: transfer,
		}},
	}, ngUEStream)
}

func (c *amf) sessionSetUp(n node, raw []byte) error {
	value, err := ngValue(raw)
	if err != nil {
		return err
	}

	resp, err := ngap.ParsePDUSessionResourceSetupResponse(value)
	if err != nil {
		return err
	}

	if len(resp.PDUSessionResourceSetup) != 1 {
		return fmt.Errorf("PDU SESSION RESOURCE SETUP RESPONSE with %d sessions", len(resp.PDUSessionResourceSetup))
	}

	tr, err := ngap.ParsePDUSessionResourceSetupResponseTransfer(resp.PDUSessionResourceSetup[0].Transfer)
	if err != nil {
		return err
	}

	dl := tr.DLQosFlowPerTNLInformation.UPTransportLayerInformation.GTPTunnel
	if dl == nil {
		return errors.New("PDU SESSION RESOURCE SETUP RESPONSE without a downlink GTP tunnel")
	}

	if err := c.m.checkDownlink(dl.TransportLayerAddress); err != nil {
		return err
	}

	ranID, err := c.ranID(*resp.AMFUENGAPID)
	if err != nil {
		return err
	}

	return n.send(&ngap.DownlinkNASTransport{AMFUENGAPID: *resp.AMFUENGAPID, RANUENGAPID: ranID, NASPDU: ngNAS}, ngUEStream)
}

func (c *amf) releaseRequested(n node, p *ngap.RawPDU) error {
	id, ranID, err := c.ids(p)
	if err != nil {
		return err
	}

	return n.send(&ngap.UEContextReleaseCommand{
		UENGAPIDs: ngap.UENGAPIDs{UENGAPIDPair: &ngap.UENGAPIDPair{AMFUENGAPID: id, RANUENGAPID: ranID}},
		Cause:     &ngReleaseCause,
	}, ngUEStream)
}

func (c *amf) released(p *ngap.RawPDU) error {
	id, _, err := c.ids(p)
	if err != nil {
		return err
	}

	c.mu.Lock()
	delete(c.ues, uint64(id))
	c.mu.Unlock()

	return nil
}

type gnb struct {
	radio
}

func (m *benchMOCN) startGNB(tb testing.TB, n int) *gnb {
	tb.Helper()

	g := &gnb{radio: newRadio(m, m.dialRadio(tb, ngPPID), n)}
	m.serve(g.node, g.handle)

	broadcast := make(ngap.BroadcastPLMNList, 0, len(plmns))
	for _, p := range plmns {
		broadcast = append(broadcast, ngap.BroadcastPLMNItem{
			PLMNIdentity: mustPLMN[ngPLMN](p), TAISliceSupportList: ngap.SliceSupportList{ngSlice},
		})
	}

	name := fmt.Sprintf("bench-gnb-%d", n)
	if err := g.send(&ngap.NGSetupRequest{
		GlobalRANNodeID: ngap.GlobalRANNodeID{GlobalGNBID: &ngap.GlobalGNBID{
			PLMNIdentity: broadcast[0].PLMNIdentity,
			GNBID:        ngap.GNBID{GNBID: &ngap.GNBIDValue{Value: uint32(0x1000 + n), Bits: 24}},
		}},
		RANNodeName:      &name,
		SupportedTAList:  ngap.SupportedTAList{{TAC: 1, BroadcastPLMNList: broadcast}},
		DefaultPagingDRX: ngap.Ptr(ngap.PagingDRXv128),
	}, 0); err != nil {
		tb.Fatalf("send NG SETUP REQUEST: %v", err)
	}

	return g
}

func (g *gnb) handle(raw []byte) error {
	p, err := ngap.ParseRawPDU(raw)
	if err != nil {
		return err
	}

	trig, proc := p.Trigger(), p.ProcedureCode()

	switch {
	case trig == ngap.TriggeringSuccessfulOutcome && proc == ngap.ProcNGSetup:
		g.ready()
		return nil
	case trig == ngap.TriggeringInitiatingMessage && proc == ngap.ProcAMFConfigurationUpdate:
		return g.send(&ngap.AMFConfigurationUpdateAcknowledge{}, 0)
	case trig == ngap.TriggeringInitiatingMessage && proc == ngap.ProcPaging:
		value, err := ngValue(raw)
		if err != nil {
			return err
		}

		paging, err := ngap.ParsePaging(value)
		if err != nil {
			return err
		}

		if paging.UEPagingIdentity == nil || paging.UEPagingIdentity.FiveGSTMSI == nil {
			return errors.New("paging without a 5G-S-TMSI")
		}

		return g.paged(uint32(paging.UEPagingIdentity.FiveGSTMSI.FiveGTMSI))
	case trig == ngap.TriggeringInitiatingMessage && proc == ngap.ProcUEContextRelease:
		ids, _, err := p.UENGAPIDs()
		if err != nil {
			return err
		}

		if ids.UENGAPIDPair == nil {
			return errors.New("UE CONTEXT RELEASE COMMAND without a RAN UE NGAP ID")
		}

		return g.deliver(uint64(ids.UENGAPIDPair.RANUENGAPID), raw)
	}

	if id, ok, err := p.RANUENGAPID(); err == nil && ok {
		return g.deliver(uint64(id), raw)
	}

	return fmt.Errorf("gNB: unexpected NGAP %v %v", trig, proc)
}

func (g *gnb) uli(op int) ngap.UserLocationInformation {
	plmn := mustPLMN[ngPLMN](plmns[op])

	return ngap.UserLocationInformation{NR: &ngap.UserLocationInformationNR{
		NRCGI: ngap.NRCGI{PLMNIdentity: plmn, NRCellIdentity: 0x19b01}, TAI: ngap.TAI{PLMNIdentity: plmn, TAC: 1},
	}}
}

func (g *gnb) awaitNG(u *benchUE, proc ngap.ProcedureCode) ([]byte, error) {
	raw, err := g.m.await(u.ch)
	if err != nil {
		return nil, fmt.Errorf("await NGAP %v: %w", proc, err)
	}

	return raw, expectNG(raw, proc)
}

func (g *gnb) register(u *benchUE) error {
	id := ngap.RANUENGAPID(u.id)

	if err := g.send(&ngap.InitialUEMessage{
		RANUENGAPID: id, NASPDU: ngNAS, UserLocationInformation: g.uli(u.op),
		RRCEstablishmentCause: ngap.Ptr(ngap.RRCEstablishmentCause(3)),
	}, ngUEStream); err != nil {
		return err
	}

	raw, err := g.awaitNG(u, ngap.ProcInitialContextSetup)
	if err != nil {
		return err
	}

	p, err := ngap.ParseRawPDU(raw)
	if err != nil {
		return err
	}

	amfID, ok, err := p.AMFUENGAPID()
	if err != nil || !ok {
		return fmt.Errorf("INITIAL CONTEXT SETUP REQUEST without an AMF UE NGAP ID: %w", err)
	}

	u.coreID = uint64(amfID)

	if err := g.send(&ngap.InitialContextSetupResponse{AMFUENGAPID: &amfID, RANUENGAPID: &id}, ngUEStream); err != nil {
		return err
	}

	_, err = g.awaitNG(u, ngap.ProcDownlinkNASTransport)

	return err
}

func (g *gnb) establish(u *benchUE) error {
	id, amfID := ngap.RANUENGAPID(u.id), ngap.AMFUENGAPID(u.coreID)

	if err := g.send(&ngap.UplinkNASTransport{
		AMFUENGAPID: amfID, RANUENGAPID: id, NASPDU: ngNAS, UserLocationInformation: ngap.Ptr(g.uli(u.op)),
	}, ngUEStream); err != nil {
		return err
	}

	raw, err := g.awaitNG(u, ngap.ProcPDUSessionResourceSetup)
	if err != nil {
		return err
	}

	value, err := ngValue(raw)
	if err != nil {
		return err
	}

	req, err := ngap.ParsePDUSessionResourceSetupRequest(value)
	if err != nil {
		return err
	}

	if len(req.PDUSessionResourceSetup) != 1 {
		return fmt.Errorf("PDU SESSION RESOURCE SETUP REQUEST with %d sessions", len(req.PDUSessionResourceSetup))
	}

	tr, err := ngap.ParsePDUSessionResourceSetupRequestTransfer(req.PDUSessionResourceSetup[0].Transfer)
	if err != nil {
		return err
	}

	if tr.ULNGUUPTNLInformation.GTPTunnel == nil {
		return errors.New("PDU SESSION RESOURCE SETUP REQUEST without an uplink GTP tunnel")
	}

	if err := g.m.checkUplink(u.op, tr.ULNGUUPTNLInformation.GTPTunnel.TransportLayerAddress); err != nil {
		return err
	}

	transfer, err := (&ngap.PDUSessionResourceSetupResponseTransfer{
		DLQosFlowPerTNLInformation: ngap.QosFlowPerTNLInformation{
			UPTransportLayerInformation: ngTunnel(radioUP, radioTEID(g.index, u.id)),
			AssociatedQosFlowList:       ngap.AssociatedQosFlowList{{QosFlowIdentifier: 1}},
		},
	}).Marshal()
	if err != nil {
		return err
	}

	if err := g.send(&ngap.PDUSessionResourceSetupResponse{
		AMFUENGAPID: &amfID, RANUENGAPID: &id,
		PDUSessionResourceSetup: ngap.PDUSessionResourceSetupListSURes{{PDUSessionID: pduSession, Transfer: transfer}},
	}, ngUEStream); err != nil {
		return err
	}

	_, err = g.awaitNG(u, ngap.ProcDownlinkNASTransport)

	return err
}

func (g *gnb) release(u *benchUE) error {
	id, amfID := ngap.RANUENGAPID(u.id), ngap.AMFUENGAPID(u.coreID)

	if err := g.send(&ngap.UEContextReleaseRequest{AMFUENGAPID: amfID, RANUENGAPID: id, Cause: &ngReleaseCause}, ngUEStream); err != nil {
		return err
	}

	if _, err := g.awaitNG(u, ngap.ProcUEContextRelease); err != nil {
		return err
	}

	return g.send(&ngap.UEContextReleaseComplete{AMFUENGAPID: &amfID, RANUENGAPID: &id}, ngUEStream)
}

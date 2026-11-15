package e2ebench

import (
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"testing"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/s1"
	"github.com/ellanetworks/mocn/internal/tnl"
)

type s1PLMN = s1ap.PLMNIdentity

const (
	s1PPID     = s1.PPID
	s1UEStream = 1
)

var (
	s1NAS          = s1ap.NASPDU{0x07, 0x41, 0x71, 0x08, 0x09, 0x10, 0x10, 0x00, 0x00, 0x00, 0x00, 0x01}
	s1CoreUPs      = [2]s1ap.TransportLayerAddress{mustTLA(coreUPs[0]), mustTLA(coreUPs[1])}
	s1RadioUP      = mustTLA(radioUP)
	s1ReleaseCause = s1ap.CauseRadioNetworkUserInactivity.Cause()
)

func mustTLA(addr netip.Addr) s1ap.TransportLayerAddress {
	a, err := tnl.Address(addr)
	if err != nil {
		panic(err)
	}

	return a
}

func s1Value(raw []byte) ([]byte, error) {
	pdu, err := s1ap.Unmarshal(raw)
	if err != nil {
		return nil, err
	}

	switch m := pdu.(type) {
	case *s1ap.InitiatingMessage:
		return m.Value, nil
	case *s1ap.SuccessfulOutcome:
		return m.Value, nil
	case *s1ap.UnsuccessfulOutcome:
		return m.Value, nil
	}

	return nil, errors.New("S1AP PDU without a value")
}

func expectS1(raw []byte, proc s1ap.ProcedureCode) error {
	p, err := s1ap.ParseRawPDU(raw)
	if err != nil {
		return err
	}

	if p.Trigger() != s1ap.TriggeringInitiatingMessage || p.ProcedureCode() != proc {
		return fmt.Errorf("got S1AP %v %v, want initiating %v", p.Trigger(), p.ProcedureCode(), proc)
	}

	return nil
}

func (e *enb) awaitS1(u *benchUE, proc s1ap.ProcedureCode) ([]byte, error) {
	raw, err := e.m.await(u.ch)
	if err != nil {
		return nil, fmt.Errorf("await S1AP %v: %w", proc, err)
	}

	return raw, expectS1(raw, proc)
}

type mme struct {
	coreNode
	m    *benchMOCN
	op   int
	plmn s1PLMN
	next uint32

	mu  sync.Mutex
	ues map[uint32]uint32
}

func (c *mme) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return len(c.ues)
}

func (c *mme) enbID(id s1ap.MMEUES1APID) (s1ap.ENBUES1APID, error) {
	c.mu.Lock()
	enbID, ok := c.ues[uint32(id)]
	c.mu.Unlock()

	if !ok {
		return 0, fmt.Errorf("MME of operator %d: unknown MME UE S1AP ID %d", c.op+1, id)
	}

	return s1ap.ENBUES1APID(enbID), nil
}

func (c *mme) page(token uint32) error {
	n, err := c.node()
	if err != nil {
		return err
	}

	return n.send(&s1ap.Paging{
		UEIdentityIndexValue: s1ap.Ptr(uint16(token % 1024)),
		UEPagingID:           &s1ap.UEPagingID{STMSI: &s1ap.STMSI{MMEC: 1, MTMSI: s1ap.MTMSI(token)}},
		CNDomain:             s1ap.Ptr(s1ap.CNDomainPS),
		TAIList:              s1ap.TAIList{{TAI: s1ap.TAI{PLMNIdentity: c.plmn, TAC: 1}}},
	}, 0)
}

func (c *mme) handle(n node, raw []byte) error {
	p, err := s1ap.ParseRawPDU(raw)
	if err != nil {
		return err
	}

	trig, proc := p.Trigger(), p.ProcedureCode()

	switch {
	case trig == s1ap.TriggeringInitiatingMessage && proc == s1ap.ProcS1Setup:
		return n.send(&s1ap.S1SetupResponse{
			ServedGUMMEIs: s1ap.ServedGUMMEIs{{
				ServedPLMNs: []s1ap.PLMNIdentity{c.plmn}, ServedGroupIDs: []s1ap.MMEGroupID{{0, 1}},
				ServedMMECs: []s1ap.MMECode{1},
			}},
			RelativeMMECapacity: s1ap.Ptr(uint8(255)),
		}, 0)
	case trig == s1ap.TriggeringInitiatingMessage && proc == s1ap.ProcENBConfigurationUpdate:
		return n.send(&s1ap.ENBConfigurationUpdateAcknowledge{}, 0)
	case trig == s1ap.TriggeringInitiatingMessage && proc == s1ap.ProcInitialUEMessage:
		return c.initialUE(n, p)
	case trig == s1ap.TriggeringSuccessfulOutcome && proc == s1ap.ProcInitialContextSetup:
		return c.contextSetUp(n, raw)
	case trig == s1ap.TriggeringInitiatingMessage && proc == s1ap.ProcUEContextReleaseRequest:
		return c.releaseRequested(n, p)
	case trig == s1ap.TriggeringSuccessfulOutcome && proc == s1ap.ProcUEContextRelease:
		return c.released(p)
	}

	return fmt.Errorf("MME of operator %d: unexpected S1AP %v %v", c.op+1, trig, proc)
}

func (c *mme) initialUE(n node, p *s1ap.RawPDU) error {
	enbID, ok, err := p.ENBUES1APID()
	if err != nil || !ok {
		return fmt.Errorf("INITIAL UE MESSAGE without an eNB UE S1AP ID: %w", err)
	}

	var tai s1ap.TAI
	if ok, err := p.Get(s1ap.IDTAI, &tai); err != nil || !ok {
		return fmt.Errorf("INITIAL UE MESSAGE without a TAI: %w", err)
	}

	if tai.PLMNIdentity != c.plmn {
		return fmt.Errorf("INITIAL UE MESSAGE for PLMN %x routed to the MME of operator %d", tai.PLMNIdentity, c.op+1)
	}

	c.next++
	id := c.next

	c.mu.Lock()
	c.ues[id] = uint32(enbID)
	c.mu.Unlock()

	return n.send(&s1ap.InitialContextSetupRequest{
		MMEUES1APID:               s1ap.MMEUES1APID(id),
		ENBUES1APID:               enbID,
		UEAggregateMaximumBitRate: s1ap.UEAggregateMaximumBitRate{DL: 1000000000, UL: 500000000},
		ERABToBeSetup: []s1ap.ERABToBeSetupItemCtxtSUReq{{
			ERABID: 5, QoS: s1ap.ERABLevelQoSParameters{QCI: 9, ARP: s1ap.AllocationAndRetentionPriority{
				PriorityLevel: 15, PreemptionCapability: s1ap.PreemptionShallNotTrigger,
				PreemptionVulnerability: s1ap.PreemptionNotPreemptable,
			}},
			TransportLayerAddress: s1CoreUPs[c.op], GTPTEID: s1ap.GTPTEID(id), NASPDU: s1NAS,
		}},
		UESecurityCapabilities: s1ap.UESecurityCapabilities{
			EncryptionAlgorithms:          s1ap.SecurityAlgorithms{Value: 0x8000},
			IntegrityProtectionAlgorithms: s1ap.SecurityAlgorithms{Value: 0xc000},
		},
		SecurityKey: securityKey,
	}, s1UEStream)
}

func (c *mme) contextSetUp(n node, raw []byte) error {
	value, err := s1Value(raw)
	if err != nil {
		return err
	}

	resp, err := s1ap.ParseInitialContextSetupResponse(value)
	if err != nil {
		return err
	}

	if len(resp.ERABSetup) != 1 {
		return fmt.Errorf("INITIAL CONTEXT SETUP RESPONSE with %d E-RABs", len(resp.ERABSetup))
	}

	if err := c.m.checkDownlink(resp.ERABSetup[0].TransportLayerAddress); err != nil {
		return err
	}

	enbID, err := c.enbID(*resp.MMEUES1APID)
	if err != nil {
		return err
	}

	return n.send(&s1ap.DownlinkNASTransport{MMEUES1APID: *resp.MMEUES1APID, ENBUES1APID: enbID, NASPDU: s1NAS}, s1UEStream)
}

func (c *mme) releaseRequested(n node, p *s1ap.RawPDU) error {
	id, ok, err := p.MMEUES1APID()
	if err != nil || !ok {
		return fmt.Errorf("UE CONTEXT RELEASE REQUEST without an MME UE S1AP ID: %w", err)
	}

	enbID, err := c.enbID(id)
	if err != nil {
		return err
	}

	return n.send(&s1ap.UEContextReleaseCommand{
		UES1APIDs: s1ap.UES1APIDs{UES1APIDPair: &s1ap.UES1APIDPair{MMEUES1APID: id, ENBUES1APID: enbID}},
		Cause:     &s1ReleaseCause,
	}, s1UEStream)
}

func (c *mme) released(p *s1ap.RawPDU) error {
	id, ok, err := p.MMEUES1APID()
	if err != nil || !ok {
		return fmt.Errorf("UE CONTEXT RELEASE COMPLETE without an MME UE S1AP ID: %w", err)
	}

	if _, err := c.enbID(id); err != nil {
		return err
	}

	c.mu.Lock()
	delete(c.ues, uint32(id))
	c.mu.Unlock()

	return nil
}

type enb struct {
	radio
}

func (m *benchMOCN) startENB(tb testing.TB, n int) *enb {
	tb.Helper()

	e := &enb{radio: newRadio(m, m.dialRadio(tb, s1PPID), n)}
	m.serve(e.node, e.handle)

	broadcast := make([]s1ap.PLMNIdentity, 0, len(plmns))
	for _, p := range plmns {
		broadcast = append(broadcast, mustPLMN[s1PLMN](p))
	}

	name := fmt.Sprintf("bench-enb-%d", n)
	if err := e.send(&s1ap.S1SetupRequest{
		GlobalENBID: s1ap.GlobalENBID{
			PLMNIdentity: broadcast[0], ENBID: s1ap.ENBID{MacroENBID: s1ap.Ptr(s1ap.MacroENBID(0x1000 + n))},
		},
		ENBName:          &name,
		SupportedTAs:     s1ap.SupportedTAs{{TAC: 1, BroadcastPLMNs: broadcast}},
		DefaultPagingDRX: s1ap.Ptr(s1ap.PagingDRXv128),
	}, 0); err != nil {
		tb.Fatalf("send S1 SETUP REQUEST: %v", err)
	}

	return e
}

func (e *enb) handle(raw []byte) error {
	p, err := s1ap.ParseRawPDU(raw)
	if err != nil {
		return err
	}

	trig, proc := p.Trigger(), p.ProcedureCode()

	switch {
	case trig == s1ap.TriggeringSuccessfulOutcome && proc == s1ap.ProcS1Setup:
		e.ready()
		return nil
	case trig == s1ap.TriggeringInitiatingMessage && proc == s1ap.ProcMMEConfigurationUpdate:
		return e.send(&s1ap.MMEConfigurationUpdateAcknowledge{}, 0)
	case trig == s1ap.TriggeringInitiatingMessage && proc == s1ap.ProcPaging:
		value, err := s1Value(raw)
		if err != nil {
			return err
		}

		paging, err := s1ap.ParsePaging(value)
		if err != nil {
			return err
		}

		if paging.UEPagingID == nil || paging.UEPagingID.STMSI == nil {
			return errors.New("paging without an S-TMSI")
		}

		return e.paged(uint32(paging.UEPagingID.STMSI.MTMSI))
	case trig == s1ap.TriggeringInitiatingMessage && proc == s1ap.ProcUEContextRelease:
		ids, _, err := p.UES1APIDs()
		if err != nil {
			return err
		}

		id, ok := ids.ENBID()
		if !ok {
			return errors.New("UE CONTEXT RELEASE COMMAND without an eNB UE S1AP ID")
		}

		return e.deliver(uint64(id), raw)
	}

	if id, ok, err := p.ENBUES1APID(); err == nil && ok {
		return e.deliver(uint64(id), raw)
	}

	return fmt.Errorf("eNB: unexpected S1AP %v %v", trig, proc)
}

func (e *enb) attach(u *benchUE) error {
	id := s1ap.ENBUES1APID(u.id)
	plmn := mustPLMN[s1PLMN](plmns[u.op])

	if err := e.send(&s1ap.InitialUEMessage{
		ENBUES1APID:           id,
		NASPDU:                s1NAS,
		TAI:                   s1ap.TAI{PLMNIdentity: plmn, TAC: 1},
		EUTRANCGI:             &s1ap.EUTRANCGI{PLMNIdentity: plmn, CellID: 0x19b01},
		RRCEstablishmentCause: s1ap.Ptr(s1ap.RRCCauseMOSignalling),
	}, s1UEStream); err != nil {
		return err
	}

	raw, err := e.awaitS1(u, s1ap.ProcInitialContextSetup)
	if err != nil {
		return err
	}

	value, err := s1Value(raw)
	if err != nil {
		return err
	}

	req, err := s1ap.ParseInitialContextSetupRequest(value)
	if err != nil {
		return err
	}

	if len(req.ERABToBeSetup) != 1 {
		return fmt.Errorf("INITIAL CONTEXT SETUP REQUEST with %d E-RABs", len(req.ERABToBeSetup))
	}

	if err := e.m.checkUplink(u.op, req.ERABToBeSetup[0].TransportLayerAddress); err != nil {
		return err
	}

	u.coreID = uint64(req.MMEUES1APID)
	mmeID := req.MMEUES1APID

	if err := e.send(&s1ap.InitialContextSetupResponse{
		MMEUES1APID: &mmeID, ENBUES1APID: &id,
		ERABSetup: []s1ap.ERABSetupItemCtxtSURes{{
			ERABID: 5, TransportLayerAddress: s1RadioUP, GTPTEID: s1ap.GTPTEID(radioTEID(e.index, u.id)),
		}},
	}, s1UEStream); err != nil {
		return err
	}

	_, err = e.awaitS1(u, s1ap.ProcDownlinkNASTransport)

	return err
}

func (e *enb) release(u *benchUE) error {
	id, mmeID := s1ap.ENBUES1APID(u.id), s1ap.MMEUES1APID(u.coreID)

	if err := e.send(&s1ap.UEContextReleaseRequest{MMEUES1APID: mmeID, ENBUES1APID: id, Cause: &s1ReleaseCause}, s1UEStream); err != nil {
		return err
	}

	if _, err := e.awaitS1(u, s1ap.ProcUEContextRelease); err != nil {
		return err
	}

	return e.send(&s1ap.UEContextReleaseComplete{MMEUES1APID: &mmeID, ENBUES1APID: &id}, s1UEStream)
}

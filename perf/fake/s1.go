package fake

import (
	"fmt"
	"net/netip"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/plmnid"
	"github.com/ellanetworks/mocn/internal/s1"
	"github.com/ellanetworks/mocn/internal/tnl"
)

var s1NAS = s1ap.NASPDU{0x07, 0x41, 0x71, 0x08, 0x09, 0x10, 0x10, 0x00, 0x00, 0x00, 0x00, 0x01}

type s1Radio struct {
	plmn s1ap.PLMNIdentity
}

func newS1Radio(p db.PLMN) (*s1Radio, error) {
	id, err := plmnid.Encode[s1ap.PLMNIdentity](p)
	if err != nil {
		return nil, err
	}

	return &s1Radio{plmn: id}, nil
}

func (r *s1Radio) port() int    { return 36412 }
func (r *s1Radio) ppid() uint32 { return s1.PPID }

func (r *s1Radio) setup(radio int) ([]byte, error) {
	name := fmt.Sprintf("perf-enb-%d", radio)
	drx := s1ap.PagingDRXv128

	return (&s1ap.S1SetupRequest{
		GlobalENBID: s1ap.GlobalENBID{
			PLMNIdentity: r.plmn, ENBID: s1ap.ENBID{MacroENBID: new(s1ap.MacroENBID(radio + 1))},
		},
		ENBName:          &name,
		SupportedTAs:     s1ap.SupportedTAs{{TAC: 1, BroadcastPLMNs: []s1ap.PLMNIdentity{r.plmn}}},
		DefaultPagingDRX: &drx,
	}).Marshal()
}

func (r *s1Radio) setupResult(raw []byte) (bool, error) {
	pdu, err := s1ap.Unmarshal(raw)
	if err != nil {
		return false, err
	}

	switch m := pdu.(type) {
	case *s1ap.SuccessfulOutcome:
		return m.ProcedureCode == s1ap.ProcS1Setup, nil
	case *s1ap.UnsuccessfulOutcome:
		if m.ProcedureCode == s1ap.ProcS1Setup {
			return false, errSetupRejected
		}
	}

	return false, nil
}

func (r *s1Radio) initialUE(radio int, ue uint32) ([]byte, error) {
	return (&s1ap.InitialUEMessage{
		ENBUES1APID:           s1ap.ENBUES1APID(ue),
		NASPDU:                s1NAS,
		TAI:                   s1ap.TAI{PLMNIdentity: r.plmn, TAC: 1},
		EUTRANCGI:             &s1ap.EUTRANCGI{PLMNIdentity: r.plmn, CellID: s1ap.CellIdentity(radio+1) << 8},
		RRCEstablishmentCause: new(s1ap.RRCCauseMOData),
	}).Marshal()
}

func (r *s1Radio) handle(raw []byte, local netip.Addr, teid uint32) (reply []byte, s Session, ue uint32, ok bool,
	err error,
) {
	pdu, err := s1ap.Unmarshal(raw)
	if err != nil {
		return nil, Session{}, 0, false, err
	}

	m, isInit := pdu.(*s1ap.InitiatingMessage)
	if !isInit || m.ProcedureCode != s1ap.ProcInitialContextSetup {
		return nil, Session{}, 0, false, nil
	}

	req, err := s1ap.ParseInitialContextSetupRequest(m.Value)
	if err != nil {
		return nil, Session{}, 0, false, err
	}

	if len(req.ERABToBeSetup) == 0 {
		return nil, Session{}, 0, false, errNoBearer
	}

	erab := req.ERABToBeSetup[0]

	peer, err := address(tnl.Octets(erab.TransportLayerAddress))
	if err != nil {
		return nil, Session{}, 0, false, err
	}

	tla, err := tnl.Address(local)
	if err != nil {
		return nil, Session{}, 0, false, err
	}

	reply, err = (&s1ap.InitialContextSetupResponse{
		MMEUES1APID: new(req.MMEUES1APID),
		ENBUES1APID: new(req.ENBUES1APID),
		ERABSetup: s1ap.ERABSetupListCtxtSURes{{
			ERABID: erab.ERABID, TransportLayerAddress: tla, GTPTEID: s1ap.GTPTEID(teid),
		}},
	}).Marshal()

	s = Session{Local: local, LocalTEID: teid, Peer: peer, PeerTEID: uint32(erab.GTPTEID)}

	return reply, s, uint32(req.ENBUES1APID), true, err
}

type s1Core struct {
	plmn s1ap.PLMNIdentity
	ues  *ueTable
}

func newS1Core(p db.PLMN, ues *ueTable) (*s1Core, error) {
	id, err := plmnid.Encode[s1ap.PLMNIdentity](p)
	if err != nil {
		return nil, err
	}

	return &s1Core{plmn: id, ues: ues}, nil
}

func (c *s1Core) port() int    { return 36412 }
func (c *s1Core) ppid() uint32 { return s1.PPID }

func (c *s1Core) handle(raw []byte) ([][]byte, error) {
	pdu, err := s1ap.Unmarshal(raw)
	if err != nil {
		return nil, err
	}

	switch m := pdu.(type) {
	case *s1ap.InitiatingMessage:
		switch m.ProcedureCode {
		case s1ap.ProcS1Setup:
			return c.setup()
		case s1ap.ProcInitialUEMessage:
			return c.initialUE(m.Value)
		}
	case *s1ap.SuccessfulOutcome:
		if m.ProcedureCode == s1ap.ProcInitialContextSetup {
			return nil, c.contextSetUp(m.Value)
		}
	}

	return nil, nil
}

func (c *s1Core) setup() ([][]byte, error) {
	name := "perf-mme"

	b, err := (&s1ap.S1SetupResponse{
		MMEName: &name,
		ServedGUMMEIs: s1ap.ServedGUMMEIs{{
			ServedPLMNs:    []s1ap.PLMNIdentity{c.plmn},
			ServedGroupIDs: []s1ap.MMEGroupID{{0, 1}},
			ServedMMECs:    []s1ap.MMECode{1},
		}},
		RelativeMMECapacity: new(uint8(255)),
	}).Marshal()

	return [][]byte{b}, err
}

func (c *s1Core) initialUE(value []byte) ([][]byte, error) {
	msg, err := s1ap.ParseInitialUEMessage(value)
	if err != nil {
		return nil, err
	}

	id, gw := c.ues.open()

	tla, err := tnl.Address(gw.Addr)
	if err != nil {
		return nil, err
	}

	nas, err := (&s1ap.DownlinkNASTransport{
		MMEUES1APID: s1ap.MMEUES1APID(id), ENBUES1APID: msg.ENBUES1APID, NASPDU: s1NAS,
	}).Marshal()
	if err != nil {
		return nil, err
	}

	var key s1ap.SecurityKey
	for i := range key {
		key[i] = byte(0xa0 + i)
	}

	ics, err := (&s1ap.InitialContextSetupRequest{
		MMEUES1APID:               s1ap.MMEUES1APID(id),
		ENBUES1APID:               msg.ENBUES1APID,
		UEAggregateMaximumBitRate: s1ap.UEAggregateMaximumBitRate{DL: 10000000000, UL: 10000000000},
		ERABToBeSetup: s1ap.ERABToBeSetupListCtxtSUReq{{
			ERABID: 5,
			QoS: s1ap.ERABLevelQoSParameters{QCI: 9, ARP: s1ap.AllocationAndRetentionPriority{
				PriorityLevel: 15, PreemptionCapability: s1ap.PreemptionShallNotTrigger,
				PreemptionVulnerability: s1ap.PreemptionNotPreemptable,
			}},
			TransportLayerAddress: tla, GTPTEID: s1ap.GTPTEID(gw.TEID), NASPDU: s1NAS,
		}},
		UESecurityCapabilities: s1ap.UESecurityCapabilities{
			EncryptionAlgorithms:          s1ap.SecurityAlgorithms{Value: 0x8000},
			IntegrityProtectionAlgorithms: s1ap.SecurityAlgorithms{Value: 0xc000},
		},
		SecurityKey: key,
	}).Marshal()

	return [][]byte{nas, ics}, err
}

func (c *s1Core) contextSetUp(value []byte) error {
	resp, err := s1ap.ParseInitialContextSetupResponse(value)
	if err != nil {
		return err
	}

	if resp.MMEUES1APID == nil || len(resp.ERABSetup) == 0 {
		return errNoBearer
	}

	erab := resp.ERABSetup[0]

	peer, err := address(tnl.Octets(erab.TransportLayerAddress))
	if err != nil {
		return err
	}

	return c.ues.established(uint64(*resp.MMEUES1APID), peer, uint32(erab.GTPTEID))
}

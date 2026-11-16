package s1

import (
	"context"
	"log/slog"
	"net/netip"
	"testing"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/gtpu"
	"github.com/ellanetworks/mocn/internal/plmnid"
	"github.com/ellanetworks/mocn/internal/sctptest"
	"github.com/ellanetworks/mocn/internal/tnl"
)

var (
	benchPLMN = db.PLMN{MCC: "001", MNC: "01"}
	benchNAS  = s1ap.NASPDU{0x07, 0x41, 0x71, 0x08, 0x09, 0x10, 0x10, 0x00, 0x00, 0x00, 0x00, 0x01}
)

func benchMarshal(b *testing.B, m interface{ Marshal() ([]byte, error) }) []byte {
	b.Helper()

	out, err := m.Marshal()
	if err != nil {
		b.Fatal(err)
	}

	return out
}

func benchTLA(b *testing.B, addr string) s1ap.TransportLayerAddress {
	b.Helper()

	a, err := tnl.Address(netip.MustParseAddr(addr))
	if err != nil {
		b.Fatal(err)
	}

	return a
}

func benchIdentity(b *testing.B) s1ap.PLMNIdentity {
	b.Helper()

	id, err := plmnid.Encode[s1ap.PLMNIdentity](benchPLMN)
	if err != nil {
		b.Fatal(err)
	}

	return id
}

func benchContextSetup(b *testing.B) ([]byte, []byte) {
	b.Helper()

	req := benchMarshal(b, &s1ap.InitialContextSetupRequest{
		MMEUES1APID: 1000, ENBUES1APID: 7,
		UEAggregateMaximumBitRate: s1ap.UEAggregateMaximumBitRate{DL: 1000000000, UL: 500000000},
		ERABToBeSetup: []s1ap.ERABToBeSetupItemCtxtSUReq{{
			ERABID: 5, QoS: s1ap.ERABLevelQoSParameters{QCI: 9, ARP: s1ap.AllocationAndRetentionPriority{
				PriorityLevel: 15, PreemptionCapability: s1ap.PreemptionShallNotTrigger,
				PreemptionVulnerability: s1ap.PreemptionNotPreemptable,
			}},
			TransportLayerAddress: benchTLA(b, "10.45.0.1"), GTPTEID: 0x12345678, NASPDU: benchNAS,
		}},
		UESecurityCapabilities: s1ap.UESecurityCapabilities{
			EncryptionAlgorithms:          s1ap.SecurityAlgorithms{Value: 0x8000},
			IntegrityProtectionAlgorithms: s1ap.SecurityAlgorithms{Value: 0xc000},
		},
	})

	resp := benchMarshal(b, &s1ap.InitialContextSetupResponse{
		MMEUES1APID: s1ap.Ptr(s1ap.MMEUES1APID(1)), ENBUES1APID: s1ap.Ptr(s1ap.ENBUES1APID(7)),
		ERABSetup: []s1ap.ERABSetupItemCtxtSURes{{ERABID: 5, TransportLayerAddress: benchTLA(b, "10.90.1.21"), GTPTEID: 0x77}},
	})

	return req, resp
}

func BenchmarkUEIDRewrite(b *testing.B) {
	id := benchIdentity(b)
	ics, _ := benchContextSetup(b)

	for _, tc := range []struct {
		name string
		msg  []byte
	}{
		{"UplinkNASTransport", benchMarshal(b, &s1ap.UplinkNASTransport{
			MMEUES1APID: 1, ENBUES1APID: 7, NASPDU: benchNAS,
			EUTRANCGI: &s1ap.EUTRANCGI{PLMNIdentity: id, CellID: 0x19b01}, TAI: &s1ap.TAI{PLMNIdentity: id, TAC: 1},
		})},
		{"DownlinkNASTransport", benchMarshal(b, &s1ap.DownlinkNASTransport{MMEUES1APID: 1000, ENBUES1APID: 7, NASPDU: benchNAS})},
		{"InitialContextSetupRequest", ics},
	} {
		b.Run(tc.name, func(b *testing.B) {
			mmeID, enbID := s1ap.MMEUES1APID(42), s1ap.ENBUES1APID(9)

			b.ReportAllocs()

			for b.Loop() {
				if _, err := s1ap.Unmarshal(tc.msg); err != nil {
					b.Fatal(err)
				}

				p, err := s1ap.ParseRawPDU(tc.msg)
				if err != nil {
					b.Fatal(err)
				}

				ids, _, err := readUEIDs(p)
				if err != nil || ids.mme == nil {
					b.Fatalf("UE S1AP IDs %+v: %v", ids, err)
				}

				if _, err := rewrite(p, &mmeID, &enbID); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkTNLRewrite(b *testing.B) {
	plane := gtpu.New(gtpu.Config{
		RANAddress: netip.MustParseAddr("10.1.0.1"), RANInterface: "lo", Host: sctptest.LoopbackHost{},
		Logger: slog.New(slog.DiscardHandler),
	})

	if err := plane.SetOperators([]gtpu.Operator{{ID: 1, Name: "A", Aggregated: true}}); err != nil {
		b.Fatal(err)
	}

	mme := netip.MustParseAddr("127.0.0.1")

	route, iface, err := gtpu.Resolve(sctptest.LoopbackHost{}, mme)
	if err != nil {
		b.Fatal(err)
	}

	plane.Anchor(1, "mme", gtpu.Anchor{Peer: mme, Interface: iface, Addresses: []netip.Addr{route.Src}})

	r := &relay{plane: plane, ues: newUETable()}
	u := &ueContext{RAN: &ranAssoc{}, Core: &mmeAssoc{}, OperatorID: 1}
	req, resp := benchContextSetup(b)

	b.ReportAllocs()

	for b.Loop() {
		p, err := s1ap.ParseRawPDU(req)
		if err != nil {
			b.Fatal(err)
		}

		if err := r.tnlDownlinkLocked(u, p, nil); err != nil {
			b.Fatal(err)
		}

		if _, err := p.Marshal(); err != nil {
			b.Fatal(err)
		}

		if p, err = s1ap.ParseRawPDU(resp); err != nil {
			b.Fatal(err)
		}

		if err := r.tnlUplinkLocked(u, p); err != nil {
			b.Fatal(err)
		}

		if _, err := p.Marshal(); err != nil {
			b.Fatal(err)
		}

		if n := u.Ext.tunnels.Len(); n != 2 {
			b.Fatalf("%d tunnels for the E-RAB, want 2", n)
		}

		plane.ReleaseAll(&u.Ext.tunnels)
	}
}

func BenchmarkInitialUERouting(b *testing.B) {
	ctx, log := context.Background(), slog.New(slog.DiscardHandler)
	id := benchIdentity(b)
	msg := benchMarshal(b, &s1ap.InitialUEMessage{
		ENBUES1APID:           7,
		NASPDU:                benchNAS,
		TAI:                   s1ap.TAI{PLMNIdentity: id, TAC: 1},
		EUTRANCGI:             &s1ap.EUTRANCGI{PLMNIdentity: id, CellID: 0x19b01},
		RRCEstablishmentCause: s1ap.Ptr(s1ap.RRCCauseMOSignalling),
	})

	served := []ServedGUMMEIs{{PLMNs: []db.PLMN{benchPLMN}, GroupIDs: []uint16{1}, MMECodes: []uint8{1}}}
	cands := []mmeCandidate{
		{assoc: &mmeAssoc{}, served: served, capacity: 255},
		{assoc: &mmeAssoc{}, served: served, capacity: 255},
	}
	intN := func(n int) int { return n / 2 }
	r := &relay{ues: newUETable()}
	n := &ranAssoc{}

	b.ReportAllocs()

	for b.Loop() {
		pdu, err := s1ap.Unmarshal(msg)
		if err != nil {
			b.Fatal(err)
		}

		p, err := s1ap.ParseRawPDU(msg)
		if err != nil {
			b.Fatal(err)
		}

		enbID, ok, err := p.ENBUES1APID()
		if err != nil || !ok {
			b.Fatalf("eNB UE S1AP ID: %v", err)
		}

		tai, err := assoc.GetIE[s1ap.TAI](p.Get, s1ap.IDTAI)
		if err != nil || tai == nil {
			b.Fatalf("TAI: %v", err)
		}

		if _, err := s1ap.ParseInitialUEMessage(pdu.(*s1ap.InitiatingMessage).Value); err != nil {
			b.Fatal(err)
		}

		plmn, err := plmnid.Decode(tai.PLMNIdentity)
		if err != nil {
			b.Fatal(err)
		}

		mme, _ := selectMME(selectable(cands), identityOf(ctx, log, p, plmn), intN)
		if mme == nil {
			b.Fatal("no MME selected")
		}

		u := &ueContext{
			RAN: n, RANID: enbID, RANIDKnown: true, Core: mme, OperatorID: 1, PLMN: plmn,
			Ext: ueInfo{tai: &TAI{PLMN: plmn, TAC: uint16(tai.TAC)}},
		}

		r.updateLocation(u, p)

		if err := r.ues.Add(u); err != nil {
			b.Fatal(err)
		}

		if _, err := rewrite(p, nil, &u.CoreRANID); err != nil {
			b.Fatal(err)
		}

		r.ues.Remove(u)
	}
}

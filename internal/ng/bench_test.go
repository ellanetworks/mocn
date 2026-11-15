package ng

import (
	"context"
	"log/slog"
	"net/netip"
	"testing"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/core/per/ap"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/gtpu"
	"github.com/ellanetworks/mocn/internal/plmnid"
	"github.com/ellanetworks/mocn/internal/sctptest"
)

var (
	benchPLMN  = db.PLMN{MCC: "001", MNC: "01"}
	benchNAS   = ngap.NASPDU{0x7e, 0x00, 0x41, 0x79, 0x00, 0x0d, 0x01, 0x00, 0xf1, 0x10, 0xca, 0xfe, 0xba, 0xbe}
	benchSlice = ngap.SNSSAI{SST: 1, SD: &ngap.SD{0x01, 0x02, 0x03}}
)

func benchMarshal[T ~[]byte](b *testing.B, m interface{ Marshal() (T, error) }) T {
	b.Helper()

	out, err := m.Marshal()
	if err != nil {
		b.Fatal(err)
	}

	return out
}

func benchTunnel(addr string, teid ngap.GTPTEID) ngap.UPTransportLayerInformation {
	return ngap.UPTransportLayerInformation{GTPTunnel: &ngap.GTPTunnel{
		TransportLayerAddress: ap.TransportLayerAddressFromOctets(netip.MustParseAddr(addr).AsSlice()), GTPTEID: teid,
	}}
}

func benchULI(b *testing.B) ngap.UserLocationInformation {
	b.Helper()

	id, err := plmnid.Encode[ngap.PLMNIdentity](benchPLMN)
	if err != nil {
		b.Fatal(err)
	}

	return ngap.UserLocationInformation{NR: &ngap.UserLocationInformationNR{
		NRCGI: ngap.NRCGI{PLMNIdentity: id, NRCellIdentity: 0x19b01}, TAI: ngap.TAI{PLMNIdentity: id, TAC: 1},
	}}
}

func benchSessionSetup(b *testing.B) ([]byte, []byte) {
	b.Helper()

	reqTransfer := benchMarshal(b, &ngap.PDUSessionResourceSetupRequestTransfer{
		ULNGUUPTNLInformation: benchTunnel("10.45.0.1", 0x100),
		PDUSessionType:        ngap.PDUSessionTypeIPv4,
		QosFlowSetupRequest: ngap.QosFlowSetupRequestList{{
			QosFlowIdentifier: 1,
			QosFlowLevelQosParameters: ngap.QosFlowLevelQosParameters{
				QosCharacteristics:             ngap.QosCharacteristics{NonDynamic5QI: &ngap.NonDynamic5QIDescriptor{FiveQI: 9}},
				AllocationAndRetentionPriority: ngap.AllocationAndRetentionPriority{PriorityLevelARP: 1},
			},
		}},
	})

	respTransfer := benchMarshal(b, &ngap.PDUSessionResourceSetupResponseTransfer{
		DLQosFlowPerTNLInformation: ngap.QosFlowPerTNLInformation{
			UPTransportLayerInformation: benchTunnel("10.90.1.23", 0x200),
			AssociatedQosFlowList:       ngap.AssociatedQosFlowList{{QosFlowIdentifier: 1}},
		},
	})

	req := benchMarshal(b, &ngap.PDUSessionResourceSetupRequest{
		AMFUENGAPID: 1000, RANUENGAPID: 7,
		PDUSessionResourceSetup: ngap.PDUSessionResourceSetupListSUReq{{
			PDUSessionID: 1, SNSSAI: benchSlice, Transfer: reqTransfer,
		}},
	})

	resp := benchMarshal(b, &ngap.PDUSessionResourceSetupResponse{
		AMFUENGAPID: ngap.Ptr(ngap.AMFUENGAPID(1)), RANUENGAPID: ngap.Ptr(ngap.RANUENGAPID(7)),
		PDUSessionResourceSetup: ngap.PDUSessionResourceSetupListSURes{{PDUSessionID: 1, Transfer: respTransfer}},
	})

	return req, resp
}

func BenchmarkUEIDRewrite(b *testing.B) {
	uli := benchULI(b)
	setup, _ := benchSessionSetup(b)

	for _, tc := range []struct {
		name string
		msg  []byte
	}{
		{"UplinkNASTransport", benchMarshal(b, &ngap.UplinkNASTransport{
			AMFUENGAPID: 1, RANUENGAPID: 7, NASPDU: benchNAS, UserLocationInformation: &uli,
		})},
		{"DownlinkNASTransport", benchMarshal(b, &ngap.DownlinkNASTransport{AMFUENGAPID: 1000, RANUENGAPID: 7, NASPDU: benchNAS})},
		{"PDUSessionResourceSetupRequest", setup},
	} {
		b.Run(tc.name, func(b *testing.B) {
			amfID, ranID := ngap.AMFUENGAPID(42), ngap.RANUENGAPID(9)

			b.ReportAllocs()

			for b.Loop() {
				if _, err := ngap.Unmarshal(tc.msg); err != nil {
					b.Fatal(err)
				}

				p, err := ngap.ParseRawPDU(tc.msg)
				if err != nil {
					b.Fatal(err)
				}

				ids, _, err := readUEIDs(p)
				if err != nil || ids.amf == nil {
					b.Fatalf("UE NGAP IDs %+v: %v", ids, err)
				}

				if _, err := rewrite(p, &amfID, &ranID); err != nil {
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

	amf := netip.MustParseAddr("127.0.0.1")

	route, iface, err := gtpu.Resolve(sctptest.LoopbackHost{}, amf)
	if err != nil {
		b.Fatal(err)
	}

	plane.Anchor(1, "amf", gtpu.Anchor{Peer: amf, Interface: iface, Addresses: []netip.Addr{route.Src}})

	r := &relay{plane: plane, ues: newUETable()}
	u := &ueContext{RAN: &ranAssoc{}, Core: &amfAssoc{}, OperatorID: 1}
	req, resp := benchSessionSetup(b)

	b.ReportAllocs()

	for b.Loop() {
		p, err := ngap.ParseRawPDU(req)
		if err != nil {
			b.Fatal(err)
		}

		if err := r.tnlDownlinkLocked(u, p, nil); err != nil {
			b.Fatal(err)
		}

		if _, err := p.Marshal(); err != nil {
			b.Fatal(err)
		}

		if p, err = ngap.ParseRawPDU(resp); err != nil {
			b.Fatal(err)
		}

		if err := r.tnlUplinkLocked(u, p); err != nil {
			b.Fatal(err)
		}

		if _, err := p.Marshal(); err != nil {
			b.Fatal(err)
		}

		if n := u.Ext.tunnels.Len(); n != 2 {
			b.Fatalf("%d tunnels for the PDU session, want 2", n)
		}

		plane.ReleaseAll(&u.Ext.tunnels)
	}
}

func BenchmarkInitialUERouting(b *testing.B) {
	ctx, log := context.Background(), slog.New(slog.DiscardHandler)
	msg := benchMarshal(b, &ngap.InitialUEMessage{
		RANUENGAPID: 7, NASPDU: benchNAS, UserLocationInformation: benchULI(b),
		RRCEstablishmentCause: ngap.Ptr(ngap.RRCEstablishmentCause(3)),
	})

	support := []PLMNSupport{{PLMN: benchPLMN, Slices: []SNSSAI{{SST: 1, SD: "010203"}}}}
	cands := []amfCandidate{
		{assoc: &amfAssoc{}, served: []GUAMI{{PLMN: benchPLMN, AMFRegionID: 2, AMFSetID: 1}}, support: support, capacity: 255},
		{assoc: &amfAssoc{}, served: []GUAMI{{PLMN: benchPLMN, AMFRegionID: 2, AMFSetID: 1, AMFPointer: 1}}, support: support, capacity: 255},
	}
	intN := func(n int) int { return n / 2 }
	r := &relay{ues: newUETable()}
	n := &ranAssoc{}

	b.ReportAllocs()

	for b.Loop() {
		pdu, err := ngap.Unmarshal(msg)
		if err != nil {
			b.Fatal(err)
		}

		p, err := ngap.ParseRawPDU(msg)
		if err != nil {
			b.Fatal(err)
		}

		ranID, ok, err := p.RANUENGAPID()
		if err != nil || !ok {
			b.Fatalf("RAN UE NGAP ID: %v", err)
		}

		uli, err := assoc.GetIE[ngap.UserLocationInformation](p.Get, ngap.IDUserLocationInformation)
		if err != nil || uli == nil || uli.NR == nil {
			b.Fatalf("User Location Information: %v", err)
		}

		if _, err := ngap.ParseInitialUEMessage(pdu.(*ngap.InitiatingMessage).Value); err != nil {
			b.Fatal(err)
		}

		plmn, err := plmnid.Decode(uli.NR.TAI.PLMNIdentity)
		if err != nil {
			b.Fatal(err)
		}

		amf, _ := selectAMF(selectable(cands), identityOf(ctx, log, p, plmn), intN)
		if amf == nil {
			b.Fatal("no AMF selected")
		}

		u := &ueContext{RAN: n, RANID: ranID, RANIDKnown: true, Core: amf, OperatorID: 1, PLMN: plmn}

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

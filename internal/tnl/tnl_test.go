package tnl_test

import (
	"bytes"
	"errors"
	"net/netip"
	"testing"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/core/per"
	"github.com/ellanetworks/core/per/ap"
	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/tnl"
)

const unknownID = 0x0ffe

func address(t *testing.T, s string) ngap.TransportLayerAddress {
	t.Helper()

	a, err := tnl.Address(netip.MustParseAddr(s))
	if err != nil {
		t.Fatal(err)
	}

	return a
}

func tunnel(t *testing.T) ngap.UPTransportLayerInformation {
	t.Helper()

	return ngap.UPTransportLayerInformation{GTPTunnel: &ngap.GTPTunnel{TransportLayerAddress: address(t, "10.0.0.1"), GTPTEID: 1}}
}

func renamed(t *testing.T, b []byte, id ngap.ProtocolIEID, crit ngap.Criticality) []byte {
	t.Helper()

	from := []byte{byte(id >> 8), byte(id), byte(ngap.CriticalityIgnore) << 6}
	if bytes.Count(b, from) != 1 {
		t.Fatalf("IE %d is not encoded exactly once in %x", id, b)
	}

	return bytes.Replace(b, from, []byte{unknownID >> 8, unknownID & 0xff, byte(crit) << 6}, 1)
}

func requestTransfer(t *testing.T) *ngap.PDUSessionResourceSetupRequestTransfer {
	t.Helper()

	return &ngap.PDUSessionResourceSetupRequestTransfer{
		ULNGUUPTNLInformation:          tunnel(t),
		RedundantULNGUUPTNLInformation: new(tunnel(t)),
		PDUSessionType:                 ngap.PDUSessionTypeIPv4,
		QosFlowSetupRequest: ngap.QosFlowSetupRequestList{{
			QosFlowIdentifier: 1,
			QosFlowLevelQosParameters: ngap.QosFlowLevelQosParameters{
				QosCharacteristics:             ngap.QosCharacteristics{NonDynamic5QI: &ngap.NonDynamic5QIDescriptor{FiveQI: 9}},
				AllocationAndRetentionPriority: ngap.AllocationAndRetentionPriority{PriorityLevelARP: 1},
			},
			RedundantQosFlowIndicator: new(ngap.RedundantQosFlowIndicatorTrue),
		}},
	}
}

func responseTransfer(t *testing.T) *ngap.PDUSessionResourceSetupResponseTransfer {
	t.Helper()

	return &ngap.PDUSessionResourceSetupResponseTransfer{
		DLQosFlowPerTNLInformation: ngap.QosFlowPerTNLInformation{
			UPTransportLayerInformation: tunnel(t),
			AssociatedQosFlowList: ngap.AssociatedQosFlowList{{
				QosFlowIdentifier: 1, CurrentQoSParaSetIndex: new(ngap.AlternativeQoSParaSetIndex(1)),
			}},
		},
		RedundantDLQosFlowPerTNLInformation: &ngap.QosFlowPerTNLInformation{
			UPTransportLayerInformation: tunnel(t),
			AssociatedQosFlowList:       ngap.AssociatedQosFlowList{{QosFlowIdentifier: 1}},
		},
	}
}

func encoded(t *testing.T, m interface {
	Marshal() (ngap.TransferContainer, error)
},
) []byte {
	t.Helper()

	b, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	return b
}

func decoded[T any](t *testing.T, decode func(ngap.TransferContainer) (*T, error), b []byte) *T {
	t.Helper()

	v, err := decode(b)
	if err != nil {
		t.Fatal(err)
	}

	return v
}

func TestLosslessNGAPTransfers(t *testing.T) {
	t.Parallel()

	request := encoded(t, requestTransfer(t))
	response := encoded(t, responseTransfer(t))

	for _, c := range []struct {
		name    string
		v       any
		unknown bool
	}{
		{name: "request transfer with known extensions", v: decoded(t, ngap.DecodePDUSessionResourceSetupRequestTransfer, request)},
		{name: "response transfer with known extensions", v: decoded(t, ngap.DecodePDUSessionResourceSetupResponseTransfer, response)},
		{
			name: "unknown ignore extension of a QoS flow in a request transfer",
			v: decoded(t, ngap.DecodePDUSessionResourceSetupRequestTransfer,
				renamed(t, request, ngap.IDRedundantQosFlowIndicator, ngap.CriticalityIgnore)),
		},
		{
			name: "unknown ignore extension of an associated QoS flow in a response transfer",
			v: decoded(t, ngap.DecodePDUSessionResourceSetupResponseTransfer,
				renamed(t, response, ngap.IDCurrentQoSParaSetIndex, ngap.CriticalityIgnore)),
		},
		{
			name: "unknown reject extension of an associated QoS flow in a response transfer",
			v: decoded(t, ngap.DecodePDUSessionResourceSetupResponseTransfer,
				renamed(t, response, ngap.IDCurrentQoSParaSetIndex, ngap.CriticalityReject)),
		},
		{
			name: "unknown ignore IE of a request transfer",
			v: decoded(t, ngap.DecodePDUSessionResourceSetupRequestTransfer,
				renamed(t, request, ngap.IDRedundantULNGUUPTNLInformation, ngap.CriticalityIgnore)),
			unknown: true,
		},
		{
			name: "unknown ignore extension of a response transfer",
			v: decoded(t, ngap.DecodePDUSessionResourceSetupResponseTransfer,
				renamed(t, response, ngap.IDRedundantDLQosFlowPerTNLInformation, ngap.CriticalityIgnore)),
			unknown: true,
		},
		{
			name: "unknown UP transport layer information alternative",
			v: &ngap.PDUSessionResourceSetupResponseTransfer{
				DLQosFlowPerTNLInformation: ngap.QosFlowPerTNLInformation{
					UPTransportLayerInformation: ngap.UPTransportLayerInformation{
						ChoiceExtension: &ngap.SingleContainer{ID: unknownID, Criticality: ngap.CriticalityIgnore, Value: "\x00"},
					},
					AssociatedQosFlowList: ngap.AssociatedQosFlowList{{QosFlowIdentifier: 1}},
				},
			},
			unknown: true,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			err := tnl.Lossless(c.v)
			if c.unknown != errors.Is(err, tnl.ErrUnknownExtension) || (!c.unknown && err != nil) {
				t.Fatalf("Lossless = %v, want unknown extension %t", err, c.unknown)
			}
		})
	}
}

func TestLosslessS1APItems(t *testing.T) {
	t.Parallel()

	a, err := tnl.Address(netip.MustParseAddr("10.0.0.1"))
	if err != nil {
		t.Fatal(err)
	}

	switched := &s1ap.ERABToBeSwitchedDLItem{
		ERABID: 5, TransportLayerAddress: a, GTPTEID: 1,
		SecurityIndication: &s1ap.SecurityIndication{IntegrityProtectionIndication: s1ap.IntegrityProtectionRequired},
	}

	b, err := per.Marshal(switched, per.Aligned)
	if err != nil {
		t.Fatal(err)
	}

	var unknown s1ap.ERABSetupItemCtxtSURes
	if err := per.Unmarshal(b, &unknown, per.Aligned); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name string
		v    any
		want error
		fail bool
	}{
		{name: "item without extensions", v: &s1ap.ERABSetupItemCtxtSURes{ERABID: 5, TransportLayerAddress: a, GTPTEID: 1}},
		{name: "item with a typed extension", v: switched},
		{name: "item with an unknown extension", v: &unknown, want: tnl.ErrUnknownExtension},
		{name: "not a pointer", v: s1ap.ERABToBeModifiedListBearerModInd{}, fail: true},
		{name: "pointer to a non-struct", v: &s1ap.ERABToBeModifiedListBearerModInd{}, fail: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			err := tnl.Lossless(c.v)

			switch {
			case c.fail:
				if err == nil || errors.Is(err, tnl.ErrUnknownExtension) {
					t.Fatalf("Lossless = %v, want an argument error", err)
				}
			case c.want != nil:
				if !errors.Is(err, c.want) {
					t.Fatalf("Lossless = %v, want %v", err, c.want)
				}
			case err != nil:
				t.Fatalf("Lossless = %v, want nil", err)
			}
		})
	}
}

func TestAddressRoundTrip(t *testing.T) {
	t.Parallel()

	for _, s := range []string{"10.0.0.1", "2001:db8::1", "::ffff:10.0.0.2"} {
		a := netip.MustParseAddr(s)

		got, err := tnl.Address(a)
		if err != nil {
			t.Fatal(err)
		}

		if back, ok := netip.AddrFromSlice(tnl.Octets(got)); !ok || back != a.Unmap() {
			t.Fatalf("%s round trip = %v", s, back)
		}
	}

	odd, ok := ap.TransportLayerAddressFromBits([]byte{10, 0, 0, 0}, 31)
	if !ok || tnl.Octets(odd) != nil {
		t.Fatalf("31-bit address octets = %x, want none", tnl.Octets(odd))
	}
}

func TestUnknownNestedExtensionsArePreserved(t *testing.T) {
	t.Parallel()

	request := renamed(t, encoded(t, requestTransfer(t)), ngap.IDRedundantQosFlowIndicator, ngap.CriticalityIgnore)
	req := decoded(t, ngap.DecodePDUSessionResourceSetupRequestTransfer, request)

	if req.QosFlowSetupRequest[0].RedundantQosFlowIndicator != nil || !bytes.Equal(encoded(t, req), request) {
		t.Fatal("request transfer did not keep the unknown QoS flow extension as received")
	}

	response := renamed(t, encoded(t, responseTransfer(t)), ngap.IDCurrentQoSParaSetIndex, ngap.CriticalityIgnore)
	res := decoded(t, ngap.DecodePDUSessionResourceSetupResponseTransfer, response)

	if res.DLQosFlowPerTNLInformation.AssociatedQosFlowList[0].CurrentQoSParaSetIndex != nil || !bytes.Equal(encoded(t, res), response) {
		t.Fatal("response transfer did not keep the unknown associated QoS flow extension as received")
	}
}

package gtpu

import (
	"bytes"
	"net/netip"
	"testing"
)

func TestErrorIndicationRoundTrip(t *testing.T) {
	peer := netip.MustParseAddr("192.0.2.9")
	b := errorIndication(0xdeadbeef, peer)

	h, err := parseHeader(b)
	if err != nil || h.msg != msgErrorIndication || h.teid != 0 || h.flags&flagS == 0 {
		t.Fatalf("header = %+v, %v", h, err)
	}

	teid, got, err := parseErrorIndication(b, h)
	if err != nil || teid != 0xdeadbeef || got != peer {
		t.Fatalf("parse = %#x %v %v", teid, got, err)
	}
}

func TestEchoResponseCopiesSequence(t *testing.T) {
	b := echoResponse(0x1234)

	h, err := parseHeader(b)
	if err != nil || h.msg != msgEchoResponse || h.seq != 0x1234 || !bytes.Equal(b[h.body:], []byte{ieRecovery, 0}) {
		t.Fatalf("echo response = %x, %+v, %v", b, h, err)
	}
}

func TestExtensionHeaderRules(t *testing.T) {
	pdu := []byte{0x34, msgGPDU, 0, 16, 0, 0, 0, 1, 0, 0, 0, 0x85, 1, 0x10, 0x05, 0x40, 1, 0x12, 0x34, 0x00, 1, 2, 3, 4}

	h, err := parseHeader(pdu)
	if err != nil {
		t.Fatal(err)
	}

	exts, payload, err := extensions(pdu, h)
	if err != nil || len(exts) != 2 || exts[0].typ != 0x85 || exts[1].typ != 0x40 {
		t.Fatalf("extensions = %+v, %v", exts, err)
	}

	unknown, strip := relayable(exts)
	if unknown != nil || !strip {
		t.Fatalf("relayable = %v %v", unknown, strip)
	}

	out := withoutDiscarded(pdu, h, exts, payload)

	oh, err := parseHeader(out)
	if err != nil {
		t.Fatal(err)
	}

	oe, opay, err := extensions(out, oh)
	if err != nil || len(oe) != 1 || oe[0].typ != 0x85 || !bytes.Equal(out[opay:], []byte{1, 2, 3, 4}) {
		t.Fatalf("stripped = %x", out)
	}

	if unknown, _ := relayable([]extension{{typ: 0xc0}, {typ: 0xc5}, {typ: 0x81}}); !bytes.Equal(unknown, []byte{0xc5}) {
		t.Fatalf("unknown = %x, want only 0xc5", unknown)
	}
}

func TestMalformed(t *testing.T) {
	for _, b := range [][]byte{nil, {0x30}, {0x50, 255, 0, 0, 0, 0, 0, 1}, {0x32, 255, 0, 2, 0, 0, 0, 1, 0, 0}} {
		if _, err := parseHeader(b); err == nil {
			t.Errorf("parseHeader(%x) accepted", b)
		}
	}
}

package gtpu

import (
	"bytes"
	"testing"
)

func FuzzGTPU(f *testing.F) {
	f.Add(sequencedGPDU(1))
	f.Add(plainGPDU(2))
	f.Add(errorIndication(3, ranGW))
	f.Add([]byte{0x34, msgGPDU, 0, 12, 0, 0, 0, 1, 0, 0, 0, 0x40, 1, 0x12, 0x34, 0, 1, 2, 3, 4})
	f.Add([]byte{
		0x34, msgErrorIndication, 0, 16, 0, 0, 0, 0, 0, 0, 0, 0x40, 1, 0x08, 0x68, 0,
		ieTEIDDataI, 0, 0, 0, 1, ieGTPUPeerAddress, 0, 4, 10, 0, 0, 1,
	})

	f.Fuzz(func(t *testing.T, b []byte) {
		h, err := parseHeader(b)
		if err != nil {
			return
		}

		_, _, _ = parseErrorIndication(b, h)

		exts, payload, err := extensions(b, h)
		if err != nil {
			return
		}

		unknown, strip := relayable(exts)
		if len(unknown) > 0 || !strip {
			return
		}

		out := withoutDiscarded(b, h, exts, payload)

		oh, err := parseHeader(out)
		if err != nil {
			t.Fatalf("stripped %x into an invalid header %x: %v", b, out, err)
		}

		oe, op, err := extensions(out, oh)
		if err != nil {
			t.Fatalf("stripped %x into invalid extensions %x: %v", b, out, err)
		}

		if _, strip := relayable(oe); strip {
			t.Fatalf("discardable extension left in %x", out)
		}

		if !bytes.Equal(out[op:oh.length], b[payload:h.length]) || oh.teid != h.teid || oh.msg != h.msg {
			t.Fatalf("stripping %x changed the message: %x", b, out)
		}

		if h.flags&(flagS|flagPN) != 0 && oh.seq != h.seq {
			t.Fatalf("stripping %x lost the sequence number: %x", b, out)
		}
	})
}

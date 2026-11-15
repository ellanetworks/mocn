package gtpu

import (
	"encoding/binary"
	"errors"
	"net/netip"
)

const (
	Port = 2152

	msgEchoRequest                    = 1
	msgEchoResponse                   = 2
	msgErrorIndication                = 26
	msgSupportedExtensionHeadersNotif = 31
	msgTunnelStatus                   = 253
	msgEndMarker                      = 254
	msgGPDU                           = 255

	flagE  = 0x04
	flagS  = 0x02
	flagPN = 0x01

	ieRecovery                = 14
	ieTEIDDataI               = 16
	ieGTPUPeerAddress         = 133
	ieExtensionHeaderTypeList = 141

	extNone              = 0x00
	extPDCPPDUNumber     = 0xc0
	extComprehensionMask = 0xc0
	extDiscardContent    = 0x40
	extRequiredByNode    = 0xc0
)

var errMalformed = errors.New("malformed GTP-U message")

// TS 29.281 clause 5.1
type header struct {
	flags  byte
	msg    byte
	teid   uint32
	seq    uint16
	body   int
	length int
}

// TS 29.281 clause 5.1
func parseHeader(b []byte) (header, error) {
	if len(b) < 8 || b[0]>>5 != 1 || b[0]&0x10 == 0 {
		return header{}, errMalformed
	}

	h := header{flags: b[0], msg: b[1], teid: binary.BigEndian.Uint32(b[4:8]), body: 8}

	h.length = 8 + int(binary.BigEndian.Uint16(b[2:4]))
	if h.length > len(b) {
		return header{}, errMalformed
	}

	if h.flags&(flagE|flagS|flagPN) != 0 {
		if h.length < 12 {
			return header{}, errMalformed
		}

		h.seq = binary.BigEndian.Uint16(b[8:10])
		h.body = 12
	}

	return h, nil
}

type extension struct {
	typ   byte
	start int
	end   int
}

// TS 29.281 clause 5.2.1
func extensions(b []byte, h header) ([]extension, int, error) {
	if h.flags&flagE == 0 {
		return nil, h.body, nil
	}

	var out []extension

	next, off := b[11], 12

	for next != extNone {
		if off >= h.length {
			return nil, 0, errMalformed
		}

		n := int(b[off]) * 4
		if n == 0 || off+n > h.length {
			return nil, 0, errMalformed
		}

		out = append(out, extension{typ: next, start: off, end: off + n})
		next = b[off+n-1]
		off += n
	}

	return out, off, nil
}

// TS 29.281 clause 5.2.1
func relayable(exts []extension) (unknown []byte, strip bool) {
	for _, e := range exts {
		switch e.typ & extComprehensionMask {
		case extDiscardContent:
			strip = true
		case extRequiredByNode:
			if e.typ != extPDCPPDUNumber {
				unknown = append(unknown, e.typ)
			}
		}
	}

	return unknown, strip
}

// TS 29.281 clause 5.1
// TS 29.281 clause 5.2.1
func withoutDiscarded(b []byte, h header, exts []extension, payload int) []byte {
	var kept []extension

	for _, e := range exts {
		if e.typ&extComprehensionMask != extDiscardContent {
			kept = append(kept, e)
		}
	}

	out := make([]byte, 0, len(b))

	switch {
	case len(kept) > 0:
		out = append(out, b[:12]...)
		out[11] = kept[0].typ
	case h.flags&(flagS|flagPN) != 0:
		out = append(out, b[:12]...)
		out[0] &^= flagE
		out[11] = extNone
	default:
		out = append(out, b[:8]...)
		out[0] &^= flagE
	}

	for i, e := range kept {
		ext := append([]byte(nil), b[e.start:e.end]...)
		ext[len(ext)-1] = extNone

		if i+1 < len(kept) {
			ext[len(ext)-1] = kept[i+1].typ
		}

		out = append(out, ext...)
	}

	out = append(out, b[payload:h.length]...)
	binary.BigEndian.PutUint16(out[2:4], uint16(len(out)-8))

	return out
}

func signalling(msg byte, seq uint16, ies []byte) []byte {
	b := make([]byte, 12, 12+len(ies))
	b[0] = 0x30 | flagS
	b[1] = msg
	binary.BigEndian.PutUint16(b[2:4], uint16(4+len(ies)))
	binary.BigEndian.PutUint16(b[8:10], seq)

	return append(b, ies...)
}

// TS 29.281 clause 7.2.2
func echoResponse(seq uint16) []byte {
	return signalling(msgEchoResponse, seq, []byte{ieRecovery, 0})
}

// TS 29.281 clause 7.2.1
func echoRequest(seq uint16) []byte {
	return signalling(msgEchoRequest, seq, nil)
}

// TS 29.281 clause 7.3.1
func errorIndication(teid uint32, peer netip.Addr) []byte {
	ies := []byte{ieTEIDDataI, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(ies[1:], teid)

	a := peer.Unmap().AsSlice()
	ies = append(ies, ieGTPUPeerAddress, 0, byte(len(a)))
	ies = append(ies, a...)

	return signalling(msgErrorIndication, 0, ies)
}

// TS 29.281 clause 7.2.3
func supportedExtensionHeadersNotification(types []byte) []byte {
	ies := []byte{ieExtensionHeaderTypeList, byte(len(types))}
	ies = append(ies, types...)

	return signalling(msgSupportedExtensionHeadersNotif, 0, ies)
}

// TS 29.281 clause 7.3.1
// TS 29.281 clause 8.1
func parseErrorIndication(b []byte, h header) (uint32, netip.Addr, error) {
	_, off, err := extensions(b, h)
	if err != nil {
		return 0, netip.Addr{}, err
	}

	var (
		teid    uint32
		haveID  bool
		peer    netip.Addr
		lastTyp byte
	)

	for off < h.length {
		typ := b[off]
		if typ < lastTyp {
			return 0, netip.Addr{}, errMalformed
		}

		lastTyp = typ

		if typ&0x80 == 0 {
			switch typ {
			case ieRecovery:
				if off+2 > h.length {
					return 0, netip.Addr{}, errMalformed
				}

				off += 2
			case ieTEIDDataI:
				if off+5 > h.length {
					return 0, netip.Addr{}, errMalformed
				}

				teid, haveID = binary.BigEndian.Uint32(b[off+1:off+5]), true
				off += 5
			default:
				return 0, netip.Addr{}, errMalformed
			}

			continue
		}

		if off+3 > h.length {
			return 0, netip.Addr{}, errMalformed
		}

		n := int(binary.BigEndian.Uint16(b[off+1 : off+3]))
		if off+3+n > h.length {
			return 0, netip.Addr{}, errMalformed
		}

		if typ == ieGTPUPeerAddress {
			a, ok := netip.AddrFromSlice(b[off+3 : off+3+n])
			if !ok || (n != 4 && n != 16) {
				return 0, netip.Addr{}, errMalformed
			}

			peer = a.Unmap()
		}

		off += 3 + n
	}

	if !haveID || !peer.IsValid() {
		return 0, netip.Addr{}, errMalformed
	}

	return teid, peer, nil
}

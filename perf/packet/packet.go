package packet

import (
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
)

const (
	Port          = 2152
	EthHeader     = 14
	IPv4Header    = 20
	UDPHeader     = 8
	GTPHeader     = 8
	ExtLen        = 8
	MinInner      = IPv4Header + UDPHeader
	typeEchoReq   = 1
	typeEchoResp  = 2
	typeGPDU      = 255
	flagsPlain    = 0x30
	flagsExt      = 0x34
	flagSeq       = 0x02
	extPDUSession = 0x85
	ieRecovery    = 14
	qfi           = 9
	innerTTL      = 64
	outerTTL      = 64
	innerSrcPort  = 5000
	innerDstPort  = 5001
)

var server = netip.AddrFrom4([4]byte{10, 100, 0, 1})

var (
	ErrShort    = errors.New("short packet")
	ErrNotGTPU  = errors.New("not a GTP-U version 1 message")
	ErrTooSmall = errors.New("inner packet smaller than an IPv4 and UDP header")
)

type Flow struct {
	Src     netip.Addr `json:"src"`
	Dst     netip.Addr `json:"dst"`
	SrcPort uint16     `json:"sport"`
	TEID    uint32     `json:"teid"`
}

type Options struct {
	Inner  int
	Ext    bool
	Uplink bool
}

func (o Options) GTPLen() int {
	n := GTPHeader
	if o.Ext {
		n += ExtLen
	}

	return n
}

func (o Options) FrameLen() int {
	return EthHeader + IPv4Header + UDPHeader + o.GTPLen() + o.Inner
}

func Frame(src, dst net.HardwareAddr, f Flow, o Options, ue netip.Addr) ([]byte, error) {
	if o.Inner < MinInner {
		return nil, ErrTooSmall
	}

	b := make([]byte, o.FrameLen())
	copy(b[0:6], dst)
	copy(b[6:12], src)
	binary.BigEndian.PutUint16(b[12:14], 0x0800)

	ip := b[EthHeader:]
	udpLen := UDPHeader + o.GTPLen() + o.Inner
	ipv4(ip, f.Src, f.Dst, IPv4Header+udpLen, outerTTL)

	udp := ip[IPv4Header:]
	binary.BigEndian.PutUint16(udp[0:2], f.SrcPort)
	binary.BigEndian.PutUint16(udp[2:4], Port)
	binary.BigEndian.PutUint16(udp[4:6], uint16(udpLen))

	gtp := udp[UDPHeader:]
	n := GPDU(gtp, f.TEID, o)
	Inner(gtp[n:n+o.Inner], ue, nil)

	binary.BigEndian.PutUint16(udp[6:8], udpChecksum(f.Src, f.Dst, udp[:udpLen]))

	return b, nil
}

func GPDU(b []byte, teid uint32, o Options) int {
	b[0] = flagsPlain
	b[1] = typeGPDU
	binary.BigEndian.PutUint16(b[2:4], uint16(o.GTPLen()-GTPHeader+o.Inner))
	binary.BigEndian.PutUint32(b[4:8], teid)

	if !o.Ext {
		return GTPHeader
	}

	b[0] = flagsExt
	b[8], b[9], b[10], b[11] = 0, 0, 0, extPDUSession
	b[12] = 1
	b[13] = 0

	if o.Uplink {
		b[13] = 0x10
	}

	b[14] = qfi
	b[15] = 0

	return GTPHeader + ExtLen
}

func Inner(b []byte, src netip.Addr, payload []byte) {
	ipv4(b, src, server, len(b), innerTTL)

	udp := b[IPv4Header:]
	binary.BigEndian.PutUint16(udp[0:2], innerSrcPort)
	binary.BigEndian.PutUint16(udp[2:4], innerDstPort)
	binary.BigEndian.PutUint16(udp[4:6], uint16(len(udp)))
	copy(udp[UDPHeader:], payload)
	binary.BigEndian.PutUint16(udp[6:8], udpChecksum(src, server, udp))
}

func InnerPayload(b []byte) []byte {
	if len(b) < MinInner {
		return nil
	}

	return b[MinInner:]
}

func ipv4(b []byte, src, dst netip.Addr, total int, ttl byte) {
	b[0] = 0x45
	b[1] = 0
	binary.BigEndian.PutUint16(b[2:4], uint16(total))
	binary.BigEndian.PutUint16(b[4:6], 0)
	binary.BigEndian.PutUint16(b[6:8], 0)
	b[8] = ttl
	b[9] = 17
	b[10], b[11] = 0, 0
	s, d := src.As4(), dst.As4()
	copy(b[12:16], s[:])
	copy(b[16:20], d[:])
	binary.BigEndian.PutUint16(b[10:12], ^fold(sum(b[:IPv4Header], 0)))
}

func udpChecksum(src, dst netip.Addr, udp []byte) uint16 {
	s, d := src.As4(), dst.As4()

	var pseudo [12]byte

	copy(pseudo[0:4], s[:])
	copy(pseudo[4:8], d[:])
	pseudo[9] = 17
	binary.BigEndian.PutUint16(pseudo[10:12], uint16(len(udp)))

	c := ^fold(sum(udp, sum(pseudo[:], 0)))
	if c == 0 {
		return 0xffff
	}

	return c
}

func sum(b []byte, acc uint32) uint32 {
	for i := 0; i+1 < len(b); i += 2 {
		acc += uint32(binary.BigEndian.Uint16(b[i:]))
	}

	if len(b)%2 == 1 {
		acc += uint32(b[len(b)-1]) << 8
	}

	return acc
}

func fold(acc uint32) uint16 {
	for acc > 0xffff {
		acc = acc&0xffff + acc>>16
	}

	return uint16(acc)
}

type Message struct {
	Type uint8
	TEID uint32
	Seq  uint16
	Body []byte
}

func Parse(b []byte) (Message, error) {
	if len(b) < GTPHeader {
		return Message{}, ErrShort
	}

	if b[0]>>5 != 1 || b[0]&0x10 == 0 {
		return Message{}, ErrNotGTPU
	}

	end := GTPHeader + int(binary.BigEndian.Uint16(b[2:4]))
	if end > len(b) {
		return Message{}, ErrShort
	}

	m := Message{Type: b[1], TEID: binary.BigEndian.Uint32(b[4:8])}
	off := GTPHeader

	if b[0]&0x07 != 0 {
		if end < GTPHeader+4 {
			return Message{}, ErrShort
		}

		m.Seq = binary.BigEndian.Uint16(b[8:10])
		next := b[11]
		off += 4

		for next != 0 {
			if off >= end || b[off] == 0 || off+int(b[off])*4 > end {
				return Message{}, ErrShort
			}

			off += int(b[off]) * 4
			next = b[off-1]
		}
	}

	m.Body = b[off:end]

	return m, nil
}

func (m Message) GPDU() bool {
	return m.Type == typeGPDU
}

func (m Message) EchoRequest() bool {
	return m.Type == typeEchoReq
}

func EchoResponse(req Message) []byte {
	b := make([]byte, GTPHeader+4+2)
	b[0] = flagsPlain | flagSeq
	b[1] = typeEchoResp
	binary.BigEndian.PutUint16(b[2:4], uint16(len(b)-GTPHeader))
	binary.BigEndian.PutUint16(b[8:10], req.Seq)
	b[12] = ieRecovery
	b[13] = 0

	return b
}

func Rewrite(b []byte, teid uint32) {
	binary.BigEndian.PutUint32(b[4:8], teid)
}

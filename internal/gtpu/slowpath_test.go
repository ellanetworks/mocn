package gtpu

import (
	"bytes"
	"encoding/binary"
	"log/slog"
	"net"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func udpPeer(t *testing.T, a netip.Addr) *net.UDPConn {
	t.Helper()

	c, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(netip.AddrPortFrom(a, Port)))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = c.Close() })

	return c
}

func receive(t *testing.T, c *net.UDPConn) ([]byte, netip.AddrPort) {
	t.Helper()

	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))

	buf := make([]byte, 65535)

	n, from, err := c.ReadFromUDPAddrPort(buf)
	if err != nil {
		t.Fatalf("nothing received: %v", err)
	}

	return buf[:n], from
}

func silent(t *testing.T, c *net.UDPConn) {
	t.Helper()

	_ = c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))

	buf := make([]byte, 2048)
	if n, _, err := c.ReadFromUDPAddrPort(buf); err == nil {
		t.Fatalf("unexpected message %x", buf[:n])
	}
}

func TestSlowPath(t *testing.T) {
	requirePrivileged(t)

	ran := dummy(t, "gtpran", net.HardwareAddr{2, 0, 0, 0, 0, 1}, "10.98.0.1/24", "10.98.0.2/24")
	core := dummy(t, "gtpcore", net.HardwareAddr{2, 0, 0, 0, 0, 3}, "10.99.0.1/24", "10.99.0.2/24")

	host := &fakeHost{
		routes: map[netip.Prefix]Route{
			netip.MustParsePrefix("10.98.0.0/24"): {Src: dpRAN, Ifindex: ran},
			netip.MustParsePrefix("10.99.0.0/24"): {Src: dpCore, Ifindex: core},
		},
		names: map[int]string{ran: "gtpran", core: "gtpcore"},
	}

	p := New(Config{
		RANAddress: dpRAN, RANInterface: "gtpran", Enabled: true, Logger: slog.Default(), Quarantine: time.Minute,
		Host: host,
	})
	if err := p.Start(t.Context()); err != nil {
		t.Fatal(err)
	}

	if err := p.SetOperators([]Operator{{ID: 1, Name: "op", Aggregated: true}}); err != nil {
		t.Fatal(err)
	}

	p.Anchor(1, "mme", anchorOf(t, host, netip.MustParseAddr("10.99.0.50")))

	radio, sgw := udpPeer(t, dpRadio), udpPeer(t, dpSGW)
	path, _ := p.Path(1, "eNB:1")

	ul, err := p.Open(path, Core, TLA(dpSGW), 0x77)
	if err != nil {
		t.Fatal(err)
	}

	to := net.UDPAddrFromAddrPort(netip.AddrPortFrom(dpRAN, Port))

	big := sequencedGPDU(ul.TEID)
	big = append(big, make([]byte, 3000)...)
	binary.BigEndian.PutUint16(big[2:4], uint16(len(big)-8))

	if _, err := radio.WriteToUDP(big, to); err != nil {
		t.Fatal(err)
	}

	got, from := receive(t, sgw)
	if from.Addr() != dpCore || from.Port() != sourcePort(0x77) || binary.BigEndian.Uint32(got[4:8]) != 0x77 ||
		!bytes.Equal(got[8:], big[8:]) {
		t.Fatalf("relayed %d bytes from %v, TEID %#x", len(got), from, binary.BigEndian.Uint32(got[4:8]))
	}

	if err := setsockopt(radio, unix.IPPROTO_IP, unix.IP_TOS, 0xb8); err != nil {
		t.Fatal(err)
	}

	if err := setsockopt(sgw, unix.IPPROTO_IP, unix.IP_RECVTOS, 1); err != nil {
		t.Fatal(err)
	}

	if _, err := radio.WriteToUDP(plainGPDU(ul.TEID), to); err != nil {
		t.Fatal(err)
	}

	_ = sgw.SetReadDeadline(time.Now().Add(2 * time.Second))

	buf, oob := make([]byte, 2048), make([]byte, oobSize)

	n, oobn, _, _, err := sgw.ReadMsgUDPAddrPort(buf, oob)
	if err != nil || n != len(plainGPDU(0)) {
		t.Fatalf("read %d bytes: %v", n, err)
	}

	var in inbound
	if parseControl(oob[:oobn], &in); in.tos != 0xb8 {
		t.Fatalf("relayed with TOS %#x, want 0xb8", in.tos)
	}

	if _, err := radio.WriteToUDP(echoRequest(9), to); err != nil {
		t.Fatal(err)
	}

	got, from = receive(t, radio)
	if from != netip.AddrPortFrom(dpRAN, Port) || !bytes.Equal(got, echoResponse(9)) {
		t.Fatalf("echo response %x from %v", got, from)
	}

	ei := errorIndication(0x77, dpSGW)
	if _, err := sgw.WriteToUDP(ei, net.UDPAddrFromAddrPort(netip.AddrPortFrom(dpCore, Port))); err != nil {
		t.Fatal(err)
	}

	got, _ = receive(t, radio)

	h, err := parseHeader(got)
	if err != nil {
		t.Fatal(err)
	}

	if teid, peer, err := parseErrorIndication(got, h); err != nil || teid != ul.TEID || peer != dpRAN {
		t.Fatalf("translated Error Indication = %#x %v %v, want %#x %v", teid, peer, err, ul.TEID, dpRAN)
	}

	if _, err := radio.WriteToUDP(sequencedGPDU(0x1234), to); err != nil {
		t.Fatal(err)
	}

	got, _ = receive(t, radio)
	if h, err := parseHeader(got); err != nil || h.msg != msgErrorIndication {
		t.Fatalf("unknown TEID answered with %x", got)
	}

	p.Close(ul.TEID)

	if _, err := radio.WriteToUDP(sequencedGPDU(ul.TEID), to); err != nil {
		t.Fatal(err)
	}

	got, _ = receive(t, radio)
	if teid, peer, err := parseErrorIndication(got, mustHeader(t, got)); err != nil || teid != ul.TEID || peer != dpRAN {
		t.Fatalf("G-PDU on a closed tunnel answered with %x, want an Error Indication", got)
	}

	silent(t, sgw)
}

func mustHeader(t *testing.T, b []byte) header {
	t.Helper()

	h, err := parseHeader(b)
	if err != nil {
		t.Fatal(err)
	}

	return h
}

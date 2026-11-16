package gtpu

import (
	"bytes"
	"encoding/binary"
	"net"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func (h *harness) uplink(t *testing.T) Tunnel {
	t.Helper()

	tun, err := h.p.Open(h.path(t, 7), Core, TLA(sgw), 0xabc)
	if err != nil {
		t.Fatal(err)
	}

	return tun
}

func fromRadio(b []byte) inbound {
	return inbound{local: ranGW, from: netip.AddrPortFrom(radio, 40000), ifindex: 10, tos: 0xb8, b: b}
}

func TestSlowPathRelaysWithTrafficClassAndPinnedEgress(t *testing.T) {
	h := newHarness(t)
	tun := h.uplink(t)

	h.p.sp.handle(fromRadio(sequencedGPDU(tun.TEID)))

	if len(h.out.raws) != 1 {
		t.Fatalf("relayed %d packets", len(h.out.raws))
	}

	r := h.out.raws[0]
	if r.src != coreGW || r.dst != sgw || r.tos != 0xb8 || r.ifindex != 20 {
		t.Fatalf("sent %+v", r)
	}

	if !udpValid(coreGW, sgw, r.b) || binary.BigEndian.Uint16(r.b[0:2]) != sourcePort(0xabc) ||
		binary.BigEndian.Uint32(r.b[12:16]) != 0xabc || !bytes.Equal(r.b[16:], sequencedGPDU(0)[8:]) {
		t.Fatalf("relayed %x", r.b)
	}

	if c := h.p.Counters(tun.TEID); c.Packets != 1 || c.Bytes != 12 {
		t.Fatalf("counters = %+v, want one G-PDU and its 12-octet T-PDU", c)
	}

	endMarker := []byte{0x30, msgEndMarker, 0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(endMarker[4:8], tun.TEID)
	h.p.sp.handle(fromRadio(endMarker))

	if len(h.out.raws) != 2 {
		t.Fatalf("End Marker not relayed: %d messages", len(h.out.raws))
	}

	if c := h.p.Counters(tun.TEID); c.Packets != 1 || c.Bytes != 12 {
		t.Fatalf("counters = %+v after an End Marker, want G-PDUs only", c)
	}

	wantSample(t, h.p, "ellamocn_user_plane_slow_path_relayed_total", map[string]string{"operator": "op", "direction": "uplink"}, 2)
	wantSample(t, h.p, "ellamocn_user_plane_slow_path_relayed_total", map[string]string{"operator": "op", "direction": "downlink"}, 0)
	wantSample(t, h.p, "ellamocn_user_plane_packets_total",
		map[string]string{"operator": "op", "radio": "eNB:1", "direction": "uplink"}, 1)
	wantSample(t, h.p, "ellamocn_user_plane_bytes_total",
		map[string]string{"operator": "op", "radio": "eNB:1", "direction": "uplink"}, 12)
}

func TestSlowPathChecksIngressInterface(t *testing.T) {
	h := newHarness(t)
	tun := h.uplink(t)

	in := fromRadio(sequencedGPDU(tun.TEID))
	in.ifindex = 20

	h.p.sp.handle(in)

	if len(h.out.raws) != 0 {
		t.Fatalf("G-PDU from the operator interface relayed: %+v", h.out.raws)
	}

	wantSample(t, h.p, "ellamocn_user_plane_dropped_packets_total",
		map[string]string{"operator": "op", "path": "slow", "reason": "wrong_interface"}, 1)

	in.ifindex = loopbackIf
	in.from = netip.AddrPortFrom(coreGW, Port)

	h.p.sp.handle(in)

	if len(h.out.raws) != 1 {
		t.Fatal("G-PDU from Ella MOCN's own address over loopback dropped")
	}

	in.from = netip.AddrPortFrom(radio, Port)
	h.p.sp.handle(in)

	if len(h.out.raws) != 1 {
		t.Fatal("G-PDU from a foreign address over loopback relayed")
	}
}

func TestSlowPathErrorIndicationForUnknownTunnels(t *testing.T) {
	h := newHarness(t)
	tun := h.uplink(t)

	h.p.Close(tun.TEID)

	h.p.sp.handle(fromRadio(sequencedGPDU(tun.TEID)))

	if len(h.out.udps) != 1 {
		t.Fatalf("G-PDU on a closed tunnel answered with %+v", h.out.udps)
	}

	wantSample(t, h.p, "ellamocn_user_plane_dropped_packets_total",
		map[string]string{"operator": "", "path": "slow", "reason": "quarantined"}, 1)

	u := h.out.udps[0]
	if teid, peer, err := parseErrorIndication(u.b, mustHeader(t, u.b)); err != nil || teid != tun.TEID ||
		peer != ranGW || u.to != netip.AddrPortFrom(radio, Port) || u.local != ranGW || u.via != 10 {
		t.Fatalf("Error Indication %x to %v via %d", u.b, u.to, u.via)
	}

	h.out.reset()

	h.p.sp.handle(fromRadio([]byte{0x30, msgEndMarker, 0, 0, 0, 0, 0x99, 0x99}))
	h.p.sp.handle(fromRadio(plainGPDU(0)))

	if len(h.out.udps) != 0 {
		t.Fatalf("End Marker or TEID 0 answered with %+v", h.out.udps)
	}

	for i := range 1000 {
		h.p.sp.handle(fromRadio(plainGPDU(0x5555 + uint32(i))))
	}

	if len(h.out.udps) != 1000 {
		t.Fatalf("%d Error Indications for 1000 G-PDUs to unknown TEIDs", len(h.out.udps))
	}

	for i, u := range h.out.udps {
		if teid, peer, err := parseErrorIndication(u.b, mustHeader(t, u.b)); err != nil || teid != 0x5555+uint32(i) ||
			peer != ranGW || u.to != netip.AddrPortFrom(radio, Port) {
			t.Fatalf("Error Indication %d: %x to %v", i, u.b, u.to)
		}
	}

	wantSample(t, h.p, "ellamocn_user_plane_error_indications_total",
		map[string]string{"origin": "generated", "result": "sent"}, 1001)
}

func TestSlowPathCountsSocketDrops(t *testing.T) {
	h := newHarness(t)

	wantSample(t, h.p, "ellamocn_user_plane_dropped_packets_total",
		map[string]string{"operator": "", "path": "slow", "reason": "receive_buffer_full"}, 0)

	in := fromRadio(echoRequest(1))
	in.dropped = 7
	h.p.sp.handle(in)

	wantSample(t, h.p, "ellamocn_user_plane_dropped_packets_total",
		map[string]string{"operator": "", "path": "slow", "reason": "receive_buffer_full"}, 7)
}

func TestSlowPathTranslatesErrorIndication(t *testing.T) {
	h := newHarness(t)
	tun := h.uplink(t)

	ei := inbound{local: coreGW, from: netip.AddrPortFrom(sgw, Port), ifindex: 20, b: errorIndication(0xabc, sgw)}

	h.p.sp.handle(ei)

	if len(h.out.udps) != 0 {
		t.Fatalf("Error Indication relayed without a known radio address: %+v", h.out.udps)
	}

	wantSample(t, h.p, "ellamocn_user_plane_error_indications_total",
		map[string]string{"origin": "received", "result": "no_return_address"}, 1)

	h.p.sp.handle(fromRadio(plainGPDU(tun.TEID)))
	h.p.sp.handle(ei)

	if len(h.out.udps) != 1 {
		t.Fatalf("translated %d Error Indications", len(h.out.udps))
	}

	wantSample(t, h.p, "ellamocn_user_plane_error_indications_total",
		map[string]string{"origin": "received", "result": "translated"}, 1)

	h.p.sp.handle(inbound{local: coreGW, from: netip.AddrPortFrom(sgw, Port), ifindex: 20, b: errorIndication(0x5555, sgw)})
	h.p.sp.handle(inbound{local: coreGW, from: netip.AddrPortFrom(sgw, Port), ifindex: 20, b: signalling(msgErrorIndication, 0, []byte{ieTEIDDataI, 0, 0})})

	wantSample(t, h.p, "ellamocn_user_plane_error_indications_total",
		map[string]string{"origin": "received", "result": "no_tunnel"}, 1)
	wantSample(t, h.p, "ellamocn_user_plane_error_indications_total",
		map[string]string{"origin": "received", "result": "malformed"}, 1)

	u := h.out.udps[0]
	if teid, peer, err := parseErrorIndication(u.b, mustHeader(t, u.b)); err != nil || teid != tun.TEID || peer != ranGW ||
		u.to != netip.AddrPortFrom(radio, Port) || u.via != 10 {
		t.Fatalf("translated Error Indication %x to %v via %d", u.b, u.to, u.via)
	}

	h.dp.last[tun.TEID] = netip.MustParseAddr("10.1.0.21")
	ei.ifindex = 10
	h.p.sp.handle(ei)

	if len(h.out.udps) != 1 {
		t.Fatal("Error Indication from the radio interface translated")
	}

	ei.ifindex = 20
	h.p.sp.handle(ei)

	if got := h.out.udps[len(h.out.udps)-1].to.Addr(); got != netip.MustParseAddr("10.1.0.21") {
		t.Fatalf("Error Indication sent to %v, want the latest fast-path source", got)
	}
}

func TestSlowPathExtensionHeaders(t *testing.T) {
	h := newHarness(t)
	tun := h.uplink(t)

	required := sequencedGPDU(tun.TEID)
	required[11] = 0xc5

	h.p.sp.handle(fromRadio(required))

	if len(h.out.raws) != 0 || len(h.out.udps) != 1 || h.out.udps[0].b[1] != msgSupportedExtensionHeadersNotif ||
		h.out.udps[0].via != 10 {
		t.Fatalf("comprehension-required header: relayed %d, answered %+v", len(h.out.raws), h.out.udps)
	}

	onlyDiscard := []byte{
		0x34, msgGPDU, 0, 12, 0, 0, 0, 0, 0, 0, 0, 0x40, 1, 0x12, 0x34, 0, 0xde, 0xad, 0xbe, 0xef,
	}
	binary.BigEndian.PutUint32(onlyDiscard[4:8], tun.TEID)

	h.p.sp.handle(fromRadio(onlyDiscard))

	if len(h.out.raws) != 1 {
		t.Fatal("G-PDU with a discardable extension not relayed")
	}

	gtp := h.out.raws[0].b[8:]
	if gtp[0]&(flagE|flagS|flagPN) != 0 || binary.BigEndian.Uint16(gtp[2:4]) != 4 ||
		!bytes.Equal(gtp[8:], []byte{0xde, 0xad, 0xbe, 0xef}) {
		t.Fatalf("stripped G-PDU = %x", gtp)
	}
}

func TestSlowPathEcho(t *testing.T) {
	h := newHarness(t)
	tun := h.uplink(t)
	path := h.path(t, 7)

	if _, err := h.p.Bind(&Session{}, Binding{Path: path, Bearer: 5, Slot: Slot{Kind: KindForwardDL}}, Core,
		TLA(netip.MustParseAddr("10.2.0.99")), 1); err != nil {
		t.Fatal(err)
	}

	h.p.sp.echo()

	if len(h.out.udps) != 1 || h.out.udps[0].to != netip.AddrPortFrom(sgw, Port) ||
		h.out.udps[0].local != tun.Source || h.out.udps[0].via != 20 || h.out.udps[0].b[1] != msgEchoRequest {
		t.Fatalf("Echo Requests %+v, want one to the S-GW only, pinned to the operator's interface", h.out.udps)
	}

	h.p.sp.handle(inbound{local: coreGW, from: netip.AddrPortFrom(sgw, Port), b: echoResponse(1)})

	up := map[string]string{"operator": "op", "side": "core", "peer": sgw.String()}
	wantSample(t, h.p, "ellamocn_user_plane_peer_up", up, 1)

	for range 3 {
		h.p.sp.echo()
	}

	wantSample(t, h.p, "ellamocn_user_plane_peer_up", up, 1)
	h.p.sp.echo()
	wantSample(t, h.p, "ellamocn_user_plane_peer_up", up, 0)

	h.p.sp.handle(inbound{local: coreGW, from: netip.AddrPortFrom(sgw, Port), b: echoResponse(5)})
	wantSample(t, h.p, "ellamocn_user_plane_peer_up", up, 1)

	h.out.reset()
	h.p.sp.handle(inbound{local: ranGW, from: netip.AddrPortFrom(radio, 7777), ifindex: 10, b: echoRequest(9)})

	if len(h.out.udps) != 1 || !bytes.Equal(h.out.udps[0].b, echoResponse(9)) ||
		h.out.udps[0].to != netip.AddrPortFrom(radio, 7777) || h.out.udps[0].via != 10 {
		t.Fatalf("Echo Response %+v, want it pinned to the radio interface", h.out.udps)
	}

	h.out.reset()
	h.p.sp.handle(inbound{local: ranGW, from: netip.AddrPortFrom(radio, 7777), ifindex: 99, b: echoRequest(10)})

	if len(h.out.udps) != 0 {
		t.Fatalf("Echo Request from an interface Ella MOCN does not relay on answered: %+v", h.out.udps)
	}

	wantSample(t, h.p, "ellamocn_user_plane_dropped_packets_total",
		map[string]string{"operator": "", "path": "slow", "reason": "wrong_interface"}, 1)

	h.p.Close(tun.TEID)
	wantAbsent(t, h.p, "ellamocn_user_plane_peer_up", up)
}

func TestSlowPathDropsWhenTheRouteMoves(t *testing.T) {
	h := newHarness(t)
	tun := h.uplink(t)

	h.host.routes[netip.MustParsePrefix(sgw.String()+"/32")] = Route{Src: coreB, Ifindex: 21}

	h.p.sp.handle(fromRadio(sequencedGPDU(tun.TEID)))

	if len(h.out.raws) != 0 {
		t.Fatalf("relayed after the route moved to another interface: %+v", h.out.raws)
	}

	wantSample(t, h.p, "ellamocn_user_plane_dropped_packets_total",
		map[string]string{"operator": "op", "path": "slow", "reason": "wrong_egress_interface"}, 1)

	delete(h.host.routes, netip.MustParsePrefix(sgw.String()+"/32"))

	h.p.sp.handle(fromRadio(sequencedGPDU(tun.TEID)))

	if len(h.out.raws) != 1 || h.out.raws[0].ifindex != 20 {
		t.Fatalf("not relayed once the route is back: %+v", h.out.raws)
	}
}

func TestSocketReportsReceiveBufferDrops(t *testing.T) {
	a := netip.MustParseAddr("127.0.0.77")
	gate := make(chan struct{})
	got := make(chan inbound, 1024)

	k := &sockets{conns: map[netip.Addr]*net.UDPConn{}}
	t.Cleanup(k.close)

	if err := k.listen(a, func(in inbound) {
		<-gate

		got <- inbound{dropped: in.dropped, b: append([]byte(nil), in.b...)}
	}); err != nil {
		t.Fatal(err)
	}

	if err := setsockopt(k.conns[a], unix.SOL_SOCKET, unix.SO_RCVBUF, 1); err != nil {
		t.Fatal(err)
	}

	peer, err := net.DialUDP("udp", nil, net.UDPAddrFromAddrPort(netip.AddrPortFrom(a, Port)))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = peer.Close() })

	const burst = 500

	for range burst {
		if _, err := peer.Write(plainGPDU(1)); err != nil {
			t.Fatal(err)
		}
	}

	close(gate)

	var dropped, delivered int

	for marker := 0; ; marker++ {
		if _, err := peer.Write(echoRequest(uint16(marker))); err != nil {
			t.Fatal(err)
		}

		for {
			select {
			case in := <-got:
				dropped += int(in.dropped)

				if in.b[1] != msgEchoRequest {
					delivered++
					continue
				}

				seq := int(binary.BigEndian.Uint16(in.b[8:10]))
				if delivered == burst || dropped != burst-delivered+seq {
					t.Fatalf("%d delivered, %d drops reported, first marker %d", delivered, dropped, seq)
				}

				return
			case <-time.After(20 * time.Millisecond):
			}

			break
		}
	}
}

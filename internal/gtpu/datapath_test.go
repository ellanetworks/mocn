package gtpu

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"strings"
	"testing"

	"github.com/cilium/ebpf"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const (
	tcxNext     = 0xffffffff
	tcxDrop     = 2
	tcxRedirect = 7
	testLeg     = 3
	otherLeg    = 4

	testSkbChecksumComplete = 1 << 2
)

var (
	dpRAN    = netip.MustParseAddr("10.98.0.1")
	dpRadio  = netip.MustParseAddr("10.98.0.2")
	dpCore   = netip.MustParseAddr("10.99.0.1")
	dpSGW    = netip.MustParseAddr("10.99.0.2")
	dpRAN6   = netip.MustParseAddr("fd98::1")
	dpRadio6 = netip.MustParseAddr("fd98::2")
	dpCore6  = netip.MustParseAddr("fd99::1")
	dpSGW6   = netip.MustParseAddr("fd99::2")
	radioMAC = net.HardwareAddr{2, 0, 0, 0, 0, 4}
	sgwMAC   = net.HardwareAddr{2, 0, 0, 0, 0, 2}
)

type skbContext struct {
	Len, PktType, Mark, QueueMapping, Protocol, VlanPresent uint32
	VlanTci, VlanProto, Priority, IngressIfindex, Ifindex   uint32
	TcIndex                                                 uint32
	Cb                                                      [5]uint32
	Hash, TcClassid, Data, DataEnd, NapiID, Family          uint32
	RemoteIP4, LocalIP4                                     uint32
	RemoteIP6, LocalIP6                                     [4]uint32
	RemotePort, LocalPort, DataMeta                         uint32
	FlowKeys                                                uint64
	Tstamp                                                  uint64
	WireLen, GsoSegs                                        uint32
	Sk                                                      uint64
	GsoSize                                                 uint32
	TstampType                                              uint8
	_                                                       [3]byte
	Hwtstamp                                                uint64
}

type testDatapath struct {
	dp      *ebpfDatapath
	ranIdx  int
	coreIdx int
}

func requirePrivileged(t *testing.T) {
	t.Helper()

	if os.Geteuid() != 0 {
		if os.Getenv("EBPF_REQUIRE_PRIVILEGED") != "" {
			t.Fatal("BPF_PROG_TEST_RUN requires root")
		}

		t.Skip("BPF_PROG_TEST_RUN requires root")
	}
}

func sysctl(t *testing.T, path, value string) {
	t.Helper()

	old, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.WriteFile(path, old, 0o600) })
}

func forwarding(t *testing.T) {
	t.Helper()

	for _, f := range forwardingSysctls {
		sysctl(t, f, "1")
	}
}

func dummy(t *testing.T, name string, mac net.HardwareAddr, addrs ...string) int {
	t.Helper()

	_ = netlink.LinkDel(&netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: name}})

	l := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: name, HardwareAddr: mac}}
	if err := netlink.LinkAdd(l); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = netlink.LinkDel(l) })

	if err := netlink.LinkSetUp(l); err != nil {
		t.Fatal(err)
	}

	for _, a := range addrs {
		addr, err := netlink.ParseAddr(a)
		if err != nil {
			t.Fatal(err)
		}

		addr.Flags = unix.IFA_F_NODAD
		if err := netlink.AddrAdd(l, addr); err != nil {
			t.Fatal(err)
		}
	}

	return l.Attrs().Index
}

func neighbour(t *testing.T, index int, ip netip.Addr, mac net.HardwareAddr) {
	t.Helper()

	family := netlink.FAMILY_V6
	if ip.Is4() {
		family = netlink.FAMILY_V4
	}

	if err := netlink.NeighSet(&netlink.Neigh{
		LinkIndex: index, IP: ip.AsSlice(), HardwareAddr: mac, State: netlink.NUD_PERMANENT, Family: family,
	}); err != nil {
		t.Fatal(err)
	}
}

func load(t *testing.T) *ebpfDatapath {
	t.Helper()

	dp, err := loadDatapath(t.Context(), slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = dp.close() })

	return dp
}

func setupDatapath(t *testing.T) *testDatapath {
	t.Helper()
	requirePrivileged(t)
	forwarding(t)

	ran := dummy(t, "gtpran", net.HardwareAddr{2, 0, 0, 0, 0, 1}, "10.98.0.1/24", "fd98::1/64")
	core := dummy(t, "gtpcore", net.HardwareAddr{2, 0, 0, 0, 0, 3}, "10.99.0.1/24", "fd99::1/64")

	neighbour(t, core, dpSGW, sgwMAC)
	neighbour(t, core, dpSGW6, sgwMAC)
	neighbour(t, ran, dpRadio, radioMAC)
	neighbour(t, ran, dpRadio6, radioMAC)

	dp := load(t)

	if err := dp.setInterface("gtpran", 1<<ranLeg); err != nil {
		t.Fatal(err)
	}

	if err := dp.setInterface("gtpcore", 1<<testLeg); err != nil {
		t.Fatal(err)
	}

	return &testDatapath{dp: dp, ranIdx: ran, coreIdx: core}
}

func (d *testDatapath) uplink(t *testing.T, teid uint32, local, src, dst netip.Addr, out uint32) {
	t.Helper()
	d.put(t, teid, &Tunnel{
		Local: local, Source: src, Peer: Endpoint{Addr: dst, TEID: out}, outIndex: d.coreIdx, path: 1,
	}, ranLeg)
}

func (d *testDatapath) downlink(t *testing.T, teid uint32, local, src, dst netip.Addr, out uint32) {
	t.Helper()
	d.put(t, teid, &Tunnel{
		Local: local, Source: src, Peer: Endpoint{Addr: dst, TEID: out}, outIndex: d.ranIdx, path: 2,
	}, testLeg)
}

func (d *testDatapath) put(t *testing.T, teid uint32, tun *Tunnel, leg uint8) {
	t.Helper()

	if err := d.dp.put(teid, entryOf(tun, leg, testLeg)); err != nil {
		t.Fatal(err)
	}
}

func (d *testDatapath) runOpts(t *testing.T, ingress int, frame []byte, ctx skbContext, flags uint32) (uint32, []byte) {
	t.Helper()

	ctx.IngressIfindex, ctx.Ifindex = uint32(ingress), uint32(ingress)
	opts := &ebpf.RunOptions{Data: frame, DataOut: make([]byte, len(frame)+64), Context: ctx, Flags: flags}

	ret, err := d.dp.objs.Relay.Run(opts)
	if err != nil {
		if errors.Is(err, unix.EBADMSG) {
			t.Fatalf("skb->csum is stale after the program ran: %v", err)
		}

		t.Fatal(err)
	}

	return ret, opts.DataOut
}

func (d *testDatapath) run(t *testing.T, ingress int, frame []byte) (uint32, []byte) {
	t.Helper()

	return d.runOpts(t, ingress, frame, skbContext{}, 0)
}

func checksum(b []byte) uint16 {
	var sum uint32

	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(b[i:]))
	}

	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}

	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}

	return ^uint16(sum)
}

func pseudo(src, dst netip.Addr, proto byte, n int) []byte {
	var b []byte

	b = append(b, src.AsSlice()...)
	b = append(b, dst.AsSlice()...)

	if src.Is4() {
		return append(b, 0, proto, byte(n>>8), byte(n))
	}

	return append(b, byte(n>>24), byte(n>>16), byte(n>>8), byte(n), 0, 0, 0, proto)
}

func udpValid(src, dst netip.Addr, udp []byte) bool {
	return checksum(append(pseudo(src, dst, unix.IPPROTO_UDP, len(udp)), udp...)) == 0
}

type packet struct {
	src, dst  netip.Addr
	payload   []byte
	sport     uint16
	dport     uint16
	proto     byte
	checksum  bool
	tos       byte
	options   int
	fragment  bool
	ipv6Frag  bool
	ethertype uint16
}

func (p packet) bytes() []byte {
	if p.sport == 0 {
		p.sport = 40000
	}

	if p.dport == 0 {
		p.dport = Port
	}

	if p.proto == 0 {
		p.proto = unix.IPPROTO_UDP
	}

	l4 := make([]byte, 8+len(p.payload))
	binary.BigEndian.PutUint16(l4[0:2], p.sport)
	binary.BigEndian.PutUint16(l4[2:4], p.dport)
	binary.BigEndian.PutUint16(l4[4:6], uint16(len(l4)))
	copy(l4[8:], p.payload)

	if p.checksum {
		c := checksum(append(pseudo(p.src, p.dst, unix.IPPROTO_UDP, len(l4)), l4...))
		if c == 0 {
			c = 0xffff
		}

		binary.BigEndian.PutUint16(l4[6:8], c)
	}

	eth := []byte{2, 0, 0, 0, 0, 1, 2, 0, 0, 0, 0, 9, 0x08, 0x00}

	if p.ethertype != 0 {
		binary.BigEndian.PutUint16(eth[12:14], p.ethertype)
	}

	if p.src.Is4() {
		ihl := 5 + p.options
		ip := []byte{0x40 | byte(ihl), p.tos, 0, 0, 0, 1, 0x40, 0, 30, p.proto, 0, 0}
		binary.BigEndian.PutUint16(ip[2:4], uint16(ihl*4+len(l4)))

		if p.fragment {
			ip[6] = 0x20
		}

		ip = append(ip, p.src.AsSlice()...)
		ip = append(ip, p.dst.AsSlice()...)

		for range p.options {
			ip = append(ip, 1, 1, 1, 1)
		}

		binary.BigEndian.PutUint16(ip[10:12], checksum(ip))

		return append(append(eth, ip...), l4...)
	}

	if p.ethertype == 0 {
		eth[12], eth[13] = 0x86, 0xdd
	}

	next := p.proto

	var ext []byte

	if p.ipv6Frag {
		ext = []byte{p.proto, 0, 0, 1, 0, 0, 0, 1}
		next = 44
	}

	ip := []byte{0x60 | p.tos>>4, p.tos << 4, 0, 0, 0, 0, next, 30}
	binary.BigEndian.PutUint16(ip[4:6], uint16(len(ext)+len(l4)))
	ip = append(ip, p.src.AsSlice()...)
	ip = append(ip, p.dst.AsSlice()...)

	return append(append(append(eth, ip...), ext...), l4...)
}

func frame(src, dst netip.Addr, gtp []byte, withChecksum bool) []byte {
	return packet{src: src, dst: dst, payload: gtp, checksum: withChecksum, tos: 0xb8}.bytes()
}

func sequencedGPDU(teid uint32) []byte {
	return []byte{
		0x36, msgGPDU, 0, 20, byte(teid >> 24), byte(teid >> 16), byte(teid >> 8), byte(teid),
		0x12, 0x34, 0, 0x85, 1, 0x10, 0x05, 0, 0x45, 0, 0, 12, 1, 2, 3, 4, 5, 6, 7, 8,
	}
}

func plainGPDU(teid uint32) []byte {
	return []byte{
		0x30, msgGPDU, 0, 8, byte(teid >> 24), byte(teid >> 16), byte(teid >> 8), byte(teid), 1, 2, 3, 4, 5, 6, 7, 8,
	}
}

func checkRelayed4(t *testing.T, out []byte, src, dst netip.Addr, teid uint32, dmac net.HardwareAddr, withChecksum bool) {
	t.Helper()

	if dmac != nil && !bytes.Equal(out[0:6], dmac) {
		t.Errorf("destination MAC = %x, want %x", out[0:6], dmac)
	}

	ihl := int(out[14]&0x0f) * 4
	ip, udp := out[14:14+ihl], out[14+ihl:]

	if got := netip.AddrFrom4([4]byte(ip[12:16])); got != src {
		t.Errorf("source = %v, want %v", got, src)
	}

	if got := netip.AddrFrom4([4]byte(ip[16:20])); got != dst {
		t.Errorf("destination = %v, want %v", got, dst)
	}

	if ip[8] != 64 || ip[1] != 0xb8 || ip[6]&0x40 != 0 || checksum(ip) != 0 {
		t.Errorf("IPv4 header = %x (TTL 64, DSCP kept, DF clear, valid checksum)", ip)
	}

	if got := binary.BigEndian.Uint16(udp[0:2]); got != sourcePort(teid) {
		t.Errorf("source port = %d", got)
	}

	if withChecksum && !udpValid(src, dst, udp) {
		t.Errorf("UDP checksum invalid: %x", udp)
	}

	if !withChecksum && binary.BigEndian.Uint16(udp[6:8]) != 0 {
		t.Errorf("zero UDP checksum became %#x", binary.BigEndian.Uint16(udp[6:8]))
	}

	if got := binary.BigEndian.Uint32(udp[8+4 : 8+8]); got != teid {
		t.Errorf("TEID = %#x, want %#x", got, teid)
	}
}

func TestDatapathRelaysIPv4(t *testing.T) {
	d := setupDatapath(t)
	d.uplink(t, 0x100, dpRAN, dpCore, dpSGW, 0xabcdef01)

	for _, withChecksum := range []bool{true, false} {
		for _, flags := range []uint32{0, testSkbChecksumComplete} {
			in := frame(dpRadio, dpRAN, sequencedGPDU(0x100), withChecksum)

			ret, out := d.runOpts(t, d.ranIdx, in, skbContext{}, flags)
			if ret != tcxRedirect {
				t.Fatalf("verdict = %#x, want redirect", ret)
			}

			if len(out) != len(in) {
				t.Fatalf("length = %d, want %d", len(out), len(in))
			}

			checkRelayed4(t, out, dpCore, dpSGW, 0xabcdef01, sgwMAC, withChecksum)

			want := sequencedGPDU(0)
			if gtp := out[42:]; !bytes.Equal(gtp[8:], want[8:]) || gtp[0] != want[0] {
				t.Errorf("GTP-U header or payload changed: %x", gtp)
			}
		}
	}
}

func TestDatapathRelaysIPv4WithOptions(t *testing.T) {
	d := setupDatapath(t)
	d.uplink(t, 0x101, dpRAN, dpCore, dpSGW, 0x51)

	in := packet{src: dpRadio, dst: dpRAN, payload: plainGPDU(0x101), checksum: true, tos: 0xb8, options: 2}.bytes()

	ret, out := d.run(t, d.ranIdx, in)
	if ret != tcxRedirect {
		t.Fatalf("verdict = %#x, want redirect", ret)
	}

	checkRelayed4(t, out, dpCore, dpSGW, 0x51, sgwMAC, true)
}

func TestDatapathRelaysDownlink(t *testing.T) {
	d := setupDatapath(t)
	d.downlink(t, 0x110, dpCore, dpRAN, dpRadio, 0x61)
	d.downlink(t, 0x111, dpCore6, dpRAN6, dpRadio6, 0x62)

	ret, out := d.run(t, d.coreIdx, frame(dpSGW, dpCore, plainGPDU(0x110), true))
	if ret != tcxRedirect {
		t.Fatalf("IPv4 verdict = %#x, want redirect", ret)
	}

	checkRelayed4(t, out, dpRAN, dpRadio, 0x61, radioMAC, true)

	ret, out = d.run(t, d.coreIdx, frame(dpSGW6, dpCore6, plainGPDU(0x111), true))
	if ret != tcxRedirect {
		t.Fatalf("IPv6 verdict = %#x, want redirect", ret)
	}

	if !bytes.Equal(out[0:6], radioMAC) || !udpValid(dpRAN6, dpRadio6, out[54:]) {
		t.Errorf("IPv6 downlink = %x", out)
	}
}

func TestDatapathRelaysIPv6(t *testing.T) {
	d := setupDatapath(t)
	d.uplink(t, 0x200, dpRAN6, dpCore6, dpSGW6, 0x77)

	for _, flags := range []uint32{0, testSkbChecksumComplete} {
		in := frame(dpRadio6, dpRAN6, sequencedGPDU(0x200), true)

		ret, out := d.runOpts(t, d.ranIdx, in, skbContext{}, flags)
		if ret != tcxRedirect {
			t.Fatalf("verdict = %#x, want redirect", ret)
		}

		if len(out) != len(in) {
			t.Fatalf("length = %d, want %d", len(out), len(in))
		}

		ip, udp := out[14:54], out[54:]

		if netip.AddrFrom16([16]byte(ip[8:24])) != dpCore6 || netip.AddrFrom16([16]byte(ip[24:40])) != dpSGW6 ||
			ip[7] != 64 {
			t.Errorf("IPv6 header = %x", ip)
		}

		if tc := ip[0]<<4 | ip[1]>>4; tc != 0xb8 {
			t.Errorf("traffic class = %#x, want 0xb8", tc)
		}

		if !udpValid(dpCore6, dpSGW6, udp) {
			t.Errorf("UDP checksum invalid")
		}
	}
}

func TestDatapathEndMarkerAndTunnelStatus(t *testing.T) {
	d := setupDatapath(t)
	d.uplink(t, 0x300, dpRAN, dpCore, dpSGW, 0x9)

	for _, msg := range []byte{msgEndMarker, msgTunnelStatus} {
		pdu := []byte{0x30, msg, 0, 0, 0, 0, 0x03, 0}

		if ret, _ := d.run(t, d.ranIdx, frame(dpRadio, dpRAN, pdu, true)); ret != tcxRedirect {
			t.Fatalf("message %d verdict = %#x, want redirect", msg, ret)
		}
	}
}

func passCounts(t *testing.T, d *testDatapath) []uint64 {
	t.Helper()

	p, err := d.dp.passes()
	if err != nil {
		t.Fatal(err)
	}

	return p
}

func dropCounts(t *testing.T, d *testDatapath) []uint64 {
	t.Helper()

	dr, err := d.dp.drops()
	if err != nil {
		t.Fatal(err)
	}

	return dr[testLeg]
}

func TestDatapathPassesToTheKernel(t *testing.T) {
	d := setupDatapath(t)
	d.uplink(t, 0x400, dpRAN, dpCore, dpSGW, 0x9)
	d.uplink(t, 0x402, dpRAN6, dpCore6, dpSGW6, 0x9)

	required := sequencedGPDU(0x400)
	required[11] = 0xc5

	discard := sequencedGPDU(0x400)
	discard[11] = 0x40

	zeroLength := sequencedGPDU(0x400)
	zeroLength[12] = 0

	long := []byte{0x34, msgGPDU, 0, 0, 0, 0, 0x04, 0, 0, 0, 0, 0x85}
	for range 9 {
		long = append(long, 1, 0x10, 0x05, 0x85)
	}

	long[len(long)-1] = 0
	binary.BigEndian.PutUint16(long[2:4], uint16(len(long)-8))

	if _, _, err := extensions(long, header{flags: long[0], length: len(long)}); err != nil {
		t.Fatal(err)
	}

	version2 := plainGPDU(0x400)
	version2[0] = 0x50

	pt0 := plainGPDU(0x400)
	pt0[0] = 0x20

	none := relayPassReason(255)

	for name, c := range map[string]struct {
		frame  []byte
		ctx    skbContext
		reason relayPassReason
	}{
		"unknown TEID":             {frame(dpRadio, dpRAN, plainGPDU(0x401), true), skbContext{}, relayPassReasonPASS_NO_TUNNEL},
		"other address":            {frame(dpRadio, netip.MustParseAddr("10.98.0.7"), plainGPDU(0x400), true), skbContext{}, relayPassReasonPASS_NOT_LOCAL},
		"other family":             {frame(dpRadio6, dpRAN6, plainGPDU(0x400), true), skbContext{}, relayPassReasonPASS_NOT_LOCAL},
		"IPv4 fragment":            {packet{src: dpRadio, dst: dpRAN, payload: plainGPDU(0x400), fragment: true}.bytes(), skbContext{}, relayPassReasonPASS_IP_FRAGMENT},
		"IPv6 fragment header":     {packet{src: dpRadio6, dst: dpRAN6, payload: plainGPDU(0x402), ipv6Frag: true}.bytes(), skbContext{}, relayPassReasonPASS_IP_FRAGMENT},
		"comprehension required":   {frame(dpRadio, dpRAN, required, true), skbContext{}, relayPassReasonPASS_EXT_HEADER},
		"discard content":          {frame(dpRadio, dpRAN, discard, true), skbContext{}, relayPassReasonPASS_EXT_HEADER},
		"zero-length extension":    {frame(dpRadio, dpRAN, zeroLength, true), skbContext{}, relayPassReasonPASS_EXT_HEADER},
		"more than 8 extensions":   {frame(dpRadio, dpRAN, long, true), skbContext{}, relayPassReasonPASS_EXT_HEADER},
		"IPv6 zero checksum":       {frame(dpRadio6, dpRAN6, plainGPDU(0x402), false), skbContext{}, relayPassReasonPASS_ZERO_CHECKSUM},
		"merged":                   {frame(dpRadio, dpRAN, plainGPDU(0x400), true), skbContext{GsoSegs: 2, GsoSize: 100}, relayPassReasonPASS_MERGED},
		"echo request":             {frame(dpRadio, dpRAN, echoRequest(1), true), skbContext{}, none},
		"GTP version 2":            {frame(dpRadio, dpRAN, version2, true), skbContext{}, none},
		"GTP' (PT=0)":              {frame(dpRadio, dpRAN, pt0, true), skbContext{}, none},
		"other UDP port":           {packet{src: dpRadio, dst: dpRAN, payload: plainGPDU(0x400), dport: 2123}.bytes(), skbContext{}, none},
		"TCP":                      {packet{src: dpRadio, dst: dpRAN, payload: plainGPDU(0x400), proto: unix.IPPROTO_TCP}.bytes(), skbContext{}, none},
		"IPv6 not UDP":             {packet{src: dpRadio6, dst: dpRAN6, payload: plainGPDU(0x402), proto: unix.IPPROTO_TCP}.bytes(), skbContext{}, none},
		"ARP":                      {packet{src: dpRadio, dst: dpRAN, payload: plainGPDU(0x400), ethertype: unix.ETH_P_ARP}.bytes(), skbContext{}, none},
		"VLAN tag":                 {packet{src: dpRadio, dst: dpRAN, payload: plainGPDU(0x400), ethertype: 0x8100}.bytes(), skbContext{}, none},
		"interface not registered": {frame(dpRadio, dpRAN, plainGPDU(0x400), true), skbContext{IngressIfindex: 1}, none},
	} {
		t.Run(name, func(t *testing.T) {
			before := passCounts(t, d)
			ingress := d.ranIdx

			if c.ctx.IngressIfindex != 0 {
				ingress = 1
			}

			ret, _ := d.runOpts(t, ingress, c.frame, c.ctx, 0)
			if ret != tcxNext {
				t.Fatalf("verdict = %#x, want TCX_NEXT", ret)
			}

			after := passCounts(t, d)

			for r := range after {
				want := before[r]
				if relayPassReason(r) == c.reason {
					want++
				}

				if after[r] != want {
					t.Errorf("%s = %d, want %d", passReasons[relayPassReason(r)], after[r], want)
				}
			}
		})
	}
}

func route(t *testing.T, dst string, typ int, link int) {
	t.Helper()

	_, n, err := net.ParseCIDR(dst)
	if err != nil {
		t.Fatal(err)
	}

	r := &netlink.Route{Dst: n, Type: typ, LinkIndex: link}
	if err := netlink.RouteAdd(r); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = netlink.RouteDel(r) })
}

func TestDatapathFIBResults(t *testing.T) {
	d := setupDatapath(t)

	for i, c := range []struct {
		dst    string
		typ    int
		reason relayDropReason
	}{
		{"10.97.1.0/24", unix.RTN_BLACKHOLE, relayDropReasonDROP_FIB_BLACKHOLE},
		{"10.97.2.0/24", unix.RTN_UNREACHABLE, relayDropReasonDROP_FIB_UNREACHABLE},
		{"10.97.3.0/24", unix.RTN_PROHIBIT, relayDropReasonDROP_FIB_PROHIBIT},
	} {
		route(t, c.dst, c.typ, 0)

		teid := uint32(0x500 + i)
		d.uplink(t, teid, dpRAN, dpCore, netip.MustParseAddr(strings.Replace(c.dst, ".0/24", ".9", 1)), 1)

		before := dropCounts(t, d)

		if ret, _ := d.run(t, d.ranIdx, frame(dpRadio, dpRAN, plainGPDU(teid), true)); ret != tcxDrop {
			t.Errorf("%s: verdict = %#x, want drop", dropReasons[c.reason], ret)
		}

		if got := dropCounts(t, d)[c.reason]; got != before[c.reason]+1 {
			t.Errorf("%s drops = %d", dropReasons[c.reason], got)
		}
	}
}

func TestDatapathFragNeededAndForwardingDisabled(t *testing.T) {
	d := setupDatapath(t)
	d.uplink(t, 0x600, dpRAN, dpCore, dpSGW, 1)

	core, err := netlink.LinkByIndex(d.coreIdx)
	if err != nil {
		t.Fatal(err)
	}

	if err := netlink.LinkSetMTU(core, 576); err != nil {
		t.Fatal(err)
	}

	big := plainGPDU(0x600)
	big = append(big, make([]byte, 1000)...)
	binary.BigEndian.PutUint16(big[2:4], uint16(len(big)-8))

	before := passCounts(t, d)

	if ret, _ := d.run(t, d.ranIdx, frame(dpRadio, dpRAN, big, true)); ret != tcxNext {
		t.Fatalf("oversize verdict = %#x, want TCX_NEXT", ret)
	}

	if got := passCounts(t, d)[relayPassReasonPASS_FRAG_NEEDED]; got != before[relayPassReasonPASS_FRAG_NEEDED]+1 {
		t.Errorf("frag_needed = %d", got)
	}

	sysctl(t, "/proc/sys/net/ipv4/conf/gtpran/forwarding", "0")

	if ret, _ := d.run(t, d.ranIdx, frame(dpRadio, dpRAN, plainGPDU(0x600), true)); ret != tcxNext {
		t.Fatalf("forwarding disabled verdict = %#x, want TCX_NEXT", ret)
	}

	if got := passCounts(t, d)[relayPassReasonPASS_FWD_DISABLED]; got != before[relayPassReasonPASS_FWD_DISABLED]+1 {
		t.Errorf("forwarding_disabled = %d", got)
	}
}

func TestDatapathNoNeighbourUsesRedirectNeigh(t *testing.T) {
	d := setupDatapath(t)

	unknown := netip.MustParseAddr("10.99.0.77")
	d.uplink(t, 0x700, dpRAN, dpCore, unknown, 1)

	ret, out := d.run(t, d.ranIdx, frame(dpRadio, dpRAN, plainGPDU(0x700), true))
	if ret != tcxRedirect {
		t.Fatalf("verdict = %#x, want redirect", ret)
	}

	if !bytes.Equal(out[0:12], make([]byte, 12)) || binary.BigEndian.Uint16(out[12:14]) != unix.ETH_P_IP {
		t.Errorf("L2 header for redirect_neigh = %x", out[0:14])
	}

	checkRelayed4(t, out, dpCore, unknown, 1, nil, true)
}

func TestDatapathIsolation(t *testing.T) {
	d := setupDatapath(t)
	d.uplink(t, 0x800, dpRAN, dpCore, dpSGW, 1)

	before := dropCounts(t, d)

	if ret, _ := d.run(t, d.coreIdx, frame(dpRadio, dpRAN, plainGPDU(0x800), true)); ret != tcxDrop {
		t.Errorf("wrong ingress interface verdict = %#x, want drop", ret)
	}

	if ret, _ := d.run(t, d.coreIdx, frame(dpRadio, netip.MustParseAddr("10.98.0.9"), plainGPDU(0x800), true)); ret != tcxNext {
		t.Errorf("transit GTP-U on the wrong interface verdict = %#x, want TCX_NEXT", ret)
	}

	if got := dropCounts(t, d)[relayDropReasonDROP_WRONG_LEG]; got != before[relayDropReasonDROP_WRONG_LEG]+1 {
		t.Errorf("wrong_interface = %d", got)
	}

	d.put(t, 0x801, &Tunnel{
		Local: dpRAN, Source: dpCore, Peer: Endpoint{Addr: dpSGW, TEID: 1}, outIndex: d.ranIdx, path: 1,
	}, ranLeg)

	if ret, _ := d.run(t, d.ranIdx, frame(dpRadio, dpRAN, plainGPDU(0x801), true)); ret != tcxDrop {
		t.Errorf("wrong egress verdict = %#x, want drop", ret)
	}

	if got := dropCounts(t, d)[relayDropReasonDROP_WRONG_EGRESS]; got != before[relayDropReasonDROP_WRONG_EGRESS]+1 {
		t.Errorf("wrong_egress_interface = %d", got)
	}
}

func TestDatapathDropsWhenTheRouteMovesToAnotherInterface(t *testing.T) {
	d := setupDatapath(t)

	moved := netip.MustParseAddr("10.90.0.9")

	route(t, "10.90.0.0/24", unix.RTN_UNICAST, d.coreIdx)
	neighbour(t, d.coreIdx, moved, sgwMAC)
	d.uplink(t, 0x810, dpRAN, dpCore, moved, 1)

	if ret, _ := d.run(t, d.ranIdx, frame(dpRadio, dpRAN, plainGPDU(0x810), true)); ret != tcxRedirect {
		t.Fatalf("verdict on the tunnel's interface = %#x, want redirect", ret)
	}

	other := dummy(t, "gtpother", net.HardwareAddr{2, 0, 0, 0, 0, 8}, "10.89.0.1/24")
	if err := d.dp.setInterface("gtpother", 1<<testLeg); err != nil {
		t.Fatal(err)
	}

	r := &netlink.Route{Dst: &net.IPNet{IP: moved.AsSlice(), Mask: net.CIDRMask(32, 32)}, LinkIndex: other}
	if err := netlink.RouteAdd(r); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = netlink.RouteDel(r) })

	neighbour(t, other, moved, sgwMAC)

	before := dropCounts(t, d)

	if ret, _ := d.run(t, d.ranIdx, frame(dpRadio, dpRAN, plainGPDU(0x810), true)); ret != tcxDrop {
		t.Fatalf("verdict after the route moved to another interface of the operator = %#x, want drop", ret)
	}

	if got := dropCounts(t, d)[relayDropReasonDROP_WRONG_EGRESS]; got != before[relayDropReasonDROP_WRONG_EGRESS]+1 {
		t.Errorf("wrong_egress_interface = %d", got)
	}

	if err := netlink.RouteDel(r); err != nil {
		t.Fatal(err)
	}

	if ret, _ := d.run(t, d.ranIdx, frame(dpRadio, dpRAN, plainGPDU(0x810), true)); ret != tcxRedirect {
		t.Fatalf("verdict once the route is back = %#x, want redirect", ret)
	}
}

func TestDatapathPolicyIPsec(t *testing.T) {
	d := setupDatapath(t)
	d.uplink(t, 0x900, dpRAN, dpCore, dpSGW, 1)

	if err := d.dp.setPolicyIPsec(true); err != nil {
		t.Fatal(err)
	}

	before := passCounts(t, d)

	if ret, _ := d.run(t, d.ranIdx, frame(dpRadio, dpRAN, plainGPDU(0x900), true)); ret != tcxNext {
		t.Errorf("policy-based IPsec verdict = %#x, want TCX_NEXT", ret)
	}

	if got := passCounts(t, d)[relayPassReasonPASS_IPSEC_POLICY]; got != before[relayPassReasonPASS_IPSEC_POLICY]+1 {
		t.Errorf("ipsec_policy = %d", got)
	}
}

func xfrmi(t *testing.T, name string, ifid uint32, addr string) int {
	t.Helper()

	l := &netlink.Xfrmi{LinkAttrs: netlink.LinkAttrs{Name: name}, Ifid: ifid}
	if err := netlink.LinkAdd(l); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = netlink.LinkDel(l) })

	if err := netlink.LinkSetUp(l); err != nil {
		t.Fatal(err)
	}

	a, _ := netlink.ParseAddr(addr)
	if err := netlink.AddrAdd(l, a); err != nil {
		t.Fatal(err)
	}

	return l.Attrs().Index
}

func TestDatapathXFRMInterfaces(t *testing.T) {
	d := setupDatapath(t)

	x := xfrmi(t, "gtpxfrm", 7, "10.96.0.1/24")
	if err := d.dp.setInterface("gtpxfrm", 1<<otherLeg); err != nil {
		t.Fatal(err)
	}

	var iface relayIface
	if err := d.dp.objs.Ifaces.Lookup(uint32(x), &iface); err != nil || iface.Flags != ifaceL3|ifaceXFRM {
		t.Fatalf("xfrm interface = %+v, %v", iface, err)
	}

	sgw := netip.MustParseAddr("10.96.0.2")
	d.put(t, 0xa00, &Tunnel{
		Local: dpRAN, Source: netip.MustParseAddr("10.96.0.1"), Peer: Endpoint{Addr: sgw, TEID: 3}, outIndex: x,
		path: 1,
	}, ranLeg)

	if err := d.dp.setPolicyIPsec(true); err != nil {
		t.Fatal(err)
	}

	if ret, _ := d.run(t, d.ranIdx, frame(dpRadio, dpRAN, plainGPDU(0xa00), true)); ret != tcxNext {
		t.Fatalf("plain ingress with policy-based IPsec = %#x, want TCX_NEXT", ret)
	}

	if err := d.dp.setPolicyIPsec(false); err != nil {
		t.Fatal(err)
	}

	in := frame(dpRadio, dpRAN, plainGPDU(0xa00), true)

	ret, out := d.run(t, d.ranIdx, in)
	if ret != tcxRedirect {
		t.Fatalf("egress on the xfrm interface = %#x, want redirect", ret)
	}

	if !bytes.Equal(out[:14], in[:14]) {
		t.Errorf("L2 header rewritten for an L3 egress: %x", out[:14])
	}

	d.put(t, 0xa01, &Tunnel{
		Local: netip.MustParseAddr("10.96.0.1"), Source: dpRAN, Peer: Endpoint{Addr: dpRadio, TEID: 4},
		outIndex: d.ranIdx, path: 2,
	}, otherLeg)

	if err := d.dp.setPolicyIPsec(true); err != nil {
		t.Fatal(err)
	}

	ret, _ = d.run(t, x, frame(sgw, netip.MustParseAddr("10.96.0.1"), plainGPDU(0xa01), true))
	if ret != tcxNext {
		t.Fatalf("xfrm ingress toward a plain interface with policy-based IPsec = %#x, want TCX_NEXT", ret)
	}

	if err := d.dp.setPolicyIPsec(false); err != nil {
		t.Fatal(err)
	}

	ret, out = d.run(t, x, frame(sgw, netip.MustParseAddr("10.96.0.1"), plainGPDU(0xa01), true))
	if ret != tcxRedirect {
		t.Fatalf("xfrm ingress with a stale MAC header = %#x, want redirect", ret)
	}

	checkRelayed4(t, out, dpRAN, dpRadio, 4, radioMAC, true)
}

func TestDatapathCounters(t *testing.T) {
	d := setupDatapath(t)
	d.uplink(t, 0xb00, dpRAN, dpCore, dpSGW, 1)

	endMarker := []byte{0x30, msgEndMarker, 0, 0, 0, 0, 0x0b, 0}

	for _, gtp := range [][]byte{plainGPDU(0xb00), plainGPDU(0xb00), sequencedGPDU(0xb00), endMarker} {
		if ret, _ := d.run(t, d.ranIdx, frame(dpRadio, dpRAN, gtp, true)); ret != tcxRedirect {
			t.Fatalf("verdict = %#x", ret)
		}
	}

	truncated := plainGPDU(0xb00)
	binary.BigEndian.PutUint16(truncated[2:4], 9)

	if ret, _ := d.run(t, d.ranIdx, frame(dpRadio, dpRAN, truncated, true)); ret != tcxNext {
		t.Fatalf("G-PDU longer than its packet: verdict = %#x, want TCX_NEXT", ret)
	}

	c, last, err := d.dp.tunnel(0xb00)
	if err != nil || c.Packets != 3 || c.Bytes != 8+8+12 || last != dpRadio {
		t.Fatalf("tunnel counters = %+v %v %v, want 3 G-PDUs and their 28 T-PDU bytes", c, last, err)
	}

	paths, err := d.dp.paths()
	if err != nil || paths[1] != c {
		t.Fatalf("path counters = %+v, %v, want %+v", paths, err, c)
	}

	if err := d.dp.setPath(0xb00, 2); err != nil {
		t.Fatal(err)
	}

	if ret, _ := d.run(t, d.ranIdx, frame(dpRadio, dpRAN, plainGPDU(0xb00), true)); ret != tcxRedirect {
		t.Fatalf("verdict after a path move = %#x", ret)
	}

	moved, _, err := d.dp.tunnel(0xb00)
	if paths, _ = d.dp.paths(); err != nil || paths[1] != c || paths[2] != (Counters{Packets: 1, Bytes: 8}) ||
		moved != (Counters{Packets: c.Packets + 1, Bytes: c.Bytes + 8}) {
		t.Fatalf("after a path move: paths %+v, tunnel %+v, %v", paths, moved, err)
	}

	if err := d.dp.resetPath(2); err != nil {
		t.Fatal(err)
	}

	if err := d.dp.resetPath(1); err != nil {
		t.Fatal(err)
	}

	if paths, _ := d.dp.paths(); paths[1] != (Counters{}) {
		t.Fatalf("path counters after reset = %+v", paths[1])
	}

	if ret, _ := d.run(t, d.ranIdx, frame(dpRadio, dpRAN, plainGPDU(0xb01), true)); ret != tcxNext {
		t.Fatal("unknown TEID relayed")
	}

	if err := d.dp.remove(0xb00); err != nil {
		t.Fatal(err)
	}

	if _, _, err := d.dp.tunnel(0xb00); !errors.Is(err, ebpf.ErrKeyNotExist) {
		t.Fatalf("removed tunnel lookup = %v", err)
	}

	if err := d.dp.resetLeg(testLeg); err != nil {
		t.Fatal(err)
	}

	usage := d.dp.mapUsage()
	maps := []string{
		relayMapTunnels, relayMapTunnelStats, relayMapIfaces, relayMapConfig, relayMapPathStats, relayMapDrops,
		relayMapPasses,
	}

	for _, name := range maps {
		if usage[name].memlock == 0 || usage[name].maxEntries == 0 {
			t.Errorf("usage of map %s = %+v", name, usage[name])
		}
	}

	if usage[relayMapTunnels].maxEntries != maxTunnels || usage[relayMapTunnelStats].maxEntries != maxTunnels ||
		usage[relayMapPathStats].maxEntries != maxPaths {
		t.Errorf("capacities = %d tunnels, %d tunnel stats, %d paths", usage[relayMapTunnels].maxEntries,
			usage[relayMapTunnelStats].maxEntries, usage[relayMapPathStats].maxEntries)
	}
}

func TestDatapathTunnelStatsFollowTheTunnel(t *testing.T) {
	d := setupDatapath(t)
	d.uplink(t, 0xc00, dpRAN, dpCore, dpSGW, 1)

	if err := d.dp.put(0xc00, entryOf(&Tunnel{Local: dpRAN}, ranLeg, testLeg)); !errors.Is(err, ebpf.ErrKeyExist) {
		t.Fatalf("second put = %v, want ErrKeyExist", err)
	}

	if ret, _ := d.run(t, d.ranIdx, frame(dpRadio, dpRAN, plainGPDU(0xc00), true)); ret != tcxRedirect {
		t.Fatalf("verdict = %#x", ret)
	}

	if c, _, err := d.dp.tunnel(0xc00); err != nil || c != (Counters{Packets: 1, Bytes: 8}) {
		t.Fatalf("counters after a refused second put = %+v, %v", c, err)
	}

	if err := d.dp.remove(0xc00); err != nil {
		t.Fatal(err)
	}

	for name, m := range map[string]*ebpf.Map{relayMapTunnels: d.dp.objs.Tunnels, relayMapTunnelStats: d.dp.objs.TunnelStats} {
		if v, err := m.LookupBytes(uint32(0xc00)); v != nil || err != nil {
			t.Fatalf("%s entry after remove = %x, %v", name, v, err)
		}
	}

	if err := d.dp.remove(0xc00); err != nil {
		t.Fatalf("second remove = %v", err)
	}

	d.uplink(t, 0xc00, dpRAN, dpCore, dpSGW, 1)

	if c, last, err := d.dp.tunnel(0xc00); err != nil || c != (Counters{}) || last.IsValid() {
		t.Fatalf("reused TEID starts with %+v %v, %v", c, last, err)
	}
}

func TestDatapathPathMovesKeepEveryCount(t *testing.T) {
	d := setupDatapath(t)
	d.uplink(t, 0xd00, dpRAN, dpCore, dpSGW, 1)

	const runs = 20000

	moved := make(chan error, 1)
	stop := make(chan struct{})

	go func() {
		for i := uint32(0); ; i++ {
			select {
			case <-stop:
				moved <- nil
				return
			default:
			}

			if err := d.dp.setPath(0xd00, 1+i%2); err != nil {
				moved <- err
				return
			}
		}
	}()

	relayed := 0

	for range runs {
		ret, _ := d.run(t, d.ranIdx, frame(dpRadio, dpRAN, plainGPDU(0xd00), true))
		if ret == tcxRedirect {
			relayed++
		}
	}

	close(stop)

	if err := <-moved; err != nil {
		t.Fatal(err)
	}

	want := Counters{Packets: uint64(relayed), Bytes: uint64(relayed) * 8}

	c, last, err := d.dp.tunnel(0xd00)
	if err != nil || relayed != runs || c != want || last != dpRadio {
		t.Fatalf("tunnel counters after %d relayed G-PDUs and concurrent path moves = %+v %v, %v", relayed, c, last, err)
	}

	paths, err := d.dp.paths()
	if err != nil || paths[1].Packets+paths[2].Packets != want.Packets || paths[1].Bytes+paths[2].Bytes != want.Bytes {
		t.Fatalf("path counters = %+v, %v, want a total of %+v", paths, err, want)
	}
}

func TestDatapathRefusesASecondInstance(t *testing.T) {
	d := setupDatapath(t)
	other := load(t)

	if err := other.setInterface("gtpran", 1); err == nil || !strings.Contains(err.Error(), "already attached") {
		t.Fatalf("second attach = %v, want a refusal", err)
	}

	if _, ok := other.interfaceLegs(d.ranIdx); ok {
		t.Fatal("refused interface registered")
	}
}

func TestVerifierComplexity(t *testing.T) {
	requirePrivileged(t)

	spec, err := loadRelay()
	if err != nil {
		t.Fatal(err)
	}

	spec.Maps[relayMapTunnels].MaxEntries = maxTunnels
	spec.Maps[relayMapTunnelStats].MaxEntries = maxTunnels
	spec.Maps[relayMapPathStats].MaxEntries = maxPaths

	coll, err := ebpf.NewCollectionWithOptions(spec, ebpf.CollectionOptions{
		Programs: ebpf.ProgramOptions{LogLevel: ebpf.LogLevelStats},
	})
	if err != nil {
		t.Fatal(err)
	}

	defer coll.Close()

	log := coll.Programs["relay"].VerifierLog

	var insns int
	if i := strings.Index(log, "processed "); i >= 0 {
		_, _ = fmt.Sscanf(log[i:], "processed %d insns", &insns)
	}

	if insns == 0 || insns > 500_000 {
		t.Fatalf("verifier processed %d instructions, want fewer than half of the 1M budget:\n%s", insns, log)
	}

	t.Logf("verifier processed %d instructions", insns)
}

func TestDatapathAgreesWithTheSlowPathOnExtensionHeaders(t *testing.T) {
	d := setupDatapath(t)
	d.uplink(t, 0xc00, dpRAN, dpCore, dpSGW, 1)

	types := []byte{0x85, 0x81, 0x40, 0x41, 0xc0, 0xc5, 0x03, 0x00}

	for seed := range 2000 {
		n := seed%11 + 1
		g := []byte{0x34, msgGPDU, 0, 0, 0, 0, 0x0c, 0, 0, 0, 0, 0}

		for i := range n {
			typ := types[(seed*7+i*13)%len(types)]
			if typ == 0 {
				typ = 0x85
			}

			if i == 0 {
				g[11] = typ
			} else {
				g[len(g)-1] = typ
			}

			length := byte(1 + (seed+i)%2)
			if (seed+i)%97 == 0 {
				length = 0
			}

			g = append(g, length)
			g = append(g, make([]byte, int(max(length, 1))*4-2)...)
			g = append(g, 0)
		}

		g = append(g, 0x45, 0, 0, 20)
		binaryPutLength(g)

		h, err := parseHeader(g)
		if err != nil {
			t.Fatal(err)
		}

		exts, _, err := extensions(g, h)
		unknown, strip := relayable(exts)
		goFast := err == nil && len(unknown) == 0 && !strip && len(exts) <= int(relayLimitsMAX_EXT_HEADERS)

		ret, _ := d.run(t, d.ranIdx, frame(dpRadio, dpRAN, g, true))
		if (ret == tcxRedirect) != goFast {
			t.Fatalf("%x: datapath verdict %#x, slow path relayable %v (%v, %x, %v)", g, ret, goFast, err, unknown, strip)
		}
	}
}

func binaryPutLength(g []byte) {
	binary.BigEndian.PutUint16(g[2:4], uint16(len(g)-8))
}

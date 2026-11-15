package gtpu

import (
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"
	"unsafe"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func openTUN(t *testing.T, name string) *os.File {
	t.Helper()

	f, err := os.OpenFile("/dev/net/tun", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = f.Close() })

	var req [unix.IFNAMSIZ + 64]byte

	copy(req[:], name)
	binary.NativeEndian.PutUint16(req[unix.IFNAMSIZ:], unix.IFF_TUN|unix.IFF_NO_PI)

	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), unix.TUNSETIFF, uintptr(unsafe.Pointer(&req[0]))); errno != 0 {
		t.Fatal(errno)
	}

	l, err := netlink.LinkByName(name)
	if err != nil {
		t.Fatal(err)
	}

	addr, _ := netlink.ParseAddr("10.98.0.1/24")
	if err := netlink.AddrAdd(l, addr); err != nil {
		t.Fatal(err)
	}

	if err := netlink.LinkSetUp(l); err != nil {
		t.Fatal(err)
	}

	return f
}

func vethPair(t *testing.T) (*netlink.Veth, net.HardwareAddr) {
	t.Helper()

	v := &netlink.Veth{LinkAttrs: netlink.LinkAttrs{Name: "gtpcore"}, PeerName: "gtppeer"}
	if err := netlink.LinkAdd(v); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = netlink.LinkDel(v) })

	peer, err := netlink.LinkByName("gtppeer")
	if err != nil {
		t.Fatal(err)
	}

	addr, _ := netlink.ParseAddr("10.99.0.1/24")
	if err := netlink.AddrAdd(v, addr); err != nil {
		t.Fatal(err)
	}

	for _, l := range []netlink.Link{v, peer} {
		if err := netlink.LinkSetUp(l); err != nil {
			t.Fatal(err)
		}
	}

	if err := netlink.NeighSet(&netlink.Neigh{
		LinkIndex: v.Attrs().Index, IP: dpSGW.AsSlice(), HardwareAddr: peer.Attrs().HardwareAddr,
		State: netlink.NUD_PERMANENT, Family: netlink.FAMILY_V4,
	}); err != nil {
		t.Fatal(err)
	}

	return v, peer.Attrs().HardwareAddr
}

func capture(t *testing.T, ifname string) int {
	t.Helper()

	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW, int(htons(unix.ETH_P_ALL)))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = unix.Close(fd) })

	l, err := netlink.LinkByName(ifname)
	if err != nil {
		t.Fatal(err)
	}

	if err := unix.Bind(fd, &unix.SockaddrLinklayer{Protocol: htons(unix.ETH_P_ALL), Ifindex: l.Attrs().Index}); err != nil {
		t.Fatal(err)
	}

	tv := unix.NsecToTimeval(int64(2 * time.Second))
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv); err != nil {
		t.Fatal(err)
	}

	return fd
}

func htons(v uint16) uint16 {
	return v<<8 | v>>8
}

func TestDatapathL3IngressInterface(t *testing.T) {
	requirePrivileged(t)
	forwarding(t)

	tun := openTUN(t, "gtptun")
	core, peerMAC := vethPair(t)
	fd := capture(t, "gtppeer")

	dp := load(t)

	if err := dp.setInterface("gtptun", 1<<ranLeg); err != nil {
		t.Fatal(err)
	}

	if err := dp.setInterface("gtpcore", 1<<testLeg); err != nil {
		t.Fatal(err)
	}

	var iface relayIface

	l, _ := netlink.LinkByName("gtptun")
	if err := dp.objs.Ifaces.Lookup(uint32(l.Attrs().Index), &iface); err != nil || iface.Flags&ifaceL3 == 0 {
		t.Fatalf("tun interface = %+v, %v, want it marked L3", iface, err)
	}

	tunnel := &Tunnel{
		Local: dpRAN, Source: dpCore, Peer: Endpoint{Addr: dpSGW, TEID: 0x42}, outIndex: core.Attrs().Index, path: 2,
	}
	if err := dp.put(0x600, entryOf(tunnel, ranLeg, testLeg)); err != nil {
		t.Fatal(err)
	}

	packet := frame(dpRadio, dpRAN, sequencedGPDU(0x600), true)[14:]
	if _, err := tun.Write(packet); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 2048)

	for {
		n, _, err := unix.Recvfrom(fd, buf, 0)
		if err != nil {
			t.Fatalf("no relayed frame on the peer: %v", err)
		}

		out := buf[:n]
		if n < 14+20+8+8 || out[12] != 0x08 || out[14+9] != 17 {
			continue
		}

		checkRelayed4(t, out, dpCore, dpSGW, 0x42, peerMAC, true)

		if c, _, err := dp.tunnel(0x600); err != nil || c.Packets != 1 {
			t.Fatalf("tunnel counters = %+v, %v", c, err)
		}

		return
	}
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()

	for range 100 {
		if ok() {
			return
		}

		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for %s", what)
}

func TestDatapathFollowsRecreatedInterfaces(t *testing.T) {
	requirePrivileged(t)

	dp := load(t)

	first := dummy(t, "gtpflap", net.HardwareAddr{2, 0, 0, 0, 0, 5}, "10.95.0.1/24")
	if err := dp.setInterface("gtpflap", 1<<testLeg); err != nil {
		t.Fatal(err)
	}

	if err := netlink.LinkDel(&netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: "gtpflap"}}); err != nil {
		t.Fatal(err)
	}

	eventually(t, "the deleted interface to be forgotten", func() bool {
		_, ok := dp.interfaceLegs(first)

		var v relayIface

		return !ok && dp.objs.Ifaces.Lookup(uint32(first), &v) != nil
	})

	second := dummy(t, "gtpflap", net.HardwareAddr{2, 0, 0, 0, 0, 5}, "10.95.0.1/24")
	if second == first {
		t.Fatal("the recreated interface kept its index")
	}

	eventually(t, "the recreated interface to be attached", func() bool {
		legs, ok := dp.interfaceLegs(second)

		var v relayIface

		return ok && legs == 1<<testLeg && dp.objs.Ifaces.Lookup(uint32(second), &v) == nil && v.Legs == 1<<testLeg
	})

	if err := dp.setInterface("gtpflap", 0); err != nil {
		t.Fatal(err)
	}

	if _, ok := dp.interfaceLegs(second); ok {
		t.Fatal("interface with no legs still registered")
	}
}

func TestNetlinkHost(t *testing.T) {
	requirePrivileged(t)

	index := dummy(t, "gtpaddr", net.HardwareAddr{2, 0, 0, 0, 0, 6}, "10.94.0.1/24")
	h := NetlinkHost{}

	r, name, err := Resolve(h, netip.MustParseAddr("10.94.0.7"))
	if err != nil || r.Src != netip.MustParseAddr("10.94.0.1") || r.Ifindex != index || name != "gtpaddr" {
		t.Fatalf("route = %+v %q %v", r, name, err)
	}

	if i, err := h.InterfaceIndex("gtpaddr"); err != nil || i != index {
		t.Fatalf("index = %d, %v", i, err)
	}

	route(t, "10.93.0.0/24", unix.RTN_UNREACHABLE, 0)

	if _, err := h.Route(netip.MustParseAddr("10.93.0.1")); !errors.Is(err, ErrNoRoute) {
		t.Fatalf("route to an unreachable prefix = %v, want ErrNoRoute", err)
	}

	dp := load(t)
	if err := dp.setInterface("gtpmissing", 1); err == nil {
		t.Fatal("attached to a missing interface")
	}
}

func TestDatapathReportsRouteChanges(t *testing.T) {
	requirePrivileged(t)

	index := dummy(t, "gtproute", net.HardwareAddr{2, 0, 0, 0, 0, 7}, "10.92.0.1/24")
	dp := load(t)

	eventually(t, "the watcher to start", func() bool {
		select {
		case <-dp.changes():
			return true
		default:
			return false
		}
	})

	route(t, "10.91.0.0/24", unix.RTN_UNICAST, index)

	eventually(t, "a route change to be reported", func() bool {
		select {
		case <-dp.changes():
			return true
		default:
			return false
		}
	})
}

package gtpu

import (
	"errors"
	"net/netip"
	"slices"
	"testing"
	"time"
)

func TLA(a netip.Addr) []byte {
	return a.Unmap().AsSlice()
}

var (
	ranGW  = netip.MustParseAddr("10.1.0.1")
	coreGW = netip.MustParseAddr("10.2.0.1")
	radio  = netip.MustParseAddr("10.1.0.20")
	sgw    = netip.MustParseAddr("10.2.0.20")
	sgw6   = netip.MustParseAddr("2001:db8::20")
	ran6GW = netip.MustParseAddr("2001:db8:1::1")
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newTestPlane(t *testing.T, teids ...uint32) (*Plane, *clock) {
	t.Helper()

	h := newHarness(t)
	i := 0
	h.p.cfg.Rand = func() uint32 {
		if i < len(teids) {
			i++
			return teids[i-1]
		}

		i++

		return uint32(i)
	}

	return h.p, h.clock
}

func testPath(t *testing.T, p *Plane) Path {
	t.Helper()

	path, ok := p.Path(7, "eNB:1")
	if !ok {
		t.Fatal("no path")
	}

	return path
}

func TestOpenUplinkHandsOutRANEndpoint(t *testing.T) {
	p, _ := newTestPlane(t, 0x100)
	path := testPath(t, p)

	tun, err := p.Open(path, Core, TLA(sgw), 0xabc)
	if err != nil {
		t.Fatal(err)
	}

	if tun.TEID != 0x100 || tun.Local != ranGW || tun.Source != coreGW || tun.Ingress != RAN ||
		tun.Peer != (Endpoint{Addr: sgw, TEID: 0xabc}) {
		t.Fatalf("tunnel = %+v", tun)
	}
}

func TestOpenSelectsFamilyFromDualAddress(t *testing.T) {
	p, _ := newTestPlane(t)
	path := testPath(t, p)

	tla := append(TLA(sgw), TLA(sgw6)...)

	tun, err := p.Open(path, Core, tla, 1)
	if err != nil {
		t.Fatal(err)
	}

	if tun.Peer.Addr != sgw || !tun.Local.Is4() {
		t.Fatalf("tunnel = %+v, want the IPv4 address", tun)
	}

	if _, err := p.Open(path, Core, TLA(sgw6), 1); !errors.Is(err, ErrNoAddress) {
		t.Fatalf("IPv6-only peer with an IPv4-only radio side = %v, want ErrNoAddress", err)
	}
}

func TestOpenDerivesTheOtherFamilyFromTheRouteToThePeer(t *testing.T) {
	p, _ := newTestPlane(t)
	p.cfg.RANAddress = ran6GW
	path, _ := p.Path(7, "gNB:1")

	ul, err := p.Open(path, Core, append(TLA(sgw), TLA(sgw6)...), 1)
	if err != nil {
		t.Fatal(err)
	}

	if ul.Local != ran6GW || ul.Peer.Addr != sgw6 || ul.Source != core6GW {
		t.Fatalf("uplink = %+v, want IPv6 from the route to the S-GW", ul)
	}

	dl, err := p.Open(path, RAN, TLA(netip.MustParseAddr("2001:db8:1::20")), 2)
	if err != nil || dl.Local != core6GW || dl.Source != ran6GW {
		t.Fatalf("downlink = %+v, %v, want the derived IPv6 address toward the operator", dl, err)
	}

	other, _ := p.Path(7, "gNB:2")
	if _, err := p.Open(other, RAN, TLA(netip.MustParseAddr("2001:db8:1::21")), 3); !errors.Is(err, ErrNoAddress) {
		t.Fatalf("downlink before any IPv6 uplink = %v, want ErrNoAddress", err)
	}
}

func TestOperatorAddressIsTheSignallingSourceAddress(t *testing.T) {
	p, _ := newTestPlane(t)

	if st, _ := p.Status(7); st.State != StateReady || st.Interface != "core0" ||
		!slices.Equal(st.Addresses, []netip.Addr{coreGW}) {
		t.Fatalf("status = %+v", st)
	}

	tun, err := p.Open(testPath(t, p), Core, TLA(netip.MustParseAddr("10.2.9.9")), 1)
	if err != nil || tun.Source != coreGW || tun.outIndex != 20 || tun.inIndex != 10 {
		t.Fatalf("uplink = %+v, %v", tun, err)
	}
}

func TestReverseAndHairpin(t *testing.T) {
	p, _ := newTestPlane(t, 0x10, 0x20)
	path := testPath(t, p)

	dl, err := p.Open(path, RAN, TLA(radio), 0x55)
	if err != nil {
		t.Fatal(err)
	}

	if dl.Local != coreGW || dl.Ingress != Core {
		t.Fatalf("downlink tunnel = %+v", dl)
	}

	got, err := p.Reverse(Core, TLA(coreGW), dl.TEID)
	if err != nil || got != (Endpoint{Addr: radio, TEID: 0x55}) {
		t.Fatalf("Reverse = %+v, %v", got, err)
	}

	if _, err := p.Reverse(RAN, TLA(coreGW), dl.TEID); !errors.Is(err, ErrUnknownEndpoint) {
		t.Fatalf("Reverse from the wrong side = %v", err)
	}

	hp, err := p.Open(path, Core, TLA(coreGW), dl.TEID)
	if err != nil {
		t.Fatal(err)
	}

	if hp.Ingress != RAN || hp.Local != ranGW || hp.Source != ranGW || hp.Peer != dl.Peer {
		t.Fatalf("hairpin = %+v, want RAN to RAN toward %+v", hp, dl.Peer)
	}

	if _, err := p.Open(path, Core, TLA(coreGW), 0x9999); !errors.Is(err, ErrUnknownEndpoint) {
		t.Fatalf("own address with an unknown TEID = %v", err)
	}
}

func TestQuarantine(t *testing.T) {
	p, c := newTestPlane(t, 0x10, 0x10, 0x11, 0x10)
	path := testPath(t, p)

	a, err := p.Open(path, Core, TLA(sgw), 1)
	if err != nil {
		t.Fatal(err)
	}

	p.Close(a.TEID)

	if !p.quarantined(a.TEID) {
		t.Fatal("closed TEID not quarantined")
	}

	b, err := p.Open(path, Core, TLA(sgw), 1)
	if err != nil || b.TEID != 0x11 {
		t.Fatalf("reopen = %+v, %v, want the quarantined TEID skipped", b, err)
	}

	c.t = c.t.Add(defaultQuarantine)

	if p.quarantined(a.TEID) {
		t.Fatal("TEID still quarantined after the period")
	}

	d, err := p.Open(path, Core, TLA(sgw), 1)
	if err != nil || d.TEID != 0x10 {
		t.Fatalf("open after quarantine = %+v, %v", d, err)
	}
}

func TestNoTEIDZero(t *testing.T) {
	p, _ := newTestPlane(t, 0, 1<<28, 5)
	path := testPath(t, p)

	tun, err := p.Open(path, Core, TLA(sgw), 1)
	if err != nil || tun.TEID != 5 {
		t.Fatalf("tunnel = %+v, %v, want TEID 5", tun, err)
	}
}

func TestDirectOperatorHasNoPath(t *testing.T) {
	p, _ := newTestPlane(t)

	if err := p.SetOperators([]Operator{{ID: 7, Name: "op"}}); err != nil {
		t.Fatal(err)
	}

	if _, ok := p.Path(7, "eNB:1"); ok {
		t.Fatal("direct operator has a path")
	}

	if st, _ := p.Status(7); st.State != StateOff {
		t.Fatalf("status = %+v, want off", st)
	}
}

func TestSessionLifecycle(t *testing.T) {
	p, _ := newTestPlane(t)
	path := testPath(t, p)

	var s Session

	ul := Binding{Path: path, Proc: 9, Bearer: 5, Slot: Slot{Kind: KindUL}, Pending: true}

	e, err := p.Bind(&s, ul, Core, TLA(sgw), 0x1)
	if err != nil {
		t.Fatal(err)
	}

	if again, err := p.Bind(&s, ul, Core, TLA(sgw), 0x1); err != nil || again != e {
		t.Fatalf("rebinding the same endpoint = %+v, %v, want %+v", again, err, e)
	}

	if _, err := p.Bind(&s, Binding{Path: path, Proc: 9, Bearer: 6, Slot: Slot{Kind: KindUL}, Pending: true}, Core,
		TLA(sgw), 0x2); err != nil {
		t.Fatal(err)
	}

	p.Settle(&s, 9, func(b int) bool { return b == 5 })

	if s.Len() != 1 || p.Tunnels() != 1 {
		t.Fatalf("after settle: %d refs, %d tunnels, want 1 and 1", s.Len(), p.Tunnels())
	}

	if _, err := p.Bind(&s, Binding{Path: path, Bearer: 5, Slot: Slot{Kind: KindUL}}, Core, TLA(sgw), 0x3); err != nil {
		t.Fatal(err)
	}

	if _, err := p.Bind(&s, Binding{Path: path, Bearer: 5, Slot: Slot{Kind: KindUL}}, Core, TLA(sgw), 0x4); err != nil {
		t.Fatal(err)
	}

	if s.Len() != 2 {
		t.Fatalf("refs = %+v, want the current and one superseded tunnel", s.Refs())
	}

	if _, err := p.Bind(&s, Binding{Path: path, Bearer: 5, Slot: Slot{Kind: KindForwardDL}}, Core, TLA(sgw), 0x9); err != nil {
		t.Fatal(err)
	}

	p.Release(&s, func(r Ref) bool { return !r.Slot.Kind.Forwarding() })

	if s.Len() != 2 {
		t.Fatalf("refs after releasing forwarding = %d", s.Len())
	}

	var other Session

	p.Adopt(&s, &other, path, func(r Ref) bool { return r.Slot.Kind.Uplink() && !r.Superseded })

	if s.Len() != 1 || other.Len() != 1 {
		t.Fatalf("adopt: %d and %d refs", s.Len(), other.Len())
	}

	p.ReleaseAll(&s)
	p.ReleaseBearers(&other, 5)

	if p.Tunnels() != 0 {
		t.Fatalf("%d tunnels left", p.Tunnels())
	}
}

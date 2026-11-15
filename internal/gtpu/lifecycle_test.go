package gtpu

import (
	"errors"
	"net/netip"
	"slices"
	"testing"
)

func TestLifecycleSymmetry(t *testing.T) {
	h := newHarness(t)
	path := h.path(t, 7)

	var s Session

	steps := []func(){
		func() {
			_, _ = h.p.Bind(&s, Binding{Path: path, Proc: 1, Bearer: 5, Slot: Slot{Kind: KindUL}, Pending: true}, Core,
				TLA(sgw), 1)
			_, _ = h.p.Bind(&s, Binding{Path: path, Proc: 1, Bearer: 6, Slot: Slot{Kind: KindUL}, Pending: true}, Core,
				TLA(sgw), 2)
		},
		func() { h.p.Settle(&s, 1, func(b int) bool { return b == 5 }) },
		func() {
			_, _ = h.p.Bind(&s, Binding{Path: path, Bearer: 5, Slot: Slot{Kind: KindDL}}, RAN, TLA(radio), 3)
		},
		func() {
			_, _ = h.p.Bind(&s, Binding{Path: path, Bearer: 5, Slot: Slot{Kind: KindUL}}, Core, TLA(sgw), 4)
			_, _ = h.p.Bind(&s, Binding{Path: path, Bearer: 5, Slot: Slot{Kind: KindUL}}, Core, TLA(sgw), 5)
		},
		func() {
			_, _ = h.p.Bind(&s, Binding{Path: path, Bearer: 5, Slot: Slot{Kind: KindForwardDL}}, Core, TLA(sgw), 6)
		},
		func() { h.p.Release(&s, func(r Ref) bool { return !r.Slot.Kind.Forwarding() }) },
		func() { h.p.ReleaseBearers(&s, 6) },
		func() { h.p.ReleaseAll(&s) },
	}

	for i, step := range steps {
		step()
		h.symmetric(t)

		refs := map[uint32]bool{}
		for _, r := range s.Refs() {
			refs[r.TEID] = true
		}

		if len(refs) != h.p.Tunnels() {
			t.Fatalf("step %d: %d refs, %d tunnels", i, len(refs), h.p.Tunnels())
		}
	}

	if h.p.Tunnels() != 0 || len(h.dp.tunnels) != 0 {
		t.Fatalf("%d tunnels left", h.p.Tunnels())
	}

	for k, e := range h.p.paths {
		if e.refs != 0 {
			t.Fatalf("path %+v still has %d references", k, e.refs)
		}
	}
}

func TestOpenRegistersBothLegsOnTheirInterfaces(t *testing.T) {
	h := newHarness(t)

	ul := h.uplink(t)
	leg := h.p.legs[7]

	e := h.dp.tunnels[ul.TEID]
	if e.Leg != ranLeg || e.OutIfindex != 20 || e.Op != leg {
		t.Fatalf("uplink entry = leg %d, egress %d, operator %d", e.Leg, e.OutIfindex, e.Op)
	}

	if h.dp.ifaces["ran0"] != 1<<ranLeg || h.dp.ifaces["core0"] != 1<<leg {
		t.Fatalf("interfaces = %v", h.dp.ifaces)
	}

	dl, err := h.p.Open(h.path(t, 7), RAN, TLA(radio), 9)
	if err != nil {
		t.Fatal(err)
	}

	if e := h.dp.tunnels[dl.TEID]; e.Leg != leg || e.OutIfindex != 10 {
		t.Fatalf("downlink entry = leg %d, egress %d", e.Leg, e.OutIfindex)
	}
}

func TestOperatorRemovalFreesItsLeg(t *testing.T) {
	a := Operator{ID: 7, Name: "a", Aggregated: true}
	b := Operator{ID: 8, Name: "b", Aggregated: true}

	h := newHarness(t, a, b)
	h.anchor(t, 7, mme)
	h.anchor(t, 8, mmeB)
	legA := h.p.legs[7]

	if _, err := h.p.Open(h.path(t, 7), Core, TLA(sgw), 1); err != nil {
		t.Fatal(err)
	}

	if err := h.p.SetOperators([]Operator{b}); err != nil {
		t.Fatal(err)
	}

	h.symmetric(t)

	if h.p.Tunnels() != 0 {
		t.Fatal("removed operator's tunnels kept")
	}

	if _, ok := h.dp.ifaces["core0"]; ok {
		t.Fatalf("removed operator's interface still registered: %v", h.dp.ifaces)
	}

	if len(h.dp.resets) != 1 || h.dp.resets[0] != legA {
		t.Fatalf("drop counters reset for legs %v, want %d", h.dp.resets, legA)
	}

	if _, ok := h.p.Status(7); ok {
		t.Fatal("removed operator still has a user-plane status")
	}

	c := Operator{ID: 9, Name: "c", Aggregated: true}
	if err := h.p.SetOperators([]Operator{b, c}); err != nil {
		t.Fatal(err)
	}

	h.anchor(t, 9, mme)

	if h.p.legs[9] != legA || h.dp.ifaces["core0"] != 1<<legA || h.dp.ifaces["core1"] != 1<<h.p.legs[8] {
		t.Fatalf("legs = %v, interfaces = %v", h.p.legs, h.dp.ifaces)
	}
}

func TestReconnectingThroughAnotherInterfaceMovesTheLeg(t *testing.T) {
	h := newHarness(t)
	leg := h.p.legs[7]

	h.p.Unanchor(7, mme)

	if _, ok := h.dp.ifaces["core0"]; ok {
		t.Fatalf("leg kept on core0 without an association: %v", h.dp.ifaces)
	}

	h.anchor(t, 7, mmeB)

	if _, ok := h.dp.ifaces["core0"]; ok || h.dp.ifaces["core1"] != 1<<leg {
		t.Fatalf("interfaces = %v", h.dp.ifaces)
	}

	if st, _ := h.p.Status(7); st.State != StateReady || st.Interface != "core1" ||
		!slices.Equal(st.Addresses, []netip.Addr{coreB}) {
		t.Fatalf("status = %+v", st)
	}
}

func TestHairpinToAnotherOperatorChainsThroughTheGateway(t *testing.T) {
	h := newHarness(t,
		Operator{ID: 7, Name: "a", Aggregated: true},
		Operator{ID: 8, Name: "b", Aggregated: true})
	h.anchor(t, 7, mme)
	h.anchor(t, 8, mmeB)

	dl, err := h.p.Open(h.path(t, 8), RAN, TLA(radio), 0x55)
	if err != nil {
		t.Fatal(err)
	}

	chained, err := h.p.Open(h.path(t, 7), Core, TLA(coreB), dl.TEID)
	if err != nil {
		t.Fatalf("hairpin to another operator's tunnel = %v", err)
	}

	if chained.Ingress != RAN || chained.Egress != Core || chained.Source != coreGW ||
		chained.Peer != (Endpoint{Addr: coreB, TEID: dl.TEID}) {
		t.Fatalf("chained tunnel = %+v", chained)
	}

	if peers := h.p.peersInUse(); len(peers) != 1 {
		t.Fatalf("Echo peers = %v, want only the radio", peers)
	}

	if _, err := h.p.Open(h.path(t, 7), Core, TLA(coreB), 0x9999); !errors.Is(err, ErrUnknownEndpoint) {
		t.Fatalf("own address with an unknown TEID = %v", err)
	}
}

func TestSignallingAddressesAreOwn(t *testing.T) {
	h := newHarness(t)
	path := h.path(t, 7)

	dl, err := h.p.Open(path, RAN, TLA(radio), 1)
	if err != nil || dl.Local != coreGW {
		t.Fatalf("downlink = %+v, %v", dl, err)
	}

	if _, err := h.p.Open(path, Core, TLA(coreGW), 0x7777); !errors.Is(err, ErrUnknownEndpoint) {
		t.Fatalf("signalling address with an unknown TEID = %v, want ErrUnknownEndpoint", err)
	}
}

func TestPathsAreReclaimed(t *testing.T) {
	h := newHarness(t)

	tun := h.uplink(t)
	idx := tun.path
	h.dp.pathStats[idx] = Counters{Packets: 5, Bytes: 500}
	h.p.Close(tun.TEID)

	h.p.nextPath = maxPaths

	other, _ := h.p.Path(7, "eNB:2")

	if _, err := h.p.Open(other, Core, TLA(sgw), 1); !errors.Is(err, ErrTooManyPaths) {
		t.Fatalf("open during the path quarantine = %v", err)
	}

	h.clock.t = h.clock.t.Add(defaultQuarantine)

	reused, err := h.p.Open(other, Core, TLA(sgw), 1)
	if err != nil || reused.path != idx {
		t.Fatalf("open after the quarantine = %+v, %v, want path %d reused", reused, err, idx)
	}

	wantSample(t, h.p, "ellamocn_user_plane_packets_total",
		map[string]string{"operator": "op", "radio": "eNB:1", "direction": "uplink"}, 5)
	wantSample(t, h.p, "ellamocn_user_plane_packets_total",
		map[string]string{"operator": "op", "radio": "eNB:2", "direction": "uplink"}, 0)
}

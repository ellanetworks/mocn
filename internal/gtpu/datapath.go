package gtpu

import (
	"encoding/binary"
	"hash/fnv"
	"net/netip"
)

const (
	familyIPv4 = uint8(relayFamilyFAMILY_IPV4)
	familyIPv6 = uint8(relayFamilyFAMILY_IPV6)

	ifaceL3   = uint32(relayIfaceFlagIFACE_L3)
	ifaceXFRM = uint32(relayIfaceFlagIFACE_XFRM)

	configPolicyIPsec = uint32(relayConfigFlagCONFIG_POLICY_IPSEC)

	maxLegs    = int(relayLimitsMAX_LEGS)
	maxIfaces  = int(relayLimitsMAX_IFACES)
	dropCount  = int(relayDropReasonDROP_REASONS)
	passCount  = int(relayPassReasonPASS_REASONS)
	loopbackIf = 1

	ephemeralPorts = 49152
)

var dropReasons = map[relayDropReason]string{
	relayDropReasonDROP_WRONG_LEG:       "wrong_interface",
	relayDropReasonDROP_WRONG_EGRESS:    "wrong_egress_interface",
	relayDropReasonDROP_FIB_BLACKHOLE:   "fib_blackhole",
	relayDropReasonDROP_FIB_UNREACHABLE: "fib_unreachable",
	relayDropReasonDROP_FIB_PROHIBIT:    "fib_prohibit",
	relayDropReasonDROP_REWRITE_FAILED:  "rewrite_failed",
}

var passReasons = map[relayPassReason]string{
	relayPassReasonPASS_NO_TUNNEL:     "no_tunnel",
	relayPassReasonPASS_NOT_LOCAL:     "not_local",
	relayPassReasonPASS_IP_FRAGMENT:   "ip_fragment",
	relayPassReasonPASS_EXT_HEADER:    "extension_header",
	relayPassReasonPASS_ZERO_CHECKSUM: "zero_checksum",
	relayPassReasonPASS_MERGED:        "merged",
	relayPassReasonPASS_FRAG_NEEDED:   "frag_needed",
	relayPassReasonPASS_FWD_DISABLED:  "forwarding_disabled",
	relayPassReasonPASS_IPSEC_POLICY:  "ipsec_policy",
	relayPassReasonPASS_FIB_OTHER:     "fib_other",
}

type mapUsage struct {
	maxEntries uint32
	memlock    uint64
}

type datapath interface {
	setInterface(name string, legs uint64) error
	interfaceLegs(index int) (uint64, bool)
	changes() <-chan struct{}
	put(teid uint32, e relayTunnel) error
	remove(teid uint32) error
	setPath(teid, idx uint32) error
	setPolicyIPsec(on bool) error
	tunnel(teid uint32) (Counters, netip.Addr, error)
	paths() (map[uint32]Counters, error)
	resetPath(idx uint32) error
	drops() ([][]uint64, error)
	resetLeg(leg uint8) error
	passes() ([]uint64, error)
	mapUsage() map[string]mapUsage
	close() error
}

type noDatapath struct{}

func (noDatapath) setInterface(string, uint64) error { return nil }
func (noDatapath) interfaceLegs(int) (uint64, bool)  { return 0, false }
func (noDatapath) changes() <-chan struct{}          { return nil }
func (noDatapath) put(uint32, relayTunnel) error     { return nil }
func (noDatapath) remove(uint32) error               { return nil }
func (noDatapath) setPath(uint32, uint32) error      { return nil }
func (noDatapath) setPolicyIPsec(bool) error         { return nil }
func (noDatapath) tunnel(uint32) (Counters, netip.Addr, error) {
	return Counters{}, netip.Addr{}, nil
}
func (noDatapath) paths() (map[uint32]Counters, error) { return nil, nil }
func (noDatapath) resetPath(uint32) error              { return nil }
func (noDatapath) drops() ([][]uint64, error)          { return nil, nil }
func (noDatapath) resetLeg(uint8) error                { return nil }
func (noDatapath) passes() ([]uint64, error)           { return nil, nil }
func (noDatapath) mapUsage() map[string]mapUsage       { return nil }
func (noDatapath) close() error                        { return nil }

// TS 29.281 clause 4.4.2.0
// TS 29.281 clause 4.4.2.6
func sourcePort(teid uint32) uint16 {
	h := fnv.New32a()

	var b [4]byte

	binary.BigEndian.PutUint32(b[:], teid)
	_, _ = h.Write(b[:])

	return ephemeralPorts + uint16(h.Sum32()%(1<<14))
}

func addr16(a netip.Addr) [16]uint8 {
	var out [16]uint8

	a = a.Unmap()
	if a.Is4() {
		v := a.As4()
		copy(out[:], v[:])

		return out
	}

	return a.As16()
}

func addrOf16(b [16]byte, v4 bool) netip.Addr {
	if b == ([16]byte{}) {
		return netip.Addr{}
	}

	if v4 {
		return netip.AddrFrom4([4]byte(b[:4]))
	}

	return netip.AddrFrom16(b)
}

func entryOf(t *Tunnel, leg, op uint8) relayTunnel {
	e := relayTunnel{
		Local:      addr16(t.Local),
		Src:        addr16(t.Source),
		Dst:        addr16(t.Peer.Addr),
		Path:       t.path,
		OutIfindex: uint32(t.outIndex),
		Family:     familyIPv6,
		Leg:        leg,
		Op:         op,
	}

	if t.Local.Is4() {
		e.Family = familyIPv4
	}

	var b [4]byte

	binary.BigEndian.PutUint32(b[:], t.Peer.TEID)
	e.Teid = binary.NativeEndian.Uint32(b[:])

	var s [2]byte

	binary.BigEndian.PutUint16(s[:], sourcePort(t.Peer.TEID))
	e.Sport = binary.NativeEndian.Uint16(s[:])

	return e
}

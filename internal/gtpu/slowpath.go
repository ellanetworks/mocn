package gtpu

import (
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/net/bpf"
	"golang.org/x/net/ipv4"
	"golang.org/x/sys/unix"
)

const (
	echoInterval       = 60 * time.Second
	echoMissesForDown  = 3
	errorIndicationsPS = 100
	maxDatagram        = 65535
	oobSize            = 128
)

var supportedExtensionHeaders = []byte{0x03, 0x04, 0x20, 0x40, 0x81, 0x82, 0x83, 0x84, 0x85, 0x86, 0xc0}

type inbound struct {
	local   netip.Addr
	from    netip.AddrPort
	ifindex int
	tos     uint8
	b       []byte
}

type sender interface {
	udp(local netip.Addr, ifindex int, to netip.AddrPort, msg []byte) error
	raw(src, dst netip.Addr, tos uint8, ifindex int, udp []byte) error
	listen(local netip.Addr, deliver func(inbound)) error
	close()
}

type peerState struct {
	source  netip.Addr
	seq     uint16
	pending bool
	misses  int
}

type slowPath struct {
	p   *Plane
	log *slog.Logger
	out sender

	mu     sync.Mutex
	peers  map[netip.Addr]*peerState
	tokens int
	refill time.Time
}

func newSlowPath(p *Plane, out sender) *slowPath {
	if out == nil {
		out = &sockets{conns: map[netip.Addr]*net.UDPConn{}}
	}

	return &slowPath{p: p, log: p.log, out: out, peers: map[netip.Addr]*peerState{}}
}

func (s *slowPath) enabled() bool {
	return s.p.cfg.Enabled
}

func (s *slowPath) listen(a netip.Addr) error {
	if !s.enabled() {
		return nil
	}

	return s.out.listen(a, s.handle)
}

func (s *slowPath) close() {
	s.out.close()
}

func (s *slowPath) handle(in inbound) {
	h, err := parseHeader(in.b)
	if err != nil {
		s.p.stats.unattributedDrop(dropMalformed)
		return
	}

	switch h.msg {
	case msgEchoRequest:
		if !s.p.knownInterface(in.ifindex) {
			s.p.stats.unattributedDrop(dropWrongInterface)
			return
		}

		_ = s.out.udp(in.local, in.ifindex, in.from, echoResponse(h.seq))
	case msgEchoResponse:
		s.echoResponse(in.from.Addr().Unmap())
	case msgErrorIndication:
		s.errorIndication(in, h)
	case msgSupportedExtensionHeadersNotif:
		s.p.stats.unattributedDrop(dropExtensionNotif)
		s.log.Warn("Supported Extension Headers Notification received", slog.String("peer", in.from.Addr().String()))
	case msgGPDU, msgEndMarker, msgTunnelStatus:
		s.relay(in, h)
	default:
		s.p.stats.unattributedDrop(dropUnknownMessage)
	}
}

// TS 29.281 clause 4.4.2.4
// TS 29.281 clause 4.4.3.4
func (s *slowPath) send(local netip.Addr, ifindex int, to netip.Addr, msg []byte) {
	_ = s.out.udp(local, ifindex, netip.AddrPortFrom(to, Port), msg)
}

func (s *slowPath) allowErrorIndication() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.p.cfg.Now()
	if now.Sub(s.refill) >= time.Second {
		s.tokens, s.refill = errorIndicationsPS, now
	}

	if s.tokens == 0 {
		return false
	}

	s.tokens--

	return true
}

// TS 29.281 clause 7.3.1
// TS 29.281 clause 7.3.2.1
func (s *slowPath) unknownTunnel(in inbound, h header) {
	reason := dropNoTunnel
	if s.p.quarantined(h.teid) {
		reason = dropQuarantined
	}

	s.p.stats.unattributedDrop(reason)

	if h.msg != msgGPDU || h.teid == 0 || !s.p.knownInterface(in.ifindex) {
		return
	}

	if !s.allowErrorIndication() {
		s.p.stats.errorIndication(originGenerated, resultRateLimited)
		return
	}

	s.send(in.local, in.ifindex, in.from.Addr().Unmap(), errorIndication(h.teid, in.local))
	s.p.stats.errorIndication(originGenerated, resultSent)
}

// TS 29.281 clause 4.4.1
// TS 29.281 clause 5.2.1
// TS 29.281 clause 7.3.3
func (s *slowPath) relay(in inbound, h header) {
	t, ok := s.p.Get(h.teid)
	if !ok || t.Local != in.local {
		s.unknownTunnel(in, h)
		return
	}

	from := in.from.Addr().Unmap()

	if !s.p.arrivedOn(t, t.Ingress, in.ifindex, from) {
		s.p.stats.slowDrop(t.Operator, dropWrongInterface)
		return
	}

	exts, payload, err := extensions(in.b, h)
	if err != nil {
		s.p.stats.slowDrop(t.Operator, dropMalformed)
		return
	}

	unknown, strip := relayable(exts)
	if len(unknown) > 0 {
		s.p.stats.slowDrop(t.Operator, dropUnsupportedExtension)
		s.log.Error("dropped a GTP-U message with an extension header that must be comprehended",
			slog.Any("types", unknown), slog.String("peer", from.String()))

		if h.msg == msgGPDU {
			s.send(in.local, in.ifindex, from, supportedExtensionHeadersNotification(supportedExtensionHeaders))
		}

		return
	}

	out := append([]byte(nil), in.b[:h.length]...)
	if strip {
		out = withoutDiscarded(in.b, h, exts, payload)
	}

	binary.BigEndian.PutUint32(out[4:8], t.Peer.TEID)

	udp := make([]byte, 8+len(out))
	binary.BigEndian.PutUint16(udp[0:2], sourcePort(t.Peer.TEID))
	binary.BigEndian.PutUint16(udp[2:4], Port)
	binary.BigEndian.PutUint16(udp[4:6], uint16(len(udp)))
	copy(udp[8:], out)

	if t.Peer.Addr.Is4() {
		binary.BigEndian.PutUint16(udp[6:8], udpChecksum4(t.Source, t.Peer.Addr, udp))
	}

	egress, ok := s.p.egressIndex(t)
	if !ok {
		s.p.stats.slowDrop(t.Operator, dropNoEgressInterface)
		return
	}

	if egress != 0 {
		if r, err := s.p.host.Route(t.Peer.Addr); err != nil || r.Ifindex != egress {
			s.p.stats.slowDrop(t.Operator, dropWrongEgressInterface)
			return
		}
	}

	if err := s.out.raw(t.Source, t.Peer.Addr, in.tos, egress, udp); err != nil {
		s.p.stats.slowDrop(t.Operator, dropSendFailed)
		s.log.Debug("failed to relay a GTP-U message", slog.Any("error", err))

		return
	}

	s.p.slowRelayed(t.TEID, from, h.msg == msgGPDU, h.length-payload)
}

func udpChecksum4(src, dst netip.Addr, udp []byte) uint16 {
	s4, d4 := src.As4(), dst.As4()

	var sum uint32

	add := func(b []byte) {
		for i := 0; i+1 < len(b); i += 2 {
			sum += uint32(b[i])<<8 | uint32(b[i+1])
		}

		if len(b)%2 == 1 {
			sum += uint32(b[len(b)-1]) << 8
		}
	}

	add(s4[:])
	add(d4[:])

	sum += unix.IPPROTO_UDP + uint32(len(udp))
	add(udp)

	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}

	c := ^uint16(sum)
	if c == 0 {
		c = 0xffff
	}

	return c
}

// TS 29.281 clause 7.3.1
func (s *slowPath) errorIndication(in inbound, h header) {
	teid, peer, err := parseErrorIndication(in.b, h)
	if err != nil {
		s.p.stats.errorIndication(originReceived, resultMalformed)
		return
	}

	result := resultNoTunnel

	for _, t := range s.p.byPeerEndpoint(Endpoint{Addr: peer, TEID: teid}) {
		if t.Source != in.local || !s.p.arrivedOn(t, t.Egress, in.ifindex, in.from.Addr().Unmap()) {
			continue
		}

		to, ok := s.p.returnAddress(t.TEID)
		if !ok {
			if result == resultNoTunnel {
				result = resultNoReturnAddress
			}

			continue
		}

		result = resultTranslated

		s.send(t.Local, t.inIndex, to, errorIndication(t.TEID, t.Local))
		s.log.Info("GTP-U Error Indication relayed", slog.String("peer", peer.String()),
			slog.Uint64("peer_teid", uint64(teid)), slog.Uint64("teid", uint64(t.TEID)))
	}

	s.p.stats.errorIndication(originReceived, result)
}

// TS 29.281 clause 7.2.1
func (s *slowPath) echoLoop(ctx context.Context) {
	if !s.enabled() {
		return
	}

	t := time.NewTicker(echoInterval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}

		s.echo()
	}
}

func (s *slowPath) echo() {
	inUse := s.p.peersInUse()

	for peer, via := range inUse {
		s.mu.Lock()

		st := s.peers[peer]
		if st == nil {
			st = &peerState{}
			s.peers[peer] = st
		}

		if st.pending {
			st.misses++
		}

		st.source, st.pending = via.source, true
		st.seq++
		seq := st.seq
		s.mu.Unlock()

		s.send(via.source, via.index, peer, echoRequest(seq))
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for peer := range s.peers {
		if _, ok := inUse[peer]; !ok {
			delete(s.peers, peer)
		}
	}
}

func (s *slowPath) echoResponse(peer netip.Addr) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if st := s.peers[peer]; st != nil {
		st.pending, st.misses = false, 0
	}
}

func (s *slowPath) up(peer netip.Addr) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	st := s.peers[peer]

	return st == nil || st.misses < echoMissesForDown
}

type sockets struct {
	mu     sync.Mutex
	conns  map[netip.Addr]*net.UDPConn
	raw4   *net.IPConn
	raw6   *net.IPConn
	closed bool
}

func (k *sockets) listen(a netip.Addr, deliver func(inbound)) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.closed {
		return net.ErrClosed
	}

	if _, ok := k.conns[a]; ok {
		return nil
	}

	c, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(netip.AddrPortFrom(a, Port)))
	if err != nil {
		return err
	}

	opts := [][2]int{{unix.IPPROTO_IP, unix.IP_PKTINFO}, {unix.IPPROTO_IP, unix.IP_RECVTOS}}
	if a.Is6() {
		opts = [][2]int{{unix.IPPROTO_IPV6, unix.IPV6_RECVPKTINFO}, {unix.IPPROTO_IPV6, unix.IPV6_RECVTCLASS}}
	}

	for _, o := range opts {
		if err := setsockopt(c, o[0], o[1], 1); err != nil {
			_ = c.Close()
			return err
		}
	}

	k.conns[a] = c

	go read(a, c, deliver)

	return nil
}

func read(local netip.Addr, c *net.UDPConn, deliver func(inbound)) {
	buf := make([]byte, maxDatagram)
	oob := make([]byte, oobSize)

	for {
		n, oobn, _, from, err := c.ReadMsgUDPAddrPort(buf, oob)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}

			continue
		}

		in := inbound{local: local, from: from, b: buf[:n]}
		parseControl(oob[:oobn], &in)
		deliver(in)
	}
}

func parseControl(oob []byte, in *inbound) {
	msgs, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return
	}

	for _, m := range msgs {
		switch {
		case m.Header.Level == unix.IPPROTO_IP && m.Header.Type == unix.IP_PKTINFO &&
			len(m.Data) >= int(unsafe.Sizeof(unix.Inet4Pktinfo{})):
			in.ifindex = int(binary.NativeEndian.Uint32(m.Data[0:4]))
		case m.Header.Level == unix.IPPROTO_IP && m.Header.Type == unix.IP_TOS && len(m.Data) >= 1:
			in.tos = m.Data[0]
		case m.Header.Level == unix.IPPROTO_IPV6 && m.Header.Type == unix.IPV6_PKTINFO &&
			len(m.Data) >= int(unsafe.Sizeof(unix.Inet6Pktinfo{})):
			in.ifindex = int(binary.NativeEndian.Uint32(m.Data[16:20]))
		case m.Header.Level == unix.IPPROTO_IPV6 && m.Header.Type == unix.IPV6_TCLASS && len(m.Data) >= 4:
			in.tos = uint8(binary.NativeEndian.Uint32(m.Data[0:4]))
		}
	}
}

func setsockopt(c syscallConn, level, opt, value int) error {
	rc, err := c.SyscallConn()
	if err != nil {
		return err
	}

	var serr error

	if err := rc.Control(func(fd uintptr) {
		serr = unix.SetsockoptInt(int(fd), level, opt, value)
	}); err != nil {
		return err
	}

	return serr
}

type syscallConn interface {
	SyscallConn() (syscall.RawConn, error)
}

func (k *sockets) udp(local netip.Addr, ifindex int, to netip.AddrPort, msg []byte) error {
	k.mu.Lock()
	c := k.conns[local]
	k.mu.Unlock()

	if c == nil {
		return net.ErrClosed
	}

	_, _, err := c.WriteMsgUDPAddrPort(msg, pktinfo(local, ifindex), to)

	return err
}

func pktinfo(src netip.Addr, ifindex int) []byte {
	if src.Is4() {
		return unix.PktInfo4(&unix.Inet4Pktinfo{Ifindex: int32(ifindex), Spec_dst: src.As4()})
	}

	return unix.PktInfo6(&unix.Inet6Pktinfo{Addr: src.As16(), Ifindex: uint32(ifindex)})
}

func rejectAll() []bpf.RawInstruction {
	raw, _ := bpf.Assemble([]bpf.Instruction{bpf.RetConstant{Val: 0}})

	return raw
}

func (k *sockets) rawConn(v4 bool) (*net.IPConn, error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.closed {
		return nil, net.ErrClosed
	}

	if v4 && k.raw4 != nil {
		return k.raw4, nil
	}

	if !v4 && k.raw6 != nil {
		return k.raw6, nil
	}

	network := "ip6:udp"
	if v4 {
		network = "ip4:udp"
	}

	c, err := net.ListenIP(network, nil)
	if err != nil {
		return nil, err
	}

	if err := ipv4.NewPacketConn(c).SetBPF(rejectAll()); err != nil {
		_ = c.Close()
		return nil, err
	}

	opt := [3]int{unix.IPPROTO_IP, unix.IP_MTU_DISCOVER, unix.IP_PMTUDISC_DONT}
	if !v4 {
		opt = [3]int{unix.IPPROTO_IPV6, unix.IPV6_CHECKSUM, 6}
	}

	if err := setsockopt(c, opt[0], opt[1], opt[2]); err != nil {
		_ = c.Close()
		return nil, err
	}

	if v4 {
		k.raw4 = c
	} else {
		k.raw6 = c
	}

	return c, nil
}

func cmsgInt(level, typ int32, v int32) []byte {
	b := make([]byte, unix.CmsgSpace(4))
	h := (*unix.Cmsghdr)(unsafe.Pointer(&b[0])) // #nosec G103
	h.Level, h.Type = level, typ
	h.SetLen(unix.CmsgLen(4))
	binary.NativeEndian.PutUint32(b[unix.CmsgLen(0):], uint32(v))

	return b
}

// TS 29.281 clause 4.4.1
func (k *sockets) raw(src, dst netip.Addr, tos uint8, ifindex int, udp []byte) error {
	c, err := k.rawConn(dst.Is4())
	if err != nil {
		return err
	}

	oob := pktinfo(src, ifindex)

	if dst.Is4() {
		oob = append(oob, cmsgInt(unix.IPPROTO_IP, unix.IP_TOS, int32(tos))...)
	} else {
		oob = append(oob, cmsgInt(unix.IPPROTO_IPV6, unix.IPV6_TCLASS, int32(tos))...)
	}

	_, _, err = c.WriteMsgIP(udp, oob, &net.IPAddr{IP: dst.AsSlice()})

	return err
}

func (k *sockets) close() {
	k.mu.Lock()
	defer k.mu.Unlock()

	k.closed = true

	for a, c := range k.conns {
		_ = c.Close()

		delete(k.conns, a)
	}

	for _, c := range []*net.IPConn{k.raw4, k.raw6} {
		if c != nil {
			_ = c.Close()
		}
	}
}

package fake

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
	"slices"
	"sync"
	"time"

	"github.com/ellanetworks/mocn/perf/packet"
)

const (
	probeMagic   = 0x6d6f636e70657266
	probeLen     = 16
	probeTimeout = time.Second
)

var errNoSocket = errors.New("no GTP-U socket for the address")

type gtpuMessage struct {
	msg  packet.Message
	raw  []byte
	from netip.AddrPort
}

func listenGTPU(a netip.Addr) (*net.UDPConn, error) {
	return net.ListenUDP("udp", net.UDPAddrFromAddrPort(netip.AddrPortFrom(a, packet.Port)))
}

func serve(c *net.UDPConn, fn func(gtpuMessage)) {
	buf := make([]byte, 65535)

	for {
		n, from, err := c.ReadFromUDPAddrPort(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}

			continue
		}

		m, err := packet.Parse(buf[:n])
		if err != nil {
			continue
		}

		if m.EchoRequest() {
			_, _ = c.WriteToUDPAddrPort(packet.EchoResponse(m), from)
			continue
		}

		fn(gtpuMessage{msg: m, raw: buf[:n], from: from})
	}
}

func Reflect(c *net.UDPConn, lookup func(teid uint32) (Session, bool)) {
	serve(c, func(m gtpuMessage) {
		if !m.msg.GPDU() {
			return
		}

		to := m.from

		if s, ok := lookup(m.msg.TEID); ok {
			to = netip.AddrPortFrom(s.Peer, packet.Port)
			packet.Rewrite(m.raw, s.PeerTEID)
		}

		_, _ = c.WriteToUDPAddrPort(m.raw, to)
	})
}

type pinger struct {
	mu      sync.Mutex
	waiting map[uint64]chan time.Time
}

func newPinger() *pinger {
	return &pinger{waiting: map[uint64]chan time.Time{}}
}

func (p *pinger) receive(inner []byte) {
	at := time.Now()
	payload := packet.InnerPayload(inner)

	if len(payload) < probeLen || binary.BigEndian.Uint64(payload) != probeMagic {
		return
	}

	seq := binary.BigEndian.Uint64(payload[8:])

	p.mu.Lock()
	ch, ok := p.waiting[seq]
	p.mu.Unlock()

	if ok {
		select {
		case ch <- at:
		default:
		}
	}
}

func (p *pinger) expect(seq uint64) chan time.Time {
	ch := make(chan time.Time, 1)

	p.mu.Lock()
	p.waiting[seq] = ch
	p.mu.Unlock()

	return ch
}

func (p *pinger) forget(seq uint64) {
	p.mu.Lock()
	delete(p.waiting, seq)
	p.mu.Unlock()
}

type PingConfig struct {
	From     netip.Addr
	To       Endpoint
	Count    int
	Interval time.Duration
	Size     int
}

type Latency struct {
	Sent     int     `json:"sent"`
	Received int     `json:"received"`
	MinUs    float64 `json:"min_us"`
	AvgUs    float64 `json:"avg_us"`
	MaxUs    float64 `json:"max_us"`
	MdevUs   float64 `json:"mdev_us"`
	P50Us    float64 `json:"p50_us"`
	P99Us    float64 `json:"p99_us"`
}

func (r *RAN) Ping(ctx context.Context, cfg PingConfig) (Latency, error) {
	r.mu.Lock()
	c, ok := r.sockets[cfg.From]
	r.mu.Unlock()

	if !ok {
		return Latency{}, fmt.Errorf("%w %s", errNoSocket, cfg.From)
	}

	o := packet.Options{Inner: cfg.Size}
	if cfg.Size < packet.MinInner+probeLen {
		return Latency{}, packet.ErrTooSmall
	}

	to := netip.AddrPortFrom(cfg.To.Addr, packet.Port)
	ue := netip.AddrFrom4([4]byte{10, 45, 255, 1})
	payload := make([]byte, probeLen)
	binary.BigEndian.PutUint64(payload, probeMagic)

	var rtts []float64

	start := time.Now()

	for i := range cfg.Count {
		if ctx.Err() != nil {
			return Latency{}, ctx.Err()
		}

		seq := uint64(start.UnixNano()) + uint64(i)
		binary.BigEndian.PutUint64(payload[8:], seq)

		b := make([]byte, packet.GTPHeader+cfg.Size)
		n := packet.GPDU(b, cfg.To.TEID, o)
		packet.Inner(b[n:], ue, payload)

		ch := r.pings.expect(seq)
		sent := time.Now()

		if _, err := c.WriteToUDPAddrPort(b, to); err != nil {
			r.pings.forget(seq)
			return Latency{}, err
		}

		select {
		case at := <-ch:
			rtts = append(rtts, float64(at.Sub(sent).Nanoseconds())/1e3)
		case <-time.After(probeTimeout):
		}

		r.pings.forget(seq)

		if wait := time.Until(start.Add(time.Duration(i+1) * cfg.Interval)); wait > 0 {
			time.Sleep(wait)
		}
	}

	return summarize(cfg.Count, rtts), nil
}

func summarize(sent int, rtts []float64) Latency {
	l := Latency{Sent: sent, Received: len(rtts)}
	if len(rtts) == 0 {
		return l
	}

	sorted := slices.Sorted(slices.Values(rtts))
	l.MinUs, l.MaxUs = sorted[0], sorted[len(sorted)-1]

	var sum float64
	for _, v := range rtts {
		sum += v
	}

	l.AvgUs = sum / float64(len(rtts))

	var dev float64
	for _, v := range rtts {
		dev += math.Abs(v - l.AvgUs)
	}

	l.MdevUs = dev / float64(len(rtts))
	l.P50Us = sorted[len(sorted)/2]
	l.P99Us = sorted[min(len(sorted)-1, len(sorted)*99/100)]

	return l
}

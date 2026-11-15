package tx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/ellanetworks/mocn/perf/packet"
	"golang.org/x/sys/unix"
)

var ErrNoFlows = errors.New("no flows")

type Config struct {
	Interface string
	SrcMAC    net.HardwareAddr
	DstMAC    net.HardwareAddr
	Flows     []packet.Flow
	Options   packet.Options
	Rate      float64
	Duration  time.Duration
	CPUs      []int
	Batch     int
}

type Result struct {
	Sent    uint64        `json:"sent"`
	Elapsed time.Duration `json:"elapsed"`
	Behind  bool          `json:"behind"`
}

type worker struct {
	fd     int
	frames [][]byte
	msgs   []unix.Mmsghdr
	iovs   []unix.Iovec
	sent   atomic.Uint64
	behind atomic.Bool
}

func Run(ctx context.Context, cfg Config) (Result, error) {
	if len(cfg.Flows) == 0 {
		return Result{}, ErrNoFlows
	}

	ifi, err := net.InterfaceByName(cfg.Interface)
	if err != nil {
		return Result{}, err
	}

	workers := make([]*worker, len(cfg.CPUs))

	for i := range workers {
		w, err := newWorker(ifi.Index, cfg, i, len(workers))
		if err != nil {
			for _, o := range workers[:i] {
				_ = unix.Close(o.fd)
			}

			return Result{}, err
		}

		workers[i] = w
	}

	defer func() {
		for _, w := range workers {
			_ = unix.Close(w.fd)
		}
	}()

	perWorker := cfg.Rate / float64(len(workers))
	start := time.Now()
	deadline := start.Add(cfg.Duration)

	var wg sync.WaitGroup

	for i, w := range workers {
		wg.Go(func() { w.run(ctx, cfg.CPUs[i], perWorker, start, deadline) })
	}

	wg.Wait()

	r := Result{Elapsed: time.Since(start)}

	for _, w := range workers {
		r.Sent += w.sent.Load()
		r.Behind = r.Behind || w.behind.Load()
	}

	return r, ctx.Err()
}

func newWorker(ifindex int, cfg Config, idx, n int) (*worker, error) {
	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("packet socket: %w", err)
	}

	if err := unix.SetsockoptInt(fd, unix.SOL_PACKET, unix.PACKET_QDISC_BYPASS, 1); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("qdisc bypass: %w", err)
	}

	if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_SNDBUF, 4<<20); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}

	if err := unix.Bind(fd, &unix.SockaddrLinklayer{Protocol: htons(unix.ETH_P_IP), Ifindex: ifindex}); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("bind: %w", err)
	}

	w := &worker{fd: fd}

	for i := idx; i < len(cfg.Flows); i += n {
		f, err := packet.Frame(cfg.SrcMAC, cfg.DstMAC, cfg.Flows[i], cfg.Options, ueAddress(i))
		if err != nil {
			_ = unix.Close(fd)
			return nil, err
		}

		w.frames = append(w.frames, f)
	}

	if len(w.frames) == 0 {
		f, err := packet.Frame(cfg.SrcMAC, cfg.DstMAC, cfg.Flows[idx%len(cfg.Flows)], cfg.Options, ueAddress(idx))
		if err != nil {
			_ = unix.Close(fd)
			return nil, err
		}

		w.frames = append(w.frames, f)
	}

	w.iovs = make([]unix.Iovec, len(w.frames))
	for i, f := range w.frames {
		w.iovs[i].Base = &f[0]
		w.iovs[i].SetLen(len(f))
	}

	w.msgs = make([]unix.Mmsghdr, cfg.Batch)

	return w, nil
}

func ueAddress(i int) netip.Addr {
	return netip.AddrFrom4([4]byte{10, 45, byte(i >> 8), byte(i)})
}

func (w *worker) run(ctx context.Context, cpu int, rate float64, start, deadline time.Time) {
	runtime.LockOSThread()

	defer runtime.UnlockOSThread()

	var set unix.CPUSet

	set.Set(cpu)
	_ = unix.SchedSetaffinity(0, &set)

	batch := len(w.msgs)
	next := 0

	var (
		sent uint64
		gap  time.Duration
	)

	if rate > 0 {
		gap = time.Duration(float64(batch) / rate * float64(time.Second))
	}

	due := start

	for ctx.Err() == nil {
		now := time.Now()
		if !now.Before(deadline) {
			break
		}

		if gap > 0 {
			for now.Before(due) {
				now = time.Now()
			}

			if now.Sub(due) > 100*gap+time.Millisecond {
				w.behind.Store(true)

				due = now
			}

			due = due.Add(gap)
		}

		for i := range w.msgs {
			w.msgs[i].Hdr.Iov = &w.iovs[next]
			w.msgs[i].Hdr.Iovlen = 1

			next++
			if next == len(w.iovs) {
				next = 0
			}
		}

		done := 0
		for done < batch {
			n, _, errno := unix.Syscall6(unix.SYS_SENDMMSG, uintptr(w.fd),
				uintptr(unsafe.Pointer(&w.msgs[done])), uintptr(batch-done), 0, 0, 0)
			if errno != 0 {
				if errno == unix.ENOBUFS || errno == unix.EAGAIN {
					continue
				}

				break
			}

			done += int(n)
		}

		sent += uint64(done)
		w.sent.Store(sent)
	}
}

func htons(v uint16) uint16 {
	return v<<8 | v>>8
}

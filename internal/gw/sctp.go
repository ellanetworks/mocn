package gw

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"github.com/ellanetworks/core/sctp"
)

var ErrClosed = errors.New("association closed")

func Addresses(a any) []string {
	addr, ok := a.(*sctp.SCTPAddr)
	if !ok || addr == nil {
		return nil
	}

	out := make([]string, 0, len(addr.IPAddrs))

	for _, ip := range addr.IPAddrs {
		if a, ok := netip.AddrFromSlice(ip.IP); ok {
			out = append(out, a.Unmap().String())
		}
	}

	return out
}

func Send(conn *sctp.SCTPConn, ppid uint32, msg []byte, stream uint16) error {
	if conn == nil {
		return ErrClosed
	}

	_, err := conn.WriteMsg(msg, &sctp.SndRcvInfo{Stream: stream, PPID: sctp.PPIDWireOrder(ppid)})

	return err
}

func Sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

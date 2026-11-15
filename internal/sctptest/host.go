package sctptest

import (
	"net/netip"

	"github.com/ellanetworks/mocn/internal/gtpu"
)

const loopbackIndex = 1

type LoopbackHost struct{}

func (LoopbackHost) Route(netip.Addr) (gtpu.Route, error) {
	return gtpu.Route{Src: netip.MustParseAddr("127.0.0.1"), Ifindex: loopbackIndex}, nil
}

func (LoopbackHost) InterfaceName(int) (string, error) { return "lo", nil }

func (LoopbackHost) InterfaceIndex(string) (int, error) { return loopbackIndex, nil }

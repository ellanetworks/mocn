package gtpu

import (
	"fmt"
	"net/netip"

	"github.com/vishvananda/netlink"
)

type Route struct {
	Src     netip.Addr
	Ifindex int
}

type Host interface {
	Route(dst netip.Addr) (Route, error)
	InterfaceName(index int) (string, error)
	InterfaceIndex(name string) (int, error)
}

type NetlinkHost struct{}

func (NetlinkHost) Route(dst netip.Addr) (Route, error) {
	routes, err := netlink.RouteGet(dst.Unmap().AsSlice())
	if err != nil {
		return Route{}, fmt.Errorf("%w %s: %w", ErrNoRoute, dst, err)
	}

	if len(routes) == 0 || routes[0].LinkIndex == 0 {
		return Route{}, fmt.Errorf("%w %s", ErrNoRoute, dst)
	}

	src, _ := netip.AddrFromSlice(routes[0].Src)

	return Route{Src: src.Unmap(), Ifindex: routes[0].LinkIndex}, nil
}

func (NetlinkHost) InterfaceName(index int) (string, error) {
	l, err := netlink.LinkByIndex(index)
	if err != nil {
		return "", err
	}

	return l.Attrs().Name, nil
}

func (NetlinkHost) InterfaceIndex(name string) (int, error) {
	l, err := netlink.LinkByName(name)
	if err != nil {
		return 0, err
	}

	return l.Attrs().Index, nil
}

type Anchor struct {
	Peer      netip.Addr
	Interface string
	Addresses []netip.Addr
}

func Resolve(h Host, peer netip.Addr) (Route, string, error) {
	r, err := h.Route(peer)
	if err != nil {
		return Route{}, "", err
	}

	if !r.Src.IsValid() {
		return Route{}, "", fmt.Errorf("%w toward %s", ErrNoAddress, peer)
	}

	name, err := h.InterfaceName(r.Ifindex)
	if err != nil {
		return Route{}, "", fmt.Errorf("%w %s: %w", ErrNoRoute, peer, err)
	}

	return r, name, nil
}

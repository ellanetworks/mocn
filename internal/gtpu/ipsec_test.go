package gtpu

import (
	"errors"
	"net"
	"runtime"
	"slices"
	"testing"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
	"golang.org/x/sys/unix"
)

func esp(src, dst string) netlink.XfrmPolicyTmpl {
	return netlink.XfrmPolicyTmpl{
		Src: net.ParseIP(src), Dst: net.ParseIP(dst), Proto: netlink.XFRM_PROTO_ESP, Mode: netlink.XFRM_MODE_TUNNEL,
	}
}

func TestPolicyBasedIPsecRule(t *testing.T) {
	t.Parallel()

	tmpl := []netlink.XfrmPolicyTmpl{esp("192.0.2.1", "192.0.2.2")}

	for name, c := range map[string]struct {
		pol  netlink.XfrmPolicy
		want bool
	}{
		"template without interface ID":      {netlink.XfrmPolicy{Dir: netlink.XFRM_DIR_OUT, Tmpls: tmpl}, true},
		"forward template without interface": {netlink.XfrmPolicy{Dir: netlink.XFRM_DIR_FWD, Tmpls: tmpl}, true},
		"template with an interface ID":      {netlink.XfrmPolicy{Dir: netlink.XFRM_DIR_OUT, Tmpls: tmpl, Ifid: 1}, false},
		"socket bypass":                      {netlink.XfrmPolicy{Dir: netlink.XFRM_SOCKET_IN}, false},
		"socket policy with a template":      {netlink.XfrmPolicy{Dir: netlink.XFRM_SOCKET_OUT, Tmpls: tmpl}, false},
		"allow without template":             {netlink.XfrmPolicy{Dir: netlink.XFRM_DIR_IN}, false},
		"block without template": {
			netlink.XfrmPolicy{Dir: netlink.XFRM_DIR_OUT, Action: netlink.XFRM_POLICY_BLOCK}, false,
		},
	} {
		if got := appliesIPsecWithoutInterface(c.pol); got != c.want {
			t.Errorf("%s: policy-based IPsec = %v, want %v", name, got, c.want)
		}
	}
}

func socketBypass(family int) (int, error) {
	fd, err := unix.Socket(family, unix.SOCK_DGRAM, 0)
	if err != nil {
		return -1, err
	}

	level, opt := unix.IPPROTO_IP, unix.IP_XFRM_POLICY
	if family == unix.AF_INET6 {
		level, opt = unix.IPPROTO_IPV6, unix.IPV6_XFRM_POLICY
	}

	for _, dir := range []uint8{uint8(netlink.XFRM_DIR_IN), uint8(netlink.XFRM_DIR_OUT)} {
		info := nl.XfrmUserpolicyInfo{Dir: dir, Action: uint8(netlink.XFRM_POLICY_ALLOW)}
		info.Sel.Family = uint16(family)
		info.Lft.SoftByteLimit, info.Lft.HardByteLimit = nl.XFRM_INF, nl.XFRM_INF
		info.Lft.SoftPacketLimit, info.Lft.HardPacketLimit = nl.XFRM_INF, nl.XFRM_INF

		if err := unix.SetsockoptString(fd, level, opt, string(info.Serialize())); err != nil {
			return -1, errors.Join(err, unix.Close(fd))
		}
	}

	return fd, nil
}

func inOwnNetns(t *testing.T, f func() error) {
	t.Helper()

	done := make(chan error, 1)

	go func() {
		runtime.LockOSThread()

		if err := unix.Unshare(unix.CLONE_NEWNET); err != nil {
			done <- err
			return
		}

		done <- f()
	}()

	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPolicyBasedIPsecIgnoresSocketAndInterfacePolicies(t *testing.T) {
	requirePrivileged(t)

	inOwnNetns(t, func() error {
		var fds []int

		defer func() {
			for _, fd := range fds {
				_ = unix.Close(fd)
			}
		}()

		for _, family := range []int{unix.AF_INET, unix.AF_INET6} {
			fd, err := socketBypass(family)
			if err != nil {
				return err
			}

			fds = append(fds, fd)
		}

		_, local, _ := net.ParseCIDR("10.0.0.0/24")
		_, remote, _ := net.ParseCIDR("10.1.0.0/24")
		_, any6, _ := net.ParseCIDR("::/0")

		for _, pol := range []netlink.XfrmPolicy{
			{Src: local, Dst: remote, Dir: netlink.XFRM_DIR_OUT, Ifid: 1, Tmpls: []netlink.XfrmPolicyTmpl{esp("192.0.2.1", "192.0.2.2")}},
			{Src: remote, Dst: local, Dir: netlink.XFRM_DIR_IN, Ifid: 1, Tmpls: []netlink.XfrmPolicyTmpl{esp("192.0.2.2", "192.0.2.1")}},
			{Src: any6, Dst: any6, Dir: netlink.XFRM_DIR_IN, Priority: 1},
		} {
			if err := netlink.XfrmPolicyAdd(&pol); err != nil {
				return err
			}
		}

		policies, err := netlink.XfrmPolicyList(netlink.FAMILY_ALL)
		if err != nil {
			return err
		}

		if !slices.ContainsFunc(policies, func(p netlink.XfrmPolicy) bool { return p.Dir >= netlink.XFRM_SOCKET_IN }) {
			return errors.New("socket policies not listed")
		}

		if on, err := policyBasedIPsec(); err != nil || on {
			return errors.Join(errors.New("socket bypass and interface policies taken for policy-based IPsec"), err)
		}

		if err := netlink.XfrmPolicyAdd(&netlink.XfrmPolicy{
			Src: remote, Dst: local, Dir: netlink.XFRM_DIR_FWD, Tmpls: []netlink.XfrmPolicyTmpl{esp("192.0.2.2", "192.0.2.1")},
		}); err != nil {
			return err
		}

		if on, err := policyBasedIPsec(); err != nil || !on {
			return errors.Join(errors.New("a policy with a template and no interface ID not taken for policy-based IPsec"), err)
		}

		return nil
	})
}

package gtpu

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
	"golang.org/x/sys/unix"
)

const (
	ipsecRecheck       = 5 * time.Second
	xfrmMsgGetDefault  = 40
	xfrmUserpolicyDrop = 1
)

func policyBasedIPsec() (bool, error) {
	policies, err := netlink.XfrmPolicyList(netlink.FAMILY_ALL)
	if err != nil {
		return false, err
	}

	if slices.ContainsFunc(policies, appliesIPsecWithoutInterface) {
		return true, nil
	}

	return blockingDefaultPolicy()
}

func appliesIPsecWithoutInterface(pol netlink.XfrmPolicy) bool {
	return pol.Dir <= netlink.XFRM_DIR_FWD && len(pol.Tmpls) > 0 && pol.Ifid == 0
}

func blockingDefaultPolicy() (bool, error) {
	req := nl.NewNetlinkRequest(xfrmMsgGetDefault, 0)
	req.AddRawData(make([]byte, 4))

	msgs, err := req.Execute(unix.NETLINK_XFRM, xfrmMsgGetDefault)
	if err != nil {
		return false, err
	}

	for _, m := range msgs {
		for _, v := range m[:min(len(m), 3)] {
			if v == xfrmUserpolicyDrop {
				return true, nil
			}
		}
	}

	return false, nil
}

type xfrmWatch struct {
	changed chan struct{}
	lost    chan struct{}
	sock    *nl.NetlinkSocket
}

func subscribeXFRM() (*xfrmWatch, error) {
	sock, err := nl.Subscribe(unix.NETLINK_XFRM, nl.XFRMNLGRP_POLICY)
	if err != nil {
		return nil, err
	}

	w := &xfrmWatch{changed: make(chan struct{}, 1), lost: make(chan struct{}), sock: sock}

	go func() {
		defer close(w.lost)

		for {
			if _, _, err := sock.Receive(); err != nil {
				return
			}

			select {
			case w.changed <- struct{}{}:
			default:
			}
		}
	}()

	return w, nil
}

func (p *Plane) watchIPsec(ctx context.Context, dp datapath) {
	t := time.NewTicker(ipsecRecheck)
	defer t.Stop()

	var w *xfrmWatch

	defer func() {
		if w != nil {
			w.sock.Close()
		}
	}()

	last := -1

	for {
		if w == nil {
			var err error
			if w, err = subscribeXFRM(); err != nil {
				p.log.ErrorContext(ctx, "failed to watch IPsec policies, user traffic goes through the kernel",
					slog.Any("error", err))
			}
		}

		on, err := policyBasedIPsec()
		if err != nil {
			p.log.WarnContext(ctx, "failed to read IPsec policies", slog.Any("error", err))
		}

		on = on || err != nil || w == nil

		cur := 0
		if on {
			cur = 1
		}

		if cur != last {
			if err := dp.setPolicyIPsec(on); err != nil {
				p.log.ErrorContext(ctx, "failed to configure the user-plane datapath for IPsec", slog.Any("error", err))
			} else {
				last = cur

				if on {
					p.log.WarnContext(ctx, "IPsec policies without an xfrm interface ID: user traffic not on an xfrm interface goes through the kernel")
				}
			}
		}

		var changed, lost <-chan struct{}
		if w != nil {
			changed, lost = w.changed, w.lost
		}

		select {
		case <-ctx.Done():
			return
		case <-changed:
		case <-lost:
			w.sock.Close()
			w = nil
		case <-t.C:
		}
	}
}

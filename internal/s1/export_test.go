package s1

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
)

func (g *Gateway) HoldMMEAssociation(addrs ...string) (release func()) {
	c := &mmeClient{addrs: addrs, done: make(chan struct{})}

	g.mu.Lock()
	g.stopping[c] = struct{}{}
	g.mu.Unlock()

	return func() {
		g.mu.Lock()
		delete(g.stopping, c)
		g.mu.Unlock()
		close(c.done)
	}
}

func (g *Gateway) FailMMESends(err error) {
	g.relay.mu.Lock()
	g.relay.sendMME = func(*MMEAssociation, context.Context, []byte, uint16) error { return err }
	g.relay.mu.Unlock()
}

var MMEGUMMEIs = mmeGUMMEIs

func (g *Gateway) OverloadCounter() *prometheus.CounterVec {
	return g.overloads
}

func (g *Gateway) CloseMMESends() {
	g.relay.mu.Lock()
	g.relay.sendMME = func(a *MMEAssociation, ctx context.Context, msg []byte, stream uint16) error {
		return g.send(ctx, sideCore, nil, a.client.operator(), msg, stream)
	}
	g.relay.mu.Unlock()
}

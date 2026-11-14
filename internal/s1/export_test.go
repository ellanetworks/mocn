package s1

import (
	"context"

	"github.com/ellanetworks/core/s1ap"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

func (g *Gateway) FailMMESends(err error) {
	g.relay.mu.Lock()
	g.relay.sendMME = func(context.Context, *mmeAssoc, []byte, uint16) error { return err }
	g.relay.mu.Unlock()
}

var MMEGUMMEIs = mmeGUMMEIs

func (g *Gateway) OverloadCounter() *prometheus.CounterVec {
	return g.cfg.Metrics.Overload
}

func (g *Gateway) CloseMMESends() {
	g.relay.mu.Lock()
	g.relay.sendMME = func(ctx context.Context, a *mmeAssoc, msg []byte, stream uint16) error {
		return g.core.Transport().Send(ctx, metrics.SideCore, nil, a.Operator(), msg, stream)
	}
	g.relay.mu.Unlock()
}

func OwnsCell(id db.GlobalENBID, broadcast []db.PLMN, c s1ap.EUTRANCGI) bool {
	return ranSetup{globalENBID: id, tas: []TA{{TAC: 1, BroadcastPLMNs: broadcast}}}.ownsCell(c)
}

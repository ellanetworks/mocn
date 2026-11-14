package ng

import (
	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/prometheus/client_golang/prometheus"
)

func (g *Gateway) OverloadCounter() *prometheus.CounterVec {
	return g.cfg.Metrics.Overload
}

func OwnsCell(id db.GlobalGNBID, broadcast db.PLMN, c ngap.NRCGI) bool {
	return ranSetup{globalGNBID: id, tas: []TA{{TAC: 1, BroadcastPLMNs: []BroadcastPLMN{{PLMN: broadcast}}}}}.ownsCell(c)
}

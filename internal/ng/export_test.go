package ng

import "github.com/prometheus/client_golang/prometheus"

func (g *Gateway) OverloadCounter() *prometheus.CounterVec {
	return g.cfg.Metrics.Overload
}

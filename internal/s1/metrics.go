package s1

import "github.com/prometheus/client_golang/prometheus"

var (
	associationsDesc = prometheus.NewDesc("ellamocn_s1_associations",
		"S1 associations, by side (ran: RAN nodes, mme: configured MMEs) and state.", []string{"side", "state"}, nil)
	ranNodesDesc = prometheus.NewDesc("ellamocn_ran_nodes_connected",
		"RAN nodes whose S1 Setup succeeded.", nil, nil)
)

type stateCollector struct {
	g *Gateway
}

func (g *Gateway) Collectors() []prometheus.Collector {
	return []prometheus.Collector{g.setups, stateCollector{g: g}}
}

func (c stateCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- associationsDesc

	ch <- ranNodesDesc
}

func (c stateCollector) Collect(ch chan<- prometheus.Metric) {
	ran := map[RANNodeState]int{}
	mme := map[MMEState]int{}

	c.g.mu.Lock()

	for _, n := range c.g.ranNodes {
		if n.up {
			ran[RANNodeUp]++
		} else {
			ran[RANNodeConnected]++
		}
	}

	for _, m := range c.g.mmes {
		mme[m.status.State]++
	}

	c.g.mu.Unlock()

	for _, s := range RANNodeStates {
		ch <- prometheus.MustNewConstMetric(associationsDesc, prometheus.GaugeValue, float64(ran[s]), sideRAN, string(s))
	}

	for _, s := range MMEStates {
		ch <- prometheus.MustNewConstMetric(associationsDesc, prometheus.GaugeValue, float64(mme[s]), sideMME, string(s))
	}

	ch <- prometheus.MustNewConstMetric(ranNodesDesc, prometheus.GaugeValue, float64(ran[RANNodeUp]))
}

package s1

import "github.com/prometheus/client_golang/prometheus"

const interfaceS1 = "s1"

const (
	sideRadio = "radio"
	sideCore  = "core"
)

const (
	setupSuccess     = "success"
	setupFailure     = "failure"
	setupTimeout     = "timeout"
	setupInvalid     = "invalid"
	setupUnknownPLMN = "unknown_plmn"
	setupNoMME       = "no_mme"
	setupMMEFailure  = "mme_failure"
	setupError       = "error"
)

var setupResults = map[string][]string{
	sideRadio: {setupSuccess, setupInvalid, setupUnknownPLMN, setupNoMME, setupMMEFailure, setupError},
	sideCore:  {setupSuccess, setupFailure, setupTimeout, setupError},
}

var (
	radioAssociationsDesc = prometheus.NewDesc("ellamocn_radio_associations",
		"Radio (eNodeB) associations, by interface (s1) and state "+
			"(connected: SCTP association up, no successful S1 Setup; up: S1 Setup succeeded).",
		[]string{"interface", "state"}, nil)
	coreAssociationsDesc = prometheus.NewDesc("ellamocn_core_associations",
		"Core (MME) associations of the configured MMEs, by interface (s1), operator and state "+
			"(disconnected: no SCTP association; connected: SCTP association up, S1 Setup not done; "+
			"up: S1 Setup succeeded; setup_failed: the MME rejected S1 Setup).",
		[]string{"interface", "operator", "state"}, nil)
	ueConnectionsDesc = prometheus.NewDesc("ellamocn_ue_connections",
		"UE-associated logical S1-connections (TS 36.413 clause 3.1) relayed, by interface (s1) and operator.",
		[]string{"interface", "operator"}, nil)
)

func newSetupCounter() *prometheus.CounterVec {
	c := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "ellamocn_setup_attempts_total",
		Help: "Setup procedures (S1 Setup), by interface (s1), side and result. " +
			"side=radio: received from radios; result success, invalid (undecodable or invalid request), " +
			"unknown_plmn (no operator serves a broadcast PLMN), no_mme (no MME of the serving operators is up), " +
			"mme_failure (the serving operators' MMEs rejected S1 Setup), " +
			"error (the response could not be built or sent, or the radio left before the answer). " +
			"side=core: sent to MMEs; result success, failure (the MME rejected the request or answered with an invalid response), " +
			"timeout (no answer from the MME), " +
			"error (the request could not be built or sent, or the association dropped before the answer).",
	}, []string{"interface", "side", "result"})

	for side, results := range setupResults {
		for _, r := range results {
			c.WithLabelValues(interfaceS1, side, r)
		}
	}

	return c
}

func (g *Gateway) countSetup(side, result string) {
	g.setups.WithLabelValues(interfaceS1, side, result).Inc()
}

type stateCollector struct {
	g *Gateway
}

func (g *Gateway) Collectors() []prometheus.Collector {
	return []prometheus.Collector{g.setups, g.relay.initialUE, stateCollector{g: g}}
}

func (c stateCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- radioAssociationsDesc

	ch <- coreAssociationsDesc

	ch <- ueConnectionsDesc
}

func (c stateCollector) Collect(ch chan<- prometheus.Metric) {
	ues := c.g.relay.countByOperator()
	radios := map[RANNodeState]int{}
	names := map[int64]string{}
	ueConns := map[string]int{}
	core := map[string]map[MMEState]int{}

	c.g.mu.Lock()

	for _, n := range c.g.ranNodes {
		if n.up {
			radios[RANNodeUp]++
		} else {
			radios[RANNodeConnected]++
		}
	}

	for _, o := range c.g.operators {
		names[o.id] = o.name
		ueConns[o.name] += ues[o.id]
	}

	for _, m := range c.g.mmes {
		name, ok := names[m.operatorID]
		if !ok {
			continue
		}

		if core[name] == nil {
			core[name] = map[MMEState]int{}
		}

		core[name][m.status.State]++
	}

	c.g.mu.Unlock()

	for _, s := range RANNodeStates {
		ch <- prometheus.MustNewConstMetric(radioAssociationsDesc, prometheus.GaugeValue, float64(radios[s]),
			interfaceS1, string(s))
	}

	for name, states := range core {
		for _, s := range MMEStates {
			ch <- prometheus.MustNewConstMetric(coreAssociationsDesc, prometheus.GaugeValue, float64(states[s]),
				interfaceS1, name, string(s))
		}
	}

	for name, n := range ueConns {
		ch <- prometheus.MustNewConstMetric(ueConnectionsDesc, prometheus.GaugeValue, float64(n), interfaceS1, name)
	}
}

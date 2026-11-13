package s1

import (
	"github.com/ellanetworks/mocn/internal/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	interfaceS1 = metrics.InterfaceS1
	sideRadio   = metrics.SideRadio
	sideCore    = metrics.SideCore
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

func initMetrics(m *metrics.Metrics) {
	for side, results := range setupResults {
		for _, r := range results {
			m.Setups.WithLabelValues(interfaceS1, side, r)
		}
	}

	for _, r := range initialUEResults {
		m.InitialUE.WithLabelValues(interfaceS1, r)
	}

	for _, r := range pagingResults {
		m.Paging.WithLabelValues(interfaceS1, r)
	}

	for proc, results := range overloadResults {
		for _, r := range results {
			m.Overload.WithLabelValues(interfaceS1, proc, r)
		}
	}
}

func (g *Gateway) countSetup(side, result string) {
	g.cfg.Metrics.Setups.WithLabelValues(interfaceS1, side, result).Inc()
}

func (g *Gateway) Collectors() []prometheus.Collector {
	return g.cfg.Metrics.Collectors(g)
}

func (g *Gateway) MetricsState() metrics.State {
	ues := g.relay.countByOperator()
	s := metrics.State{
		Interface:     interfaceS1,
		Radios:        map[string]int{},
		Core:          map[string]map[string]int{},
		Overloaded:    map[string]int{},
		UEConnections: map[string]int{},
	}

	for _, st := range RANNodeStates {
		s.RadioStates = append(s.RadioStates, string(st))
	}

	for _, st := range MMEStates {
		s.CoreStates = append(s.CoreStates, string(st))
	}

	names := map[int64]string{}

	g.mu.Lock()
	defer g.mu.Unlock()

	for _, n := range g.ranNodes {
		if n.up {
			s.Radios[string(RANNodeUp)]++
		} else {
			s.Radios[string(RANNodeConnected)]++
		}
	}

	for _, o := range g.operators {
		names[o.id] = o.name
		s.UEConnections[o.name] += ues[o.id]
	}

	for _, m := range g.mmes {
		name, ok := names[m.operatorID]
		if !ok {
			continue
		}

		if s.Core[name] == nil {
			s.Core[name] = map[string]int{}
		}

		s.Core[name][string(m.status.State)]++

		if len(m.status.Overload) > 0 {
			s.Overloaded[name]++
		}
	}

	return s
}

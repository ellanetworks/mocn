package ng

import (
	"github.com/ellanetworks/mocn/internal/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	interfaceNG = metrics.InterfaceNG
	sideRadio   = metrics.SideRadio
	sideCore    = metrics.SideCore
)

const (
	setupSuccess     = "success"
	setupFailure     = "failure"
	setupTimeout     = "timeout"
	setupInvalid     = "invalid"
	setupUnknownPLMN = "unknown_plmn"
	setupNoAMF       = "no_amf"
	setupAMFFailure  = "amf_failure"
	setupError       = "error"
)

var setupResults = map[string][]string{
	sideRadio: {setupSuccess, setupInvalid, setupUnknownPLMN, setupNoAMF, setupAMFFailure, setupError},
	sideCore:  {setupSuccess, setupFailure, setupTimeout, setupError},
}

func initMetrics(m *metrics.Metrics) {
	for side, results := range setupResults {
		for _, r := range results {
			m.Setups.WithLabelValues(interfaceNG, side, r)
		}
	}

	for _, r := range initialUEResults {
		m.InitialUE.WithLabelValues(interfaceNG, r)
	}

	for _, r := range pagingResults {
		m.Paging.WithLabelValues(interfaceNG, r)
	}
}

func (g *Gateway) countSetup(side, result string) {
	g.cfg.Metrics.Setups.WithLabelValues(interfaceNG, side, result).Inc()
}

func (g *Gateway) Collectors() []prometheus.Collector {
	return g.cfg.Metrics.Collectors(g)
}

func (g *Gateway) MetricsState() metrics.State {
	ues := g.relay.countByOperator()
	s := metrics.State{
		Interface:     interfaceNG,
		Radios:        map[string]int{},
		Core:          map[string]map[string]int{},
		Overloaded:    map[string]int{},
		UEConnections: map[string]int{},
	}

	for _, st := range RANNodeStates {
		s.RadioStates = append(s.RadioStates, string(st))
	}

	for _, st := range AMFStates {
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

	for _, c := range g.amfs {
		name, ok := names[c.operatorID]
		if !ok {
			continue
		}

		if s.Core[name] == nil {
			s.Core[name] = map[string]int{}
		}

		s.Core[name][string(c.status.State)]++

		if c.status.Overload != nil {
			s.Overloaded[name]++
		}
	}

	return s
}

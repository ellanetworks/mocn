package assoc

import (
	"github.com/ellanetworks/mocn/internal/metrics"
)

const (
	SetupSuccess     = "success"
	SetupFailure     = "failure"
	SetupTimeout     = "timeout"
	SetupInvalid     = "invalid"
	SetupUnknownPLMN = "unknown_plmn"
	SetupError       = "error"
)

const (
	InitialUERelayed     = "relayed"
	InitialUEUnknownPLMN = "unknown_plmn"
	InitialUERejected    = "rejected"
	InitialUEInvalid     = "invalid"
	InitialUEError       = "error"
)

const (
	PagingRelayed = "relayed"
	PagingNoRadio = "no_radio"
	PagingInvalid = "invalid"
)

const (
	OverloadStart   = "overload_start"
	OverloadStop    = "overload_stop"
	OverloadRelayed = "relayed"
	OverloadNoRadio = "no_radio"
	OverloadIgnored = "ignored"
	OverloadInvalid = "invalid"
)

const (
	HandoverRequest   = "handover_request"
	PathSwitchRequest = "path_switch_request"
	HandoverRelayed   = "relayed"
	HandoverNoRadio   = "no_radio"
	HandoverInvalid   = "invalid"
	HandoverError     = "error"
)

const (
	PWSWriteReplaceWarning = "write_replace_warning"
	PWSRestartIndication   = "pws_restart_indication"
	PWSFailureIndication   = "pws_failure_indication"
	PWSRelayed             = "relayed"
	PWSNotPWSOperator      = "not_pws_operator"
	PWSNoRadio             = "no_radio"
	PWSInvalid             = "invalid"
)

var overloadResults = map[string][]string{
	OverloadStart: {OverloadRelayed, OverloadNoRadio, OverloadInvalid},
	OverloadStop:  {OverloadRelayed, OverloadNoRadio, OverloadIgnored, OverloadInvalid},
}

func (g *Gateway[S, R, I, C]) initMetrics() {
	m, iface := g.cfg.Metrics, g.names.Interface

	for side, results := range map[string][]string{
		metrics.SideRadio: {SetupSuccess, SetupInvalid, SetupUnknownPLMN, g.names.NoCore, g.names.CoreFailure, SetupError},
		metrics.SideCore:  {SetupSuccess, SetupFailure, SetupTimeout, SetupError},
	} {
		for _, r := range results {
			m.Setups.WithLabelValues(iface, side, r)
		}
	}

	for _, r := range []string{
		InitialUERelayed, InitialUEUnknownPLMN, g.names.NoCore, InitialUERejected, InitialUEInvalid, InitialUEError,
	} {
		m.InitialUE.WithLabelValues(iface, r)
	}

	for _, r := range []string{PagingRelayed, PagingNoRadio, PagingInvalid} {
		m.Paging.WithLabelValues(iface, r)
	}

	for proc, results := range overloadResults {
		for _, r := range results {
			m.Overload.WithLabelValues(iface, proc, r)
		}
	}

	for proc, results := range map[string][]string{
		HandoverRequest:   {HandoverRelayed, HandoverNoRadio, HandoverInvalid, HandoverError},
		PathSwitchRequest: {HandoverRelayed, g.names.NoCore, HandoverInvalid, HandoverError},
	} {
		for _, r := range results {
			m.Handover.WithLabelValues(iface, proc, r)
		}
	}

	warning := []string{PWSRelayed, PWSNotPWSOperator, PWSNoRadio, PWSInvalid}
	indication := []string{PWSRelayed, g.names.NoCore, PWSInvalid}

	for proc, results := range map[string][]string{
		PWSWriteReplaceWarning: warning,
		g.names.Kill:           warning,
		PWSRestartIndication:   indication,
		PWSFailureIndication:   indication,
	} {
		for _, r := range results {
			m.PWS.WithLabelValues(iface, proc, r)
		}
	}
}

func (g *Gateway[S, R, I, C]) countSetup(side, result string) {
	g.cfg.Metrics.Setups.WithLabelValues(g.names.Interface, side, result).Inc()
}

func (g *Gateway[S, R, I, C]) CountInitialUE(result string) {
	g.cfg.Metrics.InitialUE.WithLabelValues(g.names.Interface, result).Inc()
}

func (g *Gateway[S, R, I, C]) CountPaging(result string) {
	g.cfg.Metrics.Paging.WithLabelValues(g.names.Interface, result).Inc()
}

func (g *Gateway[S, R, I, C]) CountOverload(proc, result string) {
	g.cfg.Metrics.Overload.WithLabelValues(g.names.Interface, proc, result).Inc()
}

func (g *Gateway[S, R, I, C]) CountHandover(proc, result string) {
	g.cfg.Metrics.Handover.WithLabelValues(g.names.Interface, proc, result).Inc()
}

func (g *Gateway[S, R, I, C]) CountPWS(proc, result string) {
	g.cfg.Metrics.PWS.WithLabelValues(g.names.Interface, proc, result).Inc()
}

func (g *Gateway[S, R, I, C]) MetricsState() metrics.State {
	ues := g.proto.UEsByOperator()
	s := metrics.State{
		Interface:     g.names.Interface,
		Radios:        map[string]int{},
		Core:          map[string]map[string]int{},
		Overloaded:    map[string]int{},
		UEConnections: map[string]int{},
	}

	for _, st := range RadioStates {
		s.RadioStates = append(s.RadioStates, string(st))
	}

	for _, st := range States {
		s.CoreStates = append(s.CoreStates, string(st))
	}

	names := map[int64]string{}

	g.mu.Lock()
	defer g.mu.Unlock()

	for _, n := range g.radios {
		if n.Up {
			s.Radios[string(RadioUp)]++
		} else {
			s.Radios[string(RadioConnected)]++
		}
	}

	for _, o := range g.operators {
		names[o.ID] = o.Name
		s.UEConnections[o.Name] += ues[o.ID]
	}

	for _, c := range g.cores {
		name, ok := names[c.operatorID]
		if !ok {
			continue
		}

		if s.Core[name] == nil {
			s.Core[name] = map[string]int{}
		}

		s.Core[name][string(c.State)]++

		if g.proto.Overloaded(c.Info) {
			s.Overloaded[name]++
		}
	}

	return s
}

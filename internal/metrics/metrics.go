package metrics

import (
	"strings"
	"unicode"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	InterfaceS1 = "s1"
	InterfaceNG = "ng"
	SideRadio   = "radio"
	SideCore    = "core"
)

var (
	radioAssociationsDesc = prometheus.NewDesc("ellamocn_radio_associations",
		"Radio (eNodeB, gNodeB) associations, by interface (s1, ng) and state "+
			"(connected: SCTP association up, no successful S1 or NG Setup; up: S1 or NG Setup succeeded; "+
			"setup_failed: SCTP association up, Ella MOCN rejected the last S1 or NG Setup).",
		[]string{"interface", "state"}, nil)
	coreAssociationsDesc = prometheus.NewDesc("ellamocn_core_associations",
		"Core (MME, AMF) associations of the configured MMEs and AMFs, by interface (s1, ng), operator and state "+
			"(disconnected: no SCTP association; connected: SCTP association up, S1 or NG Setup not done; "+
			"up: S1 or NG Setup succeeded; setup_failed: the MME or AMF rejected the setup).",
		[]string{"interface", "operator", "state"}, nil)
	coreOverloadedDesc = prometheus.NewDesc("ellamocn_core_overloaded",
		"Core (MME, AMF) associations of the configured MMEs and AMFs with an ongoing overload action "+
			"(TS 36.413 clause 8.7.6, TS 38.413 clause 8.7.7), by interface (s1, ng) and operator.",
		[]string{"interface", "operator"}, nil)
	ueConnectionsDesc = prometheus.NewDesc("ellamocn_ue_connections",
		"UE-associated logical S1-connections (TS 36.413 clause 3.1) and NG-connections relayed, "+
			"by interface (s1, ng) and operator.",
		[]string{"interface", "operator"}, nil)
)

type Metrics struct {
	Setups    *prometheus.CounterVec
	InitialUE *prometheus.CounterVec
	Paging    *prometheus.CounterVec
	Overload  *prometheus.CounterVec
	Handover  *prometheus.CounterVec
	PWS       *prometheus.CounterVec
}

func New() *Metrics {
	return &Metrics{
		Setups: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ellamocn_setup_attempts_total",
			Help: "Setup procedures (S1 Setup, NG Setup), by interface (s1, ng), side and result. " +
				"side=radio: received from radios; result success, invalid (undecodable or invalid request), " +
				"unknown_plmn (no operator serves a broadcast PLMN), " +
				"no_mme, no_amf (no MME or AMF of the serving operators is up), " +
				"mme_failure, amf_failure (the serving operators' MMEs or AMFs rejected the setup), " +
				"error (the response could not be built or sent, or the radio left before the answer). " +
				"side=core: sent to MMEs and AMFs; result success, failure (the MME or AMF rejected the request or answered with an invalid response), " +
				"timeout (no answer from the MME or AMF), " +
				"error (the request could not be built or sent, or the association dropped before the answer).",
		}, []string{"interface", "side", "result"}),
		InitialUE: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ellamocn_initial_ue_messages_total",
			Help: "INITIAL UE MESSAGEs received from radios, by interface (s1, ng) and result " +
				"(relayed: sent to an MME or AMF; unknown_plmn: no operator serves the PLMN; " +
				"no_mme, no_amf: no selectable MME or AMF of the operator; " +
				"rejected: reuses an allocated eNB UE S1AP ID or RAN UE NGAP ID, an Error Indication is sent; " +
				"invalid: undecodable or missing a mandatory IE; error: internal failure or failed send to the MME or AMF).",
		}, []string{"interface", "result"}),
		Paging: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ellamocn_paging_messages_total",
			Help: "PAGING messages received from MMEs and AMFs, by interface (s1, ng) and result " +
				"(relayed: sent to the radios that serve a TAI of the list in one of the operator's PLMNs; " +
				"no_radio: no radio that is up serves such a TAI; invalid: undecodable).",
		}, []string{"interface", "result"}),
		Overload: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ellamocn_overload_messages_total",
			Help: "OVERLOAD START and OVERLOAD STOP messages received from MMEs and AMFs, by interface (s1, ng), procedure " +
				"(overload_start, overload_stop) and result (relayed: sent to the radios that broadcast one of the operator's PLMNs, including the replay of an ongoing OVERLOAD START to a radio after its S1 or NG Setup; " +
				"no_radio: no such radio is up; ignored: OVERLOAD STOP for GUMMEIs or GUAMIs with no ongoing overload action; " +
				"invalid: undecodable or with an invalid GUMMEI or GUAMI).",
		}, []string{"interface", "procedure", "result"}),
		Handover: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ellamocn_handover_messages_total",
			Help: "HANDOVER REQUEST messages received from MMEs and AMFs and PATH SWITCH REQUEST messages received from radios, " +
				"by interface (s1, ng), procedure (handover_request, path_switch_request) and result " +
				"(relayed: sent to the target radio or to the MME or AMF; no_radio: no target radio of the operator is up; " +
				"no_mme, no_amf: no selectable MME or AMF of the source MME or AMF set is up; " +
				"invalid: undecodable or missing an IE needed for routing; error: internal failure or failed send).",
		}, []string{"interface", "procedure", "result"}),
		PWS: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "ellamocn_pws_messages_total",
			Help: "PWS messages: WRITE-REPLACE WARNING REQUEST, KILL REQUEST and PWS CANCEL REQUEST received from MMEs and AMFs, " +
				"PWS RESTART INDICATION and PWS FAILURE INDICATION received from radios, by interface (s1, ng), procedure " +
				"(write_replace_warning, kill, pws_cancel, pws_restart_indication, pws_failure_indication) and result " +
				"(relayed: sent to the radios covering the warning area, or to every MME or AMF of the PWS operator that is up; " +
				"not_pws_operator: from an operator that is not the PWS operator, answered with no broadcast area; " +
				"no_radio: no radio that is up covers the warning area, answered with no broadcast area; " +
				"timeout: a radio the request was sent to did not answer within 3 seconds, counted once per radio; " +
				"no_pws_operator: no operator with MMEs or AMFs on the interface is the PWS operator, dropped; " +
				"no_mme, no_amf: no MME or AMF of the PWS operator is up, a PWS RESTART INDICATION is kept and sent when one is up; invalid: undecodable).",
		}, []string{"interface", "procedure", "result"}),
	}
}

type State struct {
	Interface     string
	RadioStates   []string
	Radios        map[string]int
	CoreStates    []string
	Core          map[string]map[string]int
	Overloaded    map[string]int
	UEConnections map[string]int
}

type Source interface {
	MetricsState() State
}

func (m *Metrics) Collectors(sources ...Source) []prometheus.Collector {
	return []prometheus.Collector{m.Setups, m.Overload, m.InitialUE, m.Paging, m.Handover, m.PWS, stateCollector(sources)}
}

type stateCollector []Source

func (c stateCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- radioAssociationsDesc

	ch <- coreAssociationsDesc

	ch <- coreOverloadedDesc

	ch <- ueConnectionsDesc
}

func (c stateCollector) Collect(ch chan<- prometheus.Metric) {
	for _, src := range c {
		s := src.MetricsState()

		for _, st := range s.RadioStates {
			ch <- prometheus.MustNewConstMetric(radioAssociationsDesc, prometheus.GaugeValue, float64(s.Radios[st]),
				s.Interface, st)
		}

		for name, states := range s.Core {
			for _, st := range s.CoreStates {
				ch <- prometheus.MustNewConstMetric(coreAssociationsDesc, prometheus.GaugeValue, float64(states[st]),
					s.Interface, name, st)
			}

			ch <- prometheus.MustNewConstMetric(coreOverloadedDesc, prometheus.GaugeValue, float64(s.Overloaded[name]),
				s.Interface, name)
		}

		for name, n := range s.UEConnections {
			ch <- prometheus.MustNewConstMetric(ueConnectionsDesc, prometheus.GaugeValue, float64(n), s.Interface, name)
		}
	}
}

func ProcedureLabel(name string) string {
	runes := []rune(name)

	var b strings.Builder

	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) &&
			(unicode.IsLower(runes[i-1]) || i+1 < len(runes) && unicode.IsLower(runes[i+1])) {
			b.WriteByte('_')
		}

		b.WriteRune(unicode.ToLower(r))
	}

	return b.String()
}

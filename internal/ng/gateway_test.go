package ng_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/core/per"
	"github.com/ellanetworks/mocn/internal/db"
	"github.com/ellanetworks/mocn/internal/ng"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func assertTAs(t *testing.T, got, want ngap.SupportedTAList) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("Supported TA List = %+v, want %+v", got, want)
	}

	for i := range got {
		if got[i].TAC != want[i].TAC || len(got[i].BroadcastPLMNList) != len(want[i].BroadcastPLMNList) {
			t.Fatalf("Supported TA List = %+v, want %+v", got, want)
		}

		for j, b := range got[i].BroadcastPLMNList {
			w := want[i].BroadcastPLMNList[j]
			if b.PLMNIdentity != w.PLMNIdentity || !reflect.DeepEqual(b.TAISliceSupportList, w.TAISliceSupportList) {
				t.Fatalf("Supported TA List = %+v, want %+v", got, want)
			}
		}
	}
}

func assertNodeName(t *testing.T, name *string) {
	t.Helper()

	if name == nil || *name != "Ella MOCN" || len(*name) < 1 || len(*name) > 150 {
		t.Fatalf("name = %v, want Ella MOCN", name)
	}

	for _, r := range *name {
		if !strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789 '()+,-./:=?", r) {
			t.Fatalf("name %q is not a PrintableString", *name)
		}
	}
}

func guamis(t *testing.T, list ngap.ServedGUAMIList) []ngap.GUAMI {
	t.Helper()

	out := make([]ngap.GUAMI, 0, len(list))
	for _, item := range list {
		out = append(out, item.GUAMI)
	}

	return out
}

func amfConfigurationUpdateWithPLMNSupport(t *testing.T, upd *ngap.AMFConfigurationUpdate, support ngap.PLMNSupportList) []byte {
	t.Helper()

	b, err := upd.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	p, err := ngap.ParseRawPDU(b)
	if err != nil {
		t.Fatal(err)
	}

	value, err := per.Marshal(support, per.Aligned)
	if err != nil {
		t.Fatal(err)
	}

	if err := p.Insert(p.Len(), ngap.RawIE{ID: ngap.IDPLMNSupportList, Criticality: ngap.CriticalityReject, Value: value}); err != nil {
		t.Fatal(err)
	}

	out, err := p.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	return out
}

func plmnSupportOf(t *testing.T, raw []byte) ngap.PLMNSupportList {
	t.Helper()

	p, err := ngap.ParseRawPDU(raw)
	if err != nil {
		t.Fatal(err)
	}

	var support ngap.PLMNSupportList
	if ok, err := p.Get(ngap.IDPLMNSupportList, &support); err != nil || !ok {
		t.Fatalf("PLMN Support List = %v, %v", ok, err)
	}

	return support
}

func TestNGSetupBothWays(t *testing.T) {
	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.2")
	h := startGateway(t, amfs.port(), operatorA("127.0.1.2"))
	amf := amfs.accept(t)

	amf.quiet(t, 300*time.Millisecond)

	h.eventually(t, "AMF connected", func() bool {
		st, ok := h.gw.AMFStatus(1, []string{"127.0.1.2"})
		return ok && st.State == ng.AMFConnected
	})

	gnb := h.connectGNB(t)
	gnb.send(t, setupRequest(t, 1, "gnb1", ngap.PagingDRXv128, ta(t, 1, "00101", "00199")))

	req := expect(t, amf, "initiating", ngap.ProcNGSetup, ngap.ParseNGSetupRequest)

	want := ngap.GlobalRANNodeID{Kind: ngap.RANNodeIDGNB, PLMNIdentity: plmnID(t, "001", "01"), Value: 0xabcde, Bits: 22}
	if req.GlobalRANNodeID != want {
		t.Fatalf("Global RAN Node ID = %+v, want %+v", req.GlobalRANNodeID, want)
	}

	assertTAs(t, req.SupportedTAList, ngap.SupportedTAList{ta(t, 1, "00101")})

	if req.DefaultPagingDRX == nil || *req.DefaultPagingDRX != ngap.PagingDRXv128 {
		t.Fatalf("NG Setup Request = %+v", req)
	}

	assertNodeName(t, req.RANNodeName)

	g1 := guami(t, "00101", 2, 1, 0)
	amf.send(t, setupResponse(t, "amf1", 10, "00101", g1, guami(t, "00199", 3, 1, 0)))

	resp := expect(t, gnb, "successful", ngap.ProcNGSetup, ngap.ParseNGSetupResponse)
	assertNodeName(t, &resp.AMFName)

	if resp.RelativeAMFCapacity == nil || *resp.RelativeAMFCapacity != 255 ||
		!reflect.DeepEqual(guamis(t, resp.ServedGUAMIList), []ngap.GUAMI{g1}) ||
		len(resp.PLMNSupportList) != 1 || resp.PLMNSupportList[0].PLMNIdentity != plmnID(t, "001", "01") ||
		!reflect.DeepEqual(resp.PLMNSupportList[0].SliceSupportList, ngap.SliceSupportList{slice}) {
		t.Fatalf("NG Setup Response = %+v", resp)
	}

	st, _ := h.gw.AMFStatus(1, []string{"127.0.1.2"})
	sd := ng.SNSSAI{SST: 1, SD: "010203"}

	if st.State != ng.AMFUp || st.AMFName != "amf1" || st.RelativeAMFCapacity == nil || *st.RelativeAMFCapacity != 10 ||
		st.PrimaryTNLAddress != "127.0.1.2" ||
		!reflect.DeepEqual(st.ServedGUAMIs, []ng.GUAMI{
			{PLMN: plmnA, AMFRegionID: 2, AMFSetID: 1}, {PLMN: db.PLMN{MCC: "001", MNC: "99"}, AMFRegionID: 3, AMFSetID: 1},
		}) ||
		!reflect.DeepEqual(st.PLMNSupport, []ng.PLMNSupport{{PLMN: plmnA, Slices: []ng.SNSSAI{sd}}}) {
		t.Fatalf("AMF status = %+v", st)
	}

	nodes := h.gw.RANNodes()
	if len(nodes) != 1 || nodes[0].State != ng.RANNodeUp || nodes[0].Name != "gnb1" || nodes[0].DefaultPagingDRX != 128 ||
		nodes[0].GlobalGNBID == nil || *nodes[0].GlobalGNBID != (db.GlobalGNBID{PLMN: plmnA, ID: 1, Length: 24}) ||
		!reflect.DeepEqual(nodes[0].SupportedTAs, []ng.TA{{TAC: 1, BroadcastPLMNs: []ng.BroadcastPLMN{
			{PLMN: plmnA, Slices: []ng.SNSSAI{sd}}, {PLMN: db.PLMN{MCC: "001", MNC: "99"}, Slices: []ng.SNSSAI{sd}},
		}}}) {
		t.Fatalf("RAN nodes = %+v", nodes)
	}

	gnb2 := h.connectGNB(t)
	gnb2.send(t, setupRequest(t, 2, "gnb2", ngap.PagingDRXv256, ta(t, 0x123456, "00101")))
	expect(t, gnb2, "successful", ngap.ProcNGSetup, ngap.ParseNGSetupResponse)

	upd := expect(t, amf, "initiating", ngap.ProcRANConfigurationUpdate, ngap.ParseRANConfigurationUpdate)
	assertTAs(t, upd.SupportedTAList, ngap.SupportedTAList{ta(t, 1, "00101"), ta(t, 0x123456, "00101")})

	if upd.DefaultPagingDRX == nil || *upd.DefaultPagingDRX != ngap.PagingDRXv256 {
		t.Fatalf("Default Paging DRX = %v, want v256", upd.DefaultPagingDRX)
	}

	assertNodeName(t, upd.RANNodeName)

	amf.send(t, &ngap.RANConfigurationUpdateAcknowledge{})

	gnb2.send(t, &ngap.RANConfigurationUpdate{SupportedTAList: ngap.SupportedTAList{ta(t, 3, "00101")}})
	expect(t, gnb2, "successful", ngap.ProcRANConfigurationUpdate, ngap.ParseRANConfigurationUpdateAcknowledge)

	upd = expect(t, amf, "initiating", ngap.ProcRANConfigurationUpdate, ngap.ParseRANConfigurationUpdate)
	assertTAs(t, upd.SupportedTAList, ngap.SupportedTAList{ta(t, 1, "00101"), ta(t, 3, "00101")})

	if upd.DefaultPagingDRX != nil {
		t.Fatalf("Default Paging DRX = %v, want absent", *upd.DefaultPagingDRX)
	}

	amf.send(t, &ngap.RANConfigurationUpdateAcknowledge{})

	_ = gnb2.conn.Abort()

	upd = expect(t, amf, "initiating", ngap.ProcRANConfigurationUpdate, ngap.ParseRANConfigurationUpdate)
	assertTAs(t, upd.SupportedTAList, ngap.SupportedTAList{ta(t, 1, "00101")})

	amf.send(t, &ngap.RANConfigurationUpdateAcknowledge{})

	g2 := guami(t, "00101", 2, 1, 1)
	sst2 := ngap.SliceSupportItem{SNSSAI: ngap.SNSSAI{SST: 2}}
	amf.sendRaw(t, amfConfigurationUpdateWithPLMNSupport(t,
		&ngap.AMFConfigurationUpdate{
			AMFName: ngap.Ptr("amf1b"), RelativeAMFCapacity: ngap.Ptr(uint8(20)),
			ServedGUAMIList: ngap.ServedGUAMIList{{GUAMI: g1}, {GUAMI: g2}},
		},
		ngap.PLMNSupportList{{PLMNIdentity: plmnID(t, "001", "01"), SliceSupportList: ngap.SliceSupportList{slice, sst2}}}))
	expect(t, amf, "successful", ngap.ProcAMFConfigurationUpdate, ngap.ParseAMFConfigurationUpdateAcknowledge)

	f := gnb.nextFrame(t)

	m, ok := f.pdu.(*ngap.InitiatingMessage)
	if !ok || m.ProcedureCode != ngap.ProcAMFConfigurationUpdate {
		t.Fatalf("got %s, want AMF Configuration Update", procedureName(f.pdu))
	}

	var name ngap.Name
	if p, err := ngap.ParseRawPDU(f.raw); err != nil {
		t.Fatal(err)
	} else if ok, err := p.Get(ngap.IDAMFName, &name); err != nil || !ok {
		t.Fatalf("AMF Name = %v, %v", ok, err)
	}

	assertNodeName(t, ngap.Ptr(string(name)))

	support := plmnSupportOf(t, f.raw)
	if len(support) != 1 || !reflect.DeepEqual(support[0].SliceSupportList, ngap.SliceSupportList{slice, sst2}) {
		t.Fatalf("PLMN Support List = %+v", support)
	}

	acu, err := ngap.ParseRawPDU(f.raw)
	if err != nil {
		t.Fatal(err)
	}

	var served ngap.ServedGUAMIList
	if ok, err := acu.Get(ngap.IDServedGUAMIList, &served); err != nil || !ok ||
		!reflect.DeepEqual(guamis(t, served), []ngap.GUAMI{g1, g2}) {
		t.Fatalf("Served GUAMI List = %+v, %v", served, err)
	}

	gnb.send(t, &ngap.AMFConfigurationUpdateAcknowledge{})

	st, _ = h.gw.AMFStatus(1, []string{"127.0.1.2"})
	if st.AMFName != "amf1b" || *st.RelativeAMFCapacity != 20 || len(st.ServedGUAMIs) != 2 ||
		len(st.PLMNSupport) != 1 || len(st.PLMNSupport[0].Slices) != 2 {
		t.Fatalf("AMF status after update = %+v", st)
	}

	gnb.quiet(t, 300*time.Millisecond)

	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(h.gw.Collectors()...)

	metrics := `
# HELP ellamocn_core_associations Core (MME, AMF) associations of the configured MMEs and AMFs, by interface (s1, ng), operator and state (disconnected: no SCTP association; connected: SCTP association up, S1 or NG Setup not done; up: S1 or NG Setup succeeded; setup_failed: the MME or AMF rejected the setup).
# TYPE ellamocn_core_associations gauge
ellamocn_core_associations{interface="ng",operator="A",state="connected"} 0
ellamocn_core_associations{interface="ng",operator="A",state="disconnected"} 0
ellamocn_core_associations{interface="ng",operator="A",state="setup_failed"} 0
ellamocn_core_associations{interface="ng",operator="A",state="up"} 1
# HELP ellamocn_radio_associations Radio (eNodeB, gNodeB) associations, by interface (s1, ng) and state (connected: SCTP association up, no successful S1 or NG Setup; up: S1 or NG Setup succeeded).
# TYPE ellamocn_radio_associations gauge
ellamocn_radio_associations{interface="ng",state="connected"} 0
ellamocn_radio_associations{interface="ng",state="up"} 1
# HELP ellamocn_setup_attempts_total Setup procedures (S1 Setup, NG Setup), by interface (s1, ng), side and result. side=radio: received from radios; result success, invalid (undecodable or invalid request), unknown_plmn (no operator serves a broadcast PLMN), no_mme, no_amf (no MME or AMF of the serving operators is up), mme_failure, amf_failure (the serving operators' MMEs or AMFs rejected the setup), error (the response could not be built or sent, or the radio left before the answer). side=core: sent to MMEs and AMFs; result success, failure (the MME or AMF rejected the request or answered with an invalid response), timeout (no answer from the MME or AMF), error (the request could not be built or sent, or the association dropped before the answer).
# TYPE ellamocn_setup_attempts_total counter
ellamocn_setup_attempts_total{interface="ng",result="amf_failure",side="radio"} 0
ellamocn_setup_attempts_total{interface="ng",result="error",side="core"} 0
ellamocn_setup_attempts_total{interface="ng",result="error",side="radio"} 0
ellamocn_setup_attempts_total{interface="ng",result="failure",side="core"} 0
ellamocn_setup_attempts_total{interface="ng",result="invalid",side="radio"} 0
ellamocn_setup_attempts_total{interface="ng",result="no_amf",side="radio"} 0
ellamocn_setup_attempts_total{interface="ng",result="success",side="core"} 1
ellamocn_setup_attempts_total{interface="ng",result="success",side="radio"} 2
ellamocn_setup_attempts_total{interface="ng",result="timeout",side="core"} 0
ellamocn_setup_attempts_total{interface="ng",result="unknown_plmn",side="radio"} 0
# HELP ellamocn_ue_connections UE-associated logical S1-connections (TS 36.413 clause 3.1) and NG-connections relayed, by interface (s1, ng) and operator.
# TYPE ellamocn_ue_connections gauge
ellamocn_ue_connections{interface="ng",operator="A"} 0
`
	if err := testutil.GatherAndCompare(registry, strings.NewReader(metrics), "ellamocn_core_associations",
		"ellamocn_radio_associations", "ellamocn_setup_attempts_total", "ellamocn_ue_connections"); err != nil {
		t.Fatal(err)
	}

	for _, c := range h.gw.Collectors() {
		if problems, err := testutil.CollectAndLint(c); err != nil || len(problems) > 0 {
			t.Fatalf("lint: %v %v", problems, err)
		}
	}
}

func TestNGSetupFailures(t *testing.T) {
	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.3")
	h := startGateway(t, amfs.port(), operatorA("127.0.1.3"), operatorB("127.0.1.4"))
	amf := amfs.accept(t)

	gnb := h.connectGNB(t)
	gnb.send(t, setupRequest(t, 1, "gnb1", ngap.PagingDRXv64, ta(t, 1, "00301")))

	fail := expect(t, gnb, "unsuccessful", ngap.ProcNGSetup, ngap.ParseNGSetupFailure)
	if fail.Cause == nil || fail.Cause.Group != ngap.CauseGroupMisc || fail.Cause.Value != ngap.CauseMiscUnknownPLMNOrSNPN {
		t.Fatalf("cause = %v, want unknown PLMN", fail.Cause)
	}

	amf.quiet(t, 200*time.Millisecond)

	enb := setupRequest(t, 1, "ng-enb", ngap.PagingDRXv64, ta(t, 1, "00101"))
	enb.GlobalRANNodeID = ngap.GlobalRANNodeID{Kind: ngap.RANNodeIDMacroNgENB, PLMNIdentity: plmnID(t, "001", "01"), Value: 1, Bits: 20}
	gnb.send(t, enb)

	fail = expect(t, gnb, "unsuccessful", ngap.ProcNGSetup, ngap.ParseNGSetupFailure)
	if fail.Cause == nil || fail.Cause.Group != ngap.CauseGroupProtocol || fail.Cause.Value != ngap.CauseProtocolSemanticError {
		t.Fatalf("cause = %v, want semantic error", fail.Cause)
	}

	gnb.send(t, setupRequest(t, 1, "gnb1", ngap.PagingDRXv64, ta(t, 1, "002002")))

	fail = expect(t, gnb, "unsuccessful", ngap.ProcNGSetup, ngap.ParseNGSetupFailure)
	if fail.Cause == nil || fail.Cause.Group != ngap.CauseGroupTransport || fail.TimeToWait == nil || *fail.TimeToWait != ngap.TimeToWaitV5s {
		t.Fatalf("NG Setup Failure = %+v, want transport resource unavailable and 5 s", fail)
	}

	gnb.send(t, setupRequest(t, 1, "gnb1", ngap.PagingDRXv64, ta(t, 1, "00101")))
	expect(t, amf, "initiating", ngap.ProcNGSetup, ngap.ParseNGSetupRequest)
	amf.send(t, &ngap.NGSetupFailure{
		Cause:      &ngap.Cause{Group: ngap.CauseGroupMisc, Value: ngap.CauseMiscUnknownPLMNOrSNPN},
		TimeToWait: ngap.Ptr(ngap.TimeToWaitV10s),
	})

	fail = expect(t, gnb, "unsuccessful", ngap.ProcNGSetup, ngap.ParseNGSetupFailure)
	if fail.Cause == nil || fail.Cause.Value != ngap.CauseMiscUnknownPLMNOrSNPN || fail.TimeToWait == nil ||
		*fail.TimeToWait != ngap.TimeToWaitV10s {
		t.Fatalf("NG Setup Failure = %+v, want the AMF's cause and time to wait", fail)
	}

	st, _ := h.gw.AMFStatus(1, []string{"127.0.1.3"})
	if st.State != ng.AMFSetupFailed {
		t.Fatalf("AMF state = %s", st.State)
	}
}

func TestNGSetupTimeout(t *testing.T) {
	skipWithoutSCTP(t)

	amfs := listenAMF(t, "127.0.1.5")
	h := startGateway(t, amfs.port(), operatorA("127.0.1.5"))
	amf := amfs.accept(t)

	gnb := h.connectGNB(t)
	gnb.send(t, setupRequest(t, 1, "gnb1", ngap.PagingDRXv64, ta(t, 1, "00101")))

	expect(t, amf, "initiating", ngap.ProcNGSetup, ngap.ParseNGSetupRequest)
	expect(t, gnb, "unsuccessful", ngap.ProcNGSetup, ngap.ParseNGSetupFailure)

	h.eventually(t, "the AMF association to drop", func() bool {
		st, _ := h.gw.AMFStatus(1, []string{"127.0.1.5"})
		return st.State == ng.AMFDisconnected
	})

	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(h.gw.Collectors()...)

	h.eventually(t, "setup attempts", func() bool {
		return testutil.ToFloat64(counter(t, registry, "radio", "no_amf")) == 1 &&
			testutil.ToFloat64(counter(t, registry, "core", "timeout")) == 1
	})
}

func counter(t *testing.T, registry *prometheus.Registry, side, result string) prometheus.Collector {
	t.Helper()

	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}

	for _, f := range families {
		if f.GetName() != "ellamocn_setup_attempts_total" {
			continue
		}

		for _, m := range f.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}

			if labels["interface"] == "ng" && labels["side"] == side && labels["result"] == result {
				return prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "x"}, func() float64 { return m.GetCounter().GetValue() })
			}
		}
	}

	t.Fatalf("no setup attempts for %s/%s", side, result)

	return nil
}

//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

type lab5G struct {
	operator string
	plmn     plmn
	amf      string
	core     string
	gnb      string
	ue       string
	radio    string
	imsi     string
	ueNet    string
}

var labs5G = []lab5G{
	{
		operator: "open5gs", plmn: plmn{MCC: "001", MNC: "01"}, amf: "10.90.0.12", core: "open5gs-5g", gnb: "gnb",
		ue: "srsue-5g", radio: "gNB:001-01:000001@22", imsi: "001010000000011", ueNet: "10.47.",
	},
	{
		operator: "open5gs-b", plmn: plmn{MCC: "001", MNC: "02"}, amf: "10.90.0.13", core: "open5gs-5g-b", gnb: "gnb-b",
		ue: "srsue-5g-b", radio: "gNB:001-02:000002@22", imsi: "001020000000012", ueNet: "10.48.",
	},
}

type amfStatus struct {
	State   string `json:"state"`
	AMFName string `json:"amf_name"`
}

type amf struct {
	TNLAddresses []string   `json:"tnl_addresses"`
	Status       *amfStatus `json:"status,omitempty"`
}

func amfLogFile(core string) []string {
	return []string{"exec", "-T", core, "cat", "/var/log/open5gs/amf.log"}
}

func amfLog(ctx context.Context, core, what string) func() error {
	return logContains(ctx, what, amfLogFile(core)...)
}

func amfUp(ctx context.Context, c *client, operatorName string) func() error {
	return func() error {
		var got struct {
			AMFs []amf `json:"amfs"`
		}

		if _, err := c.do(ctx, http.MethodGet, "/api/v1/operators/"+operatorName, nil, &got); err != nil {
			return err
		}

		if len(got.AMFs) != 1 || got.AMFs[0].Status == nil || got.AMFs[0].Status.State != "up" {
			return fmt.Errorf("AMFs = %+v", got.AMFs)
		}

		return nil
	}
}

func radioUp(ctx context.Context, c *client, ref string) func() error {
	return func() error {
		var r radio
		if _, err := c.do(ctx, http.MethodGet, "/api/v1/radios/"+ref, nil, &r); err != nil {
			return err
		}

		if r.Type != "gNB" || r.State != "up" {
			return fmt.Errorf("radio = %+v", r)
		}

		return nil
	}
}

func radioUEs(ctx context.Context, c *client, ref string) ([]ueConnection, error) {
	var list struct {
		Items []ueConnection `json:"items"`
	}

	_, err := c.do(ctx, http.MethodGet, "/api/v1/ue-connections?radio="+ref, nil, &list)

	return list.Items, err
}

func metricsText(ctx context.Context, c *client) (string, error) {
	var text string

	_, err := c.do(ctx, http.MethodGet, "/api/v1/metrics", nil, &text)

	return text, err
}

func srsueAddress(ctx context.Context, service string) (string, error) {
	out, err := compose(ctx, "logs", "--no-color", service)
	if err != nil {
		return "", err
	}

	const marker = "PDU Session Establishment successful. IP: "

	i := strings.LastIndex(out, marker)
	if i < 0 {
		return "", errors.New("srsue has no PDU session")
	}

	return strings.Fields(out[i+len(marker):])[0], nil
}

func ngRegistered(ctx context.Context, t *testing.T, l lab5G) {
	t.Helper()

	eventually(ctx, t, l.ue+" registered with a PDU session", 120*time.Second,
		logContains(ctx, "PDU Session Establishment successful. IP: "+l.ueNet, "logs", "--no-color", l.ue))
	eventually(ctx, t, l.core+" registration complete", 30*time.Second,
		amfLog(ctx, l.core, "[imsi-"+l.imsi+"] Registration complete"))
	eventually(ctx, t, l.gnb+" Initial Context Setup complete", 30*time.Second,
		logContains(ctx, ": InitialContextSetupResponse", "logs", "--no-color", l.gnb))
	eventually(ctx, t, "Ella MOCN relayed the Initial Context Setup outcome from "+l.radio, 10*time.Second,
		relayedToCore(ctx, "relayed NGAP message to AMF", l.radio, "InitialContextSetup (14)"))
}

func TestNGRegistrationAndPaging(t *testing.T) {
	ctx := t.Context()
	c := setUp(ctx, t)

	for _, l := range labs5G {
		t.Run(l.operator, func(t *testing.T) {
			t.Parallel()
			ngLifecycle(t.Context(), t, c, l)
		})
	}
}

func ngLifecycle(ctx context.Context, t *testing.T, c *client, l lab5G) {
	if _, err := compose(ctx, "up", "-d", l.gnb); err != nil {
		t.Fatal(err)
	}

	eventually(ctx, t, l.radio+" up", 120*time.Second, radioUp(ctx, c, l.radio))
	eventually(ctx, t, l.operator+" AMF up", 30*time.Second, amfUp(ctx, c, l.operator))
	eventually(ctx, t, l.core+" AMF accepted Ella MOCN", 30*time.Second, amfLog(ctx, l.core, "gNB-N2 accepted[10.90.0.30]"))

	if _, err := compose(ctx, "up", "-d", l.ue); err != nil {
		t.Fatal(err)
	}

	ngRegistered(ctx, t, l)

	if _, err := compose(ctx, "exec", "-T", l.ue, "ping", "-c", "1", "-w", "15", "-I", "tun_srsue", l.ueNet+"0.1"); err != nil {
		t.Fatalf("%s user plane: %v", l.operator, err)
	}

	eventually(ctx, t, l.operator+" UE connection in /api/v1/ue-connections", 10*time.Second, func() error {
		items, err := radioUEs(ctx, c, l.radio)
		if err != nil {
			return err
		}

		if len(items) != 1 {
			return fmt.Errorf("want 1 UE connection, got %+v", items)
		}

		u := items[0]
		if u.Interface != "ng" || u.Operator != l.operator || u.PLMN != l.plmn || u.AMF == nil ||
			len(u.AMF.TNLAddresses) != 1 || u.AMF.TNLAddresses[0] != l.amf || u.AMF.AMFUENGAPID == nil ||
			u.TAI == nil || u.TAI.TAC != "000001" || u.NRCGI == nil {
			return fmt.Errorf("UE connection = %+v", u)
		}

		return nil
	})

	eventually(ctx, t, l.operator+" UE context released on inactivity", 120*time.Second, func() error {
		items, err := radioUEs(ctx, c, l.radio)
		if err != nil {
			return err
		}

		if len(items) != 0 {
			return fmt.Errorf("%d UE connections left", len(items))
		}

		return nil
	})

	addr, err := srsueAddress(ctx, l.ue)
	if err != nil {
		t.Fatal(err)
	}

	idle := logMark(ctx, t, "logs", "--no-color", l.gnb)

	_, _ = compose(ctx, "exec", "-T", l.core, "ping", "-c", "3", "-W", "2", addr)

	eventually(ctx, t, l.gnb+" paged", 30*time.Second, logContainsAfter(ctx, "Rx PDU: Paging", idle, "logs", "--no-color", l.gnb))
}

func TestNGDeregistration(t *testing.T) {
	ctx := t.Context()
	c := setUp(ctx, t)

	l := lab5G{
		operator: "open5gs", plmn: plmn{MCC: "001", MNC: "01"}, amf: "10.90.0.12", core: "open5gs-5g", gnb: "gnb-c",
		ue: "srsue-5g-c", radio: "gNB:001-01:000003@22", imsi: "001010000000021", ueNet: "10.47.",
	}

	if _, err := compose(ctx, "up", "-d", l.gnb); err != nil {
		t.Fatal(err)
	}

	eventually(ctx, t, l.radio+" up", 120*time.Second, radioUp(ctx, c, l.radio))
	eventually(ctx, t, l.operator+" AMF up", 30*time.Second, amfUp(ctx, c, l.operator))

	if _, err := compose(ctx, "up", "-d", l.ue); err != nil {
		t.Fatal(err)
	}

	ngRegistered(ctx, t, l)

	eventually(ctx, t, l.ue+" UE connection", 10*time.Second, func() error {
		items, err := radioUEs(ctx, c, l.radio)
		if err != nil {
			return err
		}

		if len(items) != 1 || items[0].Operator != l.operator || items[0].Interface != "ng" {
			return fmt.Errorf("UE connections = %+v", items)
		}

		return nil
	})

	registered := logMark(ctx, t, amfLogFile(l.core)...)
	connected := logMark(ctx, t, "logs", "--no-color", l.gnb)

	if _, err := compose(ctx, "exec", "-T", l.core, "curl", "-sS", "-f", "--http2-prior-knowledge", "-X", "POST",
		"-H", "Content-Type: application/json", "-d", `{"deregReason":"SUBSCRIPTION_WITHDRAWN","accessType":"3GPP_ACCESS"}`,
		"http://127.0.0.5:7777/namf-callback/v1/imsi-"+l.imsi+"/dereg-notify"); err != nil {
		t.Fatal(err)
	}

	eventually(ctx, t, l.core+" network-initiated deregistration", 30*time.Second, func() error {
		if err := logContainsAfter(ctx, "Deregistration notify reason: imsi-"+l.imsi+":SUBSCRIPTION_WITHDRAWN", registered, amfLogFile(l.core)...)(); err != nil {
			return err
		}

		return logContainsAfter(ctx, "[imsi-"+l.imsi+"] Explicit De-registered", registered, amfLogFile(l.core)...)()
	})

	eventually(ctx, t, l.gnb+" UE context released after deregistration", 60*time.Second,
		logContainsAfter(ctx, `"UE Context Release Procedure" with cause=nas-normal_release`, connected, "logs", "--no-color", l.gnb))

	eventually(ctx, t, "UE connection released after deregistration", 30*time.Second, func() error {
		items, err := radioUEs(ctx, c, l.radio)
		if err != nil {
			return err
		}

		if len(items) != 0 {
			return fmt.Errorf("%d UE connections left", len(items))
		}

		return nil
	})

	if _, err := compose(ctx, "stop", "-t", "1", l.ue, l.gnb); err != nil {
		t.Fatal(err)
	}
}

func TestNGMetrics(t *testing.T) {
	ctx := t.Context()
	c := setUp(ctx, t)

	eventually(ctx, t, "NG metrics", 30*time.Second, func() error {
		text, err := metricsText(ctx, c)
		if err != nil {
			return err
		}

		for _, m := range []struct {
			name   string
			labels map[string]string
			min    float64
		}{
			{"ellamocn_setup_attempts_total", map[string]string{"interface": "ng", "side": "radio", "result": "success"}, 2},
			{"ellamocn_setup_attempts_total", map[string]string{"interface": "ng", "side": "core", "result": "success"}, 2},
			{"ellamocn_initial_ue_messages_total", map[string]string{"interface": "ng", "result": "relayed"}, 2},
			{"ellamocn_paging_messages_total", map[string]string{"interface": "ng", "result": "relayed"}, 2},
			{"ellamocn_radio_associations", map[string]string{"interface": "ng", "state": "up"}, 2},
			{"ellamocn_overload_messages_total", map[string]string{"interface": "ng", "procedure": "overload_start", "result": "relayed"}, 0},
		} {
			if v, ok := metric(text, m.name, m.labels); !ok || v < m.min {
				return fmt.Errorf("%s%v = %v, %v", m.name, m.labels, v, ok)
			}
		}

		for _, l := range labs5G {
			core := map[string]string{"interface": "ng", "operator": l.operator, "state": "up"}
			if v, ok := metric(text, "ellamocn_core_associations", core); !ok || v != 1 {
				return fmt.Errorf("ellamocn_core_associations%v = %v, %v", core, v, ok)
			}
		}

		return nil
	})
}

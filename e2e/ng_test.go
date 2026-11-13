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
		operator: "open5gs", plmn: plmn{MCC: "001", MNC: "01"}, amf: "10.90.0.12", core: "open5gs-5g", gnb: "srsgnb",
		ue: "srsue-5g", radio: "001-01:gnb22:1", imsi: "001010000000011", ueNet: "10.47.",
	},
	{
		operator: "open5gs-b", plmn: plmn{MCC: "001", MNC: "02"}, amf: "10.90.0.13", core: "open5gs-5g-b", gnb: "srsgnb-b",
		ue: "srsue-5g-b", radio: "001-02:gnb22:2", imsi: "001020000000012", ueNet: "10.48.",
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

func amfLog(ctx context.Context, core, what string) func() error {
	return logContains(ctx, what, "exec", "-T", core, "cat", "/var/log/open5gs/amf.log")
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

		if r.Interface != "ng" || r.State != "up" {
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

func TestNGRegistrationAndPaging(t *testing.T) {
	ctx := t.Context()
	c := setUp(ctx, t)

	for _, l := range labs5G {
		t.Run(l.operator, func(t *testing.T) {
			t.Parallel()
			ngLifecycle(t.Context(), t, c, l)
		})
	}

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

	eventually(ctx, t, l.ue+" registered with a PDU session", 120*time.Second,
		logContains(ctx, "PDU Session Establishment successful. IP: "+l.ueNet, "logs", "--no-color", l.ue))
	eventually(ctx, t, l.core+" registration complete", 30*time.Second,
		amfLog(ctx, l.core, "[imsi-"+l.imsi+"] Registration complete"))

	if _, err := compose(ctx, "exec", "-T", l.ue, "ping", "-c", "3", "-W", "5", "-I", "tun_srsue", l.ueNet+"0.1"); err != nil {
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
			u.TAI == nil || u.TAI.TAC != 1 || u.NRCGI == nil {
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

	_, _ = compose(ctx, "exec", "-T", l.core, "ping", "-c", "3", "-W", "2", addr)

	eventually(ctx, t, l.gnb+" paged", 30*time.Second, logContains(ctx, "Rx PDU: Paging", "logs", "--no-color", l.gnb))
}

func TestNGDeregistration(t *testing.T) {
	ctx := t.Context()
	c := setUp(ctx, t)

	const (
		ref  = "001-01:gnb22:3"
		imsi = "001010000000021"
	)

	if _, err := compose(ctx, "up", "-d", "ueransim"); err != nil {
		t.Fatal(err)
	}

	eventually(ctx, t, ref+" up", 120*time.Second, radioUp(ctx, c, ref))
	eventually(ctx, t, "open5gs AMF up", 30*time.Second, amfUp(ctx, c, "open5gs"))

	eventually(ctx, t, "UERANSIM UE registered", 60*time.Second,
		logContains(ctx, "PDU Session establishment is successful", "logs", "--no-color", "ueransim"))

	eventually(ctx, t, "UERANSIM UE connection", 10*time.Second, func() error {
		items, err := radioUEs(ctx, c, ref)
		if err != nil {
			return err
		}

		if len(items) != 1 || items[0].Operator != "open5gs" || items[0].Interface != "ng" {
			return fmt.Errorf("UE connections = %+v", items)
		}

		return nil
	})

	if _, err := compose(ctx, "exec", "-T", "ueransim", "nr-cli", "imsi-"+imsi, "-e", "deregister switch-off"); err != nil {
		t.Fatal(err)
	}

	eventually(ctx, t, "AMF deregistration", 30*time.Second, amfLog(ctx, "open5gs-5g", "[imsi-"+imsi+"] Deregistration request"))

	eventually(ctx, t, "UE connection released after deregistration", 30*time.Second, func() error {
		items, err := radioUEs(ctx, c, ref)
		if err != nil {
			return err
		}

		if len(items) != 0 {
			return fmt.Errorf("%d UE connections left", len(items))
		}

		return nil
	})
}

//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	operatorName = "open5gs"
	mmeAddress   = "10.90.0.10"
	adminEmail   = "admin@ellanetworks.com"
	adminPass    = "e2e-password-123"
)

type client struct {
	base  string
	token string
	http  *http.Client
}

func newClient() *client {
	base := os.Getenv("E2E_API")
	if base == "" {
		base = "http://127.0.0.1:5032"
	}

	return &client{base: base, http: &http.Client{Timeout: 10 * time.Second}}
}

func (c *client) do(ctx context.Context, method, path string, body any, out any) (int, error) {
	var r io.Reader

	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}

		r = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return 0, err
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, err
	}

	if resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("%s %s: %d: %s", method, path, resp.StatusCode, raw)
	}

	if out == nil {
		return resp.StatusCode, nil
	}

	if s, ok := out.(*string); ok {
		*s = string(raw)
		return resp.StatusCode, nil
	}

	var envelope struct {
		Result json.RawMessage `json:"result"`
	}

	if err := json.Unmarshal(raw, &envelope); err != nil {
		return resp.StatusCode, err
	}

	return resp.StatusCode, json.Unmarshal(envelope.Result, out)
}

func eventually(t *testing.T, what string, timeout time.Duration, check func() error) {
	t.Helper()

	deadline := time.Now().Add(timeout)

	for {
		err := check()
		if err == nil {
			t.Logf("ready: %s", what)
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s: %v", timeout, what, err)
		}

		time.Sleep(2 * time.Second)
	}
}

type plmn struct {
	MCC string `json:"mcc"`
	MNC string `json:"mnc"`
}

type globalENBID struct {
	PLMN      plmn   `json:"plmn"`
	ENBIDType string `json:"enb_id_type"`
	ENBID     int    `json:"enb_id"`
}

type mmeStatus struct {
	State             string `json:"state"`
	PrimaryTNLAddress string `json:"primary_tnl_address"`
	MMEName           string `json:"mme_name"`
}

type coreNode struct {
	TNLAddresses []string   `json:"tnl_addresses"`
	Status       *mmeStatus `json:"status,omitempty"`
}

type operator struct {
	Name        string       `json:"name"`
	PLMNs       []plmn       `json:"plmns"`
	GlobalENBID *globalENBID `json:"global_enb_id"`
	MMEs        []coreNode   `json:"mmes"`
}

type ueConnection struct {
	Radio struct {
		Ref         string `json:"ref"`
		ENBUES1APID int    `json:"enb_ue_s1ap_id"`
		MMEUES1APID int    `json:"mme_ue_s1ap_id"`
	} `json:"radio"`
	MME struct {
		TNLAddresses []string `json:"tnl_addresses"`
		ENBUES1APID  int      `json:"enb_ue_s1ap_id"`
		MMEUES1APID  *int     `json:"mme_ue_s1ap_id"`
	} `json:"mme"`
	Operator string `json:"operator"`
	PLMN     plmn   `json:"plmn"`
	TAI      *struct {
		TAC int `json:"tac"`
	} `json:"tai"`
}

type radio struct {
	Ref           string       `json:"ref"`
	GlobalENBID   *globalENBID `json:"global_enb_id"`
	ENBName       string       `json:"enb_name"`
	State         string       `json:"state"`
	TNLAddresses  []string     `json:"tnl_addresses"`
	UEConnections int          `json:"ue_connections"`
}

func metric(text, name string, labels map[string]string) (float64, bool) {
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, name+"{") {
			continue
		}

		end := strings.LastIndex(line, "}")
		if end < 0 {
			continue
		}

		matched := true

		for k, v := range labels {
			if !strings.Contains(line[:end], fmt.Sprintf("%s=%q", k, v)) {
				matched = false
				break
			}
		}

		if !matched {
			continue
		}

		f, err := strconv.ParseFloat(strings.TrimSpace(line[end+1:]), 64)
		if err != nil {
			continue
		}

		return f, true
	}

	return 0, false
}

func setUp(ctx context.Context, t *testing.T) *client {
	t.Helper()

	c := newClient()

	eventually(t, "Ella MOCN API", 120*time.Second, func() error {
		_, err := c.do(ctx, http.MethodGet, "/api/v1/status", nil, nil)
		return err
	})

	var login struct {
		Token string `json:"token"`
	}

	creds := map[string]string{"email": adminEmail, "password": adminPass}

	status, err := c.do(ctx, http.MethodPost, "/api/v1/init", creds, &login)
	if status == http.StatusForbidden {
		_, err = c.do(ctx, http.MethodPost, "/api/v1/auth/login", creds, &login)
	}

	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}

	c.token = login.Token

	op := operator{
		Name:  operatorName,
		PLMNs: []plmn{{MCC: "001", MNC: "01"}},
		GlobalENBID: &globalENBID{
			PLMN:      plmn{MCC: "001", MNC: "01"},
			ENBIDType: "macro",
			ENBID:     1,
		},
		MMEs: []coreNode{{TNLAddresses: []string{mmeAddress}}},
	}

	status, err = c.do(ctx, http.MethodPost, "/api/v1/operators", op, nil)
	if status == http.StatusConflict {
		_, err = c.do(ctx, http.MethodPut, "/api/v1/operators/"+operatorName, op, nil)
	}

	if err != nil {
		t.Fatalf("configure operator: %v", err)
	}

	return c
}

func TestS1Setup(t *testing.T) {
	ctx := context.Background()
	c := setUp(ctx, t)

	eventually(t, "radio up", 120*time.Second, func() error {
		var nodes struct {
			Items []radio `json:"items"`
		}

		if _, err := c.do(ctx, http.MethodGet, "/api/v1/radios", nil, &nodes); err != nil {
			return err
		}

		if len(nodes.Items) != 1 {
			return fmt.Errorf("want 1 radio, got %+v", nodes.Items)
		}

		if n := nodes.Items[0]; n.State != "up" {
			return fmt.Errorf("radio not up: %+v", n)
		}

		return nil
	})

	var one radio
	if _, err := c.do(ctx, http.MethodGet, "/api/v1/radios/001-01:macro:411", nil, &one); err != nil ||
		one.Ref != "001-01:macro:411" || one.State != "up" {
		t.Fatalf("GET radio by ref = %+v, %v", one, err)
	}

	eventually(t, "MME up", 120*time.Second, func() error {
		var got operator
		if _, err := c.do(ctx, http.MethodGet, "/api/v1/operators/"+operatorName, nil, &got); err != nil {
			return err
		}

		if len(got.MMEs) != 1 {
			return fmt.Errorf("want 1 MME, got %+v", got.MMEs)
		}

		s := got.MMEs[0].Status
		if s == nil {
			return errors.New("MME has no status")
		}

		if s.State != "up" {
			return fmt.Errorf("MME not up: %+v", *s)
		}

		return nil
	})

	eventually(t, "S1 Setup metrics", 30*time.Second, func() error {
		var text string
		if _, err := c.do(ctx, http.MethodGet, "/api/v1/metrics", nil, &text); err != nil {
			return err
		}

		for _, side := range []string{"radio", "core"} {
			labels := map[string]string{"interface": "s1", "side": side, "result": "success"}

			v, ok := metric(text, "ellamocn_setup_attempts_total", labels)
			if !ok {
				return fmt.Errorf("ellamocn_setup_attempts_total%v missing", labels)
			}

			if v < 1 {
				return fmt.Errorf("ellamocn_setup_attempts_total%v = %v", labels, v)
			}
		}

		radios := map[string]string{"interface": "s1", "state": "up"}
		if v, ok := metric(text, "ellamocn_radio_associations", radios); !ok || v != 1 {
			return fmt.Errorf("ellamocn_radio_associations%v = %v, %v", radios, v, ok)
		}

		core := map[string]string{"interface": "s1", "operator": operatorName, "state": "up"}
		if v, ok := metric(text, "ellamocn_core_associations", core); !ok || v != 1 {
			return fmt.Errorf("ellamocn_core_associations%v = %v, %v", core, v, ok)
		}

		return nil
	})

	eventually(t, "open5gs MME accepted the eNB", 30*time.Second, func() error {
		out, err := exec.CommandContext(ctx, "docker", "compose", "exec", "-T", "open5gs", "cat", "/var/log/open5gs/mme.log").CombinedOutput()
		if err != nil {
			return fmt.Errorf("read MME log: %w: %s", err, out)
		}

		if !strings.Contains(string(out), "eNB-S1 accepted[10.90.0.30]") {
			return errors.New("open5gs MME has no S1 association from Ella MOCN")
		}

		return nil
	})
}

func compose(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", append([]string{"compose"}, args...)...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker compose %s: %w: %s", strings.Join(args, " "), err, out)
	}

	return string(out), nil
}

func logContains(ctx context.Context, what string, args ...string) func() error {
	return func() error {
		out, err := compose(ctx, args...)
		if err != nil {
			return err
		}

		if !strings.Contains(out, what) {
			return fmt.Errorf("%q not logged yet", what)
		}

		return nil
	}
}

func mmeLog(ctx context.Context, what string) func() error {
	return logContains(ctx, what, "exec", "-T", "open5gs", "cat", "/var/log/open5gs/mme.log")
}

func ueConnections(ctx context.Context, c *client) ([]ueConnection, error) {
	var list struct {
		Items []ueConnection `json:"items"`
	}

	_, err := c.do(ctx, http.MethodGet, "/api/v1/ue-connections", nil, &list)

	return list.Items, err
}

func ping(ctx context.Context) error {
	_, err := compose(ctx, "exec", "-T", "srsue", "ping", "-c", "3", "-W", "5", "-I", "tun_srsue", "10.45.0.1")
	return err
}

func TestUEAttachServiceRequestDetach(t *testing.T) {
	ctx := context.Background()
	c := setUp(ctx, t)

	eventually(t, "srsue attached with an IP", 180*time.Second,
		logContains(ctx, "Network attach successful. IP: 10.45.", "logs", "--no-color", "srsue"))
	eventually(t, "open5gs MME attach complete", 30*time.Second, mmeLog(ctx, "[001010000000001] Attach complete"))
	eventually(t, "user plane up", 30*time.Second, func() error { return ping(ctx) })

	eventually(t, "UE connection in /api/v1/ue-connections", 10*time.Second, func() error {
		items, err := ueConnections(ctx, c)
		if err != nil {
			return err
		}

		if len(items) != 1 {
			return fmt.Errorf("want 1 UE connection, got %+v", items)
		}

		u := items[0]
		if u.Operator != operatorName || u.PLMN != (plmn{MCC: "001", MNC: "01"}) || u.Radio.Ref != "001-01:macro:411" ||
			len(u.MME.TNLAddresses) != 1 || u.MME.TNLAddresses[0] != mmeAddress || u.MME.MMEUES1APID == nil ||
			u.TAI == nil || u.TAI.TAC != 1 {
			return fmt.Errorf("UE connection = %+v", u)
		}

		var r radio
		if _, err := c.do(ctx, http.MethodGet, "/api/v1/radios/001-01:macro:411", nil, &r); err != nil {
			return err
		}

		if r.UEConnections != 1 {
			return fmt.Errorf("radio ue_connections = %d, want 1", r.UEConnections)
		}

		return nil
	})

	eventually(t, "UE context released on inactivity", 120*time.Second, func() error {
		items, err := ueConnections(ctx, c)
		if err != nil {
			return err
		}

		if len(items) != 0 {
			return fmt.Errorf("%d UE connections left", len(items))
		}

		return mmeLog(ctx, "Number of eNB-UEs is now 0")()
	})

	if err := ping(ctx); err != nil {
		t.Fatalf("ping after release: %v", err)
	}

	eventually(t, "open5gs MME service request", 30*time.Second, mmeLog(ctx, "[001010000000001] Service request"))

	if items, err := ueConnections(ctx, c); err != nil || len(items) != 1 {
		t.Fatalf("UE connections after service request = %+v, %v", items, err)
	}

	if _, err := compose(ctx, "exec", "-T", "srsue", "pkill", "-INT", "srsue"); err != nil {
		t.Fatal(err)
	}

	eventually(t, "open5gs MME detach", 30*time.Second, mmeLog(ctx, "Detach request"))

	eventually(t, "UE context released after detach", 30*time.Second, func() error {
		items, err := ueConnections(ctx, c)
		if err != nil {
			return err
		}

		if len(items) != 0 {
			return fmt.Errorf("%d UE connections left", len(items))
		}

		return nil
	})

	eventually(t, "relay metrics", 10*time.Second, func() error {
		var text string
		if _, err := c.do(ctx, http.MethodGet, "/api/v1/metrics", nil, &text); err != nil {
			return err
		}

		relayed := map[string]string{"interface": "s1", "result": "relayed"}
		if v, ok := metric(text, "ellamocn_initial_ue_messages_total", relayed); !ok || v < 2 {
			return fmt.Errorf("relayed INITIAL UE MESSAGEs = %v", v)
		}

		ues := map[string]string{"interface": "s1", "operator": operatorName}
		if v, ok := metric(text, "ellamocn_ue_connections", ues); !ok || v != 0 {
			return fmt.Errorf("ellamocn_ue_connections = %v, %v", v, ok)
		}

		return nil
	})
}

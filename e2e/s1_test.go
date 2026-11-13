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
	adminEmail = "admin@ellanetworks.com"
	adminPass  = "e2e-password-123"
)

type lab struct {
	operator string
	plmn     plmn
	enbID    int
	mme      string
	core     string
	radio    string
	srsue    string
	imsi     string
	ueNet    string
	pagedUE  bool
}

var labs = []lab{
	{
		operator: "open5gs", plmn: plmn{MCC: "001", MNC: "01"}, enbID: 1, mme: "10.90.0.10", core: "open5gs",
		radio: "001-01:macro:411", srsue: "srsue", imsi: "001010000000001", ueNet: "10.45.",
	},
	{
		operator: "open5gs-b", plmn: plmn{MCC: "001", MNC: "02"}, enbID: 2, mme: "10.90.0.11", core: "open5gs-b",
		radio: "001-02:macro:412", srsue: "srsue-b", imsi: "001020000000002", ueNet: "10.46.", pagedUE: true,
	},
}

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

	for _, l := range labs {
		op := operator{
			Name:        l.operator,
			PLMNs:       []plmn{l.plmn},
			GlobalENBID: &globalENBID{PLMN: l.plmn, ENBIDType: "macro", ENBID: l.enbID},
			MMEs:        []coreNode{{TNLAddresses: []string{l.mme}}},
		}

		status, err = c.do(ctx, http.MethodPost, "/api/v1/operators", op, nil)
		if status == http.StatusConflict {
			_, err = c.do(ctx, http.MethodPut, "/api/v1/operators/"+l.operator, op, nil)
		}

		if err != nil {
			t.Fatalf("configure operator %s: %v", l.operator, err)
		}
	}

	return c
}

func TestS1Setup(t *testing.T) {
	ctx := context.Background()
	c := setUp(ctx, t)

	eventually(t, "radios up", 120*time.Second, func() error {
		var nodes struct {
			Items []radio `json:"items"`
		}

		if _, err := c.do(ctx, http.MethodGet, "/api/v1/radios", nil, &nodes); err != nil {
			return err
		}

		if len(nodes.Items) != len(labs) {
			return fmt.Errorf("want %d radios, got %+v", len(labs), nodes.Items)
		}

		for _, n := range nodes.Items {
			if n.State != "up" {
				return fmt.Errorf("radio not up: %+v", n)
			}
		}

		return nil
	})

	for _, l := range labs {
		var one radio
		if _, err := c.do(ctx, http.MethodGet, "/api/v1/radios/"+l.radio, nil, &one); err != nil ||
			one.Ref != l.radio || one.State != "up" {
			t.Fatalf("GET radio by ref = %+v, %v", one, err)
		}

		eventually(t, l.operator+" MME up", 120*time.Second, func() error {
			var got operator
			if _, err := c.do(ctx, http.MethodGet, "/api/v1/operators/"+l.operator, nil, &got); err != nil {
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

		eventually(t, l.operator+" MME accepted Ella MOCN", 30*time.Second, mmeLog(ctx, l.core, "eNB-S1 accepted[10.90.0.30]"))
	}

	eventually(t, "S1 Setup metrics", 30*time.Second, func() error {
		var text string
		if _, err := c.do(ctx, http.MethodGet, "/api/v1/metrics", nil, &text); err != nil {
			return err
		}

		for side, want := range map[string]float64{"radio": float64(len(labs)), "core": float64(len(labs))} {
			labels := map[string]string{"interface": "s1", "side": side, "result": "success"}

			v, ok := metric(text, "ellamocn_setup_attempts_total", labels)
			if !ok {
				return fmt.Errorf("ellamocn_setup_attempts_total%v missing", labels)
			}

			if v < want {
				return fmt.Errorf("ellamocn_setup_attempts_total%v = %v", labels, v)
			}
		}

		radios := map[string]string{"interface": "s1", "state": "up"}
		if v, ok := metric(text, "ellamocn_radio_associations", radios); !ok || v != float64(len(labs)) {
			return fmt.Errorf("ellamocn_radio_associations%v = %v, %v", radios, v, ok)
		}

		for _, l := range labs {
			core := map[string]string{"interface": "s1", "operator": l.operator, "state": "up"}
			if v, ok := metric(text, "ellamocn_core_associations", core); !ok || v != 1 {
				return fmt.Errorf("ellamocn_core_associations%v = %v, %v", core, v, ok)
			}

			overloaded := map[string]string{"interface": "s1", "operator": l.operator}
			if v, ok := metric(text, "ellamocn_core_overloaded", overloaded); !ok || v != 0 {
				return fmt.Errorf("ellamocn_core_overloaded%v = %v, %v", overloaded, v, ok)
			}
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

func mmeLog(ctx context.Context, core, what string) func() error {
	return logContains(ctx, what, "exec", "-T", core, "cat", "/var/log/open5gs/mme.log")
}

func ueConnections(ctx context.Context, c *client) ([]ueConnection, error) {
	var list struct {
		Items []ueConnection `json:"items"`
	}

	_, err := c.do(ctx, http.MethodGet, "/api/v1/ue-connections", nil, &list)

	return list.Items, err
}

func ping(ctx context.Context, l lab) error {
	_, err := compose(ctx, "exec", "-T", l.srsue, "ping", "-c", "3", "-W", "5", "-I", "tun_srsue", l.ueNet+"0.1")
	return err
}

func ueAddress(ctx context.Context, l lab) (string, error) {
	out, err := compose(ctx, "logs", "--no-color", l.srsue)
	if err != nil {
		return "", err
	}

	const marker = "Network attach successful. IP: "

	i := strings.LastIndex(out, marker)
	if i < 0 {
		return "", errors.New("srsue not attached")
	}

	return strings.Fields(out[i+len(marker):])[0], nil
}

func operatorUEs(ctx context.Context, c *client, l lab) ([]ueConnection, error) {
	items, err := ueConnections(ctx, c)

	var out []ueConnection

	for _, u := range items {
		if u.Operator == l.operator {
			out = append(out, u)
		}
	}

	return out, err
}

func TestUEAttachServiceRequestDetach(t *testing.T) {
	ctx := context.Background()
	c := setUp(ctx, t)

	for _, l := range labs {
		t.Run(l.operator, func(t *testing.T) {
			t.Parallel()
			ueLifecycle(ctx, t, c, l)
		})
	}
}

func TestRelayMetrics(t *testing.T) {
	ctx := context.Background()
	c := setUp(ctx, t)

	eventually(t, "relay metrics", 10*time.Second, func() error {
		var text string
		if _, err := c.do(ctx, http.MethodGet, "/api/v1/metrics", nil, &text); err != nil {
			return err
		}

		relayed := map[string]string{"interface": "s1", "result": "relayed"}
		if v, ok := metric(text, "ellamocn_initial_ue_messages_total", relayed); !ok || v < float64(2*len(labs)) {
			return fmt.Errorf("relayed INITIAL UE MESSAGEs = %v", v)
		}

		if v, ok := metric(text, "ellamocn_paging_messages_total", relayed); !ok || v < 1 {
			return fmt.Errorf("relayed PAGING messages = %v", v)
		}

		for _, l := range labs {
			ues := map[string]string{"interface": "s1", "operator": l.operator}
			if v, ok := metric(text, "ellamocn_ue_connections", ues); !ok || v != 0 {
				return fmt.Errorf("ellamocn_ue_connections%v = %v, %v", ues, v, ok)
			}
		}

		return nil
	})
}

func ueLifecycle(ctx context.Context, t *testing.T, c *client, l lab) {
	eventually(t, l.srsue+" attached with an IP", 180*time.Second,
		logContains(ctx, "Network attach successful. IP: "+l.ueNet, "logs", "--no-color", l.srsue))
	eventually(t, l.core+" MME attach complete", 30*time.Second, mmeLog(ctx, l.core, "["+l.imsi+"] Attach complete"))
	eventually(t, l.operator+" user plane up", 30*time.Second, func() error { return ping(ctx, l) })

	eventually(t, l.operator+" UE connection in /api/v1/ue-connections", 10*time.Second, func() error {
		items, err := operatorUEs(ctx, c, l)
		if err != nil {
			return err
		}

		if len(items) != 1 {
			return fmt.Errorf("want 1 UE connection, got %+v", items)
		}

		u := items[0]
		if u.PLMN != l.plmn || u.Radio.Ref != l.radio || len(u.MME.TNLAddresses) != 1 || u.MME.TNLAddresses[0] != l.mme ||
			u.MME.MMEUES1APID == nil || u.TAI == nil || u.TAI.TAC != 1 {
			return fmt.Errorf("UE connection = %+v", u)
		}

		var r radio
		if _, err := c.do(ctx, http.MethodGet, "/api/v1/radios/"+l.radio, nil, &r); err != nil {
			return err
		}

		if r.UEConnections != 1 {
			return fmt.Errorf("radio ue_connections = %d, want 1", r.UEConnections)
		}

		return nil
	})

	eventually(t, l.operator+" UE context released on inactivity", 120*time.Second, func() error {
		items, err := operatorUEs(ctx, c, l)
		if err != nil {
			return err
		}

		if len(items) != 0 {
			return fmt.Errorf("%d UE connections left", len(items))
		}

		return mmeLog(ctx, l.core, "Number of eNB-UEs is now 0")()
	})

	if l.pagedUE {
		addr, err := ueAddress(ctx, l)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := compose(ctx, "exec", "-T", l.core, "ping", "-c", "5", "-W", "5", addr); err != nil {
			t.Fatalf("downlink ping to the idle UE: %v", err)
		}
	} else if err := ping(ctx, l); err != nil {
		t.Fatalf("ping after release: %v", err)
	}

	eventually(t, l.core+" MME service request", 30*time.Second, mmeLog(ctx, l.core, "["+l.imsi+"] Service request"))

	if items, err := operatorUEs(ctx, c, l); err != nil || len(items) != 1 {
		t.Fatalf("UE connections after service request = %+v, %v", items, err)
	}

	if _, err := compose(ctx, "exec", "-T", l.srsue, "pkill", "-INT", "srsue"); err != nil {
		t.Fatal(err)
	}

	eventually(t, l.core+" MME detach", 30*time.Second, mmeLog(ctx, l.core, "Detach request"))

	eventually(t, l.operator+" UE context released after detach", 30*time.Second, func() error {
		items, err := operatorUEs(ctx, c, l)
		if err != nil {
			return err
		}

		if len(items) != 0 {
			return fmt.Errorf("%d UE connections left", len(items))
		}

		return nil
	})
}

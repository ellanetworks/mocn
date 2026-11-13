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
	LastFailureCause  string `json:"last_failure_cause"`
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

type ranNode struct {
	GlobalENBID      *globalENBID `json:"global_enb_id"`
	ENBName          string       `json:"enb_name"`
	State            string       `json:"state"`
	TNLAddresses     []string     `json:"tnl_addresses"`
	LastFailureCause string       `json:"last_failure_cause"`
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

func TestS1Setup(t *testing.T) {
	ctx := context.Background()
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

	eventually(t, "RAN node up", 120*time.Second, func() error {
		var nodes struct {
			Items []ranNode `json:"items"`
		}

		if _, err := c.do(ctx, http.MethodGet, "/api/v1/ran-nodes", nil, &nodes); err != nil {
			return err
		}

		if len(nodes.Items) != 1 {
			return fmt.Errorf("want 1 RAN node, got %+v", nodes.Items)
		}

		if n := nodes.Items[0]; n.State != "up" {
			return fmt.Errorf("RAN node not up: %+v", n)
		}

		return nil
	})

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

		for _, side := range []string{"ran", "mme"} {
			labels := map[string]string{"side": side, "outcome": "success"}

			v, ok := metric(text, "ellamocn_s1_setup_total", labels)
			if !ok {
				return fmt.Errorf("ellamocn_s1_setup_total%v missing", labels)
			}

			if v < 1 {
				return fmt.Errorf("ellamocn_s1_setup_total%v = %v", labels, v)
			}
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

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
	gnbID    int
	mme      string
	amf      string
	core     string
	coreLog  coreLog
	dn       string
	dnHost   string
	enb      string
	radio    string
	srsue    string
	imsi     string
	ueNet    string
	pagedUE  bool
}

var labs = []lab{
	{
		operator: "ella-core", plmn: plmn{MCC: "001", MNC: "01"}, enbID: 1, gnbID: 1, mme: "10.90.0.14", amf: "10.90.0.14",
		core: "ella-core", coreLog: ellaCoreLog, dn: "10.90.3.3", dnHost: "router", enb: "srsenb",
		radio: "eNB:001-01:MacroeNB-0019b", srsue: "srsue", imsi: "001010000000001", ueNet: "10.45.",
	},
	{
		operator: "open5gs-b", plmn: plmn{MCC: "001", MNC: "02"}, enbID: 2, gnbID: 2, mme: "10.90.0.11", amf: "10.90.0.13",
		core: "open5gs-b", coreLog: open5gsMMELog("open5gs-b"), dn: "10.46.0.1", dnHost: "open5gs-b", enb: "srsenb-b",
		radio: "eNB:001-02:MacroeNB-0019c", srsue: "srsue-b", imsi: "001020000000002", ueNet: "10.46.", pagedUE: true,
	},
}

type coreLog struct {
	args           []string
	accepted       []string
	attached       []string
	idle           []string
	serviceRequest []string
	detached       []string
}

const ellaSUPI = `"supi":"imsi-{imsi}"`

var ellaCoreLog = coreLog{
	args:           []string{"logs", "--no-color", "ella-core"},
	accepted:       []string{`"msg":"Radio setup complete"`, `"ran_addr":"10.90.0.30`, `"rat":"4g"`},
	attached:       []string{`"msg":"UE registration accepted"`, ellaSUPI},
	idle:           []string{`"msg":"UE idle"`, ellaSUPI, `"rat":"4g"`},
	serviceRequest: []string{`"msg":"Service Request accepted"`, ellaSUPI},
	detached:       []string{`"msg":"Detach Request"`, ellaSUPI},
}

func open5gsMMELog(core string) coreLog {
	return coreLog{
		args:           []string{"exec", "-T", core, "cat", "/var/log/open5gs/mme.log"},
		accepted:       []string{"eNB-S1 accepted[10.90.0.30]"},
		attached:       []string{"[{imsi}] Attach complete"},
		idle:           []string{"Number of eNB-UEs is now 0"},
		serviceRequest: []string{"[{imsi}] Service request"},
		detached:       []string{"Detach request"},
	}
}

type client struct {
	base  string
	token string
	http  *http.Client
}

func newClient(env, fallback string) *client {
	base := os.Getenv(env)
	if base == "" {
		base = fallback
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

func eventually(ctx context.Context, t *testing.T, what string, timeout time.Duration, check func() error) {
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

		select {
		case <-ctx.Done():
			t.Fatalf("stopped waiting for %s: %v (last error: %v)", what, ctx.Err(), err)
		case <-time.After(500 * time.Millisecond):
		}
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

type globalGNBID struct {
	PLMN      plmn `json:"plmn"`
	GNBID     int  `json:"gnb_id"`
	BitLength int  `json:"bit_length"`
}

type operator struct {
	Name        string       `json:"name"`
	PLMNs       []plmn       `json:"plmns"`
	GlobalENBID *globalENBID `json:"global_enb_id"`
	GlobalGNBID *globalGNBID `json:"global_gnb_id"`
	MMEs        []coreNode   `json:"mmes"`
	AMFs        []coreNode   `json:"amfs"`
}

type ueConnection struct {
	Interface string `json:"interface"`
	Radio     struct {
		Ref         string `json:"ref"`
		ENBUES1APID int    `json:"enb_ue_s1ap_id"`
		MMEUES1APID int    `json:"mme_ue_s1ap_id"`
		RANUENGAPID int    `json:"ran_ue_ngap_id"`
		AMFUENGAPID int    `json:"amf_ue_ngap_id"`
	} `json:"radio"`
	MME struct {
		TNLAddresses []string `json:"tnl_addresses"`
		ENBUES1APID  int      `json:"enb_ue_s1ap_id"`
		MMEUES1APID  *int     `json:"mme_ue_s1ap_id"`
	} `json:"mme"`
	AMF *struct {
		TNLAddresses []string `json:"tnl_addresses"`
		RANUENGAPID  int      `json:"ran_ue_ngap_id"`
		AMFUENGAPID  *int     `json:"amf_ue_ngap_id"`
	} `json:"amf"`
	Operator string `json:"operator"`
	PLMN     plmn   `json:"plmn"`
	TAI      *struct {
		TAC string `json:"tac"`
	} `json:"tai"`
	NRCGI *struct {
		CellID int `json:"cell_id"`
	} `json:"nr_cgi"`
}

type radio struct {
	Ref           string       `json:"ref"`
	Type          string       `json:"type"`
	GlobalENBID   *globalENBID `json:"global_enb_id"`
	Name          string       `json:"name"`
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

var ellaCoreSubscribers = []string{"001010000000001", "001010000000011", "001010000000021"}

func setUpEllaCore(ctx context.Context, t *testing.T) *client {
	t.Helper()

	c := newClient("E2E_CORE_API", "http://127.0.0.1:5033")

	eventually(ctx, t, "Ella Core API", 120*time.Second, func() error {
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
		t.Fatalf("authenticate to Ella Core: %v", err)
	}

	c.token = login.Token

	var apiToken struct {
		Token string `json:"token"`
	}

	name := map[string]string{"name": fmt.Sprintf("e2e-%d", time.Now().UnixNano())}
	if _, err := c.do(ctx, http.MethodPost, "/api/v1/users/me/api-tokens", name, &apiToken); err != nil {
		t.Fatalf("create an Ella Core API token: %v", err)
	}

	c.token = apiToken.Token

	if _, err := c.do(ctx, http.MethodPut, "/api/v1/slices/default/data-networks/internet", map[string]bool{"nat": false}, nil); err != nil {
		t.Fatalf("disable NAT on Ella Core: %v", err)
	}

	for _, imsi := range ellaCoreSubscribers {
		status, err := c.do(ctx, http.MethodGet, "/api/v1/subscribers/"+imsi, nil, nil)
		if status != http.StatusNotFound {
			if err != nil {
				t.Fatalf("get Ella Core subscriber %s: %v", imsi, err)
			}

			continue
		}

		sub := map[string]string{
			"imsi":           imsi,
			"key":            "465b5ce8b199b49faa5f0a2ee238a6bc",
			"opc":            "cd63cb71954a9f4e48a5994e37a02baf",
			"sequenceNumber": "000000000001",
			"profile_name":   "default",
		}

		if _, err := c.do(ctx, http.MethodPost, "/api/v1/subscribers", sub, nil); err != nil {
			t.Fatalf("create Ella Core subscriber %s: %v", imsi, err)
		}
	}

	return c
}

func setUp(ctx context.Context, t *testing.T) *client {
	t.Helper()

	setUpEllaCore(ctx, t)

	c := newClient("E2E_API", "http://127.0.0.1:5032")

	eventually(ctx, t, "Ella MOCN API", 120*time.Second, func() error {
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
			GlobalGNBID: &globalGNBID{PLMN: l.plmn, GNBID: l.gnbID, BitLength: 22},
			MMEs:        []coreNode{{TNLAddresses: []string{l.mme}}},
			AMFs:        []coreNode{{TNLAddresses: []string{l.amf}}},
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
	ctx := t.Context()
	c := setUp(ctx, t)

	if _, err := compose(ctx, "up", "-d", "srsenb", "srsenb-b"); err != nil {
		t.Fatal(err)
	}

	eventually(ctx, t, "radios up", 120*time.Second, func() error {
		var nodes struct {
			Items []radio `json:"items"`
		}

		if _, err := c.do(ctx, http.MethodGet, "/api/v1/radios", nil, &nodes); err != nil {
			return err
		}

		var enbs []radio

		for _, n := range nodes.Items {
			if n.Type == "eNB" {
				enbs = append(enbs, n)
			}
		}

		if len(enbs) != len(labs) {
			return fmt.Errorf("want %d eNBs, got %+v", len(labs), nodes.Items)
		}

		for _, n := range enbs {
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

		eventually(ctx, t, l.operator+" MME up", 120*time.Second, func() error {
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

		eventually(ctx, t, l.core+" MME accepted Ella MOCN", 30*time.Second, l.coreLog.logged(ctx, 0, l.coreLog.accepted, l.imsi))
	}

	eventually(ctx, t, "S1 Setup metrics", 30*time.Second, func() error {
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
	var stderr bytes.Buffer

	cmd := exec.CommandContext(ctx, "docker", append([]string{"compose"}, args...)...)
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("docker compose %s: %w: %s%s", strings.Join(args, " "), err, out, stderr.Bytes())
	}

	return string(out), nil
}

func startServices(ctx context.Context, t *testing.T, services ...string) {
	t.Helper()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()

		if _, err := compose(ctx, append([]string{"stop", "-t", "1"}, services...)...); err != nil {
			t.Error(err)
		}
	})

	if _, err := compose(ctx, append([]string{"up", "-d"}, services...)...); err != nil {
		t.Fatal(err)
	}
}

func logMark(ctx context.Context, t *testing.T, args ...string) int {
	t.Helper()

	out, err := compose(ctx, args...)
	if err != nil {
		t.Fatal(err)
	}

	return len(out)
}

func logContains(ctx context.Context, what string, args ...string) func() error {
	return logContainsAfter(ctx, what, 0, args...)
}

func logContainsAfter(ctx context.Context, what string, mark int, args ...string) func() error {
	return func() error {
		out, err := compose(ctx, args...)
		if err != nil {
			return err
		}

		if len(out) < mark {
			return fmt.Errorf("log shrank from %d to %d bytes", mark, len(out))
		}

		if !strings.Contains(out[mark:], what) {
			return fmt.Errorf("%q not logged yet", what)
		}

		return nil
	}
}

func relayedToCore(ctx context.Context, msg, ref, procedure string) func() error {
	return func() error {
		out, err := compose(ctx, "logs", "--no-color", "mocn")
		if err != nil {
			return err
		}

		for line := range strings.SplitSeq(out, "\n") {
			if strings.Contains(line, `"msg":"`+msg+`","radio":"`+ref+`"`) && strings.Contains(line, `"procedure":"`+procedure+`"`) {
				return nil
			}
		}

		return fmt.Errorf("%s from %s not relayed to the core yet", procedure, ref)
	}
}

func (cl coreLog) mark(ctx context.Context, t *testing.T) int {
	t.Helper()

	return logMark(ctx, t, cl.args...)
}

func (cl coreLog) logged(ctx context.Context, mark int, patterns []string, imsi string) func() error {
	return func() error {
		out, err := compose(ctx, cl.args...)
		if err != nil {
			return err
		}

		if len(out) < mark {
			return fmt.Errorf("log shrank from %d to %d bytes", mark, len(out))
		}

		want := make([]string, len(patterns))
		for i, p := range patterns {
			want[i] = strings.ReplaceAll(p, "{imsi}", imsi)
		}

	lines:
		for line := range strings.SplitSeq(out[mark:], "\n") {
			for _, w := range want {
				if !strings.Contains(line, w) {
					continue lines
				}
			}

			return nil
		}

		return fmt.Errorf("%q not logged yet", want)
	}
}

func ueConnections(ctx context.Context, c *client) ([]ueConnection, error) {
	var list struct {
		Items []ueConnection `json:"items"`
	}

	_, err := c.do(ctx, http.MethodGet, "/api/v1/ue-connections", nil, &list)

	return list.Items, err
}

func ping(ctx context.Context, l lab) error {
	return uePing(ctx, l.srsue, l.dn)
}

func uePing(ctx context.Context, ue, dn string) error {
	_, err := compose(ctx, "exec", "-T", ue, "sh", "-c",
		fmt.Sprintf("ip route replace %[1]s dev tun_srsue && ping -c 1 -w 15 -I tun_srsue %[1]s", dn))

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
		if u.Operator == l.operator && u.Interface == "s1" {
			out = append(out, u)
		}
	}

	return out, err
}

func TestUEAttachServiceRequestDetach(t *testing.T) {
	ctx := t.Context()
	c := setUp(ctx, t)

	for _, l := range labs {
		t.Run(l.operator, func(t *testing.T) {
			ueLifecycle(t.Context(), t, c, l)
		})
	}
}

func TestRelayMetrics(t *testing.T) {
	ctx := t.Context()
	c := setUp(ctx, t)

	eventually(ctx, t, "relay metrics", 10*time.Second, func() error {
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
	startServices(ctx, t, l.srsue)

	eventually(ctx, t, l.srsue+" attached with an IP", 180*time.Second,
		logContains(ctx, "Network attach successful. IP: "+l.ueNet, "logs", "--no-color", l.srsue))
	eventually(ctx, t, l.core+" MME attach complete", 30*time.Second, l.coreLog.logged(ctx, 0, l.coreLog.attached, l.imsi))
	eventually(ctx, t, l.enb+" Initial Context Setup complete", 30*time.Second,
		logContains(ctx, "Tx S1AP SDU, InitialContextSetupResponse", "logs", "--no-color", l.enb))
	eventually(ctx, t, "Ella MOCN relayed the Initial Context Setup outcome from "+l.radio, 10*time.Second,
		relayedToCore(ctx, "relayed S1AP message to MME", l.radio, "InitialContextSetup (9)"))
	eventually(ctx, t, l.operator+" user plane up", 30*time.Second, func() error { return ping(ctx, l) })

	connected := l.coreLog.mark(ctx, t)

	eventually(ctx, t, l.operator+" UE connection in /api/v1/ue-connections", 10*time.Second, func() error {
		items, err := operatorUEs(ctx, c, l)
		if err != nil {
			return err
		}

		if len(items) != 1 {
			return fmt.Errorf("want 1 UE connection, got %+v", items)
		}

		u := items[0]
		if u.PLMN != l.plmn || u.Radio.Ref != l.radio || len(u.MME.TNLAddresses) != 1 || u.MME.TNLAddresses[0] != l.mme ||
			u.MME.MMEUES1APID == nil || u.TAI == nil || u.TAI.TAC != "000001" {
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

	eventually(ctx, t, l.operator+" UE context released on inactivity", 120*time.Second, func() error {
		items, err := operatorUEs(ctx, c, l)
		if err != nil {
			return err
		}

		if len(items) != 0 {
			return fmt.Errorf("%d UE connections left", len(items))
		}

		return l.coreLog.logged(ctx, connected, l.coreLog.idle, l.imsi)()
	})

	idle := l.coreLog.mark(ctx, t)

	if l.pagedUE {
		addr, err := ueAddress(ctx, l)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := compose(ctx, "exec", "-T", l.dnHost, "ping", "-c", "1", "-w", "15", addr); err != nil {
			t.Fatalf("downlink ping to the idle UE: %v", err)
		}
	} else if err := ping(ctx, l); err != nil {
		t.Fatalf("ping after release: %v", err)
	}

	eventually(ctx, t, l.core+" MME service request", 30*time.Second, l.coreLog.logged(ctx, idle, l.coreLog.serviceRequest, l.imsi))

	if items, err := operatorUEs(ctx, c, l); err != nil || len(items) != 1 {
		t.Fatalf("UE connections after service request = %+v, %v", items, err)
	}

	attached := l.coreLog.mark(ctx, t)

	if _, err := compose(ctx, "exec", "-T", l.srsue, "pkill", "-INT", "srsue"); err != nil {
		t.Fatal(err)
	}

	eventually(ctx, t, l.core+" MME detach", 30*time.Second, l.coreLog.logged(ctx, attached, l.coreLog.detached, l.imsi))

	eventually(ctx, t, l.operator+" UE context released after detach", 30*time.Second, func() error {
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

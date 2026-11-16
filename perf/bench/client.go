package bench

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var client = &http.Client{Timeout: 10 * time.Minute}

func call(ctx context.Context, method, url string, in, out any) error {
	var body io.Reader

	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}

		body = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}

	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s %s: %s: %s", method, url, resp.Status, strings.TrimSpace(string(b)))
	}

	if out == nil {
		return nil
	}

	return json.Unmarshal(b, out)
}

type Metrics map[string]float64

func scrape(ctx context.Context, url string, names []string) (Metrics, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	m := Metrics{}

	for _, n := range names {
		m[n] = 0
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)

	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}

		name, rest, ok := strings.Cut(line, "{")
		if !ok {
			name, rest, _ = strings.Cut(line, " ")
		} else if _, after, found := strings.Cut(rest, "} "); found {
			rest = after
		}

		if _, want := m[name]; !want {
			continue
		}

		v, err := strconv.ParseFloat(strings.Fields(rest)[0], 64)
		if err == nil {
			m[name] += v
		}
	}

	return m, sc.Err()
}

func (m Metrics) delta(before Metrics) Metrics {
	d := Metrics{}
	for k, v := range m {
		d[k] = v - before[k]
	}

	return d
}

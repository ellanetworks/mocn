package logtest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log/slog"
	"sync"
	"testing"

	"github.com/ellanetworks/mocn/internal/logging"
)

type Recorder struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (r *Recorder) Logger() *slog.Logger {
	return logging.New(r, slog.LevelDebug)
}

func (r *Recorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.buf.Write(p)
}

func (r *Recorder) Records(t *testing.T) []map[string]any {
	t.Helper()

	r.mu.Lock()
	defer r.mu.Unlock()

	var out []map[string]any

	sc := bufio.NewScanner(bytes.NewReader(r.buf.Bytes()))
	for sc.Scan() {
		var rec map[string]any
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			t.Fatalf("log %q is not a JSON object: %v", sc.Text(), err)
		}

		out = append(out, rec)
	}

	return out
}

func (r *Recorder) Messages(t *testing.T, msg string) []map[string]any {
	t.Helper()

	var out []map[string]any

	for _, rec := range r.Records(t) {
		if rec["msg"] == msg {
			out = append(out, rec)
		}
	}

	return out
}

package cli

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/limits"
)

const statusLineJSON = `{"session_id":"sess-1","cwd":"/work/x","version":"2.1.0",
 "model":{"id":"claude-fable-5-1","display_name":"Fable 5.1"},
 "context_window":{"context_window_size":1000000,"used_percentage":7.4},
 "rate_limits":{"five_hour":{"used_percentage":12,"resets_at":1760400000},"seven_day":{"used_percentage":40.5,"resets_at":1760900000}},
 "cost":{"total_cost_usd":1.25}}`

// statusDaemon records the events posted to /event.
type statusDaemon struct {
	mu     sync.Mutex
	events []session.Doc
	srv    *httptest.Server
}

func newStatusDaemon(t *testing.T) *statusDaemon {
	t.Helper()
	d := &statusDaemon{}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/event" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		b, _ := io.ReadAll(r.Body)
		var doc session.Doc
		if err := json.Unmarshal(b, &doc); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		d.mu.Lock()
		d.events = append(d.events, doc)
		d.mu.Unlock()
		io.WriteString(w, "ok")
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func (d *statusDaemon) port() int { return d.srv.Listener.Addr().(*net.TCPAddr).Port }

func (d *statusDaemon) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.events)
}

// statusEnv points the clued config at a fake daemon and returns the
// config directory.
func statusEnv(t *testing.T, port int) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLUED_SERVER_URL", "")
	t.Setenv("CLUED_PORT", "")
	dir := filepath.Join(home, ".claude", "plugins", "data", "clued")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{"port": port, "mongoUrl": "mongodb://127.0.0.1:1"}
	b, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestHookStatusSamplesAndPrintsTheLine(t *testing.T) {
	daemon := newStatusDaemon(t)
	dir := statusEnv(t, daemon.port())
	state := filepath.Join(dir, "limits")

	r := execStdin(t, statusLineJSON, "hook", "status", "--state-dir", state)
	if r.code != 0 || r.stderr != "" || r.stdout != "ctx 7% of 1M · 5h 12% · 7d 40% · Fable 5.1\n" {
		t.Fatalf("first run: %+v", r)
	}
	if daemon.count() != 1 {
		t.Fatalf("posted %d events, want 1", daemon.count())
	}
	ev := daemon.events[0]
	if ev["hook_event_name"] != session.LimitsEventName || ev["session_id"] != "sess-1" || ev.Map("rate_limits").Map("seven_day")["used_percentage"] != 40.5 || ev.Map("cost")["total_cost_usd"] != 1.25 {
		t.Errorf("event = %v", ev)
	}

	// A refresh right after posts nothing but still prints.
	r = execStdin(t, statusLineJSON, "hook", "status", "--state-dir", state)
	if r.code != 0 || !strings.HasPrefix(r.stdout, "ctx 7%") || daemon.count() != 1 {
		t.Errorf("second run: %+v, events %d", r, daemon.count())
	}

	// Once the last sample is old enough the heartbeat posts again.
	prev := limits.LoadState(state, "sess-1")
	if prev == nil {
		t.Fatal("no state after the first run")
	}
	prev.At = prev.At.Add(-10 * time.Minute)
	if err := limits.SaveState(state, "sess-1", *prev); err != nil {
		t.Fatal(err)
	}
	r = execStdin(t, statusLineJSON, "hook", "status", "--state-dir", state)
	if r.code != 0 || daemon.count() != 2 {
		t.Errorf("heartbeat run: %+v, events %d", r, daemon.count())
	}

	// --then runs the previous status line command with the same stdin.
	r = execStdin(t, statusLineJSON, "hook", "status", "--state-dir", state, "--then", "jq -r .model.display_name")
	if r.code != 0 || r.stdout != "Fable 5.1\n" {
		t.Errorf("--then run: %+v", r)
	}
	// So does the command kept in statusline.json.
	if err := os.WriteFile(filepath.Join(dir, "statusline.json"), []byte(`{"then": "echo kept"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r = execStdin(t, statusLineJSON, "hook", "status", "--state-dir", state)
	if r.code != 0 || r.stdout != "kept\n" {
		t.Errorf("statusline.json run: %+v", r)
	}
	r = execStdin(t, statusLineJSON, "hook", "status", "--state-dir", state, "--quiet")
	if r.code != 0 || r.stdout != "" {
		t.Errorf("--quiet run: %+v", r)
	}

	// Input without a session, or no input, prints what it can and posts nothing.
	before := daemon.count()
	r = execStdin(t, `{"context_window":{"used_percentage":50}}`, "hook", "status", "--state-dir", state, "--then", "")
	if r.code != 0 || r.stderr != "" || daemon.count() != before {
		t.Errorf("no session: %+v", r)
	}
	r = execStdin(t, "", "hook", "status", "--state-dir", state, "--then", "")
	if r.code != 0 || r.stderr != "" || daemon.count() != before {
		t.Errorf("empty input: %+v", r)
	}
	r = execStdin(t, "{", "hook", "status", "--state-dir", state, "--then", "")
	if r.code != 0 || !strings.Contains(r.stderr, "status line input") || daemon.count() != before {
		t.Errorf("invalid input: %+v", r)
	}
}

func TestHookStatusFallsBackToTheWALWhenTheDaemonIsDown(t *testing.T) {
	// A port nothing listens on.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	dir := statusEnv(t, port)
	r := execStdin(t, statusLineJSON, "hook", "status", "--state-dir", filepath.Join(dir, "limits"))
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("run: %+v", r)
	}
	wal, err := os.ReadFile(filepath.Join(dir, "events.wal"))
	if err != nil {
		t.Fatal("the sample should be in the write-ahead log:", err)
	}
	if !strings.Contains(string(wal), `"hook_event_name":"LimitsSampled"`) {
		t.Errorf("wal = %s", wal)
	}
	if limits.LoadState(filepath.Join(dir, "limits"), "sess-1") == nil {
		t.Error("a buffered sample counts as stored")
	}
}

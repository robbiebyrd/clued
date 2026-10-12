package snapshot

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/collect"
)

var report = collect.Report{Sections: []collect.Section{
	{Name: "ENV", Body: "cwd: /work/x"},
	{Name: "BUILD", Body: "no build tool detected"},
}}

func TestJSONCarriesTheHookShape(t *testing.T) {
	at := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)
	raw, err := Event{SessionID: "s-1", Cwd: "/work/x", Report: report, At: at}.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["hook_event_name"] != EventName || got["session_id"] != "s-1" || got["cwd"] != "/work/x" {
		t.Errorf("payload = %s", raw)
	}
	if got["collected_at"] != "2026-05-06T07:08:09Z" {
		t.Errorf("collected_at = %v", got["collected_at"])
	}
	if got["report"] != report.String() {
		t.Errorf("report = %q", got["report"])
	}
	sections := got["sections"].(map[string]any)
	if sections["ENV"] != "cwd: /work/x" || sections["BUILD"] != "no build tool detected" {
		t.Errorf("sections = %v", sections)
	}
}

func TestJSONOmitsAnEmptySessionAndStampsNow(t *testing.T) {
	before := time.Now().UTC().Add(-time.Second)
	raw, err := Event{Cwd: "/w", Report: report}.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "session_id") {
		t.Errorf("empty session id must be omitted: %s", raw)
	}
	var got struct {
		CollectedAt string `json:"collected_at"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	at, err := time.Parse(time.RFC3339Nano, got.CollectedAt)
	if err != nil || at.Before(before) {
		t.Errorf("collected_at = %q (%v)", got.CollectedAt, err)
	}
}

// daemon is a fake clued daemon that records the last event body.
func daemon(t *testing.T, status int) (port int, last func() string) {
	t.Helper()
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/event" || r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	_, portStr, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	var p int
	for _, c := range portStr {
		p = p*10 + int(c-'0')
	}
	return p, func() string { return body }
}

func TestPostDeliversTheEvent(t *testing.T) {
	port, last := daemon(t, http.StatusOK)
	if err := Post(context.Background(), port, Event{SessionID: "s-2", Cwd: "/w", Report: report}); err != nil {
		t.Fatalf("Post: %v", err)
	}
	if !strings.Contains(last(), `"hook_event_name":"ContextCollected"`) || !strings.Contains(last(), `"session_id":"s-2"`) {
		t.Errorf("daemon received %s", last())
	}
}

func TestPostReportsFailures(t *testing.T) {
	port, _ := daemon(t, http.StatusBadRequest)
	if err := Post(context.Background(), port, Event{Cwd: "/w", Report: report}); err == nil || !strings.Contains(err.Error(), "400") {
		t.Errorf("non-2xx should fail, got %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	if err := Post(context.Background(), closed, Event{Cwd: "/w", Report: report}); err == nil {
		t.Error("a closed port should fail")
	}
	if err := Post(context.Background(), 0, Event{Cwd: "/w", Report: report}); err == nil {
		t.Error("port 0 should fail")
	}
}

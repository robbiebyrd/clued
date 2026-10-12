package snapshot_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/collect"
	"github.com/robbiebyrd/clued/mind-palace/collect/snapshot"
	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/daemon"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
)

// TestRealDaemonStoresTheSnapshotAsAHookEvent posts through the daemon's own
// handler and reads the stored event back, so the body shape is proven
// against the ingest code rather than a fake.
func TestRealDaemonStoresTheSnapshotAsAHookEvent(t *testing.T) {
	st := memsession.New()
	d := daemon.New(daemon.Options{
		Config:    session.Config{ProjectsDir: t.TempDir(), FileHistoryDir: t.TempDir()},
		Store:     st,
		AccountID: "acct-snap",
		Host:      session.HostInfo{Hostname: "box"},
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	srv := httptest.NewServer(d.Handler())
	defer srv.Close()
	_, portStr, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	port := 0
	for _, c := range portStr {
		port = port*10 + int(c-'0')
	}

	root := filepath.ToSlash(t.TempDir())
	report := collect.Report{Sections: []collect.Section{{Name: "ENV", Body: "cwd: " + root}, {Name: "BUILD", Body: "no build tool detected"}}}
	if err := snapshot.Post(context.Background(), port, snapshot.Event{SessionID: "live-check", Cwd: root, Report: report}); err != nil {
		t.Fatalf("Post: %v", err)
	}

	ctx := context.Background()
	events, err := st.Unenriched(ctx, "hook_events", "none", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("stored %d hook events, want 1", len(events))
	}
	ev := events[0]
	if name, _ := ev.String("hook_event_name"); name != snapshot.EventName {
		t.Errorf("hook_event_name = %q", name)
	}
	if id, _ := ev.String("session_id"); id != "live-check" {
		t.Errorf("session_id = %q", id)
	}
	if acc, _ := ev.String("account_id"); acc != "acct-snap" {
		t.Errorf("the daemon stamps the account, got %q", acc)
	}
	if text, _ := ev.String("report"); text != report.String() {
		t.Errorf("report = %q", text)
	}
	if sections := ev.Map("sections"); sections == nil {
		t.Errorf("sections missing: %v", ev)
	} else if body, _ := sections.String("BUILD"); body != "no build tool detected" {
		t.Errorf("sections.BUILD = %q", body)
	}

	// The daemon also tracks the session the snapshot names, with its cwd.
	sess, err := st.GetSession(ctx, "acct-snap", "live-check")
	if err != nil {
		t.Fatalf("session not tracked: %v", err)
	}
	if cwd, _ := sess.String("cwd"); cwd != root {
		t.Errorf("session cwd = %q, want %q", cwd, root)
	}
}

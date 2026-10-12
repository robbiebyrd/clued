package snapshot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/ingest"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
)

func TestInsertStoresTheEventThroughTheStore(t *testing.T) {
	ctx := context.Background()
	st := memsession.New()
	host := session.HostInfo{Hostname: "box", Platform: "linux"}
	svc := &ingest.Service{Store: st, AccountID: "acct-1", Host: host}
	at := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)

	if err := Insert(ctx, svc, Event{SessionID: "s-1", Cwd: "/work/x", Report: report, At: at}); err != nil {
		t.Fatal(err)
	}

	full, err := st.FullSession(ctx, "acct-1", "s-1")
	if err != nil {
		t.Fatalf("the session was not tracked under the account: %v", err)
	}
	if full["cwd"] != "/work/x" {
		t.Errorf("session cwd = %v", full["cwd"])
	}
	events := full["hook_events"].([]session.Doc)
	if len(events) != 1 {
		t.Fatalf("hook_events = %v", events)
	}
	ev := events[0]
	if ev["hook_event_name"] != EventName || ev["cwd"] != "/work/x" || ev["collected_at"] != "2026-05-06T07:08:09Z" || ev["report"] != report.String() {
		t.Errorf("event = %v", ev)
	}
	if sections := ev.Map("sections"); sections["ENV"] != "cwd: /work/x" || sections["BUILD"] != "no build tool detected" {
		t.Errorf("sections = %v", ev["sections"])
	}
	if _, ok := ev["created_at"].(time.Time); !ok {
		t.Errorf("created_at not stamped: %v", ev["created_at"])
	}
	if ev["host"] == nil {
		t.Errorf("host not stamped: %v", ev)
	}
}

func TestInsertRefusesAnEventWithoutASession(t *testing.T) {
	ctx := context.Background()
	st := memsession.New()
	walPath := filepath.Join(t.TempDir(), "events.wal")
	svc := &ingest.Service{Store: st, AccountID: "acct-1", WALPath: walPath}

	for _, sid := range []string{"", "../x", "a/b"} {
		err := Insert(ctx, svc, Event{SessionID: sid, Cwd: "/w", Report: report})
		if !errors.Is(err, ErrNoSession) {
			t.Errorf("session id %q: err = %v, want ErrNoSession", sid, err)
		}
	}
	if events, _ := st.Unenriched(ctx, "hook_events", "none", 10); len(events) != 0 {
		t.Errorf("events stored without a session: %v", events)
	}
	if _, err := os.Stat(walPath); err == nil {
		t.Error("a refused event must not be buffered in the WAL")
	}
}

// refusing is a store whose hook-event inserts fail.
type refusing struct {
	session.Store
}

func (refusing) InsertHookEvent(context.Context, session.Doc) error {
	return errors.New("store down")
}

func TestInsertBuffersInTheWALWhenTheStoreRefuses(t *testing.T) {
	ctx := context.Background()
	walPath := filepath.Join(t.TempDir(), "events.wal")
	svc := &ingest.Service{Store: refusing{memsession.New()}, AccountID: "acct-1", WALPath: walPath}

	if err := Insert(ctx, svc, Event{SessionID: "s-2", Cwd: "/w", Report: report}); err != nil {
		t.Errorf("a buffered event is not a failure: %v", err)
	}
	raw, err := os.ReadFile(walPath)
	if err != nil || !strings.Contains(string(raw), `"hook_event_name":"ContextCollected"`) {
		t.Errorf("WAL = %q (%v)", raw, err)
	}

	// Without a WAL the failure is reported.
	svc = &ingest.Service{Store: refusing{memsession.New()}, AccountID: "acct-1"}
	if err := Insert(ctx, svc, Event{SessionID: "s-3", Cwd: "/w", Report: report}); err == nil {
		t.Error("a refused event without a WAL must fail")
	}
}

package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
	"github.com/robbiebyrd/clued/mind-palace/session/query"
	"github.com/robbiebyrd/clued/mind-palace/session/summary"
)

const account = "acc"

var (
	ctx  = context.Background()
	day1 = time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// addSession stores a session of two prompts, an edit and a reply,
// starting at start, in the project directory path with origin.
func addSession(t *testing.T, st session.Store, acc, sid, path, origin string, start time.Time) {
	t.Helper()
	acts := []session.Doc{
		{"kind": "user", "label": "user", "detail": "Work on " + sid},
		{"kind": "edit", "label": "Edit", "detail": sid + ".go", "tool_name": "Edit", "tool_use_id": sid + "-e"},
		{"kind": "user", "label": "user", "detail": "And finish " + sid},
		{"kind": "response", "label": "response", "detail": "Finished " + sid},
	}
	must(t, st.UpsertSession(ctx, session.Session{SessionID: sid, AccountID: acc, ProjectPath: path, GitOrigin: origin, LastSeen: start.Add(4 * time.Minute)}))
	for seq, a := range acts {
		at := start.Add(time.Duration(seq) * time.Minute)
		must(t, st.UpsertTranscriptLines(ctx, []session.TranscriptLine{{SessionID: sid, Seq: seq, AccountID: acc, Line: map[string]any{"type": "x", "timestamp": at.Format(time.RFC3339Nano)}}}))
		pending, err := st.Unenriched(ctx, "transcript_lines", "activity-kind", 0)
		must(t, err)
		for _, d := range pending {
			if d["session_id"] == sid && d["seq"] == seq {
				must(t, st.SetEnrichment(ctx, "transcript_lines", d["_id"], "activity-kind", a))
			}
		}
	}
}

// clock is a settable clock.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

// fakeEngine counts its calls and answers with the kind of prompt it got
// and the first source it names.
type fakeEngine struct {
	mu      sync.Mutex
	name    string
	calls   int
	prompts []string
	err     error
	block   chan struct{}
}

func (f *fakeEngine) Name() string {
	if f.name == "" {
		return "claude"
	}
	return f.name
}

func (f *fakeEngine) Run(_ context.Context, prompt, _ string) (string, float64, error) {
	if f.block != nil {
		<-f.block
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.prompts = append(f.prompts, prompt)
	if f.err != nil {
		return "", 0, f.err
	}
	kind := "note"
	switch {
	case strings.Contains(prompt, "one day of work"):
		kind = "day"
	case strings.Contains(prompt, "last seven days"):
		kind = "recent"
	case strings.Contains(prompt, "one month of work"):
		kind = "archive"
	}
	return fmt.Sprintf("## %s by the engine\n%d", kind, f.calls), 0.01, nil
}

func (f *fakeEngine) count() int { f.mu.Lock(); defer f.mu.Unlock(); return f.calls }

func newService(st session.Store, c *clock) *Service {
	q := &query.Service{Store: st, AccountID: account}
	return &Service{
		Store: st, AccountID: account, Now: c.now,
		Summary: &summary.Service{Store: st, AccountID: account, Query: q, Now: c.now},
	}
}

// kinds lists the ids of the account's records of a kind, sorted.
func ids(t *testing.T, st session.Store, kind string) []string {
	t.Helper()
	events, err := st.MemoryEvents(ctx, session.MemoryQuery{AccountID: account, Kinds: []string{kind}, IncludePrivate: true})
	must(t, err)
	var out []string
	for _, ev := range events {
		id, _ := ev.Map("memory").String("id")
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func TestRememberStoresNotesHandoffsAndOnePinned(t *testing.T) {
	st := memsession.New()
	c := &clock{t: day1}
	svc := newService(st, c)
	n, err := svc.Remember(ctx, "p", "note", "Use make, not go build <private>token 123</private>", false)
	must(t, err)
	if n.Kind != KindNote || !strings.HasPrefix(n.ID, "memory-note/p/") || n.Text != "Use make, not go build" || !n.Edited {
		t.Errorf("note = %+v", n)
	}
	if _, err := svc.Remember(ctx, "p", "handoff", "Next: wire the hook", false); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"first pin", "second pin"} {
		c.set(c.now().Add(time.Minute))
		if _, err := svc.Remember(ctx, "p", "pinned", text, false); err != nil {
			t.Fatal(err)
		}
	}
	pinned, err := svc.Show(ctx, "p", false, KindPinned)
	must(t, err)
	if len(pinned) != 1 || pinned[0].Text != "second pin" || pinned[0].ID != "memory-pinned/p" {
		t.Errorf("pinned = %+v", pinned)
	}
	for _, bad := range []struct{ project, kind, text string }{{"p", "identity", "x"}, {"", "note", "x"}, {"p", "note", "<private>all</private>"}} {
		if _, err := svc.Remember(ctx, bad.project, bad.kind, bad.text, false); err == nil {
			t.Errorf("Remember(%q, %q, %q) accepted", bad.project, bad.kind, bad.text)
		}
	}
	secret := "sk-ant-" + strings.Repeat("b", 30)
	p, err := svc.Remember(ctx, "p", "note", "the key is "+secret, true)
	must(t, err)
	if !p.Private || strings.Contains(p.Text, secret) {
		t.Errorf("private note = %+v (secrets are masked even in private notes)", p)
	}
}

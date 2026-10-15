package summary

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
	"github.com/robbiebyrd/clued/mind-palace/session/query"
	"github.com/robbiebyrd/clued/mind-palace/session/redact"
)

const account = "acc"

var t0 = time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)

var ctx = context.Background()

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// addSession stores a session with labelled activities, one minute apart
// from start; the session is last seen at the last activity.
func addSession(t *testing.T, st session.Store, acc, sid, project, origin string, start time.Time, acts ...session.Doc) {
	t.Helper()
	last := start.Add(time.Duration(len(acts)) * time.Minute)
	must(t, st.UpsertSession(ctx, session.Session{SessionID: sid, AccountID: acc, ProjectPath: project, GitOrigin: origin, LastSeen: last}))
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

func user(text string) session.Doc {
	return session.Doc{"kind": "user", "label": "user", "detail": text}
}
func reply(text string) session.Doc {
	return session.Doc{"kind": "response", "label": "response", "detail": text}
}
func call(kind, tool, detail, id string) session.Doc {
	return session.Doc{"kind": kind, "label": tool, "detail": detail, "tool_name": tool, "tool_use_id": id}
}
func res(id, detail string, isError bool) session.Doc {
	return session.Doc{"kind": "result", "label": "result", "detail": detail, "tool_use_id": id, "is_error": isError}
}

// workSession is a session of two prompts with an edit, a commit, a
// failing test that was fixed and a lint failure that was not.
func workSession() []session.Doc {
	return []session.Doc{
		user("Fix the daemon restart\nand more detail"),
		call("edit", "Edit", "daemon.go", "e1"),
		call("edit", "Edit", "daemon_test.go", "e2"),
		call("edit", "Edit", "daemon.go", "e3"),
		call("bash", "Bash", "go test ./...", "b1"),
		res("b1", "FAIL: TestRestart", true),
		call("bash", "Bash", "go test ./...", "b2"),
		res("b2", "ok", false),
		call("bash", "Bash", "golangci-lint run", "b3"),
		res("b3", "daemon.go:12: unused variable", true),
		call("commit", "Bash", "Restart the daemon cleanly", "c1"),
		user("Now the docs"),
		reply("Done: the daemon restarts cleanly and the docs say so."),
	}
}

// fakeEngine records the prompts and answers with a fixed text.
type fakeEngine struct {
	prompts []string
	answer  string
	err     error
}

func (f *fakeEngine) Name() string { return "fake" }
func (f *fakeEngine) Run(_ context.Context, prompt, _ string) (string, float64, error) {
	f.prompts = append(f.prompts, prompt)
	if f.err != nil {
		return "", 0, f.err
	}
	return f.answer, 0.002, nil
}

func newService(st session.Store, now time.Time) *Service {
	return &Service{Store: st, AccountID: account, Query: &query.Service{Store: st, AccountID: account}, Now: func() time.Time { return now }}
}

func TestPendingExcludesSummarisedOtherAccountsAndLive(t *testing.T) {
	st := memsession.New()
	addSession(t, st, account, "done", "/w/a", "", t0, workSession()...)
	addSession(t, st, account, "summarised", "/w/a", "", t0, workSession()...)
	addSession(t, st, account, "live", "/w/a", "", t0.Add(3*time.Hour), workSession()...)
	addSession(t, st, "other", "foreign", "/w/a", "", t0, workSession()...)
	addSession(t, st, account, "old", "/w/a", "", t0.AddDate(0, 0, -5), workSession()...)
	ended := t0.Add(3 * time.Hour)
	addSession(t, st, account, "ended", "/w/b", "", ended, workSession()...)
	_, _, err := st.SetLiveState(ctx, account, "ended", "ended", "SessionEnd", ended)
	must(t, err)
	_, _, err = st.SetLiveState(ctx, account, "live", "working", "UserPromptSubmit", ended)
	must(t, err)
	now := t0.Add(3*time.Hour + 20*time.Minute) // "live" and "ended" were active 7 minutes ago
	svc := newService(st, now)
	if _, err := svc.Summarize(ctx, "summarised", ExtractEngine{}, ""); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Pending(ctx, t0.Add(-48*time.Hour), 30*time.Minute, 20)
	must(t, err)
	if strings.Join(got, ",") != "done,ended" {
		t.Errorf("pending = %v, want done (idle) and ended (ended, though recent)", got)
	}
	if got, _ := svc.Pending(ctx, t0.Add(-48*time.Hour), 30*time.Minute, 1); len(got) != 1 {
		t.Errorf("limit 1 = %v", got)
	}
}

func TestPendingListsASessionResumedAfterItsNote(t *testing.T) {
	st := memsession.New()
	addSession(t, st, account, "s1", "/w/a", "", t0, workSession()...)
	svc := newService(st, t0.Add(2*time.Hour))
	if _, err := svc.Summarize(ctx, "s1", ExtractEngine{}, ""); err != nil {
		t.Fatal(err)
	}
	must(t, st.TouchSession(ctx, account, "s1", t0.Add(3*time.Hour)))
	svc.Now = func() time.Time { return t0.Add(5 * time.Hour) }
	got, err := svc.Pending(ctx, t0.Add(-time.Hour), 30*time.Minute, 20)
	must(t, err)
	if strings.Join(got, ",") != "s1" {
		t.Errorf("pending = %v, want the resumed session", got)
	}
}

func TestSummarizeStoresOneMemoryWithSessionIDAndProject(t *testing.T) {
	st := memsession.New()
	addSession(t, st, account, "s1", "/w/wt-1", "git@github.com:Owner/Repo.git", t0, workSession()...)
	svc := newService(st, t0.Add(time.Hour))
	eng := &fakeEngine{answer: "## Outcome\nThe daemon restarts."}
	sum, err := svc.Summarize(ctx, "s1", eng, "haiku")
	must(t, err)
	if sum.SessionID != "s1" || sum.Project != "github.com/owner/repo" || sum.Engine != "fake" || sum.Model != "haiku" || sum.CostUSD != 0.002 || sum.Text != "## Outcome\nThe daemon restarts." {
		t.Errorf("summary = %+v", sum)
	}
	if _, err := svc.Summarize(ctx, "s1", eng, "haiku"); err != nil {
		t.Fatal(err)
	}
	events, err := st.MemoryEvents(ctx, session.MemoryQuery{AccountID: account, IncludePrivate: true})
	must(t, err)
	if len(events) != 1 {
		t.Fatalf("%d records, want one (the second store is deduplicated)", len(events))
	}
	ev := events[0]
	mem := ev.Map("memory")
	if ev["session_id"] != "s1" || mem["id"] != "session-summary/s1" || mem["kind"] != Kind || mem["project"] != "github.com/owner/repo" || mem["engine"] != "fake" {
		t.Errorf("stored = %v", ev)
	}
	cached, err := svc.Cached(ctx, "s1")
	must(t, err)
	if cached == nil || cached.Text != sum.Text || cached.SessionID != "s1" || cached.CostUSD != 0.002 {
		t.Errorf("cached = %+v", cached)
	}
	if none, err := svc.Cached(ctx, "nope"); err != nil || none != nil {
		t.Errorf("cached of an unknown session = %v, %v", none, err)
	}
	// A different text replaces the note: still one record.
	eng.answer = "## Outcome\nRewritten."
	if _, err := svc.Summarize(ctx, "s1", eng, "haiku"); err != nil {
		t.Fatal(err)
	}
	events, _ = st.MemoryEvents(ctx, session.MemoryQuery{AccountID: account, IncludePrivate: true})
	if len(events) != 1 || events[0].Map("memory")["text"] != "## Outcome\nRewritten." {
		t.Errorf("after a rewrite: %v", events)
	}
}

func TestSummarizeUnknownOrForeignSessionIsNotFound(t *testing.T) {
	st := memsession.New()
	addSession(t, st, "other", "s1", "/w/a", "", t0, workSession()...)
	if _, err := newService(st, t0).Summarize(ctx, "s1", ExtractEngine{}, ""); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestEngineErrorStoresNothing(t *testing.T) {
	st := memsession.New()
	addSession(t, st, account, "s1", "/w/a", "", t0, workSession()...)
	_, err := newService(st, t0).Summarize(ctx, "s1", &fakeEngine{err: errors.New("rate limited")}, "")
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("err = %v", err)
	}
	if events, _ := st.MemoryEvents(ctx, session.MemoryQuery{AccountID: account, IncludePrivate: true}); len(events) != 0 {
		t.Errorf("stored %v", events)
	}
	if _, err := newService(st, t0).Summarize(ctx, "s1", &fakeEngine{answer: "  "}, ""); err == nil {
		t.Error("an empty note must fail")
	}
}

func TestPromptClippedToBudget(t *testing.T) {
	st := memsession.New()
	acts := []session.Doc{user("Start the long job"), user("Keep going")}
	for i := 0; i < 300; i++ {
		acts = append(acts, call("bash", "Bash", fmt.Sprintf("step %03d %s", i, strings.Repeat("x", 100)), fmt.Sprintf("b%d", i)))
	}
	acts = append(acts, reply("all steps done"))
	addSession(t, st, account, "s1", "/w/a", "", t0, acts...)
	svc := newService(st, t0)
	svc.PromptTokens = 1000
	eng := &fakeEngine{answer: "note"}
	if _, err := svc.Summarize(ctx, "s1", eng, ""); err != nil {
		t.Fatal(err)
	}
	prompt := eng.prompts[0]
	if len(prompt) > 4000 {
		t.Errorf("prompt is %d bytes, over the 4000 of 1000 tokens", len(prompt))
	}
	for _, want := range []string{"## First request\nStart the long job", "## Last reply\nall steps done", "earlier rows left out", "step 299", "</input>"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("clipped prompt lacks %q", want)
		}
	}
	if strings.Contains(prompt, "step 000") {
		t.Error("the oldest timeline rows go first")
	}
}

func TestPromptRedactsSecrets(t *testing.T) {
	st := memsession.New()
	secret := "sk-ant-api03-" + strings.Repeat("A", 40)
	addSession(t, st, account, "s1", "/w/a", "", t0,
		user("Use the key "+secret+" to call the API"),
		user("and the internal host corp-db-77"),
		reply("Done with "+secret+" on corp-db-77"),
	)
	r, errs := redact.New([]string{`corp-db-[0-9]+`})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	svc := newService(st, t0)
	svc.Redact = r
	eng := &fakeEngine{answer: "Called the API with " + secret + " against corp-db-77"}
	sum, err := svc.Summarize(ctx, "s1", eng, "")
	must(t, err)
	for _, text := range []string{eng.prompts[0], sum.Text} {
		if strings.Contains(text, secret) || strings.Contains(text, "corp-db-77") {
			t.Errorf("secret leaked: %q", text)
		}
	}
	if !strings.Contains(eng.prompts[0], "[REDACTED:api-key]") || !strings.Contains(sum.Text, "[REDACTED:pattern-1]") {
		t.Errorf("prompt %q / note %q not masked", eng.prompts[0], sum.Text)
	}
	// Without a configured redactor the built-in rules still apply.
	svc.Redact = nil
	eng.prompts = nil
	if _, err := svc.Summarize(ctx, "s1", eng, ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(eng.prompts[0], secret) {
		t.Error("the built-in rules must always apply")
	}
}

func TestPrivateSpansNeverReachEngine(t *testing.T) {
	st := memsession.New()
	addSession(t, st, account, "s1", "/w/a", "", t0,
		user("Fix it <private>my password is hunter2</private> please"),
		user("second <private>unclosed secret"),
		reply("Fixed. <private>hunter2 again</private> Done."),
	)
	eng := &fakeEngine{answer: "Fixed it. <private>hunter2</private>"}
	sum, err := newService(st, t0).Summarize(ctx, "s1", eng, "")
	must(t, err)
	for _, text := range []string{eng.prompts[0], sum.Text} {
		if strings.Contains(text, "hunter2") || strings.Contains(text, "unclosed secret") || strings.Contains(text, "<private>") {
			t.Errorf("private text reached %q", text)
		}
	}
	if !strings.Contains(eng.prompts[0], "Fix it  please") {
		t.Errorf("the text around a span stays: %q", eng.prompts[0])
	}
}

func TestExtractEngineDeterministicNoCost(t *testing.T) {
	st := memsession.New()
	addSession(t, st, account, "s1", "/w/a", "", t0, workSession()...)
	svc := newService(st, t0)
	a, err := svc.Summarize(ctx, "s1", ExtractEngine{}, "")
	must(t, err)
	b, err := svc.Compose(ctx, "s1", ExtractEngine{}, "")
	must(t, err)
	if a.Text != b.Text || a.CostUSD != 0 || a.Engine != "extract" {
		t.Errorf("not deterministic or not free: %+v vs %+v", a, b)
	}
	want := `## First request
Fix the daemon restart and more detail
## Commits
- Restart the daemon cleanly
## Files edited
- daemon.go (2)
- daemon_test.go (1)
## Unresolved errors
- bash Bash golangci-lint run: daemon.go:12: unused variable
## Last reply
Done: the daemon restarts cleanly and the docs say so.`
	if a.Text != want {
		t.Errorf("extract note =\n%s\nwant\n%s", a.Text, want)
	}
	if strings.Contains(a.Text, "FAIL: TestRestart") {
		t.Error("an error fixed by a later run is not unresolved")
	}
}

func TestExtractEngineTopTenFiles(t *testing.T) {
	st := memsession.New()
	acts := []session.Doc{user("one"), user("two")}
	for i := 0; i < 12; i++ {
		for j := 0; j <= i; j++ {
			acts = append(acts, call("edit", "Edit", fmt.Sprintf("f%02d.go", i), fmt.Sprintf("e%d-%d", i, j)))
			acts = append(acts, user("x")) // breaks the collapsing of repeats
		}
	}
	addSession(t, st, account, "s1", "/w/a", "", t0, acts...)
	sum, err := newService(st, t0).Compose(ctx, "s1", ExtractEngine{}, "")
	must(t, err)
	if strings.Count(sum.Text, ".go (") != 10 || !strings.Contains(sum.Text, "- f11.go (12)") || strings.Contains(sum.Text, "f01.go") {
		t.Errorf("files = %s", sum.Text)
	}
}

func TestExtractEngineCondensesNotes(t *testing.T) {
	prompt := "Roll up.\n<input>\n<note id=\"session-summary/s1\" label=\"session s1\">\n## Outcome\nShipped it.\n</note>\n<note id=\"session-summary/s2\" label=\"session s2\">\nline\n</note>\n</input>"
	text, cost, err := ExtractEngine{}.Run(ctx, prompt, "")
	must(t, err)
	if cost != 0 || text != "### session s1\nOutcome:\nShipped it.\n\n### session s2\nline" {
		t.Errorf("rollup = %q", text)
	}
}

func TestShortSessionSkipped(t *testing.T) {
	st := memsession.New()
	addSession(t, st, account, "s1", "/w/a", "", t0, user("only one prompt"), reply("ok"))
	svc := newService(st, t0.Add(2*time.Hour))
	eng := &fakeEngine{answer: "x"}
	if _, err := svc.Summarize(ctx, "s1", eng, ""); !errors.Is(err, ErrSkipped) {
		t.Errorf("err = %v, want ErrSkipped", err)
	}
	if len(eng.prompts) != 0 {
		t.Error("a skipped session must not reach the engine")
	}
	if got, _ := svc.Pending(ctx, t0.Add(-time.Hour), time.Minute, 20); len(got) != 0 {
		t.Errorf("pending = %v", got)
	}
	svc.MinPrompts = 1
	if _, err := svc.Summarize(ctx, "s1", eng, ""); err != nil {
		t.Errorf("with minPrompts 1: %v", err)
	}
}

func TestOptedOutSessionSkipped(t *testing.T) {
	st := memsession.New()
	addSession(t, st, account, "off", "/w/a", "", t0, workSession()...)
	must(t, st.SetSessionMeta(ctx, account, "off", session.Doc{"memory": "off"}))
	addSession(t, st, account, "secret", "/w/secret/sub", "", t0, workSession()...)
	addSession(t, st, account, "kept", "/w/a", "", t0, workSession()...)
	svc := newService(st, t0.Add(2*time.Hour))
	svc.Exclude = []string{"/w/secret"}
	eng := &fakeEngine{answer: "x"}
	for _, sid := range []string{"off", "secret"} {
		if _, err := svc.Summarize(ctx, sid, eng, ""); !errors.Is(err, ErrSkipped) {
			t.Errorf("%s: err = %v, want ErrSkipped", sid, err)
		}
	}
	if len(eng.prompts) != 0 {
		t.Error("an opted-out session must not reach the engine")
	}
	got, err := svc.Pending(ctx, t0.Add(-time.Hour), time.Minute, 20)
	must(t, err)
	if strings.Join(got, ",") != "kept" {
		t.Errorf("pending = %v", got)
	}
}

func TestCheckpointReplacedByNoteAndEditedNoteKept(t *testing.T) {
	st := memsession.New()
	addSession(t, st, account, "s1", "/w/a", "", t0, workSession()...)
	svc := newService(st, t0)
	if _, err := svc.Checkpoint(ctx, "s1", &fakeEngine{answer: "checkpoint"}, ""); err != nil {
		t.Fatal(err)
	}
	if events, _ := st.MemoryEvents(ctx, session.MemoryQuery{AccountID: account, Kinds: []string{CheckpointKind}}); len(events) != 1 {
		t.Fatalf("checkpoints = %v", events)
	}
	if _, err := svc.Summarize(ctx, "s1", &fakeEngine{answer: "note"}, ""); err != nil {
		t.Fatal(err)
	}
	if events, _ := st.MemoryEvents(ctx, session.MemoryQuery{AccountID: account, Kinds: []string{CheckpointKind}}); len(events) != 0 {
		t.Errorf("the note must replace the checkpoint: %v", events)
	}
	// An edited note is never overwritten.
	cached, _ := svc.Cached(ctx, "s1")
	edited := session.Doc{"hook_event_name": session.MemoryEventName, "account_id": account, "session_id": "s1", "created_at": t0,
		"memory": session.Doc{"id": ID("s1"), "kind": Kind, "project": cached.Project, "text": "my words", "hash": "edited-1", "edited": true}}
	if _, err := Replace(ctx, st, account, edited); err != nil {
		t.Fatal(err)
	}
	eng := &fakeEngine{answer: "engine words"}
	sum, err := svc.Summarize(ctx, "s1", eng, "")
	must(t, err)
	if sum.Text != "my words" || len(eng.prompts) != 0 {
		t.Errorf("edited note replaced: %+v (engine calls %d)", sum, len(eng.prompts))
	}
}

// Package sessiontest is the conformance suite every session.Store adapter
// must pass. It is the Go form of test/integration/mongo.test.ts plus the
// behaviours the use cases rely on, and it uses the session package API only.
//
// Adapters return session.Doc values whose nested objects are map[string]any
// or session.Doc, whose nested lists are []any or []session.Doc, whose time
// fields are time.Time and whose numbers may be int, int32, int64 or float64;
// the suite normalises all of these before comparing.
package sessiontest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

var (
	ctx = context.Background()
	t0  = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
)

// Run executes the suite; open must return a fresh, empty store each call.
func Run(t *testing.T, open func(*testing.T) session.Store) {
	cases := []struct {
		name string
		run  func(*testing.T, session.Store)
	}{
		{"session upsert sets fields and started_at once", sessionUpsert},
		{"session upsert stores host as a nested document", sessionHost},
		{"touch updates last_seen only for the owning account", touchSession},
		{"git info is written only when git_origin is missing", gitInfoIfMissing},
		{"get session is scoped to the account", getSessionScoping},
		{"set model writes the model on a change and reports the previous one", setModel},
		{"last model before finds the newest assistant line's model under a seq or time", lastModelBefore},
		{"find sessions filters by account and regexes", findSessionsFilters},
		{"find sessions projects, orders by last_seen and limits", findSessionsOrdering},
		{"find sessions rejects an invalid regex", findSessionsInvalidRegex},
		{"hook event insert stamps nothing extra and copies the input", hookEventInsert},
		{"transcript upsert is keyed by session and seq and preserves created_at", transcriptUpsert},
		{"transcript count and lines are account scoped", transcriptScoping},
		{"transcript lines page by offset and limit, ascending or descending", transcriptPaging},
		{"subagent upsert is keyed by session, subagent and seq", subagentUpsert},
		{"subagent ids and lines are ordered and account scoped", subagentOrdering},
		{"blob upsert replaces content and preserves created_at", blobUpsert},
		{"blobs are ordered by type and name and account scoped", blobsOrdering},
		{"bash events filter by regex, session and account, newest first", bashEvents},
		{"bash events reject an invalid regex", bashEventsInvalidRegex},
		{"file events select tracked and measured file events by path, newest first", fileEvents},
		{"error lines select flagged lines by type and session, newest first", errorLines},
		{"search transcripts ranks lines by their indexed words, scoped to the account", searchTranscripts},
		{"usage totals count each message once, by session, model or day", usageTotals},
		{"memory events filter by regex, kind, session and account, newest first", memoryEvents},
		{"memory events reject an invalid regex", memoryEventsInvalidRegex},
		{"a memory event with a hash the account already holds is dropped", memoryEventDedup},
		{"usage events filter by session, time and account, newest first", limitSamples},
		{"activities select labelled lines by kind, session and time, newest first", activities},
		{"full session joins sub-collections and strips redundant fields", fullSessionShape},
		{"full session of a session with nothing else has empty arrays", fullSessionEmpty},
		{"full session is not found for a missing or foreign session", fullSessionNotFound},
		{"unenriched excludes documents that are enriched or failed", unenrichedSelection},
		{"set enrichment writes enrichments.<name> on the matching document", setEnrichment},
		{"set enrichment failure records message and time", setEnrichmentFailure},
		{"set enrichment skipped marks the document and leaves the queue", setEnrichmentSkipped},
		{"requeue skipped returns only that account's skipped documents to the queue", requeueSkipped},
		{"hook event ids by tool use are scoped to the session", hookEventIDsByToolUse},
		{"hook event ids by tool use with an empty session id match across sessions", hookEventIDsAcrossSessions},
		{"find sessions caps the limit at 500 and treats a limit of 0 or less as the cap", findSessionsLimitCap},
		{"a limit of 0 or less means no limit on lines, bash events and unenriched", nonPositiveLimitIsUnlimited},
		{"delete account removes only that account's documents", deleteAccount},
		{"returned documents are copies", returnedDocsAreCopies},
		{"stored inputs are copies", storedInputsAreCopies},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { runCase(t, c.run, open(t)) })
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// storeError carries a Store error out of ok to the subtest runner.
type storeError struct{ err error }

// ok unwraps a (value, error) result; an error aborts the subtest.
func ok[V any](v V, err error) V {
	if err != nil {
		panic(storeError{err})
	}
	return v
}

// runCase runs one subtest, turning an error raised by ok into a test failure.
func runCase(t *testing.T, run func(*testing.T, session.Store), s session.Store) {
	defer func() {
		if r := recover(); r != nil {
			se, isStoreErr := r.(storeError)
			if !isStoreErr {
				panic(r)
			}
			t.Fatalf("unexpected error: %v", se.err)
		}
	}()
	run(t, s)
}

func eq(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %#v, want %#v", what, got, want)
	}
}

func timeOf(t *testing.T, d session.Doc, key string) time.Time {
	t.Helper()
	v, ok := d[key].(time.Time)
	if !ok {
		t.Fatalf("%s = %#v, want time.Time", key, d[key])
	}
	return v
}

func hasKey(d session.Doc, key string) bool { _, ok := d[key]; return ok }

// docList reads a joined sub-array whichever slice type the adapter produces.
func docList(t *testing.T, d session.Doc, key string) []session.Doc {
	t.Helper()
	switch list := d[key].(type) {
	case []session.Doc:
		return list
	case []any:
		out := make([]session.Doc, len(list))
		for i, item := range list {
			m, ok := item.(map[string]any)
			if d2, isDoc := item.(session.Doc); isDoc {
				m, ok = d2, true
			}
			if !ok {
				t.Fatalf("%s[%d] = %#v, want object", key, i, item)
			}
			out[i] = session.Doc(m)
		}
		return out
	}
	t.Fatalf("%s = %#v, want a list of objects", key, d[key])
	return nil
}

// num normalises any integer or float representation of a number.
func num(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return -1
}

func ints(docs []session.Doc, key string) []int {
	out := make([]int, len(docs))
	for i, d := range docs {
		out[i] = num(d[key])
	}
	return out
}

// strList reads a list of strings whichever slice type the adapter produces.
func strList(v any) []string {
	switch list := v.(type) {
	case []string:
		return list
	case []any:
		out := make([]string, len(list))
		for i, e := range list {
			out[i], _ = e.(string)
		}
		return out
	}
	return nil
}

func sortedKeys(d session.Doc) []string {
	var ks []string
	for k := range d {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func strs(docs []session.Doc, key string) []string {
	out := make([]string, len(docs))
	for i, d := range docs {
		out[i], _ = d.String(key)
	}
	return out
}

// idString renders a document _id the way Store.HookEventIDsByToolUse does.
func idString(id any) string {
	if h, ok := id.(interface{ Hex() string }); ok {
		return h.Hex()
	}
	return fmt.Sprint(id)
}

func sessionUpsert(t *testing.T, s session.Store) {
	must(t, s.UpsertSession(ctx, session.Session{SessionID: "s1", Cwd: "/tmp", ProjectPath: "/p", AccountID: "acc", LastSeen: t0}))
	must(t, s.UpsertSession(ctx, session.Session{SessionID: "s1", TranscriptPath: "/t.jsonl", GitOrigin: "git@x:y.git", GitBranch: "main", AccountID: "acc", LastSeen: t0.Add(time.Hour)}))

	got := ok(s.GetSession(ctx, "acc", "s1"))
	eq(t, "cwd kept when later upsert omits it", got["cwd"], "/tmp")
	eq(t, "project_path kept", got["project_path"], "/p")
	eq(t, "transcript_path", got["transcript_path"], "/t.jsonl")
	eq(t, "git_origin", got["git_origin"], "git@x:y.git")
	eq(t, "git_branch", got["git_branch"], "main")
	eq(t, "account_id", got["account_id"], "acc")
	if !timeOf(t, got, "started_at").Equal(t0) {
		t.Errorf("started_at = %v, want first insert time %v", got["started_at"], t0)
	}
	if !timeOf(t, got, "last_seen").Equal(t0.Add(time.Hour)) {
		t.Errorf("last_seen = %v, want %v", got["last_seen"], t0.Add(time.Hour))
	}
	if hasKey(got, "_id") {
		t.Errorf("GetSession returned _id")
	}
}

func sessionHost(t *testing.T, s session.Store) {
	ip := "10.0.0.1"
	must(t, s.UpsertSession(ctx, session.Session{SessionID: "s1", AccountID: "acc", LastSeen: t0, Host: &session.HostInfo{Hostname: "h", IP: &ip, Username: "u", UID: 501, Platform: "darwin"}}))
	got := ok(s.GetSession(ctx, "acc", "s1"))
	host := got.Map("host")
	if host == nil {
		t.Fatalf("host = %#v, want nested document", got["host"])
	}
	eq(t, "host.hostname", host["hostname"], "h")
	eq(t, "host.platform", host["platform"], "darwin")
	eq(t, "host.ip", host["ip"], "10.0.0.1")
}

func touchSession(t *testing.T, s session.Store) {
	must(t, s.UpsertSession(ctx, session.Session{SessionID: "s1", AccountID: "acc", LastSeen: t0}))
	later := t0.Add(time.Hour)

	must(t, s.TouchSession(ctx, "other", "s1", later))
	must(t, s.TouchSession(ctx, "acc", "missing", later))
	got := ok(s.GetSession(ctx, "acc", "s1"))
	if !timeOf(t, got, "last_seen").Equal(t0) {
		t.Errorf("last_seen changed by a foreign account: %v", got["last_seen"])
	}
	if _, err := s.GetSession(ctx, "acc", "missing"); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("touch created a session: err = %v", err)
	}

	must(t, s.TouchSession(ctx, "acc", "s1", later))
	got = ok(s.GetSession(ctx, "acc", "s1"))
	if !timeOf(t, got, "last_seen").Equal(later) {
		t.Errorf("last_seen = %v, want %v", got["last_seen"], later)
	}
	if !timeOf(t, got, "started_at").Equal(t0) {
		t.Errorf("touch changed started_at: %v", got["started_at"])
	}
}

func gitInfoIfMissing(t *testing.T, s session.Store) {
	must(t, s.UpsertSession(ctx, session.Session{SessionID: "s1", AccountID: "acc", LastSeen: t0}))
	must(t, s.UpsertSession(ctx, session.Session{SessionID: "s2", AccountID: "acc", LastSeen: t0, GitOrigin: "keep", GitBranch: "keep-branch"}))

	must(t, s.SetGitInfoIfMissing(ctx, "s1", "origin-1", "br"))
	must(t, s.SetGitInfoIfMissing(ctx, "s2", "origin-2", "other"))
	must(t, s.SetGitInfoIfMissing(ctx, "absent", "origin-3", "br"))

	s1 := ok(s.GetSession(ctx, "acc", "s1"))
	eq(t, "s1 git_origin", s1["git_origin"], "origin-1")
	eq(t, "s1 git_branch", s1["git_branch"], "br")
	s2 := ok(s.GetSession(ctx, "acc", "s2"))
	eq(t, "s2 git_origin untouched", s2["git_origin"], "keep")
	eq(t, "s2 git_branch untouched", s2["git_branch"], "keep-branch")
	if _, err := s.GetSession(ctx, "acc", "absent"); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("git info created a session: err = %v", err)
	}

	must(t, s.UpsertSession(ctx, session.Session{SessionID: "s3", AccountID: "acc", LastSeen: t0}))
	must(t, s.SetGitInfoIfMissing(ctx, "s3", "origin-4", ""))
	s3 := ok(s.GetSession(ctx, "acc", "s3"))
	eq(t, "s3 git_origin", s3["git_origin"], "origin-4")
	if hasKey(s3, "git_branch") {
		t.Errorf("empty branch was written: %#v", s3["git_branch"])
	}
}

func getSessionScoping(t *testing.T, s session.Store) {
	must(t, s.UpsertSession(ctx, session.Session{SessionID: "s1", AccountID: "acc", LastSeen: t0}))
	if _, err := s.GetSession(ctx, "other", "s1"); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("foreign account: err = %v, want ErrNotFound", err)
	}
	if _, err := s.GetSession(ctx, "acc", "nope"); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("missing session: err = %v, want ErrNotFound", err)
	}
}

func seedSessions(t *testing.T, s session.Store) {
	t.Helper()
	for _, x := range []session.Session{
		{SessionID: "a", AccountID: "acc", ProjectPath: "/work/Alpha", Cwd: "/work/Alpha/sub", GitOrigin: "git@github.com:me/alpha.git", LastSeen: t0.Add(1 * time.Hour)},
		{SessionID: "b", AccountID: "acc", ProjectPath: "/work/beta", Cwd: "/elsewhere/gamma", GitOrigin: "git@github.com:me/beta.git", LastSeen: t0.Add(3 * time.Hour)},
		{SessionID: "c", AccountID: "acc", ProjectPath: "/work/gamma", Cwd: "/work/gamma", LastSeen: t0.Add(2 * time.Hour)},
		{SessionID: "z", AccountID: "other", ProjectPath: "/work/alpha", LastSeen: t0.Add(9 * time.Hour)},
	} {
		must(t, s.UpsertSession(ctx, x))
	}
}

func findSessionsFilters(t *testing.T, s session.Store) {
	seedSessions(t, s)
	find := func(q session.SessionQuery) []string {
		return strs(ok(s.FindSessions(ctx, q)), "session_id")
	}

	eq(t, "account only", find(session.SessionQuery{AccountID: "acc", Limit: 10}), []string{"b", "c", "a"})
	eq(t, "project regex is case-insensitive", find(session.SessionQuery{AccountID: "acc", ProjectPathRe: "alpha", Limit: 10}), []string{"a"})
	eq(t, "git origin regex", find(session.SessionQuery{AccountID: "acc", GitOriginRe: "BETA", Limit: 10}), []string{"b"})
	eq(t, "query matches project_path or cwd", find(session.SessionQuery{AccountID: "acc", QueryRe: "gamma", Limit: 10}), []string{"b", "c"})
	eq(t, "filters combine", find(session.SessionQuery{AccountID: "acc", QueryRe: "gamma", ProjectPathRe: "beta", Limit: 10}), []string{"b"})
	eq(t, "no match", find(session.SessionQuery{AccountID: "acc", ProjectPathRe: "zzz", Limit: 10}), []string{})
}

func setModel(t *testing.T, s session.Store) {
	must(t, s.UpsertSession(ctx, session.Session{SessionID: "s1", AccountID: "acc", LastSeen: t0}))
	previous, changed, err := s.SetModel(ctx, "acc", "s1", "claude-a", t0)
	must(t, err)
	eq(t, "first model", []any{previous, changed}, []any{"", true})
	got := ok(s.GetSession(ctx, "acc", "s1"))
	eq(t, "model stored", got["model"], "claude-a")
	if !timeOf(t, got, "model_changed_at").Equal(t0) {
		t.Errorf("model_changed_at = %v, want %v", got["model_changed_at"], t0)
	}

	previous, changed, err = s.SetModel(ctx, "acc", "s1", "claude-a", t0.Add(time.Hour))
	must(t, err)
	eq(t, "same model", []any{previous, changed}, []any{"claude-a", false})
	if !timeOf(t, ok(s.GetSession(ctx, "acc", "s1")), "model_changed_at").Equal(t0) {
		t.Error("the same model must not move model_changed_at")
	}

	previous, changed, err = s.SetModel(ctx, "acc", "s1", "claude-b", t0.Add(time.Hour))
	must(t, err)
	eq(t, "changed model", []any{previous, changed}, []any{"claude-a", true})
	got = ok(s.GetSession(ctx, "acc", "s1"))
	eq(t, "new model", got["model"], "claude-b")
	if !timeOf(t, got, "model_changed_at").Equal(t0.Add(time.Hour)) {
		t.Errorf("model_changed_at = %v", got["model_changed_at"])
	}

	if _, _, err := s.SetModel(ctx, "other", "s1", "claude-c", t0); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("foreign account: err = %v, want ErrNotFound", err)
	}
	if _, _, err := s.SetModel(ctx, "acc", "missing", "claude-c", t0); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("missing session: err = %v, want ErrNotFound", err)
	}
	previous, changed, err = s.SetModel(ctx, "acc", "s1", "", t0)
	must(t, err)
	eq(t, "empty model is ignored", []any{previous, changed}, []any{"", false})
	eq(t, "model kept", ok(s.GetSession(ctx, "acc", "s1"))["model"], "claude-b")

	found := ok(s.FindSessions(ctx, session.SessionQuery{AccountID: "acc", Limit: 5}))
	eq(t, "find sessions projects the model", found[0]["model"], "claude-b")
}

func lastModelBefore(t *testing.T, s session.Store) {
	assistant := func(sid string, seq int, model string) session.TranscriptLine {
		return session.TranscriptLine{SessionID: sid, Seq: seq, AccountID: "acc", Line: map[string]any{"type": "assistant", "message": map[string]any{"role": "assistant", "model": model}}}
	}
	must(t, s.UpsertTranscriptLines(ctx, []session.TranscriptLine{tl("s1", 0, "user", "acc"), assistant("s1", 1, "claude-a"), assistant("s1", 2, session.SyntheticModel)}))
	time.Sleep(5 * time.Millisecond)
	must(t, s.UpsertTranscriptLines(ctx, []session.TranscriptLine{assistant("s1", 3, "claude-b"), assistant("s2", 0, "claude-z")}))
	model := func(sid string, seq int, at time.Time) string {
		return ok(s.LastModelBefore(ctx, sid, seq, at))
	}
	eq(t, "newest model of the session", model("s1", -1, time.Time{}), "claude-b")
	eq(t, "below a seq, skipping synthetic lines", model("s1", 3, time.Time{}), "claude-a")
	eq(t, "no assistant line below", model("s1", 1, time.Time{}), "")
	eq(t, "unknown session", model("zzz", -1, time.Time{}), "")
	first := ok(s.TranscriptLines(ctx, session.LineQuery{AccountID: "acc", SessionID: "s1", Limit: 2}))
	at := timeOf(t, first[1], "created_at")
	eq(t, "not after a time", model("s1", -1, at), "claude-a")
	eq(t, "before the first line", model("s1", -1, at.Add(-time.Second)), "")
}

func findSessionsOrdering(t *testing.T, s session.Store) {
	seedSessions(t, s)
	must(t, s.UpsertSession(ctx, session.Session{SessionID: "a", AccountID: "acc", TranscriptPath: "/t", LastSeen: t0.Add(1 * time.Hour)}))

	got := ok(s.FindSessions(ctx, session.SessionQuery{AccountID: "acc", Limit: 2}))
	eq(t, "newest first, limited", strs(got, "session_id"), []string{"b", "c"})

	keys := sortedKeys
	eq(t, "projection of a full session", keys(got[0]), []string{"cwd", "git_origin", "last_seen", "project_path", "session_id", "started_at"})

	all := ok(s.FindSessions(ctx, session.SessionQuery{AccountID: "acc", Limit: 10}))
	eq(t, "projection omits absent fields and transcript_path", keys(all[1]), []string{"cwd", "last_seen", "project_path", "session_id", "started_at"})
}

func findSessionsInvalidRegex(t *testing.T, s session.Store) {
	seedSessions(t, s)
	for _, q := range []session.SessionQuery{
		{AccountID: "acc", ProjectPathRe: "(", Limit: 5},
		{AccountID: "acc", GitOriginRe: "(", Limit: 5},
		{AccountID: "acc", QueryRe: "(", Limit: 5},
	} {
		if _, err := s.FindSessions(ctx, q); err == nil {
			t.Errorf("FindSessions(%+v) accepted an invalid regex", q)
		}
	}
}

func hookEventInsert(t *testing.T, s session.Store) {
	ev := session.Doc{"session_id": "s1", "type": "PreToolUse", "tool_input": session.Doc{"command": "ls"}}
	must(t, s.InsertHookEvent(ctx, ev))
	ev["type"] = "mutated"
	ev.Map("tool_input")["command"] = "mutated"

	docs := ok(s.Unenriched(ctx, "hook_events", "probe", 10))
	if len(docs) != 1 {
		t.Fatalf("hook_events holds %d documents, want 1", len(docs))
	}
	got := docs[0]
	if !hasKey(got, "_id") {
		t.Errorf("stored hook event has no _id")
	}
	eq(t, "stored fields (nothing stamped)", sortedKeys(got), []string{"_id", "session_id", "tool_input", "type"})
	eq(t, "type (input copied)", got["type"], "PreToolUse")
	eq(t, "tool_input.command (input copied)", got.ToolInput()["command"], "ls")
}

func tl(sid string, seq int, typ, acc string) session.TranscriptLine {
	return session.TranscriptLine{SessionID: sid, Seq: seq, Line: map[string]any{"type": typ}, AccountID: acc}
}

func transcriptUpsert(t *testing.T, s session.Store) {
	must(t, s.UpsertTranscriptLines(ctx, []session.TranscriptLine{tl("s1", 0, "human", "acc"), tl("s1", 1, "assistant", "acc")}))
	first := ok(s.TranscriptLines(ctx, session.LineQuery{AccountID: "acc", SessionID: "s1"}))
	created := timeOf(t, first[0], "created_at")

	time.Sleep(5 * time.Millisecond)
	must(t, s.UpsertTranscriptLines(ctx, []session.TranscriptLine{tl("s1", 0, "system", "acc")}))

	if n := ok(s.CountTranscriptLines(ctx, "acc", "s1")); n != 2 {
		t.Errorf("count after re-upsert = %d, want 2", n)
	}
	got := ok(s.TranscriptLines(ctx, session.LineQuery{AccountID: "acc", SessionID: "s1"}))
	if !timeOf(t, got[0], "created_at").Equal(created) {
		t.Errorf("created_at overwritten: %v, want %v", got[0]["created_at"], created)
	}
	eq(t, "line replaced", got[0].Line()["type"], "system")
	eq(t, "other line untouched", got[1].Line()["type"], "assistant")

	host := &session.HostInfo{Hostname: "h"}
	must(t, s.UpsertTranscriptLines(ctx, []session.TranscriptLine{{SessionID: "s2", Seq: 0, Line: map[string]any{}, AccountID: "acc", Host: host}}))
	h := ok(s.TranscriptLines(ctx, session.LineQuery{AccountID: "acc", SessionID: "s2"}))
	eq(t, "host stored", h[0].Map("host")["hostname"], "h")
	eq(t, "session_id stored", h[0]["session_id"], "s2")
	eq(t, "seq stored", num(h[0]["seq"]), 0)
	eq(t, "account_id stored", h[0]["account_id"], "acc")
}

func transcriptScoping(t *testing.T, s session.Store) {
	must(t, s.UpsertTranscriptLines(ctx, []session.TranscriptLine{tl("s1", 0, "a", "acc"), tl("s1", 1, "b", "acc"), tl("s2", 0, "c", "acc")}))

	eq(t, "count", ok(s.CountTranscriptLines(ctx, "acc", "s1")), 2)
	eq(t, "count for a foreign account", ok(s.CountTranscriptLines(ctx, "other", "s1")), 0)
	eq(t, "count for a missing session", ok(s.CountTranscriptLines(ctx, "acc", "none")), 0)

	foreign := ok(s.TranscriptLines(ctx, session.LineQuery{AccountID: "other", SessionID: "s1"}))
	eq(t, "lines for a foreign account", len(foreign), 0)
	unfiltered := ok(s.TranscriptLines(ctx, session.LineQuery{SessionID: "s1"}))
	eq(t, "empty account means no account filter", ints(unfiltered, "seq"), []int{0, 1})
}

func transcriptPaging(t *testing.T, s session.Store) {
	var lines []session.TranscriptLine
	for _, seq := range []int{4, 0, 3, 1, 2} { // inserted out of order
		lines = append(lines, tl("s1", seq, fmt.Sprint("t", seq), "acc"))
	}
	must(t, s.UpsertTranscriptLines(ctx, lines))
	q := func(offset, limit int, desc bool) []session.Doc {
		return ok(s.TranscriptLines(ctx, session.LineQuery{AccountID: "acc", SessionID: "s1", Offset: offset, Limit: limit, Descending: desc}))
	}

	all := q(0, 10, false)
	eq(t, "ascending", ints(all, "seq"), []int{0, 1, 2, 3, 4})
	if hasKey(all[0], "_id") {
		t.Errorf("TranscriptLines returned _id")
	}
	eq(t, "offset and limit", ints(q(1, 2, false), "seq"), []int{1, 2})
	eq(t, "offset past the end", ints(q(9, 2, false), "seq"), []int{})
	eq(t, "descending", ints(q(0, 3, true), "seq"), []int{4, 3, 2})
	eq(t, "descending with offset", ints(q(1, 2, true), "seq"), []int{3, 2})
}

func sub(sid, agent string, seq int, acc string) session.SubagentLine {
	return session.SubagentLine{SessionID: sid, SubagentID: agent, Seq: seq, Line: map[string]any{"type": "user"}, AccountID: acc}
}

func subagentUpsert(t *testing.T, s session.Store) {
	must(t, s.UpsertSubagentLine(ctx, sub("s1", "agent-abc", 0, "acc")))
	first := ok(s.SubagentLines(ctx, "acc", "s1", "agent-abc"))
	created := timeOf(t, first[0], "created_at")

	time.Sleep(5 * time.Millisecond)
	again := sub("s1", "agent-abc", 0, "acc")
	again.Line = map[string]any{"type": "assistant"}
	must(t, s.UpsertSubagentLine(ctx, again))

	got := ok(s.SubagentLines(ctx, "acc", "s1", "agent-abc"))
	eq(t, "one line per key", len(got), 1)
	if !timeOf(t, got[0], "created_at").Equal(created) {
		t.Errorf("created_at overwritten: %v, want %v", got[0]["created_at"], created)
	}
	eq(t, "line replaced", got[0].Line()["type"], "assistant")
	if hasKey(got[0], "_id") {
		t.Errorf("SubagentLines returned _id")
	}
}

func subagentOrdering(t *testing.T, s session.Store) {
	for _, l := range []session.SubagentLine{
		sub("s1", "agent-b", 1, "acc"), sub("s1", "agent-b", 0, "acc"),
		sub("s1", "agent-a", 0, "acc"), sub("s2", "agent-z", 0, "acc"),
		sub("s1", "agent-foreign", 0, "other"),
	} {
		must(t, s.UpsertSubagentLine(ctx, l))
	}
	eq(t, "ids sorted and distinct", ok(s.SubagentIDs(ctx, "acc", "s1")), []string{"agent-a", "agent-b"})
	eq(t, "ids for a foreign account", ok(s.SubagentIDs(ctx, "nobody", "s1")), []string{})
	eq(t, "lines ordered by seq", ints(ok(s.SubagentLines(ctx, "acc", "s1", "agent-b")), "seq"), []int{0, 1})
	eq(t, "lines for a foreign account", len(ok(s.SubagentLines(ctx, "other", "s1", "agent-b"))), 0)
}

func blob(sid, typ, name, content, acc string) session.Blob {
	return session.Blob{SessionID: sid, BlobType: typ, Name: name, Content: content, Encoding: "utf8", AccountID: acc}
}

func blobUpsert(t *testing.T, s session.Store) {
	must(t, s.UpsertBlob(ctx, blob("s1", "tool-result", "abc.txt", "first", "acc")))
	first := ok(s.Blobs(ctx, "acc", "s1"))
	created := timeOf(t, first[0], "created_at")

	time.Sleep(5 * time.Millisecond)
	must(t, s.UpsertBlob(ctx, blob("s1", "tool-result", "abc.txt", "second", "acc")))

	got := ok(s.Blobs(ctx, "acc", "s1"))
	eq(t, "one blob per key", len(got), 1)
	eq(t, "content replaced", got[0]["content"], "second")
	eq(t, "encoding", got[0]["encoding"], "utf8")
	if !timeOf(t, got[0], "created_at").Equal(created) {
		t.Errorf("created_at overwritten: %v, want %v", got[0]["created_at"], created)
	}
	if hasKey(got[0], "_id") {
		t.Errorf("Blobs returned _id")
	}
}

func blobsOrdering(t *testing.T, s session.Store) {
	for _, b := range []session.Blob{
		blob("s1", "tool-result", "b.txt", "1", "acc"), blob("s1", "file-history", "z", "2", "acc"),
		blob("s1", "tool-result", "a.txt", "3", "acc"), blob("s2", "tool-result", "x", "4", "acc"),
		blob("s1", "tool-result", "foreign", "5", "other"),
	} {
		must(t, s.UpsertBlob(ctx, b))
	}
	got := ok(s.Blobs(ctx, "acc", "s1"))
	eq(t, "ordered by blob_type then name", strs(got, "name"), []string{"z", "a.txt", "b.txt"})
	eq(t, "blobs for a foreign account", len(ok(s.Blobs(ctx, "nobody", "s1"))), 0)
}

func bash(sid, acc, cmd string, at time.Time) session.Doc {
	return session.Doc{"session_id": sid, "account_id": acc, "tool_name": "Bash", "tool_input": session.Doc{"command": cmd}, "created_at": at, "extra": "x"}
}

func bashEvents(t *testing.T, s session.Store) {
	for _, ev := range []session.Doc{
		bash("s1", "acc", "git push origin", t0),
		bash("s1", "acc", "ls -la", t0.Add(time.Minute)),
		bash("s2", "acc", "GIT status", t0.Add(2*time.Minute)),
		bash("s3", "other", "git log", t0.Add(3*time.Minute)),
		{"session_id": "s1", "account_id": "acc", "tool_name": "Read", "tool_input": session.Doc{"command": "git"}, "created_at": t0},
		{"session_id": "s1", "account_id": "acc", "tool_name": "Bash", "tool_input": session.Doc{"command": 7}, "created_at": t0},
	} {
		must(t, s.InsertHookEvent(ctx, ev))
	}
	cmds := func(q session.CommandQuery) []string {
		var out []string
		for _, d := range ok(s.BashEvents(ctx, q)) {
			out = append(out, d.ToolInput()["command"].(string))
		}
		return out
	}

	eq(t, "regex is case-insensitive, newest first, own account and Bash only",
		cmds(session.CommandQuery{AccountID: "acc", PatternRe: "git", Limit: 10}), []string{"GIT status", "git push origin"})
	eq(t, "session scoping", cmds(session.CommandQuery{AccountID: "acc", PatternRe: "git", SessionIDs: []string{"s1"}, Limit: 10}), []string{"git push origin"})
	eq(t, "limit", cmds(session.CommandQuery{AccountID: "acc", PatternRe: ".", Limit: 1}), []string{"GIT status"})
	eq(t, "no match", cmds(session.CommandQuery{AccountID: "acc", PatternRe: "zzz", Limit: 10}), []string(nil))

	got := ok(s.BashEvents(ctx, session.CommandQuery{AccountID: "acc", PatternRe: "ls", Limit: 10}))
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	eq(t, "projection fields", sortedKeys(got[0]), []string{"created_at", "session_id", "tool_input"})
	eq(t, "session_id", got[0]["session_id"], "s1")
	eq(t, "tool_input.command", got[0].ToolInput()["command"], "ls -la")
	if !timeOf(t, got[0], "created_at").Equal(t0.Add(time.Minute)) {
		t.Errorf("created_at = %v, want %v", got[0]["created_at"], t0.Add(time.Minute))
	}
}

func bashEventsInvalidRegex(t *testing.T, s session.Store) {
	if _, err := s.BashEvents(ctx, session.CommandQuery{AccountID: "acc", PatternRe: "(", Limit: 5}); err == nil {
		t.Errorf("BashEvents accepted an invalid regex")
	}
}

func fullSessionShape(t *testing.T, s session.Store) {
	const sid = "view-test-session"
	host := &session.HostInfo{Hostname: "h"}
	must(t, s.UpsertSession(ctx, session.Session{SessionID: sid, Cwd: "/tmp", AccountID: "acc", Host: host, LastSeen: t0}))
	must(t, s.UpsertSession(ctx, session.Session{SessionID: "other-session", AccountID: "acc", LastSeen: t0}))
	must(t, s.UpsertTranscriptLines(ctx, []session.TranscriptLine{
		{SessionID: sid, Seq: 1, Line: map[string]any{"type": "human"}, AccountID: "acc", Host: host},
		{SessionID: sid, Seq: 0, Line: map[string]any{"type": "system"}, AccountID: "acc", Host: host},
		{SessionID: "other-session", Seq: 0, Line: map[string]any{}, AccountID: "acc"},
	}))
	must(t, s.UpsertSubagentLine(ctx, session.SubagentLine{SessionID: sid, SubagentID: "agent-y", Seq: 0, Line: map[string]any{}, AccountID: "acc", Host: host}))
	must(t, s.UpsertSubagentLine(ctx, session.SubagentLine{SessionID: sid, SubagentID: "agent-x", Seq: 1, Line: map[string]any{}, AccountID: "acc", Host: host}))
	must(t, s.UpsertSubagentLine(ctx, session.SubagentLine{SessionID: sid, SubagentID: "agent-x", Seq: 0, Line: map[string]any{}, AccountID: "acc", Host: host}))
	must(t, s.UpsertBlob(ctx, blob(sid, "tool-result", "out.txt", "secret", "acc")))
	must(t, s.UpsertBlob(ctx, blob(sid, "file-history", "h", "secret", "acc")))
	must(t, s.InsertHookEvent(ctx, session.Doc{"session_id": sid, "hook_event_name": "Stop", "account_id": "acc", "created_at": t0.Add(time.Minute)}))
	must(t, s.InsertHookEvent(ctx, session.Doc{"session_id": sid, "hook_event_name": "PreToolUse", "account_id": "acc", "created_at": t0}))
	must(t, s.InsertHookEvent(ctx, session.Doc{"session_id": "other-session", "hook_event_name": "Other", "account_id": "acc", "created_at": t0}))

	doc := ok(s.FullSession(ctx, "acc", sid))
	eq(t, "session_id", doc["session_id"], sid)
	eq(t, "session cwd", doc["cwd"], "/tmp")
	if hasKey(doc, "_id") {
		t.Errorf("FullSession returned _id")
	}

	lines := docList(t, doc, "transcript_lines")
	eq(t, "transcript_lines sorted by seq", ints(lines, "seq"), []int{0, 1})
	for _, k := range []string{"_id", "session_id", "account_id", "host"} {
		if hasKey(lines[0], k) {
			t.Errorf("transcript line still has %s", k)
		}
	}
	eq(t, "transcript line keeps its line", lines[0].Line()["type"], "system")

	agents := docList(t, doc, "subagent_lines")
	eq(t, "subagent_lines sorted by subagent_id then seq", strs(agents, "subagent_id"), []string{"agent-x", "agent-x", "agent-y"})
	eq(t, "subagent seq order", ints(agents, "seq"), []int{0, 1, 0})
	for _, k := range []string{"_id", "session_id", "account_id", "host"} {
		if hasKey(agents[0], k) {
			t.Errorf("subagent line still has %s", k)
		}
	}

	blobs := docList(t, doc, "blobs")
	eq(t, "blobs sorted by blob_type then name", strs(blobs, "name"), []string{"h", "out.txt"})
	for _, k := range []string{"_id", "session_id", "account_id", "content"} {
		if hasKey(blobs[0], k) {
			t.Errorf("blob still has %s", k)
		}
	}
	eq(t, "blob keeps its type", blobs[1]["blob_type"], "tool-result")

	events := docList(t, doc, "hook_events")
	eq(t, "hook_events sorted by created_at", strs(events, "hook_event_name"), []string{"PreToolUse", "Stop"})
	for _, k := range []string{"_id", "session_id", "account_id"} {
		if hasKey(events[0], k) {
			t.Errorf("hook event still has %s", k)
		}
	}
}

func fullSessionEmpty(t *testing.T, s session.Store) {
	must(t, s.UpsertSession(ctx, session.Session{SessionID: "s1", AccountID: "acc", LastSeen: t0}))
	doc := ok(s.FullSession(ctx, "acc", "s1"))
	for _, k := range []string{"transcript_lines", "subagent_lines", "blobs", "hook_events"} {
		if got := docList(t, doc, k); len(got) != 0 {
			t.Errorf("%s = %#v, want empty", k, got)
		}
	}
}

func fullSessionNotFound(t *testing.T, s session.Store) {
	must(t, s.UpsertSession(ctx, session.Session{SessionID: "s1", AccountID: "acc", LastSeen: t0}))
	if _, err := s.FullSession(ctx, "other", "s1"); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("foreign account: err = %v, want ErrNotFound", err)
	}
	if _, err := s.FullSession(ctx, "acc", "missing"); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("missing session: err = %v, want ErrNotFound", err)
	}
}

func unenrichedSelection(t *testing.T, s session.Store) {
	must(t, s.UpsertTranscriptLines(ctx, []session.TranscriptLine{tl("s1", 0, "a", "acc"), tl("s1", 1, "b", "acc"), tl("s1", 2, "c", "acc")}))
	pending := ok(s.Unenriched(ctx, "transcript_lines", "hook-linker", 10))
	eq(t, "all pending", len(pending), 3)
	for _, d := range pending {
		if !hasKey(d, "_id") {
			t.Fatalf("Unenriched dropped _id: %#v", d)
		}
	}
	bySeq := map[int]any{}
	for _, d := range pending {
		bySeq[num(d["seq"])] = d["_id"]
	}

	must(t, s.SetEnrichment(ctx, "transcript_lines", bySeq[0], "hook-linker", session.Doc{"ok": true}))
	must(t, s.SetEnrichmentFailure(ctx, "transcript_lines", bySeq[1], "hook-linker", "boom", t0))
	must(t, s.SetEnrichmentSkipped(ctx, "transcript_lines", bySeq[2], "hook-linker"))

	eq(t, "enriched, failed and skipped documents leave the queue", len(ok(s.Unenriched(ctx, "transcript_lines", "hook-linker", 10))), 0)

	must(t, s.UpsertTranscriptLines(ctx, []session.TranscriptLine{tl("s1", 3, "d", "acc")}))
	left := ok(s.Unenriched(ctx, "transcript_lines", "hook-linker", 10))
	eq(t, "only the untouched document is pending", ints(left, "seq"), []int{3})
	eq(t, "a different enricher still sees all", len(ok(s.Unenriched(ctx, "transcript_lines", "other", 10))), 4)
	eq(t, "limit", len(ok(s.Unenriched(ctx, "transcript_lines", "other", 2))), 2)
	eq(t, "other collection is independent", len(ok(s.Unenriched(ctx, "hook_events", "hook-linker", 10))), 0)
}

func setEnrichment(t *testing.T, s session.Store) {
	must(t, s.UpsertTranscriptLines(ctx, []session.TranscriptLine{tl("s1", 0, "a", "acc"), tl("s1", 1, "b", "acc")}))
	pending := ok(s.Unenriched(ctx, "transcript_lines", "e", 10))
	id0, seq0 := pending[0]["_id"], num(pending[0]["seq"])

	result := session.Doc{"tool_use_ids": []string{"t1"}}
	must(t, s.SetEnrichment(ctx, "transcript_lines", id0, "e", result))
	result["tool_use_ids"] = "mutated"
	must(t, s.SetEnrichment(ctx, "transcript_lines", "no-such-id", "e", session.Doc{}))

	lines := ok(s.TranscriptLines(ctx, session.LineQuery{AccountID: "acc", SessionID: "s1"}))
	for _, l := range lines {
		if num(l["seq"]) == seq0 {
			eq(t, "enrichments.e.tool_use_ids", strList(l.Map("enrichments").Map("e")["tool_use_ids"]), []string{"t1"})
		} else if hasKey(l, "enrichments") {
			t.Errorf("enrichment leaked to another document: %#v", l)
		}
	}
}

func setEnrichmentFailure(t *testing.T, s session.Store) {
	must(t, s.UpsertTranscriptLines(ctx, []session.TranscriptLine{tl("s1", 0, "a", "acc")}))
	id := ok(s.Unenriched(ctx, "transcript_lines", "e", 10))[0]["_id"]
	must(t, s.SetEnrichmentFailure(ctx, "transcript_lines", id, "e", "boom", t0))

	l := ok(s.TranscriptLines(ctx, session.LineQuery{AccountID: "acc", SessionID: "s1"}))[0]
	failed := l.Map("enrichments").Map("e_failed")
	eq(t, "message", failed["message"], "boom")
	if !timeOf(t, failed, "at").Equal(t0) {
		t.Errorf("at = %v, want %v", failed["at"], t0)
	}
	if hasKey(l.Map("enrichments"), "e") {
		t.Errorf("failure also wrote the result key")
	}
}

func setEnrichmentSkipped(t *testing.T, s session.Store) {
	must(t, s.UpsertTranscriptLines(ctx, []session.TranscriptLine{tl("s1", 0, "a", "acc"), tl("s1", 1, "b", "acc")}))
	pending := ok(s.Unenriched(ctx, "transcript_lines", "e", 10))
	var skippedID any
	for _, d := range pending {
		if num(d["seq"]) == 0 {
			skippedID = d["_id"]
		}
	}
	must(t, s.SetEnrichmentSkipped(ctx, "transcript_lines", skippedID, "e"))

	eq(t, "skipped document leaves the queue", ints(ok(s.Unenriched(ctx, "transcript_lines", "e", 10)), "seq"), []int{1})
	eq(t, "a different enricher still sees both", len(ok(s.Unenriched(ctx, "transcript_lines", "other", 10))), 2)
	for _, l := range ok(s.TranscriptLines(ctx, session.LineQuery{AccountID: "acc", SessionID: "s1"})) {
		if num(l["seq"]) != 0 {
			continue
		}
		eq(t, "enrichments.e_skipped", l.Map("enrichments")["e_skipped"], true)
		if hasKey(l.Map("enrichments"), "e") {
			t.Errorf("skip also wrote the result key")
		}
	}
	must(t, s.SetEnrichmentSkipped(ctx, "transcript_lines", "no-such-id", "e"))
}

func requeueSkipped(t *testing.T, s session.Store) {
	must(t, s.UpsertTranscriptLines(ctx, []session.TranscriptLine{tl("s1", 0, "a", "acc"), tl("s1", 1, "b", "acc"), tl("s2", 0, "c", "other")}))
	for _, d := range ok(s.Unenriched(ctx, "transcript_lines", "e", 10)) {
		must(t, s.SetEnrichmentSkipped(ctx, "transcript_lines", d["_id"], "e"))
		must(t, s.SetEnrichmentSkipped(ctx, "transcript_lines", d["_id"], "f"))
	}
	eq(t, "all skipped", len(ok(s.Unenriched(ctx, "transcript_lines", "e", 10))), 0)

	eq(t, "requeued", ok(s.RequeueSkipped(ctx, "transcript_lines", "acc", "e")), int64(2))
	back := ok(s.Unenriched(ctx, "transcript_lines", "e", 10))
	eq(t, "the account's lines are queued again", len(back), 2)
	for _, d := range back {
		eq(t, "account", d["account_id"], "acc")
	}
	eq(t, "another enricher's marks stay", len(ok(s.Unenriched(ctx, "transcript_lines", "f", 10))), 0)
	eq(t, "nothing left to requeue", ok(s.RequeueSkipped(ctx, "transcript_lines", "acc", "e")), int64(0))
	if _, err := s.RequeueSkipped(ctx, "transcript_lines", "", "e"); !errors.Is(err, session.ErrNoAccount) {
		t.Errorf("empty account: err = %v, want ErrNoAccount", err)
	}
}

func hookEventIDsByToolUse(t *testing.T, s session.Store) {
	for _, ev := range []session.Doc{
		{"session_id": "s1", "tool_use_id": "tu1"},
		{"session_id": "s1", "tool_use_id": "tu2"},
		{"session_id": "s1", "tool_use_id": "tu3"},
		{"session_id": "s2", "tool_use_id": "tu1"},
		{"session_id": "s1"},
	} {
		must(t, s.InsertHookEvent(ctx, ev))
	}
	want := map[string]bool{}
	for _, d := range ok(s.Unenriched(ctx, "hook_events", "probe", 10)) {
		if d["session_id"] == "s1" && (d["tool_use_id"] == "tu1" || d["tool_use_id"] == "tu2") {
			want[idString(d["_id"])] = true
		}
	}

	got := ok(s.HookEventIDsByToolUse(ctx, "s1", []string{"tu1", "tu2", "absent"}))
	gotSet := map[string]bool{}
	for _, id := range got {
		gotSet[id] = true
	}
	eq(t, "ids of the session's matching events", gotSet, want)
	eq(t, "no duplicates", len(got), len(want))
	eq(t, "empty list", len(ok(s.HookEventIDsByToolUse(ctx, "s1", nil))), 0)
}

func hookEventIDsAcrossSessions(t *testing.T, s session.Store) {
	for _, ev := range []session.Doc{
		{"session_id": "s1", "tool_use_id": "tu1"},
		{"session_id": "s2", "tool_use_id": "tu1"},
		{"session_id": "s2", "tool_use_id": "tu9"},
	} {
		must(t, s.InsertHookEvent(ctx, ev))
	}
	want := map[string]bool{}
	for _, d := range ok(s.Unenriched(ctx, "hook_events", "probe", 10)) {
		if d["tool_use_id"] == "tu1" {
			want[idString(d["_id"])] = true
		}
	}
	got := ok(s.HookEventIDsByToolUse(ctx, "", []string{"tu1"}))
	gotSet := map[string]bool{}
	for _, id := range got {
		gotSet[id] = true
	}
	eq(t, "ids of matching events in every session", gotSet, want)
	eq(t, "count", len(got), 2)
}

func returnedDocsAreCopies(t *testing.T, s session.Store) {
	must(t, s.UpsertSession(ctx, session.Session{SessionID: "s1", AccountID: "acc", Cwd: "/a", LastSeen: t0}))
	must(t, s.UpsertTranscriptLines(ctx, []session.TranscriptLine{tl("s1", 0, "a", "acc")}))
	must(t, s.UpsertSubagentLine(ctx, sub("s1", "ag", 0, "acc")))
	must(t, s.UpsertBlob(ctx, blob("s1", "tool-result", "n", "c", "acc")))
	must(t, s.InsertHookEvent(ctx, bash("s1", "acc", "git x", t0)))

	got := ok(s.GetSession(ctx, "acc", "s1"))
	got["cwd"] = "mutated"
	eq(t, "session unchanged", ok(s.GetSession(ctx, "acc", "s1"))["cwd"], "/a")

	lines := ok(s.TranscriptLines(ctx, session.LineQuery{AccountID: "acc", SessionID: "s1"}))
	lines[0].Line()["type"] = "mutated"
	again := ok(s.TranscriptLines(ctx, session.LineQuery{AccountID: "acc", SessionID: "s1"}))
	eq(t, "nested line unchanged", again[0].Line()["type"], "a")

	full := ok(s.FullSession(ctx, "acc", "s1"))
	docList(t, full, "transcript_lines")[0].Line()["type"] = "mutated"
	docList(t, full, "subagent_lines")[0].Line()["type"] = "mutated"
	docList(t, full, "blobs")[0]["name"] = "mutated"
	docList(t, full, "hook_events")[0].ToolInput()["command"] = "mutated"
	full["cwd"] = "mutated"
	again2 := ok(s.FullSession(ctx, "acc", "s1"))
	eq(t, "full session cwd", again2["cwd"], "/a")
	eq(t, "full session transcript line", docList(t, again2, "transcript_lines")[0].Line()["type"], "a")
	eq(t, "full session subagent line", docList(t, again2, "subagent_lines")[0].Line()["type"], "user")
	eq(t, "full session blob name", docList(t, again2, "blobs")[0]["name"], "n")
	eq(t, "full session hook event", docList(t, again2, "hook_events")[0].ToolInput()["command"], "git x")

	ok(s.SubagentLines(ctx, "acc", "s1", "ag"))[0].Line()["type"] = "mutated"
	eq(t, "subagent lines are copies", ok(s.SubagentLines(ctx, "acc", "s1", "ag"))[0].Line()["type"], "user")
	ok(s.Blobs(ctx, "acc", "s1"))[0]["content"] = "mutated"
	eq(t, "blobs are copies", ok(s.Blobs(ctx, "acc", "s1"))[0]["content"], "c")
	ok(s.BashEvents(ctx, session.CommandQuery{AccountID: "acc", PatternRe: "git", Limit: 5}))[0].ToolInput()["command"] = "mutated"
	eq(t, "bash events are copies", ok(s.BashEvents(ctx, session.CommandQuery{AccountID: "acc", PatternRe: "git", Limit: 5}))[0].ToolInput()["command"], "git x")
	ok(s.FindSessions(ctx, session.SessionQuery{AccountID: "acc", Limit: 5}))[0]["cwd"] = "mutated"
	eq(t, "found sessions are copies", ok(s.FindSessions(ctx, session.SessionQuery{AccountID: "acc", Limit: 5}))[0]["cwd"], "/a")
	ok(s.Unenriched(ctx, "hook_events", "e", 5))[0].ToolInput()["command"] = "mutated"
	eq(t, "unenriched documents are copies", ok(s.Unenriched(ctx, "hook_events", "e", 5))[0].ToolInput()["command"], "git x")
}

func findSessionsLimitCap(t *testing.T, s session.Store) {
	for i := 0; i < 501; i++ {
		must(t, s.UpsertSession(ctx, session.Session{SessionID: fmt.Sprint("s", i), AccountID: "acc", LastSeen: t0.Add(time.Duration(i) * time.Second)}))
	}
	for _, limit := range []int{0, -1, 600} {
		got := ok(s.FindSessions(ctx, session.SessionQuery{AccountID: "acc", Limit: limit}))
		eq(t, fmt.Sprint("count with limit ", limit), len(got), 500)
		eq(t, fmt.Sprint("newest first with limit ", limit), got[0]["session_id"], "s500")
	}
	eq(t, "limit below the cap", len(ok(s.FindSessions(ctx, session.SessionQuery{AccountID: "acc", Limit: 3}))), 3)
}

func nonPositiveLimitIsUnlimited(t *testing.T, s session.Store) {
	must(t, s.UpsertTranscriptLines(ctx, []session.TranscriptLine{tl("s1", 0, "a", "acc"), tl("s1", 1, "b", "acc"), tl("s1", 2, "c", "acc")}))
	for i := 0; i < 3; i++ {
		must(t, s.InsertHookEvent(ctx, bash("s1", "acc", "git x", t0.Add(time.Duration(i)*time.Minute))))
	}
	for _, limit := range []int{0, -1} {
		name := fmt.Sprint(" with limit ", limit)
		eq(t, "transcript lines"+name, len(ok(s.TranscriptLines(ctx, session.LineQuery{AccountID: "acc", SessionID: "s1", Limit: limit}))), 3)
		eq(t, "transcript lines after offset"+name, len(ok(s.TranscriptLines(ctx, session.LineQuery{AccountID: "acc", SessionID: "s1", Offset: 1, Limit: limit}))), 2)
		eq(t, "bash events"+name, len(ok(s.BashEvents(ctx, session.CommandQuery{AccountID: "acc", PatternRe: "git", Limit: limit}))), 3)
		eq(t, "unenriched lines"+name, len(ok(s.Unenriched(ctx, "transcript_lines", "e", limit))), 3)
		eq(t, "unenriched hook events"+name, len(ok(s.Unenriched(ctx, "hook_events", "e", limit))), 3)
	}
}

// storedInputsAreCopies checks that mutating a value after handing it to the
// store does not change what the store holds.
func storedInputsAreCopies(t *testing.T, s session.Store) {
	ip := "10.0.0.1"
	host := &session.HostInfo{Hostname: "h", IP: &ip}
	must(t, s.UpsertSession(ctx, session.Session{SessionID: "s1", AccountID: "acc", LastSeen: t0, Host: host}))

	line := map[string]any{"type": "a", "nested": map[string]any{"k": "v"}}
	must(t, s.UpsertTranscriptLines(ctx, []session.TranscriptLine{{SessionID: "s1", Seq: 0, Line: line, AccountID: "acc", Host: host}}))
	subLine := map[string]any{"type": "a"}
	must(t, s.UpsertSubagentLine(ctx, session.SubagentLine{SessionID: "s1", SubagentID: "ag", Seq: 0, Line: subLine, AccountID: "acc"}))
	host.Hostname = "mutated"
	ip = "mutated"
	line["type"] = "mutated"
	line["nested"].(map[string]any)["k"] = "mutated"
	subLine["type"] = "mutated"

	got := ok(s.GetSession(ctx, "acc", "s1"))
	eq(t, "session host.hostname", got.Map("host")["hostname"], "h")
	eq(t, "session host.ip", got.Map("host")["ip"], "10.0.0.1")
	stored := ok(s.TranscriptLines(ctx, session.LineQuery{AccountID: "acc", SessionID: "s1"}))[0]
	eq(t, "transcript line type", stored.Line()["type"], "a")
	eq(t, "transcript line nested", stored.Line().Map("nested")["k"], "v")
	eq(t, "transcript host.hostname", stored.Map("host")["hostname"], "h")
	eq(t, "subagent line type", ok(s.SubagentLines(ctx, "acc", "s1", "ag"))[0].Line()["type"], "a")
}

// memoryEvent is a MemorySaved event as the memory ingest posts it, once the
// daemon has stamped account_id and created_at.
func memoryEvent(sid, acc, kind, text string, at time.Time) session.Doc {
	return session.Doc{
		"session_id": sid, "account_id": acc, "hook_event_name": session.MemoryEventName, "cwd": "/p/" + sid, "created_at": at,
		"memory": session.Doc{"kind": kind, "file": "now.md", "text": text, "hash": "h-" + acc + "-" + text},
	}
}

func memoryEvents(t *testing.T, s session.Store) {
	for _, ev := range []session.Doc{
		memoryEvent("s1", "acc", "entry", "## 09:15 | main\nFixed the Daemon restart", t0),
		memoryEvent("s1", "acc", "handoff", "Next: wire the daemon listener", t0.Add(time.Minute)),
		memoryEvent("s2", "acc", "recent", "week of DAEMON work", t0.Add(2*time.Minute)),
		memoryEvent("s3", "other", "entry", "daemon elsewhere", t0.Add(3*time.Minute)),
		{"session_id": "s1", "account_id": "acc", "hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": session.Doc{"command": "daemon"}, "created_at": t0},
		{"session_id": "s1", "account_id": "acc", "hook_event_name": session.MemoryEventName, "memory": session.Doc{"kind": "entry", "text": 7, "hash": "h-odd"}, "created_at": t0},
	} {
		must(t, s.InsertHookEvent(ctx, ev))
	}
	texts := func(q session.MemoryQuery) []string {
		var out []string
		for _, d := range ok(s.MemoryEvents(ctx, q)) {
			out = append(out, d.Map("memory")["text"].(string))
		}
		return out
	}
	eq(t, "regex is case-insensitive, newest first, own account and memory events only",
		texts(session.MemoryQuery{AccountID: "acc", PatternRe: "daemon", Limit: 10}),
		[]string{"week of DAEMON work", "Next: wire the daemon listener", "## 09:15 | main\nFixed the Daemon restart"})
	eq(t, "kind filter", texts(session.MemoryQuery{AccountID: "acc", PatternRe: "daemon", Kinds: []string{"handoff", "recent"}, Limit: 10}),
		[]string{"week of DAEMON work", "Next: wire the daemon listener"})
	eq(t, "session scoping", texts(session.MemoryQuery{AccountID: "acc", PatternRe: "daemon", SessionIDs: []string{"s1"}, Limit: 10}),
		[]string{"Next: wire the daemon listener", "## 09:15 | main\nFixed the Daemon restart"})
	eq(t, "empty pattern matches every text", len(texts(session.MemoryQuery{AccountID: "acc", Limit: 10})), 3)
	eq(t, "limit", texts(session.MemoryQuery{AccountID: "acc", Limit: 1}), []string{"week of DAEMON work"})
	eq(t, "no match", texts(session.MemoryQuery{AccountID: "acc", PatternRe: "zzz", Limit: 10}), []string(nil))

	got := ok(s.MemoryEvents(ctx, session.MemoryQuery{AccountID: "acc", PatternRe: "wire", Limit: 10}))
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	eq(t, "projection fields", sortedKeys(got[0]), []string{"created_at", "cwd", "memory", "session_id"})
	eq(t, "cwd", got[0]["cwd"], "/p/s1")
	eq(t, "memory.kind", got[0].Map("memory")["kind"], "handoff")
	if !timeOf(t, got[0], "created_at").Equal(t0.Add(time.Minute)) {
		t.Errorf("created_at = %v", got[0]["created_at"])
	}
}

// limitSample is a LimitsSampled event as `hook status` posts it, once the
// daemon has stamped account_id and created_at.
func limitSample(sid, acc string, used float64, at time.Time) session.Doc {
	return session.Doc{
		"session_id": sid, "account_id": acc, "hook_event_name": session.LimitsEventName, "cwd": "/p/" + sid,
		"created_at": at, "sampled_at": at.UTC().Format(time.RFC3339Nano),
		"model":          session.Doc{"id": "claude-fable-5-1", "display_name": "Fable 5.1"},
		"version":        "2.1.0",
		"context_window": session.Doc{"used_percentage": used, "context_window_size": 1000000},
		"rate_limits":    session.Doc{"five_hour": session.Doc{"used_percentage": 12.0, "resets_at": 1760400000}},
		"cost":           session.Doc{"total_cost_usd": 0.5},
	}
}

func limitSamples(t *testing.T, s session.Store) {
	for _, ev := range []session.Doc{
		limitSample("s1", "acc", 10, t0),
		limitSample("s1", "acc", 20, t0.Add(time.Minute)),
		limitSample("s2", "acc", 30, t0.Add(2*time.Minute)),
		limitSample("s3", "other", 40, t0.Add(3*time.Minute)),
		{"session_id": "s1", "account_id": "acc", "hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": session.Doc{"command": "x"}, "created_at": t0},
	} {
		must(t, s.InsertHookEvent(ctx, ev))
	}
	used := func(q session.LimitQuery) []float64 {
		var out []float64
		for _, d := range ok(s.LimitSamples(ctx, q)) {
			switch v := d.Map("context_window")["used_percentage"].(type) {
			case float64:
				out = append(out, v)
			case int:
				out = append(out, float64(v))
			case int32:
				out = append(out, float64(v))
			case int64:
				out = append(out, float64(v))
			default:
				t.Fatalf("used_percentage is %T", v)
			}
		}
		return out
	}
	eq(t, "own account and usage events only, newest first", used(session.LimitQuery{AccountID: "acc", Limit: 10}), []float64{30, 20, 10})
	eq(t, "session scoping", used(session.LimitQuery{AccountID: "acc", SessionIDs: []string{"s1"}, Limit: 10}), []float64{20, 10})
	eq(t, "since", used(session.LimitQuery{AccountID: "acc", Since: t0.Add(time.Minute), Limit: 10}), []float64{30, 20})
	eq(t, "limit", used(session.LimitQuery{AccountID: "acc", Limit: 1}), []float64{30})
	eq(t, "unknown session", len(used(session.LimitQuery{AccountID: "acc", SessionIDs: []string{"zzz"}, Limit: 10})), 0)

	got := ok(s.LimitSamples(ctx, session.LimitQuery{AccountID: "acc", SessionIDs: []string{"s2"}, Limit: 10}))
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	eq(t, "projection fields", sortedKeys(got[0]), []string{"context_window", "cost", "created_at", "cwd", "model", "rate_limits", "sampled_at", "session_id", "version"})
	eq(t, "cwd", got[0]["cwd"], "/p/s2")
	eq(t, "model id", got[0].Map("model")["id"], "claude-fable-5-1")
	if !timeOf(t, got[0], "created_at").Equal(t0.Add(2 * time.Minute)) {
		t.Errorf("created_at = %v", got[0]["created_at"])
	}
}

func memoryEventsInvalidRegex(t *testing.T, s session.Store) {
	if _, err := s.MemoryEvents(ctx, session.MemoryQuery{AccountID: "acc", PatternRe: "(", Limit: 10}); err == nil {
		t.Error("invalid regex accepted")
	}
}

func memoryEventDedup(t *testing.T, s session.Store) {
	first := memoryEvent("s1", "acc", "entry", "same text", t0)
	must(t, s.InsertHookEvent(ctx, first))
	again := memoryEvent("s2", "acc", "day", "same text", t0.Add(time.Minute)) // same hash: moved into a today file
	must(t, s.InsertHookEvent(ctx, again))
	must(t, s.InsertHookEvent(ctx, memoryEvent("s1", "other", "entry", "same text", t0)))
	plain := session.Doc{"session_id": "s1", "account_id": "acc", "hook_event_name": "Stop"}
	must(t, s.InsertHookEvent(ctx, plain))
	must(t, s.InsertHookEvent(ctx, plain))

	mine := ok(s.MemoryEvents(ctx, session.MemoryQuery{AccountID: "acc", Limit: 10}))
	eq(t, "one record per account and hash", len(mine), 1)
	eq(t, "the first copy is kept", mine[0]["session_id"], "s1")
	eq(t, "another account keeps its own copy", len(ok(s.MemoryEvents(ctx, session.MemoryQuery{AccountID: "other", Limit: 10}))), 1)
	all := ok(s.Unenriched(ctx, "hook_events", "probe", 10))
	eq(t, "events without a hash are never deduplicated", len(all), 4)
}

func deleteAccount(t *testing.T, s session.Store) {
	for _, acc := range []string{"a", "b"} {
		must(t, s.UpsertSession(ctx, session.Session{SessionID: "s-" + acc, AccountID: acc, ProjectPath: "/p", LastSeen: t0}))
		must(t, s.InsertHookEvent(ctx, bash("s-"+acc, acc, "make "+acc, t0)))
		must(t, s.UpsertTranscriptLines(ctx, []session.TranscriptLine{tl("s-"+acc, 1, "user", acc), tl("s-"+acc, 2, "assistant", acc)}))
		must(t, s.UpsertSubagentLine(ctx, sub("s-"+acc, "agent", 1, acc)))
		must(t, s.UpsertBlob(ctx, blob("s-"+acc, "tool-result", "r", "x", acc)))
	}
	if _, err := s.DeleteAccount(ctx, ""); !errors.Is(err, session.ErrNoAccount) {
		t.Fatalf("DeleteAccount(\"\") = %v, want ErrNoAccount", err)
	}
	n := ok(s.DeleteAccount(ctx, "a"))
	if n < 6 {
		t.Errorf("deleted %d documents, want at least the 6 of account a", n)
	}
	if _, err := s.GetSession(ctx, "a", "s-a"); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("a's session after delete: %v", err)
	}
	eq(t, "a's transcript lines", ok(s.CountTranscriptLines(ctx, "a", "s-a")), 0)
	eq(t, "a's blobs", len(ok(s.Blobs(ctx, "a", "s-a"))), 0)
	eq(t, "a's subagents", len(ok(s.SubagentIDs(ctx, "a", "s-a"))), 0)
	eq(t, "a's bash events", len(ok(s.BashEvents(ctx, session.CommandQuery{AccountID: "a", PatternRe: "."}))), 0)
	// Account b is untouched.
	if _, err := s.GetSession(ctx, "b", "s-b"); err != nil {
		t.Errorf("b's session after a's delete: %v", err)
	}
	eq(t, "b's transcript lines", ok(s.CountTranscriptLines(ctx, "b", "s-b")), 2)
	eq(t, "b's blobs", len(ok(s.Blobs(ctx, "b", "s-b"))), 1)
	eq(t, "b's subagents", len(ok(s.SubagentIDs(ctx, "b", "s-b"))), 1)
	eq(t, "b's bash events", len(ok(s.BashEvents(ctx, session.CommandQuery{AccountID: "b", PatternRe: "."}))), 1)
	eq(t, "a second delete", ok(s.DeleteAccount(ctx, "a")), int64(0))
}

func fileEvents(t *testing.T, s session.Store) {
	tracked := func(sid, acc, tool, path string, at time.Time) session.Doc {
		return session.Doc{"session_id": sid, "account_id": acc, "hook_event_name": "PreToolUse", "tool_name": tool, "tool_use_id": "tu-" + sid + tool,
			"tool_input": session.Doc{"file_path": path}, "created_at": at,
			"enrichments": session.Doc{"file-tracker": session.Doc{"file_path": path, "operation": strings.ToLower(tool)}}}
	}
	for _, ev := range []session.Doc{
		tracked("s1", "acc", "Read", "/repo/main.go", t0),
		tracked("s1", "acc", "Edit", "/repo/main.go", t0.Add(time.Minute)),
		{"session_id": "s1", "account_id": "acc", "hook_event_name": "PostToolUse", "tool_name": "Edit", "tool_use_id": "tu-s1Edit",
			"tool_input": session.Doc{"file_path": "/repo/main.go", "old_string": "a", "new_string": "b\nc"}, "created_at": t0.Add(2 * time.Minute),
			"enrichments": session.Doc{"edit-diff-stats": session.Doc{"lines_added": 2, "lines_removed": 1, "net_change": 1}}},
		tracked("s2", "acc", "Write", "/repo/README.md", t0.Add(3*time.Minute)),
		tracked("s3", "other", "Read", "/repo/main.go", t0.Add(4*time.Minute)),
		// A PostToolUse without diff stats and a PreToolUse without a tracked path are not file events.
		{"session_id": "s1", "account_id": "acc", "hook_event_name": "PostToolUse", "tool_name": "Read", "tool_input": session.Doc{"file_path": "/repo/main.go"}, "created_at": t0},
		{"session_id": "s1", "account_id": "acc", "hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": session.Doc{"command": "cat main.go"}, "created_at": t0},
	} {
		must(t, s.InsertHookEvent(ctx, ev))
	}
	kinds := func(q session.FileQuery) []string {
		var out []string
		for _, d := range ok(s.FileEvents(ctx, q)) {
			ev, _ := d.String("hook_event_name")
			tool, _ := d.String("tool_name")
			out = append(out, ev+":"+tool)
		}
		return out
	}
	eq(t, "path regex, newest first, own account", kinds(session.FileQuery{AccountID: "acc", PathRe: "MAIN\\.go$", Limit: 10}),
		[]string{"PostToolUse:Edit", "PreToolUse:Edit", "PreToolUse:Read"})
	eq(t, "every file", len(ok(s.FileEvents(ctx, session.FileQuery{AccountID: "acc", Limit: 10}))), 4)
	eq(t, "session scoping", kinds(session.FileQuery{AccountID: "acc", SessionIDs: []string{"s2"}, Limit: 10}), []string{"PreToolUse:Write"})
	eq(t, "limit", len(ok(s.FileEvents(ctx, session.FileQuery{AccountID: "acc", Limit: 1}))), 1)
	got := ok(s.FileEvents(ctx, session.FileQuery{AccountID: "acc", PathRe: "main", Limit: 1}))[0]
	eq(t, "diff stats", num(got.Map("enrichments").Map("edit-diff-stats")["lines_added"]), 2)
	eq(t, "file path", got.ToolInput()["file_path"], "/repo/main.go")
	eq(t, "tool use id", got["tool_use_id"], "tu-s1Edit")
	if _, err := s.FileEvents(ctx, session.FileQuery{AccountID: "acc", PathRe: "(", Limit: 1}); err == nil {
		t.Error("FileEvents accepted an invalid regex")
	}
}

func errorLines(t *testing.T, s session.Store) {
	flag := func(sid string, seq int, acc string, errType any) {
		must(t, s.UpsertTranscriptLines(ctx, []session.TranscriptLine{tl(sid, seq, "user", acc)}))
		var id any
		for _, d := range ok(s.Unenriched(ctx, "transcript_lines", "error-flag", 0)) {
			if d["session_id"] == sid && num(d["seq"]) == seq {
				id = d["_id"]
			}
		}
		result := session.Doc{"is_error": errType != nil, "error_type": errType}
		must(t, s.SetEnrichment(ctx, "transcript_lines", id, "error-flag", result))
		time.Sleep(5 * time.Millisecond) // created_at orders the lines
	}
	flag("s1", 0, "acc", "tool_failure")
	flag("s1", 1, "acc", nil)
	flag("s1", 2, "acc", "error_text")
	flag("s2", 0, "acc", "hook_error")
	flag("s3", 0, "other", "tool_failure")
	keys := func(q session.ErrorQuery) []string {
		var out []string
		for _, d := range ok(s.ErrorLines(ctx, q)) {
			sid, _ := d.String("session_id")
			typ, _ := d.Map("enrichments").Map("error-flag").String("error_type")
			out = append(out, fmt.Sprintf("%s/%d/%s", sid, num(d["seq"]), typ))
		}
		return out
	}
	eq(t, "errors only, newest first, own account", keys(session.ErrorQuery{AccountID: "acc", Limit: 10}),
		[]string{"s2/0/hook_error", "s1/2/error_text", "s1/0/tool_failure"})
	eq(t, "types", keys(session.ErrorQuery{AccountID: "acc", Types: []string{"tool_failure", "hook_error"}, Limit: 10}),
		[]string{"s2/0/hook_error", "s1/0/tool_failure"})
	eq(t, "session scoping", keys(session.ErrorQuery{AccountID: "acc", SessionIDs: []string{"s1"}, Limit: 1}), []string{"s1/2/error_text"})
	got := ok(s.ErrorLines(ctx, session.ErrorQuery{AccountID: "acc", Limit: 1}))[0]
	eq(t, "projection", sortedKeys(got), []string{"created_at", "enrichments", "line", "seq", "session_id"})
}

func activities(t *testing.T, s session.Store) {
	label := func(sid string, seq int, acc, kind string, enrich bool) {
		line := tl(sid, seq, "assistant", acc)
		line.Line = map[string]any{"type": "assistant", "timestamp": fmt.Sprintf("2026-10-14T10:00:%02dZ", seq)}
		must(t, s.UpsertTranscriptLines(ctx, []session.TranscriptLine{line}))
		if !enrich {
			return
		}
		var id any
		for _, d := range ok(s.Unenriched(ctx, "transcript_lines", "activity-kind", 0)) {
			if d["session_id"] == sid && num(d["seq"]) == seq {
				id = d["_id"]
			}
		}
		result := session.Doc{"kind": kind, "label": kind, "detail": "d-" + kind, "tool_name": "", "tool_use_id": ""}
		must(t, s.SetEnrichment(ctx, "transcript_lines", id, "activity-kind", result))
		time.Sleep(5 * time.Millisecond) // created_at orders the lines
	}
	label("s1", 0, "acc", "user", true)
	label("s1", 1, "acc", "bash", true)
	label("s1", 2, "acc", "edit", false)
	label("s2", 0, "acc", "response", true)
	label("s3", 0, "other", "user", true)
	keys := func(q session.ActivityQuery) []string {
		var out []string
		for _, d := range ok(s.Activities(ctx, q)) {
			sid, _ := d.String("session_id")
			kind, _ := d.Map("enrichments").Map("activity-kind").String("kind")
			out = append(out, fmt.Sprintf("%s/%d/%s", sid, num(d["seq"]), kind))
		}
		return out
	}
	eq(t, "labelled lines only, newest first, own account", keys(session.ActivityQuery{AccountID: "acc", Limit: 10}),
		[]string{"s2/0/response", "s1/1/bash", "s1/0/user"})
	eq(t, "kinds", keys(session.ActivityQuery{AccountID: "acc", Kinds: []string{"user", "bash"}, Limit: 10}),
		[]string{"s1/1/bash", "s1/0/user"})
	eq(t, "session scoping and limit", keys(session.ActivityQuery{AccountID: "acc", SessionIDs: []string{"s1"}, Limit: 1}), []string{"s1/1/bash"})
	got := ok(s.Activities(ctx, session.ActivityQuery{AccountID: "acc", Limit: 1}))[0]
	eq(t, "projection", sortedKeys(got), []string{"created_at", "enrichments", "line", "seq", "session_id"})
	eq(t, "line keeps only its timestamp", sortedKeys(got.Map("line")), []string{"timestamp"})
	eq(t, "timestamp", got.Map("line")["timestamp"], "2026-10-14T10:00:00Z")
	eq(t, "detail", got.Map("enrichments").Map("activity-kind")["detail"], "d-response")
	since := timeOf(t, got, "created_at")
	eq(t, "since", keys(session.ActivityQuery{AccountID: "acc", Since: since, Limit: 10}), []string{"s2/0/response"})
}

func searchTranscripts(t *testing.T, s session.Store) {
	index := func(sid string, seq int, acc, text string) {
		must(t, s.UpsertTranscriptLines(ctx, []session.TranscriptLine{tl(sid, seq, "assistant", acc)}))
		var id any
		for _, d := range ok(s.Unenriched(ctx, "transcript_lines", "search-text", 0)) {
			if d["session_id"] == sid && num(d["seq"]) == seq {
				id = d["_id"]
			}
		}
		must(t, s.SetEnrichment(ctx, "transcript_lines", id, "search-text", text))
	}
	index("s1", 0, "acc", "the oauth migration broke the oauth login flow")
	index("s1", 1, "acc", "oauth tokens were rotated today for the whole team")
	index("s2", 0, "acc", "unrelated build output from the compiler run")
	index("s3", 0, "other", "oauth migration notes from another account")
	keys := func(q session.TextQuery) []string {
		var out []string
		for _, d := range ok(s.SearchTranscripts(ctx, q)) {
			sid, _ := d.String("session_id")
			out = append(out, fmt.Sprintf("%s/%d", sid, num(d["seq"])))
		}
		return out
	}
	eq(t, "best match first, own account only", keys(session.TextQuery{AccountID: "acc", Query: "oauth migration", Limit: 10}), []string{"s1/0", "s1/1"})
	eq(t, "case-insensitive", keys(session.TextQuery{AccountID: "acc", Query: "COMPILER", Limit: 10}), []string{"s2/0"})
	eq(t, "phrase", keys(session.TextQuery{AccountID: "acc", Query: `"oauth login"`, Limit: 10}), []string{"s1/0"})
	eq(t, "excluded word", keys(session.TextQuery{AccountID: "acc", Query: "oauth -tokens", Limit: 10}), []string{"s1/0"})
	eq(t, "session scoping", keys(session.TextQuery{AccountID: "acc", Query: "oauth", SessionIDs: []string{"s2"}, Limit: 10}), []string(nil))
	eq(t, "limit", len(ok(s.SearchTranscripts(ctx, session.TextQuery{AccountID: "acc", Query: "oauth", Limit: 1}))), 1)
	eq(t, "empty query", len(ok(s.SearchTranscripts(ctx, session.TextQuery{AccountID: "acc", Query: "  ", Limit: 10}))), 0)
	got := ok(s.SearchTranscripts(ctx, session.TextQuery{AccountID: "acc", Query: "rotated", Limit: 1}))[0]
	eq(t, "projection", sortedKeys(got), []string{"created_at", "enrichments", "score", "seq", "session_id"})
	if score, isNum := got["score"].(float64); !isNum || score <= 0 {
		t.Errorf("score = %#v, want a positive float64", got["score"])
	}
	eq(t, "indexed text", got.Map("enrichments")["search-text"], "oauth tokens were rotated today for the whole team")
}

// usageResult has the shape of the usage enricher's result; a struct, as the
// enricher stores it, so a store must keep it readable by field.
type usageResult struct {
	Model                    string `json:"model" bson:"model"`
	MessageID                string `json:"message_id" bson:"message_id"`
	Day                      string `json:"day" bson:"day"`
	InputTokens              int64  `json:"input_tokens" bson:"input_tokens"`
	CacheCreationInputTokens int64  `json:"cache_creation_input_tokens" bson:"cache_creation_input_tokens"`
	CacheCreation1hTokens    int64  `json:"cache_creation_1h_tokens" bson:"cache_creation_1h_tokens"`
	CacheReadInputTokens     int64  `json:"cache_read_input_tokens" bson:"cache_read_input_tokens"`
	OutputTokens             int64  `json:"output_tokens" bson:"output_tokens"`
	ContextTokens            int64  `json:"context_tokens" bson:"context_tokens"`
}

func usageTotals(t *testing.T, s session.Store) {
	record := func(sid string, seq int, acc string, r usageResult) {
		must(t, s.UpsertTranscriptLines(ctx, []session.TranscriptLine{tl(sid, seq, "assistant", acc)}))
		var id any
		for _, d := range ok(s.Unenriched(ctx, "transcript_lines", "usage", 0)) {
			if d["session_id"] == sid && num(d["seq"]) == seq {
				id = d["_id"]
			}
		}
		must(t, s.SetEnrichment(ctx, "transcript_lines", id, "usage", r))
	}
	const sonnet, haiku = "claude-sonnet-4-5", "claude-haiku-4-5"
	first := usageResult{Model: sonnet, MessageID: "m1", Day: "2026-10-01", InputTokens: 10, CacheCreationInputTokens: 100,
		CacheCreation1hTokens: 40, CacheReadInputTokens: 1000, OutputTokens: 5, ContextTokens: 1110}
	record("s1", 0, "acc", first)
	second := first // the next content block of the same message, with the final output count
	second.OutputTokens = 50
	record("s1", 1, "acc", second)
	record("s1", 2, "acc", usageResult{Model: haiku, MessageID: "m2", Day: "2026-10-02", InputTokens: 1, OutputTokens: 2, ContextTokens: 1})
	record("s2", 0, "acc", usageResult{Model: sonnet, MessageID: "m3", Day: "2026-10-02", InputTokens: 20, OutputTokens: 30, ContextTokens: 20})
	record("s3", 0, "other", usageResult{Model: sonnet, MessageID: "m4", Day: "2026-10-02", InputTokens: 999, OutputTokens: 999})
	must(t, s.UpsertTranscriptLines(ctx, []session.TranscriptLine{tl("s2", 1, "user", "acc")})) // no usage

	s1Sonnet := session.UsageTotal{Key: "s1", Model: sonnet, Messages: 1, InputTokens: 10, CacheCreationInputTokens: 100,
		CacheCreation1hTokens: 40, CacheReadInputTokens: 1000, OutputTokens: 50, PeakContextTokens: 1110}
	s1Haiku := session.UsageTotal{Key: "s1", Model: haiku, Messages: 1, InputTokens: 1, OutputTokens: 2, PeakContextTokens: 1}
	s2Sonnet := session.UsageTotal{Key: "s2", Model: sonnet, Messages: 1, InputTokens: 20, OutputTokens: 30, PeakContextTokens: 20}
	keyed := func(t session.UsageTotal, key string) session.UsageTotal { t.Key = key; return t }
	bySonnet := keyed(s1Sonnet, sonnet)
	bySonnet.Add(s2Sonnet)

	eq(t, "by session, each message once, own account", ok(s.UsageTotals(ctx, session.UsageQuery{AccountID: "acc"})),
		[]session.UsageTotal{s1Haiku, s1Sonnet, s2Sonnet})
	eq(t, "by model", ok(s.UsageTotals(ctx, session.UsageQuery{AccountID: "acc", GroupBy: session.UsageByModel})),
		[]session.UsageTotal{keyed(s1Haiku, haiku), bySonnet})
	eq(t, "by day", ok(s.UsageTotals(ctx, session.UsageQuery{AccountID: "acc", GroupBy: session.UsageByDay})),
		[]session.UsageTotal{keyed(s1Sonnet, "2026-10-01"), keyed(s1Haiku, "2026-10-02"), keyed(s2Sonnet, "2026-10-02")})
	eq(t, "since and until", ok(s.UsageTotals(ctx, session.UsageQuery{AccountID: "acc", GroupBy: session.UsageByModel, Since: "2026-10-02", Until: "2026-10-02"})),
		[]session.UsageTotal{keyed(s1Haiku, haiku), keyed(s2Sonnet, sonnet)})
	eq(t, "session scoping", ok(s.UsageTotals(ctx, session.UsageQuery{AccountID: "acc", SessionIDs: []string{"s2"}})),
		[]session.UsageTotal{s2Sonnet})
	eq(t, "nothing matches", len(ok(s.UsageTotals(ctx, session.UsageQuery{AccountID: "acc", Until: "2026-09-30"}))), 0)
}

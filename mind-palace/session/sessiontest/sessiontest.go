// Package sessiontest is the conformance suite every session.Store adapter
// must pass. It is the Go form of test/integration/mongo.test.ts plus the
// behaviours the use cases rely on, and it uses the session package API only.
package sessiontest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
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
		{"full session joins sub-collections and strips redundant fields", fullSessionShape},
		{"full session of a session with nothing else has empty arrays", fullSessionEmpty},
		{"full session is not found for a missing or foreign session", fullSessionNotFound},
		{"unenriched excludes documents that are enriched or failed", unenrichedSelection},
		{"set enrichment writes enrichments.<name> on the matching document", setEnrichment},
		{"set enrichment failure records message and time", setEnrichmentFailure},
		{"hook event ids by tool use are scoped to the session", hookEventIDsByToolUse},
		{"returned documents are copies", returnedDocsAreCopies},
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

func ints(docs []session.Doc, key string) []int {
	out := make([]int, len(docs))
	for i, d := range docs {
		out[i], _ = d[key].(int)
	}
	return out
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

func findSessionsOrdering(t *testing.T, s session.Store) {
	seedSessions(t, s)
	must(t, s.UpsertSession(ctx, session.Session{SessionID: "a", AccountID: "acc", TranscriptPath: "/t", LastSeen: t0.Add(1 * time.Hour)}))

	got := ok(s.FindSessions(ctx, session.SessionQuery{AccountID: "acc", Limit: 2}))
	eq(t, "newest first, limited", strs(got, "session_id"), []string{"b", "c"})

	keys := func(d session.Doc) []string {
		var ks []string
		for k := range d {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		return ks
	}
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
	delete(got, "_id")
	eq(t, "stored hook event (nothing stamped, input copied)", got,
		session.Doc{"session_id": "s1", "type": "PreToolUse", "tool_input": session.Doc{"command": "ls"}})
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
	eq(t, "seq stored", h[0]["seq"], 0)
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
	eq(t, "projection", got[0], session.Doc{"session_id": "s1", "tool_input": session.Doc{"command": "ls -la"}, "created_at": t0.Add(time.Minute)})
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
		bySeq[d["seq"].(int)] = d["_id"]
	}

	must(t, s.SetEnrichment(ctx, "transcript_lines", bySeq[0], "hook-linker", session.Doc{"ok": true}))
	must(t, s.SetEnrichmentFailure(ctx, "transcript_lines", bySeq[1], "hook-linker", "boom", t0))

	left := ok(s.Unenriched(ctx, "transcript_lines", "hook-linker", 10))
	eq(t, "only the untouched document is pending", ints(left, "seq"), []int{2})
	eq(t, "a different enricher still sees all", len(ok(s.Unenriched(ctx, "transcript_lines", "other", 10))), 3)
	eq(t, "limit", len(ok(s.Unenriched(ctx, "transcript_lines", "other", 2))), 2)
	eq(t, "other collection is independent", len(ok(s.Unenriched(ctx, "hook_events", "hook-linker", 10))), 0)
}

func setEnrichment(t *testing.T, s session.Store) {
	must(t, s.UpsertTranscriptLines(ctx, []session.TranscriptLine{tl("s1", 0, "a", "acc"), tl("s1", 1, "b", "acc")}))
	pending := ok(s.Unenriched(ctx, "transcript_lines", "e", 10))
	id0, seq0 := pending[0]["_id"], pending[0]["seq"]

	result := session.Doc{"tool_use_ids": []string{"t1"}}
	must(t, s.SetEnrichment(ctx, "transcript_lines", id0, "e", result))
	result["tool_use_ids"] = "mutated"
	must(t, s.SetEnrichment(ctx, "transcript_lines", "no-such-id", "e", session.Doc{}))

	lines := ok(s.TranscriptLines(ctx, session.LineQuery{AccountID: "acc", SessionID: "s1"}))
	for _, l := range lines {
		if l["seq"] == seq0 {
			eq(t, "enrichments.e", l.Map("enrichments")["e"], session.Doc{"tool_use_ids": []string{"t1"}})
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

func returnedDocsAreCopies(t *testing.T, s session.Store) {
	must(t, s.UpsertSession(ctx, session.Session{SessionID: "s1", AccountID: "acc", Cwd: "/a", LastSeen: t0}))
	must(t, s.UpsertTranscriptLines(ctx, []session.TranscriptLine{tl("s1", 0, "a", "acc")}))

	got := ok(s.GetSession(ctx, "acc", "s1"))
	got["cwd"] = "mutated"
	eq(t, "session unchanged", ok(s.GetSession(ctx, "acc", "s1"))["cwd"], "/a")

	lines := ok(s.TranscriptLines(ctx, session.LineQuery{AccountID: "acc", SessionID: "s1"}))
	lines[0].Line()["type"] = "mutated"
	again := ok(s.TranscriptLines(ctx, session.LineQuery{AccountID: "acc", SessionID: "s1"}))
	eq(t, "nested line unchanged", again[0].Line()["type"], "a")

	full := ok(s.FullSession(ctx, "acc", "s1"))
	docList(t, full, "transcript_lines")[0].Line()["type"] = "mutated"
	eq(t, "full session is a copy", ok(s.TranscriptLines(ctx, session.LineQuery{AccountID: "acc", SessionID: "s1"}))[0].Line()["type"], "a")
}

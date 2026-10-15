package memory

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
	"github.com/robbiebyrd/clued/mind-palace/session/summary"
)

// put stores a native record directly.
func put(t *testing.T, svc *Service, kind, id, day, text string, at time.Time, extra session.Doc) {
	t.Helper()
	if extra == nil {
		extra = session.Doc{}
	}
	extra["day"] = day
	ev := svc.record(kind, id, project, text, at, extra)
	if kind == KindSession || kind == KindCheckpoint {
		ev["session_id"] = strings.TrimPrefix(strings.TrimPrefix(id, KindSession+"/"), KindCheckpoint+"/")
	}
	if _, err := summary.Replace(ctx, svc.Store, account, ev); err != nil {
		t.Fatal(err)
	}
}

func kindsOf(notes []Note) string {
	var out []string
	for _, n := range notes {
		out = append(out, n.Kind)
	}
	return strings.Join(out, ",")
}

func size(notes []Note) int {
	n := 0
	for _, note := range notes {
		n += len(note.Text)
	}
	return n
}

// seedBundle stores one record of each layer, five old session notes and
// one of today.
func seedBundle(t *testing.T, svc *Service, now time.Time) {
	t.Helper()
	put(t, svc, KindPinned, "memory-pinned/"+project, "", "Always run make check.", now.Add(-30*24*time.Hour), session.Doc{"edited": true})
	put(t, svc, KindHandoff, "memory-handoff/"+project+"/old", "", "an old handoff", now.Add(-8*24*time.Hour), nil)
	put(t, svc, KindHandoff, "memory-handoff/"+project+"/new", "", "Next: finish the hook tests.", now.Add(-time.Hour), nil)
	put(t, svc, KindRecent, "memory-recent/"+project, "", "This week: native memory.", now.Add(-12*time.Hour), nil)
	put(t, svc, KindArchive, "memory-archive/"+project+"/2026-08", "2026-08-31", "August archive.", now.Add(-40*24*time.Hour), nil)
	put(t, svc, KindArchive, "memory-archive/"+project+"/2026-09", "2026-09-30", "September archive.", now.Add(-10*24*time.Hour), nil)
	for i := 1; i <= 5; i++ {
		at := now.Add(-time.Duration(i) * 24 * time.Hour)
		put(t, svc, KindSession, fmt.Sprintf("session-summary/old%d", i), at.Format(time.DateOnly), fmt.Sprintf("old note %d", i), at, nil)
	}
	put(t, svc, KindSession, "session-summary/today", now.Format(time.DateOnly), "today's note", now.Add(-2*time.Hour), nil)
	put(t, svc, KindCheckpoint, "memory-checkpoint/live", now.Format(time.DateOnly), "checkpoint of the live session", now.Add(-time.Minute), nil)
	put(t, svc, KindDay, "memory-day/"+project+"/x", "2026-10-12", "a day", now.Add(-24*time.Hour), nil)
}

func TestBundleOrderAndBudget(t *testing.T) {
	st := memsession.New()
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	svc := newService(st, &clock{t: now})
	seedBundle(t, svc, now)
	notes, err := svc.Bundle(ctx, project, 1500, BundleStart)
	must(t, err)
	if got := kindsOf(notes); got != "memory-pinned,memory-handoff,memory-recent,session-summary,session-summary,session-summary,session-summary,memory-archive" {
		t.Fatalf("order = %s", got)
	}
	var texts []string
	for _, n := range notes {
		texts = append(texts, n.Text)
	}
	joined := strings.Join(texts, "|")
	for _, want := range []string{"Next: finish the hook tests.", "today's note", "old note 1", "old note 2", "old note 3", "September archive."} {
		if !strings.Contains(joined, want) {
			t.Errorf("bundle lacks %q: %s", want, joined)
		}
	}
	for _, unwanted := range []string{"an old handoff", "old note 4", "August", "checkpoint", "a day"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("bundle holds %q: %s", unwanted, joined)
		}
	}
	// A small budget is never exceeded: notes are cut, the rest left out.
	put(t, svc, KindPinned, "memory-pinned/"+project, "", strings.Repeat("pinned words ", 100), now, session.Doc{"edited": true})
	put(t, svc, KindRecent, "memory-recent/"+project, "", strings.Repeat("recent words ", 100), now, nil)
	for _, budget := range []int{50, 100, 300} {
		notes, err := svc.Bundle(ctx, project, budget, BundleStart)
		must(t, err)
		if size(notes) > budget*CharsPerToken {
			t.Errorf("budget %d: %d characters, over %d", budget, size(notes), budget*CharsPerToken)
		}
		if len(notes) == 0 || notes[0].Kind != KindPinned || !strings.HasSuffix(notes[0].Text, "…") {
			t.Errorf("budget %d: the pinned note leads, cut: %+v", budget, notes)
		}
	}
	// What a section leaves unused passes down: a short pinned note leaves
	// room for the whole recent record.
	put(t, svc, KindPinned, "memory-pinned/"+project, "", "short", now, session.Doc{"edited": true})
	notes, _ = svc.Bundle(ctx, project, 500, BundleStart)
	for _, n := range notes {
		if n.Kind == KindRecent && strings.HasSuffix(n.Text, "…") {
			t.Errorf("recent cut although the pinned share was unused: %d chars", len(n.Text))
		}
	}
	if none, _ := svc.Bundle(ctx, "/w/other", 1500, BundleStart); len(none) != 0 {
		t.Errorf("another project's bundle = %+v", none)
	}
}

func TestBundleCompactModeOnlyPinnedHandoffCheckpoint(t *testing.T) {
	st := memsession.New()
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	svc := newService(st, &clock{t: now})
	seedBundle(t, svc, now)
	notes, err := svc.Bundle(ctx, project, 1500, BundleCompact)
	must(t, err)
	if got := kindsOf(notes); got != "memory-pinned,memory-handoff,memory-checkpoint" {
		t.Errorf("compact = %s", got)
	}
}

func TestBundleExcludesPrivate(t *testing.T) {
	st := memsession.New()
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	svc := newService(st, &clock{t: now})
	if _, err := svc.Remember(ctx, project, "handoff", "private handoff", true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Remember(ctx, project, "note", "public note <private>hidden part</private>", false); err != nil {
		t.Fatal(err)
	}
	notes, err := svc.Bundle(ctx, project, 1500, BundleStart)
	must(t, err)
	for _, n := range notes {
		if n.Private || strings.Contains(n.Text, "private handoff") || strings.Contains(n.Text, "hidden part") {
			t.Errorf("private content injected: %+v", n)
		}
	}
	if len(notes) != 1 || notes[0].Text != "public note" {
		t.Errorf("bundle = %+v", notes)
	}
}

func TestBundleLegacyOnlyWhenNoNativeRecords(t *testing.T) {
	st := memsession.New()
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	svc := newService(st, &clock{t: now})
	must(t, st.UpsertSession(ctx, session.Session{SessionID: "old", AccountID: account, ProjectPath: project, LastSeen: now.AddDate(0, -1, 0)}))
	must(t, st.UpsertSession(ctx, session.Session{SessionID: "elsewhere", AccountID: account, ProjectPath: "/w/beta", LastSeen: now.AddDate(0, -1, 0)}))
	legacy := func(sid, kind, text string) session.Doc {
		return session.Doc{"session_id": sid, "account_id": account, "hook_event_name": session.MemoryEventName, "cwd": project, "created_at": now.AddDate(0, -1, 0),
			"memory": session.Doc{"kind": kind, "file": kind + ".md", "text": text, "hash": "legacy-" + sid + text}}
	}
	for _, ev := range []session.Doc{legacy("old", "identity", "I am the alpha project."), legacy("old", "entry", "## 09:00 | main\nlegacy entry"), legacy("elsewhere", "entry", "beta entry")} {
		must(t, st.InsertHookEvent(ctx, ev))
	}
	notes, err := svc.Bundle(ctx, project, 1500, BundleStart)
	must(t, err)
	if got := kindsOf(notes); got != "identity,entry" || !notes[0].Legacy {
		t.Errorf("legacy bundle = %+v", notes)
	}
	if _, err := svc.Remember(ctx, project, "pinned", "native pinned", false); err != nil {
		t.Fatal(err)
	}
	notes, _ = svc.Bundle(ctx, project, 1500, BundleStart)
	if got := kindsOf(notes); got != "memory-pinned" {
		t.Errorf("with a native record the legacy ones go: %s", got)
	}
}

func TestWorktreesShareProjectKey(t *testing.T) {
	for _, origin := range []string{"git@github.com:Owner/Repo.git", "https://github.com/owner/repo", "ssh://git@github.com:22/owner/repo.git", "https://user@GitHub.com/owner/repo.git/"} {
		if got := ProjectKey(origin, "/anywhere"); got != "github.com/owner/repo" {
			t.Errorf("ProjectKey(%q) = %q", origin, got)
		}
	}
	if got := ProjectKey("", "/w/alpha/"); got != "/w/alpha" {
		t.Errorf("no origin: %q", got)
	}
	if ProjectKey("", "") != "" {
		t.Error("nothing yields no key")
	}
	st := memsession.New()
	c := &clock{t: day1.AddDate(0, 0, 1)}
	svc := newService(st, c)
	addSession(t, st, account, "wt1", "/w/repo", "git@github.com:owner/repo.git", day1)
	addSession(t, st, account, "wt2", "/w/repo-feature", "https://github.com/owner/repo.git", day1.Add(time.Hour))
	if _, err := svc.Run(ctx, "github.com/owner/repo", summary.ExtractEngine{}, ""); err != nil {
		t.Fatal(err)
	}
	notes, err := svc.Bundle(ctx, "github.com/owner/repo", 1500, BundleStart)
	must(t, err)
	joined := ""
	for _, n := range notes {
		joined += n.Text + "|"
	}
	if !strings.Contains(joined, "Work on wt1") || !strings.Contains(joined, "Work on wt2") {
		t.Errorf("worktrees do not share memory: %s", joined)
	}
}

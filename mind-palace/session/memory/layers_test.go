package memory

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
	"github.com/robbiebyrd/clued/mind-palace/session/summary"
)

const project = "/w/alpha"

func TestRunBuildsDayAfterUTCDayEnds(t *testing.T) {
	st := memsession.New()
	addSession(t, st, account, "s1", project, "", day1)
	addSession(t, st, account, "s2", project, "", day1.Add(time.Hour))
	addSession(t, st, account, "s3", project, "", day1.AddDate(0, 0, 1))
	c := &clock{t: day1.AddDate(0, 0, 1).Add(3 * time.Hour)} // 2026-10-14 12:00
	svc := newService(st, c)
	eng := &fakeEngine{}
	rep, err := svc.Run(ctx, project, eng, "haiku")
	must(t, err)
	if rep.Notes != 3 || rep.Days != 1 {
		t.Errorf("report = %+v", rep)
	}
	if got := ids(t, st, KindSession); !reflect.DeepEqual(got, []string{"session-summary/s1", "session-summary/s2", "session-summary/s3"}) {
		t.Errorf("notes = %v", got)
	}
	if got := ids(t, st, KindDay); !reflect.DeepEqual(got, []string{"memory-day//w/alpha/2026-10-13"}) {
		t.Errorf("days = %v, want only the finished day", got)
	}
	days, _ := svc.Show(ctx, project, false, KindDay)
	if len(days) != 1 || !strings.HasPrefix(days[0].Text, "## day by the engine") || days[0].Day != "2026-10-13" || days[0].Engine != "claude" {
		t.Errorf("day = %+v", days)
	}
	var dayPrompt string
	for _, p := range eng.prompts {
		if strings.Contains(p, "one day of work") {
			dayPrompt = p
		}
	}
	if !strings.Contains(dayPrompt, `label="session s1"`) || !strings.Contains(dayPrompt, `label="session s2"`) || strings.Contains(dayPrompt, "session s3") {
		t.Errorf("the day's prompt holds that day's notes only:\n%s", dayPrompt)
	}
}

// daily runs memory once a day at noon from start for n days, with one
// session each morning.
func daily(t *testing.T, st session.Store, svc *Service, c *clock, eng *fakeEngine, start time.Time, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		day := start.AddDate(0, 0, i)
		addSession(t, st, account, "d"+day.Format("0102"), project, "", day)
		c.set(day.Add(3 * time.Hour))
		if _, err := svc.Run(ctx, project, eng, ""); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRunRebuildsRecentFromLastSevenDays(t *testing.T) {
	st := memsession.New()
	c := &clock{}
	svc := newService(st, c)
	svc.Limits.MaxRunsPerDay = -1
	daily(t, st, svc, c, &fakeEngine{}, day1, 10) // 13 to 22 October; now the 22nd at noon
	recent, err := svc.Show(ctx, project, false, KindRecent)
	must(t, err)
	if len(recent) != 1 || recent[0].ID != "memory-recent//w/alpha" {
		t.Fatalf("recent = %+v", recent)
	}
	events, _ := st.MemoryEvents(ctx, session.MemoryQuery{AccountID: account, Kinds: []string{KindRecent}})
	mem := events[0].Map("memory")
	if mem["from"] != "2026-10-15" || mem["to"] != "2026-10-21" {
		t.Errorf("recent covers %v..%v, want the seven finished days 2026-10-15..2026-10-21", mem["from"], mem["to"])
	}
}

func TestRunFoldsOlderDaysIntoMonthArchive(t *testing.T) {
	st := memsession.New()
	c := &clock{}
	svc := newService(st, c)
	svc.Limits.MaxRunsPerDay = -1
	start := time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC)
	daily(t, st, svc, c, &fakeEngine{}, start, 20) // 25 September to 14 October
	if got := ids(t, st, KindArchive); !reflect.DeepEqual(got, []string{"memory-archive//w/alpha/2026-09", "memory-archive//w/alpha/2026-10"}) {
		t.Fatalf("archives = %v", got)
	}
	events, _ := st.MemoryEvents(ctx, session.MemoryQuery{AccountID: account, Kinds: []string{KindArchive}, IDs: []string{"memory-archive//w/alpha/2026-10"}})
	// Now is 14 October: days before the 7th are older than the recent window.
	if mem := events[0].Map("memory"); mem["month"] != "2026-10" || mem["day"] != "2026-10-06" {
		t.Errorf("October archive = %v", mem)
	}
	if n := len(ids(t, st, KindDay)); n != 19 {
		t.Errorf("%d day records, want 19 (the days are kept)", n)
	}
}

func TestRunIdempotent(t *testing.T) {
	st := memsession.New()
	c := &clock{}
	svc := newService(st, c)
	svc.Limits.MaxRunsPerDay = -1
	eng := &fakeEngine{}
	daily(t, st, svc, c, eng, day1, 10)
	before := eng.count()
	rep, err := svc.Run(ctx, project, eng, "")
	must(t, err)
	if eng.count() != before || rep.EngineRuns != 0 || rep.Notes+rep.Days+rep.Recent+rep.Archives != 0 {
		t.Errorf("a second run made %d engine calls: %+v", eng.count()-before, rep)
	}
	extract := &Service{Store: st, AccountID: account, Now: c.now, Summary: svc.Summary}
	if rep, _ := extract.Run(ctx, project, summary.ExtractEngine{}, ""); rep.Notes+rep.Days+rep.Recent+rep.Archives != 0 {
		t.Errorf("an extract run with nothing new wrote %+v", rep)
	}
}

func TestRunWithTheFreeEngineIsIdempotent(t *testing.T) {
	st := memsession.New()
	addSession(t, st, account, "s1", project, "", day1)
	c := &clock{t: day1.AddDate(0, 0, 1)}
	svc := newService(st, c)
	first, err := svc.Run(ctx, project, summary.ExtractEngine{}, "")
	must(t, err)
	if first.Notes != 1 || first.Days != 1 || first.EngineRuns != 0 || first.CostUSD != 0 {
		t.Errorf("first = %+v", first)
	}
	count := func() int {
		events, _ := st.MemoryEvents(ctx, session.MemoryQuery{AccountID: account, IncludePrivate: true})
		return len(events)
	}
	n := count()
	second, err := svc.Run(ctx, project, summary.ExtractEngine{}, "")
	must(t, err)
	if second.Notes+second.Days+second.Recent+second.Archives != 0 || count() != n {
		t.Errorf("second = %+v, records %d → %d", second, n, count())
	}
}

func TestRunRespectsMaxRunsPerDay(t *testing.T) {
	st := memsession.New()
	for _, sid := range []string{"a", "b", "c"} {
		addSession(t, st, account, sid, project, "", day1)
	}
	c := &clock{t: day1.AddDate(0, 0, 1)}
	svc := newService(st, c)
	svc.StateDir = t.TempDir()
	svc.Limits.MaxRunsPerDay = 2
	eng := &fakeEngine{}
	rep, err := svc.Run(ctx, project, eng, "")
	must(t, err)
	if eng.count() != 2 || rep.EngineRuns != 2 || !rep.CapReached || rep.Fallbacks != 3 || rep.Notes != 3 || rep.Days != 1 || rep.Recent != 1 {
		t.Errorf("report = %+v (engine calls %d)", rep, eng.count())
	}
	notes, _ := svc.Show(ctx, project, false, KindSession, KindDay)
	engines := map[string]int{}
	for _, n := range notes {
		engines[n.Engine]++
	}
	if engines["claude"] != 2 || engines["extract"] != 2 {
		t.Errorf("engines = %v", engines)
	}
	if svc.RunsToday() != 2 {
		t.Errorf("runs today = %d", svc.RunsToday())
	}
	// The count is kept in StateDir: another service the same day is capped.
	other := newService(st, c)
	other.StateDir, other.Limits.MaxRunsPerDay = svc.StateDir, 2
	addSession(t, st, account, "d", project, "", day1.AddDate(0, 0, 1))
	c.set(day1.AddDate(0, 0, 1).Add(2 * time.Hour))
	rep, err = other.Run(ctx, project, eng, "")
	must(t, err)
	if eng.count() != 2 || !rep.CapReached {
		t.Errorf("the cap is per day across runs: %+v", rep)
	}
	// An engine error falls back to extract too.
	failing := &fakeEngine{err: errors.New("boom")}
	fresh := newService(memsession.New(), c)
	addSession(t, fresh.Store, account, "e", project, "", day1)
	rep, err = fresh.Run(ctx, project, failing, "")
	must(t, err)
	if rep.Notes != 1 || rep.Fallbacks == 0 || len(rep.Errors) == 0 {
		t.Errorf("failing engine = %+v", rep)
	}
}

func TestCheckpointReplacedBySessionNote(t *testing.T) {
	st := memsession.New()
	addSession(t, st, account, "s1", project, "", day1)
	c := &clock{t: day1.Add(5 * time.Minute)}
	svc := newService(st, c)
	must(t, svc.Checkpoint(ctx, "s1", summary.ExtractEngine{}, ""))
	if got := ids(t, st, KindCheckpoint); !reflect.DeepEqual(got, []string{"memory-checkpoint/s1"}) {
		t.Fatalf("checkpoints = %v", got)
	}
	c.set(day1.Add(2 * time.Hour))
	if _, err := svc.Run(ctx, project, summary.ExtractEngine{}, ""); err != nil {
		t.Fatal(err)
	}
	if got := ids(t, st, KindCheckpoint); len(got) != 0 {
		t.Errorf("checkpoint left after the note: %v", got)
	}
	if got := ids(t, st, KindSession); !reflect.DeepEqual(got, []string{"session-summary/s1"}) {
		t.Errorf("notes = %v", got)
	}
}

func TestEditedRecordNeverOverwritten(t *testing.T) {
	st := memsession.New()
	addSession(t, st, account, "s1", project, "", day1)
	c := &clock{t: day1.AddDate(0, 0, 1)}
	svc := newService(st, c)
	eng := &fakeEngine{}
	if _, err := svc.Run(ctx, project, eng, ""); err != nil {
		t.Fatal(err)
	}
	md, err := svc.EditMarkdown(ctx, project)
	must(t, err)
	edited := strings.Replace(md, "## day by the engine", "## my own words for the day", 1)
	if _, err := svc.ApplyMarkdown(ctx, project, edited); err != nil {
		t.Fatal(err)
	}
	// A new session of the same day changes the day's sources.
	addSession(t, st, account, "s2", project, "", day1.Add(2*time.Hour))
	if _, err := svc.Run(ctx, project, eng, ""); err != nil {
		t.Fatal(err)
	}
	days, _ := svc.Show(ctx, project, false, KindDay)
	if len(days) != 1 || !strings.HasPrefix(days[0].Text, "## my own words for the day") || !days[0].Edited {
		t.Errorf("edited day overwritten: %+v", days)
	}
}

func TestRunLockPreventsConcurrentRuns(t *testing.T) {
	st := memsession.New()
	addSession(t, st, account, "s1", project, "", day1)
	c := &clock{t: day1.AddDate(0, 0, 1)}
	svc := newService(st, c)
	svc.StateDir = t.TempDir()
	eng := &fakeEngine{block: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		_, err := svc.Run(ctx, project, eng, "")
		done <- err
	}()
	// Wait until the first run holds the lock (its lock file exists).
	deadline := time.Now().Add(5 * time.Second)
	for {
		if m, _ := filepath.Glob(filepath.Join(svc.StateDir, "run-*.lock")); len(m) == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := svc.Run(ctx, project, summary.ExtractEngine{}, ""); !errors.Is(err, ErrLocked) {
		t.Errorf("concurrent run = %v, want ErrLocked", err)
	}
	close(eng.block)
	must(t, <-done)
	if m, _ := filepath.Glob(filepath.Join(svc.StateDir, "run-*.lock")); len(m) != 0 {
		t.Errorf("lock file left: %v", m)
	}
	// Another process's lock file blocks too, until it is stale.
	other := newService(st, c)
	other.StateDir = svc.StateDir
	if _, err := other.Run(ctx, project, summary.ExtractEngine{}, ""); err != nil {
		t.Fatal(err)
	}
	unlock, err := other.lock(project)
	must(t, err)
	files, _ := filepath.Glob(filepath.Join(svc.StateDir, "run-*.lock"))
	if len(files) != 1 {
		t.Fatalf("lock files = %v", files)
	}
	// Simulate another process: drop this process's mutex but keep the file.
	held, _ := os.ReadFile(files[0])
	unlock()
	must(t, os.WriteFile(files[0], held, 0o600))
	if _, err := other.Run(ctx, project, summary.ExtractEngine{}, ""); !errors.Is(err, ErrLocked) {
		t.Errorf("run with another process's lock file = %v, want ErrLocked", err)
	}
	old := time.Now().Add(-time.Hour)
	must(t, os.Chtimes(files[0], old, old))
	if _, err := other.Run(ctx, project, summary.ExtractEngine{}, ""); err != nil {
		t.Errorf("a stale lock must be taken over: %v", err)
	}
}

func TestStatusCountsLayersRunsAndCost(t *testing.T) {
	st := memsession.New()
	addSession(t, st, account, "s1", project, "", day1)
	c := &clock{t: day1.AddDate(0, 0, 1)}
	svc := newService(st, c)
	if _, err := svc.Run(ctx, project, &fakeEngine{}, ""); err != nil {
		t.Fatal(err)
	}
	status, err := svc.Status(ctx, project)
	must(t, err)
	if status.Records[KindSession] != 1 || status.Records[KindDay] != 1 || status.Records[KindRecent] != 1 || status.RunsToday != 3 || status.MaxRunsPerDay != DefaultMaxRunsPerDay || status.CostMonthUSD < 0.0299 || status.LastBuilt == nil {
		t.Errorf("status = %+v", status)
	}
}

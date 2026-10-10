package enrich

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
)

const hookEvents = "hook_events"

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)) }

// seedEvents inserts one hook event per tool name for sessionID.
func seedEvents(t *testing.T, store session.Store, sessionID string, tools ...string) {
	t.Helper()
	ctx := context.Background()
	if err := store.UpsertSession(ctx, session.Session{SessionID: sessionID, AccountID: "acct"}); err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools {
		if err := store.InsertHookEvent(ctx, session.Doc{"session_id": sessionID, "account_id": "acct", "tool_name": tool}); err != nil {
			t.Fatal(err)
		}
	}
}

// storedEvents reads the hook events of sessionID back through the store.
func storedEvents(t *testing.T, store session.Store, sessionID string) []session.Doc {
	t.Helper()
	full, err := store.FullSession(context.Background(), "acct", sessionID)
	if err != nil {
		t.Fatal(err)
	}
	events, ok := full[hookEvents].([]session.Doc)
	if !ok {
		t.Fatalf("hook_events has type %T", full[hookEvents])
	}
	return events
}

func echoEnricher(name string, batchLimit int, matches func(session.Doc) bool) session.Enricher {
	return session.Enricher{
		Name: name, Collection: hookEvents, Enabled: true, BatchLimit: batchLimit,
		Matches: matches,
		Enrich: func(_ context.Context, doc session.Doc, _ session.Lookup) (any, error) {
			tool, _ := doc.String("tool_name")
			return session.Doc{"tool": tool}, nil
		},
	}
}

func matchAll(session.Doc) bool { return true }

func TestPassWritesResultUnderEnrichmentsName(t *testing.T) {
	store := memsession.New()
	seedEvents(t, store, "s1", "Bash")

	if err := Pass(context.Background(), store, []session.Enricher{echoEnricher("echo", 0, matchAll)}, discardLogger()); err != nil {
		t.Fatal(err)
	}

	events := storedEvents(t, store, "s1")
	got := events[0].Map("enrichments").Map("echo")
	if tool, _ := got.String("tool"); tool != "Bash" {
		t.Fatalf("enrichments.echo = %v", events[0]["enrichments"])
	}
	if _, failed := events[0].Map("enrichments")["echo_failed"]; failed {
		t.Fatal("unexpected failure record")
	}
}

func TestPassRecordsFailureWithMessageAndTime(t *testing.T) {
	store := memsession.New()
	seedEvents(t, store, "s1", "Bash")
	boom := session.Enricher{
		Name: "boom", Collection: hookEvents, Enabled: true, Matches: matchAll,
		Enrich: func(context.Context, session.Doc, session.Lookup) (any, error) { return nil, errors.New("kaput") },
	}

	before := time.Now()
	if err := Pass(context.Background(), store, []session.Enricher{boom}, discardLogger()); err != nil {
		t.Fatalf("enrich errors must not be returned: %v", err)
	}

	enr := storedEvents(t, store, "s1")[0].Map("enrichments")
	if _, ok := enr["boom"]; ok {
		t.Fatal("result written despite failure")
	}
	failure := enr.Map("boom_failed")
	if msg, _ := failure.String("message"); msg != "kaput" {
		t.Fatalf("message = %v", failure["message"])
	}
	at, ok := failure["at"].(time.Time)
	if !ok || at.Before(before) || at.After(time.Now()) {
		t.Fatalf("at = %v", failure["at"])
	}

	// A recorded failure is not retried.
	var calls atomic.Int32
	boom.Enrich = func(context.Context, session.Doc, session.Lookup) (any, error) {
		calls.Add(1)
		return nil, errors.New("again")
	}
	if err := Pass(context.Background(), store, []session.Enricher{boom}, discardLogger()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatalf("failed doc retried %d times", calls.Load())
	}
}

func TestPassLeavesNonMatchingDocsUntouched(t *testing.T) {
	store := memsession.New()
	seedEvents(t, store, "s1", "Bash", "Read")
	bashOnly := echoEnricher("echo", 0, func(d session.Doc) bool { tool, _ := d.String("tool_name"); return tool == "Bash" })

	if err := Pass(context.Background(), store, []session.Enricher{bashOnly}, discardLogger()); err != nil {
		t.Fatal(err)
	}

	for _, ev := range storedEvents(t, store, "s1") {
		tool, _ := ev.String("tool_name")
		_, hasEnrichments := ev["enrichments"]
		if tool == "Bash" && !hasEnrichments {
			t.Error("matching doc not enriched")
		}
		if tool == "Read" && hasEnrichments {
			t.Errorf("non-matching doc modified: %v", ev["enrichments"])
		}
	}
}

func TestPassRespectsBatchLimit(t *testing.T) {
	store := memsession.New()
	seedEvents(t, store, "s1", "a", "b", "c", "d", "e")
	limited := echoEnricher("echo", 2, matchAll)

	if err := Pass(context.Background(), store, []session.Enricher{limited}, discardLogger()); err != nil {
		t.Fatal(err)
	}

	enriched := 0
	for _, ev := range storedEvents(t, store, "s1") {
		if _, ok := ev["enrichments"]; ok {
			enriched++
		}
	}
	if enriched != 2 {
		t.Fatalf("enriched %d docs in one pass, want 2", enriched)
	}
}

// failingQueries wraps a real store and fails Unenriched for one collection.
type failingQueries struct {
	session.Store
	collection string
}

func (f failingQueries) Unenriched(ctx context.Context, collection, enricher string, limit int) ([]session.Doc, error) {
	if collection == f.collection {
		return nil, errors.New("query exploded")
	}
	return f.Store.Unenriched(ctx, collection, enricher, limit)
}

func TestPassLogsQueryErrorAndContinuesWithNextEnricher(t *testing.T) {
	inner := memsession.New()
	seedEvents(t, inner, "s1", "Bash")
	store := failingQueries{Store: inner, collection: "transcript_lines"}
	broken := echoEnricher("broken", 0, matchAll)
	broken.Collection = "transcript_lines"
	var logs bytes.Buffer

	err := Pass(context.Background(), store, []session.Enricher{broken, echoEnricher("echo", 0, matchAll)}, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatalf("query failures are logged, not returned: %v", err)
	}

	if !strings.Contains(logs.String(), "query exploded") || !strings.Contains(logs.String(), "broken") {
		t.Fatalf("query error not logged: %q", logs.String())
	}
	if _, ok := storedEvents(t, inner, "s1")[0]["enrichments"]; !ok {
		t.Fatal("pass did not continue with the next enricher")
	}
}

// failingWrites wraps a real store and fails SetEnrichment, and
// SetEnrichmentFailure too when failures is set.
type failingWrites struct {
	session.Store
	failures bool
}

func (failingWrites) SetEnrichment(context.Context, string, any, string, any) error {
	return errors.New("write exploded")
}

func (f failingWrites) SetEnrichmentFailure(ctx context.Context, collection string, id any, enricher, message string, at time.Time) error {
	if f.failures {
		return errors.New("failure write exploded")
	}
	return f.Store.SetEnrichmentFailure(ctx, collection, id, enricher, message, at)
}

func TestPassReturnsFirstStoreErrorAfterFinishingPass(t *testing.T) {
	inner := memsession.New()
	seedEvents(t, inner, "s1", "Bash")
	var second atomic.Int32
	next := echoEnricher("next", 0, matchAll)
	next.Enrich = func(context.Context, session.Doc, session.Lookup) (any, error) { second.Add(1); return 1, nil }

	err := Pass(context.Background(), failingWrites{Store: inner, failures: true}, []session.Enricher{echoEnricher("echo", 0, matchAll), next}, discardLogger())

	if err == nil || !strings.Contains(err.Error(), "write exploded") {
		t.Fatalf("err = %v", err)
	}
	if second.Load() != 1 {
		t.Fatal("pass stopped before running the remaining enrichers")
	}
}

func TestLoopEnrichesOnTicksAndStopsWhenContextEnds(t *testing.T) {
	store := memsession.New()
	seedEvents(t, store, "s1", "Bash")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Loop(ctx, store, []session.Enricher{echoEnricher("echo", 0, matchAll)}, 10*time.Millisecond, nil)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, ok := storedEvents(t, store, "s1")[0]["enrichments"]; ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("loop never enriched the document")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// A document arriving later is picked up by a later tick.
	seedEvents(t, store, "s2", "Read")
	for {
		if _, ok := storedEvents(t, store, "s2")[0]["enrichments"]; ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("loop did not pick up a later document")
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Loop did not return after ctx was cancelled")
	}
}

func TestPassRecordsFailureWhenResultWriteFails(t *testing.T) {
	inner := memsession.New()
	seedEvents(t, inner, "s1", "Bash")
	var calls atomic.Int32
	counted := echoEnricher("echo", 0, matchAll)
	counted.Enrich = func(context.Context, session.Doc, session.Lookup) (any, error) { calls.Add(1); return 1, nil }
	enrichers := []session.Enricher{counted}

	if err := Pass(context.Background(), failingWrites{Store: inner}, enrichers, discardLogger()); err != nil {
		t.Fatalf("recorded failure must not be returned: %v", err)
	}
	if err := Pass(context.Background(), failingWrites{Store: inner}, enrichers, discardLogger()); err != nil {
		t.Fatal(err)
	}

	failure := storedEvents(t, inner, "s1")[0].Map("enrichments").Map("echo_failed")
	if msg, _ := failure.String("message"); msg != "write exploded" {
		t.Fatalf("failure = %v", failure)
	}
	if calls.Load() != 1 {
		t.Fatalf("Enrich ran %d times, want 1", calls.Load())
	}
}

func TestPassRecordsPanicsAsFailures(t *testing.T) {
	cases := map[string]session.Enricher{
		"enrich":  {Matches: matchAll, Enrich: func(context.Context, session.Doc, session.Lookup) (any, error) { panic("enrich blew up") }},
		"matches": {Matches: func(session.Doc) bool { panic("matches blew up") }, Enrich: echoEnricher("x", 0, matchAll).Enrich},
	}
	for name, e := range cases {
		t.Run(name, func(t *testing.T) {
			store := memsession.New()
			seedEvents(t, store, "s1", "Bash")
			e.Name, e.Collection, e.Enabled = "p", hookEvents, true
			var logs bytes.Buffer

			if err := Pass(context.Background(), store, []session.Enricher{e}, slog.New(slog.NewTextHandler(&logs, nil))); err != nil {
				t.Fatal(err)
			}

			failure := storedEvents(t, store, "s1")[0].Map("enrichments").Map("p_failed")
			if msg, _ := failure.String("message"); !strings.Contains(msg, "blew up") {
				t.Fatalf("failure = %v", failure)
			}
		})
	}
}

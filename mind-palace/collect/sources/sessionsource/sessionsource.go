// Package sessionsource implements prime.SessionSource and prime.MemorySource
// over the captured session store: the most recent session captured for a
// working directory, with its most recent distinct Bash commands, and the
// memory bundle (session/memory) of that directory's project.
package sessionsource

import (
	"context"
	"regexp"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/robbiebyrd/clued/mind-palace/collect/prime"
	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/gitinfo"
	"github.com/robbiebyrd/clued/mind-palace/session/memory"
	"github.com/robbiebyrd/clued/mind-palace/session/query"
)

// Source looks up captured sessions for one account. The store is connected
// lazily on the first query and closed by Close.
type Source struct {
	connect   func(ctx context.Context) (session.Store, error)
	accountID string

	mu    sync.Mutex
	store session.Store
}

var (
	_ prime.SessionSource = (*Source)(nil)
	_ prime.MemorySource  = (*Source)(nil)
)

// New returns a source that connects with connect on first use and scopes
// every query to accountID.
func New(connect func(ctx context.Context) (session.Store, error), accountID string) *Source {
	return &Source{connect: connect, accountID: accountID}
}

// Current returns the most recent session whose project path or working
// directory is exactly cwd, or nil, nil when none was captured. An empty cwd
// yields nil, nil without touching the store.
func (s *Source) Current(ctx context.Context, cwd string) (*prime.SessionSnapshot, error) {
	if cwd == "" {
		return nil, nil
	}
	st, err := s.open(ctx)
	if err != nil {
		return nil, err
	}
	svc := &query.Service{Store: st, AccountID: s.accountID}
	docs, err := svc.FindSessions(ctx, query.FindSessionsArgs{
		Query: "^" + regexp.QuoteMeta(cwd) + "$",
		Limit: 1,
	})
	if err != nil {
		return nil, err
	}
	if len(docs) == 0 {
		return nil, nil
	}
	id, _ := docs[0].String("session_id")
	if id == "" {
		return nil, nil
	}
	// The snapshot is the found session plus its top commands: two store
	// round trips after the find, where SessionContext makes about ten,
	// too many for the prime budget over a clued server.
	top, err := svc.TopCommands(ctx, id)
	if err != nil {
		return nil, err
	}
	found := docs[0]
	model, _ := found.String("model")
	liveState, _ := found.String("live_state")
	hotness, _ := found.String("hotness")
	return &prime.SessionSnapshot{
		SessionID:   id,
		Model:       model,
		LiveState:   liveState,
		Hotness:     hotness,
		StartedAt:   timeOf(found, "started_at"),
		LastSeen:    timeOf(found, "last_seen"),
		TopCommands: top,
	}, nil
}

// Origin looks up the git origin of a directory; tests replace it.
var Origin = gitinfo.Origin

// Bundle returns the memory bundle of cwd's project (memory.ProjectKey of
// its git origin and the path) within budgetTokens, the compact one when
// compact is set; a start bundle with budget left over ends with
// yesterday's digest, cut to fit. An empty cwd yields none without
// touching the store.
func (s *Source) Bundle(ctx context.Context, cwd string, budgetTokens int, compact bool) ([]prime.MemoryNote, error) {
	if cwd == "" {
		return []prime.MemoryNote{}, nil
	}
	st, err := s.open(ctx)
	if err != nil {
		return nil, err
	}
	mode := memory.BundleStart
	if compact {
		mode = memory.BundleCompact
	}
	svc := &memory.Service{Store: st, AccountID: s.accountID}
	bundle, err := svc.Bundle(ctx, memory.ProjectKey(Origin(ctx, cwd), cwd), budgetTokens, mode)
	if err != nil {
		return nil, err
	}
	notes := make([]prime.MemoryNote, 0, len(bundle)+1)
	used := 0
	for _, n := range bundle {
		used += len(n.Text)
		notes = append(notes, prime.MemoryNote{ID: n.ID, Kind: n.Kind, Day: n.Day, Text: n.Text, Legacy: n.Legacy, CreatedAt: n.CreatedAt})
	}
	if left := budgetTokens*memory.CharsPerToken - used; !compact && left >= minDigest {
		if d := s.yesterdaysDigest(ctx, &query.Service{Store: st, AccountID: s.accountID}); d != nil {
			d.Text = clip(d.Text, left)
			notes = append(notes, *d)
		}
	}
	return notes, nil
}

// clip cuts text to at most n bytes at a rune boundary, marking the cut.
func clip(text string, n int) string {
	const mark = "…"
	if len(text) <= n {
		return text
	}
	cut := n - len(mark)
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + mark
}

// minDigest is the budget left over (in characters) a digest needs to be
// shown at all.
const minDigest = 200

// yesterdaysDigest is the digest `session digest` stored for yesterday
// (UTC), as a note of kind digest; nil when there is none (or the store
// fails: the digest is a bonus, not the section).
func (s *Source) yesterdaysDigest(ctx context.Context, svc *query.Service) *prime.MemoryNote {
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format(time.DateOnly)
	hits, err := svc.SearchMemory(ctx, query.SearchMemoryArgs{Kind: "digest", Limit: 20})
	if err != nil {
		return nil
	}
	for _, h := range hits {
		if h.Day == yesterday && h.File == "digest/"+yesterday+".md" {
			return &prime.MemoryNote{Kind: h.Kind, Day: h.Day, Text: h.Text, CreatedAt: h.CreatedAt}
		}
	}
	return nil
}

// open connects once; a failed connect is retried on the next call.
func (s *Source) open(ctx context.Context) (session.Store, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store != nil {
		return s.store, nil
	}
	st, err := s.connect(ctx)
	if err != nil {
		return nil, err
	}
	s.store = st
	return st, nil
}

// Close closes the store when one was connected; it is a no-op otherwise.
func (s *Source) Close(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.store == nil {
		return nil
	}
	err := s.store.Close(ctx)
	s.store = nil
	return err
}

// timeOf reads a timestamp field that a store may hold as a time.Time, a
// *time.Time or an RFC3339 string; anything else is the zero time.
func timeOf(d session.Doc, key string) time.Time {
	switch v := d[key].(type) {
	case time.Time:
		return v
	case *time.Time:
		if v != nil {
			return *v
		}
	case string:
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return t
		}
	}
	return time.Time{}
}

// Package sessionsource implements prime.SessionSource and prime.MemorySource
// over the captured session store: the most recent session captured for a
// working directory, with its most recent distinct Bash commands, and the
// newest memory records (claude-remember summaries) of that directory.
package sessionsource

import (
	"context"
	"regexp"
	"sync"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/collect/prime"
	"github.com/robbiebyrd/clued/mind-palace/session"
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
	c, err := svc.SessionContext(ctx, id)
	if err != nil {
		return nil, err
	}
	top := c.TopCommands
	if top == nil {
		top = []string{}
	}
	model, _ := c.Session.String("model")
	return &prime.SessionSnapshot{
		SessionID:   id,
		Model:       model,
		StartedAt:   timeOf(c.Session, "started_at"),
		LastSeen:    timeOf(c.Session, "last_seen"),
		TopCommands: top,
	}, nil
}

// Recent returns the newest memory records of the sessions whose project
// path is cwd, at most limit of them; an empty cwd yields none without
// touching the store.
func (s *Source) Recent(ctx context.Context, cwd string, limit int) ([]prime.MemoryNote, error) {
	if cwd == "" {
		return []prime.MemoryNote{}, nil
	}
	st, err := s.open(ctx)
	if err != nil {
		return nil, err
	}
	svc := &query.Service{Store: st, AccountID: s.accountID}
	hits, err := svc.SearchMemory(ctx, query.SearchMemoryArgs{ProjectPath: cwd, Limit: limit})
	if err != nil {
		return nil, err
	}
	notes := make([]prime.MemoryNote, 0, len(hits))
	for _, h := range hits {
		notes = append(notes, prime.MemoryNote{Kind: h.Kind, Day: h.Day, Time: h.Time, Branch: h.Branch, Text: h.Text, CreatedAt: h.CreatedAt})
	}
	return notes, nil
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

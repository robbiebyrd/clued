// Package events is the in-process publish/subscribe bus that the WebSocket
// and MCP entrypoints use to stream document updates.
package events

import (
	"sync"

	"github.com/robbiebyrd/clued/mind-palace/model"
)

// Event actions; the type is "<kind>.<action>" (plan.created, story.updated).
const (
	ActionCreated = "created"
	ActionUpdated = "updated"
	ActionDeleted = "deleted"
)

// Template event types (the kind is carried in Kind).
const (
	TemplateUpdated = "template.updated"
	TemplateDeleted = "template.deleted"
)

// Event describes one change.
type Event struct {
	Type string `json:"event"`
	// Kind is the document kind ("plan", "story").
	Kind string `json:"kind,omitempty"`
	// ID is the document id for document events.
	ID         string          `json:"documentId,omitempty"`
	TemplateID string          `json:"templateId,omitempty"`
	Document   *model.Document `json:"document,omitempty"`
	// Operation is the service operation that produced the change.
	Operation string `json:"operation,omitempty"`
	At        string `json:"at"`
}

// Filter selects the events a subscription receives: Kind narrows to one
// kind ("" = every kind); IDs narrows to specific documents (nil = every
// document, including template events).
type Filter struct {
	Kind string
	IDs  []string
}

// Subscription receives the events matching its filter.
type Subscription struct {
	C    chan Event
	bus  *Bus
	id   int
	kind string
	ids  map[string]bool // nil = every document
	mu   sync.Mutex
}

// Matches reports whether the subscription wants the event.
func (s *Subscription) Matches(e Event) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.kind != "" && e.Kind != s.kind {
		return false
	}
	if s.ids == nil {
		return true
	}
	return e.ID != "" && s.ids[e.ID]
}

// SetFilter replaces the document filter (nil means every document).
func (s *Subscription) SetFilter(ids []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ids == nil {
		s.ids = nil
		return
	}
	s.ids = make(map[string]bool, len(ids))
	for _, id := range ids {
		s.ids[id] = true
	}
}

// Add widens the filter with more document IDs.
func (s *Subscription) Add(ids ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ids == nil {
		return
	}
	for _, id := range ids {
		s.ids[id] = true
	}
}

// Remove narrows the filter.
func (s *Subscription) Remove(ids ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ids == nil {
		return
	}
	for _, id := range ids {
		delete(s.ids, id)
	}
}

// Close unsubscribes.
func (s *Subscription) Close() { s.bus.unsubscribe(s) }

// Bus fans events out to subscribers. Slow subscribers drop events rather
// than block publishers.
type Bus struct {
	mu   sync.RWMutex
	subs map[int]*Subscription
	next int
}

// New creates a bus.
func New() *Bus { return &Bus{subs: map[int]*Subscription{}} }

// Subscribe registers interest in the events matching the filter.
func (b *Bus) Subscribe(f Filter, buffer int) *Subscription {
	if buffer <= 0 {
		buffer = 64
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.next++
	s := &Subscription{C: make(chan Event, buffer), bus: b, id: b.next, kind: f.Kind}
	s.SetFilter(f.IDs)
	b.subs[s.id] = s
	return s
}

func (b *Bus) unsubscribe(s *Subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.subs[s.id]; ok {
		delete(b.subs, s.id)
		close(s.C)
	}
}

// Publish delivers an event to every matching subscriber.
func (b *Bus) Publish(e Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, s := range b.subs {
		if !s.Matches(e) {
			continue
		}
		select {
		case s.C <- e:
		default:
		}
	}
}

// Len returns the subscriber count.
func (b *Bus) Len() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}

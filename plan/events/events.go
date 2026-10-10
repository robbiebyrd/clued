// Package events is the in-process publish/subscribe bus that the WebSocket
// and MCP entrypoints use to stream plan updates.
package events

import (
	"sync"

	"github.com/robbiebyrd/clued/plan/model"
)

// Event types.
const (
	PlanCreated     = "plan.created"
	PlanUpdated     = "plan.updated"
	PlanDeleted     = "plan.deleted"
	TemplateUpdated = "template.updated"
	TemplateDeleted = "template.deleted"
)

// Event describes one change.
type Event struct {
	Type       string      `json:"event"`
	PlanID     string      `json:"planId,omitempty"`
	TemplateID string      `json:"templateId,omitempty"`
	Plan       *model.Plan `json:"plan,omitempty"`
	// Operation is the service operation that produced the change.
	Operation string `json:"operation,omitempty"`
	At        string `json:"at"`
}

// Subscription receives events for the plans it was created for.
type Subscription struct {
	C      chan Event
	bus    *Bus
	id     int
	filter map[string]bool // nil = every plan
	mu     sync.Mutex
}

// Matches reports whether the subscription wants the event.
func (s *Subscription) Matches(e Event) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.filter == nil {
		return true
	}
	if e.PlanID != "" && s.filter[e.PlanID] {
		return true
	}
	return false
}

// SetFilter replaces the plan filter (nil means every plan).
func (s *Subscription) SetFilter(planIDs []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if planIDs == nil {
		s.filter = nil
		return
	}
	s.filter = make(map[string]bool, len(planIDs))
	for _, id := range planIDs {
		s.filter[id] = true
	}
}

// Add widens the filter with more plan IDs.
func (s *Subscription) Add(planIDs ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.filter == nil {
		return
	}
	for _, id := range planIDs {
		s.filter[id] = true
	}
}

// Remove narrows the filter.
func (s *Subscription) Remove(planIDs ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.filter == nil {
		return
	}
	for _, id := range planIDs {
		delete(s.filter, id)
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

// Subscribe registers interest in the given plans (nil = all).
func (b *Bus) Subscribe(planIDs []string, buffer int) *Subscription {
	if buffer <= 0 {
		buffer = 64
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.next++
	s := &Subscription{C: make(chan Event, buffer), bus: b, id: b.next}
	s.SetFilter(planIDs)
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

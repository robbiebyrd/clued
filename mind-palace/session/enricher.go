package session

import (
	"context"
	"sort"
	"sync"
)

// Enricher derives extra data for stored documents of one collection. Matches
// selects the documents it applies to; Enrich computes the result stored under
// enrichments.<Name>.
type Enricher struct {
	Name       string
	Collection string
	Enabled    bool
	BatchLimit int
	Matches    func(Doc) bool
	Enrich     func(ctx context.Context, doc Doc, lookup Lookup) (any, error)
}

var (
	registryMu sync.Mutex
	registry   = map[string]Enricher{}
)

// Register adds an enricher to the registry. It panics when the name is
// already registered.
func Register(e Enricher) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := registry[e.Name]; dup {
		panic("session: enricher registered twice: " + e.Name)
	}
	registry[e.Name] = e
}

// Active returns a new slice of the enabled enrichers whose names are not in
// disabled, sorted by name.
func Active(disabled []string) []Enricher {
	skip := make(map[string]bool, len(disabled))
	for _, name := range disabled {
		skip[name] = true
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	active := []Enricher{}
	for _, e := range registry {
		if e.Enabled && !skip[e.Name] {
			active = append(active, e)
		}
	}
	sort.Slice(active, func(i, j int) bool { return active[i].Name < active[j].Name })
	return active
}

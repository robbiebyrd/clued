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

// Active returns a new slice of the enrichers that will run, sorted by name:
// those enabled by default plus the names in enabled (which switches on
// enrichers that ship disabled, such as privacy-redact), minus the names in
// disabled. A name in both lists stays off.
func Active(disabled, enabled []string) []Enricher {
	skip := make(map[string]bool, len(disabled))
	for _, name := range disabled {
		skip[name] = true
	}
	on := make(map[string]bool, len(enabled))
	for _, name := range enabled {
		on[name] = true
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	active := []Enricher{}
	for _, e := range registry {
		if (e.Enabled || on[e.Name]) && !skip[e.Name] {
			active = append(active, e)
		}
	}
	sort.Slice(active, func(i, j int) bool { return active[i].Name < active[j].Name })
	return active
}

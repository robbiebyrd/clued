package session

import (
	"context"
	"errors"
	"sort"
	"sync"
)

// ErrSkip, returned (possibly wrapped) by Enrich or for one document by
// EnrichBatch, marks the document skipped for the enricher instead of
// failed: it leaves the queue and Store.RequeueSkipped can put it back (an
// allowance used up for the month, say).
var ErrSkip = errors.New("enricher: skip")

// Enricher derives extra data for stored documents of one collection. Matches
// selects the documents it applies to; Enrich computes the result stored under
// enrichments.<Name>. An enricher that works on many documents at once (one
// call to an embedding model) sets EnrichBatch instead: the loop calls it
// once per pass with the matching documents the policy allows, and it
// returns one result and one error per document, by position; a nil error
// stores the result, ErrSkip marks the document skipped, any other error
// is recorded as its failure. EnrichBatch takes precedence over Enrich.
type Enricher struct {
	Name string
	// Group, when set, is a second name the enricher answers to in the
	// enabled and disabled lists, so one name switches a family on or off
	// (privacy-redact covers the transcript and memory redactors).
	Group      string
	Collection string
	Enabled    bool
	BatchLimit int
	Matches    func(Doc) bool
	Enrich     func(ctx context.Context, doc Doc, lookup Lookup) (any, error)
	// EnrichBatch, when set, enriches the batch's documents in one call.
	EnrichBatch func(ctx context.Context, docs []Doc, lookup Lookup) ([]any, []error)
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

// Registered returns every registered enricher, sorted by name.
func Registered() []Enricher {
	registryMu.Lock()
	defer registryMu.Unlock()
	out := make([]Enricher, 0, len(registry))
	for _, e := range registry {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Allows reports whether e runs under the disabled and enabled lists, by the
// rule Active applies: enabled by default or listed in enabled, and not
// listed in disabled (a name matches e's Name or its Group).
func Allows(e Enricher, disabled, enabled []string) bool {
	listed := func(names []string) bool {
		for _, n := range names {
			if n == e.Name || (e.Group != "" && n == e.Group) {
				return true
			}
		}
		return false
	}
	return (e.Enabled || listed(enabled)) && !listed(disabled)
}

// Active returns a new slice of the enrichers that will run, sorted by name:
// those enabled by default plus the names in enabled (which switches on
// enrichers that ship disabled, such as privacy-redact), minus the names in
// disabled. A name in both lists stays off. A name matches an enricher's
// Name or its Group.
func Active(disabled, enabled []string) []Enricher {
	active := []Enricher{}
	for _, e := range Registered() {
		if Allows(e, disabled, enabled) {
			active = append(active, e)
		}
	}
	return active
}

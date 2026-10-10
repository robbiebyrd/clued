// Package kind describes the entity kinds the mind-palace manages: Plans and
// Stories today, with room for more. A Kind carries the behaviour that differs
// between entities (where they live, what their links may reference, which
// completion rules apply) so the service, storage and entrypoints stay generic.
package kind

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Kind names.
const (
	Plan  = "plan"
	Story = "story"
)

// Kind is the description of one entity kind.
type Kind struct {
	// Name is the singular identifier used in APIs ("plan").
	Name string
	// Plural is the collection name used in paths and tables ("plans").
	Plural string
	// Title is the human-readable singular ("Plan").
	Title string
	// DefaultDir is where file storage keeps the documents.
	DefaultDir string
	// Scheme is the MCP resource URI scheme ("plan://").
	Scheme string
	// ProgressKeyRe matches a progress key: phase/section for plans ("1.1"),
	// any depth for stories ("2.1.3").
	ProgressKeyRe *regexp.Regexp
	// ProgressKeyNoun names a progress key in messages ("section", "step").
	ProgressKeyNoun string
	// UnknownProgressKeyError is the error kind raised when a progress
	// operation names a heading that does not exist.
	UnknownProgressKeyError string
	// LinkedError is the error kind raised when delete is refused because
	// other documents link to this one.
	LinkedError string
	// PlanRelations are the relations allowed in links.plans from this kind.
	PlanRelations []string
	// StoryRelations are the relations allowed in links.stories from this kind.
	StoryRelations []string
	// ProgressStories allows `stories` lists on progress entries (plans).
	ProgressStories bool
	// PlanLinkSections allows a third tuple element on links.plans (stories).
	PlanLinkSections bool
	// HasPurpose allows the `purpose` front matter field.
	HasPurpose bool
	// TracksStarted records `started` when the document enters in_progress.
	TracksStarted bool
	// CriteriaGate refuses a move to complete while acceptance criteria are open.
	CriteriaGate bool
	// AutoComplete moves the document to complete when its last step completes.
	AutoComplete bool
	// WorkLog gives the document an append-only Work Log section.
	WorkLog bool
	// StrictProgressOnUpdate rejects content updates that orphan progress keys.
	StrictProgressOnUpdate bool
	// RepoFiles allows links.repo.pull_request and links.repo.files.
	RepoFiles bool
	// ProtectDefaultTemplate refuses to delete the stored default template
	// (it can still be updated); otherwise deleting it restores the built-in.
	ProtectDefaultTemplate bool
	// LinkTargetsMustExist requires the target of a same-kind link to exist.
	LinkTargetsMustExist bool
	// Immutable lists the managed front matter fields a patch may not touch.
	Immutable []string
	// EventPrefix prefixes event types ("plan" → plan.created).
	EventPrefix string
}

var (
	planKind = &Kind{
		Name: Plan, Plural: "plans", Title: "Plan", DefaultDir: "docs/plans", Scheme: "plan://",
		ProgressKeyRe:           regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`),
		ProgressKeyNoun:         "section",
		UnknownProgressKeyError: "UnknownSection",
		LinkedError:             "LinkedPlan",
		PlanRelations:           []string{"parent", "included", "depends", "blocks"},
		StoryRelations:          []string{"included", "depends", "blocks"},
		ProgressStories:         true,
		LinkTargetsMustExist:    true,
		Immutable:               []string{"id", "created", "updated", "completed"},
		EventPrefix:             "plan",
	}
	storyKind = &Kind{
		Name: Story, Plural: "stories", Title: "Story", DefaultDir: "docs/stories", Scheme: "story://",
		ProgressKeyRe:           regexp.MustCompile(`^[0-9]+(\.[0-9]+)*$`),
		ProgressKeyNoun:         "step",
		UnknownProgressKeyError: "UnknownStep",
		LinkedError:             "LinkedStory",
		PlanRelations:           []string{"included", "depends", "blocks"},
		StoryRelations:          []string{"parent", "included", "depends", "blocks"},
		PlanLinkSections:        true,
		HasPurpose:              true,
		TracksStarted:           true,
		CriteriaGate:            true,
		AutoComplete:            true,
		WorkLog:                 true,
		StrictProgressOnUpdate:  true,
		RepoFiles:               true,
		ProtectDefaultTemplate:  true,
		LinkTargetsMustExist:    true,
		Immutable:               []string{"id", "created", "updated", "started", "completed"},
		EventPrefix:             "story",
	}
	registry = map[string]*Kind{Plan: planKind, Story: storyKind}
)

// Get returns a kind by name or plural (case-insensitive).
func Get(name string) (*Kind, bool) {
	n := strings.ToLower(strings.TrimSpace(name))
	if k, ok := registry[n]; ok {
		return k, true
	}
	for _, k := range registry {
		if k.Plural == n {
			return k, true
		}
	}
	return nil, false
}

// Must returns a kind or panics; for the built-in names.
func Must(name string) *Kind {
	k, ok := Get(name)
	if !ok {
		panic(fmt.Sprintf("unknown kind %q", name))
	}
	return k
}

// Names lists the registered kind names, sorted.
func Names() []string {
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// All returns every registered kind in name order.
func All() []*Kind {
	var out []*Kind
	for _, n := range Names() {
		out = append(out, registry[n])
	}
	return out
}

// Event returns the event type for an action ("created", "updated", "deleted").
func (k *Kind) Event(action string) string { return k.EventPrefix + "." + action }

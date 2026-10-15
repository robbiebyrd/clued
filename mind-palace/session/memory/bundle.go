package memory

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// BundleMode picks what a bundle holds.
type BundleMode int

const (
	// BundleStart is the bundle of a new, resumed or cleared session:
	// pinned, the newest handoff under seven days, recent, the notes
	// remembered in the last seven days (at most three), today's session
	// notes and the last three before today, the head of the newest
	// archive.
	BundleStart BundleMode = iota
	// BundleCompact is the smaller bundle after a compaction: pinned, the
	// newest handoff and the newest checkpoint.
	BundleCompact
)

// DefaultBudgetTokens is the bundle budget when none is given.
const DefaultBudgetTokens = 1500

// CharsPerToken is the estimate the budget is counted with.
const CharsPerToken = 4

// minPiece is the shortest cut of a note worth injecting; the pinned note
// is cut down to tinyPiece before it is left out.
const (
	minPiece  = 80
	tinyPiece = 16
)

// handoffAge is how long a handoff is injected.
const handoffAge = 7 * 24 * time.Hour

// lastNotes is how many of the newest session notes a start bundle holds
// beside today's.
const lastNotes = 3

// section is one part of a bundle: its share of the budget and its notes
// in the order they are placed.
type section struct {
	share float64
	notes []Note
}

// Bundle returns the project's memory to inject, in order, within
// budgetTokens (DefaultBudgetTokens when zero or less; the texts' length
// estimated at CharsPerToken characters a token, so the bundle is never
// over budget). Each section has a share of the budget; what a section
// leaves unused passes down to the next, and a note longer than what is
// left is cut. Private notes are never included. A project with no native
// record gets its legacy claude-remember records instead.
func (s *Service) Bundle(ctx context.Context, project string, budgetTokens int, mode BundleMode) ([]Note, error) {
	if budgetTokens <= 0 {
		budgetTokens = DefaultBudgetTokens
	}
	if project == "" {
		return []Note{}, nil
	}
	events, err := s.records(ctx, project, false)
	if err != nil {
		return nil, err
	}
	notes := notesOf(events)
	var sections []section
	if len(notes) == 0 {
		if sections, err = s.legacySections(ctx, project, mode); err != nil {
			return nil, err
		}
	} else {
		sections = s.sections(notes, mode)
	}
	return place(sections, budgetTokens*CharsPerToken), nil
}

// sections arranges native notes (newest first) for the mode.
func (s *Service) sections(notes []Note, mode BundleMode) []section {
	now := s.now()
	today := now.UTC().Format(time.DateOnly)
	var pinned, handoff, recent, archive, checkpoint, userNotes, sessionNotes []Note
	earlier := 0
	for _, n := range notes {
		switch n.Kind {
		case KindPinned:
			pinned = append(pinned, n)
		case KindHandoff:
			if handoff == nil && now.Sub(n.CreatedAt) < handoffAge {
				handoff = []Note{n}
			}
		case KindRecent:
			if recent == nil {
				recent = []Note{n}
			}
		case KindArchive:
			if archive == nil || n.Day > archive[0].Day {
				archive = []Note{n}
			}
		case KindCheckpoint:
			if checkpoint == nil {
				checkpoint = []Note{n}
			}
		case KindNote:
			if now.Sub(n.CreatedAt) < handoffAge && len(userNotes) < lastNotes {
				userNotes = append(userNotes, n)
			}
		case KindSession:
			if n.Day == today || n.CreatedAt.UTC().Format(time.DateOnly) == today {
				sessionNotes = append(sessionNotes, n)
			} else if earlier < lastNotes {
				sessionNotes = append(sessionNotes, n)
				earlier++
			}
		}
	}
	if mode == BundleCompact {
		return []section{{0.4, pinned}, {0.3, handoff}, {0.3, checkpoint}}
	}
	return []section{
		{0.3, pinned},
		{0.15, handoff},
		{0.25, recent},
		{0.2, append(userNotes, sessionNotes...)},
		{0.1, archive},
	}
}

// legacySections arranges the claude-remember records of the project's
// sessions: identity as the pinned note, the newest handoff, recent, the
// newest entries and the newest archive.
func (s *Service) legacySections(ctx context.Context, project string, mode BundleMode) ([]section, error) {
	found, err := s.Store.FindSessions(ctx, session.SessionQuery{AccountID: s.AccountID, Limit: 500})
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, d := range found {
		if projectOf(d) == project {
			id, _ := d.String("session_id")
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	events, err := s.Store.MemoryEvents(ctx, session.MemoryQuery{AccountID: s.AccountID, Kinds: LegacyKinds, SessionIDs: ids, Limit: 200})
	if err != nil {
		return nil, err
	}
	var identity, handoff, recent, archive, entries []Note
	for _, n := range notesOf(events) {
		switch n.Kind {
		case "identity":
			if identity == nil {
				identity = []Note{n}
			}
		case "handoff":
			if handoff == nil {
				handoff = []Note{n}
			}
		case "recent":
			if recent == nil {
				recent = []Note{n}
			}
		case "archive":
			if archive == nil {
				archive = []Note{n}
			}
		case "entry", "day":
			if len(entries) < lastNotes {
				entries = append(entries, n)
			}
		}
	}
	if mode == BundleCompact {
		return []section{{0.6, identity}, {0.4, handoff}}, nil
	}
	return []section{{0.3, identity}, {0.15, handoff}, {0.25, recent}, {0.2, entries}, {0.1, archive}}, nil
}

// place fills the budget section by section.
func place(sections []section, budget int) []Note {
	out := []Note{}
	carry := 0
	spent := 0
	for i, sec := range sections {
		allow := int(sec.share*float64(budget)) + carry
		if i == len(sections)-1 {
			allow = budget - spent
		}
		used := 0
		for _, n := range sec.notes {
			left := allow - used
			if left <= 0 {
				break
			}
			text := strings.TrimSpace(n.Text)
			if len(text) > left {
				if left < minPiece && (i > 0 || left < tinyPiece) {
					continue
				}
				text = clip(text, left)
			}
			n.Text = text
			used += len(text)
			out = append(out, n)
		}
		spent += used
		carry = allow - used
	}
	return out
}

// clip cuts text to at most n bytes, at a rune boundary, marking the cut.
func clip(text string, n int) string {
	const mark = "…"
	if len(text) <= n {
		return text
	}
	cut := n - len(mark)
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return strings.TrimRight(text[:cut], " \n") + mark
}

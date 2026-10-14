package query

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// MaxGetLines is how many lines one get_lines call may name.
const MaxGetLines = 100

// lineWindow is the widest run of positions one store read covers.
const lineWindow = 500

// GetLinesArgs are the get_lines tool parameters: a session and the seq
// numbers of the lines to fetch (from search_transcripts, find_errors or a
// truncated read).
type GetLinesArgs struct {
	SessionID string `json:"session_id"`
	Seqs      []int  `json:"seqs"`
}

// ErrNoSeqs is returned for a get_lines call that names no line, or too
// many.
var ErrNoSeqs = fmt.Errorf("name between 1 and %d seqs", MaxGetLines)

// Lines returns the session's transcript lines with the given seqs, in seq
// order; a seq the transcript does not have is left out.
func (s *Service) Lines(ctx context.Context, args GetLinesArgs) ([]session.Doc, error) {
	if len(args.Seqs) == 0 || len(args.Seqs) > MaxGetLines {
		return nil, ErrNoSeqs
	}
	if args.SessionID == "" {
		return nil, errors.New("session_id is required")
	}
	if _, err := s.Store.GetSession(ctx, s.AccountID, args.SessionID); err != nil {
		return nil, fmt.Errorf("session %s: %w", args.SessionID, err)
	}
	seqs := slices.Clone(args.Seqs)
	slices.Sort(seqs)
	seqs = slices.Compact(seqs)
	want := map[int]bool{}
	for _, q := range seqs {
		want[q] = true
	}
	out := []session.Doc{}
	// A line's position is its seq when the transcript is stored whole, so
	// each window of nearby seqs is one read.
	for i := 0; i < len(seqs); {
		start := max(seqs[i], 0)
		j := i
		for j < len(seqs) && seqs[j]-start < lineWindow {
			j++
		}
		lines, err := s.Store.TranscriptLines(ctx, session.LineQuery{
			AccountID: s.AccountID, SessionID: args.SessionID, Offset: start, Limit: seqs[j-1] - start + 1,
		})
		if err != nil {
			return nil, err
		}
		for _, l := range lines {
			if want[toInt(l["seq"])] {
				out = append(out, l)
			}
		}
		i = j
	}
	return out, nil
}

package query

import (
	"context"
	"strings"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

const (
	defaultSearchLimit = 20
	searchSnippetChars = 240
)

// SearchTranscriptsArgs are the search_transcripts tool parameters. Query
// holds words (any of which matches), "quoted phrases" (all of which must
// appear) and -excluded words.
type SearchTranscriptsArgs struct {
	Query       string `json:"query"`
	SessionID   string `json:"session_id,omitempty"`
	GitOrigin   string `json:"git_origin,omitempty"`
	ProjectPath string `json:"project_path,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

// TranscriptHit is a transcript line that matched a search: where it is,
// how well it matched (higher is better) and the text around the match.
type TranscriptHit struct {
	SessionID   string    `json:"session_id"`
	Seq         int       `json:"seq"`
	Score       float64   `json:"score"`
	Snippet     string    `json:"snippet"`
	ProjectPath *string   `json:"project_path"`
	GitOrigin   *string   `json:"git_origin"`
	CreatedAt   time.Time `json:"created_at"`
}

// SearchTranscripts ranks the transcript lines of the scope by the words of
// the query, best first. Lines are searchable once the search-text enricher
// has indexed them (a few seconds after capture; older transcripts are
// indexed by the enrichment loop's later passes). A session_id that is not
// the account's yields no hits; read_transcript with the hit's seq as
// offset shows the conversation around it.
func (s *Service) SearchTranscripts(ctx context.Context, args SearchTranscriptsArgs) ([]TranscriptHit, error) {
	if strings.TrimSpace(args.Query) == "" {
		return []TranscriptHit{}, nil
	}
	sessionIDs, scoped, err := s.scope(ctx, args.SessionID, args.GitOrigin, args.ProjectPath)
	if err != nil {
		return nil, err
	}
	if scoped && len(sessionIDs) == 0 {
		return []TranscriptHit{}, nil
	}
	lines, err := s.Store.SearchTranscripts(ctx, session.TextQuery{AccountID: s.AccountID, Query: args.Query, SessionIDs: sessionIDs, Limit: withDefault(args.Limit, defaultSearchLimit)})
	if err != nil {
		return nil, err
	}
	terms := queryTerms(args.Query)
	sessions := map[string]session.Doc{}
	hits := make([]TranscriptHit, 0, len(lines))
	for _, l := range lines {
		id, _ := l.String("session_id")
		if _, known := sessions[id]; !known {
			if sessions[id], err = s.sessionOrNil(ctx, id); err != nil {
				return nil, err
			}
		}
		text, _ := l.Map("enrichments")["search-text"].(string)
		score, _ := l["score"].(float64)
		createdAt, _ := l["created_at"].(time.Time)
		hits = append(hits, TranscriptHit{
			SessionID: id, Seq: toInt(l["seq"]), Score: score,
			Snippet:     session.Snippet(text, terms, searchSnippetChars),
			ProjectPath: stringPtr(sessions[id], "project_path"), GitOrigin: stringPtr(sessions[id], "git_origin"),
			CreatedAt: createdAt,
		})
	}
	return hits, nil
}

// queryTerms are the positive words and phrases of a text query, for
// placing the snippet.
func queryTerms(q string) []string {
	var terms []string
	for _, part := range strings.Split(q, `"`) {
		for _, f := range strings.Fields(part) {
			if !strings.HasPrefix(f, "-") {
				terms = append(terms, f)
			}
		}
	}
	return terms
}

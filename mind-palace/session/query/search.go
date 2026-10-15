package query

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

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

// MaxSemanticQueryChars caps the query SemanticSearch embeds.
const MaxSemanticQueryChars = 1000

// ErrNoEmbedder is SemanticSearch's error when the service has no Embedder.
var ErrNoEmbedder = errors.New("semantic search is not available: no embedder is configured")

// SemanticSearchArgs are the search_semantic tool parameters. Since is an
// RFC 3339 time or a duration back from now ("720h"); Limit defaults to
// 20 and is capped at 100.
type SemanticSearchArgs struct {
	Query       string `json:"query"`
	SessionID   string `json:"session_id,omitempty"`
	GitOrigin   string `json:"git_origin,omitempty"`
	ProjectPath string `json:"project_path,omitempty"`
	Since       string `json:"since,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

// SemanticSearch ranks the prompts and replies of the scope by how close
// their meaning is to the query's, best first: the query (cut to
// MaxSemanticQueryChars characters) is embedded as a query and compared
// with the vectors the embedding enricher stored under the embedder's
// model. Score is the cosine similarity. A session_id that is not the
// account's yields no hits.
func (s *Service) SemanticSearch(ctx context.Context, args SemanticSearchArgs) ([]TranscriptHit, error) {
	if s.Embedder == nil {
		return nil, ErrNoEmbedder
	}
	text := strings.TrimSpace(args.Query)
	if text == "" {
		return []TranscriptHit{}, nil
	}
	if utf8.RuneCountInString(text) > MaxSemanticQueryChars {
		text = string([]rune(text)[:MaxSemanticQueryChars])
	}
	since, err := parseSince(args.Since, s.now())
	if err != nil {
		return nil, err
	}
	sessionIDs, scoped, err := s.scope(ctx, args.SessionID, args.GitOrigin, args.ProjectPath)
	if err != nil {
		return nil, err
	}
	if scoped && len(sessionIDs) == 0 {
		return []TranscriptHit{}, nil
	}
	vecs, err := s.Embedder.Embed(ctx, []string{text}, session.EmbedQuery)
	if err != nil {
		return nil, err
	}
	if len(vecs) != 1 {
		return nil, errors.New("semantic search: the embedder returned no vector")
	}
	lines, err := s.Store.SearchVectors(ctx, session.VectorQuery{
		AccountID: s.AccountID, Model: s.Embedder.Model(), Vector: vecs[0],
		SessionIDs: sessionIDs, Since: since, Limit: withDefault(args.Limit, defaultSearchLimit),
	})
	if err != nil {
		return nil, err
	}
	sessions := map[string]session.Doc{}
	hits := make([]TranscriptHit, 0, len(lines))
	for _, l := range lines {
		id, _ := l.String("session_id")
		if _, known := sessions[id]; !known {
			if sessions[id], err = s.sessionOrNil(ctx, id); err != nil {
				return nil, err
			}
		}
		snippet, _ := l.Map("enrichments")["search-text"].(string)
		score, _ := l["score"].(float64)
		createdAt, _ := l["created_at"].(time.Time)
		hits = append(hits, TranscriptHit{
			SessionID: id, Seq: toInt(l["seq"]), Score: score,
			Snippet:     session.Snippet(snippet, nil, searchSnippetChars),
			ProjectPath: stringPtr(sessions[id], "project_path"), GitOrigin: stringPtr(sessions[id], "git_origin"),
			CreatedAt: createdAt,
		})
	}
	return hits, nil
}

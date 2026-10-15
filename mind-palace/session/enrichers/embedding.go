package enrichers

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// Limits of what the embedding enricher sends.
const (
	// EmbedMinChars is the shortest text embedded: shorter prompts and
	// replies ("ok", "yes, go ahead") carry no meaning worth a vector.
	EmbedMinChars = 24
	// embedTextLimit caps the bytes of one text sent to the embedder.
	embedTextLimit = 8 << 10
	// embedBatchLimit is the number of lines one pass embeds.
	embedBatchLimit = 64
)

// EmbedMeter is called once per account in a batch, before Embed, with the
// number of lines about to be embedded for it; an error skips those lines
// (session.ErrSkip: they leave the queue and nothing is sent).
type EmbedMeter func(ctx context.Context, accountID string, lines int) error

// EmbedUsage receives the tokens the embedder counted for an account's
// lines, when the embedder is a session.TokenCountingEmbedder.
type EmbedUsage func(ctx context.Context, accountID string, tokens int)

// Embedding is the embedding enricher over e, named session.EmbeddingName,
// on transcript_lines, disabled by default (an account opts in: the text
// goes to the embedder's provider), 64 lines per pass. It embeds prompts
// and replies only: the text of user and assistant messages (a string, or
// their text blocks; tool calls, tool results, thinking and meta lines
// are left out) of at least EmbedMinChars characters, with credentials
// masked as search-text masks them and cut to about 8 KB. The result is a
// session.Embedding (int8 plus scale). meter, when not nil, is asked per
// account before anything is sent.
func Embedding(e session.Embedder, meter EmbedMeter) session.Enricher {
	return EmbeddingWithUsage(e, meter, nil)
}

// EmbeddingWithUsage is Embedding that also reports each account's tokens
// to usage (nil for none).
func EmbeddingWithUsage(e session.Embedder, meter EmbedMeter, usage EmbedUsage) session.Enricher {
	return session.Enricher{
		Name:       session.EmbeddingName,
		Collection: "transcript_lines",
		Enabled:    false,
		BatchLimit: embedBatchLimit,
		Matches: func(doc session.Doc) bool {
			text, ok := EmbedText(doc)
			return ok && utf8.RuneCountInString(text) >= EmbedMinChars
		},
		EnrichBatch: func(ctx context.Context, docs []session.Doc, _ session.Lookup) ([]any, []error) {
			return embedBatch(ctx, e, meter, usage, docs)
		},
	}
}

// embedBatch embeds the docs one account at a time, in the batch's order of
// accounts, so the meter and the usage see each account's own lines.
func embedBatch(ctx context.Context, e session.Embedder, meter EmbedMeter, usage EmbedUsage, docs []session.Doc) ([]any, []error) {
	results, errs := make([]any, len(docs)), make([]error, len(docs))
	groups := map[string][]int{}
	var accounts []string
	for i, d := range docs {
		acct, _ := d.String("account_id")
		if _, seen := groups[acct]; !seen {
			accounts = append(accounts, acct)
		}
		groups[acct] = append(groups[acct], i)
	}
	for _, acct := range accounts {
		idx := groups[acct]
		fail := func(err error) {
			for _, i := range idx {
				errs[i] = err
			}
		}
		if meter != nil {
			if err := meter(ctx, acct, len(idx)); err != nil {
				fail(fmt.Errorf("embedding not metered: %v: %w", err, session.ErrSkip))
				continue
			}
		}
		texts := make([]string, len(idx))
		for j, i := range idx {
			text, _ := EmbedText(docs[i])
			texts[j] = truncateUTF8(redactSecrets(text), embedTextLimit)
		}
		var vecs [][]float32
		var err error
		if counting, ok := e.(session.TokenCountingEmbedder); ok && usage != nil {
			var tokens int
			if vecs, tokens, err = counting.EmbedCounted(ctx, texts, session.EmbedDocument); err == nil {
				usage(ctx, acct, tokens)
			}
		} else {
			vecs, err = e.Embed(ctx, texts, session.EmbedDocument)
		}
		if err == nil && len(vecs) != len(texts) {
			err = fmt.Errorf("%d vectors for %d texts", len(vecs), len(texts))
		}
		if err != nil {
			fail(fmt.Errorf("embedding: %w", err))
			continue
		}
		for j, i := range idx {
			if len(vecs[j]) != e.Dim() {
				errs[i] = fmt.Errorf("embedding: vector of %d numbers, want %d", len(vecs[j]), e.Dim())
				continue
			}
			results[i] = session.Quantize(e.Model(), vecs[j])
		}
	}
	return results, errs
}

// EmbedText is the text the embedding enricher embeds for a transcript
// line, and whether the line is a prompt or a reply at all: the message's
// string content, or its text blocks joined by newlines, of a user or
// assistant line that is not a meta line.
func EmbedText(doc session.Doc) (string, bool) {
	line := doc.Line()
	lineType, _ := line.String("type")
	role, _ := doc.Message().String("role")
	if (lineType != "user" && lineType != "assistant") || role != lineType {
		return "", false
	}
	if meta, _ := line["isMeta"].(bool); meta {
		return "", false
	}
	if text, ok := doc.Message().String("content"); ok {
		text = strings.TrimSpace(text)
		return text, text != ""
	}
	var parts []string
	for _, b := range doc.Content() {
		if kind, _ := b.String("type"); kind == "text" {
			if s, _ := b.String("text"); strings.TrimSpace(s) != "" {
				parts = append(parts, strings.TrimSpace(s))
			}
		}
	}
	text := strings.Join(parts, "\n")
	return text, text != ""
}

// truncateUTF8 cuts s to at most n bytes at a rune boundary.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 {
		if r, size := utf8.DecodeLastRuneInString(s); r != utf8.RuneError || size != 1 {
			break
		}
		s = s[:len(s)-1]
	}
	return s
}

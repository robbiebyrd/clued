package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// unitEmbedder embeds every text as the same vector.
type unitEmbedder struct{}

func (unitEmbedder) Model() string { return "unit" }
func (unitEmbedder) Dim() int      { return 2 }
func (unitEmbedder) Embed(_ context.Context, texts []string, _ session.InputKind) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = []float32{1, 1}
	}
	return out, nil
}

func TestCluedSemanticSearchIsOfferedAndScopedToThePrincipal(t *testing.T) {
	var asked []string
	ts, _, sessions, _ := newCluedServerWith(t, func(o *CluedOptions) {
		o.Semantic = &SemanticOptions{Embedder: unitEmbedder{}, Allow: func(_ context.Context, acc string) error {
			asked = append(asked, acc)
			if acc == "acct-star" {
				return errors.New("semantic search is a Pro feature")
			}
			return nil
		}}
	})
	ctx := context.Background()
	for _, acc := range []string{"acct-ab", "acct-star"} {
		sid := "s-" + acc
		if err := sessions.UpsertSession(ctx, session.Session{SessionID: sid, AccountID: acc, LastSeen: time.Now()}); err != nil {
			t.Fatal(err)
		}
		if err := sessions.UpsertTranscriptLines(ctx, []session.TranscriptLine{{SessionID: sid, Seq: 0, AccountID: acc, Line: map[string]any{"type": "user"}}}); err != nil {
			t.Fatal(err)
		}
		docs, _ := sessions.Unenriched(ctx, "transcript_lines", session.EmbeddingName, 0)
		for _, d := range docs {
			if d["session_id"] == sid {
				_ = sessions.SetEnrichment(ctx, "transcript_lines", d["_id"], session.EmbeddingName, session.Quantize("unit", []float32{1, 1}))
			}
		}
	}
	sa, err := connectMCP(t, ts.URL+PathMCP, tokenAB, "a")
	if err != nil {
		t.Fatal(err)
	}
	res, text := toolCall(t, sa, "search_semantic", map[string]any{"query": "anything at all"})
	if res.IsError || !strings.Contains(text, `"s-acct-ab"`) || strings.Contains(text, "s-acct-star") {
		t.Fatalf("search_semantic as acct-ab = %s", text)
	}
	ss, err := connectMCP(t, ts.URL+PathMCP, tokenStar, "a")
	if err != nil {
		t.Fatal(err)
	}
	if res, text := toolCall(t, ss, "search_semantic", map[string]any{"query": "anything at all"}); !res.IsError || text != "semantic search is a Pro feature" {
		t.Fatalf("search_semantic as acct-star = %v %s", res.IsError, text)
	}
	if len(asked) != 2 || asked[0] != "acct-ab" || asked[1] != "acct-star" {
		t.Fatalf("Allow asked %v", asked)
	}
}

func TestCluedWithoutSemanticOffersNoSemanticTool(t *testing.T) {
	ts, _, _, _ := newCluedServer(t)
	sa, err := connectMCP(t, ts.URL+PathMCP, tokenAB, "a")
	if err != nil {
		t.Fatal(err)
	}
	tools, err := sa.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools.Tools {
		if tl.Name == "search_semantic" {
			t.Fatal("search_semantic offered without CluedOptions.Semantic")
		}
	}
}

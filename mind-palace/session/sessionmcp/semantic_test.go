package sessionmcp_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/sessionmcp"
)

// topicEmbedder maps a text to a 2-number vector: OAuth texts point one
// way, everything else the other.
type topicEmbedder struct{ kinds []session.InputKind }

func (*topicEmbedder) Model() string { return "topic-1" }
func (*topicEmbedder) Dim() int      { return 2 }
func (e *topicEmbedder) Embed(_ context.Context, texts []string, kind session.InputKind) ([][]float32, error) {
	e.kinds = append(e.kinds, kind)
	out := make([][]float32, len(texts))
	for i, s := range texts {
		if strings.Contains(strings.ToLower(s), "login") || strings.Contains(strings.ToLower(s), "oauth") {
			out[i] = []float32{1, 0.1}
		} else {
			out[i] = []float32{0.1, 1}
		}
	}
	return out, nil
}

// embedAll gives every seeded line a search text and its embedding: the
// even lines of sess-3 and the foreign session's line are about OAuth.
func embedAll(t *testing.T, st session.Store, e session.Embedder) {
	t.Helper()
	ctx := context.Background()
	for i, d := range must2(st.Unenriched(ctx, "transcript_lines", "search-text", 0)) {
		text := "routine output from the build tool"
		if (d["session_id"] == "sess-3" && i%2 == 0) || d["session_id"] == "sess-acct-2" {
			text = "we reworked how users sign in with OAuth"
		}
		must(t, st.SetEnrichment(ctx, "transcript_lines", d["_id"], "search-text", text))
		vec := must2(e.Embed(ctx, []string{text}, session.EmbedDocument))[0]
		must(t, st.SetEnrichment(ctx, "transcript_lines", d["_id"], session.EmbeddingName, session.Quantize(e.Model(), vec)))
	}
}

func TestSearchSemanticAbsentWithoutAnEmbedder(t *testing.T) {
	eachStore(t, func(t *testing.T) {
		tools := newTools(seeded(t), session.Config{})
		for _, remote := range []bool{false, true} {
			sub := tools.Subset(remote)
			if toolNames(t, connectClient(t, sub.Server(), nil))["search_semantic"] {
				t.Errorf("remote=%v: search_semantic listed without an embedder", remote)
			}
			if strings.Contains(sub.Instructions(), "search_semantic") {
				t.Errorf("remote=%v: instructions mention search_semantic", remote)
			}
		}
		tools.Embedder = &topicEmbedder{}
		for _, remote := range []bool{false, true} {
			sub := tools.Subset(remote)
			if !toolNames(t, connectClient(t, sub.Server(), nil))["search_semantic"] {
				t.Errorf("remote=%v: search_semantic missing with an embedder", remote)
			}
			if !strings.HasSuffix(sub.Instructions(), sessionmcp.SemanticInstructions) {
				t.Errorf("remote=%v: instructions = %q", remote, sub.Instructions())
			}
		}
	})
}

func TestSearchSemanticAllowErrorIsTheToolError(t *testing.T) {
	eachStore(t, func(t *testing.T) {
		emb := &topicEmbedder{}
		tools := newTools(seeded(t), session.Config{})
		tools.Embedder = emb
		var asked []string
		tools.AllowSemantic = func(_ context.Context, acc string) error {
			asked = append(asked, acc)
			return errors.New("semantic search needs Clued Cloud Pro")
		}
		cs := connectClient(t, tools.Server(), nil)
		wantError(t, call(t, cs, "search_semantic", obj{"query": "how do people log in"}), "semantic search needs Clued Cloud Pro")
		if len(asked) != 1 || asked[0] != account {
			t.Errorf("Allow asked for %v", asked)
		}
		if len(emb.kinds) != 0 {
			t.Error("the query was embedded although Allow refused")
		}
	})
}

func TestSearchSemanticHitsOnlyTheCallersAccount(t *testing.T) {
	eachStore(t, func(t *testing.T) {
		st := seeded(t)
		emb := &topicEmbedder{}
		embedAll(t, st, emb)
		tools := newTools(st, session.Config{})
		tools.Embedder = emb
		tools.AllowSemantic = func(context.Context, string) error { return nil }
		emb.kinds = nil
		cs := connectClient(t, tools.Server(), nil)
		hits := okJSON[[]obj](t, call(t, cs, "search_semantic", obj{"query": "how does the OAuth login work", "limit": 5}))
		if len(hits) != 5 {
			t.Fatalf("search_semantic = %v", hits)
		}
		for _, h := range hits {
			if h["session_id"] != "sess-3" || !strings.Contains(h["snippet"].(string), "OAuth") || h["git_origin"] != "https://github.com/user/myrepo.git" {
				t.Errorf("hit = %v", h)
			}
		}
		if len(emb.kinds) != 1 || emb.kinds[0] != session.EmbedQuery {
			t.Errorf("query embedded as %v, want one query", emb.kinds)
		}
		if got := text(t, call(t, cs, "search_semantic", obj{"query": "oauth", "session_id": "sess-acct-2"})); got != "[]" {
			t.Errorf("another account's session: %s", got)
		}
		all := okJSON[[]obj](t, call(t, cs, "search_semantic", obj{"query": "oauth sign in", "limit": 100, "max_tokens": 1000000}))
		for _, h := range all {
			if h["session_id"] == "sess-acct-2" {
				t.Fatalf("a foreign line was returned: %v", h)
			}
		}
		if got := text(t, call(t, cs, "search_semantic", obj{"query": "  "})); got != "[]" {
			t.Errorf("blank query: %s", got)
		}
		res := call(t, cs, "search_semantic", obj{"query": "oauth", "since": "yesterday"})
		if !res.IsError {
			t.Error("an invalid since must be an error")
		}
	})
}

package enrichers

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/enrich"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
)

// fakeEmbedder records what it is sent and returns a 3-number vector per
// text: its length, a 1 and a 0.
type fakeEmbedder struct {
	mu    sync.Mutex
	calls [][]string
}

func (*fakeEmbedder) Model() string { return "fake-1" }
func (*fakeEmbedder) Dim() int      { return 3 }
func (f *fakeEmbedder) Embed(_ context.Context, texts []string, kind session.InputKind) ([][]float32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if kind != session.EmbedDocument {
		return nil, errors.New("lines are documents")
	}
	f.calls = append(f.calls, append([]string(nil), texts...))
	out := make([][]float32, len(texts))
	for i, s := range texts {
		out[i] = []float32{float32(len(s)), 1, 0}
	}
	return out, nil
}

func (f *fakeEmbedder) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var all []string
	for _, c := range f.calls {
		all = append(all, c...)
	}
	return all
}

const longPrompt = "please refactor the session store so it pages"

func line(typ string, message map[string]any) map[string]any {
	return map[string]any{"type": typ, "message": message}
}

func seedLines(t *testing.T, st session.Store, acct, sid string, lines ...map[string]any) {
	t.Helper()
	tl := make([]session.TranscriptLine, len(lines))
	for i, l := range lines {
		tl[i] = session.TranscriptLine{SessionID: sid, Seq: i, AccountID: acct, Line: l}
	}
	if err := st.UpsertTranscriptLines(context.Background(), tl); err != nil {
		t.Fatal(err)
	}
}

func embeddings(t *testing.T, st session.Store, acct, sid string) []session.Doc {
	t.Helper()
	lines, err := st.TranscriptLines(context.Background(), session.LineQuery{AccountID: acct, SessionID: sid})
	if err != nil {
		t.Fatal(err)
	}
	out := make([]session.Doc, len(lines))
	for i, l := range lines {
		out[i] = l.Map("enrichments")
	}
	return out
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)) }

func TestEmbeddingMatchesPromptsAndReplies(t *testing.T) {
	e := Embedding(&fakeEmbedder{}, nil)
	if e.Name != "embedding" || e.Collection != "transcript_lines" || e.Enabled || e.BatchLimit != 64 || e.EnrichBatch == nil {
		t.Fatalf("enricher = %+v", e)
	}
	doc := func(l map[string]any) session.Doc { return session.Doc{"line": l} }
	cases := []struct {
		name string
		line map[string]any
		want bool
	}{
		{"typed prompt", line("user", map[string]any{"role": "user", "content": longPrompt}), true},
		{"reply text block", line("assistant", map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "I paged the store with a cursor over last_seen."}}}), true},
		{"short prompt", line("user", map[string]any{"role": "user", "content": "yes, go ahead"}), false},
		{"tool result", line("user", map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "content": strings.Repeat("output ", 20)}}}), false},
		{"tool call", line("assistant", map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "name": "Bash", "input": map[string]any{"command": strings.Repeat("ls ", 20)}}}}), false},
		{"thinking", line("assistant", map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "thinking", "thinking": strings.Repeat("hmm ", 20)}}}), false},
		{"meta line", map[string]any{"type": "user", "isMeta": true, "message": map[string]any{"role": "user", "content": longPrompt}}, false},
		{"system line", map[string]any{"type": "system", "content": longPrompt}, false},
	}
	for _, c := range cases {
		if got := e.Matches(doc(c.line)); got != c.want {
			t.Errorf("%s: Matches = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestEmbeddingInputRedacted(t *testing.T) {
	st := memsession.New()
	fake := &fakeEmbedder{}
	secret := "sk-ant-api03-" + strings.Repeat("x", 40)
	seedLines(t, st, "acct", "s1",
		line("user", map[string]any{"role": "user", "content": "use the key " + secret + " for the deploy"}),
		line("user", map[string]any{"role": "user", "content": strings.Repeat("é", 6000)}))
	if err := enrich.Pass(context.Background(), st, []session.Enricher{Embedding(fake, nil)}, quiet()); err != nil {
		t.Fatal(err)
	}
	sent := fake.sent()
	if len(sent) != 2 {
		t.Fatalf("sent %d texts", len(sent))
	}
	if strings.Contains(sent[0], "sk-ant-") || !strings.Contains(sent[0], "[REDACTED:api-key]") {
		t.Errorf("secret reached the embedder: %q", sent[0])
	}
	if len(sent[1]) > 8<<10 || !strings.HasPrefix(sent[1], "é") || strings.ContainsRune(sent[1], '�') {
		t.Errorf("long text cut to %d bytes, valid=%v", len(sent[1]), !strings.ContainsRune(sent[1], '�'))
	}
	for _, enr := range embeddings(t, st, "acct", "s1") {
		emb := enr.Map("embedding")
		if emb["model"] != "fake-1" || emb["dim"] != 3 || len(emb["v"].([]int8)) != 3 {
			t.Errorf("stored embedding = %v", emb)
		}
	}
}

func TestEmbeddingMeterPerAccount(t *testing.T) {
	st := memsession.New()
	fake := &fakeEmbedder{}
	seedLines(t, st, "a", "s-a", line("user", map[string]any{"role": "user", "content": longPrompt}), line("user", map[string]any{"role": "user", "content": longPrompt + " too"}))
	seedLines(t, st, "b", "s-b", line("user", map[string]any{"role": "user", "content": longPrompt}), line("user", map[string]any{"role": "user", "content": "short"}))
	metered := map[string]int{}
	calls := 0
	meter := func(_ context.Context, acct string, lines int) error {
		calls++
		metered[acct] += lines
		if len(fake.sent()) != 0 && calls == 1 {
			t.Error("meter ran after Embed")
		}
		return nil
	}
	tokens := map[string]int{}
	counting := &countingEmbedder{fakeEmbedder: fake}
	usage := func(_ context.Context, acct string, n int) { tokens[acct] += n }
	if err := enrich.Pass(context.Background(), st, []session.Enricher{EmbeddingWithUsage(counting, meter, usage)}, quiet()); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || metered["a"] != 2 || metered["b"] != 1 {
		t.Fatalf("meter calls = %d, lines = %v", calls, metered)
	}
	if len(fake.calls) != 2 {
		t.Fatalf("Embed calls = %d, want one per account", len(fake.calls))
	}
	if tokens["a"] != 2*7 || tokens["b"] != 7 {
		t.Fatalf("usage = %v", tokens)
	}
}

// countingEmbedder reports 7 tokens per text.
type countingEmbedder struct{ *fakeEmbedder }

func (c *countingEmbedder) EmbedCounted(ctx context.Context, texts []string, kind session.InputKind) ([][]float32, int, error) {
	v, err := c.Embed(ctx, texts, kind)
	return v, 7 * len(texts), err
}

func TestEmbeddingMeterRefusalSkips(t *testing.T) {
	st := memsession.New()
	fake := &fakeEmbedder{}
	seedLines(t, st, "paused", "s-p", line("user", map[string]any{"role": "user", "content": longPrompt}))
	seedLines(t, st, "ok", "s-o", line("user", map[string]any{"role": "user", "content": longPrompt}))
	meter := func(_ context.Context, acct string, _ int) error {
		if acct == "paused" {
			return errors.New("allowance used")
		}
		return nil
	}
	if err := enrich.Pass(context.Background(), st, []session.Enricher{Embedding(fake, meter)}, quiet()); err != nil {
		t.Fatal(err)
	}
	if p := embeddings(t, st, "paused", "s-p")[0]; p["embedding_skipped"] != true || p["embedding"] != nil || p["embedding_failed"] != nil {
		t.Errorf("refused account's line = %v, want skipped", p)
	}
	if o := embeddings(t, st, "ok", "s-o")[0]; o.Map("embedding") == nil {
		t.Errorf("other account's line not embedded: %v", o)
	}
	if sent := fake.sent(); len(sent) != 1 {
		t.Errorf("sent %v, want only the allowed account's line", sent)
	}
}

func TestEmbeddingFailureIsRecorded(t *testing.T) {
	st := memsession.New()
	seedLines(t, st, "a", "s", line("user", map[string]any{"role": "user", "content": longPrompt}))
	if err := enrich.Pass(context.Background(), st, []session.Enricher{Embedding(brokenEmbedder{}, nil)}, quiet()); err != nil {
		t.Fatal(err)
	}
	if msg, _ := embeddings(t, st, "a", "s")[0].Map("embedding_failed").String("message"); !strings.Contains(msg, "provider down") {
		t.Errorf("failure = %q", msg)
	}
}

type brokenEmbedder struct{}

func (brokenEmbedder) Model() string { return "b" }
func (brokenEmbedder) Dim() int      { return 3 }
func (brokenEmbedder) Embed(context.Context, []string, session.InputKind) ([][]float32, error) {
	return nil, errors.New("provider down")
}

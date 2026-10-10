package enrichers

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// requireTranscriptEnricher asserts the registration metadata every
// transcript-line enricher in this group shares.
func requireTranscriptEnricher(t *testing.T, e session.Enricher, name string) {
	t.Helper()
	if e.Name != name {
		t.Errorf("Name = %q, want %q", e.Name, name)
	}
	if e.Collection != "transcript_lines" {
		t.Errorf("Collection = %q, want transcript_lines", e.Collection)
	}
	if !e.Enabled {
		t.Error("Enabled = false, want true")
	}
}

// marshalEnrichment runs the enricher on doc and returns the result as JSON,
// which pins both the values and the stored field names and nulls.
func marshalEnrichment(t *testing.T, e session.Enricher, doc session.Doc) string {
	t.Helper()
	got, err := e.Enrich(context.Background(), doc, nil)
	if err != nil {
		t.Fatalf("Enrich returned error: %v", err)
	}
	out, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	return string(out)
}

// requireBSONTagsMirrorJSON asserts every field of the result type carries the
// same snake_case name in its json and bson tags.
func requireBSONTagsMirrorJSON(t *testing.T, result any) {
	t.Helper()
	typ := reflect.TypeOf(result)
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		j, b := f.Tag.Get("json"), f.Tag.Get("bson")
		if j == "" || j != b {
			t.Errorf("%s.%s: json tag %q, bson tag %q", typ.Name(), f.Name, j, b)
		}
	}
}

func TestAttachmentExtractorNameAndCollection(t *testing.T) {
	requireTranscriptEnricher(t, AttachmentExtractor, "attachment-extractor")
}

func TestAttachmentExtractorMatchesAttachmentLineTypeOnly(t *testing.T) {
	cases := []struct {
		doc  session.Doc
		want bool
	}{
		{session.Doc{"line": map[string]any{"type": "attachment"}}, true},
		{session.Doc{"line": map[string]any{"type": "assistant"}}, false},
		{session.Doc{"line": map[string]any{"type": "user"}}, false},
		{session.Doc{}, false},
	}
	for _, c := range cases {
		if got := AttachmentExtractor.Matches(c.doc); got != c.want {
			t.Errorf("Matches(%v) = %v, want %v", c.doc, got, c.want)
		}
	}
}

func TestAttachmentExtractorExtractsFilePathAndLanguage(t *testing.T) {
	got := marshalEnrichment(t, AttachmentExtractor, session.Doc{"line": map[string]any{
		"type":       "attachment",
		"attachment": map[string]any{"file_path": "src/util.ts", "content": "export const x = 1;"},
	}})
	want := `{"file_path":"src/util.ts","language":"typescript","size_chars":19}`
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestAttachmentExtractorLanguageIsNullForUnknownExtension(t *testing.T) {
	got := marshalEnrichment(t, AttachmentExtractor, session.Doc{"line": map[string]any{
		"type":       "attachment",
		"attachment": map[string]any{"file_path": "data.parquet", "content": "binary"},
	}})
	want := `{"file_path":"data.parquet","language":null,"size_chars":6}`
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestAttachmentExtractorFilePathIsNullWhenAbsent(t *testing.T) {
	got := marshalEnrichment(t, AttachmentExtractor, session.Doc{"line": map[string]any{
		"type":       "attachment",
		"attachment": map[string]any{"content": "some text"},
	}})
	want := `{"file_path":null,"language":null,"size_chars":9}`
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestAttachmentExtractorSizeCharsIsNullWhenContentAbsent(t *testing.T) {
	got := marshalEnrichment(t, AttachmentExtractor, session.Doc{"line": map[string]any{
		"type":       "attachment",
		"attachment": map[string]any{"file_path": "foo.ts"},
	}})
	want := `{"file_path":"foo.ts","language":"typescript","size_chars":null}`
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestAttachmentExtractorAllNullsForEmptyAttachment(t *testing.T) {
	got := marshalEnrichment(t, AttachmentExtractor, session.Doc{"line": map[string]any{
		"type":       "attachment",
		"attachment": map[string]any{},
	}})
	want := `{"file_path":null,"language":null,"size_chars":null}`
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// size_chars is JavaScript's String.length, which counts UTF-16 code units.
func TestAttachmentExtractorSizeCharsCountsUTF16CodeUnits(t *testing.T) {
	got := marshalEnrichment(t, AttachmentExtractor, session.Doc{"line": map[string]any{
		"type":       "attachment",
		"attachment": map[string]any{"content": "a\U0001F600é" + strings.Repeat("b", 2)},
	}})
	want := `{"file_path":null,"language":null,"size_chars":6}`
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestAttachmentExtractorResultTagsMatch(t *testing.T) {
	requireBSONTagsMirrorJSON(t, AttachmentExtraction{})
}

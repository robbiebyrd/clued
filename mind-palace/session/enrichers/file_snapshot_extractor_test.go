package enrichers

import (
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

func snapshotDoc(backups map[string]any) session.Doc {
	return session.Doc{"line": map[string]any{
		"type":     "file-history-snapshot",
		"snapshot": map[string]any{"trackedFileBackups": backups},
	}}
}

func TestFileSnapshotExtractorNameAndCollection(t *testing.T) {
	requireTranscriptEnricher(t, FileSnapshotExtractor, "file-snapshot-extractor")
}

func TestFileSnapshotExtractorMatchesSnapshotWithNonEmptyBackups(t *testing.T) {
	if !FileSnapshotExtractor.Matches(snapshotDoc(map[string]any{"src/a.ts": map[string]any{}})) {
		t.Error("Matches = false, want true")
	}
}

func TestFileSnapshotExtractorDoesNotMatchWhenBackupsEmpty(t *testing.T) {
	if FileSnapshotExtractor.Matches(snapshotDoc(map[string]any{})) {
		t.Error("Matches = true, want false")
	}
}

func TestFileSnapshotExtractorDoesNotMatchWrongLineType(t *testing.T) {
	for _, doc := range []session.Doc{
		{"line": map[string]any{"type": "assistant"}},
		{"line": map[string]any{"type": "user"}},
		{},
	} {
		if FileSnapshotExtractor.Matches(doc) {
			t.Errorf("Matches(%v) = true, want false", doc)
		}
	}
}

func TestFileSnapshotExtractorDoesNotMatchWhenSnapshotMissing(t *testing.T) {
	if FileSnapshotExtractor.Matches(session.Doc{"line": map[string]any{"type": "file-history-snapshot"}}) {
		t.Error("Matches = true, want false")
	}
}

func TestFileSnapshotExtractorExtractsFilesFromBackupKeys(t *testing.T) {
	got := marshalEnrichment(t, FileSnapshotExtractor, snapshotDoc(map[string]any{
		"src/app.ts":  map[string]any{"content": "..."},
		"src/util.ts": map[string]any{"content": "..."},
	}))
	want := `{"files":["src/app.ts","src/util.ts"],"languages":["typescript"],"file_count":2}`
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestFileSnapshotExtractorDetectsLanguagesFromFileExtensions(t *testing.T) {
	got := marshalEnrichment(t, FileSnapshotExtractor, snapshotDoc(map[string]any{
		"src/app.ts":  map[string]any{},
		"src/util.ts": map[string]any{},
		"README.md":   map[string]any{},
	}))
	want := `{"files":["README.md","src/app.ts","src/util.ts"],"languages":["markdown","typescript"],"file_count":3}`
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestFileSnapshotExtractorDeduplicatesLanguages(t *testing.T) {
	got := marshalEnrichment(t, FileSnapshotExtractor, snapshotDoc(map[string]any{
		"a.ts": map[string]any{}, "b.ts": map[string]any{}, "c.ts": map[string]any{},
	}))
	want := `{"files":["a.ts","b.ts","c.ts"],"languages":["typescript"],"file_count":3}`
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestFileSnapshotExtractorUnknownExtensionProducesNoLanguageEntry(t *testing.T) {
	got := marshalEnrichment(t, FileSnapshotExtractor, snapshotDoc(map[string]any{"data.parquet": map[string]any{}}))
	want := `{"files":["data.parquet"],"languages":[],"file_count":1}`
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestFileSnapshotExtractorResultTagsMatch(t *testing.T) {
	requireBSONTagsMirrorJSON(t, FileSnapshot{})
}

package enrichers

import (
	"context"
	"sort"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// FileSnapshot is the stored result of the file-snapshot-extractor enricher.
type FileSnapshot struct {
	Files     []string `json:"files" bson:"files"`
	Languages []string `json:"languages" bson:"languages"`
	FileCount int      `json:"file_count" bson:"file_count"`
}

func trackedFileBackups(doc session.Doc) session.Doc {
	return doc.Line().Map("snapshot").Map("trackedFileBackups")
}

// FileSnapshotExtractor lists the files and languages of a file-history
// snapshot. Files are sorted so the result does not depend on map order.
var FileSnapshotExtractor = session.Enricher{
	Name:       "file-snapshot-extractor",
	Collection: "transcript_lines",
	Enabled:    true,
	Matches: func(doc session.Doc) bool {
		lineType, _ := doc.Line().String("type")
		return lineType == "file-history-snapshot" && len(trackedFileBackups(doc)) > 0
	},
	Enrich: func(_ context.Context, doc session.Doc, _ session.Lookup) (any, error) {
		backups := trackedFileBackups(doc)
		files := make([]string, 0, len(backups))
		for path := range backups {
			files = append(files, path)
		}
		sort.Strings(files)

		languages := []string{}
		seen := map[string]bool{}
		for _, path := range files {
			if lang := session.ExtToLang(path); lang != "" && !seen[lang] {
				seen[lang] = true
				languages = append(languages, lang)
			}
		}
		return FileSnapshot{Files: files, Languages: languages, FileCount: len(files)}, nil
	},
}

func init() { session.Register(FileSnapshotExtractor) }

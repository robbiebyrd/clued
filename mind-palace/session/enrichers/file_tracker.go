package enrichers

import (
	"context"
	"regexp"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

type fileTrackerResult struct {
	FilePath     *string `json:"file_path" bson:"file_path"`
	Language     *string `json:"language" bson:"language"`
	Operation    string  `json:"operation" bson:"operation"`
	ArtifactType *string `json:"artifact_type" bson:"artifact_type"`
}

var fileOperations = map[string]string{
	"Read": "read", "Write": "write", "Edit": "edit", "Glob": "glob", "LS": "ls",
}

// FileTracker records the file each file-tool call targets, with its language
// and what kind of artifact it is.
var FileTracker = session.Enricher{
	Name:       "file-tracker",
	Collection: "hook_events",
	Enabled:    true,
	Matches: func(doc session.Doc) bool {
		event, _ := doc.String("hook_event_name")
		tool, _ := doc.String("tool_name")
		_, isFileTool := fileOperations[tool]
		return event == "PreToolUse" && isFileTool
	},
	Enrich: func(_ context.Context, doc session.Doc, _ session.Lookup) (any, error) {
		tool, _ := doc.String("tool_name")
		result := fileTrackerResult{Operation: fileOperations[tool]}
		input := doc.ToolInput()
		target := input["file_path"]
		if target == nil {
			target = input["path"]
		}
		if path, ok := target.(string); ok {
			result.FilePath = &path
			result.Language = nonEmpty(session.ExtToLang(path))
			result.ArtifactType = nonEmpty(detectArtifact(path))
		}
		return result, nil
	},
}

func init() { session.Register(FileTracker) }

var (
	testDirSegment   = regexp.MustCompile(`/tests?/`)
	specDirSegment   = regexp.MustCompile(`/specs?/`)
	planDirSegment   = regexp.MustCompile(`/plans?/`)
	testFilePattern  = regexp.MustCompile(`\.(test|spec)\.[^.]+$`)
	specFilename     = regexp.MustCompile(`\b(spec|design|architecture)\b`)
	planFilename     = regexp.MustCompile(`\b(plan|roadmap|todo)\b`)
	configExtensions = map[string]bool{".json": true, ".yaml": true, ".yml": true, ".toml": true, ".env": true}
	sourceExtensions = map[string]bool{
		".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".mjs": true, ".cjs": true,
		".py": true, ".rb": true, ".go": true, ".rs": true, ".java": true, ".c": true, ".cpp": true, ".cs": true,
		".sh": true, ".bash": true, ".html": true, ".css": true, ".scss": true, ".vue": true, ".svelte": true,
		".sql": true, ".graphql": true,
	}
)

// detectArtifact classifies a path as plan, spec, doc, test, config or code;
// "" when it is none of them. Directory context beats filename keywords.
func detectArtifact(path string) string {
	if path == "" {
		return ""
	}
	lower := hookEventLower(path)
	base := lower[strings.LastIndex(lower, "/")+1:]
	ext := ""
	if dot := strings.LastIndex(base, "."); dot >= 0 {
		ext = base[dot:]
	}

	switch {
	case testDirSegment.MatchString(lower):
		return "test"
	case specDirSegment.MatchString(lower):
		return "spec"
	case planDirSegment.MatchString(lower):
		return "plan"
	case testFilePattern.MatchString(lower):
		return "test"
	case specFilename.MatchString(base):
		return "spec"
	case planFilename.MatchString(base):
		return "plan"
	case ext == ".md" || ext == ".mdx":
		return "doc"
	case configExtensions[ext]:
		return "config"
	case sourceExtensions[ext]:
		return "code"
	}
	return ""
}

// nonEmpty returns a pointer to s, or nil (stored as null) when s is empty.
func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

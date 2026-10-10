package enrichers

import (
	"context"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

func fileTrackerOf(t *testing.T, tool string, input map[string]any) fileTrackerResult {
	t.Helper()
	got, err := FileTracker.Enrich(context.Background(), session.Doc{
		"hook_event_name": "PreToolUse",
		"tool_name":       tool,
		"tool_input":      input,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return got.(fileTrackerResult)
}

func fileTrackerArtifact(t *testing.T, path string) *string {
	t.Helper()
	return fileTrackerOf(t, "Read", map[string]any{"file_path": path}).ArtifactType
}

func expectArtifact(t *testing.T, path, want string) {
	t.Helper()
	got := fileTrackerArtifact(t, path)
	if got == nil || *got != want {
		t.Errorf("%s: got %v, want %q", path, got, want)
	}
}

func expectNoArtifact(t *testing.T, path string) {
	t.Helper()
	if got := fileTrackerArtifact(t, path); got != nil {
		t.Errorf("%s: got %q, want null", path, *got)
	}
}

func TestFileTrackerNameAndCollection(t *testing.T) {
	if FileTracker.Name != "file-tracker" || FileTracker.Collection != "hook_events" || !FileTracker.Enabled {
		t.Errorf("unexpected metadata: %+v", FileTracker)
	}
}

func TestFileTrackerMatchesPreToolUseFileTool(t *testing.T) {
	for _, tool := range []string{"Read", "Edit", "Write", "Glob", "LS"} {
		if !FileTracker.Matches(session.Doc{"hook_event_name": "PreToolUse", "tool_name": tool}) {
			t.Errorf("%s should match", tool)
		}
	}
}

func TestFileTrackerDoesNotMatchBashOrPostToolUse(t *testing.T) {
	if FileTracker.Matches(session.Doc{"hook_event_name": "PreToolUse", "tool_name": "Bash"}) {
		t.Error("Bash should not match")
	}
	if FileTracker.Matches(session.Doc{"hook_event_name": "PostToolUse", "tool_name": "Read"}) {
		t.Error("PostToolUse should not match")
	}
	if FileTracker.Matches(session.Doc{}) {
		t.Error("empty doc should not match")
	}
}

func TestFileTrackerReadExtractsFilePathLanguageOperation(t *testing.T) {
	r := fileTrackerOf(t, "Read", map[string]any{"file_path": "/src/app.ts"})
	if r.FilePath == nil || *r.FilePath != "/src/app.ts" {
		t.Errorf("file_path: %v", r.FilePath)
	}
	if r.Language == nil || *r.Language != "typescript" {
		t.Errorf("language: %v", r.Language)
	}
	if r.Operation != "read" {
		t.Errorf("operation: %q", r.Operation)
	}
}

func TestFileTrackerWriteExtractsFilePathAndOperation(t *testing.T) {
	r := fileTrackerOf(t, "Write", map[string]any{"file_path": "/src/util.py", "content": "x"})
	if r.Operation != "write" {
		t.Errorf("operation: %q", r.Operation)
	}
	if r.Language == nil || *r.Language != "python" {
		t.Errorf("language: %v", r.Language)
	}
}

func TestFileTrackerEditExtractsOperation(t *testing.T) {
	r := fileTrackerOf(t, "Edit", map[string]any{"file_path": "/src/main.go", "old_string": "a", "new_string": "b"})
	if r.Operation != "edit" {
		t.Errorf("operation: %q", r.Operation)
	}
	if r.Language == nil || *r.Language != "go" {
		t.Errorf("language: %v", r.Language)
	}
}

func TestFileTrackerGlobHasGlobOperationAndNoLanguage(t *testing.T) {
	r := fileTrackerOf(t, "Glob", map[string]any{"pattern": "**/*.ts"})
	if r.Operation != "glob" || r.FilePath != nil || r.Language != nil {
		t.Errorf("got %+v", r)
	}
}

func TestFileTrackerLSUsesPathField(t *testing.T) {
	r := fileTrackerOf(t, "LS", map[string]any{"path": "/src/app.ts"})
	if r.Operation != "ls" || r.FilePath == nil || *r.FilePath != "/src/app.ts" {
		t.Errorf("got %+v", r)
	}
}

func TestFileTrackerFilePathWinsOverPath(t *testing.T) {
	r := fileTrackerOf(t, "Read", map[string]any{"file_path": "/a.go", "path": "/b.py"})
	if r.FilePath == nil || *r.FilePath != "/a.go" {
		t.Errorf("got %v", r.FilePath)
	}
}

func TestFileTrackerNullFilePathFallsBackToPath(t *testing.T) {
	r := fileTrackerOf(t, "Read", map[string]any{"file_path": nil, "path": "/b.py"})
	if r.FilePath == nil || *r.FilePath != "/b.py" {
		t.Errorf("got %v", r.FilePath)
	}
}

func TestFileTrackerEmptyFilePathIsKeptButHasNoLanguageOrArtifact(t *testing.T) {
	r := fileTrackerOf(t, "Read", map[string]any{"file_path": ""})
	if r.FilePath == nil || *r.FilePath != "" || r.Language != nil || r.ArtifactType != nil {
		t.Errorf("got %+v", r)
	}
}

func TestFileTrackerArtifactTypeTestWinsByPathPattern(t *testing.T) {
	expectArtifact(t, "test/foo.test.ts", "test")
}

func TestFileTrackerArtifactTypeSpecWinsByPathSegment(t *testing.T) {
	expectArtifact(t, "docs/specs/my-design.md", "spec")
}

func TestFileTrackerArtifactTypePlanWinsByPathSegmentOverDesignKeyword(t *testing.T) {
	expectArtifact(t, "docs/plans/api-design.md", "plan")
}

func TestFileTrackerArtifactTypePlanWinsByFilenameKeyword(t *testing.T) {
	expectArtifact(t, "docs/plans/2026-01-01-roadmap.md", "plan")
}

func TestFileTrackerArtifactTypeDocForPlainMarkdown(t *testing.T) {
	expectArtifact(t, "README.md", "doc")
}

func TestFileTrackerArtifactTypeConfigForJSON(t *testing.T) {
	expectArtifact(t, "tsconfig.json", "config")
}

func TestFileTrackerArtifactTypeCodeOutsideTestSpecPlan(t *testing.T) {
	r := fileTrackerOf(t, "Edit", map[string]any{"file_path": "src/daemon.ts", "old_string": "", "new_string": ""})
	if r.ArtifactType == nil || *r.ArtifactType != "code" {
		t.Errorf("got %v", r.ArtifactType)
	}
}

func TestFileTrackerArtifactTypeNullForUnknownExtension(t *testing.T) {
	expectNoArtifact(t, "data/export.parquet")
}

func TestFileTrackerNullFilePathWhenToolInputHasNoPathFields(t *testing.T) {
	r := fileTrackerOf(t, "LS", map[string]any{})
	if r.FilePath != nil || r.Language != nil || r.ArtifactType != nil {
		t.Errorf("got %+v", r)
	}
}

func TestFileTrackerDirectoryContextPriority(t *testing.T) {
	expectArtifact(t, "/repo/tests/plan.md", "test")
	expectArtifact(t, "/repo/test/spec.ts", "test")
	expectArtifact(t, "/repo/specs/plan.md", "spec")
	expectArtifact(t, "/repo/plan/readme.md", "plan")
	expectArtifact(t, "/repo/tests/specs/x.go", "test")
	expectArtifact(t, "/repo/specs/plans/x.go", "spec")
}

func TestFileTrackerFilenamePatternPriority(t *testing.T) {
	expectArtifact(t, "src/a.spec.ts", "test")
	expectArtifact(t, "src/plan.test.ts", "test")
	expectArtifact(t, "src/design.ts", "spec")
	expectArtifact(t, "ARCHITECTURE.md", "spec")
	expectArtifact(t, "my-spec.json", "spec")
	expectArtifact(t, "todo.md", "plan")
	expectArtifact(t, "roadmap.yaml", "plan")
	expectArtifact(t, "plan-spec.md", "spec")
}

func TestFileTrackerWordBoundariesAreAsciiLikeJavaScript(t *testing.T) {
	expectArtifact(t, "specification.md", "doc")
	expectArtifact(t, "inspector.go", "code")
	expectArtifact(t, "plans.md", "doc")
	expectArtifact(t, "my_plan.md", "doc")
	expectArtifact(t, "my\u00E9plan.md", "plan")
}

func TestFileTrackerPathMatchingIsCaseInsensitive(t *testing.T) {
	expectArtifact(t, "/Repo/Tests/Foo.TS", "test")
	expectArtifact(t, "DESIGN.MD", "spec")
	expectArtifact(t, "App.TSX", "code")
}

func TestFileTrackerExtensionClasses(t *testing.T) {
	for _, p := range []string{"a.mdx"} {
		expectArtifact(t, p, "doc")
	}
	for _, p := range []string{"a.yaml", "a.yml", "a.toml", "a.env", "a.json"} {
		expectArtifact(t, p, "config")
	}
	for _, p := range []string{"a.tsx", "a.js", "a.jsx", "a.mjs", "a.cjs", "a.py", "a.rb", "a.go", "a.rs", "a.java", "a.c", "a.cpp", "a.cs", "a.sh", "a.bash", "a.html", "a.css", "a.scss", "a.vue", "a.svelte", "a.sql", "a.graphql"} {
		expectArtifact(t, p, "code")
	}
	expectNoArtifact(t, "Makefile")
	expectNoArtifact(t, "a.sass")
}

func TestFileTrackerDotInDirectoryIsNotAnExtension(t *testing.T) {
	expectNoArtifact(t, "dir.json/README")
}

func TestFileTrackerResultShape(t *testing.T) {
	hookEventTagsMatch(t, fileTrackerResult{})
	path, lang, artifact := "/a.go", "go", "code"
	j := hookEventJSON(t, fileTrackerResult{FilePath: &path, Language: &lang, Operation: "read", ArtifactType: &artifact})
	if j != `{"file_path":"/a.go","language":"go","operation":"read","artifact_type":"code"}` {
		t.Errorf("got %s", j)
	}
	j = hookEventJSON(t, fileTrackerResult{Operation: "glob"})
	if j != `{"file_path":null,"language":null,"operation":"glob","artifact_type":null}` {
		t.Errorf("got %s", j)
	}
}

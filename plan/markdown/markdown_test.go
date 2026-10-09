package markdown

import (
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/plan/model"
)

const doc = "---\nid: \"0002-a3f\"\ntitle: \"Description of plan\"\ntype: dsgn\nstatus: ready\npriority: \"1\"\ncreated: \"2026-09-09T14:07:05.352Z\"\nupdated: \"2026-09-09T14:35:37.046Z\"\ncompleted: \"\"\nplans:\n  - [\"0031-34c\", \"blocks\"]\nprogress:\n  1.1:\n    status: completed\n---\n\n# Description of plan\n\n## Summary\n\n```md\n# not a heading\n### 9.9: not a section\n```\n\n## Phase 1: Core\n\n### 1.1: Model\n\n### 1.2: Store\n\n## Phase 2: Wrap up\n"

func TestParseAndRender(t *testing.T) {
	fm, content, err := Parse(doc)
	if err != nil {
		t.Fatal(err)
	}
	if fm.ID != "0002-a3f" || fm.Plans[0].Relation != "blocks" || fm.Progress["1.1"].Status != "completed" {
		t.Errorf("front matter: %+v", fm)
	}
	if !strings.HasPrefix(content, "# Description of plan\n") {
		t.Errorf("content: %q", content)
	}
	out, err := Render(fm, content)
	if err != nil {
		t.Fatal(err)
	}
	fm2, content2, err := Parse(out)
	if err != nil {
		t.Fatalf("re-parse: %v\n%s", err, out)
	}
	if fm2.ID != fm.ID || content2 != content {
		t.Errorf("round trip mismatch:\n%s", out)
	}
	if !strings.HasPrefix(out, "---\nid: 0002-a3f\n") {
		t.Errorf("unexpected header:\n%s", out)
	}
}

func TestSplitErrors(t *testing.T) {
	if _, _, err := Split("# no front matter"); err == nil {
		t.Error("expected error for missing front matter")
	}
	if _, _, err := Split("---\nid: x\n"); err == nil {
		t.Error("expected error for unclosed front matter")
	}
	fm, c, err := Split("---\nid: x\n---")
	if err != nil || fm != "id: x\n" || c != "" {
		t.Errorf("split without content: %q %q %v", fm, c, err)
	}
}

func TestHeadings(t *testing.T) {
	_, content, _ := Parse(doc)
	h1, ok := FirstH1(content)
	if !ok || h1 != "Description of plan" {
		t.Errorf("h1 = %q %v", h1, ok)
	}
	got := SectionNumbers(content)
	want := []string{"1", "1.1", "1.2", "2"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("sections = %v want %v", got, want)
	}
	if !HasSection(content, "1.2") || HasSection(content, "9.9") {
		t.Error("HasSection")
	}
	replaced := SetH1(content, "New title")
	if h, _ := FirstH1(replaced); h != "New title" {
		t.Errorf("SetH1 replace: %q", h)
	}
	if strings.Count(replaced, "# New title") != 1 {
		t.Error("SetH1 should replace exactly once")
	}
	added := SetH1("## Only h2\n", "Added")
	if !strings.HasPrefix(added, "# Added\n\n## Only h2") {
		t.Errorf("SetH1 prepend: %q", added)
	}
	if n := SectionNumbers("## 3. Third\n### 3.1 Sub\n### Section 3.2: Other\n## Phase 4 — Four\n"); strings.Join(n, ",") != "3,3.1,3.2,4" {
		t.Errorf("alt heading forms: %v", n)
	}
	_ = model.FileNameRe
}

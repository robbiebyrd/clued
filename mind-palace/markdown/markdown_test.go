package markdown

import (
	"reflect"
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/model"
)

const doc = "---\nid: \"0002-a3f\"\ntitle: \"Description of plan\"\ntype: dsgn\nstatus: ready\npriority: \"1\"\ncreated: \"2026-09-09T14:07:05.352Z\"\nupdated: \"2026-09-09T14:35:37.046Z\"\ncompleted: \"\"\nplans:\n  - [\"0031-34c\", \"blocks\"]\nprogress:\n  1.1:\n    status: completed\n---\n\n# Description of plan\n\n## Summary\n\n```md\n# not a heading\n### 9.9: not a section\n```\n\n## Phase 1: Core\n\n### 1.1: Model\n\n### 1.2: Store\n\n## Phase 2: Wrap up\n"

func TestParseAndRender(t *testing.T) {
	fm, content, err := Parse(doc)
	if err != nil {
		t.Fatal(err)
	}
	if fm.ID != "0002-a3f" || fm.PlanLinks()[0].Relation != "blocks" || fm.Progress["1.1"].Status != "completed" {
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
	// The legacy top-level plans list is written back under links.plans.
	if strings.Contains(out, "\nplans:\n") || !strings.Contains(out, "links:\n  plans:\n") {
		t.Errorf("legacy plans not migrated under links:\n%s", out)
	}
}

func TestLegacyPlansMigration(t *testing.T) {
	// Legacy and current layouts merge; the legacy list comes first.
	fm, err := ParseFrontMatter("id: 0001-abc\nplans:\n  - [\"0002-bbb\", \"blocks\"]\nlinks:\n  plans:\n    - [\"0003-ccc\", \"depends\"]\n  web:\n    jira: https://j/1\n")
	if err != nil {
		t.Fatal(err)
	}
	links := fm.PlanLinks()
	want := []model.Link{{ID: "0002-bbb", Relation: "blocks"}, {ID: "0003-ccc", Relation: "depends"}}
	if !reflect.DeepEqual(links, want) || fm.Links.Web["jira"] != "https://j/1" {
		t.Errorf("merged links: %+v", fm.Links)
	}
	// Only the legacy list, with no other links.
	fm, err = ParseFrontMatter("id: 0001-abc\nplans:\n  - [\"0002-bbb\", \"parent\"]\n")
	if err != nil || len(fm.PlanLinks()) != 1 || fm.Links.Repo != nil {
		t.Errorf("legacy only: %+v %v", fm.Links, err)
	}
	// A document that is nothing but a legacy plans list still parses.
	fm, err = ParseFrontMatter("plans:\n  - [\"0002-bbb\", \"parent\"]\n")
	if err != nil || len(fm.PlanLinks()) != 1 {
		t.Errorf("legacy without other keys: %+v %v", fm.Links, err)
	}
	// Malformed legacy entries are still rejected; unknown keys still fail.
	if _, err := ParseFrontMatter("plans:\n  - [\"\", \"parent\"]\n"); err == nil {
		t.Error("malformed legacy link accepted")
	}
	if _, err := ParseFrontMatter("id: 0001-abc\nplanz: []\n"); err == nil {
		t.Error("unknown key accepted")
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

func TestSplitEdgeCases(t *testing.T) {
	if _, _, err := Split("---"); err == nil {
		t.Error("delimiter-only document must not panic or parse")
	}
	if _, _, err := Split("---\n"); err == nil {
		t.Error("open delimiter only must fail")
	}
	if _, err := ParseFrontMatter("id: [unclosed"); err == nil {
		t.Error("corrupt YAML must be reported")
	}
	if fm, err := ParseFrontMatter(""); err != nil || fm.ID != "" {
		t.Errorf("empty front matter: %v", err)
	}
	if _, err := ParseFrontMatter("bogus: 1"); err == nil {
		t.Error("unknown front matter field must be reported")
	}
}

func TestFenceMatching(t *testing.T) {
	content := "# Title\n\n````md\n```\n# inner one\n### 7.1: inner\n```\n~~~\n# inner two\n````\n\n## Phase 1: Real\n\n~~~\n# inner three\n``` \n~~~\n"
	h1, _ := FirstH1(content)
	if h1 != "Title" {
		t.Errorf("h1 = %q", h1)
	}
	if got := SectionNumbers(content); strings.Join(got, ",") != "1" {
		t.Errorf("sections = %v", got)
	}
	out := SetH1(content, "New")
	if strings.Count(out, "# inner") != 3 || !strings.HasPrefix(out, "# New\n") {
		t.Errorf("SetH1 touched fenced content:\n%s", out)
	}
	// A closing fence with an info string does not close the block.
	content = "```\n# a\n``` not-a-close\n# b\n```\n# Outside\n"
	if h1, _ := FirstH1(content); h1 != "Outside" {
		t.Errorf("info-string fence should not close: %q", h1)
	}
	// Indented fences (up to three spaces) count.
	if h1, ok := FirstH1("   ```\n# hidden\n   ```\n"); ok {
		t.Errorf("indented fence not honoured: %q", h1)
	}
}

func TestSectionsCriteriaAndWorkLog(t *testing.T) {
	content := "# T\n\n## Steps\n\n### 1: A\n\n#### 1.1: B\n\n## Acceptance Criteria\n\n- [ ] VERIFY: go test ./...\n- [x] Empty POST returns 400\n- [~] [MANUAL] Check the dashboard\n\n```\n- [ ] not a criterion\n```\n\n## Work Log\n\n### 2026-09-09T14:07:05.352Z - Started\n"
	if got := strings.Join(SectionNumbers(content), ","); got != "1,1.1" {
		t.Errorf("work log timestamps must not count as sections: %s", got)
	}
	items, ok := Criteria(content)
	if !ok || len(items) != 3 {
		t.Fatalf("criteria: %v %d", ok, len(items))
	}
	if items[0].Kind != KindVerify || items[0].Text != "go test ./..." || items[0].State != StateOpen || items[0].Position != 1 {
		t.Errorf("item 1: %+v", items[0])
	}
	if items[1].State != StateDone || items[1].Kind != "" || items[2].State != StateNotApplicable || items[2].Kind != KindManual || items[2].Text != "Check the dashboard" {
		t.Errorf("items: %+v", items[1:])
	}
	if p := CriteriaProblems(content); len(p) != 0 {
		t.Errorf("valid section reported: %v", p)
	}
	if p := CriteriaProblems("# T\n\n## Acceptance Criteria\n\nprose\n"); len(p) != 2 {
		t.Errorf("prose and no items should be reported: %v", p)
	}
	if p := CriteriaProblems("# T\n"); len(p) != 1 {
		t.Errorf("missing section: %v", p)
	}
	out, err := SetCriterion(content, 1, StateDone)
	if err != nil || !strings.Contains(out, "- [x] VERIFY: go test ./...") {
		t.Errorf("SetCriterion: %v\n%s", err, out)
	}
	if _, err := SetCriterion(content, 4, StateDone); err == nil {
		t.Error("position out of range")
	}
	out = AddCriterion(content, "New one", KindManual)
	items, _ = Criteria(out)
	if len(items) != 4 || items[3].Text != "New one" || items[3].Kind != KindManual || !strings.Contains(out, "- [~] [MANUAL] Check the dashboard\n- [ ] [MANUAL] New one\n") {
		t.Errorf("AddCriterion appended wrongly:\n%s", out)
	}
	out = AddCriterion("# T\n", "First", "")
	if items, ok := Criteria(out); !ok || len(items) != 1 || !strings.HasSuffix(out, "## Acceptance Criteria\n\n- [ ] First\n") {
		t.Errorf("AddCriterion without section:\n%s", out)
	}
	out, err = RemoveCriterion(content, 2)
	if err != nil || strings.Contains(out, "Empty POST") {
		t.Errorf("RemoveCriterion: %v\n%s", err, out)
	}
	out = AppendWorkLog(content, "2026-09-10T00:00:00.000Z", "Done")
	if entries := WorkLogEntries(out); len(entries) != 2 || entries[1] != "2026-09-10T00:00:00.000Z - Done" {
		t.Errorf("AppendWorkLog: %v\n%s", entries, out)
	}
	if !strings.HasSuffix(out, "### 2026-09-09T14:07:05.352Z - Started\n\n### 2026-09-10T00:00:00.000Z - Done\n") {
		t.Errorf("AppendWorkLog layout:\n%q", out)
	}
	// Work Log in the middle of the document stays in place.
	mid := "# T\n\n## Work Log\n\n## Later\n\ntext\n"
	out = AppendWorkLog(mid, "2026-09-10T00:00:00.000Z", "Entry")
	if !strings.Contains(out, "## Work Log\n\n### 2026-09-10T00:00:00.000Z - Entry\n\n## Later\n") {
		t.Errorf("AppendWorkLog mid-document:\n%q", out)
	}
	out = AppendWorkLog("# T\n", "2026-09-10T00:00:00.000Z", "Entry")
	if !strings.HasSuffix(out, "## Work Log\n\n### 2026-09-10T00:00:00.000Z - Entry\n") {
		t.Errorf("AppendWorkLog without section:\n%q", out)
	}
	if sec, ok := FindSection(content, "acceptance criteria"); !ok || sec.Start != 8 {
		t.Errorf("FindSection: %+v %v", sec, ok)
	}
}

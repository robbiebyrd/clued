package service

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/events"
	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/model"
)

const storyInput = `{
  "frontMatter": {"title": "Fix empty payload crash", "purpose": "Stop the handler crashing", "type": "bug", "status": "approved", "priority": "P1", "effort": "Small"%s},
  "body": {
    "problemStatement": {"statement": "The handler panics on an empty body.", "issue": "handler.go:42", "impact": "500s."},
    "steps": [{"number": "1", "name": "Guard", "description": "Return 400.", "steps": [{"number": "1.1", "name": "Test", "description": "Regression test."}]}, {"number": "2", "name": "Release", "description": "Ship."}],
    "acceptanceCriteria": [{"text": "go test ./...", "kind": "verify"}, {"text": "Empty POST returns 400"}],
    "files": [{"path": "src/handler.go", "change": "guard"}]
  }
}`

var workLogRe = regexp.MustCompile(`## Work Log\n\n### 2026-09-09T14:07:[0-9]{2}\.352Z - Fixed the guard; 12/12 tests pass\n$`)

func storyJSON(extraFrontMatter string) json.RawMessage {
	return json.RawMessage(strings.Replace(storyInput, "%s", extraFrontMatter, 1))
}

func createStory(t *testing.T, s *Service, extra string) *model.Document {
	t.Helper()
	d, err := s.Create(ctx, storyJSON(extra), "")
	if err != nil {
		t.Fatalf("create story: %v", err)
	}
	return d
}

func TestStoryCreate(t *testing.T) {
	plans, _, fs := newService(t)
	stories := plans.Palace().Stories()
	if stories.Kind().Name != kind.Story || plans.Palace().Plans() != plans {
		t.Fatal("palace wiring")
	}
	d := createStory(t, stories, "")
	fm := d.FrontMatter
	if d.Kind != kind.Story || !strings.HasPrefix(fm.ID, "0001-") || fm.Type != "bugs" || fm.Status != "ready" || fm.Priority != "1" || fm.Effort != "S" || fm.Purpose != "Stop the handler crashing" {
		t.Errorf("story front matter: %+v", fm)
	}
	if fm.Started != "" || fm.Completed != "" {
		t.Errorf("started/completed must be absent: %+v", fm)
	}
	if d.Path != fm.ID+"-bugs-fix-empty-payload-crash.md" {
		t.Errorf("path = %q", d.Path)
	}
	if _, err := os.Stat(fs.AbsPath(kind.Story, d.Path)); err != nil {
		t.Error("story not written under the stories root")
	}
	if got := strings.Join(fm.Progress.SortedKeys(), ","); got != "1,1.1,2" {
		t.Errorf("seeded steps = %s", got)
	}
	for _, want := range []string{"## Problem Statement", "### 1: Guard", "#### 1.1: Test", "## Acceptance Criteria\n\n- [ ] VERIFY: go test ./...\n- [ ] Empty POST returns 400\n", "## Files", "## Work Log\n"} {
		if !strings.Contains(d.Content, want) {
			t.Errorf("content missing %q:\n%s", want, d.Content)
		}
	}
	// Plans and stories have independent id spaces.
	p := create(t, plans, "create-dsgn.json")
	if p.ID()[:5] != "0001-" {
		t.Errorf("plan sequence should not be shared with stories: %s", p.ID())
	}
	if _, err := plans.Get(ctx, d.ID()); !IsKind(err, KindNotFound) {
		t.Error("story id must not resolve as a plan")
	}
	if got, err := stories.Get(ctx, "docs/stories/"+d.Path); err != nil || got.ID() != d.ID() {
		t.Errorf("resolve by path: %v", err)
	}
	// Story-only fields are rejected on plans, plan-only shapes on stories.
	bad := `{"frontMatter":{"title":"x","type":"dsgn","priority":"1","purpose":"no"},"body":{"summary":{"goal":"g","problem":"p"},"design":{}}}`
	if _, err := plans.Create(ctx, json.RawMessage(bad), ""); !IsKind(err, KindValidation) {
		t.Errorf("purpose on a plan: %v", err)
	}
	if _, err := stories.Create(ctx, storyJSON(`,"links":{"plans":[["`+p.ID()+`","parent"]]}`), ""); !IsKind(err, KindValidation) {
		t.Errorf("parent is not a story→plan relation: %v", err)
	}
	if _, err := stories.Create(ctx, storyJSON(`,"links":{"plans":[["0099-zzz","included"]]}`), ""); !IsKind(err, KindNotFound) {
		t.Errorf("linked plan must exist: %v", err)
	}
	if _, err := stories.Create(ctx, storyJSON(`,"links":{"stories":[["0099-zzz","parent"]]}`), ""); !IsKind(err, KindNotFound) {
		t.Errorf("linked story must exist: %v", err)
	}
	// Sections on a plan link must be in the plan's progress map.
	impl := create(t, plans, "create-impl.json")
	if _, err := stories.Create(ctx, storyJSON(`,"links":{"plans":[["`+impl.ID()+`","included",["1.1","7"]]]}`), ""); !IsKind(err, KindUnknownSection) {
		t.Errorf("unknown plan section: %v", err)
	}
	child := createStory(t, stories, `,"links":{"plans":[["`+impl.ID()+`","included",["1.1","2"]]],"stories":[["`+d.ID()+`","parent"]]}`)
	if l := child.FrontMatter.PlanLinks(); len(l) != 1 || strings.Join(l[0].Sections, ",") != "1.1,2" {
		t.Errorf("plan link sections: %+v", l)
	}
	// Creating directly as in_progress / complete applies the managed timestamps and the gate.
	ip, err := stories.Create(ctx, storyJSON(`,"status":"in_progress"`), "")
	if err != nil || ip.FrontMatter.Started == "" {
		t.Errorf("in_progress on create sets started: %v %+v", err, ip)
	}
	if _, err := stories.Create(ctx, storyJSON(`,"status":"complete"`), ""); !IsKind(err, KindIncompleteCriteria) {
		t.Errorf("complete on create with open criteria: %v", err)
	}
	// Explicit progress keys must match steps.
	if _, err := stories.Create(ctx, storyJSON(`,"progress":{"9":{"status":"pending"}}`), ""); !IsKind(err, KindUnknownStep) {
		t.Errorf("unknown step on create: %v", err)
	}
}

func TestStoryStatusAndCriteria(t *testing.T) {
	plans, _, fs := newService(t)
	s := plans.Palace().Stories()
	d := createStory(t, s, "")
	id := d.ID()
	d, err := s.SetStatus(ctx, id, "start", false)
	if err != nil || d.FrontMatter.Status != "in_progress" || d.FrontMatter.Started == "" {
		t.Fatalf("start: %v %+v", err, d.FrontMatter)
	}
	started := d.FrontMatter.Started
	// Criteria gate: force does not override.
	for _, force := range []bool{false, true} {
		_, err := s.SetStatus(ctx, id, "complete", force)
		if !IsKind(err, KindIncompleteCriteria) || len(AsError(err).Problems) != 2 {
			t.Errorf("complete with open criteria (force=%v): %v", force, err)
		}
	}
	if _, err := s.PatchFrontMatter(ctx, id, map[string]any{"status": "complete"}, true); !IsKind(err, KindIncompleteCriteria) {
		t.Errorf("patch to complete is gated too: %v", err)
	}
	rep, _ := s.GetCriteria(ctx, id)
	if rep.Open != 2 || len(rep.Criteria) != 2 || rep.Criteria[0].Kind != "verify" {
		t.Errorf("criteria: %+v", rep)
	}
	if _, err := s.SetCriterion(ctx, id, 3, "done"); !IsKind(err, KindUnknownCriterion) {
		t.Errorf("unknown criterion: %v", err)
	}
	if _, err := s.SetCriterion(ctx, id, 1, "maybe"); !IsKind(err, KindBadRequest) {
		t.Errorf("bad state: %v", err)
	}
	if _, err := s.SetCriterion(ctx, id, 1, "done"); err != nil {
		t.Fatal(err)
	}
	d, _ = s.SetCriterion(ctx, id, 2, "n/a")
	if !strings.Contains(d.Content, "- [x] VERIFY: go test ./...\n- [~] Empty POST returns 400\n") {
		t.Errorf("criteria not rewritten:\n%s", d.Content)
	}
	d, err = s.AddCriterion(ctx, id, "Dashboard checked", "manual")
	if err != nil || !strings.Contains(d.Content, "- [ ] [MANUAL] Dashboard checked\n") {
		t.Errorf("add criterion: %v", err)
	}
	if _, err := s.AddCriterion(ctx, id, "x", "robot"); !IsKind(err, KindBadRequest) {
		t.Errorf("bad kind: %v", err)
	}
	if _, err := s.RemoveCriterion(ctx, id, 9); !IsKind(err, KindUnknownCriterion) {
		t.Errorf("remove unknown criterion: %v", err)
	}
	d, _ = s.RemoveCriterion(ctx, id, 3)
	if strings.Contains(d.Content, "Dashboard") {
		t.Error("remove criterion")
	}
	d, err = s.SetStatus(ctx, id, "complete", false)
	if err != nil || d.FrontMatter.Completed == "" || d.FrontMatter.Started != started {
		t.Fatalf("complete: %v %+v", err, d.FrontMatter)
	}
	completed := d.FrontMatter.Completed
	d, _ = s.SetStatus(ctx, id, "in_progress", false)
	if d.FrontMatter.Started == started || d.FrontMatter.Completed != completed {
		t.Errorf("returning to in_progress overwrites started and keeps completed: %+v", d.FrontMatter)
	}
	// Archive moves the file under the stories root and back.
	d, err = s.SetStatus(ctx, id, "archived", true)
	if err != nil || d.Path != "archive/"+id+"-bugs-fix-empty-payload-crash.md" {
		t.Fatalf("archive: %v %q", err, d.Path)
	}
	if _, err := os.Stat(fs.AbsPath(kind.Story, d.Path)); err != nil {
		t.Error("archived story missing")
	}
	d, _ = s.SetStatus(ctx, id, "ready", false)
	if strings.HasPrefix(d.Path, "archive/") {
		t.Error("un-archive")
	}
	// Work log is append-only and timestamped by the service.
	d, err = s.AppendWorkLog(ctx, id, "Fixed the guard; 12/12 tests pass")
	if err != nil || !workLogRe.MatchString(d.Content) {
		t.Errorf("work log: %v\n%q", err, d.Content)
	}
	if _, err := s.AppendWorkLog(ctx, id, "two\nlines"); !IsKind(err, KindBadRequest) {
		t.Errorf("multi-line entry: %v", err)
	}
	if _, err := plans.AppendWorkLog(ctx, id, "x"); !IsKind(err, KindBadRequest) {
		t.Errorf("plans have no work log: %v", err)
	}
	// Work-log timestamps never count as steps.
	if rep, _ := s.GetProgress(ctx, id); strings.Join(rep.Sections, ",") != "1,1.1,2" {
		t.Errorf("sections after work log: %v", rep.Sections)
	}
	// Immutable fields include started for stories.
	for _, f := range []string{"id", "created", "updated", "started", "completed"} {
		if _, err := s.PatchFrontMatter(ctx, id, map[string]any{f: "x"}, false); !IsKind(err, KindImmutableField) {
			t.Errorf("patch %s: %v", f, err)
		}
	}
	d, err = s.PatchFrontMatter(ctx, id, map[string]any{"purpose": "New purpose", "type": "refactor"}, false)
	if err != nil || d.FrontMatter.Purpose != "New purpose" || d.FrontMatter.Type != "impr" || d.Path != id+"-impr-fix-empty-payload-crash.md" {
		t.Errorf("patch purpose/type: %v %+v", err, d)
	}
	d, _ = s.SetPurpose(ctx, id, "Again")
	if d.FrontMatter.Purpose != "Again" {
		t.Error("setPurpose")
	}
	if _, err := plans.SetPurpose(ctx, id, "x"); !IsKind(err, KindBadRequest) {
		t.Error("plans have no purpose")
	}
}

func TestStoryAutoCompleteAndStrictUpdate(t *testing.T) {
	plans, _, _ := newService(t)
	s := plans.Palace().Stories()
	d := createStory(t, s, "")
	id := d.ID()
	s.SetStatus(ctx, id, "in_progress", false)
	s.SetProgress(ctx, id, "1", "done")
	s.SetProgress(ctx, id, "1.1", "done")
	d, _ = s.SetProgress(ctx, id, "2", "done")
	if d.FrontMatter.Status != "in_progress" {
		t.Error("all steps done but criteria open: no auto-complete")
	}
	s.SetCriterion(ctx, id, 1, "done")
	s.SetCriterion(ctx, id, 2, "done")
	s.SetProgress(ctx, id, "2", "pending")
	d, err := s.SetProgress(ctx, id, "2", "done")
	if err != nil || d.FrontMatter.Status != "complete" || d.FrontMatter.Completed == "" {
		t.Errorf("auto-complete: %v %+v", err, d.FrontMatter)
	}
	// Not in_progress: no auto-complete.
	d2 := createStory(t, s, "")
	s.SetCriterion(ctx, d2.ID(), 1, "done")
	s.SetCriterion(ctx, d2.ID(), 2, "done")
	for _, step := range []string{"1", "1.1", "2"} {
		d2, _ = s.SetProgress(ctx, d2.ID(), step, "done")
	}
	if d2.FrontMatter.Status != "ready" {
		t.Errorf("auto-complete requires in_progress: %s", d2.FrontMatter.Status)
	}
	if _, err := s.SetProgress(ctx, d2.ID(), "2.1.3", "done"); !IsKind(err, KindUnknownStep) {
		t.Errorf("unknown deep step: %v", err)
	}
	if _, err := s.SetProgress(ctx, d2.ID(), "x", "done"); !IsKind(err, KindBadRequest) {
		t.Errorf("bad step: %v", err)
	}
	if _, err := s.AddProgressStory(ctx, d2.ID(), "1", "0001-abc"); !IsKind(err, KindBadRequest) {
		t.Errorf("story progress entries carry no stories: %v", err)
	}
	// Deep steps work when the content has them.
	deep := "# Fix empty payload crash\n\n## Steps\n\n### 1: A\n\n#### 1.1: B\n\n##### 1.1.1: C\n\n### 2: D\n\n## Acceptance Criteria\n\n- [x] ok\n\n## Work Log\n"
	if _, err := s.Update(ctx, d2.ID(), deep); err != nil {
		t.Fatal(err)
	}
	if d2, err := s.SetProgress(ctx, d2.ID(), "1.1.1", "blocked"); err != nil || d2.FrontMatter.Progress["1.1.1"].Status != "blocked" {
		t.Errorf("deep step: %v", err)
	}
	// Update that orphans progress keys is rejected (UnknownStep) and nothing is written.
	if _, err := s.Update(ctx, d2.ID(), "# Fix empty payload crash\n\n## Steps\n\n### 1: A\n\n## Acceptance Criteria\n\n- [x] ok\n"); !IsKind(err, KindUnknownStep) {
		t.Errorf("orphaned progress keys: %v", err)
	}
	if got, _ := s.Get(ctx, d2.ID()); got.Content != deep {
		t.Error("rejected update must not write")
	}
	s.RemoveProgress(ctx, d2.ID(), "1.1.1")
	s.RemoveProgress(ctx, d2.ID(), "1.1")
	s.RemoveProgress(ctx, d2.ID(), "2")
	if _, err := s.Update(ctx, d2.ID(), "# Fix empty payload crash\n\n## Steps\n\n### 1: A\n\n## Acceptance Criteria\n\n- [x] ok\n"); err != nil {
		t.Errorf("update after removing progress: %v", err)
	}
	// Validate reports a missing or malformed criteria section.
	s.Update(ctx, d2.ID(), "# Fix empty payload crash\n\n## Steps\n\n### 1: A\n\n## Acceptance Criteria\n\nprose only\n")
	v, _ := s.Validate(ctx, d2.ID())
	if v.Valid || !strings.Contains(strings.Join(v.Problems, "\n"), "not a checklist item") {
		t.Errorf("validate criteria: %+v", v)
	}
	if _, err := s.SetStatus(ctx, d2.ID(), "in_progress", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetStatus(ctx, d2.ID(), "complete", false); !IsKind(err, KindIncompleteCriteria) {
		t.Errorf("no checklist items: %v", err)
	}
}

func TestStoryLinksAndDeleteProtection(t *testing.T) {
	plans, _, _ := newService(t)
	s := plans.Palace().Stories()
	impl := create(t, plans, "create-impl.json")
	a := createStory(t, s, "")
	b := createStory(t, s, "")
	// Story → plan links with sections.
	if _, err := s.AddPlanLink(ctx, a.ID(), impl.ID(), "parent", nil); !IsKind(err, KindBadRequest) {
		t.Errorf("parent relation: %v", err)
	}
	if _, err := s.AddPlanLink(ctx, a.ID(), impl.ID(), "included", []string{"1.1", "9"}); !IsKind(err, KindUnknownSection) {
		t.Errorf("unknown section: %v", err)
	}
	if _, err := s.AddPlanLink(ctx, a.ID(), impl.ID(), "included", []string{"1.1.1"}); !IsKind(err, KindBadRequest) {
		t.Errorf("deep section number: %v", err)
	}
	d, err := s.AddPlanLink(ctx, a.ID(), impl.ID(), "included", []string{"1.1,2"})
	if err != nil || strings.Join(d.FrontMatter.PlanLinks()[0].Sections, ",") != "1.1,2" {
		t.Fatalf("link with sections: %v %+v", err, d.FrontMatter.Links)
	}
	d, _ = s.AddPlanLink(ctx, a.ID(), impl.ID(), "included", nil) // duplicate keeps sections
	if len(d.FrontMatter.PlanLinks()) != 1 || len(d.FrontMatter.PlanLinks()[0].Sections) != 2 {
		t.Errorf("duplicate link: %+v", d.FrontMatter.PlanLinks())
	}
	d, _ = s.AddPlanLink(ctx, a.ID(), impl.ID(), "included", []string{"1"}) // replaces sections
	if strings.Join(d.FrontMatter.PlanLinks()[0].Sections, ",") != "1" {
		t.Errorf("re-link replaces sections: %+v", d.FrontMatter.PlanLinks())
	}
	if _, err := s.SetPlanSections(ctx, a.ID(), "0099-zzz", []string{"1"}); !IsKind(err, KindNotFound) {
		t.Errorf("sections on missing link: %v", err)
	}
	if _, err := s.SetPlanSections(ctx, a.ID(), impl.ID(), []string{"8"}); !IsKind(err, KindUnknownSection) {
		t.Errorf("bad sections: %v", err)
	}
	d, _ = s.SetPlanSections(ctx, a.ID(), impl.ID(), nil)
	if d.FrontMatter.PlanLinks()[0].Sections != nil {
		t.Error("empty list removes the third element")
	}
	if _, err := plans.SetPlanSections(ctx, impl.ID(), impl.ID(), nil); !IsKind(err, KindBadRequest) {
		t.Error("plans cannot set sections")
	}
	list, _ := s.List(ctx, ListFilter{Plan: impl.ID()})
	if len(list) != 1 || list[0].Kind != kind.Story {
		t.Errorf("list stories by plan: %+v", list)
	}
	// Story → story links need an existing target and allow parent.
	if _, err := s.AddStoryLink(ctx, a.ID(), "0099-zzz", "parent"); !IsKind(err, KindNotFound) {
		t.Errorf("missing story target: %v", err)
	}
	if _, err := s.AddStoryLink(ctx, a.ID(), a.ID(), "parent"); !IsKind(err, KindBadRequest) {
		t.Errorf("self link: %v", err)
	}
	if _, err := s.AddStoryLink(ctx, a.ID(), b.ID(), "parent"); err != nil {
		t.Fatal(err)
	}
	// Delete protection across kinds.
	if _, err := s.Delete(ctx, b.ID(), false); !IsKind(err, KindLinkedStory) || AsError(err).Problems[0] != "story "+a.ID() {
		t.Errorf("story linked from story: %v", err)
	}
	if _, err := plans.Delete(ctx, impl.ID(), false); !IsKind(err, KindLinkedPlan) || AsError(err).Problems[0] != "story "+a.ID() {
		t.Errorf("plan linked from story: %v", err)
	}
	plans.AddStoryLink(ctx, impl.ID(), a.ID(), "included")
	plans.AddProgressStory(ctx, impl.ID(), "1.1", b.ID())
	if _, err := s.Delete(ctx, a.ID(), false); !IsKind(err, KindLinkedStory) || AsError(err).Problems[0] != "plan "+impl.ID() {
		t.Errorf("story linked from plan links.stories: %v", err)
	}
	plans.RemoveStoryLink(ctx, impl.ID(), a.ID(), "")
	s.RemoveStoryLink(ctx, a.ID(), b.ID(), "")
	if _, err := s.Delete(ctx, b.ID(), false); !IsKind(err, KindLinkedStory) {
		t.Errorf("story linked from plan progress: %v", err)
	}
	res, err := s.Delete(ctx, b.ID(), true)
	if err != nil || res.Kind != kind.Story || len(res.LinkedBy) != 1 {
		t.Errorf("forced delete: %v %+v", err, res)
	}
	if v, _ := plans.Validate(ctx, impl.ID()); !v.Valid {
		t.Errorf("a plan may reference stories that do not exist (tracked elsewhere): %v", v.Problems)
	}
	// Plans may link stories that do not exist (tracked elsewhere); stories may not.
	if _, err := plans.AddStoryLink(ctx, impl.ID(), "0042-abc", "depends"); err != nil {
		t.Errorf("plan link to unknown story: %v", err)
	}
	// Repo files and pull request.
	d, err = s.SetRepo(ctx, a.ID(), "git@github.com:o/p.git", "", "https://github.com/o/p/pull/1")
	if err != nil || d.FrontMatter.Links.Repo.PullRequest == "" {
		t.Fatalf("setRepo pull request: %v", err)
	}
	d, _ = s.AddFile(ctx, a.ID(), "src/handler.go")
	d, _ = s.AddFile(ctx, a.ID(), "src/handler.go")
	d, _ = s.AddFile(ctx, a.ID(), "src/other.go")
	if len(d.FrontMatter.Links.Repo.Files) != 2 {
		t.Errorf("files: %+v", d.FrontMatter.Links.Repo)
	}
	if _, err := s.RemoveFile(ctx, a.ID(), "nope"); !IsKind(err, KindNotFound) {
		t.Errorf("remove missing file: %v", err)
	}
	d, _ = s.RemoveFile(ctx, a.ID(), "src/handler.go")
	d, _ = s.RemoveFile(ctx, a.ID(), "src/other.go")
	if d.FrontMatter.Links.Repo.Files != nil {
		t.Errorf("files should be gone: %+v", d.FrontMatter.Links.Repo)
	}
	d, _ = s.ClearRepo(ctx, a.ID())
	if d.FrontMatter.Links.Repo != nil {
		t.Error("clearRepo")
	}
}

func TestStoryTemplatesAndEvents(t *testing.T) {
	plans, _, _ := newService(t)
	s := plans.Palace().Stories()
	tpl, _ := s.GetTemplate(ctx, "")
	if tpl.Kind != kind.Story || !strings.Contains(tpl.Content, "## Problem Statement") {
		t.Errorf("story default template: %+v", tpl)
	}
	if err := s.DeleteTemplate(ctx, "default"); !IsKind(err, KindConflict) {
		t.Errorf("default story template is protected: %v", err)
	}
	if _, err := s.UpdateTemplate(ctx, "default", "# {{ .Title }}\n\n## Acceptance Criteria\n\n- [ ] ok\n\n## Work Log\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTemplate(ctx, "default"); !IsKind(err, KindConflict) {
		t.Errorf("stored default story template is protected: %v", err)
	}
	// Templates are per kind: the plan default is untouched.
	if tpl, _ := plans.GetTemplate(ctx, ""); !strings.Contains(tpl.Content, "## Summary") {
		t.Error("plan template changed by story template update")
	}
	if _, err := s.CreateTemplate(ctx, "tiny", "# {{ .Title }}\n{{ .Body.ProblemStatement.Statement }}\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := plans.GetTemplate(ctx, "tiny"); !IsKind(err, KindNotFound) {
		t.Error("story template visible to plans")
	}
	sub := s.Bus().Subscribe(events.Filter{Kind: kind.Story}, 16)
	d, err := s.Create(ctx, storyJSON(""), "tiny")
	if err != nil || d.Content != "# Fix empty payload crash\nThe handler panics on an empty body.\n" {
		t.Errorf("create with story template: %v %q", err, d.Content)
	}
	create(t, plans, "create-dsgn.json")
	s.SetPriority(ctx, d.ID(), "P5")
	var types []string
	for len(sub.C) > 0 {
		e := <-sub.C
		types = append(types, e.Type+":"+e.Operation)
	}
	if strings.Join(types, ",") != "story.created:create,story.updated:setPriority" {
		t.Errorf("story events only: %v", types)
	}
}

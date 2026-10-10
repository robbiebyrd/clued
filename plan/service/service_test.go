package service

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/plan/config"
	"github.com/robbiebyrd/clued/plan/events"
	"github.com/robbiebyrd/clued/plan/model"
	"github.com/robbiebyrd/clued/plan/store"
	"github.com/robbiebyrd/clued/plan/store/filestore"
	"github.com/robbiebyrd/clued/plan/store/memstore"
)

var ctx = context.Background()

func newService(t *testing.T) (*Service, *memstore.Store, *filestore.Store) {
	t.Helper()
	mem := memstore.New("mem")
	fs := filestore.New("file", t.TempDir())
	if err := fs.Init(ctx); err != nil {
		t.Fatal(err)
	}
	s, err := New(config.Default(), store.NewMulti(fs, mem), events.New())
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 9, 14, 7, 5, 352_000_000, time.UTC)
	tick := 0
	s.SetClock(func() time.Time { tick++; return base.Add(time.Duration(tick) * time.Second) })
	return s, mem, fs
}

func input(t *testing.T, file string) json.RawMessage {
	t.Helper()
	b, err := os.ReadFile("../testdata/" + file)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func create(t *testing.T, s *Service, file string) *model.Plan {
	t.Helper()
	p, err := s.Create(ctx, input(t, file), "")
	if err != nil {
		t.Fatalf("create %s: %v", file, err)
	}
	return p
}

func TestCreate(t *testing.T) {
	s, mem, fs := newService(t)
	p := create(t, s, "create-impl.json")
	fm := p.FrontMatter
	if !model.PlanIDRe.MatchString(fm.ID) || !strings.HasPrefix(fm.ID, "0001-") {
		t.Errorf("id = %q", fm.ID)
	}
	if fm.Status != "ready" || fm.Priority != "2" || fm.Effort != "M" {
		t.Errorf("synonyms not normalised: %+v", fm)
	}
	if fm.Created != "2026-09-09T14:07:06.352Z" || fm.Updated != fm.Created || fm.Completed != "" {
		t.Errorf("timestamps: %+v", fm)
	}
	if raw, _ := fs.GetPlan(ctx, fm.ID); raw.FrontMatter.Completed != "" {
		t.Error("completed should be absent until set")
	}
	if doc, _ := os.ReadFile(fs.AbsPath(p.Path)); strings.Contains(string(doc), "completed:") {
		t.Error("completed must be omitted from the file until set")
	}
	if p.Path != fm.ID+"-impl-add-plan-service.md" {
		t.Errorf("path = %q", p.Path)
	}
	if !strings.HasPrefix(p.Content, "# Add plan service\n") {
		t.Errorf("content: %q", p.Content[:40])
	}
	if got := strings.Join(fm.Progress.SortedKeys(), ","); got != "1,1.1,2" {
		t.Errorf("seeded progress = %s", got)
	}
	if fm.Progress["1.1"].Status != "pending" {
		t.Error("seeded status")
	}
	// Both copies hold it.
	for _, st := range []store.Store{mem, fs} {
		if _, err := st.GetPlan(ctx, fm.ID); err != nil {
			t.Errorf("%s: %v", st.Name(), err)
		}
	}
	// Second plan gets the next sequence number.
	p2 := create(t, s, "create-dsgn.json")
	if !strings.HasPrefix(p2.ID(), "0002-") || p2.Path != p2.ID()+"-dsgn-plan-service-design.md" {
		t.Errorf("second plan: %s %s", p2.ID(), p2.Path)
	}
	if p2.FrontMatter.Progress != nil {
		t.Error("design plan should not seed progress")
	}
	// Resolve by path and by id.
	for _, ident := range []string{p2.ID(), p2.Path, "docs/plans/" + p2.Path} {
		if got, err := s.Get(ctx, ident); err != nil || got.ID() != p2.ID() {
			t.Errorf("resolve %q: %v", ident, err)
		}
	}
	if _, err := s.Get(ctx, "0099-zzz"); !IsKind(err, KindNotFound) {
		t.Errorf("missing plan: %v", err)
	}
	if _, err := s.Get(ctx, "README.md"); !IsKind(err, KindNotFound) {
		t.Errorf("bad identifier: %v", err)
	}
}

func TestCreateValidation(t *testing.T) {
	s, _, _ := newService(t)
	// Unknown status.
	bad := `{"frontMatter":{"title":"x","type":"dsgn","status":"bogus","priority":"1"},"body":{"summary":{"goal":"g","problem":"p"}}}`
	_, err := s.Create(ctx, json.RawMessage(bad), "")
	if !IsKind(err, KindValidation) || !strings.Contains(err.Error(), "bogus") {
		t.Errorf("unknown status: %v", err)
	}
	// Design plan missing required design sections.
	bad = `{"frontMatter":{"title":"x","type":"dsgn","priority":"1"},"body":{"summary":{"goal":"g","problem":"p"}}}`
	if _, err := s.Create(ctx, json.RawMessage(bad), ""); !IsKind(err, KindValidation) {
		t.Errorf("dsgn without design: %v", err)
	}
	// Draft with empty design and default status works; priority as number.
	ok := `{"frontMatter":{"title":"A draft","type":"Draft","priority":3},"body":{"summary":{"goal":"g","problem":"p"},"design":{}}}`
	p, err := s.Create(ctx, json.RawMessage(ok), "")
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	if p.FrontMatter.Status != "pending" || p.FrontMatter.Type != "drft" || p.FrontMatter.Priority != "3" {
		t.Errorf("draft defaults: %+v", p.FrontMatter)
	}
	// Linked plan must exist (links.plans and the legacy top-level plans form).
	bad = `{"frontMatter":{"title":"x","type":"drft","priority":"1","links":{"plans":[["0099-zzz","parent"]]}},"body":{"summary":{"goal":"g","problem":"p"},"design":{}}}`
	if _, err := s.Create(ctx, json.RawMessage(bad), ""); !IsKind(err, KindNotFound) {
		t.Errorf("dangling link: %v", err)
	}
	bad = `{"frontMatter":{"title":"x","type":"drft","priority":"1","plans":[["0099-zzz","parent"]]},"body":{"summary":{"goal":"g","problem":"p"},"design":{}}}`
	if _, err := s.Create(ctx, json.RawMessage(bad), ""); !IsKind(err, KindNotFound) {
		t.Errorf("dangling legacy link: %v", err)
	}
	legacy := `{"frontMatter":{"title":"Child","type":"drft","priority":"1","plans":[["` + p.ID() + `","parent"]]},"body":{"summary":{"goal":"g","problem":"p"},"design":{}}}`
	child, err := s.Create(ctx, json.RawMessage(legacy), "")
	if err != nil {
		t.Fatalf("legacy plans input: %v", err)
	}
	if got := child.FrontMatter.PlanLinks(); len(got) != 1 || got[0].ID != p.ID() || got[0].Relation != "parent" {
		t.Errorf("legacy plans not moved under links: %+v", child.FrontMatter.Links)
	}
	// Both forms at once are merged, legacy first.
	both := `{"frontMatter":{"title":"Both","type":"drft","priority":"1","plans":[["` + p.ID() + `","parent"]],"links":{"plans":[["` + child.ID() + `","blocks"]]}},"body":{"summary":{"goal":"g","problem":"p"},"design":{}}}`
	merged, err := s.Create(ctx, json.RawMessage(both), "")
	if err != nil {
		t.Fatalf("merged plans input: %v", err)
	}
	if got := merged.FrontMatter.PlanLinks(); len(got) != 2 || got[0].ID != p.ID() || got[1].ID != child.ID() || got[1].Relation != "blocks" {
		t.Errorf("legacy and current plans not merged: %+v", got)
	}
	// A malformed links value is still rejected by the schema when legacy plans are present.
	bad = `{"frontMatter":{"title":"x","type":"drft","priority":"1","plans":[["` + p.ID() + `","parent"]],"links":"nope"},"body":{"summary":{"goal":"g","problem":"p"},"design":{}}}`
	if _, err := s.Create(ctx, json.RawMessage(bad), ""); !IsKind(err, KindValidation) {
		t.Errorf("non-object links with legacy plans: %v", err)
	}
	// Not JSON.
	if _, err := s.Create(ctx, json.RawMessage("nope"), ""); !IsKind(err, KindValidation) {
		t.Errorf("garbage: %v", err)
	}
	// Requested id is honoured when free, rejected when taken.
	req := `{"frontMatter":{"id":"0042-abc","title":"x","type":"drft","priority":"1"},"body":{"summary":{"goal":"g","problem":"p"},"design":{}}}`
	if p, err := s.Create(ctx, json.RawMessage(req), ""); err != nil || p.ID() != "0042-abc" {
		t.Errorf("requested id: %v", err)
	}
	if _, err := s.Create(ctx, json.RawMessage(req), ""); !IsKind(err, KindConflict) {
		t.Errorf("duplicate id: %v", err)
	}
	// Next sequence continues after the highest.
	if p, err := s.Create(ctx, json.RawMessage(ok), ""); err != nil || !strings.HasPrefix(p.ID(), "0043-") {
		t.Errorf("sequence after 0042: %v %v", p, err)
	}
}

func TestStatusWorkflow(t *testing.T) {
	s, _, fs := newService(t)
	p := create(t, s, "create-dsgn.json") // pending
	id := p.ID()
	if _, err := s.SetStatus(ctx, id, "complete", false); !IsKind(err, KindInvalidTransition) {
		t.Errorf("pending→complete should be rejected: %v", err)
	}
	tr, _ := s.GetTransitions(ctx, id)
	if strings.Join(tr.Transitions, ",") != "blocked,rejected,validated" {
		t.Errorf("transitions: %v", tr.Transitions)
	}
	for _, st := range []string{"valid", "approved", "start"} {
		if _, err := s.SetStatus(ctx, id, st, false); err != nil {
			t.Fatalf("→%s: %v", st, err)
		}
	}
	p, err := s.SetStatus(ctx, id, "done", false)
	if err != nil {
		t.Fatal(err)
	}
	if p.FrontMatter.Status != "complete" || p.FrontMatter.Completed == "" {
		t.Errorf("complete: %+v", p.FrontMatter)
	}
	if rep, _ := s.Validate(ctx, id); !rep.Valid {
		t.Errorf("complete plan invalid: %v", rep.Problems)
	}
	firstCompleted := p.FrontMatter.Completed
	p, _ = s.SetStatus(ctx, id, "in_progress", false)
	if p.FrontMatter.Completed != firstCompleted {
		t.Error("leaving complete must keep completed")
	}
	p, _ = s.SetStatus(ctx, id, "complete", false)
	if p.FrontMatter.Completed == "" || p.FrontMatter.Completed == firstCompleted {
		t.Errorf("reaching complete again must overwrite completed: %q vs %q", p.FrontMatter.Completed, firstCompleted)
	}
	p, _ = s.SetStatus(ctx, id, "in_progress", false)
	// Force skips the workflow.
	p, err = s.SetStatus(ctx, id, "archived", true)
	if err != nil {
		t.Fatal(err)
	}
	if p.Path != "archive/"+id+"-dsgn-plan-service-design.md" {
		t.Errorf("archived path = %q", p.Path)
	}
	if _, err := os.Stat(fs.AbsPath(p.Path)); err != nil {
		t.Error("archived file missing")
	}
	if _, err := os.Stat(fs.AbsPath(id + "-dsgn-plan-service-design.md")); err == nil {
		t.Error("active file should be gone after archiving")
	}
	list, _ := s.List(ctx, ListFilter{})
	if len(list) != 0 {
		t.Error("archived plans hidden from default list")
	}
	list, _ = s.List(ctx, ListFilter{IncludeArchived: true})
	if len(list) != 1 {
		t.Error("includeArchived")
	}
	// Same status is a no-op.
	before := p.FrontMatter.Updated
	p, _ = s.SetStatus(ctx, id, "archived", false)
	if p.FrontMatter.Updated != before {
		t.Error("no-op status change should not write")
	}
	p, _ = s.SetStatus(ctx, id, "ready", false)
	if strings.HasPrefix(p.Path, "archive/") {
		t.Error("un-archiving should move the file back")
	}
	if _, err := s.SetStatus(ctx, id, "weird", false); !IsKind(err, KindBadRequest) {
		t.Errorf("unknown status: %v", err)
	}
}

func TestFieldSetters(t *testing.T) {
	s, _, _ := newService(t)
	p := create(t, s, "create-dsgn.json")
	id := p.ID()
	p, err := s.SetTitle(ctx, id, "Renamed plan", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p.Content, "# Renamed plan\n") || p.Path != id+"-dsgn-plan-service-design.md" {
		t.Errorf("setTitle: %q %q", p.Content[:20], p.Path)
	}
	p, _ = s.SetTitle(ctx, id, "Renamed again", true)
	if p.Path != id+"-dsgn-renamed-again.md" {
		t.Errorf("rename: %q", p.Path)
	}
	p, err = s.SetType(ctx, id, "impl")
	if err != nil {
		t.Fatal(err)
	}
	if p.Path != id+"-impl-renamed-again.md" || p.FrontMatter.Type != "impl" {
		t.Errorf("setType: %+v", p.Path)
	}
	if _, err := s.SetType(ctx, id, "nope"); !IsKind(err, KindBadRequest) {
		t.Error("bad type")
	}
	p, _ = s.SetPriority(ctx, id, "Critical")
	if p.FrontMatter.Priority != "1" {
		t.Error("priority label")
	}
	p, _ = s.SetEffort(ctx, id, 8)
	if p.FrontMatter.Effort != "L" {
		t.Error("effort points")
	}
	p, _ = s.ClearEffort(ctx, id)
	if p.FrontMatter.Effort != "" {
		t.Error("clear effort")
	}
	// Patch.
	if _, err := s.PatchFrontMatter(ctx, id, map[string]any{"id": "x"}, false); !IsKind(err, KindImmutableField) {
		t.Errorf("patch id: %v", err)
	}
	if _, err := s.PatchFrontMatter(ctx, id, map[string]any{"bogus": 1}, false); !IsKind(err, KindBadRequest) {
		t.Errorf("patch unknown: %v", err)
	}
	if _, err := s.PatchFrontMatter(ctx, id, map[string]any{"status": "complete"}, false); !IsKind(err, KindInvalidTransition) {
		t.Errorf("patch status respects workflow: %v", err)
	}
	p, err = s.PatchFrontMatter(ctx, id, map[string]any{"priority": "P4", "effort": "XL", "status": "validated", "title": "Patched", "links": map[string]any{"web": map[string]any{"jira": "https://j/1"}}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.FrontMatter.Priority != "4" || p.FrontMatter.Effort != "XL" || p.FrontMatter.Status != "validated" || p.FrontMatter.Title != "Patched" || p.FrontMatter.Links.Web["jira"] != "https://j/1" {
		t.Errorf("patch: %+v", p.FrontMatter)
	}
	// A links patch merges per kind: omitted kinds (here plans) are kept, null clears.
	other := create(t, s, "create-dsgn.json")
	if _, err := s.AddPlanLink(ctx, id, other.ID(), "depends"); err != nil {
		t.Fatal(err)
	}
	p, err = s.PatchFrontMatter(ctx, id, map[string]any{"links": map[string]any{"web": map[string]any{"docs": "https://d/1"}}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.FrontMatter.PlanLinks()) != 1 || p.FrontMatter.Links.Web["docs"] != "https://d/1" || p.FrontMatter.Links.Web["jira"] != "" {
		t.Errorf("links patch should keep plans and replace web: %+v", p.FrontMatter.Links)
	}
	p, _ = s.PatchFrontMatter(ctx, id, map[string]any{"links": map[string]any{"plans": nil}}, false)
	if len(p.FrontMatter.PlanLinks()) != 0 || p.FrontMatter.Links.Web["docs"] != "https://d/1" {
		t.Errorf("null should clear only plans: %+v", p.FrontMatter.Links)
	}
	if _, err := s.PatchFrontMatter(ctx, id, map[string]any{"links": map[string]any{"wiki": "x"}}, false); !IsKind(err, KindBadRequest) {
		t.Errorf("unknown link kind: %v", err)
	}
	if _, err := s.PatchFrontMatter(ctx, id, map[string]any{"links": "nope"}, false); !IsKind(err, KindBadRequest) {
		t.Errorf("non-object links patch: %v", err)
	}
	// links.plans inside a links patch is checked like addPlanLink.
	if _, err := s.PatchFrontMatter(ctx, id, map[string]any{"links": map[string]any{"plans": [][]string{{"0099-zzz", "blocks"}}}}, false); !IsKind(err, KindNotFound) {
		t.Errorf("patch links.plans to missing plan: %v", err)
	}
	if _, err := s.PatchFrontMatter(ctx, id, map[string]any{"plans": [][]string{{id, "blocks"}}}, false); !IsKind(err, KindBadRequest) {
		t.Errorf("patch plans self link: %v", err)
	}
	// Patch is validated as a whole: a bad URL fails and nothing is written.
	before, _ := s.Get(ctx, id)
	if _, err := s.PatchFrontMatter(ctx, id, map[string]any{"links": map[string]any{"web": map[string]any{"x": "ftp://nope"}}}, false); !IsKind(err, KindValidation) {
		t.Errorf("bad url: %v", err)
	}
	after, _ := s.Get(ctx, id)
	if !store.PlansEqual(before, after) {
		t.Error("failed patch must not write")
	}
}

func TestLinks(t *testing.T) {
	s, _, _ := newService(t)
	a := create(t, s, "create-dsgn.json")
	b := create(t, s, "create-dsgn.json")
	if _, err := s.AddPlanLink(ctx, a.ID(), "0099-zzz", "blocks"); !IsKind(err, KindNotFound) {
		t.Errorf("link to missing plan: %v", err)
	}
	if _, err := s.AddPlanLink(ctx, a.ID(), a.ID(), "blocks"); !IsKind(err, KindBadRequest) {
		t.Errorf("self link: %v", err)
	}
	if _, err := s.AddPlanLink(ctx, a.ID(), b.ID(), "owns"); !IsKind(err, KindBadRequest) {
		t.Errorf("bad relation: %v", err)
	}
	p, err := s.AddPlanLink(ctx, a.ID(), b.ID(), "blocks")
	if err != nil {
		t.Fatal(err)
	}
	p, _ = s.AddPlanLink(ctx, a.ID(), b.ID(), "blocks") // duplicate ignored
	p, _ = s.AddPlanLink(ctx, a.ID(), b.ID(), "depends")
	if len(p.FrontMatter.Links.Plans) != 2 {
		t.Errorf("plans = %v", p.FrontMatter.Links.Plans)
	}
	list, _ := s.List(ctx, ListFilter{Plan: b.ID()})
	if len(list) != 1 || list[0].FrontMatter.ID != a.ID() {
		t.Errorf("list by plan: %v", list)
	}
	// Delete protection.
	if _, err := s.Delete(ctx, b.ID(), false); !IsKind(err, KindLinkedPlan) {
		t.Errorf("delete linked: %v", err)
	}
	p, _ = s.RemovePlanLink(ctx, a.ID(), b.ID(), "depends")
	if len(p.FrontMatter.Links.Plans) != 1 {
		t.Error("remove one relation")
	}
	p, _ = s.RemovePlanLink(ctx, a.ID(), b.ID(), "")
	if p.FrontMatter.Links != nil {
		t.Errorf("links should be nil once the last plan link is removed: %+v", p.FrontMatter.Links)
	}
	if _, err := s.RemovePlanLink(ctx, a.ID(), b.ID(), ""); !IsKind(err, KindNotFound) {
		t.Error("remove missing link")
	}
	// Stories, specs, web, repo.
	for _, bad := range []string{"abc", "001-abc", "0001", "0001-ABC"} {
		if _, err := s.AddStoryLink(ctx, a.ID(), bad, "included"); !IsKind(err, KindBadRequest) {
			t.Errorf("story id %q accepted", bad)
		}
	}
	if _, err := s.AddStoryLink(ctx, a.ID(), "0001-abc", "parent"); !IsKind(err, KindBadRequest) {
		t.Error("parent is not a story relation")
	}
	p, _ = s.AddStoryLink(ctx, a.ID(), "0001-abc", "included")
	list, _ = s.List(ctx, ListFilter{Story: "0001-abc"})
	if len(list) != 1 {
		t.Error("list by story")
	}
	p, _ = s.AddSpec(ctx, a.ID(), "./docs/x.md")
	p, _ = s.SetWebLink(ctx, a.ID(), "jira", "https://jira/1")
	p, _ = s.SetWebLink(ctx, a.ID(), "jira", "https://jira/2")
	p, err = s.SetRepo(ctx, a.ID(), "git@github.com:org/p.git", "")
	if err != nil {
		t.Fatal(err)
	}
	p, _ = s.SetRepo(ctx, a.ID(), "", "~/Projects/p")
	l := p.FrontMatter.Links
	if l.Web["jira"] != "https://jira/2" || l.Specs[0] != "./docs/x.md" || l.Repo.Remote != "git@github.com:org/p.git" || l.Repo.Local != "~/Projects/p" || l.Stories[0].ID != "0001-abc" {
		t.Errorf("links: %+v", l)
	}
	if _, err := s.SetWebLink(ctx, a.ID(), "bad", "not a url"); !IsKind(err, KindValidation) {
		t.Errorf("bad url: %v", err)
	}
	p, _ = s.RemoveStoryLink(ctx, a.ID(), "0001-abc", "")
	p, _ = s.RemoveSpec(ctx, a.ID(), "./docs/x.md")
	p, _ = s.RemoveWebLink(ctx, a.ID(), "jira")
	p, _ = s.ClearRepo(ctx, a.ID())
	if p.FrontMatter.Links != nil {
		t.Errorf("links should be nil once empty: %+v", p.FrontMatter.Links)
	}
	if _, err := s.RemoveWebLink(ctx, a.ID(), "jira"); !IsKind(err, KindNotFound) {
		t.Error("remove missing web link")
	}
	// Now delete works and reports.
	res, err := s.Delete(ctx, b.ID(), false)
	if err != nil || res.ID != b.ID() {
		t.Errorf("delete: %v %v", res, err)
	}
	if _, err := s.Get(ctx, b.ID()); !IsKind(err, KindNotFound) {
		t.Error("deleted plan still resolves")
	}
}

func TestProgress(t *testing.T) {
	s, _, _ := newService(t)
	p := create(t, s, "create-impl.json")
	id := p.ID()
	rep, _ := s.GetProgress(ctx, id)
	if strings.Join(rep.Sections, ",") != "1,1.1,2" || len(rep.Progress) != 3 {
		t.Errorf("progress report: %+v", rep)
	}
	if _, err := s.SetProgress(ctx, id, "9.9", "done"); !IsKind(err, KindUnknownSection) {
		t.Errorf("unknown section: %v", err)
	}
	if _, err := s.SetProgress(ctx, id, "x", "done"); !IsKind(err, KindBadRequest) {
		t.Errorf("bad section number: %v", err)
	}
	p, err := s.SetProgress(ctx, id, "1.1", "done")
	if err != nil {
		t.Fatal(err)
	}
	if p.FrontMatter.Progress["1.1"].Status != "complete" {
		t.Error("progress synonym")
	}
	p, _ = s.AddProgressStory(ctx, id, "1.1", "0001-abc")
	p, _ = s.AddProgressStory(ctx, id, "1.1", "0001-abc")
	if len(p.FrontMatter.Progress["1.1"].Stories) != 1 {
		t.Error("duplicate story")
	}
	list, _ := s.List(ctx, ListFilter{Story: "0001-abc"})
	if len(list) != 1 {
		t.Error("list by progress story")
	}
	if _, err := s.RemoveProgressStory(ctx, id, "1.1", "0002-xyz"); !IsKind(err, KindNotFound) {
		t.Error("remove missing story")
	}
	p, _ = s.RemoveProgressStory(ctx, id, "1.1", "0001-abc")
	if len(p.FrontMatter.Progress["1.1"].Stories) != 0 {
		t.Error("remove story")
	}
	p, _ = s.RemoveProgress(ctx, id, "1.1")
	if _, ok := p.FrontMatter.Progress["1.1"]; ok {
		t.Error("remove progress")
	}
	if _, err := s.RemoveProgress(ctx, id, "1.1"); !IsKind(err, KindUnknownSection) {
		t.Error("remove missing progress")
	}
	// Removing a heading from the content leaves a dangling key that validate reports.
	p, _ = s.Update(ctx, id, "# Add plan service\n\n## Phase 1: Core\n")
	v, _ := s.Validate(ctx, id)
	if v.Valid || !strings.Contains(strings.Join(v.Problems, "\n"), "/progress/2") {
		t.Errorf("validate should flag dangling progress: %+v", v)
	}
}

func TestUpdateAndTemplates(t *testing.T) {
	s, _, _ := newService(t)
	p := create(t, s, "create-dsgn.json")
	id := p.ID()
	// Content without H1 gets one; a different H1 is rewritten to the title.
	p, _ = s.Update(ctx, id, "## Summary\n\nnew\n")
	if !strings.HasPrefix(p.Content, "# Plan service design\n\n## Summary") {
		t.Errorf("update prepends H1: %q", p.Content)
	}
	p, _ = s.Update(ctx, id, "# Wrong\n\nbody\n")
	if h, _ := firstLine(p.Content); h != "# Plan service design" {
		t.Errorf("update rewrites H1: %q", h)
	}
	// A whole document keeps only the body.
	p, _ = s.Update(ctx, id, "---\nid: 0000-xxx\ntitle: Hijack\n---\n\n# Plan service design\n\nfrom doc\n")
	if p.ID() != id || !strings.Contains(p.Content, "from doc") || strings.Contains(p.Content, "Hijack") {
		t.Errorf("update with document: %+v", p)
	}
	// Templates.
	tpl, _ := s.GetTemplate(ctx, "")
	if tpl.ID != "default" || !strings.Contains(tpl.Content, "## Summary") {
		t.Error("default template")
	}
	if _, err := s.CreateTemplate(ctx, "tiny", "{{ .Title "); !IsKind(err, KindValidation) {
		t.Errorf("broken template: %v", err)
	}
	if _, err := s.CreateTemplate(ctx, "../x", "# x"); !IsKind(err, KindBadRequest) {
		t.Error("bad template id")
	}
	if _, err := s.CreateTemplate(ctx, "tiny", "# {{ .Title }}\n\nGoal: {{ .Body.Summary.Goal }}\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTemplate(ctx, "tiny", "# x"); !IsKind(err, KindConflict) {
		t.Error("duplicate template")
	}
	list, _ := s.ListTemplates(ctx)
	if len(list) != 2 || list[0].ID != "default" || list[1].ID != "tiny" {
		t.Errorf("templates: %v", list)
	}
	p2, err := s.Create(ctx, input(t, "create-dsgn.json"), "tiny")
	if err != nil {
		t.Fatal(err)
	}
	if p2.Content != "# Plan service design\n\nGoal: Decide the shape of the plan service.\n" {
		t.Errorf("tiny template output: %q", p2.Content)
	}
	if _, err := s.UpdateTemplate(ctx, "missing", "# x"); !IsKind(err, KindNotFound) {
		t.Error("update missing template")
	}
	if _, err := s.UpdateTemplate(ctx, "default", "# {{ .Title }}\n"); err != nil {
		t.Fatal(err)
	}
	tpl, _ = s.GetTemplate(ctx, "default")
	if tpl.Content != "# {{ .Title }}\n" {
		t.Error("default override")
	}
	if err := s.DeleteTemplate(ctx, "default"); err != nil {
		t.Fatal(err)
	}
	tpl, _ = s.GetTemplate(ctx, "default")
	if !strings.Contains(tpl.Content, "## Summary") {
		t.Error("deleting the override restores the built-in")
	}
	if err := s.DeleteTemplate(ctx, "default"); !IsKind(err, KindNotFound) {
		t.Error("deleting built-in default")
	}
	if _, err := s.Create(ctx, input(t, "create-dsgn.json"), "nope"); !IsKind(err, KindNotFound) {
		t.Error("create with missing template")
	}
}

func firstLine(s string) (string, bool) {
	i := strings.Index(s, "\n")
	if i < 0 {
		return s, true
	}
	return s[:i], true
}

func TestFanoutAndSync(t *testing.T) {
	s, mem, _ := newService(t)
	p := create(t, s, "create-dsgn.json")
	mem.FailWrites = os.ErrPermission
	got, err := s.SetPriority(ctx, p.ID(), "P0")
	if got == nil || !IsKind(err, KindStorage) || !AsError(err).Partial {
		t.Fatalf("partial write: %v %v", got, err)
	}
	mem.FailWrites = nil
	if _, err := s.Sync(ctx, "file", "nope", "skip"); !IsKind(err, KindNotFound) {
		t.Errorf("sync unknown store: %v", err)
	}
	if _, err := s.Sync(ctx, "file", "mem", "weird"); !IsKind(err, KindBadRequest) {
		t.Errorf("sync bad mode: %v", err)
	}
	rep, err := s.Sync(ctx, "file", "mem", "error")
	if !IsKind(err, KindConflict) || len(rep.Conflicts) != 1 {
		t.Errorf("sync conflict: %+v %v", rep, err)
	}
	rep, err = s.Sync(ctx, "file", "mem", "overwrite")
	if err != nil || len(rep.Overwritten) != 1 {
		t.Errorf("sync overwrite: %+v %v", rep, err)
	}
	m, _ := mem.GetPlan(ctx, p.ID())
	if m.FrontMatter.Priority != "0" {
		t.Error("sync did not copy")
	}
	if strings.Join(s.StoreNames(), ",") != "file (file),mem (memory)" {
		t.Errorf("store names: %v", s.StoreNames())
	}
}

func TestEvents(t *testing.T) {
	s, _, _ := newService(t)
	sub := s.Bus().Subscribe(nil, 16)
	p := create(t, s, "create-dsgn.json")
	s.SetPriority(ctx, p.ID(), "P5")
	s.Delete(ctx, p.ID(), false)
	var types []string
	for len(sub.C) > 0 {
		e := <-sub.C
		types = append(types, e.Type+":"+e.Operation)
	}
	if strings.Join(types, ",") != "plan.created:create,plan.updated:setPriority,plan.deleted:delete" {
		t.Errorf("events: %v", types)
	}
}

func TestErrorMapping(t *testing.T) {
	e := AsError(os.ErrNotExist)
	if e.Kind != KindStorage || e.HTTPStatus() != 500 {
		t.Error("unknown errors map to StorageError")
	}
	if (&Error{Kind: KindNotFound}).HTTPStatus() != 404 || (&Error{Kind: KindLinkedPlan}).HTTPStatus() != 409 || (&Error{Kind: KindValidation}).HTTPStatus() != 400 {
		t.Error("status mapping")
	}
	if AsError(nil) != nil {
		t.Error("nil")
	}
}

func TestTimestampsNormalisedOnWrite(t *testing.T) {
	s, _, fs := newService(t)
	p := create(t, s, "create-dsgn.json")
	// Write a file by hand with non-canonical timestamps and legacy completed: "".
	doc := "---\nid: " + p.ID() + "\ntitle: Plan service design\ntype: dsgn\nstatus: pending\npriority: \"1\"\ncreated: 2026-01-01T00:00:00Z\nupdated: \"2026-01-01T02:00:00.5+02:00\"\ncompleted: \"\"\n---\n\n# Plan service design\n"
	if err := os.WriteFile(fs.AbsPath(p.Path), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := s.Validate(ctx, p.ID())
	if err != nil || !rep.Valid {
		t.Fatalf("non-canonical timestamps should validate after normalisation: %+v %v", rep, err)
	}
	got, err := s.SetPriority(ctx, p.ID(), "P2")
	if err != nil {
		t.Fatal(err)
	}
	if got.FrontMatter.Created != "2026-01-01T00:00:00.000Z" {
		t.Errorf("created not normalised: %q", got.FrontMatter.Created)
	}
	if got.FrontMatter.Completed != "" {
		t.Errorf("empty completed should stay unset: %q", got.FrontMatter.Completed)
	}
	out, _ := os.ReadFile(fs.AbsPath(p.Path))
	if strings.Contains(string(out), "completed:") || !strings.Contains(string(out), "created: \"2026-01-01T00:00:00.000Z\"") {
		t.Errorf("file not rewritten canonically:\n%s", out)
	}
	// Garbage timestamps are reported, not silently accepted.
	doc = strings.Replace(doc, "created: 2026-01-01T00:00:00Z", "created: yesterday", 1)
	os.WriteFile(fs.AbsPath(p.Path), []byte(doc), 0o644)
	if _, err := s.SetPriority(ctx, p.ID(), "P3"); !IsKind(err, KindValidation) {
		t.Errorf("bad timestamp: %v", err)
	}
}

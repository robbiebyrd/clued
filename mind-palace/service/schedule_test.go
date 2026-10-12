package service

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/kind"
)

func TestPlanningFieldsDefaultOnCreate(t *testing.T) {
	s, _, fs := newService(t)
	p := create(t, s, "create-dsgn.json")
	fm := p.FrontMatter
	if fm.Tags == nil || len(fm.Tags) != 0 || fm.Milestone != "" || fm.DueDate != "" || fm.PlannedStart != "" || fm.PlannedEnd != "" || fm.Order != 0 || fm.Assignee != "" {
		t.Errorf("defaults: %+v", fm)
	}
	raw, err := os.ReadFile(fs.AbsPath(kind.Plan, p.Path))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"tags: []\n", "milestone: \"\"\n", "due_date: \"\"\n", "planned_start: \"\"\n", "planned_end: \"\"\n", "order: 0\n"} {
		if !strings.Contains(string(raw), line) {
			t.Errorf("file lacks %q:\n%s", line, raw)
		}
	}
	if strings.Contains(string(raw), "assignee") {
		t.Errorf("a plan must not carry assignee:\n%s", raw)
	}
	b, _ := json.Marshal(fm)
	if !strings.Contains(string(b), `"tags":[]`) || strings.Contains(string(b), `"assignee"`) {
		t.Errorf("json: %s", b)
	}
}

func TestPlanningFieldsOnCreate(t *testing.T) {
	s, _, _ := newService(t)
	in := `{"frontMatter":{"title":"Planned","type":"drft","priority":"2","tags":[" security","feature","security"],"milestone":" milestone_beta ","due_date":"2026-03-01T10:00:00Z","planned_start":"2026-02-01","planned_end":"2026-02-28","order":"3"},"body":{"summary":{"goal":"g","problem":"p"},"design":{}}}`
	p, err := s.Create(ctx, json.RawMessage(in), "")
	if err != nil {
		t.Fatal(err)
	}
	fm := p.FrontMatter
	if strings.Join(fm.Tags, ",") != "security,feature" || fm.Milestone != "milestone_beta" || fm.DueDate != "2026-03-01" || fm.PlannedStart != "2026-02-01" || fm.PlannedEnd != "2026-02-28" || fm.Order != 3 {
		t.Errorf("normalised: %+v", fm)
	}
	for name, bad := range map[string]string{
		"end before start": `"planned_start":"2026-02-28","planned_end":"2026-02-01"`,
		"bad date":         `"due_date":"next week"`,
		"tag with space":   `"tags":["two words"]`,
		"assignee on plan": `"assignee":"github:octocat"`,
		"float order":      `"order":1.5`,
	} {
		in := `{"frontMatter":{"title":"Bad","type":"drft","priority":"2",` + bad + `},"body":{"summary":{"goal":"g","problem":"p"},"design":{}}}`
		if _, err := s.Create(ctx, json.RawMessage(in), ""); !IsKind(err, KindValidation) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestPlanningFieldSetters(t *testing.T) {
	s, _, _ := newService(t)
	p := create(t, s, "create-dsgn.json")
	id := p.ID()
	p, err := s.SetTags(ctx, id, []string{"feature", " security ", "feature", ""})
	if err != nil || strings.Join(p.FrontMatter.Tags, ",") != "feature,security" {
		t.Errorf("setTags: %v %v", p.FrontMatter.Tags, err)
	}
	if _, err := s.SetTags(ctx, id, []string{"two words"}); !IsKind(err, KindBadRequest) {
		t.Errorf("tag with whitespace: %v", err)
	}
	p, _ = s.AddTag(ctx, id, "security") // no-op
	p, _ = s.AddTag(ctx, id, "docs")
	if strings.Join(p.FrontMatter.Tags, ",") != "feature,security,docs" {
		t.Errorf("addTag: %v", p.FrontMatter.Tags)
	}
	p, _ = s.RemoveTag(ctx, id, "security")
	p, _ = s.RemoveTag(ctx, id, "absent")
	if strings.Join(p.FrontMatter.Tags, ",") != "feature,docs" {
		t.Errorf("removeTag: %v", p.FrontMatter.Tags)
	}
	p, _ = s.SetTags(ctx, id, nil)
	if p.FrontMatter.Tags == nil || len(p.FrontMatter.Tags) != 0 {
		t.Errorf("clear tags: %#v", p.FrontMatter.Tags)
	}
	p, _ = s.SetMilestone(ctx, id, " milestone_beta ")
	if p.FrontMatter.Milestone != "milestone_beta" {
		t.Errorf("milestone: %q", p.FrontMatter.Milestone)
	}
	p, err = s.SetDueDate(ctx, id, "2026-03-01 10:00:00 UTC")
	if err != nil || p.FrontMatter.DueDate != "2026-03-01" {
		t.Errorf("dueDate: %q %v", p.FrontMatter.DueDate, err)
	}
	if _, err := s.SetDueDate(ctx, id, "soon"); !IsKind(err, KindBadRequest) {
		t.Errorf("bad due date: %v", err)
	}
	p, err = s.SetPlannedDates(ctx, id, "2026-02-01", "2026-02-28")
	if err != nil || p.FrontMatter.PlannedStart != "2026-02-01" || p.FrontMatter.PlannedEnd != "2026-02-28" {
		t.Errorf("planned: %+v %v", p.FrontMatter, err)
	}
	if _, err := s.SetPlannedDates(ctx, id, "2026-02-28", "2026-02-01"); !IsKind(err, KindBadRequest) {
		t.Errorf("end before start: %v", err)
	}
	p, _ = s.SetPlannedDates(ctx, id, "", "2026-02-28")
	if p.FrontMatter.PlannedStart != "" || p.FrontMatter.PlannedEnd != "2026-02-28" {
		t.Errorf("clear start: %+v", p.FrontMatter)
	}
	p, _ = s.SetOrder(ctx, id, 7)
	if p.FrontMatter.Order != 7 {
		t.Errorf("order: %d", p.FrontMatter.Order)
	}
	if _, err := s.SetAssignee(ctx, id, "github:octocat"); !IsKind(err, KindBadRequest) {
		t.Errorf("plans have no assignee: %v", err)
	}
	// Patch covers every field, with the same rules.
	p, err = s.PatchFrontMatter(ctx, id, map[string]any{"tags": []any{"a", "b"}, "milestone": "m1", "due_date": "2026-04-01", "planned_start": "2026-03-01", "planned_end": "2026-03-31", "order": json.Number("2")}, false)
	if err != nil {
		t.Fatal(err)
	}
	fm := p.FrontMatter
	if strings.Join(fm.Tags, ",") != "a,b" || fm.Milestone != "m1" || fm.DueDate != "2026-04-01" || fm.PlannedStart != "2026-03-01" || fm.PlannedEnd != "2026-03-31" || fm.Order != 2 {
		t.Errorf("patch: %+v", fm)
	}
	if _, err := s.PatchFrontMatter(ctx, id, map[string]any{"planned_end": "2026-02-01"}, false); !IsKind(err, KindBadRequest) {
		t.Errorf("patch end before start: %v", err)
	}
	if _, err := s.PatchFrontMatter(ctx, id, map[string]any{"order": "x"}, false); !IsKind(err, KindBadRequest) {
		t.Errorf("patch bad order: %v", err)
	}
	if _, err := s.PatchFrontMatter(ctx, id, map[string]any{"assignee": "github:octocat"}, false); !IsKind(err, KindBadRequest) {
		t.Errorf("patch assignee on plan: %v", err)
	}
	p, _ = s.PatchFrontMatter(ctx, id, map[string]any{"tags": nil, "milestone": nil, "due_date": "", "order": nil}, false)
	if len(p.FrontMatter.Tags) != 0 || p.FrontMatter.Milestone != "" || p.FrontMatter.DueDate != "" || p.FrontMatter.Order != 0 {
		t.Errorf("patch clears: %+v", p.FrontMatter)
	}
}

func TestStoryAssigneeAndListFilters(t *testing.T) {
	plans, _, _ := newService(t)
	stories := plans.Palace().Stories()
	a := createStory(t, stories, `,"assignee":"github:octocat","tags":["security"],"milestone":"m1"`)
	b := createStory(t, stories, `,"tags":["feature"],"milestone":"m2"`)
	if a.FrontMatter.Assignee != "github:octocat" || b.FrontMatter.Assignee != "" {
		t.Errorf("assignee on create: %q %q", a.FrontMatter.Assignee, b.FrontMatter.Assignee)
	}
	d, err := stories.SetAssignee(ctx, b.ID(), " github:hubot ")
	if err != nil || d.FrontMatter.Assignee != "github:hubot" {
		t.Errorf("setAssignee: %q %v", d.FrontMatter.Assignee, err)
	}
	if _, err := stories.SetAssignee(ctx, b.ID(), "two words"); !IsKind(err, KindBadRequest) {
		t.Errorf("assignee with whitespace: %v", err)
	}
	d, _ = stories.PatchFrontMatter(ctx, b.ID(), map[string]any{"assignee": ""}, false)
	if d.FrontMatter.Assignee != "" {
		t.Errorf("patch clears assignee: %q", d.FrontMatter.Assignee)
	}
	ids := func(f ListFilter) string {
		out, err := stories.List(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		var s []string
		for _, x := range out {
			s = append(s, x.FrontMatter.ID)
		}
		return strings.Join(s, ",")
	}
	if got := ids(ListFilter{Tag: "security"}); got != a.ID() {
		t.Errorf("tag filter: %q", got)
	}
	if got := ids(ListFilter{Milestone: "m2"}); got != b.ID() {
		t.Errorf("milestone filter: %q", got)
	}
	if got := ids(ListFilter{Assignee: "github:octocat"}); got != a.ID() {
		t.Errorf("assignee filter: %q", got)
	}
	if got := ids(ListFilter{Tag: "nope"}); got != "" {
		t.Errorf("no match: %q", got)
	}
}

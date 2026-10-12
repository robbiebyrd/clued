package service

import (
	"testing"
)

// finishStory completes every step and checks every criterion of a story
// without closing it: the steps complete first while the criteria are still
// open (so auto-complete stays off), then the criteria are checked.
func finishStory(t *testing.T, stories *Service, id string) {
	t.Helper()
	if _, err := stories.SetStatus(ctx, id, "in_progress", false); err != nil {
		t.Fatal(err)
	}
	for _, step := range []string{"1", "1.1", "2"} {
		if _, err := stories.SetProgress(ctx, id, step, "complete"); err != nil {
			t.Fatal(err)
		}
	}
	for pos := 1; pos <= 2; pos++ {
		if _, err := stories.SetCriterion(ctx, id, pos, "done"); err != nil {
			t.Fatal(err)
		}
	}
	d, err := stories.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if d.FrontMatter.Status != "in_progress" {
		t.Fatalf("fixture: status = %q, want the story left open", d.FrontMatter.Status)
	}
}

func TestReviewReportsStoryWithAllStepsAndCriteriaDone(t *testing.T) {
	plans, _, _ := newService(t)
	stories := plans.Palace().Stories()
	done := createStory(t, stories, "")
	finishStory(t, stories, done.ID())
	open := createStory(t, stories, "")
	if _, err := stories.SetStatus(ctx, open.ID(), "in_progress", false); err != nil {
		t.Fatal(err)
	}
	if _, err := stories.SetProgress(ctx, open.ID(), "1", "complete"); err != nil {
		t.Fatal(err)
	}

	r, err := plans.Palace().Review(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Stories) != 1 || r.Stories[0].ID != done.ID() || r.Stories[0].Status != "in_progress" {
		t.Fatalf("stories = %+v", r.Stories)
	}
	if r.Stories[0].Reason != "all 3 steps are complete and all 2 acceptance criteria are done" {
		t.Errorf("reason = %q", r.Stories[0].Reason)
	}
	if len(r.Plans) != 0 {
		t.Errorf("plans = %+v, want none", r.Plans)
	}
}

func TestReviewSkipsStoriesWithOpenCriteriaOrSettledStatus(t *testing.T) {
	plans, _, _ := newService(t)
	stories := plans.Palace().Stories()
	criteriaOpen := createStory(t, stories, "")
	if _, err := stories.SetStatus(ctx, criteriaOpen.ID(), "in_progress", false); err != nil {
		t.Fatal(err)
	}
	for _, step := range []string{"1", "1.1", "2"} {
		if _, err := stories.SetProgress(ctx, criteriaOpen.ID(), step, "complete"); err != nil {
			t.Fatal(err)
		}
	}
	closed := createStory(t, stories, "")
	finishStory(t, stories, closed.ID())
	if _, err := stories.SetStatus(ctx, closed.ID(), "complete", false); err != nil {
		t.Fatal(err)
	}

	r, err := plans.Palace().Review(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !r.IsEmpty() {
		t.Errorf("review = %+v, want empty", r)
	}
}

func TestReviewReportsPlanWhoseStoriesAreAllComplete(t *testing.T) {
	plans, _, _ := newService(t)
	stories := plans.Palace().Stories()
	plan := create(t, plans, "create-impl.json")
	// The fixture links a story that does not exist; a dangling story can
	// never be complete, so drop it to review the real links.
	if _, err := plans.RemoveStoryLink(ctx, plan.ID(), "0001-abc", "included"); err != nil {
		t.Fatal(err)
	}
	linkedFromStory := createStory(t, stories, "")
	if _, err := stories.AddPlanLink(ctx, linkedFromStory.ID(), plan.ID(), "included", nil); err != nil {
		t.Fatal(err)
	}
	linkedFromPlan := createStory(t, stories, "")
	if _, err := plans.AddStoryLink(ctx, plan.ID(), linkedFromPlan.ID(), "included"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{linkedFromStory.ID(), linkedFromPlan.ID()} {
		finishStory(t, stories, id)
		if _, err := stories.SetStatus(ctx, id, "complete", false); err != nil {
			t.Fatal(err)
		}
	}
	noStories := create(t, plans, "create-dsgn.json")

	r, err := plans.Palace().Review(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Plans) != 1 || r.Plans[0].ID != plan.ID() {
		t.Fatalf("plans = %+v, want only %s (not %s, which has no stories)", r.Plans, plan.ID(), noStories.ID())
	}
	if got := r.Plans[0].Stories; len(got) != 2 || got[0] != linkedFromStory.ID() || got[1] != linkedFromPlan.ID() {
		t.Errorf("stories = %v", got)
	}
	if r.Plans[0].Reason != "all 2 linked stories are complete" {
		t.Errorf("reason = %q", r.Plans[0].Reason)
	}
	if len(r.Stories) != 0 {
		t.Errorf("stories = %+v, want none (they are complete)", r.Stories)
	}
}

func TestReviewSkipsPlanWithAnOpenStory(t *testing.T) {
	plans, _, _ := newService(t)
	stories := plans.Palace().Stories()
	plan := create(t, plans, "create-impl.json")
	// The fixture links a story that does not exist; a dangling story can
	// never be complete, so drop it to review the real links.
	if _, err := plans.RemoveStoryLink(ctx, plan.ID(), "0001-abc", "included"); err != nil {
		t.Fatal(err)
	}
	done := createStory(t, stories, "")
	finishStory(t, stories, done.ID())
	if _, err := stories.SetStatus(ctx, done.ID(), "complete", false); err != nil {
		t.Fatal(err)
	}
	open := createStory(t, stories, "")
	for _, id := range []string{done.ID(), open.ID()} {
		if _, err := plans.AddStoryLink(ctx, plan.ID(), id, "included"); err != nil {
			t.Fatal(err)
		}
	}
	r, err := plans.Palace().Review(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Plans) != 0 {
		t.Errorf("plans = %+v, want none", r.Plans)
	}
}

func TestReviewIsEmptyWithoutDocuments(t *testing.T) {
	plans, _, _ := newService(t)
	r, err := plans.Palace().Review(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !r.IsEmpty() || r.Plans == nil || r.Stories == nil {
		t.Errorf("review = %+v, want empty non-nil lists", r)
	}
}

func TestReviewSkipsPlanWithDanglingStoryLink(t *testing.T) {
	plans, _, _ := newService(t)
	// create-impl.json links story 0001-abc, which does not exist.
	create(t, plans, "create-impl.json")
	r, err := plans.Palace().Review(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Plans) != 0 {
		t.Errorf("plans = %+v, want none: a missing story cannot be complete", r.Plans)
	}
}

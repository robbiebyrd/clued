package service

import (
	"strings"
	"sync"
	"testing"
)

func TestDependenciesGateStartAndCompletion(t *testing.T) {
	plans, _, _ := newService(t)
	stories := plans.Palace().Stories()
	a := createStory(t, stories, "")
	b := createStory(t, stories, "")
	c := createStory(t, stories, "")
	if _, err := stories.AddStoryLink(ctx, a.ID(), b.ID(), "depends"); err != nil {
		t.Fatal(err)
	}
	// c blocks a: a cannot complete until c does.
	if _, err := stories.AddStoryLink(ctx, c.ID(), a.ID(), "blocks"); err != nil {
		t.Fatal(err)
	}

	_, err := stories.SetStatus(ctx, a.ID(), "in_progress", false)
	if !IsKind(err, KindBlockedByDependency) || !strings.Contains(err.Error(), b.ID()) || strings.Contains(err.Error(), c.ID()) {
		t.Fatalf("starting with an open dependency: %v", err)
	}
	if deps, err := stories.Dependencies(ctx, a.ID()); err != nil || len(deps) != 2 || deps[0].Relation != "depends" || deps[1].Relation != "blocked_by" {
		t.Fatalf("Dependencies = %+v, %v", deps, err)
	}
	if _, err := stories.SetStatus(ctx, b.ID(), "rejected", false); err != nil {
		t.Fatal(err)
	}
	if _, err := stories.SetStatus(ctx, a.ID(), "in_progress", false); err != nil {
		t.Fatalf("start once the dependency is rejected: %v", err)
	}
	for _, pos := range []int{1, 2} {
		if _, err := stories.SetCriterion(ctx, a.ID(), pos, "done"); err != nil {
			t.Fatal(err)
		}
	}
	_, err = stories.SetStatus(ctx, a.ID(), "complete", false)
	if !IsKind(err, KindBlockedByDependency) || !strings.Contains(err.Error(), c.ID()) || !strings.Contains(err.Error(), "blocks it") {
		t.Fatalf("completing while blocked: %v", err)
	}
	// Finishing every step does not complete it either while c blocks it.
	for _, step := range []string{"1", "1.1", "2"} {
		if _, err := stories.SetProgress(ctx, a.ID(), step, "complete"); err != nil {
			t.Fatal(err)
		}
	}
	if d, _ := stories.Get(ctx, a.ID()); d.FrontMatter.Status != "in_progress" {
		t.Fatalf("auto-completed past a blocker: %s", d.FrontMatter.Status)
	}
	if _, err := stories.SetStatus(ctx, a.ID(), "complete", true); err != nil {
		t.Fatalf("force overrides: %v", err)
	}
}

func TestDependsOnAPlan(t *testing.T) {
	plans, _, _ := newService(t)
	stories := plans.Palace().Stories()
	p := create(t, plans, "create-dsgn.json")
	s := createStory(t, stories, "")
	if _, err := stories.AddPlanLink(ctx, s.ID(), p.ID(), "depends", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := stories.SetStatus(ctx, s.ID(), "in_progress", false); !IsKind(err, KindBlockedByDependency) {
		t.Fatalf("story depending on an open plan started: %v", err)
	}
}

func TestReadyListsStartableWorkInPriorityOrder(t *testing.T) {
	plans, _, _ := newService(t)
	stories := plans.Palace().Stories()
	low := createStory(t, stories, `, "priority": "4"`)
	high := createStory(t, stories, `, "priority": "0", "tags": ["api"]`)
	blocked := createStory(t, stories, `, "priority": "0"`)
	pending := createStory(t, stories, `, "status": "pending"`)
	mine := createStory(t, stories, `, "priority": "2", "assignee": "github:someone"`)
	if _, err := stories.AddStoryLink(ctx, blocked.ID(), low.ID(), "depends"); err != nil {
		t.Fatal(err)
	}
	ids := func(f ReadyFilter) string {
		list, err := stories.Ready(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, s := range list {
			out = append(out, s.FrontMatter.ID)
		}
		return strings.Join(out, ",")
	}
	if got, want := ids(ReadyFilter{}), strings.Join([]string{high.ID(), mine.ID(), low.ID()}, ","); got != want {
		t.Errorf("ready = %s, want %s (the blocked and the pending story are left out)", got, want)
	}
	if got := ids(ReadyFilter{Unassigned: true, Limit: 1}); got != high.ID() {
		t.Errorf("unassigned, limit 1 = %s", got)
	}
	if got := ids(ReadyFilter{Tag: "api"}); got != high.ID() {
		t.Errorf("tag = %s", got)
	}
	_ = pending
}

func TestClaimHasOneWinner(t *testing.T) {
	plans, _, _ := newService(t)
	stories := plans.Palace().Stories()
	s := createStory(t, stories, "")
	var wg sync.WaitGroup
	var mu sync.Mutex
	winners, conflicts := []string{}, 0
	for i := 0; i < 8; i++ {
		who := "github:agent" + string(rune('a'+i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := stories.Claim(ctx, s.ID(), who, false)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				winners = append(winners, d.FrontMatter.Assignee)
			case IsKind(err, KindConflict) || IsKind(err, KindInvalidTransition):
				conflicts++
			default:
				t.Errorf("claim by %s: %v", who, err)
			}
		}()
	}
	wg.Wait()
	if len(winners) != 1 || conflicts != 7 {
		t.Fatalf("winners %v, conflicts %d", winners, conflicts)
	}
	d, _ := stories.Get(ctx, s.ID())
	if d.FrontMatter.Assignee != winners[0] || d.FrontMatter.Status != "in_progress" || d.FrontMatter.Started == "" {
		t.Fatalf("after the claim: %+v", d.FrontMatter)
	}
	if again, err := stories.Claim(ctx, s.ID(), winners[0], false); err != nil || again.FrontMatter.Assignee != winners[0] {
		t.Fatalf("re-claim by the holder: %v", err)
	}
	if _, err := stories.Claim(ctx, s.ID(), "github:other", false); !IsKind(err, KindConflict) {
		t.Fatalf("claim of a held story: %v", err)
	}
	if d, err := stories.Claim(ctx, s.ID(), "github:other", true); err != nil || d.FrontMatter.Assignee != "github:other" {
		t.Fatalf("forced takeover: %v", err)
	}
	if _, err := stories.Claim(ctx, s.ID(), "", false); !IsKind(err, KindBadRequest) {
		t.Fatalf("claim without an assignee: %v", err)
	}
	if _, err := plans.Claim(ctx, "0001", "github:x", false); !IsKind(err, KindBadRequest) {
		t.Fatalf("claim of a plan: %v", err)
	}
	blocked := createStory(t, stories, "")
	dep := createStory(t, stories, "")
	if _, err := stories.AddStoryLink(ctx, blocked.ID(), dep.ID(), "depends"); err != nil {
		t.Fatal(err)
	}
	if _, err := stories.Claim(ctx, blocked.ID(), "github:x", false); !IsKind(err, KindBlockedByDependency) {
		t.Fatalf("claim with an open dependency: %v", err)
	}
}

package service

import (
	"context"
	"fmt"
	"sort"

	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/markdown"
	"github.com/robbiebyrd/clued/mind-palace/model"
)

// ReviewItem is a document whose work is finished while its status is still
// open: a plan whose stories are all complete, or a story whose steps and
// acceptance criteria are all done. Closing it is the user's call, so the
// review only reports it.
type ReviewItem struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Path   string `json:"path"`
	// Reason says what is finished, in words.
	Reason string `json:"reason"`
	// Stories lists the complete stories of a plan item.
	Stories []string `json:"stories,omitempty"`
}

// Review is the result of Palace.Review.
type Review struct {
	Plans   []ReviewItem `json:"plans"`
	Stories []ReviewItem `json:"stories"`
}

// IsEmpty reports whether nothing needs the user's attention.
func (r *Review) IsEmpty() bool { return len(r.Plans) == 0 && len(r.Stories) == 0 }

// settled statuses need no review: the work is closed one way or another.
var settled = map[string]bool{"complete": true, "archived": true, "rejected": true}

// Review finds plans whose linked stories are all complete and stories whose
// steps and acceptance criteria are all done, but whose own status is still
// open. A plan's stories are those in its links.stories, those listed on its
// progress entries and those whose links.plans name it; a plan with no
// stories is not reported. Documents in complete, archived or rejected
// status are skipped.
func (p *Palace) Review(ctx context.Context) (*Review, error) {
	plans, err := p.store.List(ctx, kind.Plan)
	if err != nil {
		return nil, err
	}
	stories, err := p.store.List(ctx, kind.Story)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*model.Document, len(stories))
	for _, s := range stories {
		byID[s.ID()] = s
	}
	out := &Review{Plans: []ReviewItem{}, Stories: []ReviewItem{}}

	storySvc := p.services[kind.Story]
	for _, s := range stories {
		if settled[s.FrontMatter.Status] || storySvc == nil {
			continue
		}
		steps := markdown.SectionNumbers(s.Content)
		if !storySvc.allStepsComplete(s) {
			continue
		}
		items, ok := markdown.Criteria(s.Content)
		if !ok || len(items) == 0 {
			continue
		}
		done := true
		for _, c := range items {
			if c.State == markdown.StateOpen {
				done = false
				break
			}
		}
		if !done {
			continue
		}
		out.Stories = append(out.Stories, ReviewItem{
			Kind: kind.Story, ID: s.ID(), Title: s.FrontMatter.Title, Status: s.FrontMatter.Status, Path: s.Path,
			Reason: fmt.Sprintf("all %d steps are complete and all %d acceptance criteria are done", len(steps), len(items)),
		})
	}

	for _, pl := range plans {
		if settled[pl.FrontMatter.Status] {
			continue
		}
		ids := planStories(pl, stories)
		if len(ids) == 0 {
			continue
		}
		complete := true
		for _, id := range ids {
			s, ok := byID[id]
			if !ok || s.FrontMatter.Status != "complete" {
				complete = false
				break
			}
		}
		if !complete {
			continue
		}
		out.Plans = append(out.Plans, ReviewItem{
			Kind: kind.Plan, ID: pl.ID(), Title: pl.FrontMatter.Title, Status: pl.FrontMatter.Status, Path: pl.Path,
			Reason: fmt.Sprintf("all %d linked stories are complete", len(ids)), Stories: ids,
		})
	}
	return out, nil
}

// planStories returns the sorted ids of the stories attached to a plan.
func planStories(plan *model.Document, stories []*model.Document) []string {
	set := map[string]bool{}
	for _, l := range plan.FrontMatter.StoryLinks() {
		set[l.ID] = true
	}
	for _, e := range plan.FrontMatter.Progress {
		for _, id := range e.Stories {
			set[id] = true
		}
	}
	for _, s := range stories {
		if hasPlanLink(s.FrontMatter, plan.ID()) {
			set[s.ID()] = true
		}
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

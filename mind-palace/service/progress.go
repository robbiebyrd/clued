package service

import (
	"context"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/events"
	"github.com/robbiebyrd/clued/mind-palace/markdown"
	"github.com/robbiebyrd/clued/mind-palace/model"
)

// ProgressReport is the result of GetProgress.
type ProgressReport struct {
	ID       string         `json:"id"`
	Status   string         `json:"status"`
	Sections []string       `json:"sections"`
	Progress model.Progress `json:"progress"`
}

// GetProgress returns the status of every numbered heading.
func (s *Service) GetProgress(ctx context.Context, identifier string) (*ProgressReport, error) {
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	prog := d.FrontMatter.Progress
	if prog == nil {
		prog = model.Progress{}
	}
	sections := markdown.SectionNumbers(d.Content)
	if sections == nil {
		sections = []string{}
	}
	return &ProgressReport{ID: d.ID(), Status: d.FrontMatter.Status, Sections: sections, Progress: prog}, nil
}

func (s *Service) requireSection(d *model.Document, section string) (string, error) {
	section = strings.TrimSpace(section)
	if !s.kind.ProgressKeyRe.MatchString(section) {
		return "", newErr(KindBadRequest, "%q is not a %s number (e.g. \"2\" or \"1.1\")", section, s.kind.ProgressKeyNoun)
	}
	if !markdown.HasSection(d.Content, section) {
		return "", newErr(s.kind.UnknownProgressKeyError, "%s %s has no numbered heading %q (found: %s)", s.kind.Name, d.ID(), section, strings.Join(markdown.SectionNumbers(d.Content), ", "))
	}
	return section, nil
}

// SetProgress sets a numbered heading's status. For kinds that auto-complete,
// completing the last remaining heading while the document is in_progress
// with every acceptance criterion checked moves it to complete.
func (s *Service) SetProgress(ctx context.Context, identifier, section, status string) (*model.Document, error) {
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	section, err = s.requireSection(d, section)
	if err != nil {
		return nil, err
	}
	st, ok := s.cfg.NormalizeStatus(status)
	if !ok {
		return nil, newErr(KindBadRequest, "unknown status %q (known: %s)", status, strings.Join(s.cfg.StatusNames(), ", "))
	}
	if d.FrontMatter.Progress == nil {
		d.FrontMatter.Progress = model.Progress{}
	}
	e := d.FrontMatter.Progress[section]
	e.Status = st
	d.FrontMatter.Progress[section] = e
	if s.kind.AutoComplete && st == "complete" && d.FrontMatter.Status == "in_progress" && s.allStepsComplete(d) {
		if err := s.criteriaGate(d); err == nil {
			if _, err := s.applyStatus(d, "complete", true); err != nil {
				return nil, err
			}
			d.Path = s.pathFor(d, "")
		}
	}
	return s.save(ctx, d, "setProgress", events.ActionUpdated)
}

// allStepsComplete reports whether every numbered heading in the content has
// a complete progress entry.
func (s *Service) allStepsComplete(d *model.Document) bool {
	sections := markdown.SectionNumbers(d.Content)
	if len(sections) == 0 {
		return false
	}
	for _, n := range sections {
		if d.FrontMatter.Progress[n].Status != "complete" {
			return false
		}
	}
	return true
}

// AddProgressStory links a story to a phase or section (plans).
func (s *Service) AddProgressStory(ctx context.Context, identifier, section, storyID string) (*model.Document, error) {
	if !s.kind.ProgressStories {
		return nil, newErr(KindBadRequest, "%s progress entries do not list stories", s.kind.Name)
	}
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	section, err = s.requireSection(d, section)
	if err != nil {
		return nil, err
	}
	storyID = model.NormalizeID(storyID)
	if !model.StoryIDRe.MatchString(storyID) {
		return nil, newErr(KindBadRequest, "%q is not a story id (expected AAAA-BBB, e.g. 0001-abc)", storyID)
	}
	if d.FrontMatter.Progress == nil {
		d.FrontMatter.Progress = model.Progress{}
	}
	e, ok := d.FrontMatter.Progress[section]
	if !ok {
		e.Status = config.DefaultStatus
	}
	if contains(e.Stories, storyID) {
		return d, nil
	}
	e.Stories = append(e.Stories, storyID)
	d.FrontMatter.Progress[section] = e
	return s.save(ctx, d, "addProgressStory", events.ActionUpdated)
}

// RemoveProgressStory unlinks a story from a phase or section (plans).
func (s *Service) RemoveProgressStory(ctx context.Context, identifier, section, storyID string) (*model.Document, error) {
	if !s.kind.ProgressStories {
		return nil, newErr(KindBadRequest, "%s progress entries do not list stories", s.kind.Name)
	}
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	storyID = model.NormalizeID(storyID)
	e, ok := d.FrontMatter.Progress[section]
	if !ok {
		return nil, newErr(s.kind.UnknownProgressKeyError, "%s %s has no progress entry %q", s.kind.Name, d.ID(), section)
	}
	if !contains(e.Stories, storyID) {
		return nil, newErr(KindNotFound, "section %s of %s %s has no story %s", section, s.kind.Name, d.ID(), storyID)
	}
	var kept []string
	for _, x := range e.Stories {
		if x != storyID {
			kept = append(kept, x)
		}
	}
	e.Stories = kept
	d.FrontMatter.Progress[section] = e
	return s.save(ctx, d, "removeProgressStory", events.ActionUpdated)
}

// RemoveProgress deletes a numbered heading's progress entry.
func (s *Service) RemoveProgress(ctx context.Context, identifier, section string) (*model.Document, error) {
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	section = strings.TrimSpace(section)
	if _, ok := d.FrontMatter.Progress[section]; !ok {
		return nil, newErr(s.kind.UnknownProgressKeyError, "%s %s has no progress entry %q", s.kind.Name, d.ID(), section)
	}
	delete(d.FrontMatter.Progress, section)
	if len(d.FrontMatter.Progress) == 0 {
		d.FrontMatter.Progress = nil
	}
	return s.save(ctx, d, "removeProgress", events.ActionUpdated)
}

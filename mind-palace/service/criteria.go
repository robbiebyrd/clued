package service

import (
	"context"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/events"
	"github.com/robbiebyrd/clued/mind-palace/markdown"
	"github.com/robbiebyrd/clued/mind-palace/model"
)

// CriteriaReport is the result of GetCriteria.
type CriteriaReport struct {
	ID       string               `json:"id"`
	Status   string               `json:"status"`
	Criteria []markdown.Criterion `json:"criteria"`
	// Open counts the items still unchecked.
	Open int `json:"open"`
}

func (s *Service) requireCriteria() error {
	if !s.kind.CriteriaGate {
		return newErr(KindBadRequest, "%ss have no acceptance criteria operations", s.kind.Name)
	}
	return nil
}

// GetCriteria lists the acceptance criteria with position, text, kind and state.
func (s *Service) GetCriteria(ctx context.Context, identifier string) (*CriteriaReport, error) {
	if err := s.requireCriteria(); err != nil {
		return nil, err
	}
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	items, _ := markdown.Criteria(d.Content)
	if items == nil {
		items = []markdown.Criterion{}
	}
	open := 0
	for _, c := range items {
		if c.State == markdown.StateOpen {
			open++
		}
	}
	return &CriteriaReport{ID: d.ID(), Status: d.FrontMatter.Status, Criteria: items, Open: open}, nil
}

func normalizeCriterionState(state string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "open", "todo", "unchecked", "", " ":
		return markdown.StateOpen, true
	case "done", "checked", "complete", "completed", "x":
		return markdown.StateDone, true
	case "not_applicable", "not-applicable", "na", "n/a", "skip", "skipped", "~":
		return markdown.StateNotApplicable, true
	}
	return "", false
}

// SetCriterion sets the state of the item at a 1-based position.
func (s *Service) SetCriterion(ctx context.Context, identifier string, position int, state string) (*model.Document, error) {
	if err := s.requireCriteria(); err != nil {
		return nil, err
	}
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	st, ok := normalizeCriterionState(state)
	if !ok {
		return nil, newErr(KindBadRequest, "unknown criterion state %q (open, done, not_applicable)", state)
	}
	content, err := markdown.SetCriterion(d.Content, position, st)
	if err != nil {
		return nil, newErr(KindUnknownCriterion, "%s %s: %v", s.kind.Name, d.ID(), err)
	}
	d.Content = content
	return s.save(ctx, d, "setCriterion", events.ActionUpdated)
}

// AddCriterion appends an item, optionally marked verify or manual.
func (s *Service) AddCriterion(ctx context.Context, identifier, text, kindName string) (*model.Document, error) {
	if err := s.requireCriteria(); err != nil {
		return nil, err
	}
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, newErr(KindBadRequest, "text must not be empty")
	}
	switch strings.ToLower(strings.TrimSpace(kindName)) {
	case "":
		kindName = ""
	case markdown.KindVerify:
		kindName = markdown.KindVerify
	case markdown.KindManual:
		kindName = markdown.KindManual
	default:
		return nil, newErr(KindBadRequest, "unknown criterion kind %q (verify, manual)", kindName)
	}
	d.Content = markdown.AddCriterion(d.Content, text, kindName)
	return s.save(ctx, d, "addCriterion", events.ActionUpdated)
}

// RemoveCriterion deletes the item at a 1-based position.
func (s *Service) RemoveCriterion(ctx context.Context, identifier string, position int) (*model.Document, error) {
	if err := s.requireCriteria(); err != nil {
		return nil, err
	}
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	content, err := markdown.RemoveCriterion(d.Content, position)
	if err != nil {
		return nil, newErr(KindUnknownCriterion, "%s %s: %v", s.kind.Name, d.ID(), err)
	}
	d.Content = content
	return s.save(ctx, d, "removeCriterion", events.ActionUpdated)
}

// AppendWorkLog appends "### <now> - <entry>" under the Work Log section.
func (s *Service) AppendWorkLog(ctx context.Context, identifier, entry string) (*model.Document, error) {
	if !s.kind.WorkLog {
		return nil, newErr(KindBadRequest, "%ss have no work log", s.kind.Name)
	}
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return nil, newErr(KindBadRequest, "entry must not be empty")
	}
	if strings.Contains(entry, "\n") {
		return nil, newErr(KindBadRequest, "entry must be a single line")
	}
	d.Content = markdown.AppendWorkLog(d.Content, s.timestamp(), entry)
	return s.save(ctx, d, "appendWorkLog", events.ActionUpdated)
}

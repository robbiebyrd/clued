package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/events"
	"github.com/robbiebyrd/clued/mind-palace/model"
)

// Planning fields shared by every kind: tags, milestone, due_date,
// planned_start, planned_end and order, plus assignee for kinds that have
// one. Every setter validates like the schema does so the error names the
// field rather than a JSON pointer.

// normalizeTags trims, drops empties and de-duplicates (first occurrence
// wins); it reports a tag that contains whitespace.
func normalizeTags(tags []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if strings.ContainsAny(t, " \t\r\n") {
			return nil, newErr(KindBadRequest, "tag %q must not contain whitespace", t)
		}
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out, nil
}

func normalizeDate(field, value string) (string, error) {
	n, err := model.NormalizeDate(value)
	if err != nil {
		return "", newErr(KindBadRequest, "%s: %v", field, err)
	}
	return n, nil
}

func normalizeAssignee(assignee string) (string, error) {
	assignee = strings.TrimSpace(assignee)
	if strings.ContainsAny(assignee, " \t\r\n") {
		return "", newErr(KindBadRequest, "assignee %q must not contain whitespace (use provider:handle, e.g. github:octocat)", assignee)
	}
	return assignee, nil
}

// toInt accepts the integer forms a JSON or CLI value can take.
func toInt(field string, v any) (int, error) {
	switch n := v.(type) {
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case float64:
		if n != float64(int(n)) {
			return 0, newErr(KindBadRequest, "%s must be an integer, got %v", field, v)
		}
		return int(n), nil
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, newErr(KindBadRequest, "%s must be an integer, got %v", field, v)
		}
		return int(i), nil
	case string:
		var i int
		if _, err := fmt.Sscanf(strings.TrimSpace(n), "%d", &i); err != nil || fmt.Sprint(i) != strings.TrimSpace(n) {
			return 0, newErr(KindBadRequest, "%s must be an integer, got %q", field, n)
		}
		return i, nil
	}
	return 0, newErr(KindBadRequest, "%s must be an integer, got %v", field, v)
}

// checkPlannedDates refuses a planned_end before planned_start.
func checkPlannedDates(fm *model.FrontMatter) error {
	if fm.PlannedStart != "" && fm.PlannedEnd != "" && fm.PlannedEnd < fm.PlannedStart {
		return newErr(KindBadRequest, "planned_end %s is before planned_start %s", fm.PlannedEnd, fm.PlannedStart)
	}
	return nil
}

// SetTags replaces the tags (an empty list clears them).
func (s *Service) SetTags(ctx context.Context, identifier string, tags []string) (*model.Document, error) {
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	t, err := normalizeTags(tags)
	if err != nil {
		return nil, err
	}
	d.FrontMatter.Tags = t
	return s.save(ctx, d, "setTags", events.ActionUpdated)
}

// AddTag appends a tag unless it is already present.
func (s *Service) AddTag(ctx context.Context, identifier, tag string) (*model.Document, error) {
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(tag) == "" {
		return nil, newErr(KindBadRequest, "tag must not be empty")
	}
	t, err := normalizeTags(append(append([]string{}, d.FrontMatter.Tags...), tag))
	if err != nil {
		return nil, err
	}
	if len(t) == len(d.FrontMatter.Tags) {
		return d, nil
	}
	d.FrontMatter.Tags = t
	return s.save(ctx, d, "addTag", events.ActionUpdated)
}

// RemoveTag removes a tag; removing one that is absent is not an error.
func (s *Service) RemoveTag(ctx context.Context, identifier, tag string) (*model.Document, error) {
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	tag = strings.TrimSpace(tag)
	kept := []string{}
	for _, t := range d.FrontMatter.Tags {
		if t != tag {
			kept = append(kept, t)
		}
	}
	if len(kept) == len(d.FrontMatter.Tags) {
		return d, nil
	}
	d.FrontMatter.Tags = kept
	return s.save(ctx, d, "removeTag", events.ActionUpdated)
}

// SetMilestone sets the milestone (empty clears it).
func (s *Service) SetMilestone(ctx context.Context, identifier, milestone string) (*model.Document, error) {
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	d.FrontMatter.Milestone = strings.TrimSpace(milestone)
	return s.save(ctx, d, "setMilestone", events.ActionUpdated)
}

// SetDueDate sets due_date from a YYYY-MM-DD date or an accepted timestamp
// (its UTC day is kept); empty clears it.
func (s *Service) SetDueDate(ctx context.Context, identifier, date string) (*model.Document, error) {
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	n, err := normalizeDate("due_date", date)
	if err != nil {
		return nil, err
	}
	d.FrontMatter.DueDate = n
	return s.save(ctx, d, "setDueDate", events.ActionUpdated)
}

// SetPlannedDates sets planned_start and planned_end together; an empty
// value clears that date, and the end may not be before the start.
func (s *Service) SetPlannedDates(ctx context.Context, identifier, start, end string) (*model.Document, error) {
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	if d.FrontMatter.PlannedStart, err = normalizeDate("planned_start", start); err != nil {
		return nil, err
	}
	if d.FrontMatter.PlannedEnd, err = normalizeDate("planned_end", end); err != nil {
		return nil, err
	}
	if err := checkPlannedDates(&d.FrontMatter); err != nil {
		return nil, err
	}
	return s.save(ctx, d, "setPlannedDates", events.ActionUpdated)
}

// SetOrder sets the sort position.
func (s *Service) SetOrder(ctx context.Context, identifier string, order int) (*model.Document, error) {
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	d.FrontMatter.Order = order
	return s.save(ctx, d, "setOrder", events.ActionUpdated)
}

// SetAssignee sets the assignee (kinds that have one); empty clears it.
func (s *Service) SetAssignee(ctx context.Context, identifier, assignee string) (*model.Document, error) {
	if !s.kind.HasAssignee {
		return nil, newErr(KindBadRequest, "%ss have no assignee field", s.kind.Name)
	}
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	a, err := normalizeAssignee(assignee)
	if err != nil {
		return nil, err
	}
	d.FrontMatter.Assignee = a
	return s.save(ctx, d, "setAssignee", events.ActionUpdated)
}

// patchPlanning applies one of the planning fields from a patch; it reports
// whether the key was one of them.
func (s *Service) patchPlanning(d *model.Document, key string, v any) (bool, error) {
	fm := &d.FrontMatter
	switch key {
	case "tags":
		if v == nil {
			fm.Tags = []string{}
			return true, nil
		}
		var tags []string
		if err := reencode(v, &tags); err != nil {
			return true, newErr(KindBadRequest, "tags must be a list of strings")
		}
		t, err := normalizeTags(tags)
		if err != nil {
			return true, err
		}
		fm.Tags = t
	case "milestone":
		str, ok := v.(string)
		if v != nil && !ok {
			return true, newErr(KindBadRequest, "milestone must be a string")
		}
		fm.Milestone = strings.TrimSpace(str)
	case "due_date", "planned_start", "planned_end":
		str, ok := v.(string)
		if v != nil && !ok {
			return true, newErr(KindBadRequest, "%s must be a YYYY-MM-DD string", key)
		}
		n, err := normalizeDate(key, str)
		if err != nil {
			return true, err
		}
		switch key {
		case "due_date":
			fm.DueDate = n
		case "planned_start":
			fm.PlannedStart = n
		default:
			fm.PlannedEnd = n
		}
	case "order":
		if v == nil {
			fm.Order = 0
			return true, nil
		}
		n, err := toInt("order", v)
		if err != nil {
			return true, err
		}
		fm.Order = n
	case "assignee":
		if !s.kind.HasAssignee {
			return true, newErr(KindBadRequest, "%ss have no assignee field", s.kind.Name)
		}
		str, ok := v.(string)
		if v != nil && !ok {
			return true, newErr(KindBadRequest, "assignee must be a string")
		}
		a, err := normalizeAssignee(str)
		if err != nil {
			return true, err
		}
		fm.Assignee = a
	default:
		return false, nil
	}
	return true, nil
}

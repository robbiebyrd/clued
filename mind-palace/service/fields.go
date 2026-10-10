package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/events"
	"github.com/robbiebyrd/clued/mind-palace/markdown"
	"github.com/robbiebyrd/clued/mind-palace/model"
)

// SetTitle changes the title and H1; the file keeps its slug unless renameFile.
func (s *Service) SetTitle(ctx context.Context, identifier, title string, renameFile bool) (*model.Document, error) {
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, newErr(KindBadRequest, "title must not be empty")
	}
	d.FrontMatter.Title = title
	slug := ""
	if renameFile {
		slug = model.Slugify(title)
	}
	d.Path = s.pathFor(d, slug)
	return s.save(ctx, d, "setTitle", events.ActionUpdated)
}

// SetPurpose sets or replaces `purpose` (kinds that have one).
func (s *Service) SetPurpose(ctx context.Context, identifier, purpose string) (*model.Document, error) {
	if !s.kind.HasPurpose {
		return nil, newErr(KindBadRequest, "%ss have no purpose field", s.kind.Name)
	}
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	purpose = strings.TrimSpace(purpose)
	if purpose == "" {
		return nil, newErr(KindBadRequest, "purpose must not be empty")
	}
	d.FrontMatter.Purpose = purpose
	return s.save(ctx, d, "setPurpose", events.ActionUpdated)
}

// SetType changes the type and renames the file's type part.
func (s *Service) SetType(ctx context.Context, identifier, typ string) (*model.Document, error) {
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	t, ok := s.cfg.NormalizeType(typ)
	if !ok {
		return nil, newErr(KindBadRequest, "unknown %s type %q (known: %s)", s.kind.Name, typ, strings.Join(s.cfg.TypeNames(), ", "))
	}
	d.FrontMatter.Type = t
	d.Path = s.pathFor(d, "")
	return s.save(ctx, d, "setType", events.ActionUpdated)
}

// SetStatus moves the document through the workflow.
func (s *Service) SetStatus(ctx context.Context, identifier, status string, force bool) (*model.Document, error) {
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	changed, err := s.applyStatus(d, status, force)
	if err != nil {
		return nil, err
	}
	if !changed {
		return d, nil
	}
	d.Path = s.pathFor(d, "")
	return s.save(ctx, d, "setStatus", events.ActionUpdated)
}

// applyStatus normalises and applies a status change; it reports whether
// anything changed. Reaching in_progress records `started` (kinds that track
// it) and reaching complete records `completed`; both are overwritten on a
// later return and kept when the document moves away. Kinds with a criteria
// gate refuse complete while acceptance criteria are open, force or not.
func (s *Service) applyStatus(d *model.Document, status string, force bool) (bool, error) {
	to, ok := s.cfg.NormalizeStatus(status)
	if !ok {
		return false, newErr(KindBadRequest, "unknown status %q (known: %s)", status, strings.Join(s.cfg.StatusNames(), ", "))
	}
	from := d.FrontMatter.Status
	if to == from {
		return false, nil
	}
	if !force && !s.cfg.CanTransition(from, to) {
		return false, newErr(KindInvalidTransition, "%s %s cannot move from %q to %q (allowed: %s); use force to override", s.kind.Name, d.ID(), from, to, strings.Join(s.cfg.Transitions(from), ", "))
	}
	if to == "complete" {
		if err := s.criteriaGate(d); err != nil {
			return false, err
		}
	}
	d.FrontMatter.Status = to
	now := s.timestamp()
	if to == "in_progress" && s.kind.TracksStarted {
		d.FrontMatter.Started = now
	}
	if to == "complete" {
		d.FrontMatter.Completed = now
	}
	return true, nil
}

// criteriaGate refuses completion while an acceptance criterion is open.
func (s *Service) criteriaGate(d *model.Document) error {
	if !s.kind.CriteriaGate {
		return nil
	}
	items, ok := markdown.Criteria(d.Content)
	if !ok {
		return &Error{Kind: KindIncompleteCriteria, Message: fmt.Sprintf("%s %s has no %q section", s.kind.Name, d.ID(), markdown.CriteriaHeading)}
	}
	if len(items) == 0 {
		return &Error{Kind: KindIncompleteCriteria, Message: fmt.Sprintf("%s %s cannot be completed: it has no acceptance criteria", s.kind.Name, d.ID())}
	}
	var open []string
	for _, c := range items {
		if c.State == markdown.StateOpen {
			open = append(open, fmt.Sprintf("%d: %s", c.Position, c.Text))
		}
	}
	if len(open) > 0 {
		return &Error{Kind: KindIncompleteCriteria, Message: fmt.Sprintf("%s %s cannot be completed: %d acceptance criteria are unchecked", s.kind.Name, d.ID(), len(open)), Problems: open}
	}
	return nil
}

// Transitions lists where a document can move next.
type Transitions struct {
	ID          string   `json:"id"`
	Status      string   `json:"status"`
	Transitions []string `json:"transitions"`
}

// GetTransitions lists the statuses reachable from the current status.
func (s *Service) GetTransitions(ctx context.Context, identifier string) (*Transitions, error) {
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	t := s.cfg.Transitions(d.FrontMatter.Status)
	if t == nil {
		t = []string{}
	}
	return &Transitions{ID: d.ID(), Status: d.FrontMatter.Status, Transitions: t}, nil
}

// SetPriority sets the priority from a number or label.
func (s *Service) SetPriority(ctx context.Context, identifier string, priority any) (*model.Document, error) {
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	v, ok := s.cfg.NormalizePriority(priority)
	if !ok {
		return nil, newErr(KindBadRequest, "unknown priority %v", priority)
	}
	d.FrontMatter.Priority = v
	return s.save(ctx, d, "setPriority", events.ActionUpdated)
}

// SetEffort sets the effort from a size, label or points.
func (s *Service) SetEffort(ctx context.Context, identifier string, effort any) (*model.Document, error) {
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	v, ok := s.cfg.NormalizeEffort(effort)
	if !ok {
		return nil, newErr(KindBadRequest, "unknown effort %v", effort)
	}
	d.FrontMatter.Effort = v
	return s.save(ctx, d, "setEffort", events.ActionUpdated)
}

// ClearEffort removes the effort.
func (s *Service) ClearEffort(ctx context.Context, identifier string) (*model.Document, error) {
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	d.FrontMatter.Effort = ""
	return s.save(ctx, d, "clearEffort", events.ActionUpdated)
}

// PatchFrontMatter applies several field changes in one write.
func (s *Service) PatchFrontMatter(ctx context.Context, identifier string, patch map[string]any, force bool) (*model.Document, error) {
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	for _, k := range s.kind.Immutable {
		if _, ok := patch[k]; ok {
			return nil, newErr(KindImmutableField, "%q is managed by the service and cannot be patched", k)
		}
	}
	keys := make([]string, 0, len(patch))
	for k := range patch {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := patch[k]
		switch k {
		case "title":
			str, _ := v.(string)
			if strings.TrimSpace(str) == "" {
				return nil, newErr(KindBadRequest, "title must be a non-empty string")
			}
			d.FrontMatter.Title = strings.TrimSpace(str)
		case "purpose":
			if !s.kind.HasPurpose {
				return nil, newErr(KindBadRequest, "%ss have no purpose field", s.kind.Name)
			}
			if v == nil || v == "" {
				d.FrontMatter.Purpose = ""
				continue
			}
			str, _ := v.(string)
			if strings.TrimSpace(str) == "" {
				return nil, newErr(KindBadRequest, "purpose must be a string")
			}
			d.FrontMatter.Purpose = strings.TrimSpace(str)
		case "type":
			str, _ := v.(string)
			t, ok := s.cfg.NormalizeType(str)
			if !ok {
				return nil, newErr(KindBadRequest, "unknown %s type %v", s.kind.Name, v)
			}
			d.FrontMatter.Type = t
		case "status":
			str, _ := v.(string)
			if _, err := s.applyStatus(d, str, force); err != nil {
				return nil, err
			}
		case "priority":
			pv, ok := s.cfg.NormalizePriority(v)
			if !ok {
				return nil, newErr(KindBadRequest, "unknown priority %v", v)
			}
			d.FrontMatter.Priority = pv
		case "effort":
			if v == nil || v == "" {
				d.FrontMatter.Effort = ""
				continue
			}
			ev, ok := s.cfg.NormalizeEffort(v)
			if !ok {
				return nil, newErr(KindBadRequest, "unknown effort %v", v)
			}
			d.FrontMatter.Effort = ev
		case "plans":
			// Shorthand for links.plans.
			var links []model.Link
			if err := reencode(v, &links); err != nil {
				return nil, newErr(KindBadRequest, "plans: %v", err)
			}
			for _, l := range links {
				if err := s.checkPlanLink(ctx, d.ID(), l); err != nil {
					return nil, err
				}
			}
			d.FrontMatter.SetPlanLinks(links)
		case "links":
			// Merge per link kind: a kind present in the patch replaces that
			// kind (null clears it); kinds left out are kept.
			if v == nil {
				d.FrontMatter.Links = nil
				continue
			}
			patchLinks, ok := v.(map[string]any)
			if !ok {
				return nil, newErr(KindBadRequest, "links must be an object")
			}
			links := &model.Links{}
			if d.FrontMatter.Links != nil {
				links = d.FrontMatter.Links.Clone()
			}
			for lk, lv := range patchLinks {
				var err error
				switch lk {
				case "repo":
					links.Repo = nil
					if lv != nil {
						err = reencode(lv, &links.Repo)
					}
				case "specs":
					links.Specs = nil
					if lv != nil {
						err = reencode(lv, &links.Specs)
					}
				case "web":
					links.Web = nil
					if lv != nil {
						err = reencode(lv, &links.Web)
					}
				case "stories":
					links.Stories = nil
					if lv != nil {
						err = reencode(lv, &links.Stories)
					}
					for _, l := range links.Stories {
						if err := s.checkStoryLink(ctx, d.ID(), l); err != nil {
							return nil, err
						}
					}
				case "plans":
					links.Plans = nil
					if lv != nil {
						err = reencode(lv, &links.Plans)
					}
					for _, l := range links.Plans {
						if err := s.checkPlanLink(ctx, d.ID(), l); err != nil {
							return nil, err
						}
					}
				default:
					return nil, newErr(KindBadRequest, "unknown link kind %q (repo, specs, web, stories, plans)", lk)
				}
				if err != nil {
					return nil, newErr(KindBadRequest, "links.%s: %v", lk, err)
				}
			}
			if links.IsEmpty() {
				links = nil
			}
			d.FrontMatter.Links = links
		case "progress":
			var prog model.Progress
			if err := reencode(v, &prog); err != nil {
				return nil, newErr(KindBadRequest, "progress: %v", err)
			}
			for k, e := range prog {
				if !markdown.HasSection(d.Content, k) {
					return nil, newErr(s.kind.UnknownProgressKeyError, "progress key %q has no numbered heading in the content", k)
				}
				if n, ok := s.cfg.NormalizeStatus(e.Status); ok {
					e.Status = n
					prog[k] = e
				}
			}
			d.FrontMatter.Progress = prog
		default:
			return nil, newErr(KindBadRequest, "unknown front matter field %q", k)
		}
	}
	d.Path = s.pathFor(d, "")
	return s.save(ctx, d, "patchFrontMatter", events.ActionUpdated)
}

func reencode(from any, to any) error {
	b, err := json.Marshal(from)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, to)
}

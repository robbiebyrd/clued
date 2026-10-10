package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/events"
	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/markdown"
	"github.com/robbiebyrd/clued/mind-palace/model"
	"github.com/robbiebyrd/clued/mind-palace/render"
	"github.com/robbiebyrd/clued/mind-palace/store"
)

// CreateInput is the typed form of the creation JSON Schema. Body is decoded
// per kind by render.DecodeBody.
type CreateInput struct {
	Slug        string            `json:"slug,omitempty"`
	FrontMatter model.FrontMatter `json:"frontMatter"`
	Body        json.RawMessage   `json:"body"`
}

// Create makes a new document from input matching the creation schema.
// templateID selects the content template ("" = default).
func (s *Service) Create(ctx context.Context, input json.RawMessage, templateID string) (*model.Document, error) {
	raw, problems := s.normalizeCreateInput(input)
	if len(problems) > 0 {
		return nil, &Error{Kind: KindValidation, Message: "create: input is invalid", Problems: problems}
	}
	// The managed timestamps the service is about to set are supplied to the
	// schema so a document created directly as in_progress or complete passes
	// the conditional requirements; Create overwrites them below.
	now := s.timestamp()
	if fm, ok := raw["frontMatter"].(map[string]any); ok {
		if fm["status"] == "in_progress" && s.kind.TracksStarted {
			fm["started"] = now
		}
		if fm["status"] == "complete" {
			fm["completed"] = now
		}
	}
	if problems := s.validator.ValidateCreate(raw); len(problems) > 0 {
		return nil, &Error{Kind: KindValidation, Message: fmt.Sprintf("create: input does not match the %s schema", s.kind.Name), Problems: problems}
	}
	buf, _ := json.Marshal(raw)
	var in CreateInput
	dec := json.NewDecoder(bytes.NewReader(buf))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return nil, newErr(KindBadRequest, "create: %v", err)
	}
	body, err := render.DecodeBody(s.kind.Name, in.Body)
	if err != nil {
		return nil, newErr(KindBadRequest, "create: body: %v", err)
	}
	tmpl, err := s.GetTemplate(ctx, templateID)
	if err != nil {
		return nil, err
	}

	s.palace.mu.Lock()
	defer s.palace.mu.Unlock()

	for _, l := range in.FrontMatter.PlanLinks() {
		if err := s.checkPlanLink(ctx, "", l); err != nil {
			return nil, err
		}
	}
	for _, l := range in.FrontMatter.StoryLinks() {
		if err := s.checkStoryLink(ctx, "", l); err != nil {
			return nil, err
		}
	}
	id, err := s.allocateID(ctx, in.FrontMatter.ID)
	if err != nil {
		return nil, err
	}
	fm := in.FrontMatter
	fm.ID = id
	fm.Created, fm.Updated = now, now
	// Started and completed are managed by the service: omitted until the
	// document reaches in_progress / complete.
	fm.Started, fm.Completed = "", ""
	if fm.Status == "in_progress" && s.kind.TracksStarted {
		fm.Started = now
	}
	if fm.Status == "complete" {
		fm.Completed = now
	}
	slug := in.Slug
	if slug == "" {
		slug = model.Slugify(fm.Title)
	}
	content, err := render.Render(tmpl.Content, render.Data{ID: id, Title: fm.Title, Type: fm.Type, Body: body})
	if err != nil {
		return nil, newErr(KindBadRequest, "create: %v", err)
	}
	// Seed progress for every numbered heading the body declares.
	if fm.Progress == nil {
		if sectioned, ok := body.(render.Sectioned); ok {
			if sections := sectioned.SectionNumbers(); len(sections) > 0 {
				fm.Progress = model.Progress{}
				for _, n := range sections {
					fm.Progress[n] = model.ProgressEntry{Status: config.DefaultStatus}
				}
			}
		}
	} else {
		for _, k := range fm.Progress.SortedKeys() {
			if !markdown.HasSection(content, k) {
				return nil, newErr(s.kind.UnknownProgressKeyError, "create: progress key %q has no numbered heading in the content", k)
			}
		}
	}
	d := &model.Document{Kind: s.kind.Name, FrontMatter: fm, Content: content}
	if fm.Status == "complete" {
		if err := s.criteriaGate(d); err != nil {
			return nil, err
		}
	}
	d.Path = s.pathFor(d, slug)
	return s.save(ctx, d, "create", events.ActionCreated)
}

// normalizeCreateInput decodes the raw input and maps synonyms to primary
// values so the schema sees canonical data.
func (s *Service) normalizeCreateInput(input json.RawMessage) (map[string]any, []string) {
	var raw map[string]any
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return nil, []string{"/: input must be a JSON object: " + err.Error()}
	}
	fm, _ := raw["frontMatter"].(map[string]any)
	if fm == nil {
		return raw, []string{"/frontMatter: required"}
	}
	var problems []string
	if v, ok := fm["status"]; !ok || v == nil || v == "" {
		fm["status"] = config.DefaultStatus
	} else if str, ok := v.(string); ok {
		if n, ok := s.cfg.NormalizeStatus(str); ok {
			fm["status"] = n
		} else {
			problems = append(problems, fmt.Sprintf("/frontMatter/status: unknown status %q (known: %s)", str, strings.Join(s.cfg.StatusNames(), ", ")))
		}
	}
	if v, ok := fm["type"]; ok {
		if str, ok := v.(string); ok {
			if n, ok := s.cfg.NormalizeType(str); ok {
				fm["type"] = n
			} else {
				problems = append(problems, fmt.Sprintf("/frontMatter/type: unknown %s type %q (known: %s)", s.kind.Name, str, strings.Join(s.cfg.TypeNames(), ", ")))
			}
		}
	}
	if v, ok := fm["priority"]; ok && v != nil {
		if n, ok := s.cfg.NormalizePriority(v); ok {
			fm["priority"] = n
		} else {
			problems = append(problems, fmt.Sprintf("/frontMatter/priority: unknown priority %v", v))
		}
	}
	if v, ok := fm["effort"]; ok {
		if v == nil || v == "" {
			delete(fm, "effort")
		} else if n, ok := s.cfg.NormalizeEffort(v); ok {
			fm["effort"] = n
		} else {
			problems = append(problems, fmt.Sprintf("/frontMatter/effort: unknown effort %v", v))
		}
	}
	// Managed fields are set by the service.
	for _, k := range []string{"created", "updated", "started", "completed"} {
		delete(fm, k)
	}
	// A top-level plans list (pre-links.plans layout) is accepted and merged
	// into links.plans, legacy entries first, as ParseFrontMatter does. A
	// links value that is not an object is left alone so the schema rejects it.
	if legacy, ok := fm["plans"]; ok {
		links, isObject := fm["links"].(map[string]any)
		if fm["links"] == nil || isObject {
			delete(fm, "plans")
			if links == nil {
				links = map[string]any{}
				fm["links"] = links
			}
			merged, _ := legacy.([]any)
			if current, ok := links["plans"].([]any); ok {
				merged = append(merged, current...)
			}
			if merged == nil && legacy != nil {
				links["plans"] = legacy // not a list: let the schema report it
			} else {
				links["plans"] = merged
			}
		}
	}
	if prog, ok := fm["progress"].(map[string]any); ok {
		for k, v := range prog {
			if e, ok := v.(map[string]any); ok {
				if st, ok := e["status"].(string); ok {
					if n, ok := s.cfg.NormalizeStatus(st); ok {
						e["status"] = n
					}
				}
				if stories, ok := e["stories"].([]any); ok {
					for i, sid := range stories {
						if str, ok := sid.(string); ok {
							stories[i] = model.NormalizeID(str)
						}
					}
				}
			}
			prog[k] = v
		}
	}
	if links, ok := fm["links"].(map[string]any); ok {
		for _, key := range []string{"stories", "plans"} {
			if list, ok := links[key].([]any); ok {
				for _, item := range list {
					if tuple, ok := item.([]any); ok && len(tuple) > 0 {
						if str, ok := tuple[0].(string); ok {
							tuple[0] = model.NormalizeID(str)
						}
					}
				}
			}
		}
	}
	return raw, problems
}

// allocateID returns the next sequence number with a fresh random suffix, or
// honours a requested unused ID.
func (s *Service) allocateID(ctx context.Context, requested string) (string, error) {
	docs, err := s.palace.store.List(ctx, s.kind.Name)
	if err != nil {
		return "", storageErr(err)
	}
	used := map[string]bool{}
	maxSeq := 0
	for _, d := range docs {
		used[d.ID()] = true
		if n := model.SequenceOf(d.ID()); n > maxSeq {
			maxSeq = n
		}
	}
	if requested != "" {
		if used[requested] {
			return "", newErr(KindConflict, "%s id %s already exists", s.kind.Name, requested)
		}
		return requested, nil
	}
	seq := maxSeq + 1
	if seq > 9999 {
		return "", newErr(KindConflict, "%s sequence numbers are exhausted (9999)", s.kind.Name)
	}
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("%04d-%s", seq, s.palace.randomID())
		if !used[id] {
			return id, nil
		}
	}
	return "", newErr(KindConflict, "could not allocate a unique %s id", s.kind.Name)
}

// Get retrieves a document (path, front matter and content).
func (s *Service) Get(ctx context.Context, identifier string) (*model.Document, error) {
	return s.Resolve(ctx, identifier)
}

// GetFrontMatter retrieves only the front matter.
func (s *Service) GetFrontMatter(ctx context.Context, identifier string) (*model.FrontMatter, error) {
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	return &d.FrontMatter, nil
}

// GetContent retrieves only the content.
func (s *Service) GetContent(ctx context.Context, identifier string) (string, error) {
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return "", err
	}
	return d.Content, nil
}

// ListFilter narrows List.
type ListFilter struct {
	Type     string `json:"type,omitempty"`
	Status   string `json:"status,omitempty"`
	Priority string `json:"priority,omitempty"`
	// Plan keeps documents linked to this plan id (links.plans, any relation).
	Plan string `json:"plan,omitempty"`
	// Story keeps documents linked to this story id (links.stories or progress).
	Story string `json:"story,omitempty"`
	// IncludeArchived includes documents in archived status (default:
	// excluded unless Status asks for them).
	IncludeArchived bool `json:"includeArchived,omitempty"`
}

// Summary is a document without its content.
type Summary struct {
	Kind        string            `json:"kind"`
	Path        string            `json:"path"`
	FrontMatter model.FrontMatter `json:"frontMatter"`
}

// List returns summaries matching the filter, ordered by ID.
func (s *Service) List(ctx context.Context, f ListFilter) ([]Summary, error) {
	if f.Type != "" {
		n, ok := s.cfg.NormalizeType(f.Type)
		if !ok {
			return nil, newErr(KindBadRequest, "unknown %s type %q", s.kind.Name, f.Type)
		}
		f.Type = n
	}
	if f.Status != "" {
		n, ok := s.cfg.NormalizeStatus(f.Status)
		if !ok {
			return nil, newErr(KindBadRequest, "unknown status %q", f.Status)
		}
		f.Status = n
	}
	if f.Priority != "" {
		n, ok := s.cfg.NormalizePriority(f.Priority)
		if !ok {
			return nil, newErr(KindBadRequest, "unknown priority %q", f.Priority)
		}
		f.Priority = n
	}
	if f.Story != "" {
		f.Story = model.NormalizeID(f.Story)
	}
	if f.Plan != "" {
		f.Plan = model.NormalizeID(f.Plan)
	}
	docs, err := s.palace.store.List(ctx, s.kind.Name)
	if err != nil {
		return nil, storageErr(err)
	}
	out := []Summary{}
	for _, d := range docs {
		fm := d.FrontMatter
		if f.Type != "" && fm.Type != f.Type {
			continue
		}
		if f.Status != "" && fm.Status != f.Status {
			continue
		}
		if f.Status == "" && !f.IncludeArchived && fm.Status == "archived" {
			continue
		}
		if f.Priority != "" && fm.Priority != f.Priority {
			continue
		}
		if f.Plan != "" && !hasPlanLink(fm, f.Plan) {
			continue
		}
		if f.Story != "" && !hasStory(fm, f.Story) {
			continue
		}
		out = append(out, Summary{Kind: s.kind.Name, Path: d.Path, FrontMatter: fm})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FrontMatter.ID < out[j].FrontMatter.ID })
	return out, nil
}

func hasPlanLink(fm model.FrontMatter, id string) bool {
	for _, l := range fm.PlanLinks() {
		if l.ID == id {
			return true
		}
	}
	return false
}

func hasStory(fm model.FrontMatter, id string) bool {
	for _, l := range fm.StoryLinks() {
		if l.ID == id {
			return true
		}
	}
	for _, e := range fm.Progress {
		for _, sid := range e.Stories {
			if sid == id {
				return true
			}
		}
	}
	return false
}

// Update replaces the document's content. Front matter only gets a new
// `updated`. For kinds with strict progress, progress keys that no longer
// match a numbered heading are rejected.
func (s *Service) Update(ctx context.Context, identifier, content string) (*model.Document, error) {
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(strings.TrimLeft(content, "\xEF\xBB\xBF"), "---\n") {
		// A whole document was supplied: keep its content, ignore its front matter.
		if _, body, err := markdown.Split(content); err == nil {
			content = body
		}
	}
	if s.kind.StrictProgressOnUpdate {
		var orphans []string
		for _, k := range d.FrontMatter.Progress.SortedKeys() {
			if !markdown.HasSection(content, k) {
				orphans = append(orphans, k)
			}
		}
		if len(orphans) > 0 {
			return nil, &Error{Kind: s.kind.UnknownProgressKeyError, Message: fmt.Sprintf("update: progress keys %s have no numbered heading in the new content; remove their progress entries first", strings.Join(orphans, ", ")), Problems: orphans}
		}
	}
	d.Content = content
	return s.save(ctx, d, "update", events.ActionUpdated)
}

// DeleteResult reports what Delete removed.
type DeleteResult struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Path string `json:"path"`
	// LinkedBy lists the documents ("<kind> <id>") that linked to the deleted one.
	LinkedBy []string `json:"linkedBy,omitempty"`
}

// Delete removes a document. Other documents (of any kind) linking to it
// block the delete unless forced.
func (s *Service) Delete(ctx context.Context, identifier string, force bool) (*DeleteResult, error) {
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	linked, err := s.linkedBy(ctx, d.ID())
	if err != nil {
		return nil, err
	}
	if len(linked) > 0 && !force {
		return nil, &Error{Kind: s.kind.LinkedError, Message: fmt.Sprintf("%s %s is linked from %s; use force to delete anyway (prefer archiving)", s.kind.Name, d.ID(), strings.Join(linked, ", ")), Problems: linked}
	}
	err = s.palace.store.Delete(ctx, s.kind.Name, d.ID())
	res := &DeleteResult{Kind: s.kind.Name, ID: d.ID(), Path: d.Path, LinkedBy: linked}
	var fe *store.FanoutError
	if errors.As(err, &fe) {
		s.publish(events.ActionDeleted, d, "delete")
		return res, &Error{Kind: KindStorage, Message: fe.Error(), Partial: true, Cause: err}
	}
	if err != nil {
		return nil, storageErr(err)
	}
	s.publish(events.ActionDeleted, d, "delete")
	return res, nil
}

// linkedBy lists the documents of every kind that reference id as a
// document of this kind, as "<kind> <id>".
func (s *Service) linkedBy(ctx context.Context, id string) ([]string, error) {
	var out []string
	for _, other := range s.palace.Services() {
		docs, err := s.palace.store.List(ctx, other.kind.Name)
		if err != nil {
			return nil, storageErr(err)
		}
		for _, d := range docs {
			if other.kind.Name == s.kind.Name && d.ID() == id {
				continue
			}
			var linked bool
			if s.kind.Name == kind.Story {
				linked = hasStory(d.FrontMatter, id)
			} else {
				linked = hasPlanLink(d.FrontMatter, id)
			}
			if linked {
				out = append(out, other.kind.Name+" "+d.ID())
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// ValidationReport is the result of Validate.
type ValidationReport struct {
	Kind     string   `json:"kind"`
	ID       string   `json:"id"`
	Path     string   `json:"path"`
	Valid    bool     `json:"valid"`
	Problems []string `json:"problems"`
}

// Validate checks a document without changing it.
func (s *Service) Validate(ctx context.Context, identifier string) (*ValidationReport, error) {
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	problems := s.check(d)
	for _, k := range d.FrontMatter.Progress.SortedKeys() {
		if !markdown.HasSection(d.Content, k) {
			problems = append(problems, fmt.Sprintf("/progress/%s: no numbered heading %q in the content", k, k))
		}
	}
	for _, l := range d.FrontMatter.PlanLinks() {
		if ok, err := s.other(kind.Plan).exists(ctx, l.ID); err == nil && !ok {
			problems = append(problems, fmt.Sprintf("/links/plans: linked plan %s does not exist", l.ID))
		}
	}
	if s.kind.LinkTargetsMustExist && s.kind.Name == kind.Story {
		for _, l := range d.FrontMatter.StoryLinks() {
			if ok, err := s.exists(ctx, l.ID); err == nil && !ok {
				problems = append(problems, fmt.Sprintf("/links/stories: linked story %s does not exist", l.ID))
			}
		}
	}
	if s.kind.CriteriaGate {
		problems = append(problems, markdown.CriteriaProblems(d.Content)...)
	}
	sort.Strings(problems)
	if problems == nil {
		problems = []string{}
	}
	return &ValidationReport{Kind: s.kind.Name, ID: d.ID(), Path: d.Path, Valid: len(problems) == 0, Problems: problems}, nil
}

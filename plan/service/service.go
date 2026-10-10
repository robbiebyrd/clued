// Package service implements the Plan Service interface: every operation an
// entrypoint (CLI, HTTP, WebSocket, MCP) exposes. It enforces the rules that
// apply to every write — schema validation, managed timestamps, immutable
// id/created, title↔H1 sync, synonym normalisation — on top of a store.
package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/robbiebyrd/clued/plan/config"
	"github.com/robbiebyrd/clued/plan/events"
	"github.com/robbiebyrd/clued/plan/markdown"
	"github.com/robbiebyrd/clued/plan/model"
	"github.com/robbiebyrd/clued/plan/render"
	"github.com/robbiebyrd/clued/plan/schema"
	"github.com/robbiebyrd/clued/plan/store"
)

// TimestampLayout is the ISO 8601 UTC form used in front matter.
const TimestampLayout = "2006-01-02T15:04:05.000Z"

// Service is the plan service.
type Service struct {
	cfg       *config.Config
	store     store.Store
	validator *schema.Validator
	bus       *events.Bus
	now       func() time.Time
	randomID  func() string
	mu        sync.Mutex
}

// New builds a service over a store. bus may be nil.
func New(cfg *config.Config, st store.Store, bus *events.Bus) (*Service, error) {
	if cfg == nil {
		cfg = config.Default()
	}
	v, err := schema.New(cfg)
	if err != nil {
		return nil, err
	}
	if bus == nil {
		bus = events.New()
	}
	return &Service{cfg: cfg, store: st, validator: v, bus: bus, now: time.Now, randomID: randomID}, nil
}

// Config returns the effective configuration.
func (s *Service) Config() *config.Config { return s.cfg }

// Bus returns the event bus.
func (s *Service) Bus() *events.Bus { return s.bus }

// Store returns the underlying store.
func (s *Service) Store() store.Store { return s.store }

// Schema returns the effective JSON Schema.
func (s *Service) Schema() []byte { return s.validator.JSON() }

// StoreNames lists the stores writes fan out to.
func (s *Service) StoreNames() []string {
	if m, ok := s.store.(*store.MultiStore); ok {
		var out []string
		for _, st := range m.Stores() {
			out = append(out, st.Name()+" ("+st.Kind()+")")
		}
		return out
	}
	return []string{s.store.Name() + " (" + s.store.Kind() + ")"}
}

// SetClock overrides the clock (tests).
func (s *Service) SetClock(now func() time.Time) { s.now = now }

func (s *Service) timestamp() string { return s.now().UTC().Format(TimestampLayout) }

const idAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

func randomID() string {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = idAlphabet[int(b[i])%len(idAlphabet)]
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// Resolution and persistence helpers

// Resolve finds a plan by ID or by file path.
func (s *Service) Resolve(ctx context.Context, identifier string) (*model.Plan, error) {
	id := strings.TrimSpace(identifier)
	if id == "" {
		return nil, newErr(KindBadRequest, "a plan identifier (id or path) is required")
	}
	if !model.PlanIDRe.MatchString(id) {
		fid, _, _, ok := model.ParseFileName(id)
		if !ok {
			return nil, newErr(KindNotFound, "%q is neither a plan id nor a plan file name", identifier)
		}
		id = fid
	}
	p, err := s.store.GetPlan(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, newErr(KindNotFound, "plan %s not found", id)
	}
	if err != nil {
		return nil, &Error{Kind: KindStorage, Message: err.Error(), Cause: err}
	}
	return p, nil
}

// pathFor computes the canonical path: archived plans live under archive/.
func (s *Service) pathFor(p *model.Plan, slug string) string {
	if slug == "" {
		if _, _, cur, ok := model.ParseFileName(p.Path); ok {
			slug = cur
		} else {
			slug = model.Slugify(p.FrontMatter.Title)
		}
	}
	name := model.FileName(p.FrontMatter.ID, p.FrontMatter.Type, slug)
	if p.FrontMatter.Status == "archived" {
		return "archive/" + name
	}
	return name
}

// check validates a plan before it is written and returns the problems.
func (s *Service) check(ctx context.Context, p *model.Plan) []string {
	problems := s.validator.ValidateStoredFrontMatter(p.FrontMatter)
	problems = append(problems, s.invariants(ctx, p)...)
	sort.Strings(problems)
	return problems
}

// invariants are the rules beyond the schema.
func (s *Service) invariants(ctx context.Context, p *model.Plan) []string {
	var problems []string
	fm := &p.FrontMatter
	if _, ok := s.cfg.NormalizeType(fm.Type); !ok {
		problems = append(problems, fmt.Sprintf("/type: unknown plan type %q", fm.Type))
	}
	if _, ok := s.cfg.Status(fm.Status); !ok {
		problems = append(problems, fmt.Sprintf("/status: unknown status %q", fm.Status))
	}
	if h1, ok := markdown.FirstH1(p.Content); !ok || h1 != fm.Title {
		problems = append(problems, fmt.Sprintf("/content: H1 %q does not match title %q", h1, fm.Title))
	}
	id, typ, _, ok := model.ParseFileName(p.Path)
	switch {
	case !ok:
		problems = append(problems, fmt.Sprintf("/path: %q is not a valid plan file name", p.Path))
	case id != fm.ID:
		problems = append(problems, fmt.Sprintf("/path: file name id %q does not match id %q", id, fm.ID))
	case typ != fm.Type:
		problems = append(problems, fmt.Sprintf("/path: file name type %q does not match type %q", typ, fm.Type))
	}
	if ok {
		archived := strings.HasPrefix(p.Path, "archive/")
		if archived != (fm.Status == "archived") {
			problems = append(problems, fmt.Sprintf("/path: %q is inconsistent with status %q", p.Path, fm.Status))
		}
	}
	for _, l := range fm.PlanLinks() {
		if l.ID == fm.ID {
			problems = append(problems, "/links/plans: a plan cannot link to itself")
		}
	}
	return problems
}

// save stamps `updated`, validates and writes the plan, then publishes.
func (s *Service) save(ctx context.Context, p *model.Plan, op string, eventType string) (*model.Plan, error) {
	if eventType == events.PlanCreated {
		p.FrontMatter.Updated = p.FrontMatter.Created
	} else {
		p.FrontMatter.Updated = s.timestamp()
	}
	p.Content = markdown.SetH1(p.Content, p.FrontMatter.Title)
	if problems := s.check(ctx, p); len(problems) > 0 {
		return nil, &Error{Kind: KindValidation, Message: fmt.Sprintf("%s: plan %s failed validation", op, p.ID()), Problems: problems}
	}
	err := s.store.PutPlan(ctx, p)
	var fe *store.FanoutError
	if errors.As(err, &fe) {
		s.publish(eventType, p, op)
		return p, &Error{Kind: KindStorage, Message: fe.Error(), Partial: true, Cause: err}
	}
	if err != nil {
		return nil, &Error{Kind: KindStorage, Message: err.Error(), Cause: err}
	}
	s.publish(eventType, p, op)
	return p, nil
}

func (s *Service) publish(eventType string, p *model.Plan, op string) {
	if s.bus == nil {
		return
	}
	s.bus.Publish(events.Event{Type: eventType, PlanID: p.ID(), Plan: p.Clone(), Operation: op, At: s.timestamp()})
}

// ---------------------------------------------------------------------------
// Plans

// CreateInput is the typed form of the creation JSON Schema.
type CreateInput struct {
	Slug        string            `json:"slug,omitempty"`
	FrontMatter model.FrontMatter `json:"frontMatter"`
	Body        render.Body       `json:"body"`
}

// Create makes a new plan from input matching the creation schema. templateID
// selects the content template ("" = default).
func (s *Service) Create(ctx context.Context, input json.RawMessage, templateID string) (*model.Plan, error) {
	raw, problems := s.normalizeCreateInput(input)
	if len(problems) > 0 {
		return nil, &Error{Kind: KindValidation, Message: "create: input is invalid", Problems: problems}
	}
	if problems := s.validator.ValidateCreate(raw); len(problems) > 0 {
		return nil, &Error{Kind: KindValidation, Message: "create: input does not match the plan schema", Problems: problems}
	}
	buf, _ := json.Marshal(raw)
	var in CreateInput
	dec := json.NewDecoder(bytes.NewReader(buf))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return nil, newErr(KindBadRequest, "create: %v", err)
	}
	tmpl, err := s.GetTemplate(ctx, templateID)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, l := range in.FrontMatter.PlanLinks() {
		if _, err := s.store.GetPlan(ctx, l.ID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, newErr(KindNotFound, "create: linked plan %s not found", l.ID)
			}
			return nil, &Error{Kind: KindStorage, Message: err.Error(), Cause: err}
		}
	}
	id, err := s.allocateID(ctx, in.FrontMatter.ID)
	if err != nil {
		return nil, err
	}
	fm := in.FrontMatter
	fm.ID = id
	now := s.timestamp()
	fm.Created, fm.Updated = now, now
	fm.Completed = ""
	if fm.Status == "complete" {
		fm.Completed = now
	}
	slug := in.Slug
	if slug == "" {
		slug = model.Slugify(fm.Title)
	}
	content, err := render.Render(tmpl.Content, render.Data{ID: id, Title: fm.Title, Type: fm.Type, Body: in.Body})
	if err != nil {
		return nil, newErr(KindBadRequest, "create: %v", err)
	}
	// Seed progress for every numbered phase/section the body declares.
	if fm.Progress == nil {
		if sections := in.Body.SectionNumbers(); len(sections) > 0 {
			fm.Progress = model.Progress{}
			for _, n := range sections {
				fm.Progress[n] = model.ProgressEntry{Status: config.DefaultStatus}
			}
		}
	} else {
		for k := range fm.Progress {
			if !markdown.HasSection(content, k) {
				return nil, newErr(KindUnknownSection, "create: progress key %q has no numbered heading in the content", k)
			}
		}
	}
	p := &model.Plan{FrontMatter: fm, Content: content}
	p.Path = s.pathFor(p, slug)
	return s.save(ctx, p, "create", events.PlanCreated)
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
				problems = append(problems, fmt.Sprintf("/frontMatter/type: unknown plan type %q (known: %s)", str, strings.Join(s.cfg.TypeNames(), ", ")))
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
	for _, k := range []string{"created", "updated", "completed"} {
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
			}
			prog[k] = v
		}
	}
	return raw, problems
}

// allocateID returns the next sequence number with a fresh random suffix, or
// honours a requested unused ID.
func (s *Service) allocateID(ctx context.Context, requested string) (string, error) {
	plans, err := s.store.ListPlans(ctx)
	if err != nil {
		return "", &Error{Kind: KindStorage, Message: err.Error(), Cause: err}
	}
	used := map[string]bool{}
	maxSeq := 0
	for _, p := range plans {
		used[p.ID()] = true
		if n := model.SequenceOf(p.ID()); n > maxSeq {
			maxSeq = n
		}
	}
	if requested != "" {
		if used[requested] {
			return "", newErr(KindConflict, "plan id %s already exists", requested)
		}
		return requested, nil
	}
	seq := maxSeq + 1
	if seq > 9999 {
		return "", newErr(KindConflict, "plan sequence numbers are exhausted (9999)")
	}
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("%04d-%s", seq, s.randomID())
		if !used[id] {
			return id, nil
		}
	}
	return "", newErr(KindConflict, "could not allocate a unique plan id")
}

// Get retrieves a plan (path, front matter and content).
func (s *Service) Get(ctx context.Context, identifier string) (*model.Plan, error) {
	return s.Resolve(ctx, identifier)
}

// GetFrontMatter retrieves only the front matter.
func (s *Service) GetFrontMatter(ctx context.Context, identifier string) (*model.FrontMatter, error) {
	p, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	return &p.FrontMatter, nil
}

// GetContent retrieves only the content.
func (s *Service) GetContent(ctx context.Context, identifier string) (string, error) {
	p, err := s.Resolve(ctx, identifier)
	if err != nil {
		return "", err
	}
	return p.Content, nil
}

// ListFilter narrows List.
type ListFilter struct {
	Type     string `json:"type,omitempty"`
	Status   string `json:"status,omitempty"`
	Priority string `json:"priority,omitempty"`
	// Plan keeps plans linked to this plan id (any relation).
	Plan string `json:"plan,omitempty"`
	// Story keeps plans linked to this story id (links.stories or progress).
	Story string `json:"story,omitempty"`
	// IncludeArchived includes plans in archived status (default: excluded
	// unless Status asks for them).
	IncludeArchived bool `json:"includeArchived,omitempty"`
}

// Summary is a plan without its content.
type Summary struct {
	Path        string            `json:"path"`
	FrontMatter model.FrontMatter `json:"frontMatter"`
}

// List returns plan summaries matching the filter, ordered by ID.
func (s *Service) List(ctx context.Context, f ListFilter) ([]Summary, error) {
	if f.Type != "" {
		n, ok := s.cfg.NormalizeType(f.Type)
		if !ok {
			return nil, newErr(KindBadRequest, "unknown plan type %q", f.Type)
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
	plans, err := s.store.ListPlans(ctx)
	if err != nil {
		return nil, &Error{Kind: KindStorage, Message: err.Error(), Cause: err}
	}
	out := []Summary{}
	for _, p := range plans {
		fm := p.FrontMatter
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
		out = append(out, Summary{Path: p.Path, FrontMatter: fm})
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
	if fm.Links != nil {
		for _, l := range fm.Links.Stories {
			if l.ID == id {
				return true
			}
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

// Update replaces the plan's content. Front matter only gets a new `updated`.
func (s *Service) Update(ctx context.Context, identifier, content string) (*model.Plan, error) {
	p, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(strings.TrimLeft(content, "\xEF\xBB\xBF"), "---\n") {
		// A whole document was supplied: keep its content, ignore its front matter.
		if _, body, err := markdown.Split(content); err == nil {
			content = body
		}
	}
	p.Content = content
	return s.save(ctx, p, "update", events.PlanUpdated)
}

// DeleteResult reports what Delete removed.
type DeleteResult struct {
	ID       string   `json:"id"`
	Path     string   `json:"path"`
	LinkedBy []string `json:"linkedBy,omitempty"`
}

// Delete removes a plan. Other plans linking to it block the delete unless forced.
func (s *Service) Delete(ctx context.Context, identifier string, force bool) (*DeleteResult, error) {
	p, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	linked, err := s.linkedBy(ctx, p.ID())
	if err != nil {
		return nil, err
	}
	if len(linked) > 0 && !force {
		return nil, &Error{Kind: KindLinkedPlan, Message: fmt.Sprintf("plan %s is linked from %s; use force to delete anyway (prefer archiving)", p.ID(), strings.Join(linked, ", ")), Problems: linked}
	}
	err = s.store.DeletePlan(ctx, p.ID())
	res := &DeleteResult{ID: p.ID(), Path: p.Path, LinkedBy: linked}
	var fe *store.FanoutError
	if errors.As(err, &fe) {
		s.bus.Publish(events.Event{Type: events.PlanDeleted, PlanID: p.ID(), Operation: "delete", At: s.timestamp()})
		return res, &Error{Kind: KindStorage, Message: fe.Error(), Partial: true, Cause: err}
	}
	if err != nil {
		return nil, &Error{Kind: KindStorage, Message: err.Error(), Cause: err}
	}
	s.bus.Publish(events.Event{Type: events.PlanDeleted, PlanID: p.ID(), Operation: "delete", At: s.timestamp()})
	return res, nil
}

func (s *Service) linkedBy(ctx context.Context, id string) ([]string, error) {
	plans, err := s.store.ListPlans(ctx)
	if err != nil {
		return nil, &Error{Kind: KindStorage, Message: err.Error(), Cause: err}
	}
	var out []string
	for _, other := range plans {
		if other.ID() == id {
			continue
		}
		if hasPlanLink(other.FrontMatter, id) {
			out = append(out, other.ID())
		}
	}
	sort.Strings(out)
	return out, nil
}

// ValidationReport is the result of Validate.
type ValidationReport struct {
	ID       string   `json:"id"`
	Path     string   `json:"path"`
	Valid    bool     `json:"valid"`
	Problems []string `json:"problems"`
}

// Validate checks a plan without changing it.
func (s *Service) Validate(ctx context.Context, identifier string) (*ValidationReport, error) {
	p, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	problems := s.check(ctx, p)
	for _, k := range p.FrontMatter.Progress.SortedKeys() {
		if !markdown.HasSection(p.Content, k) {
			problems = append(problems, fmt.Sprintf("/progress/%s: no numbered heading %q in the content", k, k))
		}
	}
	for _, l := range p.FrontMatter.PlanLinks() {
		if _, err := s.store.GetPlan(ctx, l.ID); errors.Is(err, store.ErrNotFound) {
			problems = append(problems, fmt.Sprintf("/links/plans: linked plan %s does not exist", l.ID))
		}
	}
	sort.Strings(problems)
	if problems == nil {
		problems = []string{}
	}
	return &ValidationReport{ID: p.ID(), Path: p.Path, Valid: len(problems) == 0, Problems: problems}, nil
}

// ---------------------------------------------------------------------------
// Templates

// GetTemplate returns a template; "" or "default" falls back to the built-in.
func (s *Service) GetTemplate(ctx context.Context, id string) (*model.Template, error) {
	if id == "" {
		id = render.DefaultTemplateID
	}
	t, err := s.store.GetTemplate(ctx, id)
	if err == nil {
		return t, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, &Error{Kind: KindStorage, Message: err.Error(), Cause: err}
	}
	if id == render.DefaultTemplateID {
		return &model.Template{ID: id, Content: render.DefaultTemplate()}, nil
	}
	return nil, newErr(KindNotFound, "template %q not found", id)
}

// ListTemplates lists stored templates plus the built-in default.
func (s *Service) ListTemplates(ctx context.Context) ([]*model.Template, error) {
	list, err := s.store.ListTemplates(ctx)
	if err != nil {
		return nil, &Error{Kind: KindStorage, Message: err.Error(), Cause: err}
	}
	hasDefault := false
	for _, t := range list {
		if t.ID == render.DefaultTemplateID {
			hasDefault = true
		}
	}
	if !hasDefault {
		list = append([]*model.Template{{ID: render.DefaultTemplateID, Content: render.DefaultTemplate()}}, list...)
	}
	return list, nil
}

func checkTemplateID(id string) error {
	if id == "" || strings.ContainsAny(id, `/\ `) || id == "." || id == ".." {
		return newErr(KindBadRequest, "invalid template id %q", id)
	}
	return nil
}

// CreateTemplate stores a new template.
func (s *Service) CreateTemplate(ctx context.Context, id, content string) (*model.Template, error) {
	if err := checkTemplateID(id); err != nil {
		return nil, err
	}
	if err := render.Validate(content); err != nil {
		return nil, &Error{Kind: KindValidation, Message: "template does not parse", Problems: []string{err.Error()}}
	}
	if _, err := s.store.GetTemplate(ctx, id); err == nil {
		return nil, newErr(KindConflict, "template %q already exists", id)
	}
	return s.putTemplate(ctx, &model.Template{ID: id, Content: content})
}

// UpdateTemplate replaces an existing template (the built-in default may be overridden).
func (s *Service) UpdateTemplate(ctx context.Context, id, content string) (*model.Template, error) {
	if err := checkTemplateID(id); err != nil {
		return nil, err
	}
	if err := render.Validate(content); err != nil {
		return nil, &Error{Kind: KindValidation, Message: "template does not parse", Problems: []string{err.Error()}}
	}
	if _, err := s.GetTemplate(ctx, id); err != nil {
		return nil, err
	}
	return s.putTemplate(ctx, &model.Template{ID: id, Content: content})
}

func (s *Service) putTemplate(ctx context.Context, t *model.Template) (*model.Template, error) {
	t.Updated = s.timestamp()
	err := s.store.PutTemplate(ctx, t)
	var fe *store.FanoutError
	if errors.As(err, &fe) {
		return t, &Error{Kind: KindStorage, Message: fe.Error(), Partial: true, Cause: err}
	}
	if err != nil {
		return nil, &Error{Kind: KindStorage, Message: err.Error(), Cause: err}
	}
	s.bus.Publish(events.Event{Type: events.TemplateUpdated, TemplateID: t.ID, At: s.timestamp()})
	return t, nil
}

// DeleteTemplate removes a stored template. Deleting an override of the
// default restores the built-in.
func (s *Service) DeleteTemplate(ctx context.Context, id string) error {
	if err := checkTemplateID(id); err != nil {
		return err
	}
	err := s.store.DeleteTemplate(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return newErr(KindNotFound, "template %q not found", id)
	}
	var fe *store.FanoutError
	if errors.As(err, &fe) {
		return &Error{Kind: KindStorage, Message: fe.Error(), Partial: true, Cause: err}
	}
	if err != nil {
		return &Error{Kind: KindStorage, Message: err.Error(), Cause: err}
	}
	s.bus.Publish(events.Event{Type: events.TemplateDeleted, TemplateID: id, At: s.timestamp()})
	return nil
}

// ---------------------------------------------------------------------------
// Front matter: fields

// SetTitle changes the title and H1; the file keeps its slug unless renameFile.
func (s *Service) SetTitle(ctx context.Context, identifier, title string, renameFile bool) (*model.Plan, error) {
	p, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, newErr(KindBadRequest, "title must not be empty")
	}
	p.FrontMatter.Title = title
	slug := ""
	if renameFile {
		slug = model.Slugify(title)
	}
	p.Path = s.pathFor(p, slug)
	return s.save(ctx, p, "setTitle", events.PlanUpdated)
}

// SetType changes the plan type and renames the file's type part.
func (s *Service) SetType(ctx context.Context, identifier, planType string) (*model.Plan, error) {
	p, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	t, ok := s.cfg.NormalizeType(planType)
	if !ok {
		return nil, newErr(KindBadRequest, "unknown plan type %q (known: %s)", planType, strings.Join(s.cfg.TypeNames(), ", "))
	}
	p.FrontMatter.Type = t
	p.Path = s.pathFor(p, "")
	return s.save(ctx, p, "setType", events.PlanUpdated)
}

// SetStatus moves the plan through the workflow.
func (s *Service) SetStatus(ctx context.Context, identifier, status string, force bool) (*model.Plan, error) {
	p, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	changed, err := s.applyStatus(p, status, force)
	if err != nil {
		return nil, err
	}
	if !changed {
		return p, nil
	}
	p.Path = s.pathFor(p, "")
	return s.save(ctx, p, "setStatus", events.PlanUpdated)
}

// applyStatus normalises and applies a status change; it reports whether
// anything changed.
func (s *Service) applyStatus(p *model.Plan, status string, force bool) (bool, error) {
	to, ok := s.cfg.NormalizeStatus(status)
	if !ok {
		return false, newErr(KindBadRequest, "unknown status %q (known: %s)", status, strings.Join(s.cfg.StatusNames(), ", "))
	}
	from := p.FrontMatter.Status
	if to == from {
		return false, nil
	}
	if !force && !s.cfg.CanTransition(from, to) {
		return false, newErr(KindInvalidTransition, "plan %s cannot move from %q to %q (allowed: %s); use force to override", p.ID(), from, to, strings.Join(s.cfg.Transitions(from), ", "))
	}
	p.FrontMatter.Status = to
	switch {
	case to == "complete":
		p.FrontMatter.Completed = s.timestamp()
	case from == "complete":
		p.FrontMatter.Completed = ""
	}
	return true, nil
}

// Transitions lists where a plan can move next.
type Transitions struct {
	ID          string   `json:"id"`
	Status      string   `json:"status"`
	Transitions []string `json:"transitions"`
}

// GetTransitions lists the statuses reachable from the plan's current status.
func (s *Service) GetTransitions(ctx context.Context, identifier string) (*Transitions, error) {
	p, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	t := s.cfg.Transitions(p.FrontMatter.Status)
	if t == nil {
		t = []string{}
	}
	return &Transitions{ID: p.ID(), Status: p.FrontMatter.Status, Transitions: t}, nil
}

// SetPriority sets the priority from a number or label.
func (s *Service) SetPriority(ctx context.Context, identifier string, priority any) (*model.Plan, error) {
	p, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	v, ok := s.cfg.NormalizePriority(priority)
	if !ok {
		return nil, newErr(KindBadRequest, "unknown priority %v", priority)
	}
	p.FrontMatter.Priority = v
	return s.save(ctx, p, "setPriority", events.PlanUpdated)
}

// SetEffort sets the effort from a size, label or points.
func (s *Service) SetEffort(ctx context.Context, identifier string, effort any) (*model.Plan, error) {
	p, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	v, ok := s.cfg.NormalizeEffort(effort)
	if !ok {
		return nil, newErr(KindBadRequest, "unknown effort %v", effort)
	}
	p.FrontMatter.Effort = v
	return s.save(ctx, p, "setEffort", events.PlanUpdated)
}

// ClearEffort removes the effort.
func (s *Service) ClearEffort(ctx context.Context, identifier string) (*model.Plan, error) {
	p, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	p.FrontMatter.Effort = ""
	return s.save(ctx, p, "clearEffort", events.PlanUpdated)
}

// PatchFrontMatter applies several field changes in one write.
func (s *Service) PatchFrontMatter(ctx context.Context, identifier string, patch map[string]any, force bool) (*model.Plan, error) {
	p, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	for _, k := range []string{"id", "created", "updated", "completed"} {
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
			p.FrontMatter.Title = strings.TrimSpace(str)
		case "type":
			str, _ := v.(string)
			t, ok := s.cfg.NormalizeType(str)
			if !ok {
				return nil, newErr(KindBadRequest, "unknown plan type %v", v)
			}
			p.FrontMatter.Type = t
		case "status":
			str, _ := v.(string)
			if _, err := s.applyStatus(p, str, force); err != nil {
				return nil, err
			}
		case "priority":
			pv, ok := s.cfg.NormalizePriority(v)
			if !ok {
				return nil, newErr(KindBadRequest, "unknown priority %v", v)
			}
			p.FrontMatter.Priority = pv
		case "effort":
			if v == nil || v == "" {
				p.FrontMatter.Effort = ""
				continue
			}
			ev, ok := s.cfg.NormalizeEffort(v)
			if !ok {
				return nil, newErr(KindBadRequest, "unknown effort %v", v)
			}
			p.FrontMatter.Effort = ev
		case "plans":
			// Shorthand for links.plans.
			var links []model.Link
			if err := reencode(v, &links); err != nil {
				return nil, newErr(KindBadRequest, "plans: %v", err)
			}
			for _, l := range links {
				if err := s.checkPlanLink(ctx, p.ID(), l); err != nil {
					return nil, err
				}
			}
			p.FrontMatter.SetPlanLinks(links)
		case "links":
			// Merge per link kind: a kind present in the patch replaces that
			// kind (null clears it); kinds left out are kept.
			if v == nil {
				p.FrontMatter.Links = nil
				continue
			}
			patchLinks, ok := v.(map[string]any)
			if !ok {
				return nil, newErr(KindBadRequest, "links must be an object")
			}
			links := &model.Links{}
			if p.FrontMatter.Links != nil {
				l := *p.FrontMatter.Links
				links = &l
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
				case "plans":
					links.Plans = nil
					if lv != nil {
						err = reencode(lv, &links.Plans)
					}
					for _, l := range links.Plans {
						if err := s.checkPlanLink(ctx, p.ID(), l); err != nil {
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
			p.FrontMatter.Links = links
		case "progress":
			var prog model.Progress
			if err := reencode(v, &prog); err != nil {
				return nil, newErr(KindBadRequest, "progress: %v", err)
			}
			for k, e := range prog {
				if !markdown.HasSection(p.Content, k) {
					return nil, newErr(KindUnknownSection, "progress key %q has no numbered heading in the content", k)
				}
				if n, ok := s.cfg.NormalizeStatus(e.Status); ok {
					e.Status = n
					prog[k] = e
				}
			}
			p.FrontMatter.Progress = prog
		default:
			return nil, newErr(KindBadRequest, "unknown front matter field %q", k)
		}
	}
	p.Path = s.pathFor(p, "")
	return s.save(ctx, p, "patchFrontMatter", events.PlanUpdated)
}

func reencode(from any, to any) error {
	b, err := json.Marshal(from)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, to)
}

// ---------------------------------------------------------------------------
// Front matter: links

func (s *Service) checkPlanLink(ctx context.Context, selfID string, l model.Link) error {
	if !model.PlanIDRe.MatchString(l.ID) {
		return newErr(KindBadRequest, "%q is not a plan id", l.ID)
	}
	if l.ID == selfID {
		return newErr(KindBadRequest, "a plan cannot link to itself")
	}
	if !contains(model.PlanRelations, l.Relation) {
		return newErr(KindBadRequest, "unknown plan relation %q (parent, included, depends, blocks)", l.Relation)
	}
	if _, err := s.store.GetPlan(ctx, l.ID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return newErr(KindNotFound, "linked plan %s not found", l.ID)
		}
		return &Error{Kind: KindStorage, Message: err.Error(), Cause: err}
	}
	return nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// AddPlanLink adds [targetPlanId, relation] to links.plans (duplicates ignored).
func (s *Service) AddPlanLink(ctx context.Context, identifier, target, relation string) (*model.Plan, error) {
	p, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	l := model.Link{ID: target, Relation: relation}
	if err := s.checkPlanLink(ctx, p.ID(), l); err != nil {
		return nil, err
	}
	for _, x := range p.FrontMatter.PlanLinks() {
		if x == l {
			return p, nil
		}
	}
	p.FrontMatter.SetPlanLinks(append(p.FrontMatter.PlanLinks(), l))
	return s.save(ctx, p, "addPlanLink", events.PlanUpdated)
}

// RemovePlanLink removes a plan link (every relation when relation is "").
func (s *Service) RemovePlanLink(ctx context.Context, identifier, target, relation string) (*model.Plan, error) {
	p, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	kept, removed := filterLinks(p.FrontMatter.PlanLinks(), target, relation)
	if removed == 0 {
		return nil, newErr(KindNotFound, "plan %s has no link to %s%s", p.ID(), target, relationSuffix(relation))
	}
	p.FrontMatter.SetPlanLinks(kept)
	return s.save(ctx, p, "removePlanLink", events.PlanUpdated)
}

func relationSuffix(rel string) string {
	if rel == "" {
		return ""
	}
	return " with relation " + rel
}

func filterLinks(links []model.Link, target, relation string) ([]model.Link, int) {
	var kept []model.Link
	removed := 0
	for _, l := range links {
		if l.ID == target && (relation == "" || l.Relation == relation) {
			removed++
			continue
		}
		kept = append(kept, l)
	}
	return kept, removed
}

func (s *Service) ensureLinks(p *model.Plan) *model.Links {
	if p.FrontMatter.Links == nil {
		p.FrontMatter.Links = &model.Links{}
	}
	return p.FrontMatter.Links
}

func (s *Service) tidyLinks(p *model.Plan) {
	if p.FrontMatter.Links != nil && p.FrontMatter.Links.IsEmpty() {
		p.FrontMatter.Links = nil
	}
}

// AddStoryLink adds [storyId, relation] to links.stories.
func (s *Service) AddStoryLink(ctx context.Context, identifier, storyID, relation string) (*model.Plan, error) {
	p, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	if !model.StoryIDRe.MatchString(storyID) {
		return nil, newErr(KindBadRequest, "%q is not a story id", storyID)
	}
	if !contains(model.StoryRelations, relation) {
		return nil, newErr(KindBadRequest, "unknown story relation %q (included, depends, blocks)", relation)
	}
	l := model.Link{ID: storyID, Relation: relation}
	links := s.ensureLinks(p)
	for _, x := range links.Stories {
		if x == l {
			return p, nil
		}
	}
	links.Stories = append(links.Stories, l)
	return s.save(ctx, p, "addStoryLink", events.PlanUpdated)
}

// RemoveStoryLink removes a story link (every relation when relation is "").
func (s *Service) RemoveStoryLink(ctx context.Context, identifier, storyID, relation string) (*model.Plan, error) {
	p, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	var kept []model.Link
	removed := 0
	if p.FrontMatter.Links != nil {
		kept, removed = filterLinks(p.FrontMatter.Links.Stories, storyID, relation)
	}
	if removed == 0 {
		return nil, newErr(KindNotFound, "plan %s has no link to story %s%s", p.ID(), storyID, relationSuffix(relation))
	}
	p.FrontMatter.Links.Stories = kept
	s.tidyLinks(p)
	return s.save(ctx, p, "removeStoryLink", events.PlanUpdated)
}

// AddSpec adds a spec path to links.specs.
func (s *Service) AddSpec(ctx context.Context, identifier, spec string) (*model.Plan, error) {
	p, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, newErr(KindBadRequest, "spec must not be empty")
	}
	links := s.ensureLinks(p)
	if contains(links.Specs, spec) {
		return p, nil
	}
	links.Specs = append(links.Specs, spec)
	return s.save(ctx, p, "addSpec", events.PlanUpdated)
}

// RemoveSpec removes a spec from links.specs.
func (s *Service) RemoveSpec(ctx context.Context, identifier, spec string) (*model.Plan, error) {
	p, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	if p.FrontMatter.Links == nil || !contains(p.FrontMatter.Links.Specs, spec) {
		return nil, newErr(KindNotFound, "plan %s has no spec %q", p.ID(), spec)
	}
	var kept []string
	for _, x := range p.FrontMatter.Links.Specs {
		if x != spec {
			kept = append(kept, x)
		}
	}
	p.FrontMatter.Links.Specs = kept
	s.tidyLinks(p)
	return s.save(ctx, p, "removeSpec", events.PlanUpdated)
}

// SetWebLink adds or replaces a named web link.
func (s *Service) SetWebLink(ctx context.Context, identifier, label, url string) (*model.Plan, error) {
	p, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	label, url = strings.TrimSpace(label), strings.TrimSpace(url)
	if label == "" || url == "" {
		return nil, newErr(KindBadRequest, "label and url are required")
	}
	links := s.ensureLinks(p)
	if links.Web == nil {
		links.Web = map[string]string{}
	}
	links.Web[label] = url
	return s.save(ctx, p, "setWebLink", events.PlanUpdated)
}

// RemoveWebLink removes a named web link.
func (s *Service) RemoveWebLink(ctx context.Context, identifier, label string) (*model.Plan, error) {
	p, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	if p.FrontMatter.Links == nil || p.FrontMatter.Links.Web[label] == "" {
		return nil, newErr(KindNotFound, "plan %s has no web link %q", p.ID(), label)
	}
	delete(p.FrontMatter.Links.Web, label)
	if len(p.FrontMatter.Links.Web) == 0 {
		p.FrontMatter.Links.Web = nil
	}
	s.tidyLinks(p)
	return s.save(ctx, p, "removeWebLink", events.PlanUpdated)
}

// SetRepo sets links.repo.remote and/or links.repo.local.
func (s *Service) SetRepo(ctx context.Context, identifier, remote, local string) (*model.Plan, error) {
	p, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	remote, local = strings.TrimSpace(remote), strings.TrimSpace(local)
	if remote == "" && local == "" {
		return nil, newErr(KindBadRequest, "remote or local is required")
	}
	links := s.ensureLinks(p)
	if links.Repo == nil {
		links.Repo = &model.Repo{}
	}
	if remote != "" {
		links.Repo.Remote = remote
	}
	if local != "" {
		links.Repo.Local = local
	}
	return s.save(ctx, p, "setRepo", events.PlanUpdated)
}

// ClearRepo removes links.repo.
func (s *Service) ClearRepo(ctx context.Context, identifier string) (*model.Plan, error) {
	p, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	if p.FrontMatter.Links == nil || p.FrontMatter.Links.Repo == nil {
		return p, nil
	}
	p.FrontMatter.Links.Repo = nil
	s.tidyLinks(p)
	return s.save(ctx, p, "clearRepo", events.PlanUpdated)
}

// ---------------------------------------------------------------------------
// Front matter: progress

// ProgressReport is the result of GetProgress.
type ProgressReport struct {
	ID       string         `json:"id"`
	Status   string         `json:"status"`
	Sections []string       `json:"sections"`
	Progress model.Progress `json:"progress"`
}

// GetProgress returns the status of every numbered phase and section.
func (s *Service) GetProgress(ctx context.Context, identifier string) (*ProgressReport, error) {
	p, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	prog := p.FrontMatter.Progress
	if prog == nil {
		prog = model.Progress{}
	}
	sections := markdown.SectionNumbers(p.Content)
	if sections == nil {
		sections = []string{}
	}
	return &ProgressReport{ID: p.ID(), Status: p.FrontMatter.Status, Sections: sections, Progress: prog}, nil
}

func (s *Service) requireSection(p *model.Plan, section string) error {
	section = strings.TrimSpace(section)
	if !model.SectionNumberRe.MatchString(section) {
		return newErr(KindBadRequest, "%q is not a section number (e.g. \"2\" or \"1.1\")", section)
	}
	if !markdown.HasSection(p.Content, section) {
		return newErr(KindUnknownSection, "plan %s has no numbered heading %q (found: %s)", p.ID(), section, strings.Join(markdown.SectionNumbers(p.Content), ", "))
	}
	return nil
}

// SetProgress sets a phase or section status.
func (s *Service) SetProgress(ctx context.Context, identifier, section, status string) (*model.Plan, error) {
	p, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	if err := s.requireSection(p, section); err != nil {
		return nil, err
	}
	st, ok := s.cfg.NormalizeStatus(status)
	if !ok {
		return nil, newErr(KindBadRequest, "unknown status %q (known: %s)", status, strings.Join(s.cfg.StatusNames(), ", "))
	}
	if p.FrontMatter.Progress == nil {
		p.FrontMatter.Progress = model.Progress{}
	}
	e := p.FrontMatter.Progress[section]
	e.Status = st
	p.FrontMatter.Progress[section] = e
	return s.save(ctx, p, "setProgress", events.PlanUpdated)
}

// AddProgressStory links a story to a phase or section.
func (s *Service) AddProgressStory(ctx context.Context, identifier, section, storyID string) (*model.Plan, error) {
	p, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	if err := s.requireSection(p, section); err != nil {
		return nil, err
	}
	if !model.StoryIDRe.MatchString(storyID) {
		return nil, newErr(KindBadRequest, "%q is not a story id", storyID)
	}
	if p.FrontMatter.Progress == nil {
		p.FrontMatter.Progress = model.Progress{}
	}
	e, ok := p.FrontMatter.Progress[section]
	if !ok {
		e.Status = config.DefaultStatus
	}
	if contains(e.Stories, storyID) {
		return p, nil
	}
	e.Stories = append(e.Stories, storyID)
	p.FrontMatter.Progress[section] = e
	return s.save(ctx, p, "addProgressStory", events.PlanUpdated)
}

// RemoveProgressStory unlinks a story from a phase or section.
func (s *Service) RemoveProgressStory(ctx context.Context, identifier, section, storyID string) (*model.Plan, error) {
	p, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	e, ok := p.FrontMatter.Progress[section]
	if !ok {
		return nil, newErr(KindUnknownSection, "plan %s has no progress entry %q", p.ID(), section)
	}
	if !contains(e.Stories, storyID) {
		return nil, newErr(KindNotFound, "section %s of plan %s has no story %s", section, p.ID(), storyID)
	}
	var kept []string
	for _, x := range e.Stories {
		if x != storyID {
			kept = append(kept, x)
		}
	}
	e.Stories = kept
	p.FrontMatter.Progress[section] = e
	return s.save(ctx, p, "removeProgressStory", events.PlanUpdated)
}

// RemoveProgress deletes a phase or section's progress entry.
func (s *Service) RemoveProgress(ctx context.Context, identifier, section string) (*model.Plan, error) {
	p, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	if _, ok := p.FrontMatter.Progress[section]; !ok {
		return nil, newErr(KindUnknownSection, "plan %s has no progress entry %q", p.ID(), section)
	}
	delete(p.FrontMatter.Progress, section)
	if len(p.FrontMatter.Progress) == 0 {
		p.FrontMatter.Progress = nil
	}
	return s.save(ctx, p, "removeProgress", events.PlanUpdated)
}

// ---------------------------------------------------------------------------
// Storage

// Sync copies plans and templates from one configured store to another.
func (s *Service) Sync(ctx context.Context, from, to string, mode string) (*store.SyncReport, error) {
	m, ok := s.store.(*store.MultiStore)
	if !ok {
		return nil, newErr(KindBadRequest, "sync needs more than one configured store")
	}
	src, ok := m.Find(from)
	if !ok {
		return nil, newErr(KindNotFound, "store %q is not configured (have: %s)", from, strings.Join(s.StoreNames(), ", "))
	}
	dst, ok := m.Find(to)
	if !ok {
		return nil, newErr(KindNotFound, "store %q is not configured (have: %s)", to, strings.Join(s.StoreNames(), ", "))
	}
	if src == dst {
		return nil, newErr(KindBadRequest, "source and target store are the same")
	}
	cm, err := store.ParseConflictMode(mode)
	if err != nil {
		return nil, newErr(KindBadRequest, "%v", err)
	}
	rep, err := store.Sync(ctx, src, dst, cm)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			return rep, &Error{Kind: KindConflict, Message: err.Error(), Problems: rep.Conflicts, Cause: err}
		}
		return rep, &Error{Kind: KindStorage, Message: err.Error(), Cause: err}
	}
	return rep, nil
}

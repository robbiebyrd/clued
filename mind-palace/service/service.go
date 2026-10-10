// Package service implements the Plan and Story services: every operation an
// entrypoint (CLI, HTTP, WebSocket, MCP) exposes. One Service exists per
// document kind; a Palace groups them over a shared store so cross-kind
// rules (a story linking to a plan, delete protection) can be enforced.
//
// Every write is validated against the kind's JSON Schema, stamps `updated`,
// keeps id/created immutable, syncs the content H1 with the title and
// normalises synonyms before writing.
package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/events"
	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/markdown"
	"github.com/robbiebyrd/clued/mind-palace/model"
	"github.com/robbiebyrd/clued/mind-palace/schema"
	"github.com/robbiebyrd/clued/mind-palace/store"
)

// TimestampLayout is the canonical front matter timestamp (see model).
const TimestampLayout = model.TimestampLayout

// Palace holds one Service per document kind over a shared store and bus.
type Palace struct {
	cfg      *config.Config
	store    store.Store
	bus      *events.Bus
	now      func() time.Time
	randomID func() string
	// mu serialises id allocation across kinds.
	mu       sync.Mutex
	services map[string]*Service
}

// NewPalace builds a service for every registered kind. bus may be nil.
func NewPalace(cfg *config.Config, st store.Store, bus *events.Bus) (*Palace, error) {
	if cfg == nil {
		cfg = config.Default()
	}
	if bus == nil {
		bus = events.New()
	}
	p := &Palace{cfg: cfg, store: st, bus: bus, now: time.Now, randomID: randomID, services: map[string]*Service{}}
	for _, k := range kind.All() {
		kc := cfg.Kind(k.Name)
		if kc == nil {
			return nil, fmt.Errorf("no configuration for kind %q", k.Name)
		}
		v, err := schema.New(k.Name, kc)
		if err != nil {
			return nil, err
		}
		p.services[k.Name] = &Service{palace: p, kind: k, cfg: kc, validator: v}
	}
	return p, nil
}

// New builds a Palace and returns the service for one kind. It is a
// convenience for callers that only need plans or only stories.
func New(kindName string, cfg *config.Config, st store.Store, bus *events.Bus) (*Service, error) {
	p, err := NewPalace(cfg, st, bus)
	if err != nil {
		return nil, err
	}
	return p.Service(kindName)
}

// Service returns the service for a kind name or plural ("plan", "stories").
func (p *Palace) Service(kindName string) (*Service, error) {
	k, ok := kind.Get(kindName)
	if !ok {
		return nil, newErr(KindBadRequest, "unknown document kind %q (known: %s)", kindName, strings.Join(kind.Names(), ", "))
	}
	return p.services[k.Name], nil
}

// Plans returns the plan service.
func (p *Palace) Plans() *Service { return p.services[kind.Plan] }

// Stories returns the story service.
func (p *Palace) Stories() *Service { return p.services[kind.Story] }

// Services returns every service in kind-name order.
func (p *Palace) Services() []*Service {
	var out []*Service
	for _, k := range kind.All() {
		out = append(out, p.services[k.Name])
	}
	return out
}

// Config returns the effective configuration.
func (p *Palace) Config() *config.Config { return p.cfg }

// Bus returns the event bus.
func (p *Palace) Bus() *events.Bus { return p.bus }

// Store returns the underlying store.
func (p *Palace) Store() store.Store { return p.store }

// SetClock overrides the clock (tests).
func (p *Palace) SetClock(now func() time.Time) { p.now = now }

func (p *Palace) timestamp() string { return p.now().UTC().Format(TimestampLayout) }

// StoreNames lists the stores writes fan out to.
func (p *Palace) StoreNames() []string {
	if m, ok := p.store.(*store.MultiStore); ok {
		var out []string
		for _, st := range m.Stores() {
			out = append(out, st.Name()+" ("+st.Driver()+")")
		}
		return out
	}
	return []string{p.store.Name() + " (" + p.store.Driver() + ")"}
}

// Sync copies every document and template from one configured store to another.
func (p *Palace) Sync(ctx context.Context, from, to string, mode string) (*store.SyncReport, error) {
	m, ok := p.store.(*store.MultiStore)
	if !ok {
		return nil, newErr(KindBadRequest, "sync needs more than one configured store")
	}
	src, ok := m.Find(from)
	if !ok {
		return nil, newErr(KindNotFound, "store %q is not configured (have: %s)", from, strings.Join(p.StoreNames(), ", "))
	}
	dst, ok := m.Find(to)
	if !ok {
		return nil, newErr(KindNotFound, "store %q is not configured (have: %s)", to, strings.Join(p.StoreNames(), ", "))
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
			return rep, &Error{Kind: KindConflict, Message: err.Error(), Problems: rep.Conflicts(), Cause: err}
		}
		return rep, storageErr(err)
	}
	return rep, nil
}

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

// Service is the service for one document kind.
type Service struct {
	palace    *Palace
	kind      *kind.Kind
	cfg       *config.KindConfig
	validator *schema.Validator
}

// Kind returns the kind this service manages.
func (s *Service) Kind() *kind.Kind { return s.kind }

// Palace returns the owning palace.
func (s *Service) Palace() *Palace { return s.palace }

// Config returns the effective configuration (every kind).
func (s *Service) Config() *config.Config { return s.palace.cfg }

// KindConfig returns this kind's configuration.
func (s *Service) KindConfig() *config.KindConfig { return s.cfg }

// Bus returns the event bus.
func (s *Service) Bus() *events.Bus { return s.palace.bus }

// Store returns the underlying store.
func (s *Service) Store() store.Store { return s.palace.store }

// Schema returns the effective creation JSON Schema of this kind.
func (s *Service) Schema() []byte { return s.validator.JSON() }

// StoreNames lists the stores writes fan out to.
func (s *Service) StoreNames() []string { return s.palace.StoreNames() }

// SetClock overrides the clock (tests).
func (s *Service) SetClock(now func() time.Time) { s.palace.SetClock(now) }

func (s *Service) timestamp() string { return s.palace.timestamp() }

// other returns the service of another kind.
func (s *Service) other(kindName string) *Service { return s.palace.services[kindName] }

// ---------------------------------------------------------------------------
// Resolution and persistence helpers

// Resolve finds a document by ID or by file path.
func (s *Service) Resolve(ctx context.Context, identifier string) (*model.Document, error) {
	id := strings.TrimSpace(identifier)
	if id == "" {
		return nil, newErr(KindBadRequest, "a %s identifier (id or path) is required", s.kind.Name)
	}
	id = model.NormalizeID(id)
	if !model.IDRe.MatchString(id) {
		fid, _, _, ok := model.ParseFileName(id)
		if !ok {
			return nil, newErr(KindNotFound, "%q is neither a %s id nor a %s file name", identifier, s.kind.Name, s.kind.Name)
		}
		id = fid
	}
	d, err := s.palace.store.Get(ctx, s.kind.Name, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, newErr(KindNotFound, "%s %s not found", s.kind.Name, id)
	}
	if err != nil {
		return nil, storageErr(err)
	}
	d.Kind = s.kind.Name
	return d, nil
}

// exists reports whether a document of this kind exists.
func (s *Service) exists(ctx context.Context, id string) (bool, error) {
	_, err := s.palace.store.Get(ctx, s.kind.Name, id)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, storageErr(err)
	}
	return true, nil
}

// pathFor computes the canonical path: archived documents live under archive/.
func (s *Service) pathFor(d *model.Document, slug string) string {
	if slug == "" {
		if _, _, cur, ok := model.ParseFileName(d.Path); ok {
			slug = cur
		} else {
			slug = model.Slugify(d.FrontMatter.Title)
		}
	}
	name := model.FileName(d.FrontMatter.ID, d.FrontMatter.Type, slug)
	if d.FrontMatter.Status == "archived" {
		return "archive/" + name
	}
	return name
}

// check validates a document before it is written and returns the problems.
// Timestamps and ids are normalised to their canonical forms first.
func (s *Service) check(d *model.Document) []string {
	d.FrontMatter.NormalizeTimestamps()
	d.FrontMatter.NormalizeIDs()
	problems := s.validator.ValidateStoredFrontMatter(d.FrontMatter)
	problems = append(problems, s.invariants(d)...)
	sort.Strings(problems)
	return problems
}

// invariants are the rules beyond the schema.
func (s *Service) invariants(d *model.Document) []string {
	var problems []string
	fm := &d.FrontMatter
	if _, ok := s.cfg.NormalizeType(fm.Type); !ok {
		problems = append(problems, fmt.Sprintf("/type: unknown %s type %q", s.kind.Name, fm.Type))
	}
	if _, ok := s.cfg.Status(fm.Status); !ok {
		problems = append(problems, fmt.Sprintf("/status: unknown status %q", fm.Status))
	}
	if h1, ok := markdown.FirstH1(d.Content); !ok || h1 != fm.Title {
		problems = append(problems, fmt.Sprintf("/content: H1 %q does not match title %q", h1, fm.Title))
	}
	id, typ, _, ok := model.ParseFileName(d.Path)
	switch {
	case !ok:
		problems = append(problems, fmt.Sprintf("/path: %q is not a valid %s file name", d.Path, s.kind.Name))
	case id != fm.ID:
		problems = append(problems, fmt.Sprintf("/path: file name id %q does not match id %q", id, fm.ID))
	case typ != fm.Type:
		problems = append(problems, fmt.Sprintf("/path: file name type %q does not match type %q", typ, fm.Type))
	}
	if ok {
		archived := strings.HasPrefix(d.Path, "archive/")
		if archived != (fm.Status == "archived") {
			problems = append(problems, fmt.Sprintf("/path: %q is inconsistent with status %q", d.Path, fm.Status))
		}
	}
	for _, l := range s.sameKindLinks(fm) {
		if l.ID == fm.ID {
			problems = append(problems, fmt.Sprintf("/links/%s: a %s cannot link to itself", s.kind.Plural, s.kind.Name))
		}
	}
	return problems
}

// sameKindLinks returns the links to documents of this kind.
func (s *Service) sameKindLinks(fm *model.FrontMatter) []model.Link {
	if s.kind.Name == kind.Story {
		return fm.StoryLinks()
	}
	return fm.PlanLinks()
}

// save stamps `updated`, validates and writes the document, then publishes.
func (s *Service) save(ctx context.Context, d *model.Document, op string, action string) (*model.Document, error) {
	d.Kind = s.kind.Name
	if action == events.ActionCreated {
		d.FrontMatter.Updated = d.FrontMatter.Created
	} else {
		d.FrontMatter.Updated = s.timestamp()
	}
	d.Content = markdown.SetH1(d.Content, d.FrontMatter.Title)
	if problems := s.check(d); len(problems) > 0 {
		return nil, &Error{Kind: KindValidation, Message: fmt.Sprintf("%s: %s %s failed validation", op, s.kind.Name, d.ID()), Problems: problems}
	}
	err := s.palace.store.Put(ctx, d)
	var fe *store.FanoutError
	if errors.As(err, &fe) {
		s.publish(action, d, op)
		return d, &Error{Kind: KindStorage, Message: fe.Error(), Partial: true, Cause: err}
	}
	if err != nil {
		return nil, storageErr(err)
	}
	s.publish(action, d, op)
	return d, nil
}

func (s *Service) publish(action string, d *model.Document, op string) {
	if s.palace.bus == nil {
		return
	}
	e := events.Event{Type: s.kind.Event(action), Kind: s.kind.Name, ID: d.ID(), Operation: op, At: s.timestamp()}
	if action != events.ActionDeleted {
		e.Document = d.Clone()
	}
	s.palace.bus.Publish(e)
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

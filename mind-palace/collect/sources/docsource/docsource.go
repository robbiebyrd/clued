// Package docsource implements prime.DocSource over the mind-palace plan and
// story services, opened for one project root the way the CLI opens them.
package docsource

import (
	"context"
	"os"
	"path/filepath"
	"sort"

	"github.com/robbiebyrd/clued/mind-palace/collect/prime"
	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/events"
	"github.com/robbiebyrd/clued/mind-palace/service"
	"github.com/robbiebyrd/clued/mind-palace/store"

	// The default configuration stores documents in files; the driver
	// registers itself on import.
	_ "github.com/robbiebyrd/clued/mind-palace/store/filestore"
)

// configFiles are the mind-palace config file names Open looks for directly
// in the project root, in order.
var configFiles = []string{"mind-palace.config.yaml", "mind-palace.config.yml", "mind-palace.config.json"}

// activeStatuses are the story statuses the STORIES section shows.
var activeStatuses = []string{"pending", "ready", "in_progress"}

// Source reads plans and stories from a palace.
type Source struct {
	palace *service.Palace
	store  store.Store // closed by Close; nil when the palace was supplied
}

var _ prime.DocSource = (*Source)(nil)

// New wraps an existing palace. Close is then a no-op.
func New(p *service.Palace) *Source { return &Source{palace: p} }

// Open opens the palace of the project at root. It returns nil, nil, without
// creating anything, when root holds neither a mind-palace config file nor
// the plans directory (the configured one, docs/plans by default). The
// config file in root is loaded when present, otherwise the defaults are
// used; relative plan and story directories are resolved against root.
func Open(ctx context.Context, root string) (*Source, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var cfg *config.Config
	if path := findConfig(root); path != "" {
		if cfg, err = config.Load(path); err != nil {
			return nil, err
		}
	} else {
		cfg = config.Default()
		if !isDir(resolveAgainst(root, cfg.Plans.Dir)) {
			return nil, nil
		}
	}
	cfg.Plans.Dir = resolveAgainst(root, cfg.Plans.Dir)
	cfg.Stories.Dir = resolveAgainst(root, cfg.Stories.Dir)

	st, err := store.OpenAll(ctx, cfg)
	if err != nil {
		return nil, err
	}
	p, err := service.NewPalace(cfg, st, events.New())
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	return &Source{palace: p, store: st}, nil
}

// findConfig returns the first config file present directly in root, or "".
func findConfig(root string) string {
	for _, name := range configFiles {
		path := filepath.Join(root, name)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path
		}
	}
	return ""
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// resolveAgainst joins a relative path onto base; absolute paths and empty
// strings are returned unchanged.
func resolveAgainst(base, p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(base, p)
}

// ActiveStories lists the pending, ready and in_progress stories by id.
func (s *Source) ActiveStories(ctx context.Context) ([]prime.DocSummary, error) {
	out := []prime.DocSummary{}
	for _, status := range activeStatuses {
		docs, err := s.list(ctx, s.palace.Stories(), status)
		if err != nil {
			return nil, err
		}
		out = append(out, docs...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ActivePlans lists the in_progress plans by id, then the ready ones by id.
func (s *Source) ActivePlans(ctx context.Context) ([]prime.DocSummary, error) {
	out := []prime.DocSummary{}
	for _, status := range []string{"in_progress", "ready"} {
		docs, err := s.list(ctx, s.palace.Plans(), status)
		if err != nil {
			return nil, err
		}
		out = append(out, docs...)
	}
	return out, nil
}

// PlanContent returns the Markdown content of one plan.
func (s *Source) PlanContent(ctx context.Context, id string) (string, error) {
	return s.palace.Plans().GetContent(ctx, id)
}

// Close closes the store Open opened; it is a no-op for a Source made by New.
func (s *Source) Close() error {
	if s.store == nil {
		return nil
	}
	err := s.store.Close()
	s.store = nil
	return err
}

// list returns the summaries of one status, by id as the service orders them.
func (s *Source) list(ctx context.Context, svc *service.Service, status string) ([]prime.DocSummary, error) {
	docs, err := svc.List(ctx, service.ListFilter{Status: status})
	if err != nil {
		return nil, err
	}
	out := make([]prime.DocSummary, 0, len(docs))
	for _, d := range docs {
		fm := d.FrontMatter
		out = append(out, prime.DocSummary{ID: fm.ID, Type: fm.Type, Status: fm.Status, Title: fm.Title})
	}
	return out, nil
}

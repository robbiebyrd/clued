// Package filestore stores plans as Markdown files. It is the default (and
// only enabled-by-default) storage plugin.
//
// Layout under the plans directory (docs/plans by default):
//
//	<id>-<type>-<slug>.md            active plans
//	archive/<id>-<type>-<slug>.md    archived plans
//	templates/<templateId>.md        plan content templates
package filestore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/robbiebyrd/clued/plan/config"
	"github.com/robbiebyrd/clued/plan/markdown"
	"github.com/robbiebyrd/clued/plan/model"
	"github.com/robbiebyrd/clued/plan/store"
)

// Subdirectories inside the plans root.
const (
	ArchiveDir   = "archive"
	TemplatesDir = "templates"
)

func init() {
	store.Register("file", func(def config.StorageDef, plansDir string) (store.Store, error) {
		dir := def.Options["dir"]
		if dir == "" {
			dir = plansDir
		}
		return New(def.Name, dir), nil
	})
}

// Store is a file-based plan store.
type Store struct {
	name string
	root string
}

// New creates a store rooted at dir ("~" is expanded).
func New(name, dir string) *Store {
	if name == "" {
		name = "file"
	}
	return &Store{name: name, root: expandHome(dir)}
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") || p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// Root returns the plans directory.
func (s *Store) Root() string { return s.root }

func (s *Store) Name() string { return s.name }
func (s *Store) Kind() string { return "file" }

func (s *Store) Init(context.Context) error {
	for _, d := range []string{s.root, filepath.Join(s.root, ArchiveDir)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Close() error { return nil }

// AbsPath returns the absolute location of a plan-relative path.
func (s *Store) AbsPath(rel string) string {
	return filepath.Join(s.root, filepath.FromSlash(rel))
}

// scan lists plan files in the root and archive directories.
func (s *Store) scan() ([]string, error) {
	var out []string
	for _, sub := range []string{"", ArchiveDir} {
		entries, err := os.ReadDir(filepath.Join(s.root, sub))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if _, _, _, ok := model.ParseFileName(e.Name()); !ok {
				continue
			}
			rel := e.Name()
			if sub != "" {
				rel = sub + "/" + e.Name()
			}
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (s *Store) read(rel string) (*model.Plan, error) {
	data, err := os.ReadFile(s.AbsPath(rel))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	fm, content, err := markdown.Parse(string(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	if fm.ID == "" {
		if id, _, _, ok := model.ParseFileName(rel); ok {
			fm.ID = id
		}
	}
	return &model.Plan{Path: rel, FrontMatter: fm, Content: content}, nil
}

func (s *Store) ListPlans(context.Context) ([]*model.Plan, error) {
	paths, err := s.scan()
	if err != nil {
		return nil, err
	}
	var out []*model.Plan
	for _, rel := range paths {
		p, err := s.read(rel)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out, nil
}

// findPath locates the file holding a plan ID.
func (s *Store) findPath(id string) (string, error) {
	paths, err := s.scan()
	if err != nil {
		return "", err
	}
	for _, rel := range paths {
		if fid, _, _, _ := model.ParseFileName(rel); fid == id {
			return rel, nil
		}
	}
	return "", store.ErrNotFound
}

func (s *Store) GetPlan(_ context.Context, id string) (*model.Plan, error) {
	rel, err := s.findPath(id)
	if err != nil {
		return nil, err
	}
	return s.read(rel)
}

func (s *Store) PutPlan(_ context.Context, p *model.Plan) error {
	if p.Path == "" {
		return errors.New("plan has no path")
	}
	doc, err := markdown.RenderPlan(p)
	if err != nil {
		return err
	}
	prev, err := s.findPath(p.ID())
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	abs := s.AbsPath(p.Path)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	if err := writeAtomic(abs, []byte(doc)); err != nil {
		return err
	}
	if prev != "" && prev != p.Path {
		if err := os.Remove(s.AbsPath(prev)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (s *Store) DeletePlan(_ context.Context, id string) error {
	rel, err := s.findPath(id)
	if err != nil {
		return err
	}
	return os.Remove(s.AbsPath(rel))
}

func (s *Store) templatePath(id string) string {
	return filepath.Join(s.root, TemplatesDir, id+".md")
}

func (s *Store) ListTemplates(context.Context) ([]*model.Template, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, TemplatesDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []*model.Template
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		t, err := s.GetTemplate(context.Background(), strings.TrimSuffix(e.Name(), ".md"))
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

func (s *Store) GetTemplate(_ context.Context, id string) (*model.Template, error) {
	if err := checkTemplateID(id); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(s.templatePath(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	info, _ := os.Stat(s.templatePath(id))
	t := &model.Template{ID: id, Content: string(data)}
	if info != nil {
		t.Updated = info.ModTime().UTC().Format(time.RFC3339Nano)
	}
	return t, nil
}

func (s *Store) PutTemplate(_ context.Context, t *model.Template) error {
	if err := checkTemplateID(t.ID); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(s.root, TemplatesDir), 0o755); err != nil {
		return err
	}
	return writeAtomic(s.templatePath(t.ID), []byte(t.Content))
}

func (s *Store) DeleteTemplate(_ context.Context, id string) error {
	if err := checkTemplateID(id); err != nil {
		return err
	}
	err := os.Remove(s.templatePath(id))
	if errors.Is(err, os.ErrNotExist) {
		return store.ErrNotFound
	}
	return err
}

func checkTemplateID(id string) error {
	if id == "" || strings.ContainsAny(id, `/\`) || id == "." || id == ".." {
		return fmt.Errorf("invalid template id %q", id)
	}
	return nil
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".plan-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

var _ store.Store = (*Store)(nil)

// Package filestore stores documents as Markdown files. It is the default
// (and only enabled-by-default) storage plugin.
//
// Each kind has its own root (docs/plans and docs/stories by default):
//
//	<id>-<type>-<slug>.md            active documents
//	archive/<id>-<type>-<slug>.md    archived documents
//	templates/<templateId>.md        content templates of that kind
//
// Options: plansDir, storiesDir (or <plural>Dir for any kind) override the
// configured directories; dir is a legacy alias of plansDir.
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

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/markdown"
	"github.com/robbiebyrd/clued/mind-palace/model"
	"github.com/robbiebyrd/clued/mind-palace/store"
)

// Subdirectories inside each kind's root.
const (
	ArchiveDir   = "archive"
	TemplatesDir = "templates"
)

func init() {
	store.Register("file", func(def config.StorageDef, dirs map[string]string) (store.Store, error) {
		roots := map[string]string{}
		for _, k := range kind.All() {
			dir := def.Options[k.Plural+"Dir"]
			if dir == "" && k.Name == kind.Plan {
				dir = def.Options["dir"]
			}
			if dir == "" {
				dir = dirs[k.Name]
			}
			if dir == "" {
				dir = k.DefaultDir
			}
			roots[k.Name] = dir
		}
		return New(def.Name, roots), nil
	})
}

// Store is a file-based document store.
type Store struct {
	name  string
	roots map[string]string
}

// New creates a store with one root directory per kind name ("~" is
// expanded). Kinds without a root fall back to their default directory.
func New(name string, roots map[string]string) *Store {
	if name == "" {
		name = "file"
	}
	s := &Store{name: name, roots: map[string]string{}}
	for _, k := range kind.All() {
		dir := roots[k.Name]
		if dir == "" {
			dir = k.DefaultDir
		}
		s.roots[k.Name] = expandHome(dir)
	}
	return s
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") || p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// Root returns the directory of a kind.
func (s *Store) Root(kindName string) string { return s.roots[kindName] }

func (s *Store) Name() string   { return s.name }
func (s *Store) Driver() string { return "file" }

func (s *Store) Init(context.Context) error {
	for _, root := range s.roots {
		for _, d := range []string{root, filepath.Join(root, ArchiveDir)} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) Close() error { return nil }

func (s *Store) root(kindName string) (string, error) {
	root, ok := s.roots[kindName]
	if !ok {
		return "", fmt.Errorf("unknown document kind %q", kindName)
	}
	return root, nil
}

// AbsPath returns the absolute location of a kind-relative path.
func (s *Store) AbsPath(kindName, rel string) string {
	return filepath.Join(s.roots[kindName], filepath.FromSlash(rel))
}

// scan lists document files in the root and archive directories of a kind.
func (s *Store) scan(kindName string) ([]string, error) {
	root, err := s.root(kindName)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, sub := range []string{"", ArchiveDir} {
		entries, err := os.ReadDir(filepath.Join(root, sub))
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

func (s *Store) read(kindName, rel string) (*model.Document, error) {
	data, err := os.ReadFile(s.AbsPath(kindName, rel))
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
	return &model.Document{Kind: kindName, Path: rel, FrontMatter: fm, Content: content}, nil
}

func (s *Store) List(_ context.Context, kindName string) ([]*model.Document, error) {
	paths, err := s.scan(kindName)
	if err != nil {
		return nil, err
	}
	var out []*model.Document
	for _, rel := range paths {
		d, err := s.read(kindName, rel)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out, nil
}

// findPath locates the file holding a document ID.
func (s *Store) findPath(kindName, id string) (string, error) {
	paths, err := s.scan(kindName)
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

func (s *Store) Get(_ context.Context, kindName, id string) (*model.Document, error) {
	rel, err := s.findPath(kindName, id)
	if err != nil {
		return nil, err
	}
	return s.read(kindName, rel)
}

func (s *Store) Put(_ context.Context, d *model.Document) error {
	if d.Path == "" {
		return errors.New("document has no path")
	}
	if _, err := s.root(d.Kind); err != nil {
		return err
	}
	doc, err := markdown.RenderDocument(d)
	if err != nil {
		return err
	}
	prev, err := s.findPath(d.Kind, d.ID())
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	abs := s.AbsPath(d.Kind, d.Path)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	if err := writeAtomic(abs, []byte(doc)); err != nil {
		return err
	}
	if prev != "" && prev != d.Path {
		if err := os.Remove(s.AbsPath(d.Kind, prev)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (s *Store) Delete(_ context.Context, kindName, id string) error {
	rel, err := s.findPath(kindName, id)
	if err != nil {
		return err
	}
	return os.Remove(s.AbsPath(kindName, rel))
}

func (s *Store) templatePath(kindName, id string) string {
	return filepath.Join(s.roots[kindName], TemplatesDir, id+".md")
}

func (s *Store) ListTemplates(ctx context.Context, kindName string) ([]*model.Template, error) {
	root, err := s.root(kindName)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(root, TemplatesDir))
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
		t, err := s.GetTemplate(ctx, kindName, strings.TrimSuffix(e.Name(), ".md"))
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

func (s *Store) GetTemplate(_ context.Context, kindName, id string) (*model.Template, error) {
	if err := checkTemplateID(id); err != nil {
		return nil, err
	}
	if _, err := s.root(kindName); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(s.templatePath(kindName, id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	info, _ := os.Stat(s.templatePath(kindName, id))
	t := &model.Template{Kind: kindName, ID: id, Content: string(data)}
	if info != nil {
		t.Updated = info.ModTime().UTC().Format(time.RFC3339Nano)
	}
	return t, nil
}

func (s *Store) PutTemplate(_ context.Context, t *model.Template) error {
	if err := checkTemplateID(t.ID); err != nil {
		return err
	}
	root, err := s.root(t.Kind)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, TemplatesDir), 0o755); err != nil {
		return err
	}
	return writeAtomic(s.templatePath(t.Kind, t.ID), []byte(t.Content))
}

func (s *Store) DeleteTemplate(_ context.Context, kindName, id string) error {
	if err := checkTemplateID(id); err != nil {
		return err
	}
	if _, err := s.root(kindName); err != nil {
		return err
	}
	err := os.Remove(s.templatePath(kindName, id))
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
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mind-palace-*.tmp")
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

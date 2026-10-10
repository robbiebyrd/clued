package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"

	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/model"
)

// ConflictMode says what Sync does when a document exists in both stores
// with different contents.
type ConflictMode string

const (
	// ConflictError stops at the first conflict.
	ConflictError ConflictMode = "error"
	// ConflictSkip leaves the target copy alone and reports the conflict.
	ConflictSkip ConflictMode = "skip"
	// ConflictOverwrite replaces the target copy with the source copy.
	ConflictOverwrite ConflictMode = "overwrite"
)

// ParseConflictMode accepts the mode names and a few aliases.
func ParseConflictMode(s string) (ConflictMode, error) {
	switch s {
	case "", "error", "fail", "abort":
		return ConflictError, nil
	case "skip", "report":
		return ConflictSkip, nil
	case "overwrite", "force", "source":
		return ConflictOverwrite, nil
	}
	return "", fmt.Errorf("unknown conflict mode %q (error, skip, overwrite)", s)
}

// KindSync summarises the documents of one kind in a Sync run.
type KindSync struct {
	Copied      []string `json:"copied"`
	Unchanged   []string `json:"unchanged"`
	Overwritten []string `json:"overwritten"`
	Conflicts   []string `json:"conflicts"`
}

func newKindSync() *KindSync {
	return &KindSync{Copied: []string{}, Unchanged: []string{}, Overwritten: []string{}, Conflicts: []string{}}
}

// SyncReport summarises a Sync run. Documents is keyed by kind name;
// template ids are reported as "<kind>/<id>".
type SyncReport struct {
	From          string               `json:"from"`
	To            string               `json:"to"`
	Mode          string               `json:"mode"`
	Documents     map[string]*KindSync `json:"documents"`
	Templates     []string             `json:"templates"`
	TemplateConfl []string             `json:"templateConflicts"`
}

// Conflicts lists every conflicting document as "<kind> <id>", kind by kind.
func (r *SyncReport) Conflicts() []string {
	var out []string
	for _, k := range kind.Names() {
		if ks := r.Documents[k]; ks != nil {
			for _, id := range ks.Conflicts {
				out = append(out, k+" "+id)
			}
		}
	}
	return out
}

// ErrConflict is wrapped by the error Sync returns in ConflictError mode.
var ErrConflict = errors.New("sync conflict")

// Sync copies every document of every kind, and every template, from one
// store into another. Only items present in the source are considered; the
// target may hold more.
func Sync(ctx context.Context, from, to Store, mode ConflictMode) (*SyncReport, error) {
	rep := &SyncReport{From: from.Name(), To: to.Name(), Mode: string(mode),
		Documents: map[string]*KindSync{}, Templates: []string{}, TemplateConfl: []string{}}
	for _, k := range kind.All() {
		ks := newKindSync()
		rep.Documents[k.Name] = ks
		if err := syncDocuments(ctx, from, to, mode, k.Name, ks); err != nil {
			return rep, err
		}
	}
	for _, k := range kind.All() {
		if err := syncTemplates(ctx, from, to, mode, k.Name, rep); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

func syncDocuments(ctx context.Context, from, to Store, mode ConflictMode, k string, ks *KindSync) error {
	docs, err := from.List(ctx, k)
	if err != nil {
		return fmt.Errorf("list %ss in %q: %w", k, from.Name(), err)
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].ID() < docs[j].ID() })
	for _, d := range docs {
		existing, err := to.Get(ctx, k, d.ID())
		switch {
		case errors.Is(err, ErrNotFound):
			if err := to.Put(ctx, d.Clone()); err != nil {
				return fmt.Errorf("copy %s %s to %q: %w", k, d.ID(), to.Name(), err)
			}
			ks.Copied = append(ks.Copied, d.ID())
		case err != nil:
			return fmt.Errorf("read %s %s in %q: %w", k, d.ID(), to.Name(), err)
		case DocumentsEqual(existing, d):
			ks.Unchanged = append(ks.Unchanged, d.ID())
		default:
			switch mode {
			case ConflictOverwrite:
				if err := to.Put(ctx, d.Clone()); err != nil {
					return fmt.Errorf("overwrite %s %s in %q: %w", k, d.ID(), to.Name(), err)
				}
				ks.Overwritten = append(ks.Overwritten, d.ID())
			case ConflictSkip:
				ks.Conflicts = append(ks.Conflicts, d.ID())
			default:
				ks.Conflicts = append(ks.Conflicts, d.ID())
				return fmt.Errorf("%w: %s %s differs between %q and %q", ErrConflict, k, d.ID(), from.Name(), to.Name())
			}
		}
	}
	return nil
}

func syncTemplates(ctx context.Context, from, to Store, mode ConflictMode, k string, rep *SyncReport) error {
	templates, err := from.ListTemplates(ctx, k)
	if err != nil {
		return fmt.Errorf("list %s templates in %q: %w", k, from.Name(), err)
	}
	for _, t := range templates {
		label := k + "/" + t.ID
		existing, err := to.GetTemplate(ctx, k, t.ID)
		switch {
		case errors.Is(err, ErrNotFound):
			c := *t
			if err := to.PutTemplate(ctx, &c); err != nil {
				return fmt.Errorf("copy template %s: %w", label, err)
			}
			rep.Templates = append(rep.Templates, label)
		case err != nil:
			return err
		case existing.Content == t.Content:
		default:
			switch mode {
			case ConflictOverwrite:
				c := *t
				if err := to.PutTemplate(ctx, &c); err != nil {
					return err
				}
				rep.Templates = append(rep.Templates, label)
			case ConflictSkip:
				rep.TemplateConfl = append(rep.TemplateConfl, label)
			default:
				rep.TemplateConfl = append(rep.TemplateConfl, label)
				return fmt.Errorf("%w: template %s differs between %q and %q", ErrConflict, label, from.Name(), to.Name())
			}
		}
	}
	return nil
}

// DocumentsEqual compares kind, path, front matter and content.
func DocumentsEqual(a, b *model.Document) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Kind == b.Kind && a.Path == b.Path && a.Content == b.Content && reflect.DeepEqual(normalizeFM(a.FrontMatter), normalizeFM(b.FrontMatter))
}

// normalizeFM makes nil and empty collections compare equal.
func normalizeFM(f model.FrontMatter) model.FrontMatter {
	c := f.Clone()
	if len(c.Progress) == 0 {
		c.Progress = nil
	}
	if c.Links != nil {
		if len(c.Links.Specs) == 0 {
			c.Links.Specs = nil
		}
		if len(c.Links.Web) == 0 {
			c.Links.Web = nil
		}
		if len(c.Links.Stories) == 0 {
			c.Links.Stories = nil
		}
		if len(c.Links.Plans) == 0 {
			c.Links.Plans = nil
		}
		if c.Links.Repo != nil && len(c.Links.Repo.Files) == 0 {
			c.Links.Repo.Files = nil
		}
		if c.Links.IsEmpty() {
			c.Links = nil
		}
	}
	for k, e := range c.Progress {
		if len(e.Stories) == 0 {
			e.Stories = nil
			c.Progress[k] = e
		}
	}
	if c.Links != nil {
		for i := range c.Links.Plans {
			if len(c.Links.Plans[i].Sections) == 0 {
				c.Links.Plans[i].Sections = nil
			}
		}
	}
	return c
}

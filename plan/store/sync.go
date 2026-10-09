package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"

	"github.com/robbiebyrd/clued/plan/model"
)

// ConflictMode says what Sync does when a plan exists in both stores with
// different contents.
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

// SyncReport summarises a Sync run.
type SyncReport struct {
	From          string   `json:"from"`
	To            string   `json:"to"`
	Mode          string   `json:"mode"`
	Copied        []string `json:"copied"`
	Unchanged     []string `json:"unchanged"`
	Overwritten   []string `json:"overwritten"`
	Conflicts     []string `json:"conflicts"`
	Templates     []string `json:"templates"`
	TemplateConfl []string `json:"templateConflicts"`
}

// ErrConflict is wrapped by the error Sync returns in ConflictError mode.
var ErrConflict = errors.New("sync conflict")

// Sync copies every plan and template from one store into another.
// Only plans present in the source are considered; the target may hold more.
func Sync(ctx context.Context, from, to Store, mode ConflictMode) (*SyncReport, error) {
	rep := &SyncReport{From: from.Name(), To: to.Name(), Mode: string(mode),
		Copied: []string{}, Unchanged: []string{}, Overwritten: []string{}, Conflicts: []string{}, Templates: []string{}, TemplateConfl: []string{}}
	plans, err := from.ListPlans(ctx)
	if err != nil {
		return rep, fmt.Errorf("list plans in %q: %w", from.Name(), err)
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].ID() < plans[j].ID() })
	for _, p := range plans {
		existing, err := to.GetPlan(ctx, p.ID())
		switch {
		case errors.Is(err, ErrNotFound):
			if err := to.PutPlan(ctx, p.Clone()); err != nil {
				return rep, fmt.Errorf("copy %s to %q: %w", p.ID(), to.Name(), err)
			}
			rep.Copied = append(rep.Copied, p.ID())
		case err != nil:
			return rep, fmt.Errorf("read %s in %q: %w", p.ID(), to.Name(), err)
		case PlansEqual(existing, p):
			rep.Unchanged = append(rep.Unchanged, p.ID())
		default:
			switch mode {
			case ConflictOverwrite:
				if err := to.PutPlan(ctx, p.Clone()); err != nil {
					return rep, fmt.Errorf("overwrite %s in %q: %w", p.ID(), to.Name(), err)
				}
				rep.Overwritten = append(rep.Overwritten, p.ID())
			case ConflictSkip:
				rep.Conflicts = append(rep.Conflicts, p.ID())
			default:
				rep.Conflicts = append(rep.Conflicts, p.ID())
				return rep, fmt.Errorf("%w: plan %s differs between %q and %q", ErrConflict, p.ID(), from.Name(), to.Name())
			}
		}
	}
	templates, err := from.ListTemplates(ctx)
	if err != nil {
		return rep, fmt.Errorf("list templates in %q: %w", from.Name(), err)
	}
	for _, t := range templates {
		existing, err := to.GetTemplate(ctx, t.ID)
		switch {
		case errors.Is(err, ErrNotFound):
			c := *t
			if err := to.PutTemplate(ctx, &c); err != nil {
				return rep, fmt.Errorf("copy template %s: %w", t.ID, err)
			}
			rep.Templates = append(rep.Templates, t.ID)
		case err != nil:
			return rep, err
		case existing.Content == t.Content:
		default:
			switch mode {
			case ConflictOverwrite:
				c := *t
				if err := to.PutTemplate(ctx, &c); err != nil {
					return rep, err
				}
				rep.Templates = append(rep.Templates, t.ID)
			case ConflictSkip:
				rep.TemplateConfl = append(rep.TemplateConfl, t.ID)
			default:
				rep.TemplateConfl = append(rep.TemplateConfl, t.ID)
				return rep, fmt.Errorf("%w: template %s differs between %q and %q", ErrConflict, t.ID, from.Name(), to.Name())
			}
		}
	}
	return rep, nil
}

// PlansEqual compares path, front matter and content.
func PlansEqual(a, b *model.Plan) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Path == b.Path && a.Content == b.Content && reflect.DeepEqual(normalizeFM(a.FrontMatter), normalizeFM(b.FrontMatter))
}

// normalizeFM makes nil and empty collections compare equal.
func normalizeFM(f model.FrontMatter) model.FrontMatter {
	c := f.Clone()
	if len(c.Plans) == 0 {
		c.Plans = nil
	}
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
	return c
}

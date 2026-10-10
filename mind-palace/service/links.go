package service

import (
	"context"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/events"
	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/model"
)

// checkPlanLink validates a link to a plan from a document of this kind:
// the id shape, the relation, no self-link, the plan exists and (for links
// that carry sections) every section is in the plan's progress map.
func (s *Service) checkPlanLink(ctx context.Context, selfID string, l model.Link) error {
	if !model.PlanIDRe.MatchString(l.ID) {
		return newErr(KindBadRequest, "%q is not a plan id (expected AAAA-BBB, e.g. 0001-abc)", l.ID)
	}
	if s.kind.Name == kind.Plan && l.ID == selfID {
		return newErr(KindBadRequest, "a plan cannot link to itself")
	}
	if !contains(s.kind.PlanRelations, l.Relation) {
		return newErr(KindBadRequest, "unknown plan relation %q (%s)", l.Relation, strings.Join(s.kind.PlanRelations, ", "))
	}
	if len(l.Sections) > 0 && !s.kind.PlanLinkSections {
		return newErr(KindBadRequest, "plan links from a %s cannot carry sections", s.kind.Name)
	}
	plan, err := s.other(kind.Plan).Resolve(ctx, l.ID)
	if err != nil {
		if IsKind(err, KindNotFound) {
			return newErr(KindNotFound, "linked plan %s not found", l.ID)
		}
		return err
	}
	return s.checkPlanSections(plan, l.Sections)
}

// checkPlanSections verifies every section is a key of the plan's progress map.
func (s *Service) checkPlanSections(plan *model.Document, sections []string) error {
	for _, sec := range sections {
		if !model.SectionNumberRe.MatchString(sec) {
			return newErr(KindBadRequest, "%q is not a plan section number (e.g. \"2\" or \"1.1\")", sec)
		}
		if _, ok := plan.FrontMatter.Progress[sec]; !ok {
			return newErr(KindUnknownSection, "plan %s has no section %q in its progress map (have: %s)", plan.ID(), sec, strings.Join(plan.FrontMatter.Progress.SortedKeys(), ", "))
		}
	}
	return nil
}

// checkStoryLink validates a link to a story from a document of this kind.
// Stories must exist when the kind requires same-kind targets to exist;
// plans may reference stories that are tracked elsewhere.
func (s *Service) checkStoryLink(ctx context.Context, selfID string, l model.Link) error {
	if !model.StoryIDRe.MatchString(l.ID) {
		return newErr(KindBadRequest, "%q is not a story id (expected AAAA-BBB, e.g. 0001-abc)", l.ID)
	}
	if s.kind.Name == kind.Story && l.ID == selfID {
		return newErr(KindBadRequest, "a story cannot link to itself")
	}
	if !contains(s.kind.StoryRelations, l.Relation) {
		return newErr(KindBadRequest, "unknown story relation %q (%s)", l.Relation, strings.Join(s.kind.StoryRelations, ", "))
	}
	if len(l.Sections) > 0 {
		return newErr(KindBadRequest, "story links cannot carry sections")
	}
	if s.kind.Name == kind.Story && s.kind.LinkTargetsMustExist {
		ok, err := s.exists(ctx, l.ID)
		if err != nil {
			return err
		}
		if !ok {
			return newErr(KindNotFound, "linked story %s not found", l.ID)
		}
	}
	return nil
}

// AddPlanLink adds [planId, relation] to links.plans (duplicates ignored).
// sections, for kinds that support them, lists the plan sections this
// document implements; each must be in the plan's progress map.
func (s *Service) AddPlanLink(ctx context.Context, identifier, target, relation string, sections []string) (*model.Document, error) {
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	l := model.Link{ID: model.NormalizeID(target), Relation: relation, Sections: cleanSections(sections)}
	if err := s.checkPlanLink(ctx, d.ID(), l); err != nil {
		return nil, err
	}
	links := d.FrontMatter.PlanLinks()
	for i, x := range links {
		if x.Same(l) {
			if len(l.Sections) == 0 || equalStrings(x.Sections, l.Sections) {
				return d, nil
			}
			links[i].Sections = l.Sections
			d.FrontMatter.SetPlanLinks(links)
			return s.save(ctx, d, "addPlanLink", events.ActionUpdated)
		}
	}
	d.FrontMatter.SetPlanLinks(append(links, l))
	return s.save(ctx, d, "addPlanLink", events.ActionUpdated)
}

// SetPlanSections replaces the section list on every link to a plan. An
// empty list removes the third element.
func (s *Service) SetPlanSections(ctx context.Context, identifier, target string, sections []string) (*model.Document, error) {
	if !s.kind.PlanLinkSections {
		return nil, newErr(KindBadRequest, "plan links from a %s cannot carry sections", s.kind.Name)
	}
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	target = model.NormalizeID(target)
	sections = cleanSections(sections)
	links := d.FrontMatter.PlanLinks()
	found := false
	for i := range links {
		if links[i].ID == target {
			found = true
			links[i].Sections = sections
		}
	}
	if !found {
		return nil, newErr(KindNotFound, "%s %s has no link to plan %s", s.kind.Name, d.ID(), target)
	}
	if len(sections) > 0 {
		plan, err := s.other(kind.Plan).Resolve(ctx, target)
		if err != nil {
			if IsKind(err, KindNotFound) {
				return nil, newErr(KindNotFound, "linked plan %s not found", target)
			}
			return nil, err
		}
		if err := s.checkPlanSections(plan, sections); err != nil {
			return nil, err
		}
	}
	d.FrontMatter.SetPlanLinks(links)
	return s.save(ctx, d, "setPlanSections", events.ActionUpdated)
}

func cleanSections(in []string) []string {
	var out []string
	for _, s := range in {
		for _, part := range strings.Split(s, ",") {
			if p := strings.TrimSpace(part); p != "" && !contains(out, p) {
				out = append(out, p)
			}
		}
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// RemovePlanLink removes a plan link (every relation when relation is "").
func (s *Service) RemovePlanLink(ctx context.Context, identifier, target, relation string) (*model.Document, error) {
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	target = model.NormalizeID(target)
	kept, removed := filterLinks(d.FrontMatter.PlanLinks(), target, relation)
	if removed == 0 {
		return nil, newErr(KindNotFound, "%s %s has no link to plan %s%s", s.kind.Name, d.ID(), target, relationSuffix(relation))
	}
	d.FrontMatter.SetPlanLinks(kept)
	return s.save(ctx, d, "removePlanLink", events.ActionUpdated)
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

func (s *Service) ensureLinks(d *model.Document) *model.Links {
	if d.FrontMatter.Links == nil {
		d.FrontMatter.Links = &model.Links{}
	}
	return d.FrontMatter.Links
}

func (s *Service) tidyLinks(d *model.Document) {
	if d.FrontMatter.Links != nil && d.FrontMatter.Links.IsEmpty() {
		d.FrontMatter.Links = nil
	}
}

// AddStoryLink adds [storyId, relation] to links.stories (duplicates ignored).
func (s *Service) AddStoryLink(ctx context.Context, identifier, target, relation string) (*model.Document, error) {
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	l := model.Link{ID: model.NormalizeID(target), Relation: relation}
	if err := s.checkStoryLink(ctx, d.ID(), l); err != nil {
		return nil, err
	}
	for _, x := range d.FrontMatter.StoryLinks() {
		if x.Same(l) {
			return d, nil
		}
	}
	d.FrontMatter.SetStoryLinks(append(d.FrontMatter.StoryLinks(), l))
	return s.save(ctx, d, "addStoryLink", events.ActionUpdated)
}

// RemoveStoryLink removes a story link (every relation when relation is "").
func (s *Service) RemoveStoryLink(ctx context.Context, identifier, target, relation string) (*model.Document, error) {
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	target = model.NormalizeID(target)
	kept, removed := filterLinks(d.FrontMatter.StoryLinks(), target, relation)
	if removed == 0 {
		return nil, newErr(KindNotFound, "%s %s has no link to story %s%s", s.kind.Name, d.ID(), target, relationSuffix(relation))
	}
	d.FrontMatter.SetStoryLinks(kept)
	return s.save(ctx, d, "removeStoryLink", events.ActionUpdated)
}

// AddSpec adds a spec path to links.specs.
func (s *Service) AddSpec(ctx context.Context, identifier, spec string) (*model.Document, error) {
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, newErr(KindBadRequest, "spec must not be empty")
	}
	links := s.ensureLinks(d)
	if contains(links.Specs, spec) {
		return d, nil
	}
	links.Specs = append(links.Specs, spec)
	return s.save(ctx, d, "addSpec", events.ActionUpdated)
}

// RemoveSpec removes a spec from links.specs.
func (s *Service) RemoveSpec(ctx context.Context, identifier, spec string) (*model.Document, error) {
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	if d.FrontMatter.Links == nil || !contains(d.FrontMatter.Links.Specs, spec) {
		return nil, newErr(KindNotFound, "%s %s has no spec %q", s.kind.Name, d.ID(), spec)
	}
	var kept []string
	for _, x := range d.FrontMatter.Links.Specs {
		if x != spec {
			kept = append(kept, x)
		}
	}
	d.FrontMatter.Links.Specs = kept
	s.tidyLinks(d)
	return s.save(ctx, d, "removeSpec", events.ActionUpdated)
}

// SetWebLink adds or replaces a named web link.
func (s *Service) SetWebLink(ctx context.Context, identifier, label, url string) (*model.Document, error) {
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	label, url = strings.TrimSpace(label), strings.TrimSpace(url)
	if label == "" || url == "" {
		return nil, newErr(KindBadRequest, "label and url are required")
	}
	links := s.ensureLinks(d)
	if links.Web == nil {
		links.Web = map[string]string{}
	}
	links.Web[label] = url
	return s.save(ctx, d, "setWebLink", events.ActionUpdated)
}

// RemoveWebLink removes a named web link.
func (s *Service) RemoveWebLink(ctx context.Context, identifier, label string) (*model.Document, error) {
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	if d.FrontMatter.Links == nil || d.FrontMatter.Links.Web[label] == "" {
		return nil, newErr(KindNotFound, "%s %s has no web link %q", s.kind.Name, d.ID(), label)
	}
	delete(d.FrontMatter.Links.Web, label)
	if len(d.FrontMatter.Links.Web) == 0 {
		d.FrontMatter.Links.Web = nil
	}
	s.tidyLinks(d)
	return s.save(ctx, d, "removeWebLink", events.ActionUpdated)
}

// SetRepo sets links.repo.remote, links.repo.local and/or links.repo.pull_request.
func (s *Service) SetRepo(ctx context.Context, identifier, remote, local, pullRequest string) (*model.Document, error) {
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	remote, local, pullRequest = strings.TrimSpace(remote), strings.TrimSpace(local), strings.TrimSpace(pullRequest)
	if remote == "" && local == "" && pullRequest == "" {
		return nil, newErr(KindBadRequest, "remote, local or pullRequest is required")
	}
	if pullRequest != "" && !s.kind.RepoFiles {
		return nil, newErr(KindBadRequest, "%ss have no links.repo.pull_request", s.kind.Name)
	}
	links := s.ensureLinks(d)
	if links.Repo == nil {
		links.Repo = &model.Repo{}
	}
	if remote != "" {
		links.Repo.Remote = remote
	}
	if local != "" {
		links.Repo.Local = local
	}
	if pullRequest != "" {
		links.Repo.PullRequest = pullRequest
	}
	return s.save(ctx, d, "setRepo", events.ActionUpdated)
}

// ClearRepo removes links.repo (files included).
func (s *Service) ClearRepo(ctx context.Context, identifier string) (*model.Document, error) {
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	if d.FrontMatter.Links == nil || d.FrontMatter.Links.Repo == nil {
		return d, nil
	}
	d.FrontMatter.Links.Repo = nil
	s.tidyLinks(d)
	return s.save(ctx, d, "clearRepo", events.ActionUpdated)
}

// AddFile adds a repository-relative path to links.repo.files (duplicates ignored).
func (s *Service) AddFile(ctx context.Context, identifier, path string) (*model.Document, error) {
	if !s.kind.RepoFiles {
		return nil, newErr(KindBadRequest, "%ss have no links.repo.files", s.kind.Name)
	}
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, newErr(KindBadRequest, "path must not be empty")
	}
	links := s.ensureLinks(d)
	if links.Repo == nil {
		links.Repo = &model.Repo{}
	}
	if contains(links.Repo.Files, path) {
		return d, nil
	}
	links.Repo.Files = append(links.Repo.Files, path)
	return s.save(ctx, d, "addFile", events.ActionUpdated)
}

// RemoveFile removes a path from links.repo.files.
func (s *Service) RemoveFile(ctx context.Context, identifier, path string) (*model.Document, error) {
	if !s.kind.RepoFiles {
		return nil, newErr(KindBadRequest, "%ss have no links.repo.files", s.kind.Name)
	}
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	links := d.FrontMatter.Links
	if links == nil || links.Repo == nil || !contains(links.Repo.Files, path) {
		return nil, newErr(KindNotFound, "%s %s has no file %q", s.kind.Name, d.ID(), path)
	}
	var kept []string
	for _, x := range links.Repo.Files {
		if x != path {
			kept = append(kept, x)
		}
	}
	links.Repo.Files = kept
	if links.Repo.IsEmpty() {
		links.Repo = nil
	}
	s.tidyLinks(d)
	return s.save(ctx, d, "removeFile", events.ActionUpdated)
}

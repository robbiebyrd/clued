package ops

import (
	"context"
	"encoding/json"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/robbiebyrd/clued/plan/service"
)

// Parameter structs. The `jsonschema` tag becomes the property description.

type PlanParams struct {
	Plan string `json:"plan" jsonschema:"Plan identifier: a plan id (0002-a3f) or the path to the plan file"`
}

type CreateParams struct {
	Input    json.RawMessage `json:"input" jsonschema:"Plan input matching the Plan creation JSON Schema: {slug?, frontMatter, body}"`
	Template string          `json:"template,omitempty" jsonschema:"Content template id (default: the default template)"`
}

type ListParams struct {
	Type            string `json:"type,omitempty" jsonschema:"Keep plans of this type"`
	Status          string `json:"status,omitempty" jsonschema:"Keep plans in this status (synonyms accepted)"`
	Priority        string `json:"priority,omitempty" jsonschema:"Keep plans with this priority (number or label)"`
	Plan            string `json:"plan,omitempty" jsonschema:"Keep plans linked to this plan id"`
	Story           string `json:"story,omitempty" jsonschema:"Keep plans linked to this story id"`
	IncludeArchived bool   `json:"includeArchived,omitempty" jsonschema:"Include archived plans (hidden by default unless status filters for them)"`
}

type UpdateParams struct {
	Plan    string `json:"plan" jsonschema:"Plan identifier"`
	Content string `json:"content" jsonschema:"The new Markdown content (below the front matter). Its H1 is forced to the title."`
}

type DeleteParams struct {
	Plan  string `json:"plan" jsonschema:"Plan identifier"`
	Force bool   `json:"force,omitempty" jsonschema:"Delete even when other plans link to this one"`
}

type TemplateParams struct {
	Template string `json:"template,omitempty" jsonschema:"Template id (default: default)"`
}

type TemplateContentParams struct {
	Template string `json:"template" jsonschema:"Template id"`
	Content  string `json:"content" jsonschema:"Template text (a Go text/template over the plan body)"`
}

type SetTitleParams struct {
	Plan       string `json:"plan" jsonschema:"Plan identifier"`
	Title      string `json:"title" jsonschema:"New title (also the content H1)"`
	RenameFile bool   `json:"renameFile,omitempty" jsonschema:"Also rename the file's slug from the new title"`
}

type SetTypeParams struct {
	Plan string `json:"plan" jsonschema:"Plan identifier"`
	Type string `json:"type" jsonschema:"New plan type (drft, dsgn, impl or a configured type)"`
}

type SetStatusParams struct {
	Plan   string `json:"plan" jsonschema:"Plan identifier"`
	Status string `json:"status" jsonschema:"New status (synonyms accepted, e.g. approved → ready)"`
	Force  bool   `json:"force,omitempty" jsonschema:"Skip the workflow check"`
}

type SetPriorityParams struct {
	Plan     string `json:"plan" jsonschema:"Plan identifier"`
	Priority string `json:"priority" jsonschema:"Priority number (0–5) or label (P1, Critical)"`
}

type SetEffortParams struct {
	Plan   string `json:"plan" jsonschema:"Plan identifier"`
	Effort string `json:"effort" jsonschema:"Effort size (XS–XL), label (Medium) or points (5)"`
}

type PatchParams struct {
	Plan  string         `json:"plan" jsonschema:"Plan identifier"`
	Patch map[string]any `json:"patch" jsonschema:"Fields to set: title, type, status, priority, effort (null clears), links (merged per kind: repo, specs, web, stories, plans; a kind set to null is cleared, kinds left out are kept), plans (shorthand for links.plans), progress. id/created/updated/completed are rejected."`
	Force bool           `json:"force,omitempty" jsonschema:"Skip the workflow check for status"`
}

type PlanLinkParams struct {
	Plan     string `json:"plan" jsonschema:"Plan identifier"`
	Target   string `json:"target" jsonschema:"Target plan id"`
	Relation string `json:"relation,omitempty" jsonschema:"parent, included, depends or blocks (optional on remove: every relation)"`
}

type StoryLinkParams struct {
	Plan     string `json:"plan" jsonschema:"Plan identifier"`
	Story    string `json:"story" jsonschema:"Story id (0001-abc)"`
	Relation string `json:"relation,omitempty" jsonschema:"included, depends or blocks (optional on remove: every relation)"`
}

type SpecParams struct {
	Plan string `json:"plan" jsonschema:"Plan identifier"`
	Spec string `json:"spec" jsonschema:"Spec path, e.g. ./docs/design/overview.md"`
}

type WebLinkParams struct {
	Plan  string `json:"plan" jsonschema:"Plan identifier"`
	Label string `json:"label" jsonschema:"Link label, e.g. jira"`
	URL   string `json:"url,omitempty" jsonschema:"http(s) URL (required on set)"`
}

type RepoParams struct {
	Plan   string `json:"plan" jsonschema:"Plan identifier"`
	Remote string `json:"remote,omitempty" jsonschema:"Git remote URL"`
	Local  string `json:"local,omitempty" jsonschema:"Local checkout folder"`
}

type ProgressParams struct {
	Plan    string `json:"plan" jsonschema:"Plan identifier"`
	Section string `json:"section" jsonschema:"Phase or section number, e.g. \"2\" or \"1.1\""`
	Status  string `json:"status,omitempty" jsonschema:"Status for the section (synonyms accepted)"`
	Story   string `json:"story,omitempty" jsonschema:"Story id to link/unlink"`
}

type SyncParams struct {
	From       string `json:"from" jsonschema:"Source store name"`
	To         string `json:"to" jsonschema:"Target store name"`
	OnConflict string `json:"onConflict,omitempty" jsonschema:"error (default), skip or overwrite"`
}

type Empty struct{}

// Default returns a registry with every plan operation registered.
func Default() *Registry {
	r := NewRegistry()

	// Plans
	createOp := Register(r, &Op{Name: "create", Group: "plans", Description: "Create a new Plan from input matching the creation JSON Schema. Assigns id, sets created/updated, defaults status to pending, fills the template from body and writes <id>-<type>-<slug>.md."},
		func(ctx context.Context, svc *service.Service, p CreateParams) (any, error) {
			return svc.Create(ctx, p.Input, p.Template)
		})
	createOp.schemaFn = createSchema
	Register(r, &Op{Name: "get", Group: "plans", ReadOnly: true, Description: "Retrieve a Plan: its path, front matter and content."},
		func(ctx context.Context, svc *service.Service, p PlanParams) (any, error) {
			return svc.Get(ctx, p.Plan)
		})
	Register(r, &Op{Name: "getFrontMatter", Group: "plans", ReadOnly: true, Description: "Retrieve only a Plan's front matter."},
		func(ctx context.Context, svc *service.Service, p PlanParams) (any, error) {
			return svc.GetFrontMatter(ctx, p.Plan)
		})
	Register(r, &Op{Name: "getContent", Group: "plans", ReadOnly: true, Description: "Retrieve only a Plan's Markdown content."},
		func(ctx context.Context, svc *service.Service, p PlanParams) (any, error) {
			return svc.GetContent(ctx, p.Plan)
		})
	Register(r, &Op{Name: "list", Group: "plans", ReadOnly: true, Description: "List Plans (path and front matter), optionally filtered by type, status, priority, or a linked plan or story."},
		func(ctx context.Context, svc *service.Service, p ListParams) (any, error) {
			return svc.List(ctx, service.ListFilter{Type: p.Type, Status: p.Status, Priority: p.Priority, Plan: p.Plan, Story: p.Story, IncludeArchived: p.IncludeArchived})
		})
	Register(r, &Op{Name: "update", Group: "plans", Description: "Replace a Plan's content. Front matter is unchanged apart from updated."},
		func(ctx context.Context, svc *service.Service, p UpdateParams) (any, error) {
			return svc.Update(ctx, p.Plan, p.Content)
		})
	Register(r, &Op{Name: "delete", Group: "plans", Description: "Delete a Plan file. Fails if other Plans link to it unless forced. Prefer setStatus archived to retire a plan."},
		func(ctx context.Context, svc *service.Service, p DeleteParams) (any, error) {
			return svc.Delete(ctx, p.Plan, p.Force)
		})
	Register(r, &Op{Name: "validate", Group: "plans", ReadOnly: true, Description: "Check a Plan without changing it: schema, filename vs id/type, progress keys vs numbered headings. Returns the problems."},
		func(ctx context.Context, svc *service.Service, p PlanParams) (any, error) {
			return svc.Validate(ctx, p.Plan)
		})

	// Templates
	Register(r, &Op{Name: "getTemplate", Group: "templates", ReadOnly: true, Description: "Retrieve a Plan content template (the Markdown layout an agent must follow). Default template when no id is given."},
		func(ctx context.Context, svc *service.Service, p TemplateParams) (any, error) {
			return svc.GetTemplate(ctx, p.Template)
		})
	Register(r, &Op{Name: "listTemplates", Group: "templates", ReadOnly: true, Description: "List the available Plan content templates."},
		func(ctx context.Context, svc *service.Service, _ Empty) (any, error) { return svc.ListTemplates(ctx) })
	Register(r, &Op{Name: "createTemplate", Group: "templates", Description: "Create a new Plan content template with the given id."},
		func(ctx context.Context, svc *service.Service, p TemplateContentParams) (any, error) {
			return svc.CreateTemplate(ctx, p.Template, p.Content)
		})
	Register(r, &Op{Name: "updateTemplate", Group: "templates", Description: "Update a Plan content template (updating default overrides the built-in)."},
		func(ctx context.Context, svc *service.Service, p TemplateContentParams) (any, error) {
			return svc.UpdateTemplate(ctx, p.Template, p.Content)
		})
	Register(r, &Op{Name: "deleteTemplate", Group: "templates", Description: "Delete a Plan content template."},
		func(ctx context.Context, svc *service.Service, p TemplateParams) (any, error) {
			return map[string]any{"template": p.Template, "deleted": true}, svc.DeleteTemplate(ctx, p.Template)
		})

	// Front matter: fields
	Register(r, &Op{Name: "setTitle", Group: "fields", Description: "Change title and the content H1. The filename keeps its slug unless renameFile is set."},
		func(ctx context.Context, svc *service.Service, p SetTitleParams) (any, error) {
			return svc.SetTitle(ctx, p.Plan, p.Title, p.RenameFile)
		})
	Register(r, &Op{Name: "setType", Group: "fields", Description: "Change the plan type and rename the file's type part. id is unchanged."},
		func(ctx context.Context, svc *service.Service, p SetTypeParams) (any, error) {
			return svc.SetType(ctx, p.Plan, p.Type)
		})
	Register(r, &Op{Name: "setStatus", Group: "fields", Description: "Move the Plan to a new status. Rejects moves the workflow doesn't allow unless forced. complete sets completed; leaving complete clears it; archived moves the file to archive/."},
		func(ctx context.Context, svc *service.Service, p SetStatusParams) (any, error) {
			return svc.SetStatus(ctx, p.Plan, p.Status, p.Force)
		})
	Register(r, &Op{Name: "getTransitions", Group: "fields", ReadOnly: true, Description: "List the statuses the Plan can move to from its current status."},
		func(ctx context.Context, svc *service.Service, p PlanParams) (any, error) {
			return svc.GetTransitions(ctx, p.Plan)
		})
	Register(r, &Op{Name: "setPriority", Group: "fields", Description: "Set priority from a number (0–5) or label (P1, Critical)."},
		func(ctx context.Context, svc *service.Service, p SetPriorityParams) (any, error) {
			return svc.SetPriority(ctx, p.Plan, p.Priority)
		})
	Register(r, &Op{Name: "setEffort", Group: "fields", Description: "Set effort from a size (XS–XL), label (Medium) or points (5)."},
		func(ctx context.Context, svc *service.Service, p SetEffortParams) (any, error) {
			return svc.SetEffort(ctx, p.Plan, p.Effort)
		})
	Register(r, &Op{Name: "clearEffort", Group: "fields", Description: "Remove effort."},
		func(ctx context.Context, svc *service.Service, p PlanParams) (any, error) {
			return svc.ClearEffort(ctx, p.Plan)
		})
	Register(r, &Op{Name: "patchFrontMatter", Group: "fields", Description: "Set several front matter fields in one write, following the same rules as the individual setters. Changes to id, created, updated or completed are rejected."},
		func(ctx context.Context, svc *service.Service, p PatchParams) (any, error) {
			return svc.PatchFrontMatter(ctx, p.Plan, p.Patch, p.Force)
		})

	// Front matter: links
	Register(r, &Op{Name: "addPlanLink", Group: "links", Description: "Add [targetPlanId, relation] to links.plans. Relation: parent, included, depends or blocks. The target must exist; duplicates are ignored."},
		func(ctx context.Context, svc *service.Service, p PlanLinkParams) (any, error) {
			return svc.AddPlanLink(ctx, p.Plan, p.Target, p.Relation)
		})
	Register(r, &Op{Name: "removePlanLink", Group: "links", Description: "Remove a plan link. Without a relation, removes every link to that plan."},
		func(ctx context.Context, svc *service.Service, p PlanLinkParams) (any, error) {
			return svc.RemovePlanLink(ctx, p.Plan, p.Target, p.Relation)
		})
	Register(r, &Op{Name: "addStoryLink", Group: "links", Description: "Add [storyId, relation] to links.stories (included, depends, blocks)."},
		func(ctx context.Context, svc *service.Service, p StoryLinkParams) (any, error) {
			return svc.AddStoryLink(ctx, p.Plan, p.Story, p.Relation)
		})
	Register(r, &Op{Name: "removeStoryLink", Group: "links", Description: "Remove a story link. Without a relation, removes every link to that story."},
		func(ctx context.Context, svc *service.Service, p StoryLinkParams) (any, error) {
			return svc.RemoveStoryLink(ctx, p.Plan, p.Story, p.Relation)
		})
	Register(r, &Op{Name: "addSpec", Group: "links", Description: "Add a spec path to links.specs."},
		func(ctx context.Context, svc *service.Service, p SpecParams) (any, error) {
			return svc.AddSpec(ctx, p.Plan, p.Spec)
		})
	Register(r, &Op{Name: "removeSpec", Group: "links", Description: "Remove a spec from links.specs."},
		func(ctx context.Context, svc *service.Service, p SpecParams) (any, error) {
			return svc.RemoveSpec(ctx, p.Plan, p.Spec)
		})
	Register(r, &Op{Name: "setWebLink", Group: "links", Description: "Add a named link to links.web, replacing any existing link with that label."},
		func(ctx context.Context, svc *service.Service, p WebLinkParams) (any, error) {
			return svc.SetWebLink(ctx, p.Plan, p.Label, p.URL)
		})
	Register(r, &Op{Name: "removeWebLink", Group: "links", Description: "Remove a named web link."},
		func(ctx context.Context, svc *service.Service, p WebLinkParams) (any, error) {
			return svc.RemoveWebLink(ctx, p.Plan, p.Label)
		})
	Register(r, &Op{Name: "setRepo", Group: "links", Description: "Set links.repo.remote, links.repo.local, or both."},
		func(ctx context.Context, svc *service.Service, p RepoParams) (any, error) {
			return svc.SetRepo(ctx, p.Plan, p.Remote, p.Local)
		})
	Register(r, &Op{Name: "clearRepo", Group: "links", Description: "Remove links.repo."},
		func(ctx context.Context, svc *service.Service, p PlanParams) (any, error) {
			return svc.ClearRepo(ctx, p.Plan)
		})

	// Front matter: progress
	Register(r, &Op{Name: "getProgress", Group: "progress", ReadOnly: true, Description: "Retrieve the status of every numbered phase and section."},
		func(ctx context.Context, svc *service.Service, p PlanParams) (any, error) {
			return svc.GetProgress(ctx, p.Plan)
		})
	Register(r, &Op{Name: "setProgress", Group: "progress", Description: "Set the status of a phase or section (\"2\", \"1.1\"). The section must exist as a numbered heading in the content."},
		func(ctx context.Context, svc *service.Service, p ProgressParams) (any, error) {
			return svc.SetProgress(ctx, p.Plan, p.Section, p.Status)
		})
	Register(r, &Op{Name: "addProgressStory", Group: "progress", Description: "Link a story to a phase or section."},
		func(ctx context.Context, svc *service.Service, p ProgressParams) (any, error) {
			return svc.AddProgressStory(ctx, p.Plan, p.Section, p.Story)
		})
	Register(r, &Op{Name: "removeProgressStory", Group: "progress", Description: "Unlink a story from a phase or section."},
		func(ctx context.Context, svc *service.Service, p ProgressParams) (any, error) {
			return svc.RemoveProgressStory(ctx, p.Plan, p.Section, p.Story)
		})
	Register(r, &Op{Name: "removeProgress", Group: "progress", Description: "Remove a phase or section's progress entry."},
		func(ctx context.Context, svc *service.Service, p ProgressParams) (any, error) {
			return svc.RemoveProgress(ctx, p.Plan, p.Section)
		})

	// Storage and introspection
	Register(r, &Op{Name: "sync", Group: "storage", Description: "Copy every Plan and template from one configured store to another. onConflict: error (stop), skip (report) or overwrite (source wins)."},
		func(ctx context.Context, svc *service.Service, p SyncParams) (any, error) {
			rep, err := svc.Sync(ctx, p.From, p.To, p.OnConflict)
			if err != nil {
				return rep, err
			}
			return rep, nil
		})
	Register(r, &Op{Name: "getConfig", Group: "storage", ReadOnly: true, Description: "Return the effective configuration: plan types, statuses and synonyms, workflow, priorities, efforts and the stores writes fan out to."},
		func(ctx context.Context, svc *service.Service, _ Empty) (any, error) {
			return map[string]any{"config": svc.Config(), "stores": svc.StoreNames()}, nil
		})
	Register(r, &Op{Name: "getSchema", Group: "storage", ReadOnly: true, Description: "Return the Plan creation JSON Schema with the configured enumerations applied."},
		func(ctx context.Context, svc *service.Service, _ Empty) (any, error) {
			return json.RawMessage(svc.Schema()), nil
		})
	return r
}

// createSchema builds the create parameters schema with the full plan schema inlined.
func createSchema(svc *service.Service) *jsonschema.Schema {
	var planSchema jsonschema.Schema
	if svc != nil {
		_ = json.Unmarshal(svc.Schema(), &planSchema)
	}
	planSchema.Schema = "" // nested schemas must not redeclare $schema
	planSchema.ID = ""
	return &jsonschema.Schema{
		Type:     "object",
		Required: []string{"input"},
		Properties: map[string]*jsonschema.Schema{
			"input":    &planSchema,
			"template": {Type: "string", Description: "Content template id (default: the default template)"},
		},
		AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	}
}

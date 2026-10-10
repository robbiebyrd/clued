package ops

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/service"
)

// Parameter structs. The `jsonschema` tag becomes the property description.
// The `document` field is exposed under the kind's name ("plan", "story");
// `section` is exposed as "step" for stories.

type DocParams struct {
	Document string `json:"document" jsonschema:"Document identifier: an id (0002-a3f) or the path to the file"`
}

type CreateParams struct {
	Input    json.RawMessage `json:"input" jsonschema:"Input matching the creation JSON Schema: {slug?, frontMatter, body}"`
	Template string          `json:"template,omitempty" jsonschema:"Content template id (default: the default template)"`
}

type ListParams struct {
	Type            string `json:"type,omitempty" jsonschema:"Keep documents of this type"`
	Status          string `json:"status,omitempty" jsonschema:"Keep documents in this status (synonyms accepted)"`
	Priority        string `json:"priority,omitempty" jsonschema:"Keep documents with this priority (number or label)"`
	Plan            string `json:"plan,omitempty" jsonschema:"Keep documents linked to this plan id"`
	Story           string `json:"story,omitempty" jsonschema:"Keep documents linked to this story id"`
	IncludeArchived bool   `json:"includeArchived,omitempty" jsonschema:"Include archived documents (hidden by default unless status filters for them)"`
}

type UpdateParams struct {
	Document string `json:"document" jsonschema:"Document identifier"`
	Content  string `json:"content" jsonschema:"The new Markdown content (below the front matter). Its H1 is forced to the title."`
}

type DeleteParams struct {
	Document string `json:"document" jsonschema:"Document identifier"`
	Force    bool   `json:"force,omitempty" jsonschema:"Delete even when other documents link to this one"`
}

type TemplateParams struct {
	Template string `json:"template,omitempty" jsonschema:"Template id (default: default)"`
}

type TemplateContentParams struct {
	Template string `json:"template" jsonschema:"Template id"`
	Content  string `json:"content" jsonschema:"Template text (a Go text/template over the body)"`
}

type SetTitleParams struct {
	Document   string `json:"document" jsonschema:"Document identifier"`
	Title      string `json:"title" jsonschema:"New title (also the content H1)"`
	RenameFile bool   `json:"renameFile,omitempty" jsonschema:"Also rename the file's slug from the new title"`
}

type SetPurposeParams struct {
	Document string `json:"document" jsonschema:"Document identifier"`
	Purpose  string `json:"purpose" jsonschema:"One sentence: why this document exists"`
}

type SetTypeParams struct {
	Document string `json:"document" jsonschema:"Document identifier"`
	Type     string `json:"type" jsonschema:"New type (a configured type name, label or synonym)"`
}

type SetStatusParams struct {
	Document string `json:"document" jsonschema:"Document identifier"`
	Status   string `json:"status" jsonschema:"New status (synonyms accepted, e.g. approved → ready)"`
	Force    bool   `json:"force,omitempty" jsonschema:"Skip the workflow check"`
}

type SetPriorityParams struct {
	Document string `json:"document" jsonschema:"Document identifier"`
	Priority string `json:"priority" jsonschema:"Priority number (0–5) or label (P1, Critical)"`
}

type SetEffortParams struct {
	Document string `json:"document" jsonschema:"Document identifier"`
	Effort   string `json:"effort" jsonschema:"Effort size (XS–XL), label (Medium) or points (5)"`
}

type PatchParams struct {
	Document string         `json:"document" jsonschema:"Document identifier"`
	Patch    map[string]any `json:"patch" jsonschema:"Fields to set: title, purpose (stories), type, status, priority, effort (null clears), links (merged per kind: repo, specs, web, stories, plans; a kind set to null is cleared, kinds left out are kept), plans (shorthand for links.plans), progress. Managed fields (id, created, updated, started, completed) are rejected."`
	Force    bool           `json:"force,omitempty" jsonschema:"Skip the workflow check for status"`
}

type PlanLinkParams struct {
	Document string `json:"document" jsonschema:"Document identifier"`
	Target   string `json:"target" jsonschema:"Target plan id"`
	Relation string `json:"relation,omitempty" jsonschema:"Relation (optional on remove: every relation)"`
}

type StoryPlanLinkParams struct {
	Document string   `json:"document" jsonschema:"Story identifier"`
	Target   string   `json:"target" jsonschema:"Target plan id"`
	Relation string   `json:"relation,omitempty" jsonschema:"included, depends or blocks (optional on remove: every relation)"`
	Sections []string `json:"sections,omitempty" jsonschema:"Plan section numbers this story implements (each must be in the plan's progress map)"`
}

type PlanSectionsParams struct {
	Document string   `json:"document" jsonschema:"Story identifier"`
	Target   string   `json:"target" jsonschema:"Linked plan id"`
	Sections []string `json:"sections" jsonschema:"Plan section numbers (empty removes the list)"`
}

type StoryLinkParams struct {
	Document string `json:"document" jsonschema:"Document identifier"`
	Target   string `json:"target" jsonschema:"Target story id (0001-abc)"`
	Relation string `json:"relation,omitempty" jsonschema:"Relation (optional on remove: every relation)"`
}

type SpecParams struct {
	Document string `json:"document" jsonschema:"Document identifier"`
	Spec     string `json:"spec" jsonschema:"Spec path or URL, e.g. ./docs/design/overview.md"`
}

type WebLinkParams struct {
	Document string `json:"document" jsonschema:"Document identifier"`
	Label    string `json:"label" jsonschema:"Link label, e.g. jira"`
	URL      string `json:"url,omitempty" jsonschema:"http(s) URL (required on set)"`
}

type RepoParams struct {
	Document string `json:"document" jsonschema:"Document identifier"`
	Remote   string `json:"remote,omitempty" jsonschema:"Git remote URL"`
	Local    string `json:"local,omitempty" jsonschema:"Local checkout folder"`
}

type StoryRepoParams struct {
	Document    string `json:"document" jsonschema:"Story identifier"`
	Remote      string `json:"remote,omitempty" jsonschema:"Git remote URL"`
	Local       string `json:"local,omitempty" jsonschema:"Local checkout folder"`
	PullRequest string `json:"pullRequest,omitempty" jsonschema:"URL of the pull request that delivers the story"`
}

type FileParams struct {
	Document string `json:"document" jsonschema:"Story identifier"`
	Path     string `json:"path" jsonschema:"Repository-relative file path"`
}

type ProgressParams struct {
	Document string `json:"document" jsonschema:"Document identifier"`
	Section  string `json:"section" jsonschema:"Numbered heading, e.g. \"2\" or \"1.1\""`
	Status   string `json:"status,omitempty" jsonschema:"Status for the heading (synonyms accepted)"`
}

type ProgressStoryParams struct {
	Document string `json:"document" jsonschema:"Plan identifier"`
	Section  string `json:"section" jsonschema:"Phase or section number, e.g. \"2\" or \"1.1\""`
	Story    string `json:"story" jsonschema:"Story id to link/unlink"`
}

type CriterionParams struct {
	Document string `json:"document" jsonschema:"Story identifier"`
	Position int    `json:"position" jsonschema:"1-based position under ## Acceptance Criteria"`
	State    string `json:"state,omitempty" jsonschema:"open, done or not_applicable"`
}

type AddCriterionParams struct {
	Document string `json:"document" jsonschema:"Story identifier"`
	Text     string `json:"text" jsonschema:"An observable outcome"`
	Kind     string `json:"kind,omitempty" jsonschema:"verify (a command proves it) or manual (a person checks it)"`
}

type WorkLogParams struct {
	Document string `json:"document" jsonschema:"Story identifier"`
	Entry    string `json:"entry" jsonschema:"One line to append; the service supplies the timestamp"`
}

type SyncParams struct {
	From       string `json:"from" jsonschema:"Source store name"`
	To         string `json:"to" jsonschema:"Target store name"`
	OnConflict string `json:"onConflict,omitempty" jsonschema:"error (default), skip or overwrite"`
}

type Empty struct{}

// For builds the registry of a kind: the operations every kind shares plus
// the ones its flags enable.
func For(k *kind.Kind) *Registry {
	r := NewRegistry(k)
	T := k.Title // "Plan" / "Story"
	name := k.Name
	noun := k.ProgressKeyNoun

	// Documents
	createOp := Register(r, &Op{Name: "create", Group: "documents", Description: fmt.Sprintf("Create a new %s from input matching the creation JSON Schema. Assigns id, sets created/updated, defaults status to pending, fills the template from body and writes <id>-<type>-<slug>.md.", T)},
		func(ctx context.Context, svc *service.Service, p CreateParams) (any, error) {
			return svc.Create(ctx, p.Input, p.Template)
		})
	createOp.schemaFn = createSchema
	Register(r, &Op{Name: "get", Group: "documents", ReadOnly: true, Description: fmt.Sprintf("Retrieve a %s: its path, front matter and content.", T)},
		func(ctx context.Context, svc *service.Service, p DocParams) (any, error) {
			return svc.Get(ctx, p.Document)
		})
	Register(r, &Op{Name: "getFrontMatter", Group: "documents", ReadOnly: true, Description: fmt.Sprintf("Retrieve only a %s's front matter.", T)},
		func(ctx context.Context, svc *service.Service, p DocParams) (any, error) {
			return svc.GetFrontMatter(ctx, p.Document)
		})
	Register(r, &Op{Name: "getContent", Group: "documents", ReadOnly: true, Description: fmt.Sprintf("Retrieve only a %s's Markdown content.", T)},
		func(ctx context.Context, svc *service.Service, p DocParams) (any, error) {
			return svc.GetContent(ctx, p.Document)
		})
	Register(r, &Op{Name: "list", Group: "documents", ReadOnly: true, Description: fmt.Sprintf("List %ss (path and front matter), optionally filtered by type, status, priority, or a linked plan or story.", T)},
		func(ctx context.Context, svc *service.Service, p ListParams) (any, error) {
			return svc.List(ctx, service.ListFilter{Type: p.Type, Status: p.Status, Priority: p.Priority, Plan: p.Plan, Story: p.Story, IncludeArchived: p.IncludeArchived})
		})
	updateDesc := fmt.Sprintf("Replace a %s's content. Front matter is unchanged apart from updated.", T)
	if k.StrictProgressOnUpdate {
		updateDesc += fmt.Sprintf(" Progress keys that no longer match a numbered %s heading are rejected.", noun)
	}
	Register(r, &Op{Name: "update", Group: "documents", Description: updateDesc},
		func(ctx context.Context, svc *service.Service, p UpdateParams) (any, error) {
			return svc.Update(ctx, p.Document, p.Content)
		})
	Register(r, &Op{Name: "delete", Group: "documents", Destructive: true, Description: fmt.Sprintf("Delete a %s file. Fails if other plans or stories link to it unless forced. Prefer setStatus archived to retire a %s.", T, name)},
		func(ctx context.Context, svc *service.Service, p DeleteParams) (any, error) {
			return svc.Delete(ctx, p.Document, p.Force)
		})
	validateDesc := fmt.Sprintf("Check a %s without changing it: schema, filename vs id/type, progress keys vs numbered headings", T)
	if k.CriteriaGate {
		validateDesc += ", and that the Acceptance Criteria section parses as a checklist"
	}
	Register(r, &Op{Name: "validate", Group: "documents", ReadOnly: true, Description: validateDesc + ". Returns the problems."},
		func(ctx context.Context, svc *service.Service, p DocParams) (any, error) {
			return svc.Validate(ctx, p.Document)
		})

	// Templates
	Register(r, &Op{Name: "getTemplate", Group: "templates", ReadOnly: true, Description: fmt.Sprintf("Retrieve a %s content template (the Markdown layout an agent must follow). Default template when no id is given.", T)},
		func(ctx context.Context, svc *service.Service, p TemplateParams) (any, error) {
			return svc.GetTemplate(ctx, p.Template)
		})
	Register(r, &Op{Name: "listTemplates", Group: "templates", ReadOnly: true, Description: fmt.Sprintf("List the available %s content templates.", T)},
		func(ctx context.Context, svc *service.Service, _ Empty) (any, error) { return svc.ListTemplates(ctx) })
	Register(r, &Op{Name: "createTemplate", Group: "templates", Description: fmt.Sprintf("Create a new %s content template with the given id.", T)},
		func(ctx context.Context, svc *service.Service, p TemplateContentParams) (any, error) {
			return svc.CreateTemplate(ctx, p.Template, p.Content)
		})
	Register(r, &Op{Name: "updateTemplate", Group: "templates", Description: fmt.Sprintf("Update a %s content template (updating default overrides the built-in).", T)},
		func(ctx context.Context, svc *service.Service, p TemplateContentParams) (any, error) {
			return svc.UpdateTemplate(ctx, p.Template, p.Content)
		})
	deleteTmplDesc := fmt.Sprintf("Delete a %s content template.", T)
	if k.ProtectDefaultTemplate {
		deleteTmplDesc += " The default template cannot be deleted."
	}
	Register(r, &Op{Name: "deleteTemplate", Group: "templates", Destructive: true, Description: deleteTmplDesc},
		func(ctx context.Context, svc *service.Service, p TemplateParams) (any, error) {
			return map[string]any{"template": p.Template, "deleted": true}, svc.DeleteTemplate(ctx, p.Template)
		})

	// Front matter: fields
	Register(r, &Op{Name: "setTitle", Group: "fields", Description: "Change title and the content H1. The filename keeps its slug unless renameFile is set."},
		func(ctx context.Context, svc *service.Service, p SetTitleParams) (any, error) {
			return svc.SetTitle(ctx, p.Document, p.Title, p.RenameFile)
		})
	if k.HasPurpose {
		Register(r, &Op{Name: "setPurpose", Group: "fields", Description: "Set or replace purpose."},
			func(ctx context.Context, svc *service.Service, p SetPurposeParams) (any, error) {
				return svc.SetPurpose(ctx, p.Document, p.Purpose)
			})
	}
	Register(r, &Op{Name: "setType", Group: "fields", Description: fmt.Sprintf("Change the %s type (a primary value or synonym) and rename the file's type part. id is unchanged.", name)},
		func(ctx context.Context, svc *service.Service, p SetTypeParams) (any, error) {
			return svc.SetType(ctx, p.Document, p.Type)
		})
	statusDesc := fmt.Sprintf("Move the %s to a new status. Rejects moves the workflow doesn't allow unless forced.", T)
	if k.TracksStarted {
		statusDesc += " Reaching in_progress sets started;"
	}
	statusDesc += " reaching complete sets completed (and overwrites it on a later return); leaving either keeps it."
	if k.CriteriaGate {
		statusDesc += " Moving to complete requires every acceptance criterion to be checked; force does not override this."
	}
	statusDesc += " archived moves the file to archive/; leaving archived moves it back."
	Register(r, &Op{Name: "setStatus", Group: "fields", Description: statusDesc},
		func(ctx context.Context, svc *service.Service, p SetStatusParams) (any, error) {
			return svc.SetStatus(ctx, p.Document, p.Status, p.Force)
		})
	Register(r, &Op{Name: "getTransitions", Group: "fields", ReadOnly: true, Description: fmt.Sprintf("List the statuses the %s can move to from its current status.", T)},
		func(ctx context.Context, svc *service.Service, p DocParams) (any, error) {
			return svc.GetTransitions(ctx, p.Document)
		})
	Register(r, &Op{Name: "setPriority", Group: "fields", Description: "Set priority from a number (0–5) or label (P1, Critical)."},
		func(ctx context.Context, svc *service.Service, p SetPriorityParams) (any, error) {
			return svc.SetPriority(ctx, p.Document, p.Priority)
		})
	Register(r, &Op{Name: "setEffort", Group: "fields", Description: "Set effort from a size (XS–XL), label (Medium) or points (5)."},
		func(ctx context.Context, svc *service.Service, p SetEffortParams) (any, error) {
			return svc.SetEffort(ctx, p.Document, p.Effort)
		})
	Register(r, &Op{Name: "clearEffort", Group: "fields", Description: "Remove effort."},
		func(ctx context.Context, svc *service.Service, p DocParams) (any, error) {
			return svc.ClearEffort(ctx, p.Document)
		})
	Register(r, &Op{Name: "patchFrontMatter", Group: "fields", Description: "Set several front matter fields in one write, following the same rules as the individual setters. Changes to managed fields (" + joinWords(k.Immutable) + ") are rejected."},
		func(ctx context.Context, svc *service.Service, p PatchParams) (any, error) {
			return svc.PatchFrontMatter(ctx, p.Document, p.Patch, p.Force)
		})

	// Front matter: links
	planRel := joinWords(k.PlanRelations)
	if k.PlanLinkSections {
		Register(r, &Op{Name: "addPlanLink", Group: "links", Description: "Add [planId, relation] to links.plans, with an optional list of the plan's section numbers this story implements. Relation: " + planRel + ". The plan must exist and each section must be in its progress map; duplicates are ignored."},
			func(ctx context.Context, svc *service.Service, p StoryPlanLinkParams) (any, error) {
				return svc.AddPlanLink(ctx, p.Document, p.Target, p.Relation, p.Sections)
			})
		Register(r, &Op{Name: "setPlanSections", Group: "links", Description: "Replace the section list on an existing plan link. An empty list removes the third element."},
			func(ctx context.Context, svc *service.Service, p PlanSectionsParams) (any, error) {
				return svc.SetPlanSections(ctx, p.Document, p.Target, p.Sections)
			})
	} else {
		Register(r, &Op{Name: "addPlanLink", Group: "links", Description: "Add [targetPlanId, relation] to links.plans. Relation: " + planRel + ". The target must exist; duplicates are ignored."},
			func(ctx context.Context, svc *service.Service, p PlanLinkParams) (any, error) {
				return svc.AddPlanLink(ctx, p.Document, p.Target, p.Relation, nil)
			})
	}
	Register(r, &Op{Name: "removePlanLink", Group: "links", Description: "Remove a plan link. Without a relation, removes every link to that plan."},
		func(ctx context.Context, svc *service.Service, p PlanLinkParams) (any, error) {
			return svc.RemovePlanLink(ctx, p.Document, p.Target, p.Relation)
		})
	storyDesc := "Add [storyId, relation] to links.stories. Relation: " + joinWords(k.StoryRelations) + "."
	if k.Name == kind.Story {
		storyDesc += " The target story must exist; duplicates are ignored."
	}
	Register(r, &Op{Name: "addStoryLink", Group: "links", Description: storyDesc},
		func(ctx context.Context, svc *service.Service, p StoryLinkParams) (any, error) {
			return svc.AddStoryLink(ctx, p.Document, p.Target, p.Relation)
		})
	Register(r, &Op{Name: "removeStoryLink", Group: "links", Description: "Remove a story link. Without a relation, removes every link to that story."},
		func(ctx context.Context, svc *service.Service, p StoryLinkParams) (any, error) {
			return svc.RemoveStoryLink(ctx, p.Document, p.Target, p.Relation)
		})
	Register(r, &Op{Name: "addSpec", Group: "links", Description: "Add a spec path or URL to links.specs."},
		func(ctx context.Context, svc *service.Service, p SpecParams) (any, error) {
			return svc.AddSpec(ctx, p.Document, p.Spec)
		})
	Register(r, &Op{Name: "removeSpec", Group: "links", Description: "Remove an entry from links.specs."},
		func(ctx context.Context, svc *service.Service, p SpecParams) (any, error) {
			return svc.RemoveSpec(ctx, p.Document, p.Spec)
		})
	Register(r, &Op{Name: "setWebLink", Group: "links", Description: "Add a named link to links.web, replacing any existing link with that label."},
		func(ctx context.Context, svc *service.Service, p WebLinkParams) (any, error) {
			return svc.SetWebLink(ctx, p.Document, p.Label, p.URL)
		})
	Register(r, &Op{Name: "removeWebLink", Group: "links", Description: "Remove a named web link."},
		func(ctx context.Context, svc *service.Service, p WebLinkParams) (any, error) {
			return svc.RemoveWebLink(ctx, p.Document, p.Label)
		})
	if k.RepoFiles {
		Register(r, &Op{Name: "setRepo", Group: "links", Description: "Set links.repo.remote, links.repo.local, links.repo.pull_request, or any combination."},
			func(ctx context.Context, svc *service.Service, p StoryRepoParams) (any, error) {
				return svc.SetRepo(ctx, p.Document, p.Remote, p.Local, p.PullRequest)
			})
		Register(r, &Op{Name: "clearRepo", Group: "links", Description: "Remove links.repo, including files."},
			func(ctx context.Context, svc *service.Service, p DocParams) (any, error) {
				return svc.ClearRepo(ctx, p.Document)
			})
		Register(r, &Op{Name: "addFile", Group: "links", Description: "Add a repository-relative path to links.repo.files; duplicates are ignored."},
			func(ctx context.Context, svc *service.Service, p FileParams) (any, error) {
				return svc.AddFile(ctx, p.Document, p.Path)
			})
		Register(r, &Op{Name: "removeFile", Group: "links", Description: "Remove a path from links.repo.files."},
			func(ctx context.Context, svc *service.Service, p FileParams) (any, error) {
				return svc.RemoveFile(ctx, p.Document, p.Path)
			})
	} else {
		Register(r, &Op{Name: "setRepo", Group: "links", Description: "Set links.repo.remote, links.repo.local, or both."},
			func(ctx context.Context, svc *service.Service, p RepoParams) (any, error) {
				return svc.SetRepo(ctx, p.Document, p.Remote, p.Local, "")
			})
		Register(r, &Op{Name: "clearRepo", Group: "links", Description: "Remove links.repo."},
			func(ctx context.Context, svc *service.Service, p DocParams) (any, error) {
				return svc.ClearRepo(ctx, p.Document)
			})
	}

	// Front matter: progress
	Register(r, &Op{Name: "getProgress", Group: "progress", ReadOnly: true, Description: fmt.Sprintf("Retrieve the status of every numbered %s.", noun)},
		func(ctx context.Context, svc *service.Service, p DocParams) (any, error) {
			return svc.GetProgress(ctx, p.Document)
		})
	progressDesc := fmt.Sprintf("Set the status of a %s (\"2\", \"1.1\"). The %s must exist as a numbered heading in the content.", noun, noun)
	if k.AutoComplete {
		progressDesc += fmt.Sprintf(" Completing the last remaining %s while the %s is in_progress with every acceptance criterion checked moves it to complete.", noun, name)
	}
	Register(r, &Op{Name: "setProgress", Group: "progress", Description: progressDesc},
		func(ctx context.Context, svc *service.Service, p ProgressParams) (any, error) {
			return svc.SetProgress(ctx, p.Document, p.Section, p.Status)
		})
	if k.ProgressStories {
		Register(r, &Op{Name: "addProgressStory", Group: "progress", Description: "Link a story to a phase or section."},
			func(ctx context.Context, svc *service.Service, p ProgressStoryParams) (any, error) {
				return svc.AddProgressStory(ctx, p.Document, p.Section, p.Story)
			})
		Register(r, &Op{Name: "removeProgressStory", Group: "progress", Description: "Unlink a story from a phase or section."},
			func(ctx context.Context, svc *service.Service, p ProgressStoryParams) (any, error) {
				return svc.RemoveProgressStory(ctx, p.Document, p.Section, p.Story)
			})
	}
	Register(r, &Op{Name: "removeProgress", Group: "progress", Description: fmt.Sprintf("Remove a %s's progress entry.", noun)},
		func(ctx context.Context, svc *service.Service, p ProgressParams) (any, error) {
			return svc.RemoveProgress(ctx, p.Document, p.Section)
		})

	// Content: acceptance criteria and work log
	if k.CriteriaGate {
		Register(r, &Op{Name: "getCriteria", Group: "criteria", ReadOnly: true, Description: "List every item under ## Acceptance Criteria with its 1-based position, text, kind and state (open, done, not_applicable)."},
			func(ctx context.Context, svc *service.Service, p DocParams) (any, error) {
				return svc.GetCriteria(ctx, p.Document)
			})
		Register(r, &Op{Name: "setCriterion", Group: "criteria", Description: "Set an item's state; rendered as [ ], [x] or [~]."},
			func(ctx context.Context, svc *service.Service, p CriterionParams) (any, error) {
				return svc.SetCriterion(ctx, p.Document, p.Position, p.State)
			})
		Register(r, &Op{Name: "addCriterion", Group: "criteria", Description: "Append an item, optionally marked verify or manual."},
			func(ctx context.Context, svc *service.Service, p AddCriterionParams) (any, error) {
				return svc.AddCriterion(ctx, p.Document, p.Text, p.Kind)
			})
		Register(r, &Op{Name: "removeCriterion", Group: "criteria", Description: "Remove an item."},
			func(ctx context.Context, svc *service.Service, p CriterionParams) (any, error) {
				return svc.RemoveCriterion(ctx, p.Document, p.Position)
			})
	}
	if k.WorkLog {
		Register(r, &Op{Name: "appendWorkLog", Group: "worklog", Description: "Append \"### <now> - <entry>\" under ## Work Log. The service supplies the timestamp; the section is append-only."},
			func(ctx context.Context, svc *service.Service, p WorkLogParams) (any, error) {
				return svc.AppendWorkLog(ctx, p.Document, p.Entry)
			})
	}

	// Storage and introspection
	Register(r, &Op{Name: "sync", Group: "storage", Description: "Copy every plan, story and template from one configured store to another. onConflict: error (stop), skip (report) or overwrite (source wins)."},
		func(ctx context.Context, svc *service.Service, p SyncParams) (any, error) {
			return svc.Palace().Sync(ctx, p.From, p.To, p.OnConflict)
		})
	Register(r, &Op{Name: "getConfig", Group: "storage", ReadOnly: true, Description: "Return the effective configuration: plan and story types, statuses and synonyms, workflow, priorities, efforts and the stores writes fan out to."},
		func(ctx context.Context, svc *service.Service, _ Empty) (any, error) {
			return map[string]any{"config": svc.Config(), "stores": svc.StoreNames()}, nil
		})
	Register(r, &Op{Name: "getSchema", Group: "storage", ReadOnly: true, Description: fmt.Sprintf("Return the %s creation JSON Schema with the configured enumerations applied.", T)},
		func(ctx context.Context, svc *service.Service, _ Empty) (any, error) {
			return json.RawMessage(svc.Schema()), nil
		})
	return r
}

func joinWords(list []string) string {
	s := ""
	for i, w := range list {
		if i > 0 {
			s += ", "
		}
		s += w
	}
	return s
}

// createSchema builds the create parameters schema with the full kind schema inlined.
func createSchema(svc *service.Service) *jsonschema.Schema {
	var docSchema jsonschema.Schema
	if svc != nil {
		_ = json.Unmarshal(svc.Schema(), &docSchema)
	}
	docSchema.Schema = "" // nested schemas must not redeclare $schema
	docSchema.ID = ""
	return &jsonschema.Schema{
		Type:     "object",
		Required: []string{"input"},
		Properties: map[string]*jsonschema.Schema{
			"input":    &docSchema,
			"template": {Type: "string", Description: "Content template id (default: the default template)"},
		},
		AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	}
}

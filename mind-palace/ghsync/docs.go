package ghsync

// This file keeps a project's plans and stories equal to its repository's
// default branch (plan 0011, "Plans and stories from the default branch"):
// the documents at the pushed commit are fetched into a memory store,
// validated with the repository's configured kinds, and synced into the
// project's store with store.ConflictNewer; documents deleted since the
// previous tip are archived through the service. Every outcome is handed to
// a DocsRecorder as a plan_sync, plan_sync_conflict or plan_sync_problem
// event.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/markdown"
	"github.com/robbiebyrd/clued/mind-palace/model"
	"github.com/robbiebyrd/clued/mind-palace/server/projects"
	"github.com/robbiebyrd/clued/mind-palace/service"
	"github.com/robbiebyrd/clued/mind-palace/store"
	"github.com/robbiebyrd/clued/mind-palace/store/memstore"
)

// DocEntry is one entry of a repository directory at a commit.
type DocEntry struct {
	// Name is the entry's base name.
	Name string
	// Dir is true for a directory (a submodule or symlink is neither and
	// reads as a file the sync skips when it does not parse).
	Dir bool
}

// DocFetcher reads a repository's files at a commit: the narrow part of the
// GitHub REST client (Contents) the docs sync needs, so the installation
// client satisfies it. repo is owner/repo; paths are repository-relative
// with "/" separators ("" is the root). A missing directory, file or
// commit is reported with an error that matches fs.ErrNotExist.
type DocFetcher interface {
	// ListDir lists the entries directly in dir at commit.
	ListDir(ctx context.Context, repo, commit, dir string) ([]DocEntry, error)
	// ReadFile returns a file's content at commit.
	ReadFile(ctx context.Context, repo, commit, file string) ([]byte, error)
}

// Activity event kinds of the docs sync.
const (
	// EventPlanSync records a document created, updated or archived on the
	// server from the repository (DocsEvent.Action says which).
	EventPlanSync = "plan_sync"
	// EventPlanSyncConflict records a document skipped because the server
	// copy is newer (an MCP edit not merged yet).
	EventPlanSyncConflict = "plan_sync_conflict"
	// EventPlanSyncProblem records a document (or the config file) that is
	// invalid at the commit and was not written.
	EventPlanSyncProblem = "plan_sync_problem"
)

// Actions of an EventPlanSync event.
const (
	DocsCreated  = "created"
	DocsUpdated  = "updated"
	DocsArchived = "archived"
)

// MaxDocBytes is the largest plan, story or config file the sync reads;
// a larger one is a plan_sync_problem.
const MaxDocBytes = 1 << 20

// DocsEvent is one outcome of a docs sync, in the allow-listed shape the
// activity log stores: names, ids, paths and validation messages, never a
// document's content.
type DocsEvent struct {
	// Kind is EventPlanSync, EventPlanSyncConflict or EventPlanSyncProblem.
	Kind string `json:"kind"`
	// Repo is owner/repo.
	Repo string `json:"repo"`
	// Commit is the commit synced.
	Commit string `json:"commit"`
	// Delivery is the webhook delivery that caused the sync ("" for an
	// import or a reconcile).
	Delivery string `json:"delivery,omitempty"`
	// DocKind is plan or story ("" for a problem with the config file).
	DocKind string `json:"docKind,omitempty"`
	// ID is the document id ("" when the file names none).
	ID string `json:"id,omitempty"`
	// Path is the repository path of the file.
	Path string `json:"path,omitempty"`
	// Action is DocsCreated, DocsUpdated or DocsArchived for EventPlanSync.
	Action string `json:"action,omitempty"`
	// Problems are the validation messages of EventPlanSyncProblem.
	Problems []string `json:"problems,omitempty"`
	// At is when the sync ran.
	At time.Time `json:"at"`
}

// DocsRecorder records a docs sync's outcomes in the target project's
// activity log (code.Store.AppendEvents in the host). It is called once per
// sync with every event, and not at all when nothing happened.
type DocsRecorder func(ctx context.Context, target Target, slug string, events []DocsEvent) error

// DocsRequest asks for one sync of a repository's plans and stories.
type DocsRequest struct {
	// Target is the claimed account or workspace.
	Target Target
	// Repo is owner/repo.
	Repo string
	// Slug is the project slug; "" derives it from Repo
	// (github.com__owner__repo). The repository's remote.project, when
	// set at the commit, wins over both.
	Slug string
	// Commit is the default branch's tip to sync (a push's after).
	Commit string
	// Before is the previous tip (a push's before): documents present
	// there and gone at Commit are archived. Empty or all zeros (a new
	// branch, an import, a reconcile) archives nothing.
	Before string
	// Delivery is the webhook delivery id stamped on the events.
	Delivery string
}

// DocsReport is what a docs sync did.
type DocsReport struct {
	// Slug is the project the documents were written to.
	Slug string `json:"slug"`
	// Sync is store.Sync's report (nil when the config file was invalid
	// and nothing was synced).
	Sync *store.SyncReport `json:"sync,omitempty"`
	// Archived lists "<kind> <id>" for each document archived.
	Archived []string `json:"archived"`
	// Events are the outcomes handed to the recorder.
	Events []DocsEvent `json:"events"`
}

// docsSnapshot is the repository's plans and stories at one commit.
type docsSnapshot struct {
	cfg  *config.Config
	repo RepoConfig
	// configPath is the config file read ("" when the defaults apply).
	configPath string
	// dirs is each kind's repository directory.
	dirs map[string]string
	// files maps kind → id → repository path of every document file whose
	// name parses, valid or not.
	files map[string]map[string]string
}

// SyncDocs syncs the repository's plans and stories at req.Commit into the
// target project's store: invalid documents are skipped (plan_sync_problem),
// a server copy newer than the repository's is kept (plan_sync_conflict),
// every other document is created or replaced (plan_sync), and a document
// deleted since req.Before is archived through the service. An invalid
// config file at the commit syncs nothing and is reported as a problem.
// record may be nil. The error is for what a retry can fix (fetching,
// opening or writing the store, recording); the report says what was done.
func (s *Service) SyncDocs(ctx context.Context, f DocFetcher, record DocsRecorder, req DocsRequest) (*DocsReport, error) {
	if f == nil {
		return nil, errors.New("ghsync: docs sync needs a fetcher")
	}
	if err := req.Target.Validate(); err != nil {
		return nil, err
	}
	if req.Repo == "" || req.Commit == "" {
		return nil, errors.New("ghsync: docs sync needs a repository and a commit")
	}
	at := s.now().UTC()
	rep := &DocsReport{Slug: req.Slug, Archived: []string{}, Events: []DocsEvent{}}
	if rep.Slug == "" {
		rep.Slug = projects.Slug("github.com/" + req.Repo)
	}
	event := func(e DocsEvent) {
		e.Repo, e.Commit, e.Delivery, e.At = req.Repo, req.Commit, req.Delivery, at
		rep.Events = append(rep.Events, e)
	}

	after, problem, err := readDocsConfig(ctx, f, req.Repo, req.Commit)
	if err != nil {
		return rep, err
	}
	if problem != nil {
		event(*problem)
		return rep, s.recordDocs(ctx, record, req.Target, rep)
	}
	if p := projects.Slug(after.repo.Project); p != "" {
		rep.Slug = p
	}
	if !projects.ValidSlug(rep.Slug) {
		return rep, fmt.Errorf("ghsync: invalid project slug %q", rep.Slug)
	}

	mem := memstore.New("github")
	if err := loadDocs(ctx, f, req.Repo, req.Commit, after, mem, event); err != nil {
		return rep, err
	}
	if err := validateDocs(ctx, after, mem, event); err != nil {
		return rep, err
	}

	palace, err := s.Docs(ctx, req.Target, rep.Slug)
	if err != nil {
		return rep, fmt.Errorf("ghsync: open project %s: %w", rep.Slug, err)
	}
	sr, err := store.Sync(ctx, mem, palace.Store(), store.ConflictNewer)
	rep.Sync = sr
	if err != nil {
		return rep, fmt.Errorf("ghsync: sync plans and stories: %w", err)
	}
	for _, k := range kind.Names() {
		ks := sr.Documents[k]
		if ks == nil {
			continue
		}
		for _, id := range ks.Copied {
			event(DocsEvent{Kind: EventPlanSync, DocKind: k, ID: id, Path: after.files[k][id], Action: DocsCreated})
		}
		for _, id := range ks.Overwritten {
			event(DocsEvent{Kind: EventPlanSync, DocKind: k, ID: id, Path: after.files[k][id], Action: DocsUpdated})
		}
		for _, id := range ks.Conflicts {
			event(DocsEvent{Kind: EventPlanSyncConflict, DocKind: k, ID: id, Path: after.files[k][id]})
		}
	}

	if before := req.Before; before != "" && strings.Trim(before, "0") != "" && before != req.Commit {
		if err := s.archiveDeleted(ctx, f, req.Repo, before, after, palace, rep, event); err != nil {
			return rep, err
		}
	}
	return rep, s.recordDocs(ctx, record, req.Target, rep)
}

func (s *Service) recordDocs(ctx context.Context, record DocsRecorder, target Target, rep *DocsReport) error {
	if record == nil || len(rep.Events) == 0 {
		return nil
	}
	if err := record(ctx, target, rep.Slug, rep.Events); err != nil {
		return fmt.Errorf("ghsync: record docs sync events: %w", err)
	}
	return nil
}

// archiveDeleted archives every document whose file was at before and is
// gone at the synced commit (moving a file, archive/ included, is not a
// deletion: the id is still there). A document the server does not hold or
// already holds archived is left alone.
func (s *Service) archiveDeleted(ctx context.Context, f DocFetcher, repo, before string, after *docsSnapshot, palace *service.Palace, rep *DocsReport, event func(DocsEvent)) error {
	prev, problem, err := readDocsConfig(ctx, f, repo, before)
	if errors.Is(err, fs.ErrNotExist) {
		// The previous tip is gone (a force push dropped it): nothing to
		// compare with; the next reconcile sees what the branch holds.
		return nil
	}
	if err != nil {
		return err
	}
	if problem != nil {
		// Without the previous config the previous directories are
		// unknown; archive nothing rather than guess.
		return nil
	}
	if _, err := listDocs(ctx, f, repo, before, prev); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, k := range kind.Names() {
		ids := make([]string, 0, len(prev.files[k]))
		for id := range prev.files[k] {
			if _, ok := after.files[k][id]; !ok {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		svc, err := palace.Service(k)
		if err != nil {
			return err
		}
		for _, id := range ids {
			repoPath := prev.files[k][id]
			cur, err := svc.Resolve(ctx, id)
			if service.IsKind(err, service.KindNotFound) {
				continue
			}
			if err != nil {
				return fmt.Errorf("ghsync: read %s %s: %w", k, id, err)
			}
			if cur.FrontMatter.Status == "archived" {
				continue
			}
			if _, err := svc.SetStatus(ctx, id, "archived", true); err != nil {
				if service.IsKind(err, service.KindStorage) {
					return fmt.Errorf("ghsync: archive %s %s: %w", k, id, err)
				}
				event(DocsEvent{Kind: EventPlanSyncProblem, DocKind: k, ID: id, Path: repoPath, Problems: []string{"archive: " + err.Error()}})
				continue
			}
			rep.Archived = append(rep.Archived, k+" "+id)
			event(DocsEvent{Kind: EventPlanSync, DocKind: k, ID: id, Path: repoPath, Action: DocsArchived})
		}
	}
	return nil
}

// readDocsConfig reads the repository's mind-palace config file at commit
// (the first of config.DefaultConfigFiles in the root) over the defaults.
// A config file that does not parse is returned as a problem event, not an
// error: a retry cannot fix it.
func readDocsConfig(ctx context.Context, f DocFetcher, repo, commit string) (*docsSnapshot, *DocsEvent, error) {
	snap := &docsSnapshot{cfg: config.Default(), dirs: map[string]string{}, files: map[string]map[string]string{}}
	root, err := f.ListDir(ctx, repo, commit, "")
	if err != nil {
		return nil, nil, fmt.Errorf("ghsync: list %s at %s: %w", repo, commit, err)
	}
	files := map[string]bool{}
	for _, e := range root {
		if !e.Dir {
			files[e.Name] = true
		}
	}
	for _, name := range config.DefaultConfigFiles {
		if files[name] {
			snap.configPath = name
			break
		}
	}
	problem := func(msg string) *DocsEvent {
		return &DocsEvent{Kind: EventPlanSyncProblem, Path: snap.configPath, Problems: []string{msg}}
	}
	if snap.configPath != "" {
		data, err := f.ReadFile(ctx, repo, commit, snap.configPath)
		if err != nil {
			return nil, nil, fmt.Errorf("ghsync: read %s at %s: %w", snap.configPath, commit, err)
		}
		if len(data) > MaxDocBytes {
			return nil, problem(fmt.Sprintf("larger than %d bytes", MaxDocBytes)), nil
		}
		rc, err := ParseRepoConfig(data)
		if err != nil {
			return nil, problem(err.Error()), nil
		}
		snap.repo = rc
		if err := overlayDocsConfig(snap.cfg, data, path.Ext(snap.configPath)); err != nil {
			return nil, problem(err.Error()), nil
		}
		if err := snap.cfg.Validate(config.AllowNoFileStore); err != nil {
			return nil, problem(err.Error()), nil
		}
	}
	for _, k := range kind.Names() {
		dir, ok := repoDir(snap.cfg.Kind(k).Dir)
		if !ok {
			return nil, problem(fmt.Sprintf("%s directory %q is not inside the repository", k, snap.cfg.Kind(k).Dir)), nil
		}
		snap.dirs[k] = dir
		snap.files[k] = map[string]string{}
	}
	return snap, nil, nil
}

// syncOnlyKeys are the config keys only the sync reads (ParseRepoConfig);
// the client's loader refuses unknown keys, so they are dropped before the
// overlay.
var syncOnlyKeys = []string{"github", "codeSync"}

// overlayDocsConfig applies a config file's content on cfg the way the
// client's loader does, without the keys only the sync reads.
func overlayDocsConfig(cfg *config.Config, data []byte, ext string) error {
	var m map[string]any
	isJSON := strings.EqualFold(ext, ".json")
	if isJSON {
		if err := json.Unmarshal(data, &m); err != nil {
			return err
		}
	} else if err := yaml.Unmarshal(data, &m); err != nil {
		return err
	}
	if len(m) == 0 {
		return nil
	}
	for _, k := range syncOnlyKeys {
		delete(m, k)
	}
	var out []byte
	var err error
	if isJSON {
		out, err = json.Marshal(m)
	} else {
		out, err = yaml.Marshal(m)
	}
	if err != nil {
		return err
	}
	return cfg.Overlay(out, ext)
}

// repoDir turns a configured kind directory into a repository path: slash
// separated, cleaned, relative, and not escaping the root.
func repoDir(dir string) (string, bool) {
	d := path.Clean(strings.ReplaceAll(strings.TrimSpace(dir), `\`, "/"))
	if d == "." || d == "" || path.IsAbs(d) || d == ".." || strings.HasPrefix(d, "../") {
		return "", false
	}
	return d, true
}

// docsDup is a document file whose id an earlier file already holds.
type docsDup struct {
	kind, id, path, first string
}

// listDocs fills snap.files with every document file name in each kind's
// directory and its archive/ directory at commit. Only the first file of
// an id counts (the directory before archive/, then by name); the others
// are returned.
func listDocs(ctx context.Context, f DocFetcher, repo, commit string, snap *docsSnapshot) ([]docsDup, error) {
	var dups []docsDup
	for _, k := range kind.Names() {
		for _, sub := range []string{"", "archive"} {
			dir := snap.dirs[k]
			if sub != "" {
				dir += "/" + sub
			}
			entries, err := f.ListDir(ctx, repo, commit, dir)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("ghsync: list %s at %s: %w", dir, commit, err)
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
			for _, e := range entries {
				if e.Dir {
					continue
				}
				id, _, _, ok := model.ParseFileName(e.Name)
				if !ok {
					continue
				}
				file := dir + "/" + e.Name
				if first, dup := snap.files[k][id]; dup {
					dups = append(dups, docsDup{kind: k, id: id, path: file, first: first})
					continue
				}
				snap.files[k][id] = file
			}
		}
	}
	return dups, nil
}

// loadDocs reads every document file at commit into mem. A file that
// cannot be parsed, or names an id an earlier file holds, is a problem
// event and is not loaded.
func loadDocs(ctx context.Context, f DocFetcher, repo, commit string, snap *docsSnapshot, mem store.Store, event func(DocsEvent)) error {
	dups, err := listDocs(ctx, f, repo, commit, snap)
	if err != nil {
		return err
	}
	for _, d := range dups {
		event(DocsEvent{Kind: EventPlanSyncProblem, DocKind: d.kind, ID: d.id, Path: d.path, Problems: []string{fmt.Sprintf("duplicate id: %s already holds %s", d.first, d.id)}})
	}
	for _, k := range kind.Names() {
		ids := make([]string, 0, len(snap.files[k]))
		for id := range snap.files[k] {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			file := snap.files[k][id]
			data, err := f.ReadFile(ctx, repo, commit, file)
			if err != nil {
				return fmt.Errorf("ghsync: read %s at %s: %w", file, commit, err)
			}
			d, problems := parseDoc(k, strings.TrimPrefix(file, snap.dirs[k]+"/"), id, data)
			if len(problems) > 0 {
				event(DocsEvent{Kind: EventPlanSyncProblem, DocKind: k, ID: id, Path: file, Problems: problems})
				continue
			}
			if err := mem.Put(ctx, d); err != nil {
				return err
			}
		}
	}
	return nil
}

// parseDoc parses a document file the way the file store reads one, with
// its timestamps, dates and ids normalised as the service writes them.
func parseDoc(k, rel, nameID string, data []byte) (*model.Document, []string) {
	if len(data) > MaxDocBytes {
		return nil, []string{fmt.Sprintf("larger than %d bytes", MaxDocBytes)}
	}
	fm, content, err := markdown.Parse(string(data))
	if err != nil {
		return nil, []string{err.Error()}
	}
	if fm.ID == "" {
		fm.ID = nameID
	}
	fm.NormalizeTimestamps()
	fm.NormalizeDates()
	fm.NormalizeIDs()
	if fm.ID != nameID {
		return nil, []string{fmt.Sprintf("/path: file name id %q does not match id %q", nameID, fm.ID)}
	}
	return &model.Document{Kind: k, Path: rel, FrontMatter: fm, Content: content}, nil
}

// validateDocs validates every loaded document with the repository's
// configured kinds and removes the invalid ones from mem, each as a
// problem event. A link to a document the repository does not hold is not
// a reason to skip one: the server may hold it, and the file is reviewed.
func validateDocs(ctx context.Context, snap *docsSnapshot, mem store.Store, event func(DocsEvent)) error {
	p, err := service.NewPalace(snap.cfg, mem, nil)
	if err != nil {
		event(DocsEvent{Kind: EventPlanSyncProblem, Path: snap.configPath, Problems: []string{err.Error()}})
		for _, k := range kind.Names() {
			docs, err := mem.List(ctx, k)
			if err != nil {
				return err
			}
			for _, d := range docs {
				if err := mem.Delete(ctx, k, d.ID()); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, svc := range p.Services() {
		k := svc.Kind().Name
		docs, err := mem.List(ctx, k)
		if err != nil {
			return err
		}
		for _, d := range docs {
			vr, err := svc.Validate(ctx, d.ID())
			if err != nil {
				return err
			}
			var problems []string
			for _, pr := range vr.Problems {
				if !strings.HasPrefix(pr, "/links/") {
					problems = append(problems, pr)
				}
			}
			if len(problems) == 0 {
				continue
			}
			event(DocsEvent{Kind: EventPlanSyncProblem, DocKind: k, ID: d.ID(), Path: snap.files[k][d.ID()], Problems: problems})
			if err := mem.Delete(ctx, k, d.ID()); err != nil {
				return err
			}
		}
	}
	return nil
}

// DocsTouched reports whether a push that changed paths (repository paths,
// added, modified or removed) needs a docs sync: a change under a plans or
// stories directory, or to a mind-palace config file in the root. dirs are
// the kinds' directories ("" or nil: the defaults docs/plans and
// docs/stories).
func DocsTouched(changed []string, dirs ...string) bool {
	if len(dirs) == 0 {
		dirs = []string{config.DefaultPlansDir, config.DefaultStoriesDir}
	}
	for _, c := range changed {
		c = strings.TrimPrefix(c, "/")
		for _, name := range config.DefaultConfigFiles {
			if c == name {
				return true
			}
		}
		for _, d := range dirs {
			if d, ok := repoDir(d); ok && strings.HasPrefix(c, d+"/") {
				return true
			}
		}
	}
	return false
}

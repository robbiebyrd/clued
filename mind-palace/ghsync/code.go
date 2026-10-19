package ghsync

// This file keeps a repository's GitHub-backed worktree (gh_<repository
// id>) current in the claimed project's code store (plan 0011, "Code sync
// from GitHub"): a push is applied from the comparison of its before and
// after commits, with GitHub's blob ids and every entry clean and pushed; a
// push whose before is not the stored head, a force push, a new branch or a
// truncated comparison falls back to a full import of the branch from its
// recursive tree (walking subtrees when GitHub truncates it). The branch
// policy (policy.go) decides which branches sync; a branch that turns
// inactive keeps its last tree, a deleted one loses it (RemoveRef). Every
// file passes the shared code filter (codefilter) and the built-in and
// account redaction rules before its content is stored, and a repository
// whose mind-palace config says codeSync: false is left out.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/codefilter"
	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/ghapp"
	"github.com/robbiebyrd/clued/mind-palace/server/projects"
	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/redact"
)

// GitHubHost is the Host of every GitHub-backed worktree.
const GitHubHost = "github.com"

// WorktreeID is the id of a repository's GitHub-backed worktree.
func WorktreeID(repository int64) string { return "gh_" + strconv.FormatInt(repository, 10) }

// --- sync state --------------------------------------------------------------

// BranchState is what the sync keeps about one branch.
type BranchState struct {
	// Head is the commit whose tree the code store holds for the branch
	// ("" when none was written).
	Head string `json:"head,omitempty"`
	// LastPush is when the branch last received a push (or was created).
	LastPush time.Time `json:"lastPush,omitzero"`
	// OpenPRs are the numbers of the open pull requests from the branch.
	OpenPRs []int `json:"openPRs,omitempty"`
	// Inactive is true while the policy leaves the branch out: its last
	// tree is kept, but pushes no longer update it.
	Inactive bool `json:"inactive,omitempty"`
}

// RepoState is what the sync keeps about one repository of a target.
type RepoState struct {
	// Repository is GitHub's repository id.
	Repository int64 `json:"repository"`
	// Name is owner/repo as last seen.
	Name string `json:"name"`
	// DefaultBranch is the repository's default branch as last seen.
	DefaultBranch string `json:"defaultBranch"`
	// Slug is the project the repository syncs into: remote.project from
	// the default branch's config, else github.com__owner__repo.
	Slug string `json:"slug"`
	// Config is the default branch's mind-palace config (the sync's keys),
	// read at ConfigCommit; ConfigInvalid is true when it did not parse,
	// which keeps code out until it does.
	Config        RepoConfig `json:"config"`
	ConfigCommit  string     `json:"configCommit,omitempty"`
	ConfigInvalid bool       `json:"configInvalid,omitempty"`
	// DocsDirs are the plans and stories directories the config names.
	DocsDirs []string `json:"docsDirs,omitempty"`
	// DocsCommit is the default branch commit the plans and stories were
	// last synced from.
	DocsCommit string `json:"docsCommit,omitempty"`
	// Branches is the state of every branch seen, by name.
	Branches map[string]*BranchState `json:"branches"`
}

// branch returns the state of a branch, adding it when unknown.
func (st *RepoState) branch(name string) *BranchState {
	if st.Branches == nil {
		st.Branches = map[string]*BranchState{}
	}
	b, ok := st.Branches[name]
	if !ok {
		b = &BranchState{}
		st.Branches[name] = b
	}
	return b
}

// StateStore keeps the sync state of each repository of a target. The job
// queue runs one job per repository at a time, so an implementation needs
// no locking beyond its own consistency. A lost or stale state only costs
// full imports: the before-tip rule re-imports any branch whose stored
// head it does not know.
type StateStore interface {
	// Load returns the repository's state, or nil when there is none.
	Load(ctx context.Context, target Target, repository int64) (*RepoState, error)
	// Save replaces the repository's state.
	Save(ctx context.Context, target Target, st *RepoState) error
}

// MemState is the in-memory StateStore.
type MemState struct {
	mu sync.Mutex
	m  map[string][]byte
}

// NewMemState returns an empty MemState.
func NewMemState() *MemState { return &MemState{m: map[string][]byte{}} }

var _ StateStore = (*MemState)(nil)

func stateKey(t Target, repository int64) string {
	return string(t.Kind) + "\x00" + t.ID + "\x00" + strconv.FormatInt(repository, 10)
}

// Load implements StateStore; the state returned is a copy.
func (m *MemState) Load(ctx context.Context, target Target, repository int64) (*RepoState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	data, ok := m.m[stateKey(target, repository)]
	m.mu.Unlock()
	if !ok {
		return nil, nil
	}
	var st RepoState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// Save implements StateStore; it keeps a copy.
func (m *MemState) Save(ctx context.Context, target Target, st *RepoState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.m[stateKey(target, st.Repository)] = data
	return nil
}

// --- one repository's run ---------------------------------------------------

// repoRun is one job's work on one claimed repository: the claim, the
// installation client, the state and the lazily opened code store.
type repoRun struct {
	s        *Service
	inst     int64
	claim    Claim
	client   *ghapp.Client
	owner    string
	name     string
	st       *RepoState
	fresh    bool // no state was stored before this run
	delivery string

	store    code.Store
	storeFor string // slug the store was opened for
	wtReady  bool
	seeded   bool
	redactor *redact.Redactor
}

// errNoApp is returned when a job needs GitHub but the service has no app.
var errNoApp = errors.New("ghsync: no GitHub App configured")

// openRun resolves the claim of repository and opens its client and state.
// meta, when it carries the full name and default branch (a payload's
// repository), saves the repository lookup.
func (s *Service) openRun(ctx context.Context, installation, repository int64, meta *ghapp.Repository) (*repoRun, error) {
	claim, err := s.Claims.Claim(ctx, installation, repository)
	if err != nil {
		return nil, err
	}
	if err := claim.Target.Validate(); err != nil {
		return nil, err
	}
	if s.App == nil {
		return nil, errNoApp
	}
	client, err := s.App.InstallationClient(ctx, installation)
	if err != nil {
		return nil, err
	}
	if meta == nil || meta.FullName == "" || meta.DefaultBranch == "" {
		if meta, err = client.RepositoryByID(ctx, repository); err != nil {
			return nil, err
		}
	}
	owner, name, ok := splitRepo(meta.FullName)
	if !ok {
		return nil, fmt.Errorf("ghsync: repository %d: invalid name %q", repository, meta.FullName)
	}
	st, err := s.State.Load(ctx, claim.Target, repository)
	if err != nil {
		return nil, fmt.Errorf("ghsync: load state of repository %d: %w", repository, err)
	}
	r := &repoRun{s: s, inst: installation, claim: claim, client: client, owner: owner, name: name, st: st}
	if st == nil {
		r.fresh = true
		r.st = &RepoState{Repository: repository, Branches: map[string]*BranchState{}}
	}
	if r.st.Name != meta.FullName {
		// A rename or transfer: the slug follows unless remote.project
		// pins it, so the config is read again.
		r.st.Name, r.st.ConfigCommit = meta.FullName, ""
	}
	if r.st.DefaultBranch != meta.DefaultBranch {
		r.st.DefaultBranch, r.st.ConfigCommit = meta.DefaultBranch, ""
	}
	return r, nil
}

func splitRepo(full string) (owner, name string, ok bool) {
	owner, name, ok = strings.Cut(full, "/")
	return owner, name, ok && owner != "" && name != "" && !strings.Contains(name, "/")
}

func (r *repoRun) worktree() string { return WorktreeID(r.st.Repository) }

func (r *repoRun) save(ctx context.Context) error {
	if err := r.s.State.Save(ctx, r.claim.Target, r.st); err != nil {
		return fmt.Errorf("ghsync: save state of repository %d: %w", r.st.Repository, err)
	}
	return nil
}

// loadConfig reads the mind-palace config at ref (a commit or branch on the
// default branch) unless it was read there already: the slug, the sync's
// keys and the docs directories. A config that does not parse keeps code
// out (ConfigInvalid) rather than risk ignoring codeSync: false.
func (r *repoRun) loadConfig(ctx context.Context, ref string) error {
	if ref == "" {
		ref = r.st.DefaultBranch
	}
	if r.st.ConfigCommit == ref && r.st.Slug != "" {
		return nil
	}
	st := r.st
	st.Config, st.ConfigInvalid, st.DocsDirs = RepoConfig{}, false, nil
	st.Slug = projects.Slug("github.com/" + st.Name)
	f := ClientFetcher{Client: r.client}
	root, err := f.ListDir(ctx, st.Name, ref, "")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	file := ""
	for _, name := range config.DefaultConfigFiles {
		for _, e := range root {
			if !e.Dir && e.Name == name {
				file = name
				break
			}
		}
		if file != "" {
			break
		}
	}
	if file != "" {
		data, err := f.ReadFile(ctx, st.Name, ref, file)
		if err != nil {
			return err
		}
		rc, perr := ParseRepoConfig(data)
		if perr != nil || len(data) > MaxDocBytes {
			st.ConfigInvalid = true
			r.s.log.Warn("ghsync: invalid mind-palace config; code sync held", "repository", st.Repository, "file", file)
		} else {
			st.Config = rc
			if p := projects.Slug(rc.Project); p != "" && projects.ValidSlug(p) {
				st.Slug = p
			}
			cfg := config.Default()
			if overlayDocsConfig(cfg, data, path.Ext(file)) == nil {
				st.DocsDirs = []string{cfg.Plans.Dir, cfg.Stories.Dir}
			}
		}
	}
	st.ConfigCommit = ref
	return nil
}

// codeAllowed reports whether code may be written: the claim allows it and
// the repository's config neither opts out nor fails to parse.
func (r *repoRun) codeAllowed() bool {
	if !r.claim.CodeSync || r.st.ConfigInvalid {
		return false
	}
	return r.st.Config.CodeSync == nil || *r.st.Config.CodeSync
}

// policy is the repository's effective branch policy.
func (r *repoRun) policy() BranchPolicy { return EffectivePolicy(r.claim.Policy, r.st.Config) }

// selection applies the policy to every branch the state knows.
func (r *repoRun) selection() Selection {
	branches := make([]Branch, 0, len(r.st.Branches))
	for name, b := range r.st.Branches {
		branches = append(branches, Branch{Name: name, LastPush: b.LastPush, OpenPR: len(b.OpenPRs) > 0})
	}
	return r.policy().Select(r.st.DefaultBranch, branches, r.s.now())
}

// wanted reports whether the policy syncs branch now.
func (r *repoRun) wanted(branch string) bool {
	r.st.branch(branch)
	return slices.Contains(r.selection().Active, branch)
}

// openStore opens the code store of the repository's project (the config
// must be loaded), creating the worktree's metadata on first use when
// worktree is true.
func (r *repoRun) openStore(ctx context.Context, worktree bool) (code.Store, error) {
	if r.store == nil || r.storeFor != r.st.Slug {
		cs, err := r.s.Code(ctx, r.claim.Target, r.st.Slug)
		if err != nil {
			return nil, fmt.Errorf("ghsync: open code store of %s: %w", r.st.Slug, err)
		}
		if cs == nil {
			return nil, fmt.Errorf("ghsync: no code store for %s", r.st.Slug)
		}
		r.store, r.storeFor, r.wtReady, r.seeded = cs, r.st.Slug, false, false
	}
	if worktree && !r.wtReady {
		w := code.Worktree{
			ID:            r.worktree(),
			Owner:         r.claim.Target.Owner,
			Source:        code.SourceGitHub,
			Host:          GitHubHost,
			Origin:        "https://github.com/" + r.st.Name,
			DefaultBranch: r.st.DefaultBranch,
		}
		if err := r.store.PutWorktree(ctx, w); err != nil {
			return nil, fmt.Errorf("ghsync: put worktree %s: %w", w.ID, err)
		}
		r.wtReady = true
		if r.fresh && !r.seeded {
			if err := r.seedHeads(ctx); err != nil {
				return nil, err
			}
		}
	}
	return r.store, nil
}

// seedHeads recovers the stored heads of a repository whose state was lost
// from the code store's change log, so a restart does not re-import every
// branch.
func (r *repoRun) seedHeads(ctx context.Context) error {
	r.seeded = true
	heads := map[string]string{}
	wt := r.worktree()
	var after int64
	for {
		changes, err := r.store.Changes(ctx, after, 1000)
		if err != nil {
			return fmt.Errorf("ghsync: read change log: %w", err)
		}
		for _, c := range changes {
			after = c.Seq
			if c.Worktree != wt {
				continue
			}
			switch {
			case c.Ref == "":
				clear(heads)
			case c.Head == "" && c.Full && len(c.Upserts) == 0:
				delete(heads, c.Ref)
			default:
				heads[c.Ref] = c.Head
			}
		}
		if len(changes) < 1000 {
			break
		}
	}
	for ref, head := range heads {
		if b := r.st.branch(ref); b.Head == "" {
			b.Head = head
		}
	}
	return nil
}

// --- files -------------------------------------------------------------------

// gitFile is one file of a tree at a commit, as GitHub lists it.
type gitFile struct {
	path string
	mode string
	typ  string // ghapp.TypeBlob or ghapp.TypeCommit (a submodule)
	oid  string
	size int64
}

func (r *repoRun) filterOptions() codefilter.Options {
	return codefilter.Options{MaxFileBytes: r.s.maxFileBytes}
}

func (r *repoRun) redactorFor(ctx context.Context) (*redact.Redactor, error) {
	if r.redactor != nil {
		return r.redactor, nil
	}
	var patterns []string
	if r.s.Redact != nil {
		p, err := r.s.Redact(ctx, r.claim.Target)
		if err != nil {
			return nil, fmt.Errorf("ghsync: redaction patterns: %w", err)
		}
		patterns = p
	}
	rd, errs := redact.New(patterns)
	if len(errs) > 0 {
		r.s.log.Warn("ghsync: redaction patterns skipped", "count", len(errs))
	}
	r.redactor = rd
	return rd, nil
}

// lookupChunk bounds the git ids of one SHA256ForGitOID call.
const lookupChunk = 500

// entries turns files into the tree entries to write, fetching, filtering,
// redacting and storing the content of every blob the store does not hold
// yet. skipped are the paths left out entirely (deny list, invalid path):
// a change deletes them in case an earlier rule let them in.
func (r *repoRun) entries(ctx context.Context, store code.Store, files []gitFile) (out []code.Entry, skipped []string, err error) {
	opts := r.filterOptions()
	type pending struct {
		f   gitFile
		idx int
	}
	var want []pending
	for _, f := range files {
		if !code.ValidPath(f.path) || !code.ValidGitOID(f.oid) {
			skipped = append(skipped, f.path)
			continue
		}
		size := f.size
		if f.typ == ghapp.TypeCommit {
			size = 0
		}
		dec, _ := codefilter.Decide(f.path, size, nil, opts)
		if dec == codefilter.Skip {
			skipped = append(skipped, f.path)
			continue
		}
		e := code.Entry{Path: f.path, Size: size, Mode: f.mode, Lang: session.ExtToLang(f.path), GitOID: f.oid}
		out = append(out, e)
		if f.typ == ghapp.TypeCommit || dec == codefilter.MetadataOnly {
			continue
		}
		want = append(want, pending{f: f, idx: len(out) - 1})
	}
	if len(want) == 0 {
		return out, skipped, nil
	}

	// Content the project already holds by git blob id is not fetched.
	held := map[string]string{}
	var oids []string
	seen := map[string]bool{}
	for _, p := range want {
		if !seen[p.f.oid] {
			seen[p.f.oid] = true
			oids = append(oids, p.f.oid)
		}
	}
	for start := 0; start < len(oids); start += lookupChunk {
		m, err := store.SHA256ForGitOID(ctx, oids[start:min(start+lookupChunk, len(oids))])
		if err != nil {
			return nil, nil, fmt.Errorf("ghsync: look up git blob ids: %w", err)
		}
		for k, v := range m {
			held[k] = v
		}
	}

	rd, err := r.redactorFor(ctx)
	if err != nil {
		return nil, nil, err
	}
	fetched := map[string]string{} // oid → sha256 ("" for metadata only)
	blobs := map[string][]byte{}   // sha256 → content to store
	for _, p := range want {
		if sha, ok := held[p.f.oid]; ok {
			out[p.idx].SHA256 = sha
			continue
		}
		sha, done := fetched[p.f.oid]
		if !done {
			b, err := r.client.Blob(ctx, r.owner, r.name, p.f.oid)
			switch {
			case errors.Is(err, ghapp.ErrTooLarge):
				sha = ""
			case err != nil:
				return nil, nil, err
			default:
				if dec, _ := codefilter.Decide(p.f.path, int64(len(b.Content)), b.Content, opts); dec == codefilter.Content {
					masked, _ := rd.String(string(b.Content))
					sum := sha256.Sum256([]byte(masked))
					sha = hex.EncodeToString(sum[:])
					blobs[sha] = []byte(masked)
				}
				if out[p.idx].Size == 0 {
					out[p.idx].Size = int64(len(b.Content))
				}
			}
			fetched[p.f.oid] = sha
		}
		out[p.idx].SHA256 = sha
	}
	if len(blobs) > 0 {
		shas := make([]string, 0, len(blobs))
		for sha := range blobs {
			shas = append(shas, sha)
		}
		slices.Sort(shas)
		missing, err := store.MissingBlobs(ctx, shas)
		if err != nil {
			return nil, nil, fmt.Errorf("ghsync: missing blobs: %w", err)
		}
		for _, sha := range missing {
			c := blobs[sha]
			if err := store.PutBlob(ctx, code.Blob{SHA256: sha, Size: int64(len(c)), Content: c}); err != nil {
				return nil, nil, fmt.Errorf("ghsync: put blob: %w", err)
			}
		}
	}
	return out, skipped, nil
}

// --- full import -----------------------------------------------------------

// maxTreeDepth bounds the subtree walk of a truncated tree.
const maxTreeDepth = 64

// treeFiles lists every blob and submodule of the tree at sha (a commit or
// a tree). When GitHub truncates the recursive listing, the tree is listed
// one level and each subtree walked the same way.
func (r *repoRun) treeFiles(ctx context.Context, sha, prefix string, depth int) ([]gitFile, error) {
	if depth > maxTreeDepth {
		return nil, fmt.Errorf("ghsync: tree deeper than %d levels", maxTreeDepth)
	}
	t, err := r.client.Tree(ctx, r.owner, r.name, sha, true)
	if err != nil {
		return nil, err
	}
	var out []gitFile
	if !t.Truncated {
		for _, e := range t.Entries {
			if f, ok := treeFile(prefix, e); ok {
				out = append(out, f)
			}
		}
		return out, nil
	}
	level, err := r.client.Tree(ctx, r.owner, r.name, sha, false)
	if err != nil {
		return nil, err
	}
	for _, e := range level.Entries {
		if e.Type == ghapp.TypeTree {
			sub, err := r.treeFiles(ctx, e.SHA, prefix+e.Path+"/", depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, sub...)
			continue
		}
		if f, ok := treeFile(prefix, e); ok {
			out = append(out, f)
		}
	}
	return out, nil
}

func treeFile(prefix string, e ghapp.TreeEntry) (gitFile, bool) {
	switch e.Type {
	case ghapp.TypeBlob, ghapp.TypeCommit:
		return gitFile{path: prefix + e.Path, mode: e.Mode, typ: e.Type, oid: e.SHA, size: e.Size}, true
	}
	return gitFile{}, false
}

// importBranch replaces the branch's tree with the full tree at commit.
func (r *repoRun) importBranch(ctx context.Context, branch, commit string) error {
	if !code.ValidRef(branch) || !code.ValidGitOID(commit) {
		r.s.log.Warn("ghsync: branch or commit not storable; skipped", "repository", r.st.Repository)
		return nil
	}
	store, err := r.openStore(ctx, true)
	if err != nil {
		return err
	}
	files, err := r.treeFiles(ctx, commit, "", 0)
	if err != nil {
		return err
	}
	upserts, _, err := r.entries(ctx, store, files)
	if err != nil {
		return err
	}
	return r.commit(ctx, store, code.Change{Ref: branch, Head: commit, Upserts: upserts, Full: true})
}

// commit writes c to the GitHub worktree as clean, pushed content at
// c.Head and records the head.
func (r *repoRun) commit(ctx context.Context, store code.Store, c code.Change) error {
	c.Worktree = r.worktree()
	c.Upstream, c.Ahead, c.SessionID = c.Head, 0, ""
	if _, err := store.Commit(ctx, c); err != nil {
		return fmt.Errorf("ghsync: commit %s: %w", c.Ref, err)
	}
	b := r.st.branch(c.Ref)
	b.Head, b.Inactive = c.Head, false
	return r.save(ctx)
}

// --- push ----------------------------------------------------------------

// errFullImport makes an incremental push fall back to a full import.
var errFullImport = errors.New("ghsync: full import needed")

func zeroSHA(s string) bool { return s == "" || strings.Trim(s, "0") == "" }

// pushResult says what a push did to the code, for the docs sync.
type pushResult struct {
	// changed are the paths the comparison listed (nil after a full
	// import or when code was not compared).
	changed []string
	// compared is true when changed is complete.
	compared bool
}

// syncPushCode applies a push of branch from before to after to the
// GitHub worktree, under the before-tip rule.
func (r *repoRun) syncPushCode(ctx context.Context, branch, before, after string, forced bool) (pushResult, error) {
	b := r.st.branch(branch)
	if !r.codeAllowed() {
		return pushResult{}, nil
	}
	if !r.wanted(branch) {
		b.Inactive = true
		return pushResult{}, nil
	}
	if b.Head == after {
		b.Inactive = false
		return pushResult{}, nil
	}
	if !forced && !zeroSHA(before) && b.Head == before {
		res, err := r.applyCompare(ctx, branch, before, after)
		if err == nil {
			return res, nil
		}
		if !errors.Is(err, errFullImport) && !errors.Is(err, ghapp.ErrNotFound) && !errors.Is(err, ghapp.ErrTooLarge) {
			return pushResult{}, err
		}
	}
	return pushResult{}, r.importBranch(ctx, branch, after)
}

// applyCompare writes the files changed between before and after as one
// change; errFullImport when the comparison cannot be applied as is.
func (r *repoRun) applyCompare(ctx context.Context, branch, before, after string) (pushResult, error) {
	if !code.ValidRef(branch) || !code.ValidGitOID(after) {
		return pushResult{}, errFullImport
	}
	cmp, err := r.client.Compare(ctx, r.owner, r.name, before, after)
	if err != nil {
		return pushResult{}, err
	}
	if cmp.Truncated || cmp.Status != "ahead" {
		return pushResult{}, errFullImport
	}
	var res pushResult
	res.compared = true
	var deletes []string
	var upsertPaths []string
	upsertOID := map[string]string{}
	for _, f := range cmp.Files {
		res.changed = append(res.changed, f.Filename)
		switch f.Status {
		case ghapp.FileRemoved:
			deletes = append(deletes, f.Filename)
		case ghapp.FileRenamed:
			res.changed = append(res.changed, f.PreviousFilename)
			deletes = append(deletes, f.PreviousFilename)
			upsertPaths = append(upsertPaths, f.Filename)
			upsertOID[f.Filename] = f.SHA
		case ghapp.FileUnchanged:
		default:
			upsertPaths = append(upsertPaths, f.Filename)
			upsertOID[f.Filename] = f.SHA
		}
	}
	files, err := r.lookupFiles(ctx, after, upsertPaths)
	if err != nil {
		return pushResult{}, err
	}
	for _, f := range files {
		if f.oid != upsertOID[f.path] {
			return pushResult{}, errFullImport
		}
	}
	store, err := r.openStore(ctx, true)
	if err != nil {
		return pushResult{}, err
	}
	upserts, skipped, err := r.entries(ctx, store, files)
	if err != nil {
		return pushResult{}, err
	}
	upserted := make(map[string]bool, len(upserts))
	for _, e := range upserts {
		upserted[e.Path] = true
	}
	deletes = slices.DeleteFunc(append(deletes, skipped...), func(p string) bool { return !code.ValidPath(p) || upserted[p] })
	slices.Sort(deletes)
	deletes = slices.Compact(deletes)
	return res, r.commit(ctx, store, code.Change{Ref: branch, Head: after, Upserts: upserts, Deletes: deletes})
}

// lookupFiles finds the mode, type and size of each path at commit from the
// tree listings of their directories (the comparison gives neither);
// errFullImport when one is missing.
func (r *repoRun) lookupFiles(ctx context.Context, commit string, paths []string) ([]gitFile, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	dirs := map[string][]ghapp.TreeEntry{}
	shas := map[string]string{"": commit}
	var list func(dir string) ([]ghapp.TreeEntry, error)
	list = func(dir string) ([]ghapp.TreeEntry, error) {
		if es, ok := dirs[dir]; ok {
			return es, nil
		}
		sha, ok := shas[dir]
		if !ok {
			parent, base := path.Split(dir)
			parent = strings.TrimSuffix(parent, "/")
			pes, err := list(parent)
			if err != nil {
				return nil, err
			}
			for _, e := range pes {
				if e.Path == base && e.Type == ghapp.TypeTree {
					sha, ok = e.SHA, true
					break
				}
			}
			if !ok {
				return nil, errFullImport
			}
		}
		t, err := r.client.Tree(ctx, r.owner, r.name, sha, false)
		if err != nil {
			return nil, err
		}
		dirs[dir] = t.Entries
		return t.Entries, nil
	}
	out := make([]gitFile, 0, len(paths))
	for _, p := range paths {
		dir, base := path.Split(p)
		dir = strings.TrimSuffix(dir, "/")
		es, err := list(dir)
		if err != nil {
			return nil, err
		}
		found := false
		for _, e := range es {
			if e.Path == base && e.Type != ghapp.TypeTree {
				out = append(out, gitFile{path: p, mode: e.Mode, typ: e.Type, oid: e.SHA, size: e.Size})
				found = true
				break
			}
		}
		if !found {
			return nil, errFullImport
		}
	}
	return out, nil
}

// --- branches ------------------------------------------------------------

// deleteBranch drops a deleted branch: its tree (RemoveRef) when the sync
// wrote one, and its state.
func (r *repoRun) deleteBranch(ctx context.Context, branch string) error {
	b, ok := r.st.Branches[branch]
	if !ok {
		return nil
	}
	if b.Head != "" && code.ValidRef(branch) {
		store, err := r.openStore(ctx, false)
		if err != nil {
			return err
		}
		if err := store.RemoveRef(ctx, r.worktree(), branch); err != nil && !errors.Is(err, code.ErrNotFound) {
			return fmt.Errorf("ghsync: remove ref %s: %w", branch, err)
		}
	}
	delete(r.st.Branches, branch)
	return r.save(ctx)
}

// syncBranches brings the repository to GitHub's current branches: an
// import (only, when set, that one branch) or the nightly reconcile. Open
// pull requests are listed again, every branch the policy selects whose
// stored head differs from its tip is imported, the others are marked
// inactive (their tree kept), branches gone from GitHub lose their tree,
// and the plans and stories follow the default branch's tip.
func (r *repoRun) syncBranches(ctx context.Context, only string) error {
	branches, err := r.client.Branches(ctx, r.owner, r.name)
	if err != nil {
		return err
	}
	tips := make(map[string]string, len(branches))
	for _, b := range branches {
		tips[b.Name] = b.Commit.SHA
	}
	defaultTip := tips[r.st.DefaultBranch]
	if defaultTip == "" {
		// An empty repository: nothing to import yet.
		return r.save(ctx)
	}
	if err := r.loadConfig(ctx, defaultTip); err != nil {
		return err
	}
	pulls, err := r.client.OpenPulls(ctx, r.owner, r.name)
	if err != nil {
		return err
	}
	for _, b := range r.st.Branches {
		b.OpenPRs = nil
	}
	for name := range tips {
		r.st.branch(name)
	}
	for _, pr := range pulls {
		if pr.Head.Repo != nil && pr.Head.Repo.ID != r.st.Repository {
			continue // a fork's branch is not this repository's
		}
		if _, ok := tips[pr.Head.Ref]; ok {
			b := r.st.branch(pr.Head.Ref)
			b.OpenPRs = append(b.OpenPRs, pr.Number)
		}
	}
	if r.codeAllowed() {
		sel := r.selection()
		for _, name := range sel.Active {
			if only != "" && name != only {
				continue
			}
			tip, ok := tips[name]
			if !ok {
				continue
			}
			b := r.st.branch(name)
			b.Inactive = false
			if b.Head != tip {
				if err := r.importBranch(ctx, name, tip); err != nil {
					return err
				}
			}
		}
		for _, name := range sel.Inactive {
			r.st.branch(name).Inactive = true
		}
	}
	if only == "" {
		var gone []string
		for name := range r.st.Branches {
			if _, ok := tips[name]; !ok {
				gone = append(gone, name)
			}
		}
		slices.Sort(gone)
		for _, name := range gone {
			if err := r.deleteBranch(ctx, name); err != nil {
				return err
			}
		}
	}
	if (only == "" || only == r.st.DefaultBranch) && r.st.DocsCommit != defaultTip {
		if err := r.syncDocs(ctx, defaultTip, r.st.DocsCommit); err != nil {
			return err
		}
	}
	return r.save(ctx)
}

// sweep marks the branches the policy no longer selects inactive (the
// daily inactivity sweep); their trees are kept.
func (r *repoRun) sweep(ctx context.Context) error {
	sel := r.selection()
	for _, name := range sel.Inactive {
		r.st.branch(name).Inactive = true
	}
	return r.save(ctx)
}

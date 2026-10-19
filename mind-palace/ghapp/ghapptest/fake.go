// Package ghapptest is an in-process GitHub for tests: an httptest server
// that speaks the slice of the REST API ghapp.Client uses (installations and
// their tokens, repositories, branches, git trees with truncation, blobs,
// compare, contents, open pull requests), backed by real git object ids so
// blob, tree and commit shas look and behave like GitHub's. Tests seed it
// with repositories and commits, inject failures (404, 401, rate limits,
// 5xx), read the request log, and build and deliver signed webhook payloads.
// Nothing ever reaches api.github.com.
package ghapptest

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/ghapp"
)

// Defaults of a new Fake.
const (
	DefaultAppID      = 1001
	DefaultAppSlug    = "clued-test"
	DefaultRateLimit  = 5000
	DefaultTokenTTL   = time.Hour
	maxContentsInline = 1 << 20 // the contents API inlines files up to 1 MB
)

var (
	keyOnce   sync.Once
	sharedKey *rsa.PrivateKey
)

// Key returns the RSA key every Fake signs and verifies app JWTs with,
// generated once per test binary.
func Key() *rsa.PrivateKey {
	keyOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		sharedKey = k
	})
	return sharedKey
}

// Fault makes matching requests fail. Method and Path ("" matches any) select
// requests by method and path prefix; Times is how many requests fail (0:
// every one until ClearFaults).
type Fault struct {
	Method  string
	Path    string
	Status  int
	Message string
	Header  http.Header
	Times   int

	hits int
}

// Request is one request the Fake received. Installation is the installation
// whose token authenticated it (0 for app JWT or unauthenticated requests).
type Request struct {
	Method       string
	Path         string
	RawQuery     string
	Installation int64
}

// Fake is a GitHub REST API on an httptest server. Configure the exported
// fields before the first request; seed state with the methods, which are
// safe for concurrent use.
type Fake struct {
	Server *httptest.Server
	URL    string

	AppID   int64
	AppSlug string
	Key     *rsa.PrivateKey
	// Now is the fake's clock (time.Now when nil).
	Now func() time.Time
	// PageSize caps per_page (100 when zero), so tests can force pagination.
	PageSize int
	// TreeLimit truncates recursive tree listings with more entries (0: no
	// limit); a Repo's own TreeLimit overrides it.
	TreeLimit int
	// TokenTTL is the lifetime of minted installation tokens.
	TokenTTL time.Duration
	// RateLimit is the per-installation request allowance reported in the
	// X-RateLimit-* headers.
	RateLimit int

	mu            sync.Mutex
	nextID        int64
	installations map[int64]*installation
	repos         map[int64]*Repo
	byName        map[string]*Repo
	tokens        map[string]tokenGrant
	remaining     map[int64]int
	faults        []*Fault
	requests      []Request
}

type installation struct {
	meta  ghapp.Installation
	repos map[int64]bool
}

type tokenGrant struct {
	installation int64
	expires      time.Time
}

// New starts a Fake and closes it when the test ends.
func New(t testing.TB) *Fake {
	t.Helper()
	f := &Fake{
		AppID:         DefaultAppID,
		AppSlug:       DefaultAppSlug,
		Key:           Key(),
		TokenTTL:      DefaultTokenTTL,
		RateLimit:     DefaultRateLimit,
		nextID:        100,
		installations: map[int64]*installation{},
		repos:         map[int64]*Repo{},
		byName:        map[string]*Repo{},
		tokens:        map[string]tokenGrant{},
		remaining:     map[int64]int{},
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	f.URL = f.Server.URL
	t.Cleanup(f.Server.Close)
	return f
}

// App returns a ghapp.App registered with this fake: its app id, key, base
// URL and HTTP client.
func (f *Fake) App() *ghapp.App {
	return &ghapp.App{ID: f.AppID, Key: f.Key, BaseURL: f.URL, HTTPClient: f.Server.Client(), Now: f.Now}
}

func (f *Fake) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

func (f *Fake) id() int64 {
	f.nextID++
	return f.nextID
}

// --- installations ---------------------------------------------------------

// AddInstallation installs the app with access to repos. Zero fields of inst
// are filled in: the id, the app id and slug, the account (a user named
// "octo"), the repository selection and the timestamps. It returns the
// installation as GitHub would describe it.
func (f *Fake) AddInstallation(inst ghapp.Installation, repos ...*Repo) ghapp.Installation {
	f.mu.Lock()
	defer f.mu.Unlock()
	if inst.ID == 0 {
		inst.ID = f.id()
	}
	if inst.AppID == 0 {
		inst.AppID = f.AppID
	}
	if inst.AppSlug == "" {
		inst.AppSlug = f.AppSlug
	}
	if inst.Account.Login == "" {
		inst.Account = ghapp.User{Login: "octo", Type: "User"}
	}
	if inst.Account.ID == 0 {
		inst.Account.ID = f.id()
	}
	if inst.Account.Type == "" {
		inst.Account.Type = "User"
	}
	if inst.TargetID == 0 {
		inst.TargetID = inst.Account.ID
	}
	if inst.TargetType == "" {
		inst.TargetType = inst.Account.Type
	}
	if inst.RepositorySelection == "" {
		inst.RepositorySelection = "selected"
	}
	if inst.Permissions == nil {
		inst.Permissions = map[string]string{"contents": "read", "metadata": "read", "pull_requests": "read", "checks": "read", "statuses": "read", "actions": "read"}
	}
	if inst.CreatedAt.IsZero() {
		inst.CreatedAt = ghapp.Timestamp{Time: f.now().UTC().Truncate(time.Second)}
		inst.UpdatedAt = inst.CreatedAt
	}
	in := &installation{meta: inst, repos: map[int64]bool{}}
	for _, r := range repos {
		in.repos[r.id] = true
	}
	f.installations[inst.ID] = in
	if _, ok := f.remaining[inst.ID]; !ok {
		f.remaining[inst.ID] = f.RateLimit
	}
	return inst
}

// Installation returns a seeded installation.
func (f *Fake) Installation(id int64) (ghapp.Installation, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	in, ok := f.installations[id]
	if !ok {
		return ghapp.Installation{}, false
	}
	return in.meta, true
}

// SetInstallationRepos replaces the repositories an installation can access.
func (f *Fake) SetInstallationRepos(id int64, repos ...*Repo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if in, ok := f.installations[id]; ok {
		in.repos = map[int64]bool{}
		for _, r := range repos {
			in.repos[r.id] = true
		}
	}
}

// RemoveInstallation uninstalls the app: token requests answer 404 and
// existing tokens 401.
func (f *Fake) RemoveInstallation(id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.installations, id)
}

// Suspend suspends or unsuspends an installation: token requests and calls
// with its tokens answer 403.
func (f *Fake) Suspend(id int64, suspended bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if in, ok := f.installations[id]; ok {
		if suspended {
			in.meta.SuspendedAt = ghapp.Timestamp{Time: f.now().UTC().Truncate(time.Second)}
		} else {
			in.meta.SuspendedAt = ghapp.Timestamp{}
		}
	}
}

// RevokeTokens invalidates every installation token minted so far (the next
// call with one answers 401).
func (f *Fake) RevokeTokens() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens = map[string]tokenGrant{}
}

// SetRateRemaining sets an installation's remaining request allowance; at 0
// its requests answer 403 with X-RateLimit-Remaining: 0 until it is raised.
func (f *Fake) SetRateRemaining(installationID int64, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.remaining[installationID] = n
}

// --- faults and the request log -------------------------------------------

// Inject adds a fault; faults are checked in the order added.
func (f *Fake) Inject(fl Fault) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := fl
	cp.hits = 0
	f.faults = append(f.faults, &cp)
}

// FailNext makes the next times requests under pathPrefix answer status.
func (f *Fake) FailNext(pathPrefix string, status, times int) {
	f.Inject(Fault{Path: pathPrefix, Status: status, Times: times})
}

// RateLimitNext makes the next times requests under pathPrefix answer a
// secondary rate limit: 403 with Retry-After.
func (f *Fake) RateLimitNext(pathPrefix string, retryAfter time.Duration, times int) {
	h := http.Header{}
	h.Set("Retry-After", strconv.Itoa(int(retryAfter/time.Second)))
	f.Inject(Fault{Path: pathPrefix, Status: http.StatusForbidden, Message: "You have exceeded a secondary rate limit.", Header: h, Times: times})
}

// ClearFaults removes every fault.
func (f *Fake) ClearFaults() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.faults = nil
}

// Requests returns the requests received so far.
func (f *Fake) Requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.requests)
}

// Count is how many requests with method ("" any) had a path under prefix.
func (f *Fake) Count(method, prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if (method == "" || r.Method == method) && strings.HasPrefix(r.Path, prefix) {
			n++
		}
	}
	return n
}

// ResetRequests clears the request log.
func (f *Fake) ResetRequests() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = nil
}

// --- repositories and git objects -----------------------------------------

// Repo is a repository held by the Fake: metadata, branches, git objects and
// pull requests. Its methods are safe for concurrent use.
type Repo struct {
	f  *Fake
	id int64

	meta     ghapp.Repository
	branches map[string]string // name → commit sha
	commits  map[string]*commit
	trees    map[string][]treeChild
	blobs    map[string][]byte
	pulls    []*ghapp.PullRequest
	compares map[string]*ghapp.Comparison

	// treeLimit overrides Fake.TreeLimit for this repository when non-zero.
	treeLimit int
}

type commit struct {
	sha     string
	tree    string
	parents []string
	message string
	at      time.Time
}

type treeChild struct {
	name string
	mode string // API form: 100644, 100755, 120000, 040000, 160000
	sha  string
}

func (c treeChild) typ() string {
	switch c.mode {
	case ghapp.ModeTree:
		return ghapp.TypeTree
	case ghapp.ModeSubmodule:
		return ghapp.TypeCommit
	default:
		return ghapp.TypeBlob
	}
}

// AddRepo creates an empty repository owner/name (no branches yet; the first
// Commit creates its default branch, "main").
func (f *Fake) AddRepo(owner, name string) *Repo {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.id()
	r := &Repo{
		f:  f,
		id: id,
		meta: ghapp.Repository{
			ID:            id,
			NodeID:        "R_" + strconv.FormatInt(id, 10),
			Name:          name,
			FullName:      owner + "/" + name,
			Owner:         ghapp.User{Login: owner, ID: f.id(), Type: "User"},
			Private:       true,
			Visibility:    "private",
			DefaultBranch: "main",
			HTMLURL:       "https://github.com/" + owner + "/" + name,
			CreatedAt:     ghapp.Timestamp{Time: f.now().UTC().Truncate(time.Second)},
		},
		branches: map[string]string{},
		commits:  map[string]*commit{},
		trees:    map[string][]treeChild{},
		blobs:    map[string][]byte{},
		compares: map[string]*ghapp.Comparison{},
	}
	f.repos[id] = r
	f.byName[strings.ToLower(r.meta.FullName)] = r
	return r
}

// Repo returns the repository named owner/name.
func (f *Fake) Repo(fullName string) (*Repo, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.byName[strings.ToLower(fullName)]
	return r, ok
}

// DeleteRepo removes a repository: every call for it answers 404.
func (f *Fake) DeleteRepo(r *Repo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.repos, r.id)
	delete(f.byName, strings.ToLower(r.meta.FullName))
	for _, in := range f.installations {
		delete(in.repos, r.id)
	}
}

// ID is the repository's numeric id.
func (r *Repo) ID() int64 { return r.id }

// Meta returns the repository as GitHub describes it.
func (r *Repo) Meta() ghapp.Repository {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	return r.meta
}

// Update changes the repository's metadata (rename, transfer, archive,
// default branch); the id cannot change.
func (r *Repo) Update(fn func(*ghapp.Repository)) {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	delete(r.f.byName, strings.ToLower(r.meta.FullName))
	fn(&r.meta)
	r.meta.ID = r.id
	r.meta.FullName = r.meta.Owner.Login + "/" + r.meta.Name
	r.f.byName[strings.ToLower(r.meta.FullName)] = r
}

// SetTreeLimit truncates this repository's recursive tree listings beyond n
// entries (0: use Fake.TreeLimit).
func (r *Repo) SetTreeLimit(n int) {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	r.treeLimit = n
}

// SetCompare serves cmp for compare/{base}...{head} instead of computing it
// (nil removes the override).
func (r *Repo) SetCompare(base, head string, cmp *ghapp.Comparison) {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	if cmp == nil {
		delete(r.compares, base+"..."+head)
		return
	}
	r.compares[base+"..."+head] = cmp
}

// Change is one file change of a Commit; build it with Put, PutMode, Symlink,
// Submodule or Delete.
type Change struct {
	Path    string
	Mode    string
	Content []byte
	SHA     string // a submodule's commit
	Delete  bool
}

// Put writes a regular file.
func Put(p, content string) Change {
	return Change{Path: p, Mode: ghapp.ModeFile, Content: []byte(content)}
}

// PutMode writes a file with an explicit mode (ghapp.ModeExecutable, …).
func PutMode(p, mode string, content []byte) Change {
	return Change{Path: p, Mode: mode, Content: content}
}

// Symlink writes a symbolic link to target.
func Symlink(p, target string) Change {
	return Change{Path: p, Mode: ghapp.ModeSymlink, Content: []byte(target)}
}

// Submodule records a submodule at p pinned to commit sha.
func Submodule(p, sha string) Change { return Change{Path: p, Mode: ghapp.ModeSubmodule, SHA: sha} }

// Delete removes a file.
func Delete(p string) Change { return Change{Path: p, Delete: true} }

type leaf struct {
	mode string
	sha  string
}

// Commit applies changes on top of branch's tip (or the default branch's tip
// for a new branch, or nothing for an empty repository), moves the branch to
// the new commit and returns its sha.
func (r *Repo) Commit(branch, message string, changes ...Change) string {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	parent, ok := r.branches[branch]
	if !ok {
		parent = r.branches[r.meta.DefaultBranch]
	}
	files := map[string]leaf{}
	if parent != "" {
		r.flatten(r.commits[parent].tree, "", files)
	}
	for _, ch := range changes {
		p := strings.Trim(path.Clean("/"+ch.Path), "/")
		if ch.Delete {
			delete(files, p)
			for k := range files {
				if strings.HasPrefix(k, p+"/") {
					delete(files, k)
				}
			}
			continue
		}
		if ch.Mode == ghapp.ModeSubmodule {
			files[p] = leaf{mode: ch.Mode, sha: ch.SHA}
			continue
		}
		sha := gitHash("blob", ch.Content)
		r.blobs[sha] = slices.Clone(ch.Content)
		mode := ch.Mode
		if mode == "" {
			mode = ghapp.ModeFile
		}
		files[p] = leaf{mode: mode, sha: sha}
	}
	tree := r.buildTree(files)
	var parents []string
	if parent != "" {
		parents = []string{parent}
	}
	return r.addCommit(branch, tree, parents, message)
}

// Merge records a merge commit on into whose parents are into's tip and
// from's tip, with from's tree, and returns its sha.
func (r *Repo) Merge(into, from, message string) string {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	a, b := r.branches[into], r.branches[from]
	if b == "" {
		panic("ghapptest: merge from unknown branch " + from)
	}
	var parents []string
	if a != "" {
		parents = append(parents, a)
	}
	parents = append(parents, b)
	return r.addCommit(into, r.commits[b].tree, parents, message)
}

func (r *Repo) addCommit(branch, tree string, parents []string, message string) string {
	at := r.f.now().UTC().Truncate(time.Second)
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "tree %s\n", tree)
	for _, p := range parents {
		fmt.Fprintf(&buf, "parent %s\n", p)
	}
	// The commit count keeps two otherwise identical commits apart.
	fmt.Fprintf(&buf, "author Fake <fake@example.invalid> %d +0000\ncommitter Fake <fake@example.invalid> %d +0000\n\n%s\n#%d\n", at.Unix(), at.Unix(), message, len(r.commits))
	sha := gitHash("commit", buf.Bytes())
	r.commits[sha] = &commit{sha: sha, tree: tree, parents: parents, message: message, at: at}
	r.branches[branch] = sha
	r.meta.PushedAt = ghapp.Timestamp{Time: at}
	return sha
}

// Head is branch's tip ("" when the branch does not exist).
func (r *Repo) Head(branch string) string {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	return r.branches[branch]
}

// TreeSHA is the root tree of a commit ("" when unknown).
func (r *Repo) TreeSHA(commitSHA string) string {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	if c, ok := r.commits[commitSHA]; ok {
		return c.tree
	}
	return ""
}

// Files returns path → blob sha of every blob at a commit or branch.
func (r *Repo) Files(ref string) map[string]string {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	c := r.resolveCommit(ref)
	out := map[string]string{}
	if c == nil {
		return out
	}
	files := map[string]leaf{}
	r.flatten(c.tree, "", files)
	for p, l := range files {
		if l.mode != ghapp.ModeSubmodule {
			out[p] = l.sha
		}
	}
	return out
}

// SetBranch points branch at an existing commit, as a force push or a branch
// created at a commit does.
func (r *Repo) SetBranch(branch, commitSHA string) {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	if _, ok := r.commits[commitSHA]; !ok {
		panic("ghapptest: unknown commit " + commitSHA)
	}
	r.branches[branch] = commitSHA
	r.meta.PushedAt = ghapp.Timestamp{Time: r.f.now().UTC().Truncate(time.Second)}
}

// DeleteBranch removes a branch.
func (r *Repo) DeleteBranch(branch string) {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	delete(r.branches, branch)
}

// OpenPull opens a pull request from head into base (the default branch when
// empty) and returns it. Zero fields of pr are filled in.
func (r *Repo) OpenPull(head, base string, pr ghapp.PullRequest) ghapp.PullRequest {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	if base == "" {
		base = r.meta.DefaultBranch
	}
	if pr.ID == 0 {
		pr.ID = r.f.id()
	}
	if pr.Number == 0 {
		pr.Number = len(r.pulls) + 1
	}
	if pr.Title == "" {
		pr.Title = "Merge " + head + " into " + base
	}
	pr.State = "open"
	meta := r.meta
	pr.Head = ghapp.PullRequestBranch{Label: meta.Owner.Login + ":" + head, Ref: head, SHA: r.branches[head], Repo: &meta}
	pr.Base = ghapp.PullRequestBranch{Label: meta.Owner.Login + ":" + base, Ref: base, SHA: r.branches[base], Repo: &meta}
	if pr.HTMLURL == "" {
		pr.HTMLURL = meta.HTMLURL + "/pull/" + strconv.Itoa(pr.Number)
	}
	if pr.CreatedAt.IsZero() {
		pr.CreatedAt = ghapp.Timestamp{Time: r.f.now().UTC().Truncate(time.Second)}
	}
	pr.UpdatedAt = pr.CreatedAt
	cp := pr
	r.pulls = append(r.pulls, &cp)
	return pr
}

// ClosePull closes pull request number, merged or not.
func (r *Repo) ClosePull(number int, merged bool) {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	now := ghapp.Timestamp{Time: r.f.now().UTC().Truncate(time.Second)}
	for _, pr := range r.pulls {
		if pr.Number == number {
			pr.State = "closed"
			pr.ClosedAt, pr.UpdatedAt = now, now
			if merged {
				pr.Merged = true
				pr.MergedAt = now
				pr.MergeCommitSHA = r.branches[pr.Base.Ref]
			}
		}
	}
}

func (r *Repo) flatten(treeSHA, prefix string, out map[string]leaf) {
	for _, c := range r.trees[treeSHA] {
		p := c.name
		if prefix != "" {
			p = prefix + "/" + c.name
		}
		if c.mode == ghapp.ModeTree {
			r.flatten(c.sha, p, out)
			continue
		}
		out[p] = leaf{mode: c.mode, sha: c.sha}
	}
}

// buildTree writes the tree objects for files and returns the root's sha.
func (r *Repo) buildTree(files map[string]leaf) string {
	type dir struct {
		files map[string]leaf
		dirs  map[string]map[string]leaf
	}
	d := dir{files: map[string]leaf{}, dirs: map[string]map[string]leaf{}}
	for p, l := range files {
		first, rest, nested := strings.Cut(p, "/")
		if !nested {
			d.files[first] = l
			continue
		}
		if d.dirs[first] == nil {
			d.dirs[first] = map[string]leaf{}
		}
		d.dirs[first][rest] = l
	}
	var children []treeChild
	for name, l := range d.files {
		children = append(children, treeChild{name: name, mode: l.mode, sha: l.sha})
	}
	for name, sub := range d.dirs {
		children = append(children, treeChild{name: name, mode: ghapp.ModeTree, sha: r.buildTree(sub)})
	}
	// git orders entries by name, a tree's name compared as if it ended in "/".
	sortKey := func(c treeChild) string {
		if c.mode == ghapp.ModeTree {
			return c.name + "/"
		}
		return c.name
	}
	sort.Slice(children, func(i, j int) bool { return sortKey(children[i]) < sortKey(children[j]) })
	var buf bytes.Buffer
	for _, c := range children {
		mode := strings.TrimPrefix(c.mode, "0") // git writes trees as 40000
		raw, _ := hex.DecodeString(c.sha)
		fmt.Fprintf(&buf, "%s %s\x00", mode, c.name)
		buf.Write(raw)
	}
	sha := gitHash("tree", buf.Bytes())
	r.trees[sha] = children
	return sha
}

// GitBlobSHA is git's object id of a blob with content, as GitHub reports it.
func GitBlobSHA(content []byte) string { return gitHash("blob", content) }

func gitHash(kind string, content []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "%s %d\x00", kind, len(content))
	h.Write(content)
	return hex.EncodeToString(h.Sum(nil))
}

func (r *Repo) resolveCommit(ref string) *commit {
	if sha, ok := r.branches[ref]; ok {
		return r.commits[sha]
	}
	ref = strings.TrimPrefix(ref, ghapp.BranchRefPrefix)
	if sha, ok := r.branches[ref]; ok {
		return r.commits[sha]
	}
	if c, ok := r.commits[ref]; ok {
		return c
	}
	// An abbreviated sha.
	if len(ref) >= 7 {
		for sha, c := range r.commits {
			if strings.HasPrefix(sha, ref) {
				return c
			}
		}
	}
	return nil
}

// ancestors returns every commit reachable from sha, with its distance.
func (r *Repo) ancestors(sha string) map[string]int {
	out := map[string]int{}
	queue := []string{sha}
	out[sha] = 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		c := r.commits[cur]
		if c == nil {
			continue
		}
		for _, p := range c.parents {
			if _, seen := out[p]; !seen {
				out[p] = out[cur] + 1
				queue = append(queue, p)
			}
		}
	}
	return out
}

// --- webhook payloads ------------------------------------------------------

// installationOf returns the first installation that can access repo id.
func (f *Fake) installationOf(id int64) *ghapp.InstallationRef {
	var ids []int64
	for iid, in := range f.installations {
		if in.repos[id] {
			ids = append(ids, iid)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	slices.Sort(ids)
	return &ghapp.InstallationRef{ID: ids[0]}
}

// Envelope is the repository, installation and sender a delivery about r
// carries; sender defaults to the repository's owner.
func (r *Repo) Envelope(action string, sender *ghapp.User) ghapp.Envelope {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	return r.envelope(action, sender)
}

func (r *Repo) envelope(action string, sender *ghapp.User) ghapp.Envelope {
	meta := r.meta
	if sender == nil {
		owner := meta.Owner
		sender = &owner
	}
	return ghapp.Envelope{Action: action, Repository: &meta, Installation: r.f.installationOf(r.id), Sender: sender}
}

// PushEvent builds the push payload GitHub sends when branch moves from
// before to after (ghapp.ZeroSHA for a created or deleted branch): the
// commits after has that before has not (oldest first), forced when before is
// not an ancestor of after.
func (r *Repo) PushEvent(branch, before, after string) *ghapp.PushEvent {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()
	ev := &ghapp.PushEvent{
		Envelope: r.envelope("", nil),
		Ref:      ghapp.BranchRefPrefix + branch,
		Before:   before,
		After:    after,
		Created:  before == ghapp.ZeroSHA,
		Deleted:  after == ghapp.ZeroSHA,
		Compare:  r.meta.HTMLURL + "/compare/" + short(before) + "..." + short(after),
		Commits:  []ghapp.PushCommit{},
		Pusher:   ghapp.Pusher{Name: r.meta.Owner.Login},
	}
	if ev.Deleted {
		return ev
	}
	var have map[string]int
	if !ev.Created {
		have = r.ancestors(before)
		if _, ok := r.ancestors(after)[before]; !ok {
			ev.Forced = true
		}
	}
	reach := r.ancestors(after)
	var shas []string
	for sha := range reach {
		if _, ok := have[sha]; !ok {
			shas = append(shas, sha)
		}
	}
	if ev.Created {
		// A new branch lists only the commits not on the default branch.
		if base := r.branches[r.meta.DefaultBranch]; base != "" && branch != r.meta.DefaultBranch {
			onBase := r.ancestors(base)
			shas = slices.DeleteFunc(shas, func(s string) bool { _, ok := onBase[s]; return ok })
		}
	}
	sort.Slice(shas, func(i, j int) bool { return reach[shas[i]] > reach[shas[j]] })
	for _, sha := range shas {
		ev.Commits = append(ev.Commits, r.pushCommit(sha))
	}
	head := r.pushCommit(after)
	ev.HeadCommit = &head
	return ev
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func (r *Repo) pushCommit(sha string) ghapp.PushCommit {
	c := r.commits[sha]
	pc := ghapp.PushCommit{ID: sha, Distinct: true, URL: r.meta.HTMLURL + "/commit/" + sha, Author: ghapp.CommitAuthor{Name: r.meta.Owner.Login, Username: r.meta.Owner.Login}}
	if c == nil {
		return pc
	}
	pc.TreeID, pc.Message, pc.Timestamp = c.tree, c.message, ghapp.Timestamp{Time: c.at}
	before := map[string]leaf{}
	if len(c.parents) > 0 {
		r.flatten(r.commits[c.parents[0]].tree, "", before)
	}
	after := map[string]leaf{}
	r.flatten(c.tree, "", after)
	for p, l := range after {
		if old, ok := before[p]; !ok {
			pc.Added = append(pc.Added, p)
		} else if old != l {
			pc.Modified = append(pc.Modified, p)
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			pc.Removed = append(pc.Removed, p)
		}
	}
	slices.Sort(pc.Added)
	slices.Sort(pc.Modified)
	slices.Sort(pc.Removed)
	return pc
}

// Deliver POSTs ev to url as GitHub delivers a webhook: the JSON payload with
// X-GitHub-Event, X-GitHub-Delivery (deliveryID, random when empty) and
// X-Hub-Signature-256 under secret. hc nil means http.DefaultClient.
func Deliver(ctx context.Context, hc *http.Client, url string, secret []byte, deliveryID string, ev ghapp.Event) (*http.Response, error) {
	body, err := json.Marshal(ev)
	if err != nil {
		return nil, err
	}
	return DeliverRaw(ctx, hc, url, secret, deliveryID, ev.EventName(), body)
}

// DeliverRaw is Deliver with a payload already encoded (a recorded fixture).
func DeliverRaw(ctx context.Context, hc *http.Client, url string, secret []byte, deliveryID, event string, body []byte) (*http.Response, error) {
	if deliveryID == "" {
		var b [16]byte
		_, _ = rand.Read(b[:])
		deliveryID = hex.EncodeToString(b[:])
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "GitHub-Hookshot/fake")
	req.Header.Set(ghapp.EventHeader, event)
	req.Header.Set(ghapp.DeliveryHeader, deliveryID)
	req.Header.Set(ghapp.SignatureHeader, ghapp.Sign(secret, body))
	if hc == nil {
		hc = http.DefaultClient
	}
	return hc.Do(req)
}

// --- HTTP ------------------------------------------------------------------

type apiError struct {
	status int
	msg    string
	header http.Header
}

func fail(status int, msg string) *apiError { return &apiError{status: status, msg: msg} }

var errNotFound = fail(http.StatusNotFound, "Not Found")

func (f *Fake) serve(w http.ResponseWriter, req *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec := Request{Method: req.Method, Path: req.URL.Path, RawQuery: req.URL.RawQuery}
	status, body, header := f.handle(req, &rec)
	f.requests = append(f.requests, rec)
	for k, vs := range header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func (f *Fake) handle(req *http.Request, rec *Request) (int, []byte, http.Header) {
	header := http.Header{}
	for _, fl := range f.faults {
		if (fl.Method == "" || fl.Method == req.Method) && strings.HasPrefix(req.URL.Path, fl.Path) && (fl.Times == 0 || fl.hits < fl.Times) {
			fl.hits++
			for k, vs := range fl.Header {
				header[k] = slices.Clone(vs)
			}
			msg := fl.Message
			if msg == "" {
				msg = http.StatusText(fl.Status)
			}
			return fl.Status, errorBody(msg), header
		}
	}
	parts := strings.Split(strings.Trim(req.URL.Path, "/"), "/")
	var (
		v   any
		err *apiError
	)
	if parts[0] == "app" {
		if err = f.checkJWT(req); err == nil {
			v, err = f.routeApp(req, parts, header)
		}
	} else {
		var inst *installation
		inst, err = f.checkToken(req, header)
		if err == nil {
			rec.Installation = inst.meta.ID
			v, err = f.routeInstallation(req, parts, inst, header)
		}
	}
	if err != nil {
		for k, vs := range err.header {
			header[k] = vs
		}
		return err.status, errorBody(err.msg), header
	}
	status := http.StatusOK
	if c, ok := v.(created); ok {
		status, v = http.StatusCreated, c.v
	}
	body, mErr := json.Marshal(v)
	if mErr != nil {
		return http.StatusInternalServerError, errorBody(mErr.Error()), header
	}
	return status, body, header
}

type created struct{ v any }

func errorBody(msg string) []byte {
	b, _ := json.Marshal(map[string]string{"message": msg, "documentation_url": "https://docs.github.com/rest"})
	return b
}

func bearer(req *http.Request) string {
	v, ok := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(v)
}

// checkJWT verifies the app JWT: RS256 under the fake's key, iss the app id,
// exp in the future and at most ten minutes after iat.
func (f *Fake) checkJWT(req *http.Request) *apiError {
	bad := fail(http.StatusUnauthorized, "A JSON web token could not be decoded")
	parts := strings.Split(bearer(req), ".")
	if len(parts) != 3 {
		return bad
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return bad
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(&f.Key.PublicKey, crypto.SHA256, digest[:], sig) != nil {
		return bad
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return bad
	}
	var claims struct {
		IAT int64  `json:"iat"`
		EXP int64  `json:"exp"`
		ISS string `json:"iss"`
	}
	if json.Unmarshal(raw, &claims) != nil || claims.ISS != strconv.FormatInt(f.AppID, 10) {
		return bad
	}
	now := f.now().Unix()
	if claims.EXP <= now || claims.IAT > now+60 || claims.EXP-claims.IAT > 600 {
		return fail(http.StatusUnauthorized, "'Expiration time' claim ('exp') is too far in the future")
	}
	return nil
}

func (f *Fake) checkToken(req *http.Request, header http.Header) (*installation, *apiError) {
	grant, ok := f.tokens[bearer(req)]
	if !ok || !f.now().Before(grant.expires) {
		return nil, fail(http.StatusUnauthorized, "Bad credentials")
	}
	inst, ok := f.installations[grant.installation]
	if !ok {
		return nil, fail(http.StatusUnauthorized, "Bad credentials")
	}
	if inst.meta.Suspended() {
		return nil, fail(http.StatusForbidden, "This installation has been suspended")
	}
	reset := strconv.FormatInt(f.now().Add(time.Hour).Unix(), 10)
	rem := f.remaining[inst.meta.ID]
	header.Set("X-RateLimit-Limit", strconv.Itoa(f.RateLimit))
	header.Set("X-RateLimit-Reset", reset)
	header.Set("X-RateLimit-Resource", "core")
	if rem <= 0 {
		header.Set("X-RateLimit-Remaining", "0")
		header.Set("X-RateLimit-Used", strconv.Itoa(f.RateLimit))
		return nil, fail(http.StatusForbidden, fmt.Sprintf("API rate limit exceeded for installation ID %d.", inst.meta.ID))
	}
	rem--
	f.remaining[inst.meta.ID] = rem
	header.Set("X-RateLimit-Remaining", strconv.Itoa(rem))
	header.Set("X-RateLimit-Used", strconv.Itoa(f.RateLimit-rem))
	return inst, nil
}

func (f *Fake) routeApp(req *http.Request, parts []string, header http.Header) (any, *apiError) {
	switch {
	case len(parts) == 1 && req.Method == http.MethodGet:
		return map[string]any{"id": f.AppID, "slug": f.AppSlug, "name": f.AppSlug}, nil
	case len(parts) == 2 && parts[1] == "installations" && req.Method == http.MethodGet:
		ids := make([]int64, 0, len(f.installations))
		for id := range f.installations {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		start, end := f.paginate(req, len(ids), header)
		out := make([]ghapp.Installation, 0, end-start)
		for _, id := range ids[start:end] {
			out = append(out, f.installations[id].meta)
		}
		return out, nil
	case len(parts) >= 3 && parts[1] == "installations":
		id, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			return nil, errNotFound
		}
		inst, ok := f.installations[id]
		if !ok {
			return nil, errNotFound
		}
		if len(parts) == 3 && req.Method == http.MethodGet {
			return inst.meta, nil
		}
		if len(parts) == 4 && parts[3] == "access_tokens" && req.Method == http.MethodPost {
			if inst.meta.Suspended() {
				return nil, fail(http.StatusForbidden, "This installation has been suspended")
			}
			var b [20]byte
			_, _ = rand.Read(b[:])
			tok := "ghs_" + hex.EncodeToString(b[:])
			ttl := f.TokenTTL
			if ttl <= 0 {
				ttl = DefaultTokenTTL
			}
			exp := f.now().Add(ttl).UTC().Truncate(time.Second)
			f.tokens[tok] = tokenGrant{installation: id, expires: exp}
			return created{map[string]any{"token": tok, "expires_at": exp.Format(time.RFC3339), "permissions": inst.meta.Permissions, "repository_selection": inst.meta.RepositorySelection}}, nil
		}
	}
	return nil, errNotFound
}

func (f *Fake) routeInstallation(req *http.Request, parts []string, inst *installation, header http.Header) (any, *apiError) {
	if req.Method != http.MethodGet {
		return nil, errNotFound
	}
	switch {
	case len(parts) == 2 && parts[0] == "installation" && parts[1] == "repositories":
		var repos []*Repo
		for id := range inst.repos {
			if r, ok := f.repos[id]; ok {
				repos = append(repos, r)
			}
		}
		sort.Slice(repos, func(i, j int) bool { return repos[i].id < repos[j].id })
		start, end := f.paginate(req, len(repos), header)
		page := make([]ghapp.Repository, 0, end-start)
		for _, r := range repos[start:end] {
			page = append(page, r.meta)
		}
		return map[string]any{"total_count": len(repos), "repository_selection": inst.meta.RepositorySelection, "repositories": page}, nil
	case len(parts) == 2 && parts[0] == "repositories":
		id, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return nil, errNotFound
		}
		r, ok := f.repos[id]
		if !ok || !inst.repos[id] {
			return nil, errNotFound
		}
		return r.meta, nil
	case len(parts) >= 3 && parts[0] == "repos":
		r, ok := f.byName[strings.ToLower(parts[1]+"/"+parts[2])]
		if !ok || !inst.repos[r.id] {
			return nil, errNotFound
		}
		return f.routeRepo(req, r, parts[3:], header)
	}
	return nil, errNotFound
}

func (f *Fake) routeRepo(req *http.Request, r *Repo, rest []string, header http.Header) (any, *apiError) {
	if len(rest) == 0 {
		return r.meta, nil
	}
	switch rest[0] {
	case "branches":
		if len(rest) == 1 {
			names := make([]string, 0, len(r.branches))
			for n := range r.branches {
				names = append(names, n)
			}
			slices.Sort(names)
			start, end := f.paginate(req, len(names), header)
			out := make([]ghapp.Branch, 0, end-start)
			for _, n := range names[start:end] {
				out = append(out, branchOf(n, r.branches[n]))
			}
			return out, nil
		}
		name := strings.Join(rest[1:], "/")
		sha, ok := r.branches[name]
		if !ok {
			return nil, fail(http.StatusNotFound, "Branch not found")
		}
		return branchOf(name, sha), nil
	case "pulls":
		if len(rest) != 1 {
			return nil, errNotFound
		}
		state := req.URL.Query().Get("state")
		if state == "" {
			state = "open"
		}
		pulls := []ghapp.PullRequest{}
		for _, pr := range r.pulls {
			if state == "all" || pr.State == state {
				pulls = append(pulls, *pr)
			}
		}
		start, end := f.paginate(req, len(pulls), header)
		return pulls[start:end:end], nil
	case "compare":
		spec := strings.Join(rest[1:], "/")
		base, head, ok := strings.Cut(spec, "...")
		if !ok {
			return nil, errNotFound
		}
		if cmp, ok := r.compares[spec]; ok {
			return cmp, nil
		}
		return r.compare(base, head)
	case "git":
		if len(rest) < 3 {
			return nil, errNotFound
		}
		switch rest[1] {
		case "trees":
			return r.tree(strings.Join(rest[2:], "/"), req.URL.Query().Get("recursive") != "")
		case "blobs":
			content, ok := r.blobs[rest[2]]
			if !ok || len(rest) != 3 {
				return nil, errNotFound
			}
			return map[string]any{"sha": rest[2], "size": len(content), "encoding": "base64", "content": wrap60(base64.StdEncoding.EncodeToString(content))}, nil
		}
	case "contents":
		return r.contents(strings.Join(rest[1:], "/"), req.URL.Query().Get("ref"))
	}
	return nil, errNotFound
}

func branchOf(name, sha string) ghapp.Branch {
	b := ghapp.Branch{Name: name}
	b.Commit.SHA = sha
	return b
}

func wrap60(s string) string {
	var b strings.Builder
	for len(s) > 60 {
		b.WriteString(s[:60])
		b.WriteByte('\n')
		s = s[60:]
	}
	b.WriteString(s)
	return b.String()
}

// paginate applies per_page and page, sets the Link header and returns the
// slice bounds of the page.
func (f *Fake) paginate(req *http.Request, total int, header http.Header) (int, int) {
	q := req.URL.Query()
	maxPer := 100
	if f.PageSize > 0 && f.PageSize < maxPer {
		maxPer = f.PageSize
	}
	per := 30
	if n, err := strconv.Atoi(q.Get("per_page")); err == nil && n > 0 {
		per = n
	}
	per = min(per, maxPer)
	page := 1
	if n, err := strconv.Atoi(q.Get("page")); err == nil && n > 0 {
		page = n
	}
	start := min((page-1)*per, total)
	end := min(start+per, total)
	if end < total {
		next := url.Values{}
		for k, vs := range q {
			next[k] = vs
		}
		next.Set("page", strconv.Itoa(page+1))
		next.Set("per_page", strconv.Itoa(per))
		lastPage := (total + per - 1) / per
		last := url.Values{}
		for k, vs := range next {
			last[k] = vs
		}
		last.Set("page", strconv.Itoa(lastPage))
		base := "http://" + req.Host + req.URL.Path + "?"
		header.Set("Link", fmt.Sprintf(`<%s%s>; rel="next", <%s%s>; rel="last"`, base, next.Encode(), base, last.Encode()))
	}
	return start, end
}

func (r *Repo) treeLimitOrDefault() int {
	if r.treeLimit > 0 {
		return r.treeLimit
	}
	return r.f.TreeLimit
}

func (r *Repo) tree(ref string, recursive bool) (any, *apiError) {
	sha := ref
	if _, ok := r.trees[sha]; !ok {
		c := r.resolveCommit(ref)
		if c == nil {
			// An empty tree (no entries) has no stored children but exists.
			if ref == gitHash("tree", nil) {
				return ghapp.Tree{SHA: ref, Entries: []ghapp.TreeEntry{}}, nil
			}
			return nil, errNotFound
		}
		sha = c.tree
	}
	out := ghapp.Tree{SHA: sha, Entries: []ghapp.TreeEntry{}}
	var walk func(treeSHA, prefix string)
	walk = func(treeSHA, prefix string) {
		for _, c := range r.trees[treeSHA] {
			p := c.name
			if prefix != "" {
				p = prefix + "/" + c.name
			}
			e := ghapp.TreeEntry{Path: p, Mode: c.mode, Type: c.typ(), SHA: c.sha}
			if e.Type == ghapp.TypeBlob {
				e.Size = int64(len(r.blobs[c.sha]))
			}
			out.Entries = append(out.Entries, e)
			if recursive && c.mode == ghapp.ModeTree {
				walk(c.sha, p)
			}
		}
	}
	walk(sha, "")
	if limit := r.treeLimitOrDefault(); recursive && limit > 0 && len(out.Entries) > limit {
		out.Entries = out.Entries[:limit]
		out.Truncated = true
	}
	return out, nil
}

func (r *Repo) contents(p, ref string) (any, *apiError) {
	if ref == "" {
		ref = r.meta.DefaultBranch
	}
	c := r.resolveCommit(ref)
	if c == nil {
		return nil, fail(http.StatusNotFound, "No commit found for the ref "+ref)
	}
	p = strings.Trim(p, "/")
	treeSHA := c.tree
	var self *treeChild
	if p != "" {
		segs := strings.Split(p, "/")
		for i, seg := range segs {
			var found *treeChild
			for _, ch := range r.trees[treeSHA] {
				if ch.name == seg {
					found = &ch
					break
				}
			}
			if found == nil {
				return nil, errNotFound
			}
			if i < len(segs)-1 {
				if found.mode != ghapp.ModeTree {
					return nil, errNotFound
				}
				treeSHA = found.sha
				continue
			}
			self = found
		}
	}
	if self == nil || self.mode == ghapp.ModeTree {
		if self != nil {
			treeSHA = self.sha
		}
		entries := []ghapp.ContentEntry{}
		for _, ch := range r.trees[treeSHA] {
			ep := ch.name
			if p != "" {
				ep = p + "/" + ch.name
			}
			entries = append(entries, ghapp.ContentEntry{Type: contentType(ch.mode), Name: ch.name, Path: ep, SHA: ch.sha, Size: int64(len(r.blobs[ch.sha]))})
		}
		return entries, nil
	}
	content := r.blobs[self.sha]
	out := map[string]any{"type": contentType(self.mode), "name": self.name, "path": p, "sha": self.sha, "size": len(content)}
	switch self.mode {
	case ghapp.ModeSubmodule:
		out["size"] = 0
	case ghapp.ModeSymlink:
		out["target"] = string(content)
	default:
		if len(content) > maxContentsInline {
			out["encoding"], out["content"] = "none", ""
		} else {
			out["encoding"], out["content"] = "base64", wrap60(base64.StdEncoding.EncodeToString(content))
		}
	}
	return out, nil
}

func contentType(mode string) string {
	switch mode {
	case ghapp.ModeTree:
		return "dir"
	case ghapp.ModeSymlink:
		return "symlink"
	case ghapp.ModeSubmodule:
		return "submodule"
	default:
		return "file"
	}
}

func (r *Repo) compare(baseRef, headRef string) (any, *apiError) {
	base, head := r.resolveCommit(baseRef), r.resolveCommit(headRef)
	if base == nil || head == nil {
		return nil, fail(http.StatusNotFound, "Not Found")
	}
	headAnc, baseAnc := r.ancestors(head.sha), r.ancestors(base.sha)
	// The merge base is the common ancestor nearest to head.
	mergeBase := ""
	best := -1
	for sha, d := range headAnc {
		if _, ok := baseAnc[sha]; ok && (best < 0 || d < best || (d == best && sha < mergeBase)) {
			mergeBase, best = sha, d
		}
	}
	if mergeBase == "" {
		return nil, fail(http.StatusNotFound, "No common ancestor between "+baseRef+" and "+headRef+".")
	}
	var ahead, behind []string
	for sha := range headAnc {
		if _, ok := baseAnc[sha]; !ok {
			ahead = append(ahead, sha)
		}
	}
	for sha := range baseAnc {
		if _, ok := headAnc[sha]; !ok {
			behind = append(behind, sha)
		}
	}
	cmp := ghapp.Comparison{AheadBy: len(ahead), BehindBy: len(behind), TotalCommits: len(ahead), Commits: []ghapp.CompareCommit{}, Files: []ghapp.CompareFile{}}
	switch {
	case len(ahead) == 0 && len(behind) == 0:
		cmp.Status = "identical"
	case len(behind) == 0:
		cmp.Status = "ahead"
	case len(ahead) == 0:
		cmp.Status = "behind"
	default:
		cmp.Status = "diverged"
	}
	cmp.BaseCommit = r.compareCommit(base.sha)
	cmp.MergeBase = r.compareCommit(mergeBase)
	sort.Slice(ahead, func(i, j int) bool { return headAnc[ahead[i]] > headAnc[ahead[j]] })
	if len(ahead) > 250 {
		ahead = ahead[len(ahead)-250:]
	}
	for _, sha := range ahead {
		cmp.Commits = append(cmp.Commits, r.compareCommit(sha))
	}
	before, after := map[string]leaf{}, map[string]leaf{}
	r.flatten(r.commits[mergeBase].tree, "", before)
	r.flatten(head.tree, "", after)
	var removed []string
	for p := range before {
		if _, ok := after[p]; !ok {
			removed = append(removed, p)
		}
	}
	slices.Sort(removed)
	renamedFrom := map[string]string{} // new path → old path
	usedOld := map[string]bool{}
	var added []string
	for p := range after {
		if _, ok := before[p]; !ok {
			added = append(added, p)
		}
	}
	slices.Sort(added)
	for _, p := range added {
		for _, old := range removed {
			if !usedOld[old] && before[old].sha == after[p].sha {
				renamedFrom[p], usedOld[old] = old, true
				break
			}
		}
	}
	for p, l := range after {
		old, existed := before[p]
		switch {
		case !existed && renamedFrom[p] != "":
			cmp.Files = append(cmp.Files, ghapp.CompareFile{SHA: l.sha, Filename: p, Status: ghapp.FileRenamed, PreviousFilename: renamedFrom[p]})
		case !existed:
			cmp.Files = append(cmp.Files, ghapp.CompareFile{SHA: l.sha, Filename: p, Status: ghapp.FileAdded})
		case old != l:
			cmp.Files = append(cmp.Files, ghapp.CompareFile{SHA: l.sha, Filename: p, Status: ghapp.FileModified})
		}
	}
	for _, p := range removed {
		if !usedOld[p] {
			cmp.Files = append(cmp.Files, ghapp.CompareFile{SHA: before[p].sha, Filename: p, Status: ghapp.FileRemoved})
		}
	}
	sort.Slice(cmp.Files, func(i, j int) bool { return cmp.Files[i].Filename < cmp.Files[j].Filename })
	if len(cmp.Files) > ghapp.MaxCompareFiles {
		cmp.Files = cmp.Files[:ghapp.MaxCompareFiles]
	}
	return cmp, nil
}

func (r *Repo) compareCommit(sha string) ghapp.CompareCommit {
	var cc ghapp.CompareCommit
	cc.SHA = sha
	if c := r.commits[sha]; c != nil {
		cc.Commit.Message = c.message
		cc.Commit.Tree.SHA = c.tree
	}
	return cc
}

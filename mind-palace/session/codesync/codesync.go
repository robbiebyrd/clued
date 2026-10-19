package codesync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/clientcfg"
	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/code/remotecode"
	"github.com/robbiebyrd/clued/mind-palace/codefilter"
	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/remote"
	"github.com/robbiebyrd/clued/mind-palace/server/codeapi"
	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/gitinfo"
	"github.com/robbiebyrd/clued/mind-palace/session/hostinfo"
	"github.com/robbiebyrd/clued/mind-palace/session/redact"
)

// The budget of one run (plan 0010, "Budget"): code sync gets what is left
// of the hook relay's 5 seconds, at most DefaultBudget, and reads at most
// DefaultMaxBytes of changed content; the rest stays pending for the next
// run.
const (
	DefaultBudget         = 3 * time.Second
	DefaultMaxBytes int64 = 8 << 20
)

// Touch is the session activity a run records on the worktree.
type Touch int

const (
	// TouchNone records nothing.
	TouchNone Touch = iota
	// TouchStart records the session's activity before the sync
	// (SessionStart).
	TouchStart
	// TouchEnd records the session's end after the sync (SessionEnd).
	TouchEnd
)

// Reasons a run skipped (Report.Skipped).
const (
	SkipNoCapture     = "no capture (" + session.NoCaptureEnv + "=1)"
	SkipDisabled      = "code sync is off"
	SkipNotRepository = "not in a git repository"
	SkipRepoOptOut    = "repository opted out (codeSync: false)"
	SkipExcluded      = "worktree matches excludePaths"
	SkipDirectorySlug = "project has no origin or remote.project (directory-name slug)"
	SkipNoRef         = "no branch and no commit"
	SkipLocked        = "another run holds the worktree's lock"
	SkipBackedOff     = "backed off until a refusal expires"
)

// Request is one sync run. Cwd, SessionID, Paths, Status, Full and Touch
// come from the hook event; the rest is the environment.
type Request struct {
	// Cwd is the event's working directory; the worktree is the one
	// containing it ("" means the process's directory).
	Cwd string
	// SessionID is the Claude Code session the change is attributed to.
	SessionID string
	// Paths are the files the event names (absolute, or relative to Cwd);
	// ones outside the worktree are ignored.
	Paths []string
	// Status adds the paths git status reports changed and the files the
	// state holds as dirty (after a Bash call, Stop): mv, rm, generators
	// and reverts done outside an edit tool.
	Status bool
	// Full forces a full reconcile.
	Full bool
	// Touch records session activity on the worktree.
	Touch Touch
	// Budget bounds the run (DefaultBudget when zero, none when negative);
	// a deadline on ctx bounds it too.
	Budget time.Duration
	// MaxBytes bounds the changed content read and hashed in the run
	// (DefaultMaxBytes when zero, none when negative).
	MaxBytes int64
	// DryRun works out what would be sent without sending it or saving the
	// state.
	DryRun bool

	// Config is the plugin's machine config: remote mode, codeSync and its
	// keys, excludePaths and the redaction rules.
	Config session.Config
	// StateDir holds the state and lock files (StateDir(config dir)).
	StateDir string
	// Host names the machine in the worktree id (the host name when empty).
	Host string
	// Store is the code store of the project; nil opens code/remotecode
	// over Config for the resolved project.
	Store code.Store
	// Log receives a line worth keeping in relay.log (a quota refusal).
	Log func(msg string)
	// Now is the clock (time.Now when nil).
	Now func() time.Time
}

// Report says what a run did.
type Report struct {
	// Skipped is why nothing ran (one of the Skip* reasons), "" otherwise.
	Skipped string
	// The worktree the run resolved.
	Worktree string
	Root     string
	Project  string
	Ref      string
	Head     string
	// Full is true when the run reconciled the whole worktree, and
	// FullCommitted when it committed it as one full change.
	Full          bool
	FullCommitted bool
	// Upserts and Deletes are the entries committed (in a dry run, the
	// entries that would be).
	Upserts int
	Deletes int
	// Hashed files and bytes read; Uploaded blobs and bytes sent.
	Hashed        int
	HashedBytes   int64
	Uploaded      int
	UploadedBytes int64
	// Refused counts the blobs the server refused as too large.
	Refused int
	// GitHub is true when the project is GitHub-backed (capabilities.github,
	// as the last full reconcile read it), and Omitted counts the clean,
	// pushed files the run left to GitHub: neither read, hashed nor
	// uploaded, their entries carry the git object id and size only.
	GitHub  bool
	Omitted int
	// Pending counts the paths left for the next run.
	Pending int
	// Seq is the log position of the last change committed.
	Seq int64
	// BackedOff is the refusal that started a back-off in this run.
	BackedOff string
	// Warnings are settings that could not be applied (a bad glob).
	Warnings []string
}

// Run syncs the worktree containing req.Cwd to the code store (plan 0010,
// "One sync run"): it resolves the worktree, decides between a full
// reconcile and the event's paths, re-hashes only the files whose stamp
// changed, fills the git facts, uploads the blobs the server lacks and
// commits the change, within the budget. What does not fit stays pending
// in the state. A refusal of the feature (404 on the prefix, 403, 413
// quota) backs off for 24 hours and is not an error; a network or server
// failure is returned with the work left pending.
func Run(ctx context.Context, req Request) (Report, error) {
	var rep Report
	if session.NoCapture() {
		rep.Skipped = SkipNoCapture
		return rep, nil
	}
	if !req.Config.CodeSyncEnabled() {
		rep.Skipped = SkipDisabled
		return rep, nil
	}
	if req.StateDir == "" {
		return rep, errors.New("codesync: no state directory")
	}
	start := time.Now()
	budget := req.Budget
	if budget == 0 {
		budget = DefaultBudget
	}
	if budget > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, budget)
		defer cancel()
	}

	t, skip, err := resolve(ctx, req, &rep)
	if err != nil || skip != "" {
		rep.Skipped = skip
		return rep, err
	}
	rep.Worktree, rep.Root, rep.Project, rep.Ref, rep.Head = t.id, t.root, t.project, t.ref, t.head

	release, held, err := AcquireLock(req.StateDir, t.id)
	if err != nil {
		return rep, err
	}
	defer release()
	if held {
		rep.Skipped = SkipLocked
		return rep, nil
	}
	st, err := LoadState(req.StateDir, t.id)
	if err != nil {
		// A state file that cannot be read starts over: a full reconcile
		// rebuilds it.
		rep.Warnings = append(rep.Warnings, err.Error())
		st = State{}
	}
	now := req.now()
	if st.BackedOff(now) {
		rep.Skipped = SkipBackedOff
		return rep, nil
	}

	r := &run{req: req, t: t, st: &st, rep: &rep, now: now, deadline: softDeadline(ctx, start)}
	err = r.sync(ctx)
	if req.DryRun {
		return rep, err
	}
	if r.queued {
		// Every path whose facts this run computed is either committed with
		// them or queued, and a queued path's facts are recomputed, so the
		// next run may start from this run's identity.
		st.Worktree, st.Root, st.Project = t.id, t.root, t.project
		st.DefaultBranch, st.Ref, st.Head = t.defaultBranch, t.ref, t.head
		st.Upstream, st.Ahead = t.upstream, t.ahead
	}
	rep.Pending = len(st.Pending.Upserts) + len(st.Pending.Deletes)
	if serr := SaveState(req.StateDir, t.id, st); serr != nil {
		err = errors.Join(err, serr)
	}
	return rep, err
}

// Remove drops the worktree at root (WorktreeRemove): DELETE
// worktrees/{id} on the server, then the local state. The worktree's
// directory may already be gone, so the project comes from the state; a
// worktree the state does not know has nothing on the server.
func Remove(ctx context.Context, req Request, root string) error {
	if session.NoCapture() || !req.Config.CodeSyncEnabled() {
		return nil
	}
	if req.StateDir == "" {
		return errors.New("codesync: no state directory")
	}
	if !filepath.IsAbs(root) {
		return fmt.Errorf("codesync: worktree root %q is not absolute", root)
	}
	id := WorktreeID(req.host(), filepath.Clean(root))
	release, held, err := AcquireLock(req.StateDir, id)
	if err != nil {
		return err
	}
	defer release()
	if held {
		return nil
	}
	st, err := LoadState(req.StateDir, id)
	if err == nil && st.Project != "" && !req.DryRun {
		store, oerr := req.open(st.Project)
		if oerr != nil {
			return oerr
		}
		if rerr := store.RemoveWorktree(ctx, id); rerr != nil && !errors.Is(rerr, code.ErrNotFound) &&
			!errors.Is(rerr, remotecode.ErrUnsupported) && !errors.Is(rerr, remotecode.ErrForbidden) {
			return rerr
		}
	}
	if req.DryRun {
		return nil
	}
	return RemoveState(req.StateDir, id)
}

func (req Request) now() time.Time {
	if req.Now != nil {
		return req.Now()
	}
	return time.Now()
}

func (req Request) host() string {
	if req.Host != "" {
		return req.Host
	}
	return hostinfo.Read().Hostname
}

func (req Request) log(msg string) {
	if req.Log != nil {
		req.Log(msg)
	}
}

// open returns req.Store, or the remote code store of project over the
// config's server.
func (req Request) open(project string) (code.Store, error) {
	if req.Store != nil {
		return req.Store, nil
	}
	client, err := remote.New(req.Config)
	if err != nil {
		return nil, err
	}
	return remotecode.New(client, project)
}

// softDeadline is when a run stops starting new work (hashing, uploads), a
// quarter of its time before the hard deadline so the commit still fits.
// Zero means no deadline.
func softDeadline(ctx context.Context, start time.Time) time.Time {
	d, ok := ctx.Deadline()
	if !ok {
		return time.Time{}
	}
	return start.Add(d.Sub(start) * 3 / 4)
}

// target is the worktree a run resolved.
type target struct {
	root, id, host  string
	ref, head       string
	upstream        string
	ahead           int
	defaultBranch   string
	project, origin string
	excludes        redact.Paths
	codeExcludes    codefilter.Excludes
	redactor        *redact.Redactor
	maxFileBytes    int64
	sessionID       string
}

// resolve finds the worktree, its ref, HEAD, upstream, default branch and
// project, and the filters that apply to it; skip says why it is not
// synced.
func resolve(ctx context.Context, req Request, rep *Report) (target, string, error) {
	var t target
	cwd := req.Cwd
	if cwd == "" {
		wd, err := os.Getwd()
		if err != nil {
			return t, "", err
		}
		cwd = wd
	}
	t.root = gitinfo.Root(ctx, cwd)
	if t.root == "" {
		return t, SkipNotRepository, nil
	}
	t.root = filepath.Clean(t.root)
	paths, errs := redact.NewPaths(req.Config.ExcludePaths)
	for _, e := range errs {
		rep.Warnings = append(rep.Warnings, e.Error())
	}
	if paths.Excludes(t.root) {
		return t, SkipExcluded, nil
	}
	t.excludes = paths
	repoCfg, err := loadRepoConfig(t.root)
	if err != nil {
		// The repository's opt-out cannot be read: fail closed.
		return t, "", err
	}
	if !repoCfg.CodeSyncAllowed() {
		return t, SkipRepoOptOut, nil
	}
	proj := clientcfg.ResolveProject(ctx, t.root, repoCfg.Remote.Project)
	if proj.Source == clientcfg.ProjectFromDirectory {
		return t, SkipDirectorySlug, nil
	}
	t.project = proj.Slug
	t.origin = proj.Origin
	if t.origin == "" {
		t.origin = gitinfo.Origin(ctx, t.root)
	}
	t.origin = withoutUserinfo(t.origin)

	t.head = gitinfo.Head(ctx, t.root)
	t.ref = gitinfo.Branch(ctx, t.root)
	if t.ref == "" {
		if len(t.head) < 12 {
			return t, SkipNoRef, nil
		}
		t.ref = "detached@" + t.head[:12]
	}
	if !code.ValidRef(t.ref) {
		return t, SkipNoRef, nil
	}
	t.upstream, t.ahead, _ = gitinfo.Upstream(ctx, t.root)
	t.defaultBranch = gitinfo.DefaultBranch(ctx, t.root)
	if t.defaultBranch != "" && !code.ValidRef(t.defaultBranch) {
		t.defaultBranch = ""
	}
	t.host = req.host()
	t.id = WorktreeID(t.host, t.root)

	t.codeExcludes, errs = codefilter.NewExcludes(req.Config.CodeSyncExclude)
	for _, e := range errs {
		rep.Warnings = append(rep.Warnings, e.Error())
	}
	if req.Config.RedactsAtCapture() {
		t.redactor, errs = redact.New(req.Config.RedactPatterns)
		for _, e := range errs {
			rep.Warnings = append(rep.Warnings, e.Error())
		}
	}
	t.maxFileBytes = req.Config.CodeSyncMaxFileBytes
	if t.maxFileBytes <= 0 {
		t.maxFileBytes = session.DefaultCodeSyncMaxFileBytes
	}
	if validSessionID(req.SessionID) {
		t.sessionID = req.SessionID
	}
	return t, "", nil
}

// loadRepoConfig reads the repository's mind-palace config at its root
// (defaults when there is none).
func loadRepoConfig(root string) (*config.Config, error) {
	for _, name := range config.DefaultConfigFiles {
		p := filepath.Join(root, name)
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
			return config.Load(p)
		}
	}
	return config.Default(), nil
}

// withoutUserinfo drops credentials from an origin URL
// (https://user:token@host/…); other forms are kept as they are.
func withoutUserinfo(origin string) string {
	u, err := url.Parse(origin)
	if err != nil || u.User == nil || u.Host == "" {
		return origin
	}
	u.User = nil
	return u.String()
}

// validSessionID accepts what the server accepts as a session id: 1 to 128
// letters, digits, '-', '_' and '.'. Another id is not sent.
func validSessionID(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' && c != '.' {
			return false
		}
	}
	return true
}

// run is one sync in progress.
type run struct {
	req      Request
	t        target
	st       *State
	rep      *Report
	now      time.Time
	deadline time.Time

	store code.Store
	git   *gitFacts
	opts  codefilter.Options
	batch int
	// github: clean, pushed entries are sent without content (plan 0011,
	// "Clients on a GitHub-backed project").
	github bool

	headMoved, upMoved bool
	hashedBytes        int64
	// pending are the paths the state had queued when the run started.
	pending map[string]bool
	// queued is set once the run's work is in the pending queue.
	queued bool
}

// upsert is one entry to commit, with the state it leaves once committed
// and, when it was hashed in this run, the content to upload.
type upsert struct {
	entry   code.Entry
	file    FileState
	content []byte
}

func (r *run) pastDeadline() bool {
	return !r.deadline.IsZero() && time.Now().After(r.deadline)
}

// sync does the run's work. Whatever it does not commit (the budget, a
// back-off, a conflict, a failure) stays in the pending queue.
func (r *run) sync(ctx context.Context) error {
	st, t := r.st, r.t
	refChanged := st.Worktree != t.id || st.Ref != t.ref || st.Root != t.root
	projectChanged := st.Project != t.project
	if projectChanged {
		// Another namespace on the server: nothing there is known yet.
		for p, f := range st.Files {
			f.Synced = false
			st.Files[p] = f
		}
	}
	if refChanged || projectChanged {
		st.LastFull = time.Time{}
	}
	full := r.req.Full || refChanged || projectChanged || st.FullDue(r.now)
	r.headMoved = refChanged || projectChanged || st.Head != t.head
	r.upMoved = r.headMoved || st.Upstream != t.upstream
	enumerate := full || r.headMoved
	r.rep.Full = full
	if st.Files == nil {
		st.Files = map[string]FileState{}
	}

	r.batch = codeapi.DefaultMaxBatch
	r.opts = codefilter.Options{Root: t.root, MaxFileBytes: t.maxFileBytes, Exclude: t.codeExcludes}
	if len(t.excludes) > 0 {
		r.opts.ExcludePaths = t.excludes
	}
	if !r.req.DryRun {
		var err error
		if r.store, err = r.req.open(t.project); err != nil {
			return err
		}
		c, ok := r.store.(interface {
			Capabilities(context.Context) (codeapi.Capabilities, error)
		})
		switch {
		case ok && (full || refChanged):
			caps, err := c.Capabilities(ctx)
			if err != nil {
				return r.refusal(err)
			}
			if m := caps.Code.MaxFileBytes; m > 0 && m < r.opts.MaxFileBytes {
				r.opts.MaxFileBytes = m
			}
			if caps.Code.MaxBatch > 0 {
				r.batch = caps.Code.MaxBatch
			}
			r.setGitHub(caps.GitHub)
		case !ok && full:
			// A store without capabilities gets no code from GitHub.
			r.setGitHub(false)
		}
	}
	r.github = st.GitHub
	r.rep.GitHub = r.github

	// The paths to look at.
	r.git = &gitFacts{root: t.root, upstream: t.upstream}
	if err := r.git.loadStatus(ctx); err != nil {
		return err
	}
	cand := candidates{}
	cand.add(st.Pending.Upserts...)
	cand.add(st.Pending.Deletes...)
	for _, p := range r.req.Paths {
		if rel, ok := eventRel(t.root, r.req.Cwd, p); ok {
			cand[rel] = struct{}{}
		}
	}
	if enumerate {
		list, err := r.git.listFiles(ctx)
		if err != nil {
			return err
		}
		cand.add(list...)
	}
	if enumerate || r.upMoved {
		for p := range st.Files {
			cand.add(p)
		}
	}
	if r.req.Status {
		cand.add(r.git.statusPaths...)
		for p, f := range st.Files {
			if f.Dirty {
				cand.add(p)
			}
		}
	}

	// Stat, filter, hash and fill the git facts of each.
	var ups []upsert
	var dels, leftover []string
	var all []code.Entry
	pending := map[string]bool{}
	for _, p := range st.Pending.Upserts {
		pending[p] = true
	}
	for _, p := range st.Pending.Deletes {
		pending[p] = true
	}
	r.pending = pending
	order := cand.sorted()
	for _, rel := range order {
		disk, present := statFile(t.root, rel)
		if present {
			var err error
			if present, err = r.git.tracked(ctx, rel, st); err != nil {
				return err
			}
		}
		dec := codefilter.Skip
		if present {
			dec, _ = codefilter.Decide(rel, disk.info.Size(), nil, r.opts)
			present = dec != codefilter.Skip
		}
		if !present {
			if _, known := st.Files[rel]; known || pending[rel] {
				dels = append(dels, rel)
			}
			continue
		}
		u, deferred, err := r.entry(ctx, rel, disk, dec)
		if err != nil {
			return err
		}
		if deferred {
			leftover = append(leftover, rel)
			continue
		}
		if full {
			all = append(all, u.entry)
		}
		old, known := st.Files[rel]
		if known && old.Synced && old.SHA256 == u.file.SHA256 && old.GitOID == u.file.GitOID &&
			old.Dirty == u.file.Dirty && old.Unpushed == u.file.Unpushed && old.Size == u.file.Size {
			// Unchanged on the server; keep the fresh stamp.
			u.file.Synced = true
			st.Files[rel] = u.file
			continue
		}
		ups = append(ups, u)
	}

	// Queue everything this run owes; a commit dequeues what it sent.
	for _, rel := range order {
		st.Dequeue(rel)
	}
	for _, rel := range leftover {
		st.QueueUpsert(rel)
	}
	for _, u := range ups {
		st.QueueUpsert(u.entry.Path)
	}
	for _, rel := range dels {
		st.QueueDelete(rel)
	}
	r.queued = !r.req.DryRun

	if r.req.DryRun {
		if full && len(leftover) == 0 {
			r.rep.Upserts, r.rep.Deletes = len(all), len(dels)
		} else {
			r.rep.Upserts, r.rep.Deletes = len(ups), len(dels)
		}
		r.rep.Pending = len(leftover)
		return nil
	}

	if refChanged || full {
		if err := r.putWorktree(ctx); err != nil {
			return r.refusal(err)
		}
	}
	if r.req.Touch == TouchStart && t.sessionID != "" {
		if err := r.withWorktree(ctx, func() error { return r.store.TouchSession(ctx, t.id, t.sessionID, false) }); err != nil {
			return r.refusal(err)
		}
	}

	ups, unsent, stopped, err := r.upload(ctx, ups)
	if err != nil || stopped {
		return err
	}

	fullCommit := full && len(leftover) == 0 && unsent == 0
	if done, err := r.commit(ctx, ups, dels, all, fullCommit); err != nil || !done {
		return err
	}
	if fullCommit {
		st.LastFull = r.now
		r.rep.FullCommitted = true
	}
	if r.req.Touch == TouchEnd && t.sessionID != "" {
		if err := r.withWorktree(ctx, func() error { return r.store.TouchSession(ctx, t.id, t.sessionID, true) }); err != nil {
			return r.refusal(err)
		}
	}
	return nil
}

// setGitHub records the project's capabilities.github, read on a full
// reconcile. When it turns false, the entries sent without content because
// GitHub supplied it lose their stamp and synced mark, so this reconcile
// reads, uploads and commits them again.
func (r *run) setGitHub(on bool) {
	st := r.st
	if st.GitHub && !on {
		for p, f := range st.Files {
			if omitted(f) {
				f.MTime, f.Synced = time.Time{}, false
				st.Files[p] = f
			}
		}
	}
	st.GitHub = on
}

// omitted reports whether f, on a GitHub-backed project, is an entry sent
// without content because GitHub supplies it: clean, pushed and in git.
func omitted(f FileState) bool {
	return f.SHA256 == "" && f.GitOID != "" && !f.Dirty && !f.Unpushed
}

// entry builds the tree entry of rel: its content hash (reused from the
// state when the stamp is unchanged, else read, filtered, redacted and
// hashed) and its git facts. On a GitHub-backed project a clean, pushed
// file is neither read nor hashed: its entry carries the git object id and
// size only. deferred is true when the budget is spent and rel waits for
// the next run.
func (r *run) entry(ctx context.Context, rel string, disk onDisk, dec codefilter.Decision) (upsert, bool, error) {
	st := r.st
	info := disk.info
	size, mtime := info.Size(), info.ModTime()
	f := FileState{Size: size, MTime: mtime}
	old, known := st.Files[rel]
	if r.github {
		if err := r.gitFactsOf(ctx, rel, old, known, &f); err != nil {
			return upsert{}, false, err
		}
		if omitted(f) {
			r.rep.Omitted++
			return r.upsertOf(rel, disk, f, nil), false, nil
		}
	}
	var content []byte
	switch {
	case disk.mode == "120000" || dec != codefilter.Content:
		// A symbolic link (never followed) or a file over the size cap: its
		// path and size only.
	case st.IsRefused(rel, Stamp(size, mtime)):
		// The server refused this very version as too large.
	case known && old.Size == size && old.MTime.Equal(mtime) && (old.Synced || old.SHA256 == "") &&
		!(r.github && omitted(old)):
		// (An entry GitHub supplied has no hash to reuse: now dirty or
		// unpushed, it is read.)
		f.SHA256 = old.SHA256
	default:
		if r.pastDeadline() || (r.maxBytes() > 0 && r.hashedBytes > 0 && r.hashedBytes+size > r.maxBytes()) {
			return upsert{}, true, nil
		}
		data, err := os.ReadFile(filepath.Join(r.t.root, filepath.FromSlash(rel)))
		if err != nil {
			// Gone or unreadable since the stat: try again next run.
			return upsert{}, true, nil
		}
		r.hashedBytes += int64(len(data))
		r.rep.Hashed++
		r.rep.HashedBytes += int64(len(data))
		if d, _ := codefilter.Decide(rel, int64(len(data)), data, r.opts); d == codefilter.Content {
			content = data
			if r.t.redactor != nil {
				if masked, fired := r.t.redactor.String(string(data)); len(fired) > 0 {
					content = []byte(masked)
				}
			}
			sum := sha256.Sum256(content)
			f.SHA256 = hex.EncodeToString(sum[:])
		}
	}

	if !r.github {
		if err := r.gitFactsOf(ctx, rel, old, known, &f); err != nil {
			return upsert{}, false, err
		}
	}
	return r.upsertOf(rel, disk, f, content), false, nil
}

// gitFactsOf fills f's git object id, dirty and unpushed flags, reusing the
// state's (old) when nothing they depend on moved.
func (r *run) gitFactsOf(ctx context.Context, rel string, old FileState, known bool, f *FileState) error {
	var err error
	// The state's facts of a queued path may predate a commit or a push
	// the run that queued it saw; they are read again.
	cached := known && !r.pending[rel]
	if cached && !r.headMoved {
		f.GitOID = old.GitOID
	} else if f.GitOID, err = r.git.gitOID(ctx, rel); err != nil {
		return err
	}
	f.Dirty = r.git.dirty(rel) || f.GitOID == ""
	if cached && !r.upMoved {
		f.Unpushed = old.Unpushed
	} else if f.Unpushed, err = r.git.isUnpushed(ctx, rel); err != nil {
		return err
	}
	if r.t.upstream == "" {
		f.Unpushed = true
	}
	return nil
}

// upsertOf is the upsert of rel with the facts f and the content to upload.
func (r *run) upsertOf(rel string, disk onDisk, f FileState, content []byte) upsert {
	return upsert{
		entry: code.Entry{
			Path:     rel,
			SHA256:   f.SHA256,
			Size:     f.Size,
			Mode:     disk.mode,
			Lang:     session.ExtToLang(path.Base(rel)),
			GitOID:   f.GitOID,
			Dirty:    f.Dirty,
			Unpushed: f.Unpushed,
		},
		file:    f,
		content: content,
	}
}

func (r *run) maxBytes() int64 {
	switch {
	case r.req.MaxBytes < 0:
		return 0
	case r.req.MaxBytes == 0:
		return DefaultMaxBytes
	}
	return r.req.MaxBytes
}

// upload sends the blobs hashed in this run that the server lacks. It
// returns the upserts to commit: a blob refused as too large is committed
// with sha256 "" and its path refused until it changes; the paths of a blob
// the deadline left unsent stay pending (unsent counts them). stopped is
// true after a back-off.
func (r *run) upload(ctx context.Context, ups []upsert) (kept []upsert, unsent int, stopped bool, err error) {
	var shas []string
	seen := map[string]bool{}
	for _, u := range ups {
		if u.content != nil && !seen[u.entry.SHA256] {
			seen[u.entry.SHA256] = true
			shas = append(shas, u.entry.SHA256)
		}
	}
	missing := map[string]bool{}
	for i := 0; i < len(shas); i += r.batch {
		got, err := r.store.MissingBlobs(ctx, shas[i:min(i+r.batch, len(shas))])
		if err != nil {
			err = r.refusal(err)
			return nil, 0, true, err
		}
		for _, s := range got {
			missing[s] = true
		}
	}
	tooLarge := map[string]bool{}
	notSent := map[string]bool{}
	for _, u := range ups {
		sha := u.entry.SHA256
		if u.content == nil || !missing[sha] {
			continue
		}
		missing[sha] = false
		if r.pastDeadline() {
			notSent[sha] = true
			continue
		}
		err := r.store.PutBlob(ctx, code.Blob{SHA256: sha, Size: int64(len(u.content)), Content: u.content})
		switch {
		case err == nil:
			r.rep.Uploaded++
			r.rep.UploadedBytes += int64(len(u.content))
		case errors.Is(err, remotecode.ErrTooLarge):
			tooLarge[sha] = true
			r.rep.Refused++
		default:
			err = r.refusal(err)
			return nil, 0, true, err
		}
	}
	kept = ups[:0]
	for _, u := range ups {
		switch sha := u.entry.SHA256; {
		case notSent[sha]:
			unsent++
			continue // stays pending
		case tooLarge[sha]:
			r.st.Refuse(u.entry.Path, Stamp(u.file.Size, u.file.MTime))
			u.entry.SHA256, u.file.SHA256 = "", ""
		}
		u.content = nil
		kept = append(kept, u)
	}
	return kept, unsent, false, nil
}

// commit sends the change in batches of at most r.batch entries: on a
// full commit every entry of the worktree with full set on the first batch
// (the server drops the paths it no longer sees), otherwise the upserts and
// deletes of the run. done is false when the run stopped (a conflict or a
// back-off).
func (r *run) commit(ctx context.Context, ups []upsert, dels []string, all []code.Entry, full bool) (bool, error) {
	st, t := r.st, r.t
	byPath := make(map[string]upsert, len(ups))
	for _, u := range ups {
		byPath[u.entry.Path] = u
	}
	entries := make([]code.Entry, 0, len(ups))
	deletes := dels
	if full {
		for _, e := range all {
			if u, ok := byPath[e.Path]; ok {
				e = u.entry
			}
			entries = append(entries, e)
		}
		deletes = nil
	} else {
		for _, u := range ups {
			entries = append(entries, u.entry)
		}
	}
	if len(entries)+len(deletes) == 0 && !full {
		return true, nil
	}
	total := len(entries) + len(deletes)
	for i := 0; i == 0 || i < total; i += r.batch {
		end := min(i+r.batch, total)
		c := code.Change{
			Worktree:  t.id,
			Ref:       t.ref,
			Head:      t.head,
			Upstream:  t.upstream,
			Ahead:     t.ahead,
			SessionID: t.sessionID,
			Full:      full && i == 0,
		}
		for j := i; j < end; j++ {
			if j < len(entries) {
				c.Upserts = append(c.Upserts, entries[j])
			} else {
				c.Deletes = append(c.Deletes, deletes[j-len(entries)])
			}
		}
		var seq int64
		err := r.withWorktree(ctx, func() error {
			var err error
			seq, err = r.store.Commit(ctx, c)
			return err
		})
		if errors.Is(err, remotecode.ErrConflict) || errors.Is(err, code.ErrMissingBlob) {
			r.conflict(ctx, c.Upserts)
			return false, nil
		}
		if err != nil {
			err = r.refusal(err)
			return false, err
		}
		r.rep.Seq = seq
		for _, e := range c.Upserts {
			if u, ok := byPath[e.Path]; ok {
				u.file.Synced = true
				st.Files[e.Path] = u.file
				st.Dequeue(e.Path)
				r.rep.Upserts++
			} else if full {
				r.rep.Upserts++
			}
		}
		for _, p := range c.Deletes {
			delete(st.Files, p)
			st.Dequeue(p)
			r.rep.Deletes++
		}
	}
	if full {
		// The full change dropped every path it did not list.
		for _, p := range dels {
			delete(st.Files, p)
			st.Dequeue(p)
			r.rep.Deletes++
		}
	}
	return true, nil
}

// conflict handles a 409: the server lacks a blob a commit named (after a
// purge, say). The paths whose blob is missing lose their synced mark, so
// the next run reads and uploads them again; when the server cannot say
// which, every path of the batch does.
func (r *run) conflict(ctx context.Context, entries []code.Entry) {
	var shas []string
	for _, e := range entries {
		if e.SHA256 != "" {
			shas = append(shas, e.SHA256)
		}
	}
	missing := map[string]bool{}
	got, err := r.store.MissingBlobs(ctx, shas)
	for _, s := range got {
		missing[s] = true
	}
	for _, e := range entries {
		if e.SHA256 == "" || (err == nil && !missing[e.SHA256]) {
			continue
		}
		if f, ok := r.st.Files[e.Path]; ok {
			f.Synced = false
			r.st.Files[e.Path] = f
		}
		r.st.QueueUpsert(e.Path)
	}
}

// putWorktree creates or updates the worktree on the server with the
// current ref.
func (r *run) putWorktree(ctx context.Context) error {
	t := r.t
	return r.store.PutWorktree(ctx, code.Worktree{
		ID:            t.id,
		Root:          t.root,
		Host:          t.host,
		Origin:        t.origin,
		DefaultBranch: t.defaultBranch,
		Refs:          []string{t.ref},
	})
}

// withWorktree runs fn and, when the server does not know the worktree
// (purged, or removed by another run), registers it and runs fn once more.
func (r *run) withWorktree(ctx context.Context, fn func() error) error {
	err := fn()
	if !errors.Is(err, code.ErrNotFound) || errors.Is(err, remotecode.ErrUnsupported) {
		return err
	}
	if err := r.putWorktree(ctx); err != nil {
		return err
	}
	return fn()
}

// refusal handles a failed request: the server refusing the feature (no
// code transport, no entitlement, quota full) starts a back-off and ends
// the run quietly, with one relay.log line for the quota; anything else is
// returned and the work stays pending.
func (r *run) refusal(err error) error {
	var reason string
	switch {
	case errors.Is(err, remotecode.ErrQuota):
		reason = "quota"
		r.req.log(fmt.Sprintf("code sync: the server's code quota is full; worktree %s backs off until %s",
			r.t.id, r.now.Add(BackoffFor).UTC().Format(time.RFC3339)))
	case errors.Is(err, remotecode.ErrForbidden):
		reason = "forbidden"
	case errors.Is(err, remotecode.ErrUnsupported):
		reason = "unsupported"
	default:
		return err
	}
	r.st.BackOff(r.now)
	r.rep.BackedOff = reason
	return nil
}

// String renders a report as one line, for relay.log and the CLI.
func (rep Report) String() string {
	if rep.Skipped != "" {
		return "code sync skipped: " + rep.Skipped
	}
	var b strings.Builder
	fmt.Fprintf(&b, "code sync %s@%s: %d upserts, %d deletes, %d uploaded (%d bytes), %d pending",
		rep.Worktree, rep.Ref, rep.Upserts, rep.Deletes, rep.Uploaded, rep.UploadedBytes, rep.Pending)
	if rep.FullCommitted {
		b.WriteString(", full")
	}
	if rep.GitHub {
		fmt.Fprintf(&b, ", %d left to GitHub", rep.Omitted)
	}
	if rep.BackedOff != "" {
		b.WriteString(", backed off (" + rep.BackedOff + ")")
	}
	return b.String()
}

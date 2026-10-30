package ghsync

// This file drives the sync: Handle applies one queued delivery (code sync,
// the plans and stories of the default branch, the activity log), Work runs
// the job queue, and Reconcile queues the nightly reconcile and the daily
// inactivity sweep of every claimed repository.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/ghapp"
)

// ErrBadDelivery is a delivery Handle cannot read (an unknown event, a
// payload that does not decode); a retry cannot fix it, so Work finishes
// the job.
var ErrBadDelivery = errors.New("ghsync: unreadable delivery")

// maxPayloadCommits is how many commits GitHub lists in a push payload; a
// push with that many may have more, so its file list is not complete.
const maxPayloadCommits = 20

// Handle applies one delivery: a push, a branch created or deleted, a pull
// request opened or closed, or a repository renamed updates the claimed
// target's GitHub-backed worktree under the branch policy; a push to the
// default branch that touches the plans or stories directories (or the
// mind-palace config) syncs them; an installation created, or repositories
// added to one, queues their imports; and every delivery about a
// repository records its activity events. A delivery for an unclaimed
// installation or repository does nothing. The error is for what a retry
// can fix (or a rate limit, which ghapp.RetryAfter reads).
func (s *Service) Handle(ctx context.Context, d Delivery) error {
	ev, err := deliveryEvent(d)
	if err != nil {
		return err
	}
	switch e := ev.(type) {
	case *ghapp.PingEvent:
		return nil
	case *ghapp.InstallationEvent:
		switch e.Action {
		case "created", "unsuspend", "new_permissions_accepted":
			return s.enqueueImports(ctx, d.Installation, e.Repositories)
		}
		return nil
	case *ghapp.InstallationRepositoriesEvent:
		if e.Action == "added" {
			return s.enqueueImports(ctx, d.Installation, e.RepositoriesAdded)
		}
		return nil
	}
	repoID := d.Repository
	if repoID == 0 {
		repoID = ev.RepositoryID()
	}
	if repoID == 0 {
		return nil
	}
	var meta *ghapp.Repository
	if env := envelopeOf(ev); env != nil {
		meta = env.Repository
	}
	r, err := s.openRun(ctx, d.Installation, repoID, meta)
	if err != nil {
		if errors.Is(err, ErrNotClaimed) {
			return nil
		}
		return err
	}
	r.delivery = d.ID
	at := d.ReceivedAt
	if at.IsZero() {
		at = s.now()
	}
	switch e := ev.(type) {
	case *ghapp.PushEvent:
		err = r.handlePush(ctx, e, at)
	case *ghapp.CreateEvent:
		err = r.handleCreate(ctx, e, at)
	case *ghapp.DeleteEvent:
		if e.RefType == "branch" {
			err = r.deleteBranch(ctx, strings.TrimPrefix(e.Ref, ghapp.BranchRefPrefix))
		}
	case *ghapp.PullRequestEvent:
		err = r.handlePullRequest(ctx, d, e)
	case *ghapp.RepositoryEvent:
		err = r.handleRepository(ctx, e)
	}
	if err != nil {
		return err
	}
	if err := r.loadConfig(ctx, ""); err != nil {
		return err
	}
	store, err := r.openStore(ctx, false)
	if err != nil {
		return err
	}
	if _, err := AppendActivity(ctx, store, d, ev, s.now()); err != nil {
		return err
	}
	return r.save(ctx)
}

// deliveryEvent decodes the delivery's payload, or, for a push, create or
// delete queued without one, rebuilds it from the delivery's fields.
func deliveryEvent(d Delivery) (ghapp.Event, error) {
	if len(d.Payload) > 0 {
		ev, err := ghapp.ParseEvent(d.Event, d.Payload)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrBadDelivery, err)
		}
		return ev, nil
	}
	env := ghapp.Envelope{Action: d.Action, Installation: &ghapp.InstallationRef{ID: d.Installation}}
	if d.Repository != 0 {
		env.Repository = &ghapp.Repository{ID: d.Repository, FullName: d.RepositoryName}
	}
	refType := func() string {
		if strings.HasPrefix(d.Ref, ghapp.TagRefPrefix) {
			return "tag"
		}
		return "branch"
	}
	switch d.Event {
	case ghapp.EventPush:
		return &ghapp.PushEvent{Envelope: env, Ref: d.Ref, Before: d.Before, After: d.After, Created: zeroSHA(d.Before), Deleted: zeroSHA(d.After), Pusher: ghapp.Pusher{Name: d.Sender}}, nil
	case ghapp.EventCreate:
		return &ghapp.CreateEvent{Envelope: env, Ref: strings.TrimPrefix(d.Ref, ghapp.BranchRefPrefix), RefType: refType()}, nil
	case ghapp.EventDelete:
		return &ghapp.DeleteEvent{Envelope: env, Ref: strings.TrimPrefix(d.Ref, ghapp.BranchRefPrefix), RefType: refType()}, nil
	}
	return nil, fmt.Errorf("%w: %s delivery without a payload", ErrBadDelivery, d.Event)
}

// envelopeOf returns the shared envelope of a repository delivery.
func envelopeOf(ev ghapp.Event) *ghapp.Envelope {
	switch e := ev.(type) {
	case *ghapp.PushEvent:
		return &e.Envelope
	case *ghapp.CreateEvent:
		return &e.Envelope
	case *ghapp.DeleteEvent:
		return &e.Envelope
	case *ghapp.PullRequestEvent:
		return &e.Envelope
	case *ghapp.CheckSuiteEvent:
		return &e.Envelope
	case *ghapp.CheckRunEvent:
		return &e.Envelope
	case *ghapp.StatusEvent:
		return &e.Envelope
	case *ghapp.WorkflowRunEvent:
		return &e.Envelope
	case *ghapp.RepositoryEvent:
		return &e.Envelope
	}
	return nil
}

// handlePush applies a push to a branch (tags are ignored): a deleted
// branch loses its tree, the code follows under the before-tip rule, and a
// push to the default branch re-reads the config and syncs the plans and
// stories when it touches them.
func (r *repoRun) handlePush(ctx context.Context, e *ghapp.PushEvent, at time.Time) error {
	branch, ok := e.Branch()
	if !ok || branch == "" {
		return nil
	}
	if e.Deleted || zeroSHA(e.After) {
		return r.deleteBranch(ctx, branch)
	}
	r.st.branch(branch).LastPush = at
	isDefault := branch == r.st.DefaultBranch
	cfgRef := ""
	if isDefault {
		cfgRef = e.After
	}
	if err := r.loadConfig(ctx, cfgRef); err != nil {
		return err
	}
	res, err := r.syncPushCode(ctx, branch, e.Before, e.After, e.Forced)
	if err != nil {
		return err
	}
	if err := r.save(ctx); err != nil {
		return err
	}
	if !isDefault || r.st.DocsCommit == e.After {
		return nil
	}
	changed, complete := res.changed, res.compared
	if !complete {
		changed, complete = pushChanged(e)
	}
	contiguous := r.st.DocsCommit != "" && r.st.DocsCommit == e.Before
	if complete && contiguous && !DocsTouched(changed, r.st.DocsDirs...) {
		r.st.DocsCommit = e.After
		return nil
	}
	before := r.st.DocsCommit
	if before == "" {
		before = e.Before
	}
	return r.syncDocs(ctx, e.After, before)
}

// pushChanged lists the paths a push payload's commits touched, and
// whether that list is complete (not a force push, a new branch or a push
// with more commits than the payload lists).
func pushChanged(e *ghapp.PushEvent) ([]string, bool) {
	if e.Forced || e.Created || len(e.Commits) == 0 || len(e.Commits) >= maxPayloadCommits {
		return nil, false
	}
	var out []string
	for _, c := range e.Commits {
		out = append(out, c.Added...)
		out = append(out, c.Modified...)
		out = append(out, c.Removed...)
	}
	return out, true
}

// handleCreate imports a new branch the policy selects (a push with an
// all-zero before usually follows; it then finds the head stored).
func (r *repoRun) handleCreate(ctx context.Context, e *ghapp.CreateEvent, at time.Time) error {
	if e.RefType != "branch" {
		return nil
	}
	branch := strings.TrimPrefix(e.Ref, ghapp.BranchRefPrefix)
	b := r.st.branch(branch)
	b.LastPush = at
	if err := r.loadConfig(ctx, ""); err != nil {
		return err
	}
	if !r.codeAllowed() || !r.wanted(branch) {
		return r.save(ctx)
	}
	if err := r.seed(ctx); err != nil {
		return err
	}
	tip, err := r.client.Branch(ctx, r.owner, r.name, branch)
	if errors.Is(err, ghapp.ErrNotFound) {
		return r.save(ctx) // deleted again since
	}
	if err != nil {
		return err
	}
	if b.Head != tip.Commit.SHA {
		if err := r.importBranch(ctx, branch, tip.Commit.SHA); err != nil {
			return err
		}
	}
	return r.save(ctx)
}

// handlePullRequest keeps the open pull requests current and, when one
// opens from a branch the policy now selects, brings that branch's tree up
// to its head.
func (r *repoRun) handlePullRequest(ctx context.Context, d Delivery, e *ghapp.PullRequestEvent) error {
	if r.st.OpenPRs == nil {
		r.st.OpenPRs = OpenPRs{}
	}
	for _, ae := range ActivityEvents(d, e, r.s.now()) {
		r.st.OpenPRs.Apply(ae)
	}
	switch e.Action {
	case "opened", "reopened", "ready_for_review":
	default:
		return r.save(ctx)
	}
	head := e.PullRequest.Head
	if !headInRepo(head, r.st.Repository) || head.Ref == "" {
		return r.save(ctx)
	}
	if err := r.loadConfig(ctx, ""); err != nil {
		return err
	}
	b := r.st.branch(head.Ref)
	if r.codeAllowed() && r.wanted(head.Ref) {
		if err := r.seed(ctx); err != nil {
			return err
		}
	}
	if r.codeAllowed() && r.wanted(head.Ref) && b.Head != head.SHA {
		if err := r.importBranch(ctx, head.Ref, head.SHA); err != nil && !errors.Is(err, ghapp.ErrNotFound) {
			return err
		}
	}
	return r.save(ctx)
}

// handleRepository records a rename, transfer or default branch change on
// the worktree (openRun already took the new name and default branch from
// the payload).
func (r *repoRun) handleRepository(ctx context.Context, e *ghapp.RepositoryEvent) error {
	switch e.Action {
	case "renamed", "transferred", "edited":
	default:
		return nil
	}
	if err := r.loadConfig(ctx, ""); err != nil {
		return err
	}
	written := false
	for _, b := range r.st.Branches {
		written = written || b.Head != ""
	}
	if written && r.codeAllowed() {
		if _, err := r.openStore(ctx, true); err != nil {
			return err
		}
	}
	return r.save(ctx)
}

// syncDocs syncs the plans and stories at commit (the default branch's
// tip) and records the outcome in the activity log.
func (r *repoRun) syncDocs(ctx context.Context, commit, before string) error {
	_, err := r.s.SyncDocs(ctx, ClientFetcher{Client: r.client}, ActivityDocsRecorder(r.s.Code, r.st.Repository), DocsRequest{
		Target:   r.claim.Target,
		Repo:     r.st.Name,
		Slug:     r.st.Slug,
		Commit:   commit,
		Before:   before,
		Delivery: r.delivery,
	})
	if err != nil {
		return err
	}
	r.st.DocsCommit = commit
	return r.save(ctx)
}

// enqueueImports queues an import of each repository of an installation.
func (s *Service) enqueueImports(ctx context.Context, installation int64, repos []ghapp.RepositoryRef) error {
	if len(repos) == 0 {
		_, err := s.Jobs.Enqueue(ctx, Job{Kind: JobImport, Installation: installation})
		return err
	}
	for _, r := range repos {
		if r.ID <= 0 {
			continue
		}
		if _, err := s.Jobs.Enqueue(ctx, Job{Kind: JobImport, Installation: installation, Repository: r.ID}); err != nil {
			return err
		}
	}
	return nil
}

// EnqueueImport queues the import of every repository of an installation,
// for the host to call once the installation is claimed.
func (s *Service) EnqueueImport(ctx context.Context, installation int64) error {
	return s.enqueueImports(ctx, installation, nil)
}

// --- jobs ----------------------------------------------------------------

// Work runs jobs until ctx ends, waiting PollInterval whenever none is
// ready; it returns ctx's error. Run it in as many goroutines as workers
// are wanted: the queue keeps one job per repository at a time.
func (s *Service) Work(ctx context.Context) error {
	for {
		n, err := s.RunPending(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			s.log.Error("ghsync: job queue", "err", err)
		}
		if n == 0 || err != nil {
			t := time.NewTimer(s.poll)
			select {
			case <-ctx.Done():
				t.Stop()
				return ctx.Err()
			case <-t.C:
			}
		}
	}
}

// RunPending runs jobs until none is ready and returns how many ran. The
// error is the queue's; a job's own failure is retried, paused or
// dead-lettered through the queue.
func (s *Service) RunPending(ctx context.Context) (int, error) {
	n := 0
	for {
		ran, err := s.RunOne(ctx)
		if err != nil || !ran {
			return n, err
		}
		n++
	}
}

// RunOne claims and runs one job, reporting whether one was ready.
func (s *Service) RunOne(ctx context.Context) (bool, error) {
	lease, err := s.Jobs.Claim(ctx, s.lease)
	if errors.Is(err, ErrNoJob) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	jobCtx, stop := s.keepLease(ctx, lease)
	jobErr := s.runJob(jobCtx, lease.Job)
	lease, lost := stop()
	if lost {
		return true, ErrLeaseLost
	}
	return true, s.finish(ctx, lease, jobErr)
}

// maxJobRun bounds one run of a job: without it a job that hangs would keep
// its lease, and with it its repository's queue, for good.
const maxJobRun = 6 * time.Hour

// keepLease extends lease while its job runs and cancels the returned
// context when the lease is lost, so the job stops rather than run beside
// the worker that takes it next. stop ends that and returns the current
// lease and whether it was lost. A failed extension other than a lost lease
// is tried again at the next tick.
func (s *Service) keepLease(ctx context.Context, lease Lease) (context.Context, func() (Lease, bool)) {
	jobCtx, cancel := context.WithTimeout(ctx, maxJobRun)
	done, exited := make(chan struct{}), make(chan struct{})
	cur, lost := lease, false
	go func() {
		defer close(exited)
		t := time.NewTicker(s.lease / 3)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
			}
			l, err := s.Jobs.Extend(jobCtx, cur, s.lease)
			switch {
			case errors.Is(err, ErrLeaseLost):
				lost = true
				cancel()
				return
			case err != nil:
				s.log.Warn("ghsync: extend a job lease", "job", cur.Job.ID, "err", err)
			default:
				cur = l
			}
		}
	}()
	return jobCtx, func() (Lease, bool) {
		close(done)
		<-exited
		cancel()
		return cur, lost
	}
}

func (s *Service) runJob(ctx context.Context, j Job) error {
	switch j.Kind {
	case JobDelivery:
		return s.Handle(ctx, *j.Delivery)
	case JobSweep:
		if j.Repository == 0 {
			return s.enqueueRepos(ctx, j.Installation, JobSweep)
		}
		return s.sweepRepo(ctx, j.Installation, j.Repository)
	case JobImport, JobReconcile:
		if j.Repository == 0 {
			return s.enqueueRepos(ctx, j.Installation, j.Kind)
		}
		r, err := s.openRun(ctx, j.Installation, j.Repository, nil)
		if err != nil {
			return err
		}
		return r.syncBranches(ctx, strings.TrimPrefix(j.Ref, ghapp.BranchRefPrefix))
	}
	return fmt.Errorf("%w: unknown kind %q", ErrInvalidJob, j.Kind)
}

// finish ends a lease by its job's outcome: done on success and on what a
// retry cannot fix (unclaimed, gone, unreadable, invalid), paused until a
// rate limit resets, retried with back-off otherwise.
func (s *Service) finish(ctx context.Context, lease Lease, err error) error {
	j := lease.Job
	log := s.log.With("job", j.ID, "kind", string(j.Kind), "installation", j.Installation, "repository", j.Repository)
	if j.Delivery != nil {
		log = log.With("delivery", j.Delivery.ID, "event", j.Delivery.Event)
	}
	if wait, ok := ghapp.RetryAfter(err); ok {
		if wait < time.Second {
			wait = time.Minute
		}
		log.Info("ghsync: rate limited; installation paused", "for", wait)
		return s.Jobs.Pause(ctx, lease, s.now().Add(wait))
	}
	switch {
	case err == nil:
	case errors.Is(err, ErrNotClaimed):
		log.Info("ghsync: not claimed; job dropped")
	case errors.Is(err, ghapp.ErrNotFound), errors.Is(err, ghapp.ErrInstallationNotFound):
		log.Info("ghsync: gone on GitHub; job dropped", "err", err)
	case errors.Is(err, ErrBadDelivery), errors.Is(err, ErrInvalidTarget), errors.Is(err, ErrInvalidJob), errors.Is(err, code.ErrInvalid):
		log.Error("ghsync: job cannot succeed; dropped", "err", err)
	default:
		rerr := s.Jobs.Retry(ctx, lease, Backoff(j.Attempts+1), err)
		if errors.Is(rerr, ErrDeadLettered) {
			log.Error("ghsync: job dead-lettered", "err", err)
			return nil
		}
		log.Warn("ghsync: job failed; retrying", "err", err, "attempt", j.Attempts+1)
		return rerr
	}
	return s.Jobs.Done(ctx, lease)
}

// enqueueRepos queues a job of kind for each claimed repository of an
// installation.
func (s *Service) enqueueRepos(ctx context.Context, installation int64, kind JobKind) error {
	if s.App == nil {
		return errNoApp
	}
	client, err := s.App.InstallationClient(ctx, installation)
	if err != nil {
		return err
	}
	repos, err := client.InstallationRepositories(ctx)
	if err != nil {
		return err
	}
	for _, repo := range repos {
		if _, err := s.Claims.Claim(ctx, installation, repo.ID); err != nil {
			if errors.Is(err, ErrNotClaimed) {
				continue
			}
			return err
		}
		if _, err := s.Jobs.Enqueue(ctx, Job{Kind: kind, Installation: installation, Repository: repo.ID}); err != nil {
			return err
		}
	}
	return nil
}

// sweepRepo runs the daily inactivity sweep of one repository from its
// stored state alone (no GitHub call).
func (s *Service) sweepRepo(ctx context.Context, installation, repository int64) error {
	claim, err := s.Claims.Claim(ctx, installation, repository)
	if err != nil {
		return err
	}
	st, err := s.State.Load(ctx, claim.Target, repository)
	if err != nil || st == nil {
		return err
	}
	r := &repoRun{s: s, inst: installation, claim: claim, st: st}
	return r.sweep(ctx)
}

// Reconcile queues, for every repository of every installation the app
// holds that is claimed, the nightly reconcile (each selected branch's tip
// against the stored head, drifted ones re-imported, deleted ones
// dropped, the plans and stories brought to the default branch) and the
// daily inactivity sweep. It returns how many jobs it queued. The host
// calls it once a day.
func (s *Service) Reconcile(ctx context.Context) (int, error) {
	if s.App == nil {
		return 0, errNoApp
	}
	insts, err := s.App.AppClient().Installations(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, inst := range insts {
		if inst.Suspended() {
			continue
		}
		client, err := s.App.InstallationClient(ctx, inst.ID)
		if errors.Is(err, ghapp.ErrInstallationNotFound) {
			continue
		}
		if err != nil {
			return n, err
		}
		repos, err := client.InstallationRepositories(ctx)
		if err != nil {
			return n, err
		}
		for _, repo := range repos {
			if _, err := s.Claims.Claim(ctx, inst.ID, repo.ID); err != nil {
				if errors.Is(err, ErrNotClaimed) {
					continue
				}
				return n, err
			}
			for _, kind := range []JobKind{JobReconcile, JobSweep} {
				if _, err := s.Jobs.Enqueue(ctx, Job{Kind: kind, Installation: inst.ID, Repository: repo.ID}); err != nil {
					return n, err
				}
				n++
			}
		}
	}
	return n, nil
}

// --- the docs fetcher over the REST client --------------------------------

// ClientFetcher is the DocFetcher of an installation's REST client: the
// contents API at a commit.
type ClientFetcher struct {
	Client *ghapp.Client
}

var _ DocFetcher = ClientFetcher{}

func notExist(what string, err error) error {
	return fmt.Errorf("%s: %w (%w)", what, fs.ErrNotExist, err)
}

// ListDir implements DocFetcher.
func (f ClientFetcher) ListDir(ctx context.Context, repo, commit, dir string) ([]DocEntry, error) {
	owner, name, ok := splitRepo(repo)
	if !ok {
		return nil, fmt.Errorf("ghsync: invalid repository %q", repo)
	}
	c, err := f.Client.Contents(ctx, owner, name, dir, commit)
	if errors.Is(err, ghapp.ErrNotFound) || errors.Is(err, ghapp.ErrTooLarge) {
		// Not there, or a (large) file rather than a directory.
		return nil, notExist(dir, err)
	}
	if err != nil {
		return nil, err
	}
	if c.Type != "dir" {
		return nil, fmt.Errorf("%s: not a directory: %w", dir, fs.ErrNotExist)
	}
	out := make([]DocEntry, 0, len(c.Entries))
	for _, e := range c.Entries {
		out = append(out, DocEntry{Name: e.Name, Dir: e.Type == "dir"})
	}
	return out, nil
}

// ReadFile implements DocFetcher; a file the contents API will not inline
// is read as a blob.
func (f ClientFetcher) ReadFile(ctx context.Context, repo, commit, file string) ([]byte, error) {
	owner, name, ok := splitRepo(repo)
	if !ok {
		return nil, fmt.Errorf("ghsync: invalid repository %q", repo)
	}
	c, err := f.Client.Contents(ctx, owner, name, file, commit)
	switch {
	case errors.Is(err, ghapp.ErrNotFound):
		return nil, notExist(file, err)
	case errors.Is(err, ghapp.ErrTooLarge):
		dir, base := path.Split(file)
		parent, err := f.Client.Contents(ctx, owner, name, strings.TrimSuffix(dir, "/"), commit)
		if err != nil {
			return nil, err
		}
		for _, e := range parent.Entries {
			if e.Name == base {
				b, err := f.Client.Blob(ctx, owner, name, e.SHA)
				if err != nil {
					return nil, err
				}
				return b.Content, nil
			}
		}
		return nil, notExist(file, ghapp.ErrNotFound)
	case err != nil:
		return nil, err
	}
	if c.Type != "file" {
		return nil, fmt.Errorf("%s: not a file: %w", file, fs.ErrNotExist)
	}
	return c.Content, nil
}

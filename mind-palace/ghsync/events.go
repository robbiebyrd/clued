package ghsync

// The activity log (plan 0011, "Activity log"): every subscribed webhook
// event becomes at most one code.Event holding an allow-listed subset of its
// payload (refs, commits, numbers, names, logins, states, a page URL), never
// the payload itself and never an e-mail address. ActivityEvents is the pure
// mapping, AppendActivity writes its result through a code.Store, and the
// docs sync's outcomes reach the same log through DocsActivityEvents and
// ActivityDocsRecorder. OpenPRs tracks a repository's open pull requests
// from those events for the branch policy's active rule.

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/ghapp"
)

// The pull_request actions the activity log records, as code.EventPRPrefix
// plus the action; GitHub's other actions (edited, labeled, assigned, …)
// are not recorded.
const (
	EventPROpened         = code.EventPRPrefix + "opened"
	EventPRSynchronize    = code.EventPRPrefix + "synchronize"
	EventPRClosed         = code.EventPRPrefix + "closed"
	EventPRReopened       = code.EventPRPrefix + "reopened"
	EventPRReadyForReview = code.EventPRPrefix + "ready_for_review"
)

// The repository actions the activity log records, as code.EventRepoPrefix
// plus the action. For repo_renamed and repo_transferred the event's Name
// holds the repository's previous owner/repo.
const (
	EventRepoRenamed     = code.EventRepoPrefix + "renamed"
	EventRepoTransferred = code.EventRepoPrefix + "transferred"
	EventRepoArchived    = code.EventRepoPrefix + "archived"
	EventRepoDeleted     = code.EventRepoPrefix + "deleted"
)

// Limits on the free-text fields an activity event keeps; longer values are
// cut at a rune boundary (names, logins) or dropped (URLs).
const (
	maxActivityNameBytes = 256
	maxActivityURLBytes  = 2048
)

var (
	activityPRActions   = map[string]bool{"opened": true, "synchronize": true, "closed": true, "reopened": true, "ready_for_review": true}
	activityRepoActions = map[string]bool{"renamed": true, "transferred": true, "archived": true, "deleted": true}
)

// ActivityEvents maps one webhook delivery to the activity events it
// records, following plan 0011's table:
//
//   - push to a branch: push (ref, before, after as Commit, commit count,
//     forced, pusher login); a tag push, or a push that deletes the branch
//     (the delete event records that), records nothing;
//   - create / delete of a branch: branch_created / branch_deleted (tags
//     record nothing);
//   - pull_request opened, synchronize, closed, reopened, ready_for_review:
//     pr_<action> (number, head ref and sha, base ref and sha, merged and
//     merge commit, sender login, page URL); the head ref is kept only when
//     the head branch lives in the repository itself (not a fork);
//   - check_suite, check_run and workflow_run completed, and every status:
//     ci (commit, branch, name, status, conclusion, page URL; a status keeps
//     its state as Status and no URL, since its target URL points outside
//     GitHub);
//   - repository renamed, transferred, archived, deleted: repo_<action>
//     (the repository, its previous name in Name for a rename or transfer,
//     sender login);
//   - everything else (other actions, installation events, ping): nothing.
//
// ev is the decoded payload; when it is nil it is decoded from d.Payload
// (d.Event names it), and an undecodable payload records nothing. Every
// event carries d.ID as Delivery, the repository's GitHub-backed worktree
// (gh_<id>) and its owner/repo. At is the payload's own time when it has
// one (a pull request's, check's, run's or status's update time), else
// d.ReceivedAt, else now. Fields that would not pass
// code.Event.Validate are left empty, and the result always validates.
func ActivityEvents(d Delivery, ev ghapp.Event, now time.Time) []code.Event {
	if ev == nil {
		if len(d.Payload) == 0 || d.Event == "" {
			return nil
		}
		parsed, err := ghapp.ParseEvent(d.Event, d.Payload)
		if err != nil {
			return nil
		}
		ev = parsed
	}
	at := d.ReceivedAt
	if at.IsZero() {
		at = now
	}
	var e code.Event
	switch p := ev.(type) {
	case *ghapp.PushEvent:
		branch, ok := p.Branch()
		if !ok || p.Deleted || p.After == ghapp.ZeroSHA {
			return nil
		}
		e = code.Event{
			Kind:    code.EventPush,
			Ref:     branch,
			Before:  p.Before,
			Commit:  p.After,
			Commits: len(p.Commits),
			Forced:  p.Forced,
			Actor:   p.Pusher.Name,
		}
		if e.Actor == "" {
			e.Actor = senderLogin(p.Sender)
		}
		envelopeRepo(&e, &p.Envelope)
	case *ghapp.CreateEvent:
		if p.RefType != "branch" {
			return nil
		}
		e = code.Event{Kind: code.EventBranchCreated, Ref: strings.TrimPrefix(p.Ref, ghapp.BranchRefPrefix), Actor: senderLogin(p.Sender)}
		envelopeRepo(&e, &p.Envelope)
	case *ghapp.DeleteEvent:
		if p.RefType != "branch" {
			return nil
		}
		e = code.Event{Kind: code.EventBranchDeleted, Ref: strings.TrimPrefix(p.Ref, ghapp.BranchRefPrefix), Actor: senderLogin(p.Sender)}
		envelopeRepo(&e, &p.Envelope)
	case *ghapp.PullRequestEvent:
		if !activityPRActions[p.Action] {
			return nil
		}
		pr := p.PullRequest
		number := pr.Number
		if number <= 0 {
			number = p.Number
		}
		e = code.Event{
			Kind:       code.EventPRPrefix + p.Action,
			PR:         number,
			Commit:     pr.Head.SHA,
			BaseRef:    pr.Base.Ref,
			BaseCommit: pr.Base.SHA,
			Merged:     pr.Merged,
			Actor:      senderLogin(p.Sender),
			URL:        pr.HTMLURL,
		}
		if pr.Merged {
			e.MergeCommit = pr.MergeCommitSHA
		}
		if headInRepo(pr.Head, p.RepositoryID()) {
			e.Ref = pr.Head.Ref
		}
		if !pr.UpdatedAt.IsZero() {
			at = pr.UpdatedAt.Time
		}
		envelopeRepo(&e, &p.Envelope)
	case *ghapp.CheckSuiteEvent:
		if p.Action != "completed" {
			return nil
		}
		cs := p.CheckSuite
		e = code.Event{Kind: code.EventCI, Commit: cs.HeadSHA, Ref: cs.HeadBranch, Status: cs.Status, Conclusion: cs.Conclusion}
		if cs.App != nil {
			e.Name = cs.App.Name
			if e.Name == "" {
				e.Name = cs.App.Slug
			}
		}
		if e.Status == "" {
			e.Status = "completed"
		}
		if !cs.UpdatedAt.IsZero() {
			at = cs.UpdatedAt.Time
		}
		envelopeRepo(&e, &p.Envelope)
	case *ghapp.CheckRunEvent:
		if p.Action != "completed" {
			return nil
		}
		cr := p.CheckRun
		e = code.Event{Kind: code.EventCI, Commit: cr.HeadSHA, Name: cr.Name, Status: cr.Status, Conclusion: cr.Conclusion, URL: cr.HTMLURL}
		if cr.CheckSuite != nil {
			e.Ref = cr.CheckSuite.HeadBranch
		}
		if e.Status == "" {
			e.Status = "completed"
		}
		if !cr.CompletedAt.IsZero() {
			at = cr.CompletedAt.Time
		}
		envelopeRepo(&e, &p.Envelope)
	case *ghapp.WorkflowRunEvent:
		if p.Action != "completed" {
			return nil
		}
		wr := p.WorkflowRun
		e = code.Event{Kind: code.EventCI, Commit: wr.HeadSHA, Ref: wr.HeadBranch, Name: wr.Name, Status: wr.Status, Conclusion: wr.Conclusion, URL: wr.HTMLURL}
		if e.Name == "" && p.Workflow != nil {
			e.Name = p.Workflow.Name
		}
		if e.Status == "" {
			e.Status = "completed"
		}
		if wr.Actor != nil {
			e.Actor = wr.Actor.Login
		} else {
			e.Actor = senderLogin(p.Sender)
		}
		if !wr.UpdatedAt.IsZero() {
			at = wr.UpdatedAt.Time
		}
		envelopeRepo(&e, &p.Envelope)
	case *ghapp.StatusEvent:
		e = code.Event{Kind: code.EventCI, Commit: p.SHA, Name: p.Context, Status: p.State}
		if len(p.Branches) == 1 {
			e.Ref = p.Branches[0].Name
		}
		if !p.UpdatedAt.IsZero() {
			at = p.UpdatedAt.Time
		}
		envelopeRepo(&e, &p.Envelope)
	case *ghapp.RepositoryEvent:
		if !activityRepoActions[p.Action] {
			return nil
		}
		e = code.Event{Kind: code.EventRepoPrefix + p.Action, Actor: senderLogin(p.Sender)}
		envelopeRepo(&e, &p.Envelope)
		e.Name = previousRepoName(p)
	default:
		return nil
	}
	if e.Repo == "" {
		e.Repo = d.RepositoryName
	}
	if e.Worktree == "" && d.Repository > 0 {
		e.Worktree = activityWorktree(d.Repository)
	}
	e.Delivery = d.ID
	e.At = at.UTC()
	e = sanitizeActivity(e)
	if e.Validate() != nil {
		return nil
	}
	return []code.Event{e}
}

// AppendActivity records a delivery's activity events (ActivityEvents) in
// st's activity log and returns how many it appended; a delivery that
// records nothing appends nothing and is not an error. The host calls it
// with the claimed target project's code store.
func AppendActivity(ctx context.Context, st code.Store, d Delivery, ev ghapp.Event, now time.Time) (int, error) {
	events := ActivityEvents(d, ev, now)
	if len(events) == 0 {
		return 0, nil
	}
	if st == nil {
		return 0, errors.New("ghsync: activity log needs a code store")
	}
	if err := st.AppendEvents(ctx, events); err != nil {
		return 0, fmt.Errorf("ghsync: append activity of delivery %s: %w", d.ID, err)
	}
	return len(events), nil
}

// DocsActivityEvents maps a docs sync's outcomes to activity events
// (plan_sync, plan_sync_conflict, plan_sync_problem), stamped with the
// GitHub-backed worktree when repository is not 0. Problems are kept (they
// are validation messages, never document content); an outcome whose
// fields would not validate keeps only the ones that do.
func DocsActivityEvents(repository int64, events []DocsEvent) []code.Event {
	out := make([]code.Event, 0, len(events))
	for _, de := range events {
		e := code.Event{
			Kind:     de.Kind,
			Delivery: de.Delivery,
			Repo:     de.Repo,
			Commit:   de.Commit,
			DocKind:  de.DocKind,
			DocID:    de.ID,
			Path:     de.Path,
			Action:   de.Action,
			Problems: append([]string(nil), de.Problems...),
		}
		if !de.At.IsZero() {
			e.At = de.At.UTC()
		}
		if repository > 0 {
			e.Worktree = activityWorktree(repository)
		}
		e = sanitizeActivity(e)
		if e.Validate() != nil {
			continue
		}
		out = append(out, e)
	}
	return out
}

// ActivityDocsRecorder returns the DocsRecorder that appends a docs sync's
// outcomes to the activity log of the target project's code store, opened
// with open. repository is the GitHub repository id the sync ran for (0
// when unknown), stamped as the worktree.
func ActivityDocsRecorder(open func(ctx context.Context, target Target, slug string) (code.Store, error), repository int64) DocsRecorder {
	return func(ctx context.Context, target Target, slug string, events []DocsEvent) error {
		mapped := DocsActivityEvents(repository, events)
		if len(mapped) == 0 {
			return nil
		}
		if open == nil {
			return errors.New("ghsync: activity log needs a code store opener")
		}
		st, err := open(ctx, target, slug)
		if err != nil {
			return fmt.Errorf("ghsync: open code store of %s: %w", slug, err)
		}
		if err := st.AppendEvents(ctx, mapped); err != nil {
			return fmt.Errorf("ghsync: append docs activity of %s: %w", slug, err)
		}
		return nil
	}
}

// OpenPRs is the set of a repository's open pull requests whose head branch
// lives in the repository: pull request number → head branch. It feeds the
// branch policy's active rule (Branch.OpenPR). Seed it from the list fetched
// at import (OpenPRsFromList) and keep it current with Apply over the
// activity events that follow; the zero value is not usable, use make or
// the constructors.
type OpenPRs map[int]string

// OpenPRsFromList returns the open pull requests of a GET pulls?state=open
// listing whose head is a branch of repository (its GitHub id).
func OpenPRsFromList(repository int64, prs []ghapp.PullRequest) OpenPRs {
	o := OpenPRs{}
	for _, pr := range prs {
		if pr.Number <= 0 || (pr.State != "" && pr.State != "open") || !headInRepo(pr.Head, repository) || !code.ValidRef(pr.Head.Ref) {
			continue
		}
		o[pr.Number] = pr.Head.Ref
	}
	return o
}

// Apply updates o with one activity event: a pull request opened,
// reopened, synchronized or ready for review with a head branch in the
// repository is open; a closed one, or one whose head is no longer a
// branch of the repository, is not. Other events change nothing, so the
// whole activity log can be replayed through it (the log is at least once
// and Apply is idempotent).
func (o OpenPRs) Apply(e code.Event) {
	if e.PR <= 0 || !strings.HasPrefix(e.Kind, code.EventPRPrefix) {
		return
	}
	switch e.Kind {
	case EventPRClosed:
		delete(o, e.PR)
	case EventPROpened, EventPRReopened, EventPRSynchronize, EventPRReadyForReview:
		if e.Ref == "" {
			delete(o, e.PR)
			return
		}
		o[e.PR] = e.Ref
	}
}

// Branches returns the head branches with at least one open pull request,
// sorted.
func (o OpenPRs) Branches() []string {
	seen := map[string]bool{}
	var out []string
	for _, b := range o {
		if !seen[b] {
			seen[b] = true
			out = append(out, b)
		}
	}
	sort.Strings(out)
	return out
}

// Has reports whether branch has an open pull request.
func (o OpenPRs) Has(branch string) bool {
	for _, b := range o {
		if b == branch {
			return true
		}
	}
	return false
}

// Mark returns branches with OpenPR set from o (true for a branch with an
// open pull request, false otherwise), ready for BranchPolicy.Select. The
// input is not modified.
func (o OpenPRs) Mark(branches []Branch) []Branch {
	open := map[string]bool{}
	for _, b := range o {
		open[b] = true
	}
	out := make([]Branch, len(branches))
	for i, b := range branches {
		b.OpenPR = open[b.Name]
		out[i] = b
	}
	return out
}

// ReplayOpenPRs applies the activity events of st after afterSeq that
// concern repository's GitHub-backed worktree to seed (nil starts empty)
// and returns the result with the last Seq read, to pass as afterSeq next
// time. It pages through the log, limit events at a time (500 when not
// positive).
func ReplayOpenPRs(ctx context.Context, st code.Store, repository int64, seed OpenPRs, afterSeq int64, limit int) (OpenPRs, int64, error) {
	if st == nil {
		return nil, afterSeq, errors.New("ghsync: replaying open pull requests needs a code store")
	}
	if limit <= 0 {
		limit = 500
	}
	o := OpenPRs{}
	for n, b := range seed {
		o[n] = b
	}
	worktree := activityWorktree(repository)
	for {
		page, err := st.Events(ctx, afterSeq, limit)
		if err != nil {
			return nil, afterSeq, fmt.Errorf("ghsync: read activity: %w", err)
		}
		for _, e := range page {
			if e.Seq > afterSeq {
				afterSeq = e.Seq
			}
			if e.Worktree == worktree {
				o.Apply(e)
			}
		}
		if len(page) < limit {
			return o, afterSeq, nil
		}
	}
}

// activityWorktree is the GitHub-backed worktree id of a repository.
func activityWorktree(repository int64) string {
	if repository <= 0 {
		return ""
	}
	return "gh_" + strconv.FormatInt(repository, 10)
}

// envelopeRepo copies the repository's owner/repo and worktree from a
// payload envelope.
func envelopeRepo(e *code.Event, env *ghapp.Envelope) {
	if env.Repository == nil {
		return
	}
	e.Repo = env.Repository.FullName
	e.Worktree = activityWorktree(env.Repository.ID)
}

// senderLogin is the sender's login, "" when absent.
func senderLogin(u *ghapp.User) string {
	if u == nil {
		return ""
	}
	return u.Login
}

// headInRepo reports whether a pull request's head branch lives in the
// repository (not a fork, not a deleted repository).
func headInRepo(head ghapp.PullRequestBranch, repository int64) bool {
	return head.Repo != nil && repository != 0 && head.Repo.ID == repository
}

// previousRepoName is a renamed or transferred repository's previous
// owner/repo, "" when the payload does not say.
func previousRepoName(p *ghapp.RepositoryEvent) string {
	if p.Repository == nil {
		return ""
	}
	owner, name, _ := strings.Cut(p.Repository.FullName, "/")
	if owner == "" {
		owner = p.Repository.Owner.Login
	}
	if name == "" {
		name = p.Repository.Name
	}
	switch p.Action {
	case "renamed":
		if from := p.PreviousName(); from != "" && owner != "" {
			return owner + "/" + from
		}
	case "transferred":
		if from := p.PreviousOwner(); from != "" && name != "" {
			return from + "/" + name
		}
	}
	return ""
}

// sanitizeActivity empties the fields of e that would fail
// code.Event.Validate or that are not what the field promises (a URL that
// is not https, an over-long name), so a malformed payload loses a field
// rather than its event.
func sanitizeActivity(e code.Event) code.Event {
	if e.Worktree != "" && !code.ValidWorktreeID(e.Worktree) {
		e.Worktree = ""
	}
	for _, r := range []*string{&e.Ref, &e.BaseRef} {
		if *r != "" && !code.ValidRef(*r) {
			*r = ""
		}
	}
	for _, c := range []*string{&e.Commit, &e.Before, &e.BaseCommit, &e.MergeCommit} {
		if *c != "" && !code.ValidGitOID(*c) {
			*c = ""
		}
	}
	if e.Path != "" && !code.ValidPath(e.Path) {
		e.Path = ""
	}
	if e.Commits < 0 {
		e.Commits = 0
	}
	if e.PR < 0 {
		e.PR = 0
	}
	for _, s := range []*string{&e.Repo, &e.Actor, &e.Name, &e.Status, &e.Conclusion, &e.DocKind, &e.DocID, &e.Action} {
		*s = clipActivity(*s)
	}
	e.URL = pageURL(e.URL)
	return e
}

// clipActivity cuts s to maxActivityNameBytes at a rune boundary.
func clipActivity(s string) string {
	if len(s) <= maxActivityNameBytes {
		return s
	}
	s = s[:maxActivityNameBytes]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// pageURL keeps u when it is an https URL without credentials, a query or
// a fragment and within maxActivityURLBytes; anything else is dropped.
func pageURL(u string) string {
	if u == "" || len(u) > maxActivityURLBytes {
		return ""
	}
	parsed, err := url.Parse(u)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ""
	}
	return u
}

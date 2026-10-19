// Package ghsync holds the rules of the GitHub App sync (plan 0011): an
// installation claimed by a clued account or workspace keeps each of its
// repositories' pushed code (a GitHub-backed worktree in code.Store), its
// plans and stories (from the default branch) and an activity log up to
// date from GitHub's webhooks.
//
// This file fixes the shapes every part of the sync shares: the Service and
// what it consumes (the GitHub App, the job queue, the claims and the
// per-project openers), a Delivery as the webhook endpoint queues it, and the
// Target an installation is claimed for. jobs.go holds the job queue port and
// memjobs.go its in-memory implementation; policy.go the branch policy. The
// sync itself (code.go, events.go, docs.go) builds on these.
//
// The package holds no transport: the host (clued-cloud) verifies and
// dedupes deliveries, resolves claims from its account store, runs the
// workers and opens the namespaced stores.
package ghsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/service"
)

// TargetKind says whose namespace an installation writes into.
type TargetKind string

const (
	// TargetUser is a user's own namespace (u<user id>__<slug>).
	TargetUser TargetKind = "user"
	// TargetWorkspace is a workspace's namespace (w<workspace id>__<slug>).
	TargetWorkspace TargetKind = "workspace"
)

// Target is the account or workspace an installation is claimed for. The
// host maps it to its namespace; ghsync never builds a namespace itself.
type Target struct {
	// Kind is TargetUser or TargetWorkspace.
	Kind TargetKind `json:"kind"`
	// ID is the user id (Kind user) or the workspace id (Kind workspace).
	ID string `json:"id"`
	// Owner is the clued user the GitHub-backed worktree is stamped with:
	// the claiming user, or the workspace admin who claimed it. For a user
	// target it equals ID.
	Owner string `json:"owner"`
}

// ErrInvalidTarget is returned by Target.Validate.
var ErrInvalidTarget = errors.New("ghsync: invalid target")

// Validate reports whether t names a namespace: a known kind, an id and an
// owner (equal to the id for a user target).
func (t Target) Validate() error {
	switch t.Kind {
	case TargetUser:
		if t.ID == "" || t.Owner != t.ID {
			return fmt.Errorf("%w: a user target needs an id and itself as owner", ErrInvalidTarget)
		}
	case TargetWorkspace:
		if t.ID == "" || t.Owner == "" {
			return fmt.Errorf("%w: a workspace target needs an id and an owner", ErrInvalidTarget)
		}
	default:
		return fmt.Errorf("%w: unknown kind %q", ErrInvalidTarget, t.Kind)
	}
	return nil
}

// Delivery is a webhook delivery as the endpoint queues it: what was
// verified from the headers and the minimal subset of the payload the
// workers need. It never holds the full payload, and nothing logs it beyond
// ID and Event.
type Delivery struct {
	// ID is X-GitHub-Delivery, the dedupe key.
	ID string `json:"id"`
	// Event is X-GitHub-Event (push, create, delete, pull_request, …).
	Event string `json:"event"`
	// Action is the payload's action (opened, closed, completed, …), empty
	// for events without one (push, create, delete).
	Action string `json:"action,omitempty"`
	// Installation is the GitHub installation id.
	Installation int64 `json:"installation"`
	// Repository is GitHub's numeric repository id (0 for installation
	// events that name no single repository); it keys the worktree
	// (gh_<id>) and the job queue's per-repository order.
	Repository int64 `json:"repository,omitempty"`
	// RepositoryName is owner/repo at the time of the delivery.
	RepositoryName string `json:"repositoryName,omitempty"`
	// Ref is the full ref (refs/heads/main) for push, create and delete.
	Ref string `json:"ref,omitempty"`
	// Before and After are the push's old and new tips (After all zeros
	// for a deleted ref, Before all zeros for a new one).
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
	// Sender is the login of the user who triggered the event.
	Sender string `json:"sender,omitempty"`
	// Payload is the allow-listed subset of the payload the event's
	// handler reads (the typed ghapp event re-encoded), never the raw body.
	Payload json.RawMessage `json:"payload,omitempty"`
	// ReceivedAt is when the endpoint accepted the delivery.
	ReceivedAt time.Time `json:"receivedAt"`
}

// ErrNotClaimed is returned by Claims for an installation or repository
// nothing may be written for: unknown, unclaimed, suspended, removed or not
// entitled. The worker finishes such a job without doing anything.
var ErrNotClaimed = errors.New("ghsync: installation not claimed")

// Claim is what the host knows about a claimed installation's repository.
type Claim struct {
	// Installation is the GitHub installation id.
	Installation int64 `json:"installation"`
	// Target is the account or workspace the installation is claimed for.
	Target Target `json:"target"`
	// Policy is the branch policy set on the Integrations page for the
	// repository, or the installation's when the repository has none; a
	// zero value means nothing was set (DefaultBranchPolicy applies). The
	// repository's mind-palace.config.yaml still wins (EffectivePolicy).
	Policy BranchPolicy `json:"policy"`
	// CodeSync is false when code may not be written for the target (not
	// entitled, quota full); plans, stories and events still sync.
	CodeSync bool `json:"codeSync"`
}

// Claims resolves an installation's repository to its claim. Implementations
// return ErrNotClaimed (wrapped or not) for anything that must not be
// written: an unknown, unclaimed, suspended or removed installation, or a
// repository no longer in its selection.
type Claims interface {
	Claim(ctx context.Context, installation, repository int64) (Claim, error)
}

// GitHubApp is the GitHub App the workers fetch through: *ghapp.App (stories
// 0067 and 0068), whose InstallationClient gives the REST client for an
// installation. It is an alias of any until ghapp is merged; the first story
// that calls it narrows it to *ghapp.App.
type GitHubApp = any

// CodeStore is the code store a GitHub-backed worktree is written to:
// code.Store (story 0053). It is an alias of any until the code package is
// merged; the first story that writes code narrows it to code.Store.
type CodeStore = any

// CodeOpener opens the code store of a target's project (slug as
// projects.Slug derives it, or the repository's remote.project). The host
// opens it under the target's namespace only.
type CodeOpener func(ctx context.Context, target Target, slug string) (CodeStore, error)

// DocsOpener returns the palace (plan and story services over the project's
// store) of a target's project, the same one the board and the MCP tools
// use, so archiving goes through the service and its events.
type DocsOpener func(ctx context.Context, target Target, slug string) (*service.Palace, error)

// Options configures New. Jobs, Claims, Code and Docs are required.
type Options struct {
	App    GitHubApp
	Jobs   Jobs
	Claims Claims
	Code   CodeOpener
	Docs   DocsOpener
	// Now is the clock (time.Now when nil).
	Now func() time.Time
	// Logger receives delivery ids and event names only (slog.Default
	// when nil).
	Logger *slog.Logger
}

// Service is the GitHub sync: it turns queued deliveries into writes to the
// claimed target's code, plans, stories and activity log. Construct it with
// New; the handlers (Handle, Work, Reconcile) live beside the parts of the
// sync they drive.
type Service struct {
	App    GitHubApp
	Jobs   Jobs
	Claims Claims
	Code   CodeOpener
	Docs   DocsOpener
	now    func() time.Time
	log    *slog.Logger
}

// New returns a Service over opts, refusing a missing required dependency.
func New(opts Options) (*Service, error) {
	switch {
	case opts.Jobs == nil:
		return nil, errors.New("ghsync: Jobs is required")
	case opts.Claims == nil:
		return nil, errors.New("ghsync: Claims is required")
	case opts.Code == nil:
		return nil, errors.New("ghsync: Code opener is required")
	case opts.Docs == nil:
		return nil, errors.New("ghsync: Docs opener is required")
	}
	s := &Service{App: opts.App, Jobs: opts.Jobs, Claims: opts.Claims, Code: opts.Code, Docs: opts.Docs, now: opts.Now, log: opts.Logger}
	if s.now == nil {
		s.now = time.Now
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	return s, nil
}

// Now returns the service clock's current time.
func (s *Service) Now() time.Time { return s.now() }

// Logger returns the service logger.
func (s *Service) Logger() *slog.Logger { return s.log }

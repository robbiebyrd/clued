package ghapp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Event names (the X-GitHub-Event header) of the deliveries the app
// subscribes to, plus ping, which GitHub sends when the webhook is set up.
const (
	EventPush                     = "push"
	EventCreate                   = "create"
	EventDelete                   = "delete"
	EventPullRequest              = "pull_request"
	EventCheckSuite               = "check_suite"
	EventCheckRun                 = "check_run"
	EventStatus                   = "status"
	EventWorkflowRun              = "workflow_run"
	EventInstallation             = "installation"
	EventInstallationRepositories = "installation_repositories"
	EventRepository               = "repository"
	EventPing                     = "ping"
)

// ZeroSHA is the object id GitHub sends as push "before" for a new branch and
// as "after" for a deleted one.
const ZeroSHA = "0000000000000000000000000000000000000000"

// Ref prefixes.
const (
	BranchRefPrefix = "refs/heads/"
	TagRefPrefix    = "refs/tags/"
)

// ErrUnknownEvent is returned by ParseEvent for an event name the app does
// not subscribe to. A receiver acknowledges such a delivery and drops it.
var ErrUnknownEvent = errors.New("ghapp: unknown event")

// Event is a decoded webhook payload. Every payload type below implements it,
// so a receiver can resolve the installation and the repository without a
// type switch. InstallationID and RepositoryID are 0 when the payload has
// none (a ping, or an installation event, which lists repositories instead).
type Event interface {
	EventName() string
	EventAction() string
	InstallationID() int64
	RepositoryID() int64
}

// ParseEvent decodes body as the payload of the named event. Only the fields
// the typed payloads declare are kept: e-mail addresses and everything else
// GitHub sends are dropped at decoding.
func ParseEvent(name string, body []byte) (Event, error) {
	var ev Event
	switch name {
	case EventPush:
		ev = &PushEvent{}
	case EventCreate:
		ev = &CreateEvent{}
	case EventDelete:
		ev = &DeleteEvent{}
	case EventPullRequest:
		ev = &PullRequestEvent{}
	case EventCheckSuite:
		ev = &CheckSuiteEvent{}
	case EventCheckRun:
		ev = &CheckRunEvent{}
	case EventStatus:
		ev = &StatusEvent{}
	case EventWorkflowRun:
		ev = &WorkflowRunEvent{}
	case EventInstallation:
		ev = &InstallationEvent{}
	case EventInstallationRepositories:
		ev = &InstallationRepositoriesEvent{}
	case EventRepository:
		ev = &RepositoryEvent{}
	case EventPing:
		ev = &PingEvent{}
	default:
		return nil, fmt.Errorf("%w %q", ErrUnknownEvent, name)
	}
	if err := json.Unmarshal(body, ev); err != nil {
		return nil, fmt.Errorf("ghapp: decode %s payload: %w", name, err)
	}
	return ev, nil
}

// Timestamp is a time GitHub sends either as RFC 3339 text or, in some push
// payload fields, as Unix seconds. It encodes as RFC 3339 (null when zero).
type Timestamp struct{ time.Time }

// UnmarshalJSON accepts a string, a number or null.
func (t *Timestamp) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		t.Time = time.Time{}
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		if s == "" {
			t.Time = time.Time{}
			return nil
		}
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return fmt.Errorf("ghapp: timestamp %q: %w", s, err)
		}
		t.Time = v
		return nil
	}
	n, err := strconv.ParseInt(string(data), 10, 64)
	if err != nil {
		return fmt.Errorf("ghapp: timestamp %s: %w", data, err)
	}
	t.Time = time.Unix(n, 0).UTC()
	return nil
}

// MarshalJSON writes RFC 3339, or null for the zero time.
func (t Timestamp) MarshalJSON() ([]byte, error) {
	if t.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(t.UTC().Format(time.RFC3339))
}

// User is a GitHub account (user, organization or bot) as payloads carry it:
// login, numeric id and type. E-mail addresses are never decoded.
type User struct {
	Login string `json:"login"`
	ID    int64  `json:"id"`
	Type  string `json:"type,omitempty"`
}

// Repository is a repository as the REST API and webhook payloads describe it.
type Repository struct {
	ID            int64     `json:"id"`
	NodeID        string    `json:"node_id,omitempty"`
	Name          string    `json:"name"`
	FullName      string    `json:"full_name"`
	Owner         User      `json:"owner"`
	Private       bool      `json:"private"`
	Fork          bool      `json:"fork,omitempty"`
	Archived      bool      `json:"archived,omitempty"`
	Disabled      bool      `json:"disabled,omitempty"`
	Visibility    string    `json:"visibility,omitempty"`
	DefaultBranch string    `json:"default_branch,omitempty"`
	HTMLURL       string    `json:"html_url,omitempty"`
	CreatedAt     Timestamp `json:"created_at"`
	PushedAt      Timestamp `json:"pushed_at"`
}

// RepositoryRef is the short repository form installation payloads list.
type RepositoryRef struct {
	ID       int64  `json:"id"`
	NodeID   string `json:"node_id,omitempty"`
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Private  bool   `json:"private"`
}

// InstallationRef is the installation stamped on every delivery made to an
// app.
type InstallationRef struct {
	ID     int64  `json:"id"`
	NodeID string `json:"node_id,omitempty"`
}

// Installation is an app installation (GET /app/installations/{id} and the
// installation events).
type Installation struct {
	ID                  int64             `json:"id"`
	Account             User              `json:"account"`
	AppID               int64             `json:"app_id,omitempty"`
	AppSlug             string            `json:"app_slug,omitempty"`
	TargetID            int64             `json:"target_id,omitempty"`
	TargetType          string            `json:"target_type,omitempty"`
	RepositorySelection string            `json:"repository_selection,omitempty"`
	Permissions         map[string]string `json:"permissions,omitempty"`
	Events              []string          `json:"events,omitempty"`
	HTMLURL             string            `json:"html_url,omitempty"`
	CreatedAt           Timestamp         `json:"created_at"`
	UpdatedAt           Timestamp         `json:"updated_at"`
	SuspendedAt         Timestamp         `json:"suspended_at"`
}

// Suspended reports whether the installation is suspended.
func (i *Installation) Suspended() bool { return !i.SuspendedAt.IsZero() }

// Envelope holds what most deliveries share: the action, the repository, the
// installation and the sender.
type Envelope struct {
	Action       string           `json:"action,omitempty"`
	Repository   *Repository      `json:"repository,omitempty"`
	Installation *InstallationRef `json:"installation,omitempty"`
	Sender       *User            `json:"sender,omitempty"`
}

// EventAction is the payload's action ("" for events without one).
func (e *Envelope) EventAction() string { return e.Action }

// InstallationID is the delivering installation's id, 0 when absent.
func (e *Envelope) InstallationID() int64 {
	if e.Installation == nil {
		return 0
	}
	return e.Installation.ID
}

// RepositoryID is the repository's numeric id, 0 when absent.
func (e *Envelope) RepositoryID() int64 {
	if e.Repository == nil {
		return 0
	}
	return e.Repository.ID
}

// CommitAuthor names a commit's author or committer by name and GitHub login;
// the e-mail address is not decoded.
type CommitAuthor struct {
	Name     string `json:"name,omitempty"`
	Username string `json:"username,omitempty"`
}

// PushCommit is one commit of a push payload.
type PushCommit struct {
	ID        string       `json:"id"`
	TreeID    string       `json:"tree_id,omitempty"`
	Distinct  bool         `json:"distinct"`
	Message   string       `json:"message,omitempty"`
	Timestamp Timestamp    `json:"timestamp"`
	URL       string       `json:"url,omitempty"`
	Author    CommitAuthor `json:"author"`
	Added     []string     `json:"added,omitempty"`
	Removed   []string     `json:"removed,omitempty"`
	Modified  []string     `json:"modified,omitempty"`
}

// Pusher is who pushed; GitHub sends a name (the login for a user) and an
// e-mail address, which is not decoded.
type Pusher struct {
	Name string `json:"name"`
}

// PushEvent is a push to a branch or tag.
type PushEvent struct {
	Envelope
	Ref        string       `json:"ref"`
	Before     string       `json:"before"`
	After      string       `json:"after"`
	Created    bool         `json:"created"`
	Deleted    bool         `json:"deleted"`
	Forced     bool         `json:"forced"`
	BaseRef    string       `json:"base_ref,omitempty"`
	Compare    string       `json:"compare,omitempty"`
	Commits    []PushCommit `json:"commits"`
	HeadCommit *PushCommit  `json:"head_commit,omitempty"`
	Pusher     Pusher       `json:"pusher"`
}

// EventName is "push".
func (*PushEvent) EventName() string { return EventPush }

// Branch is the pushed branch's name, false when the ref is not a branch.
func (e *PushEvent) Branch() (string, bool) { return strings.CutPrefix(e.Ref, BranchRefPrefix) }

// CreateEvent is a branch or tag created.
type CreateEvent struct {
	Envelope
	Ref          string `json:"ref"`
	RefType      string `json:"ref_type"`
	MasterBranch string `json:"master_branch,omitempty"`
	PusherType   string `json:"pusher_type,omitempty"`
}

// EventName is "create".
func (*CreateEvent) EventName() string { return EventCreate }

// DeleteEvent is a branch or tag deleted.
type DeleteEvent struct {
	Envelope
	Ref        string `json:"ref"`
	RefType    string `json:"ref_type"`
	PusherType string `json:"pusher_type,omitempty"`
}

// EventName is "delete".
func (*DeleteEvent) EventName() string { return EventDelete }

// PullRequestBranch is a pull request's head or base: the ref, the commit and
// the repository it lives in (nil when that repository was deleted).
type PullRequestBranch struct {
	Label string      `json:"label,omitempty"`
	Ref   string      `json:"ref"`
	SHA   string      `json:"sha"`
	User  *User       `json:"user,omitempty"`
	Repo  *Repository `json:"repo,omitempty"`
}

// PullRequest is a pull request as GET /repos/{o}/{r}/pulls and the
// pull_request event describe it.
type PullRequest struct {
	ID             int64             `json:"id"`
	Number         int               `json:"number"`
	State          string            `json:"state"`
	Title          string            `json:"title,omitempty"`
	Draft          bool              `json:"draft,omitempty"`
	Merged         bool              `json:"merged,omitempty"`
	MergeCommitSHA string            `json:"merge_commit_sha,omitempty"`
	HTMLURL        string            `json:"html_url,omitempty"`
	User           *User             `json:"user,omitempty"`
	Head           PullRequestBranch `json:"head"`
	Base           PullRequestBranch `json:"base"`
	CreatedAt      Timestamp         `json:"created_at"`
	UpdatedAt      Timestamp         `json:"updated_at"`
	ClosedAt       Timestamp         `json:"closed_at"`
	MergedAt       Timestamp         `json:"merged_at"`
}

// PullRequestEvent is a pull request opened, synchronized, closed, reopened,
// marked ready for review and so on (Action).
type PullRequestEvent struct {
	Envelope
	Number      int         `json:"number"`
	PullRequest PullRequest `json:"pull_request"`
}

// EventName is "pull_request".
func (*PullRequestEvent) EventName() string { return EventPullRequest }

// AppRef is the GitHub App behind a check suite or check run.
type AppRef struct {
	ID   int64  `json:"id"`
	Slug string `json:"slug,omitempty"`
	Name string `json:"name,omitempty"`
}

// CheckSuite is a check suite of a commit.
type CheckSuite struct {
	ID         int64     `json:"id"`
	HeadBranch string    `json:"head_branch,omitempty"`
	HeadSHA    string    `json:"head_sha"`
	Status     string    `json:"status,omitempty"`
	Conclusion string    `json:"conclusion,omitempty"`
	URL        string    `json:"url,omitempty"`
	App        *AppRef   `json:"app,omitempty"`
	CreatedAt  Timestamp `json:"created_at"`
	UpdatedAt  Timestamp `json:"updated_at"`
}

// CheckSuiteEvent is a check suite requested, rerequested or completed.
type CheckSuiteEvent struct {
	Envelope
	CheckSuite CheckSuite `json:"check_suite"`
}

// EventName is "check_suite".
func (*CheckSuiteEvent) EventName() string { return EventCheckSuite }

// CheckRun is one check of a check suite.
type CheckRun struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	HeadSHA     string    `json:"head_sha"`
	Status      string    `json:"status,omitempty"`
	Conclusion  string    `json:"conclusion,omitempty"`
	HTMLURL     string    `json:"html_url,omitempty"`
	DetailsURL  string    `json:"details_url,omitempty"`
	StartedAt   Timestamp `json:"started_at"`
	CompletedAt Timestamp `json:"completed_at"`
	CheckSuite  *struct {
		ID         int64  `json:"id"`
		HeadBranch string `json:"head_branch,omitempty"`
	} `json:"check_suite,omitempty"`
	App *AppRef `json:"app,omitempty"`
}

// CheckRunEvent is a check run created, completed, rerequested or with a
// requested action.
type CheckRunEvent struct {
	Envelope
	CheckRun CheckRun `json:"check_run"`
}

// EventName is "check_run".
func (*CheckRunEvent) EventName() string { return EventCheckRun }

// StatusBranch is a branch whose tip a commit status was set on.
type StatusBranch struct {
	Name   string `json:"name"`
	Commit struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

// StatusEvent is a commit status set (it has no action).
type StatusEvent struct {
	Envelope
	ID          int64          `json:"id"`
	SHA         string         `json:"sha"`
	Name        string         `json:"name,omitempty"`
	State       string         `json:"state"`
	Context     string         `json:"context,omitempty"`
	Description string         `json:"description,omitempty"`
	TargetURL   string         `json:"target_url,omitempty"`
	Branches    []StatusBranch `json:"branches,omitempty"`
	CreatedAt   Timestamp      `json:"created_at"`
	UpdatedAt   Timestamp      `json:"updated_at"`
}

// EventName is "status".
func (*StatusEvent) EventName() string { return EventStatus }

// WorkflowRun is one run of a GitHub Actions workflow.
type WorkflowRun struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name,omitempty"`
	WorkflowID int64     `json:"workflow_id,omitempty"`
	HeadBranch string    `json:"head_branch,omitempty"`
	HeadSHA    string    `json:"head_sha"`
	Event      string    `json:"event,omitempty"`
	Status     string    `json:"status,omitempty"`
	Conclusion string    `json:"conclusion,omitempty"`
	RunNumber  int       `json:"run_number,omitempty"`
	RunAttempt int       `json:"run_attempt,omitempty"`
	HTMLURL    string    `json:"html_url,omitempty"`
	Actor      *User     `json:"actor,omitempty"`
	CreatedAt  Timestamp `json:"created_at"`
	UpdatedAt  Timestamp `json:"updated_at"`
}

// Workflow is the workflow a run belongs to.
type Workflow struct {
	ID   int64  `json:"id"`
	Name string `json:"name,omitempty"`
	Path string `json:"path,omitempty"`
}

// WorkflowRunEvent is a workflow run requested, in progress or completed.
type WorkflowRunEvent struct {
	Envelope
	WorkflowRun WorkflowRun `json:"workflow_run"`
	Workflow    *Workflow   `json:"workflow,omitempty"`
}

// EventName is "workflow_run".
func (*WorkflowRunEvent) EventName() string { return EventWorkflowRun }

// InstallationEvent is the app installed (created), uninstalled (deleted),
// suspended (suspend), unsuspended (unsuspend) or its new permissions
// accepted (new_permissions_accepted).
type InstallationEvent struct {
	Action       string          `json:"action"`
	Installation Installation    `json:"installation"`
	Repositories []RepositoryRef `json:"repositories,omitempty"`
	Requester    *User           `json:"requester,omitempty"`
	Sender       *User           `json:"sender,omitempty"`
}

// EventName is "installation".
func (*InstallationEvent) EventName() string { return EventInstallation }

// EventAction is the action.
func (e *InstallationEvent) EventAction() string { return e.Action }

// InstallationID is the installation's id.
func (e *InstallationEvent) InstallationID() int64 { return e.Installation.ID }

// RepositoryID is 0: the event lists repositories instead.
func (*InstallationEvent) RepositoryID() int64 { return 0 }

// InstallationRepositoriesEvent is repositories added to or removed from an
// installation's selection.
type InstallationRepositoriesEvent struct {
	Action              string          `json:"action"`
	Installation        Installation    `json:"installation"`
	RepositorySelection string          `json:"repository_selection,omitempty"`
	RepositoriesAdded   []RepositoryRef `json:"repositories_added"`
	RepositoriesRemoved []RepositoryRef `json:"repositories_removed"`
	Requester           *User           `json:"requester,omitempty"`
	Sender              *User           `json:"sender,omitempty"`
}

// EventName is "installation_repositories".
func (*InstallationRepositoriesEvent) EventName() string { return EventInstallationRepositories }

// EventAction is the action ("added" or "removed").
func (e *InstallationRepositoriesEvent) EventAction() string { return e.Action }

// InstallationID is the installation's id.
func (e *InstallationRepositoriesEvent) InstallationID() int64 { return e.Installation.ID }

// RepositoryID is 0: the event lists repositories instead.
func (*InstallationRepositoriesEvent) RepositoryID() int64 { return 0 }

// RepositoryChanges is what a repository event's changes object says about a
// rename, a transfer or a default branch edit.
type RepositoryChanges struct {
	Repository *struct {
		Name *struct {
			From string `json:"from"`
		} `json:"name,omitempty"`
	} `json:"repository,omitempty"`
	Owner *struct {
		From struct {
			User         *User `json:"user,omitempty"`
			Organization *User `json:"organization,omitempty"`
		} `json:"from"`
	} `json:"owner,omitempty"`
	DefaultBranch *struct {
		From string `json:"from"`
	} `json:"default_branch,omitempty"`
}

// RepositoryEvent is a repository created, deleted, archived, unarchived,
// edited, renamed, transferred, publicized or privatized.
type RepositoryEvent struct {
	Envelope
	Changes *RepositoryChanges `json:"changes,omitempty"`
}

// EventName is "repository".
func (*RepositoryEvent) EventName() string { return EventRepository }

// PreviousName is the repository's name before a rename, "" otherwise.
func (e *RepositoryEvent) PreviousName() string {
	if e.Changes == nil || e.Changes.Repository == nil || e.Changes.Repository.Name == nil {
		return ""
	}
	return e.Changes.Repository.Name.From
}

// PreviousOwner is the owner's login before a transfer, "" otherwise.
func (e *RepositoryEvent) PreviousOwner() string {
	if e.Changes == nil || e.Changes.Owner == nil {
		return ""
	}
	if u := e.Changes.Owner.From.User; u != nil {
		return u.Login
	}
	if o := e.Changes.Owner.From.Organization; o != nil {
		return o.Login
	}
	return ""
}

// PingEvent is sent when the webhook is created; it carries nothing to act on.
type PingEvent struct {
	Envelope
	Zen    string `json:"zen,omitempty"`
	HookID int64  `json:"hook_id,omitempty"`
}

// EventName is "ping".
func (*PingEvent) EventName() string { return EventPing }

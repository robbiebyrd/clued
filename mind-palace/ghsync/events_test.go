package ghsync

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/code/memcode"
	"github.com/robbiebyrd/clued/mind-palace/ghapp"
)

const (
	actRepoID   int64 = 812345678
	actWorktree       = "gh_812345678"
	actSHA1           = "1111111111111111111111111111111111111111"
	actSHA2           = "2222222222222222222222222222222222222222"
	actSHA3           = "3333333333333333333333333333333333333333"
)

var actReceived = time.Date(2026, 10, 19, 13, 0, 0, 0, time.UTC)

// fixture reads a payload from ghapp's webhook fixtures.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "ghapp", "testdata", "events", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// edited returns a fixture with the values at the dotted paths replaced.
func edited(t *testing.T, name string, set map[string]any) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(fixture(t, name), &m); err != nil {
		t.Fatal(err)
	}
	for p, v := range set {
		keys := strings.Split(p, ".")
		cur := m
		for _, k := range keys[:len(keys)-1] {
			next, ok := cur[k].(map[string]any)
			if !ok {
				next = map[string]any{}
				cur[k] = next
			}
			cur = next
		}
		cur[keys[len(keys)-1]] = v
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// activity parses body as the named event and maps it.
func activity(t *testing.T, event string, body []byte) []code.Event {
	t.Helper()
	ev, err := ghapp.ParseEvent(event, body)
	if err != nil {
		t.Fatal(err)
	}
	d := Delivery{ID: "d-1", Event: event, Action: ev.EventAction(), Installation: ev.InstallationID(), Repository: ev.RepositoryID(), ReceivedAt: actReceived}
	return ActivityEvents(d, ev, time.Time{})
}

func TestActivityEventsFixtures(t *testing.T) {
	base := code.Event{Delivery: "d-1", Worktree: actWorktree, Repo: "robbiebyrd/clued"}
	with := func(e code.Event) code.Event {
		e.Delivery, e.Worktree = base.Delivery, base.Worktree
		if e.Repo == "" {
			e.Repo = base.Repo
		}
		return e
	}
	cases := []struct {
		fixture, event string
		want           []code.Event
	}{
		{"push", ghapp.EventPush, []code.Event{with(code.Event{Kind: code.EventPush, Ref: "main", Before: actSHA1, Commit: actSHA2, Commits: 2, Actor: "robbiebyrd", At: actReceived})}},
		{"create", ghapp.EventCreate, []code.Event{with(code.Event{Kind: code.EventBranchCreated, Ref: "feature/x", Actor: "robbiebyrd", At: actReceived})}},
		{"delete", ghapp.EventDelete, []code.Event{with(code.Event{Kind: code.EventBranchDeleted, Ref: "feature/x", Actor: "robbiebyrd", At: actReceived})}},
		{"pull_request", ghapp.EventPullRequest, []code.Event{with(code.Event{
			Kind: EventPRClosed, PR: 42, Ref: "feature/x", Commit: actSHA2, BaseRef: "main", BaseCommit: actSHA1,
			Merged: true, MergeCommit: actSHA3, Actor: "robbiebyrd", URL: "https://github.com/robbiebyrd/clued/pull/42",
			At: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC),
		})}},
		{"check_suite", ghapp.EventCheckSuite, []code.Event{with(code.Event{
			Kind: code.EventCI, Commit: actSHA2, Ref: "main", Name: "GitHub Actions", Status: "completed", Conclusion: "success",
			At: time.Date(2026, 10, 19, 12, 4, 0, 0, time.UTC),
		})}},
		{"check_run", ghapp.EventCheckRun, []code.Event{with(code.Event{
			Kind: code.EventCI, Commit: actSHA2, Ref: "main", Name: "test", Status: "completed", Conclusion: "failure",
			URL: "https://github.com/robbiebyrd/clued/actions/runs/1/job/40000001", At: time.Date(2026, 10, 19, 12, 3, 0, 0, time.UTC),
		})}},
		{"workflow_run", ghapp.EventWorkflowRun, []code.Event{with(code.Event{
			Kind: code.EventCI, Commit: actSHA2, Ref: "main", Name: "mind-palace", Status: "completed", Conclusion: "success",
			Actor: "robbiebyrd", URL: "https://github.com/robbiebyrd/clued/actions/runs/60000001", At: time.Date(2026, 10, 19, 12, 10, 0, 0, time.UTC),
		})}},
		{"status", ghapp.EventStatus, []code.Event{with(code.Event{
			Kind: code.EventCI, Commit: actSHA2, Ref: "main", Name: "ci/external", Status: "success",
			At: time.Date(2026, 10, 19, 12, 6, 0, 0, time.UTC),
		})}},
		{"repository", ghapp.EventRepository, []code.Event{with(code.Event{Kind: EventRepoRenamed, Repo: "robbiebyrd/clued2", Name: "robbiebyrd/clued", Actor: "robbiebyrd", At: actReceived})}},
		{"repository_transferred", ghapp.EventRepository, []code.Event{with(code.Event{Kind: EventRepoTransferred, Name: "space-hippo/clued", Actor: "robbiebyrd", At: actReceived})}},
		{"installation", ghapp.EventInstallation, nil},
		{"installation_repositories", ghapp.EventInstallationRepositories, nil},
		{"ping", ghapp.EventPing, nil},
	}
	for _, c := range cases {
		t.Run(c.fixture, func(t *testing.T) {
			got := activity(t, c.event, fixture(t, c.fixture))
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got  %+v\nwant %+v", got, c.want)
			}
			for _, e := range got {
				if err := e.Validate(); err != nil {
					t.Fatalf("invalid event: %v", err)
				}
			}
		})
	}
}

// TestActivityEventsNoEmail checks no e-mail address and no payload text
// beyond the allow-list reaches an event, for every fixture.
func TestActivityEventsNoEmail(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("..", "ghapp", "testdata", "events"))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{"repository_transferred": ghapp.EventRepository}
	for _, ent := range entries {
		name := strings.TrimSuffix(ent.Name(), ".json")
		event := names[name]
		if event == "" {
			event = name
		}
		body := fixture(t, name)
		for _, e := range activity(t, event, body) {
			b, err := json.Marshal(e)
			if err != nil {
				t.Fatal(err)
			}
			for _, banned := range []string{"@", "Fix ", "message", "api.github.com"} {
				if strings.Contains(string(b), banned) {
					t.Errorf("%s: event holds %q: %s", name, banned, b)
				}
			}
		}
	}
}

func TestActivityEventsPullRequestActions(t *testing.T) {
	for _, action := range []string{"opened", "synchronize", "reopened", "ready_for_review"} {
		got := activity(t, ghapp.EventPullRequest, edited(t, "pull_request", map[string]any{
			"action": action, "pull_request.merged": false, "pull_request.merge_commit_sha": actSHA3,
		}))
		if len(got) != 1 || got[0].Kind != "pr_"+action || got[0].Merged || got[0].MergeCommit != "" || got[0].Ref != "feature/x" || got[0].PR != 42 {
			t.Fatalf("%s: %+v", action, got)
		}
	}
	for _, action := range []string{"edited", "labeled", "assigned", "review_requested", "converted_to_draft"} {
		if got := activity(t, ghapp.EventPullRequest, edited(t, "pull_request", map[string]any{"action": action})); len(got) != 0 {
			t.Fatalf("%s recorded %+v", action, got)
		}
	}
	// A head branch in a fork keeps its commit but not its ref.
	got := activity(t, ghapp.EventPullRequest, edited(t, "pull_request", map[string]any{"action": "opened", "pull_request.head.repo.id": 99}))
	if len(got) != 1 || got[0].Ref != "" || got[0].Commit != actSHA2 {
		t.Fatalf("fork: %+v", got)
	}
}

func TestActivityEventsIgnored(t *testing.T) {
	cases := []struct {
		name, event string
		body        []byte
	}{
		{"tag push", ghapp.EventPush, edited(t, "push", map[string]any{"ref": "refs/tags/v1.0.0"})},
		{"deleting push", ghapp.EventPush, edited(t, "push", map[string]any{"deleted": true, "after": ghapp.ZeroSHA})},
		{"tag create", ghapp.EventCreate, edited(t, "create", map[string]any{"ref_type": "tag", "ref": "v1"})},
		{"tag delete", ghapp.EventDelete, edited(t, "delete", map[string]any{"ref_type": "tag", "ref": "v1"})},
		{"check suite requested", ghapp.EventCheckSuite, edited(t, "check_suite", map[string]any{"action": "requested"})},
		{"check run created", ghapp.EventCheckRun, edited(t, "check_run", map[string]any{"action": "created"})},
		{"workflow run in progress", ghapp.EventWorkflowRun, edited(t, "workflow_run", map[string]any{"action": "in_progress"})},
		{"repository edited", ghapp.EventRepository, edited(t, "repository", map[string]any{"action": "edited"})},
		{"repository publicized", ghapp.EventRepository, edited(t, "repository", map[string]any{"action": "publicized"})},
	}
	for _, c := range cases {
		if got := activity(t, c.event, c.body); len(got) != 0 {
			t.Errorf("%s recorded %+v", c.name, got)
		}
	}
}

func TestActivityEventsOtherActions(t *testing.T) {
	for _, action := range []string{"archived", "deleted"} {
		got := activity(t, ghapp.EventRepository, edited(t, "repository", map[string]any{"action": action}))
		if len(got) != 1 || got[0].Kind != "repo_"+action || got[0].Name != "" {
			t.Fatalf("%s: %+v", action, got)
		}
	}
	got := activity(t, ghapp.EventPush, edited(t, "push", map[string]any{"forced": true, "created": true, "before": ghapp.ZeroSHA, "ref": "refs/heads/feature/y"}))
	if len(got) != 1 || !got[0].Forced || got[0].Before != ghapp.ZeroSHA || got[0].Ref != "feature/y" {
		t.Fatalf("forced new branch push: %+v", got)
	}
}

func TestActivityEventsFromPayloadAndClock(t *testing.T) {
	now := time.Date(2026, 10, 20, 8, 0, 0, 0, time.FixedZone("x", 3600))
	d := Delivery{ID: "d-2", Event: ghapp.EventCreate, Repository: actRepoID, RepositoryName: "robbiebyrd/clued", Payload: fixture(t, "create")}
	got := ActivityEvents(d, nil, now)
	if len(got) != 1 || got[0].Kind != code.EventBranchCreated || got[0].Delivery != "d-2" || !got[0].At.Equal(now) || got[0].At.Location() != time.UTC {
		t.Fatalf("from payload: %+v", got)
	}
	if got := ActivityEvents(Delivery{ID: "d-3", Event: ghapp.EventPush, Payload: []byte("{")}, nil, now); got != nil {
		t.Fatalf("undecodable payload recorded %+v", got)
	}
	if got := ActivityEvents(Delivery{ID: "d-4", Event: "fork", Payload: []byte("{}")}, nil, now); got != nil {
		t.Fatalf("unknown event recorded %+v", got)
	}
	// A payload without a repository falls back to the delivery's.
	body := edited(t, "create", map[string]any{"repository": nil})
	d = Delivery{ID: "d-5", Event: ghapp.EventCreate, Repository: actRepoID, RepositoryName: "robbiebyrd/clued", Payload: body}
	got = ActivityEvents(d, nil, now)
	if len(got) != 1 || got[0].Worktree != actWorktree || got[0].Repo != "robbiebyrd/clued" {
		t.Fatalf("delivery repository: %+v", got)
	}
}

func TestActivityEventsSanitize(t *testing.T) {
	body := edited(t, "pull_request", map[string]any{
		"action":                 "opened",
		"pull_request.head.ref":  "bad..ref",
		"pull_request.base.sha":  "not-a-sha",
		"pull_request.html_url":  "http://github.com/robbiebyrd/clued/pull/42",
		"sender.login":           strings.Repeat("é", 300),
		"pull_request.head.repo": map[string]any{"id": actRepoID},
		"pull_request.number":    -3,
		"repository.full_name":   "robbiebyrd/clued",
	})
	got := activity(t, ghapp.EventPullRequest, body)
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	e := got[0]
	if e.Ref != "" || e.BaseCommit != "" || e.URL != "" || e.PR != 42 || len(e.Actor) > maxActivityNameBytes || !strings.HasPrefix(e.Actor, "é") {
		t.Fatalf("sanitized: %+v", e)
	}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"https://user:pw@github.com/x", "https://github.com/x?token=1", "https://github.com/x#frag", "ftp://github.com/x", "https://" + strings.Repeat("a", maxActivityURLBytes)} {
		if pageURL(u) != "" {
			t.Errorf("kept %q", u)
		}
	}
	if pageURL("https://github.com/o/r/pull/1") == "" {
		t.Error("dropped a page URL")
	}
}

func TestAppendActivity(t *testing.T) {
	ctx := context.Background()
	st := memcode.New()
	ev, err := ghapp.ParseEvent(ghapp.EventPush, fixture(t, "push"))
	if err != nil {
		t.Fatal(err)
	}
	n, err := AppendActivity(ctx, st, Delivery{ID: "d-1", Event: ghapp.EventPush, ReceivedAt: actReceived}, ev, time.Time{})
	if err != nil || n != 1 {
		t.Fatalf("append: %d %v", n, err)
	}
	ping, err := ghapp.ParseEvent(ghapp.EventPing, fixture(t, "ping"))
	if err != nil {
		t.Fatal(err)
	}
	if n, err := AppendActivity(ctx, nil, Delivery{ID: "d-2", Event: ghapp.EventPing}, ping, time.Time{}); err != nil || n != 0 {
		t.Fatalf("ping: %d %v", n, err)
	}
	if _, err := AppendActivity(ctx, nil, Delivery{ID: "d-3", Event: ghapp.EventPush}, ev, time.Time{}); err == nil {
		t.Fatal("nil store accepted")
	}
	got, err := st.Events(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Seq != 1 || got[0].Kind != code.EventPush || got[0].Commit != actSHA2 || got[0].Actor != "robbiebyrd" || got[0].Delivery != "d-1" {
		t.Fatalf("stored %+v", got)
	}
}

func TestDocsActivity(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	docs := []DocsEvent{
		{Kind: EventPlanSync, Repo: "robbiebyrd/clued", Commit: actSHA2, Delivery: "d-1", DocKind: "plan", ID: "0011-gha", Path: "docs/plans/0011-gha.md", Action: DocsCreated, At: at},
		{Kind: EventPlanSyncConflict, Repo: "robbiebyrd/clued", Commit: actSHA2, DocKind: "story", ID: "0073-2eu", Path: "docs/stories/0073-2eu.md", At: at},
		{Kind: EventPlanSyncProblem, Repo: "robbiebyrd/clued", Commit: actSHA2, Path: "../escape", Problems: []string{"bad config"}, At: at},
		{Kind: "Not A Kind"},
	}
	got := DocsActivityEvents(actRepoID, docs)
	want := []code.Event{
		{Kind: code.EventPlanSync, Delivery: "d-1", Worktree: actWorktree, Repo: "robbiebyrd/clued", Commit: actSHA2, DocKind: "plan", DocID: "0011-gha", Path: "docs/plans/0011-gha.md", Action: DocsCreated, At: at},
		{Kind: code.EventPlanSyncConflict, Worktree: actWorktree, Repo: "robbiebyrd/clued", Commit: actSHA2, DocKind: "story", DocID: "0073-2eu", Path: "docs/stories/0073-2eu.md", At: at},
		{Kind: code.EventPlanSyncProblem, Worktree: actWorktree, Repo: "robbiebyrd/clued", Commit: actSHA2, Problems: []string{"bad config"}, At: at},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}

	st := memcode.New()
	var opened []string
	rec := ActivityDocsRecorder(func(_ context.Context, target Target, slug string) (code.Store, error) {
		opened = append(opened, string(target.Kind)+":"+target.ID+":"+slug)
		return st, nil
	}, actRepoID)
	target := Target{Kind: TargetUser, ID: "u1", Owner: "u1"}
	if err := rec(ctx, target, "github.com__robbiebyrd__clued", docs); err != nil {
		t.Fatal(err)
	}
	if err := rec(ctx, target, "github.com__robbiebyrd__clued", nil); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 1 || opened[0] != "user:u1:github.com__robbiebyrd__clued" {
		t.Fatalf("opened %v", opened)
	}
	stored, err := st.Events(ctx, 0, 10)
	if err != nil || len(stored) != 3 {
		t.Fatalf("stored %d %v", len(stored), err)
	}
	boom := errors.New("boom")
	failing := ActivityDocsRecorder(func(context.Context, Target, string) (code.Store, error) { return nil, boom }, actRepoID)
	if err := failing(ctx, target, "s", docs); !errors.Is(err, boom) {
		t.Fatalf("open error: %v", err)
	}
}

func TestOpenPRs(t *testing.T) {
	o := OpenPRs{}
	o.Apply(code.Event{Kind: EventPROpened, PR: 1, Ref: "feature/a"})
	o.Apply(code.Event{Kind: EventPROpened, PR: 2, Ref: "feature/b"})
	o.Apply(code.Event{Kind: EventPRReopened, PR: 3, Ref: "feature/a"})
	o.Apply(code.Event{Kind: EventPROpened, PR: 4})               // fork: no ref
	o.Apply(code.Event{Kind: code.EventPush, PR: 0, Ref: "main"}) // not a pull request
	if got := o.Branches(); !reflect.DeepEqual(got, []string{"feature/a", "feature/b"}) {
		t.Fatalf("branches %v", got)
	}
	o.Apply(code.Event{Kind: EventPRClosed, PR: 1, Ref: "feature/a"})
	if !o.Has("feature/a") {
		t.Fatal("feature/a still has pull request 3")
	}
	o.Apply(code.Event{Kind: EventPRClosed, PR: 3, Ref: "feature/a"})
	o.Apply(code.Event{Kind: EventPRClosed, PR: 3, Ref: "feature/a"}) // redelivered
	if o.Has("feature/a") || !o.Has("feature/b") {
		t.Fatalf("after close: %v", o)
	}

	// The branch policy's active rule sees the open pull request.
	now := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	branches := []Branch{{Name: "main"}, {Name: "feature/b"}, {Name: "feature/c", LastPush: now.AddDate(0, 0, -30)}}
	marked := o.Mark(branches)
	if branches[1].OpenPR {
		t.Fatal("Mark modified its input")
	}
	sel := DefaultBranchPolicy().Select("main", marked, now)
	if !reflect.DeepEqual(sel.Active, []string{"main", "feature/b"}) || !reflect.DeepEqual(sel.Inactive, []string{"feature/c"}) {
		t.Fatalf("selection %+v", sel)
	}

	list := []ghapp.PullRequest{
		{Number: 7, State: "open", Head: ghapp.PullRequestBranch{Ref: "feature/x", Repo: &ghapp.Repository{ID: actRepoID}}},
		{Number: 8, State: "open", Head: ghapp.PullRequestBranch{Ref: "fork-branch", Repo: &ghapp.Repository{ID: 5}}},
		{Number: 9, State: "closed", Head: ghapp.PullRequestBranch{Ref: "old", Repo: &ghapp.Repository{ID: actRepoID}}},
		{Number: 10, State: "open", Head: ghapp.PullRequestBranch{Ref: "gone"}},
	}
	if got := OpenPRsFromList(actRepoID, list); !reflect.DeepEqual(got, OpenPRs{7: "feature/x"}) {
		t.Fatalf("from list %v", got)
	}
}

func TestReplayOpenPRs(t *testing.T) {
	ctx := context.Background()
	st := memcode.New()
	pr := func(action string, number int, repo int64) []code.Event {
		body := edited(t, "pull_request", map[string]any{
			"action": action, "pull_request.number": number, "number": number,
			"pull_request.head.ref": "feature/" + string(rune('a'+number)),
			"repository.id":         repo, "pull_request.head.repo.id": repo,
		})
		ev, err := ghapp.ParseEvent(ghapp.EventPullRequest, body)
		if err != nil {
			t.Fatal(err)
		}
		return ActivityEvents(Delivery{ID: "d", Event: ghapp.EventPullRequest}, ev, actReceived)
	}
	var all []code.Event
	all = append(all, pr("opened", 1, actRepoID)...)
	all = append(all, pr("opened", 2, actRepoID)...)
	all = append(all, pr("opened", 3, 77)...) // another repository in the project
	all = append(all, pr("closed", 1, actRepoID)...)
	all = append(all, pr("labeled", 4, actRepoID)...)
	if err := st.AppendEvents(ctx, all); err != nil {
		t.Fatal(err)
	}
	seed := OpenPRs{9: "feature/seeded"}
	got, last, err := ReplayOpenPRs(ctx, st, actRepoID, seed, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, OpenPRs{2: "feature/c", 9: "feature/seeded"}) || last != 4 {
		t.Fatalf("replay %v last %d", got, last)
	}
	if len(seed) != 1 {
		t.Fatal("seed modified")
	}
	if err := st.AppendEvents(ctx, pr("closed", 2, actRepoID)); err != nil {
		t.Fatal(err)
	}
	got, last, err = ReplayOpenPRs(ctx, st, actRepoID, got, last, 0)
	if err != nil || last != 5 || !reflect.DeepEqual(got, OpenPRs{9: "feature/seeded"}) {
		t.Fatalf("incremental %v last %d err %v", got, last, err)
	}
	if _, _, err := ReplayOpenPRs(ctx, nil, actRepoID, nil, 0, 0); err == nil {
		t.Fatal("nil store accepted")
	}
}

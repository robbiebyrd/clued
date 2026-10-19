# The clued GitHub App

The clued GitHub App keeps a project on a clued server up to date from
GitHub itself, whether or not anyone is running Claude Code: the pushed code
of each repository (the base and branch layers of the code store), its plans
and stories from the default branch, and a log of what happened in it
(pushes, branches, pull requests, CI results).

**Information flows one way, GitHub → clued.** The app asks for read-only
permissions and never writes to GitHub: no check runs, commit statuses,
comments, issues, labels or commits. clued sends no notification about CI
results or sync outcomes either; the only messages are the existing security
notifications when an installation is claimed or removed. What the app
collects is kept for later use (the code graph, the board, memory) and
**stays until it is wiped** (see [Uninstalling and deleting](#uninstalling-and-deleting)).

The rules live in the public module: `mind-palace/ghapp` (the GitHub App
plumbing: app JWT, installation tokens, webhook signature verification, a
small REST client, typed webhook events, and the `ghapptest` fake GitHub)
and `mind-palace/ghsync` (the sync: claims, code, plans and stories, the
activity log, the branch policy, the job queue port). Clued Cloud composes
them: the webhook endpoint, the install and claim pages, a MongoDB job queue
and sync state, entitlements and purgers. A self-hosted `mind-palace server
run` does not wire the app; its clients keep uploading code themselves
(`codeSync`, see the [README](../README.md#code-sync)).

## Permissions and events

One app per deployment (Clued Cloud's is registered by Space Hippo, LC).

| Permission | Access | Why |
|---|---|---|
| Metadata | read | Repositories, branches, the installation's selection |
| Contents | read | Trees, blobs, comparisons, file contents, and the `push`, `create` and `delete` events |
| Pull requests | read | Open pull requests (for the branch policy) and `pull_request` events |
| Checks | read | `check_suite` and `check_run` events |
| Commit statuses | read | `status` events |
| Actions | read | `workflow_run` events |

Subscribed events: `installation`, `installation_repositories`,
`repository`, `push`, `create`, `delete`, `pull_request`, `check_suite`,
`check_run`, `status`, `workflow_run` (and the `ping` GitHub sends when the
webhook is set up, which is acknowledged and ignored).

The REST calls the workers make through an installation token are reads
only: the installation's repositories, a repository by id, its branches
and one branch, the open pull requests, `compare/{before}...{after}`,
`git/trees/{sha}` (recursive, or one level when GitHub truncates),
`git/blobs/{sha}` and `contents/{path}?ref=` (the config file, plans and
stories). The checks, statuses and workflow runs are taken from the
webhook payloads only; the app never fetches CI logs or artifacts.

Clued Cloud's app: **Webhook URL** `https://clued.cloud/github/webhook`,
**Setup URL** `https://clued.cloud/settings/integrations/github/setup`,
request-user-authorization off (signing in with GitHub already links a
GitHub identity).

## Installing and claiming

An installation only ever writes into the namespace of the clued account or
workspace that **claimed** it. Until then its deliveries are acknowledged
and dropped. An installation belongs to exactly one target; a second
account or workspace cannot claim it.

1. **From Clued Cloud** (the normal path). Settings → Integrations →
   *Install on GitHub* (or the same button on a workspace's settings, for
   its admins) is a POST form that stores a one-time state (only its
   SHA-256, for 15 minutes, bound to the web session and to the target: the
   user, or a workspace the user administers) and sends the browser to the
   app's install page. GitHub sends the user back to the setup URL with
   `installation_id` and the state. The claim succeeds only when the state
   is live and belongs to this user and session, the user may still act
   for the target, the installation is not removed or held by another
   target, and it is the installation this install made: installed by a
   GitHub identity linked to the user, or, when the installer is linked to
   nobody, created on GitHub after the state was issued. An installer
   linked to another clued user is refused.
2. **Installed on GitHub directly** (no state). The setup URL asks the
   signed-in user to confirm the target. The claim succeeds only when the
   installation was recorded from its `installation.created` delivery and
   its installer (the delivery's sender) is a GitHub identity linked to
   the user.

Every refusal shows the same "nothing to claim" notice, whether or not the
installation exists. A claim queues a full import of every repository of
the installation (`ghsync.Service.EnqueueImport`); deliveries that arrived
before the claim were dropped, and the import makes up for them.

The choice between all repositories and selected ones is made on GitHub's
install screen; `installation_repositories` deliveries keep the selection
current (added repositories are imported, removed ones stop syncing).

**Repository → project.** A repository syncs into the project
`github.com__<owner>__<repo>` (the slug clients derive from the origin),
unless the `mind-palace.config.yaml` on its default branch sets
`remote.project`, which wins, exactly as on the client. The project lives
under the target's namespace (`u<user id>__<slug>`, or `w<workspace
id>__<slug>` for a workspace), so the namespace rules of the code store and
the plan and story store apply unchanged. GitHub's numeric repository id is
the key: after a rename or transfer the sync reads the configuration again
and writes to the new name's slug from then on (unless `remote.project`
pins it); the old project stays as it was until it is deleted, and nothing
is moved.

## Webhook intake

`POST /github/webhook` (Clued Cloud; outside every auth middleware, since
GitHub's signature is the authentication). PROTOCOL.md section 7.13 has the
exact answers.

1. The body is capped at 25 MiB (GitHub's maximum). Without
   `X-Hub-Signature-256`, or when the HMAC-SHA256 of the raw body under the
   webhook secret does not match (`hmac.Equal`), the delivery is refused
   (401) before anything in it is read. A signed delivery without a valid
   `X-GitHub-Delivery` or whose payload does not decode is 400.
2. An event the app does not handle, the `ping`, and a payload naming no
   installation are answered 200 and dropped.
3. Installation deliveries record what GitHub says about the installation
   (its account, the installer, suspension, removal) whether or not it is
   claimed; the claim itself is never changed by a delivery.
4. Deliveries are deduped by `X-GitHub-Delivery` (kept 7 days): a
   redelivery answers 200 and does nothing.
5. A delivery for an unknown, unclaimed, removed, suspended or not entitled
   installation is answered 200 and dropped: nothing is queued.
6. Otherwise (after recording an `installation_repositories` change of the
   selection) the minimal job is queued: delivery id, event, action,
   installation, repository id and name, ref, before and after, sender
   login, and the allow-listed fields of the typed event, never the raw
   body. The answer is 202, well inside GitHub's 10 seconds; no GitHub API
   call happens on the request path.

Payloads are never logged: logs carry the delivery id, the event name and
the outcome.

**Workers** drain the queue, one job per repository at a time in delivery
order. A failed job is retried with exponential back-off (30 s, doubling,
at most 1 hour) and dead-lettered after 10 attempts; a GitHub rate limit
(`Retry-After`, `X-RateLimit-Reset`) pauses that installation's jobs until
it resets instead of failing them. A job for something that is no longer
claimed, or gone on GitHub (404), is finished without doing anything. A
**nightly reconcile** compares each synced branch's tip with the stored
head and re-imports drifted ones, drops branches deleted on GitHub and
brings the plans and stories to the default branch's tip, which covers
lost or out-of-order deliveries; a **daily sweep** marks the branches that
turned inactive.

## Code sync from GitHub

Each claimed repository has one **GitHub-backed worktree** in its project's
code store: id `gh_<repository id>`, source `github`, host `github.com`,
origin `https://github.com/<owner>/<repo>`, the repository's default branch,
and as owner the claiming user (or the workspace admin who claimed it). Its
trees are written with the same `code.Store` calls as a client's: every
entry is clean and pushed (`dirty: false`, `unpushed: false`) with GitHub's
blob id as `git_oid`, each change has `head = upstream = <commit>`,
`ahead = 0` and no session.

- **A push** to a synced branch whose `before` is the stored head is
  applied from `compare/{before}...{after}`: changed files are upserted,
  removed ones deleted, a rename is a delete plus an upsert. Blobs the
  project already holds (by git blob id) are not fetched again.
- **A full import** of the branch at `after` replaces the branch's tree
  instead when the push is forced, creates the branch, does not start at
  the stored head (a lost or out-of-order delivery), or the comparison is
  truncated (300 files or more) or cannot be applied. A full import reads
  the recursive tree, walking subtrees when GitHub truncates it.
- **A branch deleted** on GitHub (`delete`, or a push whose `after` is all
  zeros) loses its tree (a tombstone change on the worktree's ref).
- **Tags** are ignored. History before the installation is not replayed:
  an import takes each branch's current tip.

**Filtering and redaction** run on the server exactly as on the client:
the built-in deny list (`.env`, `*.pem`, `*.key`, `id_rsa*`,
`credentials*.json`, …; these paths are not stored at all), the 1 MiB
content cap and binary detection (such files are entries with
`sha256: ""` and their size), submodules as entries of mode `160000`
without content, and the built-in redaction rules plus the account's
`redactPatterns` applied to content before it is hashed and stored.
`excludePaths` does not apply (it names local paths), and `.gitignore` is
moot (only committed files exist). A repository whose
`mind-palace.config.yaml` on the default branch says `codeSync: false`
gets no code from GitHub; a config file that does not parse holds code
sync back until it does (rather than risk ignoring a `codeSync: false`).
Code also needs the target's entitlement and room in its code quota; when
it is missing, plans, stories and the activity log still sync.

### Branch policy

Which branches sync is the repository's **branch policy**:

| `branches` | Synced |
|---|---|
| `active` (default) | The default branch, plus every *active* branch: one with an open pull request from the repository itself (not a fork), or one pushed to in the last `activeDays` days (default 14) |
| `default` | The default branch only |
| `all` | Every branch, up to `maxBranches` (default 200, the default branch included); the least recently pushed beyond that are inactive |

It is set in two places, per key:

- On Clued Cloud's Integrations page, per installation and per repository
  (a repository's setting over its installation's; `activeDays` at most
  365, `maxBranches` at most 1,000 there).
- In the repository's `mind-palace.config.yaml` **on the default branch**,
  which wins for each key it sets, since it is reviewed with the code:

  ```yaml
  github:
    branches: active      # active, default or all
    activeDays: 14        # how recent a push keeps a branch active (positive)
    maxBranches: 200      # the cap under branches: all (positive)
  ```

  Any key left out falls back to the page, then to the default. A value
  out of range makes the file invalid for the sync (code held back, a
  `plan_sync_problem` recorded); the client's own loader rejects it too.

A push to an inactive branch, or a pull request opened from it, makes it
active again (under `active`), and its tree is brought up to date (a full
import when the stored head is stale or missing). A branch that turns
inactive (its pull request closed and no push for `activeDays`) stops
syncing at the next daily sweep, but **its last tree is kept**: only a
branch deleted on GitHub, or a wipe, removes it.

### Clients on a GitHub-backed project

`GET …/code/capabilities` answers `"github": true` for a project that a
claimed, active installation supplies. The Claude Code client (`codeSync`)
reads it on each full reconcile and then sends **clean, pushed** files
(tracked, unchanged since HEAD, and HEAD pushed) as entries without content
(`sha256: ""`, their `git_oid` and size): it neither reads, hashes nor
uploads them. Dirty and unpushed files (the session overlays) are uploaded
in full as before. When the project stops being GitHub-backed, the next
full reconcile reads and uploads those files again. See PROTOCOL.md
sections 4.4.1 and 7.12.

## Plans and stories

On a push to the default branch that touches the plans or stories
directories, or the config file at the root, on every import and on the
nightly reconcile, the sync reads the repository's plans and stories at the
default branch's tip and writes them into the project's plan and story
store (the one the board and the MCP tools use):

1. The directories come from that commit's `mind-palace.config.yaml`
   (`plans.dir`, `stories.dir`; `docs/plans` and `docs/stories` by
   default), each with its `archive/` subdirectory. Files over 1 MiB, files
   that do not parse, duplicate ids and documents invalid under the
   repository's configured kinds are skipped and recorded as
   `plan_sync_problem` events; they are never written. A config file that
   does not parse syncs nothing and is itself a `plan_sync_problem`.
2. The rest is synced with the conflict mode **`newer`**: the repository's
   copy replaces the server's when its `updated` timestamp is the same or
   later (`plan_sync`, `created` or `updated`), and is skipped when the
   server's copy is newer, i.e. a change made through the MCP tools that has
   not been merged yet (`plan_sync_conflict`, naming the id). A copy with no
   readable `updated` is never taken to be the newer one.
3. A document whose file was there at the previous synced commit and is
   gone now is **archived** on the server (`status: archived` through the
   service, not deleted), so links from other documents keep resolving
   (`plan_sync`, `archived`). Moving a file, into `archive/` or elsewhere in
   the directories, is not a deletion.

After a sync the server holds every id from the repository, so new ids
allocated through the MCP tools follow the repository's real maximum.
Plans and stories on other branches are not synced (they are proposals
until merged), and **nothing is ever written back to GitHub**: a status
change made through the tools reaches the repository through a normal
commit.

The same mode is available to anyone: `mind-palace sync --from <store> --to
<store> --on-conflict newer` (or the `sync` MCP tool with `onConflict:
"newer"`).

## Activity log

Every delivery about a repository adds at most one event to the project's
activity log (`code.Store.AppendEvents`, the `<project>_code_events`
collection), holding an allow-listed subset of the payload, never the
payload itself and never an e-mail address:

| Delivery | Event | Kept |
|---|---|---|
| `push` to a branch | `push` | branch, before, after, commit count, forced, pusher login |
| `create` / `delete` of a branch | `branch_created` / `branch_deleted` | branch, sender login |
| `pull_request` opened, synchronize, closed, reopened, ready_for_review | `pr_<action>` | number, head branch (only when it is in the repository itself) and commit, base branch and commit, merged and merge commit, sender login, page URL |
| `check_suite`, `check_run`, `workflow_run` completed; every `status` | `ci` | commit, branch, name (check, workflow or status context), status, conclusion, page URL (none for a status, whose target URL points outside GitHub) |
| `repository` renamed, transferred, archived, deleted | `repo_<action>` | repository, its previous name for a rename or transfer, sender login |
| A plans and stories sync | `plan_sync`, `plan_sync_conflict`, `plan_sync_problem` | commit, kind, id, repository path, action or validation messages (never a document's content) |

Every event carries the delivery id (empty for an import or a reconcile),
the worktree (`gh_<id>`) and `owner/repo`. Tag pushes, other pull request
actions and installation events record nothing. The log is write-only: no
route, page or MCP tool reads it yet.

## What is stored

| Where | What |
|---|---|
| The project's code store (`<project>_code_*`) | The GitHub-backed worktree, a tree per synced branch (paths, sizes, modes, languages, git blob ids), each file's redacted content once by SHA-256, the change log, and the activity log |
| The project's plan and story store | The plans and stories of the default branch, as the MCP tools write them |
| Clued Cloud's account store | The installation (id, GitHub account login and type, installer, the claim, suspension and removal times, branch policy), its repositories (name, state, last sync, branch policy), the one-time install states (hashed, 15 minutes) and the delivery ids (7 days) |
| Clued Cloud's job queue and sync state (`github_jobs`, its leases, pauses and dead letters; per-repository state) | The queued jobs with the minimal delivery subset, and each repository's stored heads, last pushes, open pull requests and the commit the plans and stories were synced from. Dead-lettered jobs are kept 30 days |

Never stored: the raw webhook body, e-mail addresses, CI logs or artifacts,
files on the deny list, unredacted secrets in content, and the app's
credentials. The app's private key and webhook secret are read from files,
held in memory only and never logged; installation tokens live in memory
and expire within an hour.

## Uninstalling and deleting

- **Remove** on the Integrations page only unlinks the installation from
  the account or workspace and links to GitHub to uninstall the app there;
  it deletes no data.
- **Uninstalling** the app on GitHub (`installation.deleted`), suspending
  it, or removing a repository from its selection stops syncing at once:
  the installation or repository is marked removed and its deliveries and
  jobs are dropped. The projects stop being GitHub-backed
  (`capabilities.github` false), and Claude Code clients with `codeSync`
  carry on collecting, uploading clean files again from their next full
  reconcile. Reinstalling and claiming again re-links the same worktree
  (`gh_<repository id>`) and brings it up to date.
- **Data stays until it is wiped.** The GitHub-backed worktree, its trees
  and blobs, the plans and stories and the activity log are kept after an
  uninstall. They go with the project, the account or the workspace when
  that is deleted (the existing purgers); deleting an account or a
  workspace also removes its installations, their repositories, delivery
  records and install states.
- **Opting a repository out of code** without uninstalling: `codeSync:
  false` in its `mind-palace.config.yaml`, or `github.branches: default` to
  keep only the default branch.

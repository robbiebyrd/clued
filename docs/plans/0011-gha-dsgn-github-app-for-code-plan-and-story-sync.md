---
id: 0011-gha
title: A GitHub App that keeps code, plans and stories in sync and records repository activity
type: dsgn
status: pending
priority: "3"
effort: XL
created: "2026-10-19T17:30:17.235Z"
updated: "2026-10-19T17:34:00.360Z"
tags:
  - github
  - code-graph
  - cloud
  - server
  - plans
  - stories
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 0
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
  plans:
    - ["0010-awy", "depends"]
progress:
  "1":
    status: pending
  "1.1":
    status: pending
  "2":
    status: pending
  "2.1":
    status: pending
  "2.2":
    status: pending
  "2.3":
    status: pending
  "2.4":
    status: pending
  "3":
    status: pending
  "3.1":
    status: pending
  "3.2":
    status: pending
  "4":
    status: pending
  "4.1":
    status: pending
---

# A GitHub App that keeps code, plans and stories in sync and records repository activity

## Summary

**Goal:** A user (or a workspace admin) installs the clued GitHub App on their repositories, and from then on the server keeps each repository's pushed code (the base and branch layers of plan 0010), its plans and stories, and a record of its activity (pushes, branches, pull requests, CI status) up to date from GitHub's webhooks, whether or not anyone is running Claude Code.

**Problem:** Plan 0010 only learns about code when a Claude Code session runs in remote mode with `codeSync` on, so the shared base and branch layers are only as fresh as the last session that happened to be on that branch; a push from a terminal, a merged pull request or a teammate without clued never reaches the server. Plans and stories have the same gap: the server store only sees writes made through the MCP tools, so it drifts from `docs/plans` and `docs/stories` on the default branch (seen while writing 0010: the clued.cloud store held four stale test plans and none of 0005 to 0009, and handed out a colliding sequence number). Nothing records what happened in the repository (pushes, pull requests, CI results) for later use by the code graph, the board or memory.

**Approach:** A GitHub App with read-only permissions. Its webhook endpoint verifies each delivery, dedupes it, maps the installation to the clued account or workspace that claimed it, and queues a job. Workers fetch what changed through the installation token and write it through the existing ports: code into `code.Store` as a GitHub-backed worktree (clean, pushed entries with GitHub's blob ids), plans and stories from the default branch into the project's plan and story store with a defined conflict rule, and every event into a write-only activity log. The rules live in a new public-module package (`ghsync`), the GitHub plumbing in `ghapp`, and clued-cloud composes them behind an entitlement, install and claim pages, metrics, audit and purgers. When a project is GitHub-backed, Claude Code clients stop uploading clean, pushed files and send only their overlays.

---

# Part 1 — Design

## Current State

- Plan 0010 (pending) defines `code.Store`, a write-only code API, owner/session stamping and the `git_oid` / `dirty` / `unpushed` / `upstream` facts that build the base, branch and session-overlay layers. Its only writer is the Claude Code client.
- Plans and stories reach the server store only through writes made by clients (`clientcfg.Open` appends the `clued-server` store). `store.Sync` (`mind-palace/store/sync.go`) copies documents between stores with conflict modes `error`, `skip`, `overwrite`, and `Palace.Sync` exposes it.
- clued-cloud signs users in with GitHub OAuth (`signin/signin.go`); a linked identity is `account.Identity{ID: "github:<user id>", Subject, Login}`. It already verifies one provider's webhooks (Stripe, `billing/stripe`), seals secrets with `account.Sealer`, records `Audit*` kinds (`account/audit.go`) and runs purgers on account and workspace deletion.
- Workspaces bind projects (`w<workspace>__<slug>`); project slugs come from `projects.Slug` (`github.com__owner__repo`), overridable per repository by `remote.project` in `mind-palace.config.yaml`.
- Nothing in either repository talks to the GitHub API as an app, verifies GitHub webhooks, or stores repository events.

## Decisions

- **A GitHub App with read-only permissions, not OAuth tokens or deploy keys.** — Apps get per-installation, short-lived tokens, repository selection by the owner, webhooks, and higher rate limits; read-only makes installing it a small trust step and keeps the 'collect, don't expose' posture.
- **GitHub is the source of the base and branch layers when installed; clients send only overlays.** — Pushed code then reflects every push, not just pushes made during a clued session, and machines stop uploading content GitHub already has.
- **The GitHub-backed worktree uses the same `code.Store` calls and fields as a client.** — The indexer (and 0010's tests) see one model; GitHub is just a worktree whose entries are always clean and pushed.
- **Claims bind an installation to the account or workspace that started the install, or to a user whose linked GitHub identity is the installer.** — Installation ids are guessable and installations can be made by anyone on their own repositories; without a bound claim, someone could push code into another account's namespace.
- **Plans and stories flow one way, repository → server, with a `newer` conflict mode and archive-on-delete.** — The repository's default branch is the reviewed source of truth; MCP edits not yet merged must not be overwritten, and deletions must not break links.
- **Rules in the public module (`ghapp`, `ghsync`), composition in clued-cloud.** — Matches the repositories' split (rules in `mind-palace`, the cloud composes) and leaves a self-hosted server able to wire the same packages later.
- **Information flows one way, GitHub → clued.** — The app never posts check runs, statuses or comments to GitHub, and clued sends no status notifications (e-mail or web) about CI results or sync outcomes; events are recorded for later use only. The only messages sent are the existing security notifications for claiming or removing an installation.
- **Uninstalling keeps the data until it is wiped.** — The GitHub-backed worktree, its trees, blobs and activity events stay until the project, account or workspace is deleted (the existing purgers); the projects simply stop being GitHub-backed and the Claude Code clients carry on collecting, including clean files again.
- **Branch sync is configurable, defaulting to active branches plus the default branch.** — Syncing every branch of a busy repository costs imports and storage for branches nobody works on; the default policy follows where work happens, and repositories that want more (or less) can say so.
- **Queue and acknowledge fast; fetch from GitHub in workers; reconcile nightly.** — GitHub requires an answer within 10 seconds and doesn't guarantee delivery or order; the before-tip check and the nightly reconcile make the sync converge regardless.

### Rejected alternatives

- **Clone repositories on the server (`git clone` / `git fetch` with the installation token).** — Needs git and disk on the server and a working copy per repository; the REST trees, compare and blob endpoints give exactly the changed files and their blob ids.
- **Only GitHub, no client upload.** — Misses uncommitted and unpushed work, which is what sessions produce; the overlays still need the client.
- **Two-way plan and story sync (server edits committed back to the repository).** — Needs Contents: write and a commit identity, and races with people's own pushes; status changes already reach the repository through the tools and a normal commit.
- **Process webhooks synchronously.** — API calls on the request path risk GitHub's 10-second timeout and redelivery storms on large pushes.
- **A third-party GitHub client library.** — The endpoints used are few and the standard library covers JWT signing (`crypto/rsa`), HMAC and HTTP; no new linked dependency keeps `go-licenses` and `CGO_ENABLED=0` simple.

## Design

### The GitHub App

One app per deployment (clued.cloud's is registered by Space Hippo, LC). **Permissions, all read-only:** Contents, Metadata, Pull requests, Checks, Commit statuses, Actions. **Events:** `installation`, `installation_repositories`, `repository` (renamed, transferred, deleted, archived), `push`, `create`, `delete`, `pull_request`, `check_suite`, `check_run`, `status`, `workflow_run`.

- **Setup URL**: `https://clued.cloud/settings/integrations/github/setup` with `installation_id` and `setup_action`; **Webhook URL**: `https://clued.cloud/github/webhook`; request-user-authorization off (sign-in already links GitHub identities).
- The user can choose all repositories or selected ones on GitHub's install screen; `installation_repositories` keeps the selection in sync.

### Linking an installation to a clued account (claim)

An installation is only ever written into the namespace of the account or workspace that claimed it; until claimed, deliveries for it are acknowledged and dropped.

1. **From clued.cloud** (the normal path): Settings → Integrations → *Install on GitHub* is a POST form that stores a one-time `state` (hashed, 15 minutes, bound to the web session and to the target: the user, or a workspace the user administers) and redirects to the app's install page with it. GitHub sends the user back to the setup URL with `installation_id` and `state`; the handler checks the state against the session, fetches the installation with the app JWT, and records `Installation{ID, AccountLogin, AccountType, Target: user|workspace, TargetID, ClaimedBy, ClaimedAt}`.
2. **Installed from GitHub directly** (no state): the setup URL asks the signed-in user to confirm. The claim succeeds only when the user has a linked GitHub identity whose subject equals the installation's `sender.id` from the `installation.created` delivery, or (for an organization) the user is an admin of a workspace and the installation's organization is the one the user confirms; otherwise the page shows the same 'nothing to claim' answer whether or not the installation exists.
- An installation belongs to exactly one target. Claiming again from another account is refused the same way. Uninstalling on GitHub (`installation.deleted`) or suspending it stops syncing at once.
- **Repository → project**: `projects.Slug("github.com/<owner>/<repo>")`, unless the repository's `mind-palace.config.yaml` on the default branch sets `remote.project`, which wins (the same rule clients use). Projects are recorded with `RecordProject` (user) or bound to the workspace (`w<id>__<slug>`), so the 0010 namespace rules apply unchanged.
- **Renames and transfers**: GitHub's numeric repository id is the key; a rename keeps the project when `remote.project` pins it, otherwise it starts a new slug and records the old one as an alias for later migration (no data is moved automatically).

### Webhook intake

`POST /github/webhook` (no auth middleware; GitHub's signature is the authentication):
1. Body capped at 25 MiB (GitHub's maximum); reject without `X-Hub-Signature-256` or when HMAC-SHA256 over the raw body with the webhook secret doesn't match (`hmac.Equal`).
2. Dedupe by `X-GitHub-Delivery` (kept 7 days); a redelivery answers 200 and does nothing.
3. Resolve the installation; unclaimed, suspended or not entitled → 200 and a `github_deliveries_dropped{reason}` count, nothing stored.
4. Store the minimal job (`delivery id, event, installation, repository id, refs, before/after, payload subset`) in the job queue and answer 202 within GitHub's 10-second limit. No GitHub API call happens on the request path.
- Payloads are never logged; logs carry the delivery id and event name only.

### Code sync from GitHub

Each claimed repository has one **GitHub-backed worktree** per project: id `gh_<repository id>`, `Source: github`, `Host: github.com`, `Owner`: the claiming user (or the workspace admin who claimed it), `DefaultBranch` from the repository. Its trees are written with the same `code.Store` calls as a client's, so the indexer needs nothing new.

- **`push` to branch B (`before` → `after`)**: `GET compare/{before}...{after}` lists changed files with their blob shas; fetch the blobs not already held (by git blob id → SHA-256 lookup, `code_trees {git_oid}` index), filter, redact, `PutBlob`, then `Commit` a change for ref B with `head = upstream = after`, `ahead = 0`, every entry `dirty: false, unpushed: false, git_oid` set, renames as delete plus upsert, `session_id` empty.
- **Force pushes, `before` of all zeros, compares over 300 files or truncated**: a full import of B at `after` instead.
- **Full import** (installation, repository added, new branch via `create`, or forced): `GET git/trees/{sha}?recursive=1`; when GitHub truncates (very large repositories), walk subtrees; a `Full: true` change replaces the ref's tree.
- **`delete` of a branch**: `RemoveRef` on the GitHub worktree (a tombstone change), so its branch layer can be dropped.
- **Which branches** (the *branch policy*, per repository):
  - `active` (**default**): the default branch, plus every branch that is *active*: it has an open pull request, or it received a push in the last `activeDays` days (default 14).
  - `default`: the default branch only.
  - `all`: every branch, up to `maxBranches` (default 200; the least recently pushed beyond that are inactive).
  - **Where it is set**: per repository on the Integrations page (defaulting to the installation's setting, which defaults to `active`), and in the repository's `mind-palace.config.yaml` under `github.branches` / `github.activeDays` / `github.maxBranches`, which wins when present, since it is reviewed with the code.
  - **Becoming active**: a push to an inactive branch, or a pull request opened from it, makes it active; its tree is brought up to date by the before-tip rule (a full import when the stored head is stale or missing).
  - **Becoming inactive**: when its pull request closes and no push arrives within `activeDays`, a daily sweep stops syncing it. Its last tree is **kept** (marked inactive on the worktree, not deleted), so the branch layer stays until the branch is deleted on GitHub or the data is wiped.
  - Open pull requests are tracked from `pull_request` events and listed once at import (`GET pulls?state=open`). Tags are ignored.
- **Filtering and redaction on the server**: the same built-in deny list, size cap (1 MiB, the plan's `capabilities` limit) and binary detection as the 0010 client, plus the built-in redaction rules and the account's pulled `redactPatterns`; `excludePaths` doesn't apply (it names local paths). `.gitignore` is moot (only committed files exist). Repository-level `codeSync: false` in `mind-palace.config.yaml` is honoured here too.
- Ordering: jobs for one repository run one at a time in delivery order; a push whose `before` isn't the current tip of the ref falls back to a full import, which makes out-of-order or lost deliveries self-healing.
- A nightly reconcile per repository compares each tracked branch's tip with the stored head and re-imports drifted refs, covering lost deliveries.

### Clients on a GitHub-backed project (change to 0010)

- `GET capabilities` adds `"github": true` for a project that has a claimed, active installation.
- The client then **omits content for entries that are clean and pushed** (`dirty: false`, `unpushed: false`): it sends them with `sha256: ""`, `git_oid` and `size`, and skips the manifest and blob upload for them. Overlays (dirty or unpushed files) are still uploaded in full.
- The server accepts an entry without `sha256` only when `git_oid` is set, it is clean and pushed, and the project is GitHub-backed; otherwise `invalid`.
- Result: a teammate's machine uploads only what isn't on GitHub yet, and the base and branch layers come from one authoritative source.

### Plans and stories from the default branch

On a push to the default branch that touches the plans or stories directories (read from the repository's `mind-palace.config.yaml` at `after`, defaulting to `docs/plans` and `docs/stories`), and on the initial import:

1. Fetch the documents at `after` into an in-memory file store and validate them with the configured kinds (invalid documents are skipped and recorded as a `plan_sync_problem` event, never written).
2. `store.Sync` them into the project's plan and story store (the same store the board and MCP tools use, under the project's namespace) with a new conflict mode, **`newer`**: the repository copy replaces the server copy when its `updated` timestamp is the same or later, and is skipped (with a `plan_sync_conflict` event naming the id) when the server copy is newer, i.e. a change made through the MCP tools that hasn't been merged yet.
3. A document deleted in the repository is archived on the server (`status: archived` through the service, not deleted), so links from other documents keep resolving.
4. **Sequence numbers**: after a sync the server store holds every id from the repository, so `allocateID` sees the real maximum; this fixes the duplicate-sequence problem seen with plan 0005.
- No plan or story is ever written back to GitHub.

### Activity log

`code.Store.AppendEvents(ctx, []Event)` with `Event{Seq, Kind, Repo, Ref, Commit, Before, PR, Actor, Status, Conclusion, URL, At, Delivery}`: a small, allow-listed subset of each payload, never the full JSON.

| Event | Recorded as |
|---|---|
| `push` | `push` (ref, before, after, commit count, pusher login, forced) |
| `create` / `delete` (branch) | `branch_created` / `branch_deleted` |
| `pull_request` opened, synchronize, closed, reopened, ready_for_review | `pr_*` (number, head and base refs and shas, merged, merge commit) |
| `check_suite` / `check_run` completed, `status`, `workflow_run` completed | `ci` (commit, name, status, conclusion) |
| `repository` renamed, transferred, archived, deleted | `repo_*` |
| plan and story sync outcomes | `plan_sync`, `plan_sync_conflict`, `plan_sync_problem` |

- Write-only in this plan: no route, page or MCP tool reads it. It is the input for later features (CI status on the board, pull requests on stories, the code graph's 'what changed' view).
- Actor logins are stored; e-mail addresses are not.

### Jobs

`ghsync.Jobs` port: `Enqueue`, `Claim(lease)`, `Done`, `Retry(after)`, ordered per repository. `memjobs` for tests and the in-memory server; clued-cloud adds a Mongo implementation (`github_jobs`, one in-flight job per repository via a lease document, exponential back-off up to 1 hour, dead-lettered after 10 attempts with a `github_job_failed` metric and the repository's state on the Integrations page set to *failed*). Workers run in the clued-cloud process (`--github-workers`, default 4). GitHub rate limits (`X-RateLimit-Remaining`, secondary limits, `Retry-After`) pause that installation's jobs rather than failing them.

### clued-cloud

- **Config**: `--github-app-id`, `--github-app-slug`, `--github-private-key-file`, `--github-webhook-secret-file`, `--github-workers` (all with `CLUED_CLOUD_GITHUB_*` fallbacks); the feature is off when unset. The private key and webhook secret are read from files, held in memory only, never logged.
- **Pages** (server-rendered, CSP as every page, POST forms behind `protect`): Settings → Integrations lists the user's installations and synced repositories with last sync time, state (synced, importing, paused, failed) and branch policy (a POST form per repository and per installation), and an *Install on GitHub* button; the workspace settings page gets the same for workspace admins. *Remove* only unlinks the installation (and links to GitHub to uninstall the app); it never deletes data, which stays until it is wiped (project, account or workspace deletion).
- **Entitlement**: `Limits.GitHubSync` (off by default, like `CodeSync`), and code written from GitHub counts toward `Limits.CodeBytes`. Plans and stories sync is allowed whenever the installation is claimed, since plans and stories are already a core feature.
- **Audit**: `AuditGitHubInstalled`, `AuditGitHubUninstalled`, `AuditGitHubReposChanged` with sentences in `web/security.go`, and `Service.notify` to the owner when an installation is claimed or removed (the existing security notification; no other notifications are sent).
- **Notices**: codes in `web.notices` for claimed, nothing to claim, removed.
- **Metrics**: `github_deliveries` (labels `event`, `outcome=queued|duplicate|dropped|invalid_signature`), `github_jobs` (`kind`, `outcome`), `github_api_calls` (`outcome`), `github_rate_limited`, with help text in `counterHelp`.
- **Purgers**: account and workspace deletion remove their installations, jobs, delivery records and events (code and documents go with the existing purgers).
- **Uninstall** (`installation.deleted`, or a repository removed from the installation): the installation or repository is marked removed and its jobs stop; the GitHub-backed worktree is marked inactive but its trees, blobs and events are **kept until wiped** by the existing project, account and workspace purgers. The projects turn back into client-backed (`capabilities.github` false), and Claude Code clients carry on collecting, uploading clean files again from their next full reconcile. Reinstalling re-links the same worktree (`gh_<repository id>`) and brings it up to date.

### Error Handling

- Invalid signature: 401, counted, nothing stored. Duplicate delivery: 200, nothing done.
- Unknown or unclaimed installation, not entitled, suspended: 200 and dropped (GitHub must not retry forever).
- GitHub API 5xx, timeouts: job retried with back-off; 403/429 rate limits pause the installation until reset; 404 on a repository (removed from the installation, deleted): stop its jobs and mark it removed.
- Compare too large or truncated, `before` not the stored tip: full import of that ref.
- Blob over the size cap or binary: entry recorded with `sha256: ""` and size, as 0010 does.
- Plan or story invalid at the commit: skipped, `plan_sync_problem` event; conflict with a newer server copy: skipped, `plan_sync_conflict` event.
- Quota full: code jobs for that account stop and the repositories show *paused* on the Integrations page; plans and stories still sync.
- Job dead-lettered after 10 attempts: metric, *failed* state on the Integrations page, and the nightly reconcile tries again.

### Edge Cases

- Monorepo over GitHub's tree limit: subtrees walked; jobs split by directory.
- Submodules: recorded as entries of mode `160000` without content.
- LFS pointers: stored as the small pointer text (the real content isn't fetched).
- Default branch renamed: the `repository` event updates `DefaultBranch`; the base moves to the new ref on the next push or reconcile.
- A repository made private or public: no change (the app's access decides).
- A repository whose `remote.project` changes: future syncs write to the new slug; the old project stays until deleted.
- An installation that covers repositories already synced by clients: the GitHub worktree is added alongside the client worktrees; their clean entries agree by `git_oid`.
- Plans or stories directories configured differently per repository: read from that commit's `mind-palace.config.yaml`.
- A plan edited both in the repository and through MCP before merge: `newer` keeps the later edit and records a conflict event for the other.
- Pushes during the initial import: queued after it for the same repository, then applied by the before-tip rule.

## Considerations

- **Security:** - Webhook HMAC verified with the raw body and `hmac.Equal` before any parsing; deliveries deduped by id; payloads never logged.
- The app's private key and webhook secret come from files, are kept in memory only and never logged; installation tokens live in memory and expire within an hour.
- Installation claims are bound to the session that started the install (hashed one-time state) or to a verified linked GitHub identity; an unclaimed or foreign installation writes nothing, and the claim pages answer the same whether or not an installation exists.
- Read-only permissions; the app never writes to GitHub.
- Server-side deny list, size cap, binary skip and redaction apply to GitHub content just as the client's do; `codeSync: false` in a repository opts it out.
- Code and events are written only under the claimed target's namespace (`u…` / `w…`), through the same fail-closed namespacing as 0010; nothing is readable through any route.
- Uninstalling or suspending stops processing at once; the data stays until wiped, and account and workspace deletion purge installations, jobs, events and code.
- **Performance:** - Push sync costs one compare call plus one blob call per new file version; blobs already held (by git blob id) are not fetched.
- Large pushes and first imports run in workers with per-repository ordering and back-off; the webhook answers in milliseconds.
- GitHub's 5,000+ requests per hour per installation is far above normal push volume; a first import of a very large repository is spread across jobs and paused on rate limits.
- Clients on GitHub-backed projects stop uploading clean files, which removes most client traffic.

## Testing

- `ghapp`: JWT claims and signing, token caching and refresh, signature verification (valid, wrong secret, missing header, altered body), payload decoding from recorded fixtures, rate-limit header handling; an `httptest` fake of the GitHub API, never api.github.com.
- `ghsync` suite over the fake: claim rules (state bound to session, identity match, foreign installations refused), push → change with correct `git_oid` / `dirty` / `unpushed` / `upstream`, renames, force push → full import, truncated tree walk, branch delete, the branch policies (active by open pull request and by `activeDays`, inactivity sweep keeps the tree, `default`, `all` with `maxBranches`, config file overriding the page), deny list and redaction, `codeSync: false`, plan and story sync with `newer` and archive-on-delete, invalid documents skipped, activity events allow-listed, ordering and before-tip fallback, reconcile after a lost delivery.
- 0010 changes: `capabilities.github`, entries without `sha256` accepted only when clean, pushed and GitHub-backed; the client skips upload for them (plugin test with a fake GitHub-backed server).
- clued-cloud: webhook endpoint (signature, dedupe, 202), install and claim pages (CSRF, state, nothing-to-claim answers identical), workspace install by an admin only, entitlement and quota, metrics, audit and notices, purgers, uninstall keeps data and clients resume full uploads, branch policy forms; the Mongo job queue with leases and dead-lettering (with `CLUED_CLOUD_TEST_MONGODB_URI`).

## Scope

**In scope:**

- `mind-palace/ghapp`: GitHub App plumbing with the standard library only: RS256 app JWT, installation tokens (cached until shortly before expiry), webhook signature verification (`X-Hub-Signature-256`), a small REST client (repositories, git trees, blobs, compare, contents) with rate-limit and retry handling, typed webhook payloads for the events below.
- `mind-palace/ghsync`: the rules. Installation and repository lifecycle, code sync from pushes into `code.Store`, initial import, plan and story sync from the default branch, the activity log, server-side filtering and redaction; a `Jobs` queue port with an in-memory implementation; a conformance-style test suite over fakes of GitHub.
- Plan 0010 changes: a `Source` on worktrees (`client` or `github`), `capabilities` reporting a project as GitHub-backed, and the client then sending clean, pushed entries without content.
- An activity log port (`code.Store` gains `AppendEvents`; Mongo and memory) for pushes, branch create/delete, pull requests, check suites/runs, commit statuses and workflow runs: write-only, like the code itself.
- clued-cloud: the app's configuration and secrets, `POST /github/webhook`, the install flow and claim pages under Settings → Integrations, workspace installs for workspace admins, a durable job queue in Mongo, the `Limits.GitHubSync` entitlement, metrics, audit kinds, notices, purgers and uninstall handling.
- PROTOCOL.md, docs/server.md, the clued-cloud README and a `docs/github-app.md` describing permissions, events, what is stored and how to remove it.

**Out of scope:**

- Any write to GitHub (check runs, commit statuses, comments, issues, labels) and any status notification to users about CI or sync. — Decided: information flows GitHub → clued only. The app asks for read-only permissions so installing it is low-risk.
- Changing a story's or plan's status from GitHub events (closing a story when a pull request merges). — The repository's files are the source of truth for plans and stories; the server mirrors them. Status changes still happen through `story_setStatus` and land in the repository through a commit.
- Plans and stories on non-default branches. — They are proposals until merged; syncing them would put unmerged documents on the board. Can be added as a 'proposed' view later.
- Self-hosted clued servers. — The packages are in the public module so a self-hosted server can wire them later (its own app via GitHub's manifest flow); this plan ships it in clued-cloud only, where the webhook URL is public.
- GitHub Enterprise Server hosts. — `ghapp` takes the API base URL as an option, but only github.com is configured and tested.
- Reading or displaying the activity log or collected code. — Same as 0010: collect now, expose later.
- Git history before the installation. — The initial import takes the current tip of each branch; history is not replayed.

## Constraints

- Depends on plan 0010's `code.Store`, entry fields and namespacing; built after its phases 1 and 2.
- Read-only GitHub permissions; nothing is written to GitHub.
- `CGO_ENABLED=0`, `go-licenses` policy: no new linked dependency.
- Rules in the public module; clued-cloud composes; public-module changes land first and are pinned by commit.
- clued-cloud conventions: answers that reveal nothing, secrets never logged, CSP pages with POST forms behind `protect`, audit through `Service.audit`.
- Plans and stories in this repository still change only through the mind-palace tools; the sync never writes to `docs/plans` or `docs/stories`.

## Phase 1: GitHub App plumbing (`ghapp`)

### 1.1: Auth, webhooks and REST client

**Files:**
- Create: `mind-palace/ghapp/jwt.go`
- Create: `mind-palace/ghapp/tokens.go`
- Create: `mind-palace/ghapp/webhook.go`
- Create: `mind-palace/ghapp/client.go`
- Create: `mind-palace/ghapp/events.go`
- Create: `mind-palace/ghapp/ghapptest/fake.go`
- Test: `mind-palace/ghapp/ghapp_test.go`

**Depends on:** none

**Interfaces:**
- Produces: `ghapp.App{ID, Key, BaseURL}`, `App.InstallationClient(ctx, id)`, `ghapp.Verify(secret, body, header)`, `Client.Compare`, `Client.Tree`, `Client.Blob`, `Client.Contents`, `Client.Installation`, `Client.Repository`, typed events, `ghapptest.Fake` (an `httptest` GitHub)

- [ ] **Step 1: RS256 app JWT (iat − 60 s, exp ≤ 10 min) and installation tokens cached until 5 minutes before expiry**
- [ ] **Step 2: `Verify` with HMAC-SHA256 and `hmac.Equal`; typed payloads for the subscribed events**
- [ ] **Step 3: REST client with pagination, rate-limit headers, `Retry-After`, typed errors (not found, rate limited, too large)**
- [ ] **Step 4: `ghapptest.Fake` serving repositories, trees (with truncation), compare, blobs and installations from fixtures**

## Phase 2: Sync rules (`ghsync`) and 0010 changes

### 2.1: Code store additions

**Files:**
- Modify: `mind-palace/code/model.go`
- Modify: `mind-palace/code/store.go`
- Modify: `mind-palace/code/codetest/codetest.go`
- Modify: `mind-palace/code/memcode/memcode.go`
- Modify: `mind-palace/code/mongocode/mongocode.go`
- Modify: `mind-palace/server/codeapi/codeapi.go`
- Modify: `mind-palace/code/remotecode/remotecode.go`

**Depends on:** none

- [ ] **Step 1: `Worktree.Source`, `RemoveRef`, `AppendEvents` with `Event`, lookup of SHA-256 by `git_oid`**
- [ ] **Step 2: `capabilities.github` from a `CluedOptions.GitHubBacked(ctx, project)` hook; accept content-less entries only when clean, pushed and GitHub-backed**
- [ ] **Step 3: Extend `codetest` for the additions**

### 2.2: Code sync and the activity log

**Files:**
- Create: `mind-palace/ghsync/ghsync.go`
- Create: `mind-palace/ghsync/code.go`
- Create: `mind-palace/ghsync/events.go`
- Create: `mind-palace/ghsync/jobs.go`
- Create: `mind-palace/ghsync/memjobs.go`
- Create: `mind-palace/ghsync/filter.go`
- Test: `mind-palace/ghsync/ghsync_test.go`

**Depends on:** 1.1, 2.1

**Interfaces:**
- Consumes: `ghapp`, `code.Store`, `redact`
- Produces: `ghsync.Service{App, Jobs, Code(ctx, target, slug), Docs(ctx, target, slug), Claims}`, `Service.Handle(ctx, delivery)`, `Service.Work(ctx)`, `Service.Reconcile(ctx)`, `ghsync.Jobs` port

- [ ] **Step 1: Push → compare → blobs → `Commit`, with the before-tip rule and full-import fallback**
- [ ] **Step 2: Full import from recursive trees (and subtree walk when truncated); branch create and delete; the branch policy (`active` default with open pull requests and `activeDays`, `default`, `all` with `maxBranches`), the daily inactivity sweep, and reading `github.*` from the repository's `mind-palace.config.yaml`**
- [ ] **Step 3: Server-side filter and redaction shared with the 0010 client (lift the deny list and binary check into a shared package)**
- [ ] **Step 4: Allow-listed activity events for every subscribed event**
- [ ] **Step 5: `Jobs` port with per-repository ordering, leases and back-off; `memjobs`**

### 2.3: Plans and stories sync

**Files:**
- Create: `mind-palace/ghsync/docs.go`
- Modify: `mind-palace/store/sync.go`
- Modify: `mind-palace/service/service.go`
- Test: `mind-palace/ghsync/docs_test.go`
- Test: `mind-palace/store/sync_test.go`

**Depends on:** 2.2

- [ ] **Step 1: `ConflictNewer` in `store.Sync` (compare `updated`), with tests**
- [ ] **Step 2: Fetch the directories at the commit into a memory store, validate, sync with `newer`, archive deletions through the service, record outcome events**

### 2.4: Client skips clean, pushed content

**Files:**
- Modify: `mind-palace/session/codesync/codesync.go`
- Test: `mind-palace/session/codesync/codesync_test.go`
- Test: `mind-palace/plugin/codesync_test.go`

**Depends on:** 2.1

- [ ] **Step 1: Read `capabilities.github`; send clean, pushed entries without content and skip their manifest and upload; upload overlays as before**

## Phase 3: clued-cloud

### 3.1: Installations, claims and webhook

**Files:**
- Create: `clued-cloud/account/github.go`
- Create: `clued-cloud/cloud/github.go`
- Create: `clued-cloud/web/github.go`
- Create: `clued-cloud/web/templates/integrations.html`
- Modify: `clued-cloud/go.mod`
- Modify: `clued-cloud/account/account.go`
- Modify: `clued-cloud/account/memaccount/memaccount.go`
- Modify: `clued-cloud/account/mongoaccount/mongoaccount.go`
- Modify: `clued-cloud/account/accounttest/accounttest.go`
- Modify: `clued-cloud/cmd/clued-cloud/main.go`
- Modify: `clued-cloud/web/web.go`
- Modify: `clued-cloud/web/templates/settings.html`
- Modify: `clued-cloud/web/templates/workspace.html`
- Test: `clued-cloud/cloud/github_test.go`
- Test: `clued-cloud/web/github_test.go`

**Depends on:** 2.2

- [ ] **Step 1: Pin the public module** — `go get github.com/robbiebyrd/clued/mind-palace@<sha>`
- [ ] **Step 2: `Installation`, install state and delivery records in `account.Store`, both stores and `accounttest`; claim rules in `account.Service`**
- [ ] **Step 3: `POST /github/webhook`: verify, dedupe, resolve, enqueue, 202**
- [ ] **Step 4: Settings → Integrations and the workspace equivalent: install (POST with state), setup callback, claim confirmation, remove**

### 3.2: Workers, entitlement, metrics, audit, purgers

**Files:**
- Create: `clued-cloud/cloud/githubjobs.go`
- Modify: `clued-cloud/account/plans.go`
- Modify: `clued-cloud/account/audit.go`
- Modify: `clued-cloud/web/security.go`
- Modify: `clued-cloud/metrics/metrics.go`
- Modify: `clued-cloud/cloud/cloud.go`
- Modify: `clued-cloud/README.md`
- Test: `clued-cloud/cloud/githubjobs_test.go`

**Depends on:** 3.1, 2.3

- [ ] **Step 1: Mongo job queue with leases, back-off and dead letters; worker pool and nightly reconcile**
- [ ] **Step 2: Wire `ghsync.Service` with namespaced `code.Store` and plan/story openers; `GitHubBacked` hook for `capabilities`**
- [ ] **Step 3: `Limits.GitHubSync`, quota accounting, audit kinds and sentences, notices, metrics with help text**
- [ ] **Step 4: Purgers for installations, jobs, deliveries and events; uninstall keeps the data until wiped; branch policy forms on the Integrations pages**

## Phase 4: Documentation

### 4.1: Docs

**Files:**
- Create: `docs/github-app.md`
- Modify: `PROTOCOL.md`
- Modify: `docs/server.md`
- Modify: `README.md`
- Modify: `.claude/CLAUDE.md`
- Modify: `mind-palace/README.md`
- Modify: `clued-cloud/README.md`
- Modify: `clued-cloud/CLAUDE.md`

**Depends on:** 3.2

- [ ] **Step 1: `docs/github-app.md`: permissions, events, what is stored, how sync and conflicts work, how to uninstall and delete**
- [ ] **Step 2: PROTOCOL.md: `capabilities.github`, content-less entries, the webhook route; layout tables for `ghapp/`, `ghsync/`**

## Acceptance Criteria

- [ ] A user can install the app from clued.cloud on selected repositories (or all), and a workspace admin can install it for a workspace; each installation is bound to exactly one account or workspace, and nobody can claim someone else's installation.
- [ ] After installation, every claimed repository's default branch is imported, and every later push (from any tool, by anyone) updates that branch's tree with clean, pushed entries carrying GitHub's blob ids within the job queue's normal latency.
- [ ] Force pushes, lost or out-of-order deliveries and truncated compares converge to the branch's real tip (before-tip rule and nightly reconcile).
- [ ] Branch deletion on GitHub removes that ref's tree; the base follows the repository's default branch, including a rename.
- [ ] Deny-listed, binary and oversized files never have content stored; redaction applies; `codeSync: false` in a repository opts it out.
- [ ] Plans and stories on the default branch appear in the project's server store; a newer MCP edit is not overwritten (conflict recorded); a deleted document is archived; sequence numbers no longer collide.
- [ ] Push, branch, pull request and CI events are recorded as allow-listed activity events; nothing exposes them, or the code, through any route.
- [ ] On a GitHub-backed project, clients upload only dirty and unpushed files; on uninstall the GitHub-synced data stays until wiped and clients resume full uploads.
- [ ] By default only the default branch and active branches (open pull request, or pushed within `activeDays`) sync; `default` and `all` policies work, set on the Integrations page or in `mind-palace.config.yaml` (which wins); a branch going inactive keeps its last tree.
- [ ] Nothing is ever written to GitHub, and no status notifications are sent to users.
- [ ] Invalid signatures, duplicates and unclaimed installations store nothing; secrets and payloads are never logged.
- [ ] Entitlement, quota, metrics, audit events, notices and purgers work as described; `gofmt`, `go vet` and `go test -race ./...` pass in both repositories, with and without MongoDB.

## Self-Review

| Requirement | Delivered by |
|---|---|
| Install a GitHub App | 3.1, Step 4 |
| Auto-sync code | 2.2, Steps 1 and 2; 3.2, Step 1 |
| Receive status notifications of changes | 2.2, Step 4 (activity log of pushes, branches, pull requests and CI) |
| Keep plans and stories up to date | 2.3 |
| Keep code up to date alongside the client overlays | 2.1, 2.4 |
| GitHub → clued only, no outbound notifications | Decisions; Scope (out of scope) |
| Uninstall keeps data until wiped; local collection continues | Design, clued-cloud "Uninstall"; 3.2, Step 4 |
| Configurable branch sync, default: default branch plus active branches | Design, "Which branches"; 2.2, Step 2; 3.2, Step 4 |
**Residual risk:** Pushed code lives on the server even when no one opted in on their machine, so the entitlement and the install screen must say so plainly. Server-side redaction can only use the account's patterns, not each machine's. A very large monorepo's first import can take a long time under rate limits.

## Next Steps

1. Surface the activity log (inside clued only): CI status and pull requests on the board and on stories; pull requests that mention story ids in the story's view.
2. Self-hosted servers: the GitHub manifest flow (`mind-palace server github-app create`) and wiring `ghsync` into `server run`.
3. 'Proposed' plans and stories from open pull requests.

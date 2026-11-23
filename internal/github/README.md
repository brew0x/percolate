# GitHub transport

This package is the only network boundary in Percolate. The CLI fetches a
repository through it, and rendering stays pure over the resulting snapshot.
Credentials and cached responses are never written to disk.

`ResolveToken(ctx)` prefers a nonempty `GITHUB_TOKEN`, then invokes
`gh auth token --hostname <host>` with a five-second deadline. `<host>` comes
from `PERCOLATE_GITHUB_URL` (default `github.com`; the default API host
`api.github.com`, including an explicit `:443`, also maps to `github.com`).
Hostnames are lowercased, so an Enterprise credential is found regardless of
case. Failure returns one actionable line. Neither helper output, helper errors
nor tokens enter errors, and nothing is persisted.

`NewClient(token).ListPage(ctx, path)` requests one relative REST page on
api.github.com. It keeps ETags and response JSON in memory, keys pages by the
full path including query, sends If-None-Match, and reuses cached JSON on 304.
Updated 200 responses replace the cached page. Failed or malformed responses
do not. Redirects are rejected and response bodies are omitted from errors.
`ListAll(ctx, path, maxPages)` collects array endpoints that support GitHub's
100-item pages. It preserves filters and replaces page/per_page, walking pages
in order until a short page. An exact multiple needs one final empty-page
request: the local 500-item test takes six requests, including on an ETag-backed
refresh. The caller supplies a positive request bound; exhausting it or failing
any page returns no partial list. Non-array responses and pages over 100 items
are rejected. Link headers are not needed for this traversal.

`Client.GraphQL(ctx, query, variables)` posts one query to /graphql and
returns only the data member; any server-side error rejects the whole call, so
partial results cannot be mistaken for complete data. Every REST and GraphQL
request is counted by `Client.Requests()`.

Rate-limit responses (403/429 with `X-RateLimit-Remaining: 0`, or a `Retry-After`
given in seconds or as an HTTP date) produce one line that says when to come
back, rounded to a second for display only; a deadline already past reports
zero. They also return a `*RateLimitError` whose `ResetTime()` is the latest
valid deadline the headers record — reset seconds resolved against the
caller's now, an HTTP date kept exactly as parsed — so when both headers are
usable the later one wins and neither can cut the wait short, and a missing or
malformed reset cannot discard a usable `Retry-After`. Refresh scheduling waits
for that recorded data rather than a guess. Missing, malformed or absurd values
keep the bare HTTP error, including a reset timestamp so far ahead it would
saturate a duration (the same sanity check as an absurd `Retry-After` date); an
exhausted budget with no usable deadline keeps the ordinary retry.
Server error text is flattened to a single line; bodies and tokens never appear
in errors.

`Client.BuildSnapshot(ctx, SnapshotOptions)` assembles a `repo.Snapshot`
from bounded REST lists (repository meta, branches, default-branch commits,
open issues, open pull requests, workflow runs, releases with their asset
names, deployments) and batched aliased GraphQL queries that fill per-PR
reviews, closing issue references and head-commit anchors (25 PRs per query),
plus one bounded page per recent run that fills `repo.WorkflowRun.Jobs` and
one per recent deployment that fills `repo.Deployment.State`. Defaults bound
one read to recent history: 1000 open issues and pull requests, 5000
default-branch commits, 5000 workflow runs and 1000 branches, releases and
deployments each, the job lists of the 10 most recent runs, at most 100 jobs
each, and the latest recorded status of the 10 most recent deployments. The
worst-case plan is 258 requests `plannedRequests()` (1 meta, 157 list pages,
80 detail queries: a first page plus one follow-up for each of the 40
batches, 10 job pages and 10 deployment-status pages), inside the
300-request budget promised for #3; short collections cost fewer (the
500-item local fake measures 75). A plan that exceeds its budget is rejected
before any request. Every list read enforces its item cap as well as its
page count, so a repository with more items than a cap fails with one line
instead of truncating its history. A failure of any of those reads returns
no snapshot — the one deliberate exception is the per-deployment status page
described below, whose failure leaves a colour unrecorded rather than
costing the snapshot. A PR that disappears between the list and its detail
query keeps its lane but gains no reviews.

The job read is bounded the same way, with an explicit per-run and total cap.
`GET /actions/runs/{id}/jobs` is read once per run for the `MaxRunsWithJobs`
(10) most recent runs in the recorded list order, with `MaxJobsPerRun` (100)
as the per-run cap; each read is exactly one request, so the window adds at
most 10 requests to any plan. The response's own `total_count` is the
completeness witness: a run with more jobs than the cap, a page short of the
count it names, or a full page with no matching count is one clear error and
no snapshot, never a partial job strip presented as complete. A run read
inside the window records its list even when empty — `0 jobs` is an honest
statement there — while a run past the window keeps a nil `Jobs` slice, so
details keep saying `jobs not recorded` rather than a fabricated zero. Closing
the window (`MaxRunsWithJobs` 0) reproduces the pre-job read exactly; setting
it above the run count reads every run's jobs and still stays inside the
plan.

Each PR detail reads the whole review history, closing issue references and
the head commit's parent list through GraphQL connections in 100-item pages,
oldest first, with at most one follow-up page per batch. The follow-up asks for
the head commit's `oid` as well as the next parent page, so the join can verify
that both pages describe the same commit; a follow-up that loses that identity
fails the whole read. The snapshot states the complete lists it read, up to 200
reviews, 200 closing references and 200 parents per PR; a connection with a
third page fails the whole read rather than being truncated into a
plausible-looking history. The same bound covers the cursor-less page case: a
promised next page with no cursor is an error. The result is a point-in-time
view: objects can change between pages, and list ETags only deduplicate
identical repeat reads.

GitHub's `/issues` endpoint returns pull requests too, because both share one
number sequence; each PR carries a `pull_request` object. Those items, and
anything not reported open, are filtered before the cap check so a PR never
becomes a spring or consumes the issue budget. An issue's `closed_by` is filled
only from a PR's `closingIssuesReferences` node naming that exact open issue;
a PR-shaped list item, issue text, a nearby number or a reference to an issue
that is not in the snapshot never creates a link. Closing references add no
request beyond the batched detail queries.

A fork pull request's head lane uses GitHub's fork-qualified `head.label` (for
example `alice:main`) when its head repository differs from the one being read;
same-repository pulls keep the bare ref, so their lanes are unchanged. Without
this, a fork's commit would join the upstream branch of the same name and two
forks sharing a ref would collapse into one lane. Draft releases returned to
callers with push access are filtered before the cap check: a draft was never
published, so it must not draw a release mark or enter release details, and it
must not consume the release budget.

Release artifacts and deployment states are read live now, both bounded. A
release's asset names arrive on the release list page itself — GitHub carries
each release's `assets` inline — so they land in `repo.Release.Artifacts` in
GitHub order without any per-release request, and a draft's assets never
land, because drafts are filtered before the cap check. The inline list is
the complete one, not a truncated prefix. GitHub documents that array as
required with no cap and no truncation note, and live public REST reads
confirm it (2026-11-23): releases carrying 38, 55 and 168 assets each held
every asset inline, identical in name and order to their per-release assets
endpoints — the 168-asset ones are SagerNet/sing-box's v1.15.0-alpha.13 and
v1.14.4, more assets than the 100-item page that endpoint serves, the exact
shape that would expose an inline cap — so there is no per-release asset
fetch to bound. If GitHub ever truncates that inline array, the read must
stop calling it complete and add the bounded per-release assets endpoint to
the request plan; these cross-checks are how such a change would be caught.
The list is recorded
even when a release published no assets: details then count an honest
`0 artifacts`, never `artifacts not recorded`, while an item that omits the
field entirely was not read and keeps a nil list — the same nil-versus-empty
line the job read draws. A deployment's state
arrives from `GET /repos/{owner}/{repo}/deployments/{id}/statuses`: one page
per deployment for the `MaxDeploymentsWithStatuses` (10) most recent
deployments in the recorded list order, so the window adds at most 10
requests to any plan. GitHub returns that list newest first (confirmed
against live public REST data), so the first item is the latest recorded
status and no second page is ever needed; GitHub also serves states beyond
the documented enum (a live `waiting` was observed), and the snapshot records
such a word as-is, where the model keeps unknown states neutral. A status
read that fails, a body that is not a status list, or an empty list leaves
`repo.Deployment.State` empty — nothing is guessed — and the read moves on
to the next deployment instead of failing the snapshot: the deployment
itself was already recorded completely, and its state is the colour of a
flow, not the identity of an object. (A refusal caused by an exhausted
request budget is not hidden for long: the next poll's first request fails
that read and schedules the recorded pause.) A deployment past the window
keeps an empty state, which details report as `state: not recorded`; GitHub
deletes statuses older than 90 days, so an old deployment can honestly
report no state. Closing the window (0) reproduces the stateless snapshot
exactly. A snapshot that carries these fields renders artifacts above their
release and colours each deployment's flow by its recorded state, exactly as
hand-written fixtures already did; a source that omits them keeps the honest
`artifacts not recorded` and `state: not recorded` positions (a recorded empty
asset list stays the honest `0 artifacts` through record and replay).
Per-run job
lists stay as described above: a run read within the window records its list
even when empty, and a run outside it says `jobs not recorded`.

`PERCOLATE_GITHUB_URL` overrides the API base URL (used by tests against
local servers; also the seam for GitHub Enterprise hosts). It must be
https, or http on loopback for local tests: every request carries a bearer
token, so a mis-set plain-http host must not receive it in cleartext. Its host
also selects the `gh` credential hostname used by `ResolveToken`.

Recorded public fixtures (`testdata/fixtures/recorded-*.json`) must come from
public repositories only, with provenance noted; never commit private repo
data. `testdata/fixtures/recorded-hivemq.json` is the first such recording: a
real public repository read through unauthenticated GitHub REST, with its
source and method in `testdata/fixtures/README.md` and replay frames checked
in `testdata/golden/recorded-hivemq-*`. It closes #3's recording and replay
acceptance. GitHub's GraphQL details still rest on local fakes: that API needs
a token, which this recording deliberately did not use. The fakes now answer
with only the fields each query selected, `TestPullDetailQuerySelections`
pins the aliases, arguments and selections the client depends on, and
`TestPullDetailFieldTypoIsVisibleLocally` shows both a mistyped review `state`
arriving as missing snapshot data and a dropped first-page `number` failing
the REST/GraphQL pull-request match. A wrong field or alias therefore fails
`make check` here; that is not the same as a live GraphQL response having been
decoded.

The issue and closing-reference shape is proven both offline and by a public
recording: the field-aware GraphQL fake returns only selected
`closingIssuesReferences` nodes, `TestPullDetailQuerySelections` pins their
alias and arguments, `TestBuildSnapshotReadsOpenIssuesAndRealClosingReferences`
checks that a PR list item and a closed issue are excluded and that only a real
reference fills `closed_by`, and the typo test shows a dropped field reaching
the snapshot as an unlinked spring.
`testdata/fixtures/recorded-issues.json` is a real public snapshot of
`tmux-python/libtmux` read through unauthenticated REST: it carries a feeding
issue (#863, closed by open PR #901), two unlinked issues (#886 and #884) and
their reviewed golden frames. Its `closed_by` comes from GitHub's REST issue
timeline cross-reference plus PR #901's closing keyword, not from numbering or
proximity; the live read still fills `closed_by` only from a GraphQL
`closingIssuesReferences` node. That connection needs a token, so the recording
proves the JSON and golden shape rather than a live GraphQL decode; provenance
and the exact source endpoints are in `testdata/fixtures/README.md`.

Tests use local httptest servers and injected credential-helper output. They
require no GitHub access, real credentials, or changes to rendering. The
record→replay test records a PR whose GraphQL response needs a second page for
both reviews and parents, then checks the complete lists in the recording, the
identical frame bytes and a request count inside the plan; the public
recording's golden frames stay byte-identical. The opt-in `graphqlcapture`
probe (`live_capture_test.go`) is compiled but not run by `make check`; a
maintainer with a GitHub token can run it to record a real public GraphQL
snapshot and print its SHA-256. It refuses to write unless one open pull request
has both a named review and a head-commit anchor filled from that response, so
a review on one PR and a commit on another cannot stand in for the proof. That
guard is also checked offline against the real REST-derived open-PR recording's
shape (`TestCaptureGraphQLProofAcceptsRecordedOpenPR`), so a token-enabled run
cannot be refused for a structural reason; it still does not claim any GraphQL
response was decoded. The probe also writes a 0600 companion file
`<capture-out>.graphql.jsonl` containing only the successful GraphQL response
bodies, so a reviewer can inspect the exact fields GitHub returned; no request
headers or token are stored in it. The probe produces the missing live proof
for #21; it does not claim that proof has been captured or checked in.

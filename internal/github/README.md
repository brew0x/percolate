# GitHub transport

This package is the only network boundary in Percolate. The CLI fetches a
repository through it, and rendering stays pure over the resulting snapshot.
Credentials and cached responses are never written to disk.

`ResolveToken(ctx)` prefers a nonempty `GITHUB_TOKEN`, then invokes
`gh auth token --hostname github.com` with a five-second deadline. Failure
returns one actionable line. Neither helper output nor tokens enter errors.

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

Rate-limit responses (403/429 with `X-RateLimit-Remaining: 0` or a
`Retry-After`) produce one line that says when to come back. Server error
text is flattened to a single line; bodies and tokens never appear in errors.

`Client.BuildSnapshot(ctx, SnapshotOptions)` assembles a `repo.Snapshot`
from bounded REST lists (repository meta, branches, default-branch commits,
open pull requests, workflow runs, releases, deployments) and batched aliased
GraphQL queries that fill per-PR reviews and head-commit anchors (25 PRs per
query). Defaults bound one read to recent history: 1000 open pull requests,
5000 default-branch commits, 5000 workflow runs and 1000 branches, releases
and deployments each. The worst-case plan is 187 requests `plannedRequests()`,
far under the 250-request budget and the 300-request budget promised for #3;
short collections cost fewer (the 500-item fixture measures 49). A plan that
exceeds its budget is rejected before any request. Every list read enforces
its item cap as well as its page count, so a repository with more items than
a cap fails with one line instead of truncating its history. Any failure
returns no snapshot, and a PR that disappears between the list and its detail
query keeps its lane but gains no reviews. Each PR detail records at most the
10 most recent reviews (`reviews(last:10)`, the tail of GitHub's oldest-first
connection) and its head commit at most 3 parents; anything beyond those caps
is absent from the snapshot. The result is a point-in-time view: objects can
change between pages, and list ETags only deduplicate identical repeat reads.

Not read in this slice: per-run job lists and per-deployment statuses (both
are unbounded per-object fan-outs; run details and deployment state stay
empty rather than guessed). Workflow-run jobs are recorded only when a
snapshot was made by a source that included them; run details then say
`jobs not recorded` rather than a numeric zero.

`PERCOLATE_GITHUB_URL` overrides the API base URL (used by tests against
local servers; also the seam for a future GitHub Enterprise host). It must be
https, or http on loopback for local tests: every request carries a bearer
token, so a mis-set plain-http host must not receive it in cleartext.

Recorded public fixtures (`testdata/fixtures/recorded-*.json`) must come from
public repositories only, with provenance noted; never commit private repo
data. Producing the first verified public recording is the remaining open
part of issue #3.

Tests use local httptest servers and injected credential-helper output. They
require no GitHub access, real credentials, or changes to rendering.

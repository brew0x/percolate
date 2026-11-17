# GitHub transport foundation

This package is preparation for issue #3, not a live snapshot adapter. The CLI
still only accepts recorded fixtures. No network calls happen on startup.

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

This is not an atomic read: objects changing while pages are fetched can move
between pages. Snapshot assembly must account for that limitation.

Remaining work for #3: assemble repo.Snapshot from paginated REST lists and
batched GraphQL PR details; add rate-limit handling;
prove fewer than 300 requests for 500 PRs; wire owner/repo and --record into
the CLI; record a verified public repository and prove identical replay frames.
The cache is intentionally memory-only so private response data and credentials
are not persisted by this layer. Public fixture provenance must be checked
before adding recorded data to the repository.

Tests use local httptest servers and injected credential-helper output. They
require no GitHub access, real credentials, or changes to rendering.

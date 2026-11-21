# Fixture provenance

Every checked-in snapshot says where its data came from. Only public
repositories are ever recorded here; private data is never committed.

## `recorded-hivemq.json` — real public recording

- **Source:** <https://github.com/hivemq/hivemq-hello-world-datahub-module>,
  a public "hello world" example repository maintained by HiveMQ.
- **Captured:** 2026-11-17. Head commit at capture:
  `5e0b579833839b5f254b684049895d08142efa37`.
- **Method:** the unmodified `percolate` binary read the repository through
  GitHub's **public, unauthenticated REST API**. Because every request in this
  package carries a bearer token, reads were routed through a throwaway
  loopback forwarder that stripped the `Authorization` header; public endpoints
  need no token, so no credential was transmitted, used, or written. The
  recording is the `repo.Snapshot` that binary assembled, stored with
  `--record`. It contains only data GitHub serves to anyone.
- **Shape at capture:** one branch (`master`), six default-branch commits, zero
  open pull requests, zero workflow runs, one published release, zero
  deployments. With no open pull requests the read stays on REST, so this
  recording verifies the repository/branch/commit/pull/run/release/deployment
  endpoints and their JSON shapes against real GitHub responses. Per-PR GraphQL
  details still rest on the local `httptest` fixtures: GitHub's GraphQL API
  requires authentication, which was unavailable for this recording.
- **SHA-256:** `5be2a2023960efc695e8002acdee7f7fa61e3dc6b26ae89a59554aaea6083269`
- **Replay:** golden frames `testdata/golden/recorded-hivemq-*` are derived from
  this exact file and checked in CI by `TestGolden` and
  `TestRecordedPublicFixtureReplaysToGolden`.

## `recorded-pull-details.json` — real public pull-detail recording

- **Source:** <https://github.com/coq/coq>, a public repository. The two
  recorded pull requests are [#8301](https://github.com/coq/coq/pull/8301) and
  [#11914](https://github.com/coq/coq/pull/11914). Their review histories came
  from `GET /repos/coq/coq/pulls/8301/reviews?per_page=100` and
  `GET /repos/coq/coq/pulls/11914/reviews?per_page=100`; the head commit's parent
  list came from
  [`GET /repos/coq/coq/commits/cf978356dd0791cf6b4bc90dcc07a91cd57bd79e`](https://api.github.com/repos/coq/coq/commits/cf978356dd0791cf6b4bc90dcc07a91cd57bd79e).
- **Captured:** 2026-11-21. Head commits at capture:
  [`02c3cac9fa4f4e88f911486e345740ff7d0a2eab`](https://github.com/coq/coq/commit/02c3cac9fa4f4e88f911486e345740ff7d0a2eab)
  (PR #8301) and
  [`cf978356dd0791cf6b4bc90dcc07a91cd57bd79e`](https://github.com/coq/coq/commit/cf978356dd0791cf6b4bc90dcc07a91cd57bd79e)
  (PR #11914).
- **Method:** public, unauthenticated REST requests through `curl`; no token,
  credential or authenticated GraphQL endpoint was used. The JSON is a
  `repo.Snapshot` assembled from those responses and contains only data GitHub
  serves to anyone.
- **Shape at capture:** 26 branches, 12 commits (ten from `master` and the two
  PR heads), two closed pull requests, and no issues, runs, releases or
  deployments. The broad public scan did not find one public PR with both
  required properties in the same object, so this one public snapshot carries
  two real PRs: #8301 supplies 51 recorded reviews (past the old ten-review
  cap) and #11914 supplies a head commit with five parents (past the old
  three-parent cap). Each review list fits one 100-item REST page; no PR's
  fields were fabricated or merged with another PR's fields.
- **Acceptance reconciliation for #19:** the issue asks for one PR with more
  than ten reviews *and* a head commit with more than three parents in a
  recorded public fixture. On 2026-11-21 a scan of the 500 most-commented public
  `rocq-prover/rocq` pull requests with more than ten comments checked each head
  commit's parent list through the public commit endpoint. No single PR had both properties:
  #8301 has the long review history and one parent, and the only octopus head,
  #11914, has five parents and five reviews. Re-check either property with the
  URLs above. This fixture keeps both real cases rather than inventing a PR
  that does not exist; the combined single-PR case is checked locally by
  `TestWatchRecordsAndReplaysIdentically`, whose one PR has 103 reviews and a
  six-parent head commit. The public fixture and that local proof are checked
  together by `TestRecordedPullDetailsCoversBothPublicConnections`.
- **SHA-256:** `81a5aa1af746ed220663977888174d043ef0941009342be93a903c3a263a73d8`
- **Replay:** golden frames `testdata/golden/recorded-pull-details-*` are derived
  from this exact file and checked in CI by `TestGolden`,
  `TestRecordedPullDetailsFixtureReplaysToGolden` and
  `TestRecordedPullDetailsFixtureProvenance`.

## `recorded-open-pr.json` — real public open-pull-request recording

- **Source:** <https://github.com/rocq-prover/rfcs>, a public repository. The
  recorded pull request is [#64](https://github.com/rocq-prover/rfcs/pull/64),
  which was open at capture. Its real review came from
  `GET /repos/rocq-prover/rfcs/pulls/64/reviews?per_page=100`; its head commit's
  details and parent list came from
  [`GET /repos/rocq-prover/rfcs/commits/7477a535243e1ca7645c154c1b595320e74e9e3d`](https://api.github.com/repos/rocq-prover/rfcs/commits/7477a535243e1ca7645c154c1b595320e74e9e3d).
- **Captured:** 2026-11-21. Snapshot time
  `2026-11-21T11:27:42.394874235Z`. The open head commit is
  [`7477a535243e1ca7645c154c1b595320e74e9e3d`](https://github.com/rocq-prover/rfcs/commit/7477a535243e1ca7645c154c1b595320e74e9e3d),
  authored by Jim Fehrle, with parent
  `5393bb1adf13573b014a00753b691d73fc763367` on `master`.
- **Method:** public, unauthenticated REST reads with no `Authorization` header;
  no token or credential was available, used or written. The responses were
  mapped into `repo.Snapshot` with the same field mapping as
  `internal/github` (REST list order for commits, `user.login` and lowercased
  `state` for reviews, head-commit `oid`/author/date/parents for the anchor).
  The fork head lane is `head.label` (`jfehrle:debugger_ltac2`), the same
  qualifier `listPullRequests` uses. This is a focused detail recording: the
  repository had four upstream branches and 53 open pull requests at capture,
  but only the base branch `master` and #64 — the one with a fetched review
  and an anchor — are carried here. No other branch or PR's fields were
  invented or merged into it.
- **GraphQL caveat:** this recording does **not** come from an authenticated
  GraphQL call. `POST /graphql` needs a token and returned HTTP 403 here, so
  `fetchPullDetails` has still not run against a live GitHub GraphQL response.
  As a check short of that, every field and connection argument used by
  `pullDetailQuery` was compared on 2026-11-21 with GitHub's public GraphQL
  reference: `reviews(first:, after:){nodes{author{login} state}
  pageInfo{hasNextPage endCursor}}` at
  <https://docs.github.com/en/graphql/reference/pulls#pullrequest>, and
  `headRef{target{oid ... on Commit{oid committedDate author{name}
  parents(first:, after:){nodes{oid} pageInfo{hasNextPage endCursor}}}}}` at
  <https://docs.github.com/en/graphql/reference/commits#commit>. The schema
  names check out; a real authenticated GraphQL capture is still needed to
  fully close #21.
- **Shape at capture:** one branch (`master`), 135 commits (134 default-branch
  commits plus PR #64's fork head), one open pull request with one real review
  (`ppedrot`, `changes_requested`), and no workflow runs, releases,
  deployments or issues. The empty collections were each confirmed with one
  public REST list read before being omitted from this focused snapshot.
- **SHA-256:** `1059f481c5882f67ddd2422bc323e3e803c121dae4f330f9a4767bfc3cc3f019`
- **Replay:** golden frames `testdata/golden/recorded-open-pr-*` are derived
  from this exact file and checked in CI by `TestGolden`,
  `TestRecordedOpenPRFixtureReplaysToGolden` and
  `TestRecordedOpenPRFixtureProvenance`.

## `small.json`, `busy.json`, `scale.json` — fictional fixtures

Seeded, entirely fictional data generated by `internal/repo/fixturegen`
(`go generate ./internal/repo/fixturegen`). `small` uses seed 8; `busy` and
`scale` use seed 42. They exercise demonstration states (reviews, running and
failed builds) that the public recording does not. `busy` has 40 branches,
300 commits, 120 PRs and 200 runs; `scale` keeps the same branches, commits and
runs but carries 500 PRs so the at-scale replay and frame budget are checked
against hundreds of things in flight. No private data is involved.

## `release.json` — fictional releases, artifacts and deployments

A hand-written fictional snapshot: two published releases on two branches, two
and one recorded artifacts, and five deployments — three flowing out of the
first release with success, in-progress and failure states, one out of the
second, and one with no matching release standing alone. It exercises the
bottom band at every replay size. No private data is involved.

## `jobs.json` — fictional job states

A hand-written fictional snapshot showing workflow runs that record their
jobs: one running run with a finished job beside an unfinished one, one failed
run with a failure between a success and a skipped job, and one fully
successful run. It exercises the job strip and the failing-job detail text.
No private data is involved.

## `small-next.json` — fictional follow-on snapshot

A point-in-time successor to `small.json`, used as the second recorded snapshot
for transition tests and goldens. It is the same snapshot with three small,
explicit changes: one new commit (`...0d`) on `main`, workflow run 1 finished
`success`, and pull request 1 and issue 2 no longer present (a closed PR leaves
the open-PR window, and a closed issue draws no spring). It is fiction derived
from a checked-in fixture, not data fetched from any repository; no private
data is involved.

## `newest.json`, `newest-next.json` — fictional newest-commit pair

`newest-next.json` adds one new latest commit (`...0d`) to `newest.json`,
leaves workflow run 1 in progress and starts run 2 on the new commit. It
exercises the common case the `small` pair misses: a new commit whose final rank
is zero, an unchanged running run that must keep pulsing, and CI anchored to the
entering commit that must follow it down the lane. Fiction derived from a
checked-in fixture; no private data.

## `merged.json`, `merged-next.json` — fictional removed-lane pair

`merged.json` is a copy of `small.json`. `merged-next.json` removes the
`flow-01` branch, all of its commits, its pull request and its workflow run, so
the objects that fade in the transition belonged to a lane the new snapshot no
longer has. Fiction derived from a checked-in fixture; no private data.

## `feed.json`, `feed-next.json` — fictional closing-reference handoff

`feed-next.json` removes pull request 1 (old rank 0) and its branch, so pull
request 2 survives and moves into rank 0 while issue 5 stays linked to it. The
pair pins the survivor's feed running down its own lane to pr 2's diamond, and
the departed PR fading on its retained lane without attracting any feed.
Fiction derived from a checked-in fixture; no private data.

## `feed-fade.json`, `feed-fade-next.json` — fictional fading-issue handoff

The same fictional handoff as the `feed` pair, with issue 5 (linked to the
surviving pr 2) removed from the new snapshot. Issue 5's rank comes from the old
snapshot, where pr 2 held rank 1, so the pair pins its feed staying with the
named PR after that PR moves into rank 0. Reviewed transition frames are
`testdata/golden/feed-fade-*`. Fiction derived from a checked-in fixture; no
private data.

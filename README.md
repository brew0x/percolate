# Percolate

A living landscape of a repository's development lifecycle, inside your terminal.
Branches flow downward; commits, reviews and CI meet on the way to releases.
`percolate owner/repo` fetches a live snapshot from GitHub; any snapshot can be
recorded to a file and replayed identically.

```text
  PERCOLATE  1–3/3

   main                            flow-01                         flow-02
   │                               │                               │
   ●                               ● ✕                             ● ◌
   │                               │                               │
   │                               │                               │
   │                               │                               │
   ●                               ●                               ●
   │                               │                               │
   │                               │                               │
   │                               │                               │
   │                               │                               │
   ●                               ●                               ●
   │                               │                               │
   │                               │                               │
   │                               │                               │
   │                               │                               │
   ●                               ●#1                             ●
   ▼───────────────────────────────◇                               │
   │                               │                               │
   │                               │                               │
   │                               │                               │
   │                               │                               │
   │                               │                               │
   │                               │                               │
   │                               │                               │
   │                               │                               │
   ╵                               ╵                               ◆ v0.1

```

## Install

Go 1.27 or later:

```sh
go install github.com/brew0x/percolate/cmd/percolate@latest
```

## Usage

Watch a real repository (token from `GITHUB_TOKEN` or `gh auth login`):

```sh
percolate brew0x/percolate
```

Record what you saw, then replay any view of it exactly, offline:

```sh
percolate brew0x/percolate --record snapshot.json
percolate --fixture snapshot.json
```

From a checkout, open the bundled fictional landscapes:

```sh
percolate --fixture testdata/fixtures/small.json
percolate --fixture testdata/fixtures/busy.json
```

There is also a real public recording, captured from an unauthenticated read of
`hivemq/hivemq-hello-world-datahub-module` (provenance in
`testdata/fixtures/README.md`). Replay it exactly, offline:

```sh
percolate --fixture testdata/fixtures/recorded-hivemq.json
```

In live mode Percolate re-reads the repository every 60 seconds. When a poll
brings changes, the view animates them over a fixed 32 frames: new commits drip
in from the top of their lane, runs that finished settle on their outcome, and
objects that left the snapshot fade. The animation depends only on the two
snapshots and the frame index, so it replays exactly; a failed refresh is noted
in the footer and retried, and unchanged polls do not replay it.

Press arrows or tab to select a mark, enter for details, escape to close details,
and q to quit. Selection reveals offscreen lanes. Resize the terminal freely.

Capture one reproducible frame without ANSI sequences:

```sh
percolate render --fixture testdata/fixtures/small.json --frame 7 --size 100x30 --color=false
percolate version
```

`render` works with a repository too, for example to prove a recording replays
to identical frames. Without arguments, Percolate prints a hint and exits with
status 2; a fetch without any token fails with one line telling you where to
get one. Color is optional in render mode; use a Unicode-capable terminal font.

### Read limits

A live read is a bounded window of recent history, not a repository's entire
lifetime: at most 5000 default-branch commits, 5000 workflow runs, 1000 open
pull requests and 1000 branches, releases and deployments each. These fit an
active repository. If a list still exceeds its limit, Percolate stops with one
clear line rather than drawing a partial landscape that pretends to be the
whole repository; it never shows truncated history as if it were complete.
Fork pull requests use their fork-qualified head label as a lane, so a fork's
`main` is never confused with the upstream `main`; unpublished draft releases
are not shown. The worst case is under 250 API requests.
`PERCOLATE_GITHUB_URL` may point at a different GitHub host over https; plain
http is accepted only on loopback, because every request carries your token.

## Development

```sh
make check
make golden
go generate ./internal/repo/fixturegen
go test ./internal/scene -run '^$' -bench BenchmarkBusyFrame -benchmem
```

Busy uses seed 42: 40 branches, 300 commits, 120 PRs and 200 workflow runs.
Small uses seed 8 with 3 branches, 12 commits, one approved open PR, one running
and one failed workflow, and a release. Both are entirely fictional.

See [visual language](docs/visual-language.md), [shared words](CONTEXT.md), and
[decisions](docs/adr/0002-snapshot-to-frame.md). Dense marks can overlap; every
object stays inspectable by selection. Static renders show the first lane window.

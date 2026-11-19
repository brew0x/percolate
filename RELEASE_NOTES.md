# Percolate v0.1.0

This is the first version I'm putting my name on. Percolate is a window onto a
repository's life. Work drips in at the top as commits and issues, seeps through
branches and pull requests, gets filtered by reviews and checks, and collects at
the bottom as releases and deployments. Leave it on a spare screen and you can
feel how the project is doing; look closer and every mark tells you what it is.

## Get it

Homebrew, on macOS or Linux:

```sh
brew install brew0x/tap/percolate
```

Or with Go 1.27 or later:

```sh
go install github.com/brew0x/percolate/cmd/percolate@latest
```

On Windows, take the `.zip` for your CPU from this release, extract
`percolate.exe` and put it on your `PATH`. `percolate version` should answer
with `percolate 0.1.0`.

## Take it for a spin

```sh
percolate brew0x/percolate
```

The first argument is a repository as `owner/repo`. Give it a token through
`GITHUB_TOKEN` or `gh auth login`; if you'd rather not, you can replay the real
public recording that ships with a checkout:

```sh
percolate --fixture testdata/fixtures/recorded-hivemq.json
```

Arrows or tab step through marks, enter opens their recorded fields, `o` opens
the GitHub page in live mode, and `q` quits. The mouse wheel and clicking work
too.

A recorded snapshot always draws the same picture. If you want a still frame
to compare or share, ask for one:

```sh
percolate render --fixture testdata/fixtures/recorded-hivemq.json --frame 7 --size 100x30 --color=false
```

## What changed on screen

There are flowing lanes for branches, commits descending each lane, pull
request paths converging on their base, reviews at the meeting point, running
CI pulses that settle on their real outcome, and releases at the bottom with
their recorded deployments flowing out. When a live read brings a change, new
commits drip in, finished runs settle, and objects that left fade over 32
frames. Nothing moves just to look busy: stillness means nothing moved.

## What this version promises

- Every mark maps to a recorded field, and `docs/visual-language.md` says which
  one. A run with no recorded jobs says so; a missing state never reads as
  success.
- Live mode re-reads the repository every 60 seconds. Unchanged polls do not
  replay an animation; a failed one shows in the footer and is retried.
- Reads are bounded, and a repository that exceeds a limit gets one clear
  error rather than a partial landscape pretending to be complete.
- The same snapshot and frame index always produce the same frame. Checked-in
  golden frames prove that in CI, from tiny terminals up to a 500-PR landscape.

## What I haven't claimed

I check the render path offline against a real public recording from
`hivemq/hivemq-hello-world-datahub-module`, with its source and capture method
written down in `testdata/fixtures/README.md`. GitHub's GraphQL details need a
token, so those rest on local test servers. A long live watch is a thing I do
against a real repository, not something a golden frame can prove. If install
or live mode gives you trouble, please open an issue with your OS, the output
of `percolate version`, and the exact command you ran. I read them all.

Thanks to everyone who watched an early build and told me where it was
confusing. Keep telling me.

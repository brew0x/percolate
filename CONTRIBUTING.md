# Contributing

Read VISION.md, CONTEXT.md and AGENTS.md. Keep changes small and focused.
Run `make check`. For visual changes, inspect the rendered fixtures, run
`make golden`, and review the changed frames before committing.
For checked-in snapshots, use seeded fictional fixtures, or record a *public*
repository into `testdata/fixtures/recorded-*.json` with its provenance noted;
never commit private repository data. Include reproducible examples when
reporting bugs: snapshot, frame number, terminal dimensions and expected
behaviour.

## Releasing

Pushing a `vX.Y.Z` tag runs `.github/workflows/release.yml`: GoReleaser builds
the archives, publishes the GitHub release and writes a cask into the
`brew0x/homebrew-tap` repository. That cross-repository write needs a
repository secret named `HOMEBREW_TAP_GITHUB_TOKEN` holding a token with write
access to the tap; the release job's `GITHUB_TOKEN` only covers this repository.
Pull requests run the same release config as a snapshot with publishing skipped,
so no secret is available or needed there.

# Contributing

Read VISION.md, CONTEXT.md and AGENTS.md. Keep changes small and focused.
Run `make check`. For visual changes, inspect the rendered fixtures, run
`make golden`, and review the changed frames before committing.
For checked-in snapshots, use seeded fictional fixtures, or record a *public*
repository into `testdata/fixtures/recorded-*.json` with its provenance noted;
never commit private repository data. Include reproducible examples when
reporting bugs: snapshot, frame number, terminal dimensions and expected
behaviour.

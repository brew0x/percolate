# Maintainer guide

Read VISION.md first. Preserve truth, reproducibility and calm at scale.

## Layout
- internal/repo: JSON snapshot structs; no terminal or network dependencies.
- internal/repo/fixturegen: seeded fictional data; `go generate ./internal/repo/fixturegen` regenerates checked-in fixtures.
- internal/scene: pure Build and Frame, object identities and cell styles.
- cmd/percolate: flags, terminal lifecycle, selection, details, color output.
- testdata/fixtures and testdata/golden: recorded inputs and reviewed frames.
- docs/visual-language.md: every visible mark's meaning; docs/adr: decisions.

## Checks and changes
Run `make check` (format, vet, tests). Run `make golden` only after inspecting
intentional rendering changes. Benchmark with
`go test ./internal/scene -run '^$' -bench BenchmarkBusyFrame -benchmem`.

Add a mark in this order: model → scene → visual-language doc → golden.
Keep PRs small. Do not read the wall clock in models or rendering, and never use
randomness outside explicit seeds. Terminal scheduling may use Bubble Tea ticks;
rendering must use only the frame index. Never commit real private repo data.
No live GitHub fetching in this slice. Keep future adapters on the snapshot path.

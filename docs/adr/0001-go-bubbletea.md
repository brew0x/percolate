# 0001: Go with Bubble Tea v2 and Lip Gloss v2

## Context
Percolate needs a portable terminal executable and a small codebase that future
maintainers can extend without learning an application framework.

## Options
- Go and Bubble Tea: one executable, explicit message-driven state updates.
- A web view: rich graphics but requires a browser and a second rendering stack.
- Custom terminal handling: fewer libraries but substantial input/resize work.

## Decision
Use Go 1.27, Bubble Tea v2 for terminal lifecycle, input, ticks and resize, and
Lip Gloss v2 for styles. Pin exact direct versions in go.mod and keep go.sum.
Keep both UI libraries outside the repository and scene models.

## Consequences
Rendering can run without a terminal. The CLI owns scheduling and selection;
models own neither. We accept Unicode terminal font differences and expose a
plain, ANSI-free rendering mode for capture and verification.

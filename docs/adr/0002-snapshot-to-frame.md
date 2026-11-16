# 0002: Snapshot → Scene → Frame

## Context
Every visible state must be faithful and reproducible. Fixtures and future live
GitHub data must travel through the same rendering path.

## Options
- Fetch and draw in a UI update: convenient but timing and network affect output.
- Store terminal cells directly: reproducible but loses object identity.
- Build an object scene from a snapshot, then rasterise at explicit dimensions
  and frame index: separates data, meaning and presentation.

## Decision
Use plain JSON structs in internal/repo. Build(snapshot) creates a scene without
mutating input. Frame(scene, frameIndex, width, height) returns cells with runes
and style IDs. Commit age uses snapshot.now; animation uses frameIndex only.
The UI schedules ticks but passes no clock into rendering. Future live adapters
must produce snapshots, never alternative scene or drawing logic.

## Consequences
Any visual state can be reproduced from recorded data, dimensions, selection,
viewport and frame index. Goldens capture plain geometry; cell-level assertions
protect style determinism and pulses. Dense scenes retain all object identities,
with documented clipping and overlap. Pure rendering is easy to benchmark.
No live fetching or persistence is part of this first slice.

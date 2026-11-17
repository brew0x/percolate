# 0003: Live refresh and snapshot transitions

## Context
Percolate's window should show repository activity as it happens without
putting time, network, or terminal state into rendering. ADR 0002 fixes
`Build(snapshot)` and `Frame(scene, frameIndex, width, height)` as pure. Issue
#4 adds a live refresh that must animate the change between two snapshots.

## Options
- Let the fetcher push terminal cells directly between snapshots: fast, but
  timing and network would leak into what is drawn.
- Animate on wall-clock time in the renderer: convenient, but replayed frames
  would stop being reproducible.
- Poll on a fixed interval into snapshots, then compute a frame-indexed
  transition scene from (old, new, frame) and feed it to the unchanged Frame.

## Decision
`internal/live` owns only refresh policy: a `Fetcher` that returns a snapshot,
a 60-second poll interval, and a `Watcher` that streams changed snapshots.
It never draws. `internal/scene.Transition(prev, next, frame)` is a pure
function of its three arguments, clamping outside `[0, TransitionFrames)` and
equalling `Build(next)` at the last frame. It bakes drip, settle, and fade into
concrete mark and link values so `Frame` needs no animation branches. The TUI
schedules ticks and polls; it owns which two snapshots bracket a transition,
but passes no clock to either renderer. Change detection (`live.Equal`)
compares recorded repository state and ignores `Snapshot.Now`: a fresh capture
timestamp from `BuildSnapshot` is metadata about when a read happened, not a
change to what was read, so an unchanged poll never starts a transition.

## Consequences
A recorded pair of snapshots plus a frame index reproduces any in-between
frame, so goldens review the animation in CI. A failed poll is a footer status,
not a fabricated scene; an unchanged poll does not replay a transition. The
poll interval keeps an always-on window inside GitHub's read budget. Live
animation is opt-in by running a repository; fixtures and `render` stay fixed.

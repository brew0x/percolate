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
change to what was read, so an unchanged poll never starts a transition. A
rate-limit refusal carries GitHub's recorded reset instant from the transport;
both the TUI and `Watcher` schedule the next read for that instant instead of
the ordinary 60-second tick, and return to the ordinary interval after a
successful read. The wait is computed by the scheduler, never by rendering, and
is never guessed from a failed poll.

## Consequences
A recorded pair of snapshots plus a frame index reproduces any in-between
frame, so goldens review the animation in CI. The TUI passes that same
transition-relative index to both `Transition` and `Frame`, so the drawn
change never depends on when a poll happened to arrive. A failed poll is a
footer status, not a fabricated scene; an unchanged poll does not replay a
transition, but it still refreshes the scene with the new capture time, because
commit styles age against `Snapshot.Now` and a quiet repository must not
brighten forever. The poll interval keeps an always-on window inside GitHub's
read budget. Live animation is opt-in by running a repository; fixtures and
`render` stay fixed. The TUI keeps selection by recorded object identity, not
by numeric index: a refresh that inserts a commit ahead of the selected one
cannot silently move the highlight or an open detail panel. An entering newest
commit drips from a row offset rather than a negative rank, so its fall and
the paths and CI that follow it never cross the lane-name row, even in a
one-commit repository. Marks that leave draw beneath the ones that stay, and
the issue layer fills only cells that are empty or its own, so a fading
object can never take a cell a surviving object needs.

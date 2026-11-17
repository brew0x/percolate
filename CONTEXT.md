# Shared language

- **Landscape** — the repository's visible development activity.
- **Lane** — one branch flowing from newest at top toward older work below.
- **Bead** — a commit on its branch lane.
- **Flow** — a pull request's path from head branch into base branch.
- **Spring** — an open issue waiting at the top of a lane, feeding a pull request only when a recorded closing reference names it.
- **Pulse** — a running workflow's repeating, frame-indexed signal.
- **Fade** — a dimmer style for a commit older than 48 snapshot hours.
- **Mark** — a visible object symbol with its recorded identity and details.
- **Snapshot** — JSON-serialisable repository state with an explicit now.
- **Scene** — a deterministic collection of lanes and inspectable marks.
- **Frame** — a bounded grid of runes and style IDs at one animation index.

Avoid: particles, decoration, random motion, dashboard, real-time magic.
Nothing should imply activity that the snapshot does not contain.

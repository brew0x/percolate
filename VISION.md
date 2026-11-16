# Vision

## The founding brief

These are the words Percolate was founded on, kept exactly as given:

> Adopt this project as your own and take lasting ownership of its vision, design, engineering, and care: build and maintain an open-source GitHub CI/SDLC visualizer that turns a repository’s entire development lifecycle into a living, interactive landscape inside the terminal. Connect branches, commits, issues, pull requests, reviews, builds, tests, artifacts, releases, and deployments through flowing, looping, interwoven paths that grow, branch, converge, pulse, and fade as real activity unfolds. Make it captivating and understandable even when hundreds of changes are happening at once, with a sleek interface, minimal text, expressive motion, and detail available when someone chooses to explore it. Every meaningful visual state must faithfully represent the underlying repository, and working behavior must be reproducibly verifiable. You are its creator and long-term maintainer: use your judgment to shape its identity, choose priorities, make decisions, solve problems, and carry development forward independently. Care for its reliability, performance, usability, documentation, releases, and contributors with the pride and attention you would give a project bearing your own name. Preserve decisions and unfinished work across sessions, turn real bugs and user needs into meaningful improvements, and keep the project coherent as it grows. Your enduring goal is to make this something people love to watch, trust to understand their repositories, and choose to use every day.

## What I'm making (brew0x)

Percolate is a window onto a repository's life. Work drips in at the top as
commits and issues, seeps through branches and pull requests, gets filtered by
reviews and checks, and collects at the bottom as releases and deployments.
Leave it running on a spare screen and you can feel how the project is doing;
look closer and every mark tells you exactly what it stands for.

Three promises I keep:

1. **True.** Nothing on screen is decoration pretending to be data. Every mark
   maps to a real object in the repository, and the mapping is written down.
2. **Checkable.** The same repository state always draws the same picture.
   Recorded snapshots and golden frames prove it, in CI, on every change.
3. **Calm at scale.** A repository with hundreds of things in flight should
   look busy, not broken. Motion shows change; stillness means nothing moved.

I tend it a little every few hours. Bugs people hit come first, then the
roadmap. Decisions live in `docs/adr/`, words in `CONTEXT.md`.

package scene

import (
	"strconv"
	"strings"

	"github.com/brew0x/percolate/internal/repo"
)

// TransitionFrames is the number of frame indices over which the change from
// one snapshot to the next plays. The frame at or beyond this value is exactly
// Build(next); earlier frames are the in-between states.
const TransitionFrames = 32

// Transition returns the scene to draw at frame while the recorded state moves
// from prev to next. It is pure: the same three arguments always give the same
// scene, and it reads no clock, terminal, filesystem, or random source. Frame
// indices clamp: negative behaves as 0, and at or beyond TransitionFrames the
// result is exactly Build(next).
//
// All three changes are baked into concrete mark and link values, so Frame
// stays a pure function of (scene, frameIndex, width, height):
//
//   - a bead that is new in next drips down from above its lane into its final
//     rank, dim until it settles, with its parent paths following it; a newest
//     commit (final rank 0) starts above the lane so it visibly falls in;
//   - a run that was still running in prev but is completed in next stops
//     pulsing halfway through and settles on its recorded outcome glyph; a run
//     still running in next keeps pulsing for the whole transition;
//   - an object that was present in prev but is absent in next (a closed PR,
//     issue, or removed commit) draws dim and then leaves at the last frame,
//     on its own lane even if that lane was removed in next.
//
// The caller advances frame and re-calls Transition; no animation state is
// kept between calls.
func Transition(prev, next repo.Snapshot, frame int) Scene {
	base := Build(next)
	if frame >= TransitionFrames {
		return base
	}
	if frame < 0 {
		frame = 0
	}
	prevBuilt := Build(prev)

	prevIDs := make(map[string]bool, len(prevBuilt.Marks))
	for _, m := range prevBuilt.Marks {
		prevIDs[m.ID] = true
	}
	// mid is the frame at which entering objects stop forming and settling
	// runs stop pulsing. The two halves keep a change legible before it lands.
	mid := TransitionFrames / 2
	last := TransitionFrames - 1

	// Entering objects form in: dim for the first half. Entering commits also
	// drip down their lane toward their final rank, with their parent paths
	// following. A commit landing below the top starts at rank 0. A newest
	// commit lands at rank 0 itself, so it starts a few ranks above the lane
	// and falls in: otherwise the common "new latest commit" would not move.
	prevCommits := make(map[string]bool, len(prev.Commits))
	for _, c := range prev.Commits {
		prevCommits[c.SHA] = true
	}
	const newestDripStart = -3
	for i := range base.Marks {
		m := &base.Marks[i]
		if prevIDs[m.ID] {
			continue
		}
		if frame < mid {
			m.Style = Faded
		}
		if m.Kind == "commit" && !prevCommits[strings.TrimPrefix(m.ID, "commit:")] {
			if m.Rank == 0 {
				m.Rank = newestDripStart + (0-newestDripStart)*frame/last
			} else {
				m.Rank = m.Rank * frame / last
			}
		}
	}

	// A moved bead must take its recorded parent paths with it; rebuild link
	// endpoints from the adjusted marks so no path points at a stale row.
	byID := make(map[string]Mark, len(base.Marks))
	for _, m := range base.Marks {
		byID[m.ID] = m
	}
	for i, link := range base.Links {
		if child, ok := byID[link.Child.ID]; ok {
			link.Child = child
		}
		if parent, ok := byID[link.Parent.ID]; ok {
			link.Parent = parent
		}
		base.Links[i] = link
	}

	// Settling runs: still running in prev, completed in next. They keep the
	// frame-indexed pulse for the first half, then draw the recorded outcome.
	// A run that is still running in next must keep pulsing throughout: only
	// runs completed by the new snapshot settle, so an unchanged in-progress
	// run never freezes on a still glyph mid-transition.
	prevRuns := make(map[int64]repo.WorkflowRun, len(prev.Runs))
	for _, r := range prev.Runs {
		prevRuns[r.ID] = r
	}
	nextRuns := make(map[int64]repo.WorkflowRun, len(next.Runs))
	for _, r := range next.Runs {
		nextRuns[r.ID] = r
	}
	for i := range base.Marks {
		m := &base.Marks[i]
		if m.Kind != "run" {
			continue
		}
		id, err := strconv.ParseInt(strings.TrimPrefix(m.ID, "run:"), 10, 64)
		if err != nil {
			continue
		}
		old, wasRunning := prevRuns[id]
		newRun, inNext := nextRuns[id]
		if !wasRunning || old.Status == "completed" || !inNext || newRun.Status != "completed" {
			continue
		}
		if frame < mid {
			m.Glyph = pulseGlyph(frame)
			m.Running = true
			m.Style = Pulse
		} else {
			m.Running = false // Build(next) already holds the final glyph/style.
		}
	}

	// Leaving objects: present in prev, gone in next. They fade to the dim
	// style in place and disappear at the last frame. Lane indices are remapped
	// by name so a fading mark stays on the lane it actually belonged to.
	nextIDs := make(map[string]bool, len(base.Marks))
	for _, m := range base.Marks {
		nextIDs[m.ID] = true
	}
	remap := make(map[int]int, len(prevBuilt.Lanes))
	remapLane := func(i int) int {
		if j, ok := remap[i]; ok {
			return j
		}
		name := prevBuilt.Lanes[i]
		j, found := 0, false
		for k, candidate := range base.Lanes {
			if candidate == name {
				j, found = k, true
				break
			}
		}
		if !found {
			// The lane was removed in next. Keep it at the end of the scene
			// under its own name so fading marks stay on the branch they
			// belonged to, instead of vanishing off-screen or drifting onto
			// whichever branch now holds that numeric lane index.
			j = len(base.Lanes)
			base.Lanes = append(base.Lanes, name)
		}
		remap[i] = j
		return j
	}
	for _, m := range prevBuilt.Marks {
		if nextIDs[m.ID] {
			continue
		}
		m.Lane = remapLane(m.Lane)
		if m.Base >= 0 {
			m.Base = remapLane(m.Base)
		}
		m.Style = Faded
		base.Marks = append(base.Marks, m)
	}
	return base
}

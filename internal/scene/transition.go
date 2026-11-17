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
//   - a bead that is new in next drips down from the top of its lane into its
//     final rank, dim until it settles, with its parent paths following it;
//   - a run that was still running in prev but is completed in next stops
//     pulsing halfway through and settles on its recorded outcome glyph;
//   - an object that was present in prev but is absent in next (a closed PR,
//     issue, or removed commit) draws dim and then leaves at the last frame.
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
	// drip from the top, with their rank growing to its final value by the
	// last frame.
	prevCommits := make(map[string]bool, len(prev.Commits))
	for _, c := range prev.Commits {
		prevCommits[c.SHA] = true
	}
	for i := range base.Marks {
		m := &base.Marks[i]
		if prevIDs[m.ID] {
			continue
		}
		if frame < mid {
			m.Style = Faded
		}
		if m.Kind == "commit" && !prevCommits[strings.TrimPrefix(m.ID, "commit:")] {
			m.Rank = m.Rank * frame / last
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
	prevRuns := make(map[int64]repo.WorkflowRun, len(prev.Runs))
	for _, r := range prev.Runs {
		prevRuns[r.ID] = r
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
		old, ok := prevRuns[id]
		if !ok || old.Status == "completed" {
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
	for i, name := range prevBuilt.Lanes {
		for j, candidate := range base.Lanes {
			if candidate == name {
				remap[i] = j
				break
			}
		}
	}
	for _, m := range prevBuilt.Marks {
		if nextIDs[m.ID] {
			continue
		}
		if lane, ok := remap[m.Lane]; ok {
			m.Lane = lane
		}
		if m.Base >= 0 {
			if lane, ok := remap[m.Base]; ok {
				m.Base = lane
			}
		}
		m.Style = Faded
		base.Marks = append(base.Marks, m)
	}
	return base
}

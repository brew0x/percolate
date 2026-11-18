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
//   - a bead that is new in next drips toward its final rank, dim until it
//     settles, with its parent paths and anchored CI following it; a newest
//     commit (final rank 0) starts one row above the lane so it visibly falls
//     in without its path crossing the lane names;
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

	// entering holds the row values of commits that are new in next, keyed by
	// SHA. Marks anchored to one of them take the same values, so CI stays
	// beside the commit it belongs to while that commit is still falling.
	type rows struct{ rank, drip int }
	entering := make(map[string]rows)

	// Entering objects form in: dim for the first half. Entering commits also
	// drip toward their final row, with their parent paths following. A commit
	// landing below the top starts at rank 0. A newest commit lands at rank 0
	// itself, so it drips from one row above the commit region instead: it
	// visibly falls in without a negative rank feeding Frame's geometry (the
	// parent path would otherwise cross the lane-name row).
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
		if m.Kind != "commit" {
			continue
		}
		sha := strings.TrimPrefix(m.ID, "commit:")
		if prevCommits[sha] {
			continue
		}
		if m.Rank == 0 {
			if frame < mid {
				m.Drip = 1
			}
		} else {
			m.Rank = m.Rank * frame / last
		}
		entering[sha] = rows{rank: m.Rank, drip: m.Drip}
	}

	// Runs, releases and deployments are anchored to a commit's SHA in Build.
	// Follow the entering commit's row so a run drawn two columns right of a
	// new bead does not sit at its final rank while the bead is still falling.
	nextRuns := make(map[int64]repo.WorkflowRun, len(next.Runs))
	for _, r := range next.Runs {
		nextRuns[r.ID] = r
	}
	nextReleases := make(map[string]string, len(next.Releases))
	for _, r := range next.Releases {
		nextReleases[r.Tag] = r.HeadSHA
	}
	nextDeployments := make(map[int64]string, len(next.Deployments))
	for _, d := range next.Deployments {
		nextDeployments[d.ID] = d.HeadSHA
	}
	for i := range base.Marks {
		m := &base.Marks[i]
		var sha string
		switch m.Kind {
		case "run":
			if id, err := strconv.ParseInt(strings.TrimPrefix(m.ID, "run:"), 10, 64); err == nil {
				sha = nextRuns[id].HeadSHA
			}
		case "release":
			sha = nextReleases[strings.TrimPrefix(m.ID, "release:")]
		case "deployment":
			if id, err := strconv.ParseInt(strings.TrimPrefix(m.ID, "deployment:"), 10, 64); err == nil {
				sha = nextDeployments[id]
			}
		}
		if r, ok := entering[sha]; ok {
			m.Rank, m.Drip = r.rank, r.drip
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
			// The finishing run keeps pulsing, but only jobs that were
			// unfinished in prev join it: a job already completed keeps its
			// recorded outcome throughout.
			oldJobs := make(map[string]repo.Job, len(old.Jobs))
			for _, j := range old.Jobs {
				oldJobs[j.Name] = j
			}
			for j := range m.Jobs {
				if j >= len(newRun.Jobs) {
					continue
				}
				if was, ok := oldJobs[newRun.Jobs[j].Name]; ok && was.Running() {
					m.Jobs[j].Glyph = '◌'
					m.Jobs[j].Style = Pulse
					m.Jobs[j].Running = true
				}
			}
		} else {
			// Build(next) already holds the final run and job glyph/styles.
			m.Running = false
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
		if i < 0 || i >= len(prevBuilt.Lanes) {
			// Build never emits an out-of-range lane, but Transition is
			// exported and pure over snapshots: an unlinked issue's margin
			// placeholder can be the only Lane a snapshot holds, and a legal
			// snapshot must not crash the renderer.
			return i
		}
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
	var leaving []Mark
	for _, m := range prevBuilt.Marks {
		if nextIDs[m.ID] {
			continue
		}
		m.Departing = true
		// Only genuine lane indices are remapped. Base holds a lane for a
		// pull request and for a linked issue; every other kind carries the
		// unused zero value there, and an unlinked issue keeps a margin
		// placeholder in Lane with a negative Base. Remapping those would
		// re-invent a removed lane that no fading mark actually draws on.
		if m.Kind == "issue" && m.Base < 0 {
			// Margin placeholder, not a lane; the spring draws at the margin
			// regardless of Lane.
			m.Style = Faded
			leaving = append(leaving, m)
			continue
		}
		m.Lane = remapLane(m.Lane)
		if m.Kind == "pr" || m.Kind == "issue" {
			m.Base = remapLane(m.Base)
		}
		m.Style = Faded
		leaving = append(leaving, m)
	}
	// Leaving marks draw before the surviving ones, so an object that stays
	// keeps any cell it shares with something on its way out: the settled
	// state wins the cell, the fading mark shows only where it is alone.
	base.Marks = append(leaving, base.Marks...)
	return base
}

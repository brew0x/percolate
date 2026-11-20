package scene

import (
	"fmt"
	"strings"

	"github.com/brew0x/percolate/internal/repo"
)

// JobMark is one recorded job inside a workflow run. A run with a recorded
// job list draws one cell per job, in snapshot order, starting two columns
// right of the run's commit; a run whose jobs were never recorded keeps its
// single aggregate mark.
type JobMark struct {
	Glyph   rune
	Style   Style
	Running bool
	Failed  bool
}

// buildRuns turns recorded workflow runs into marks anchored to their head
// commit. A run whose SHA is absent uses the explicit unlinked lane, exactly
// like other objects that cannot be placed.
func buildRuns(s repo.Snapshot, bySHA map[string]Mark, addLane func(string) int) []Mark {
	runs := make([]Mark, 0, len(s.Runs))
	for _, r := range s.Runs {
		anchor, ok := bySHA[r.HeadSHA]
		if !ok {
			anchor = Mark{Lane: addLane("unlinked"), Total: 1}
		}
		glyph, style := runGlyph(r.Status, r.Conclusion), runStyle(r.Status, r.Conclusion)
		running := r.Status != "completed"
		runs = append(runs, Mark{
			ID:      fmt.Sprintf("run:%d", r.ID),
			Kind:    "run",
			Label:   fmt.Sprintf("CI %d", r.ID),
			Detail:  runDetail(r),
			Lane:    anchor.Lane,
			Rank:    anchor.Rank,
			Total:   anchor.Total,
			Glyph:   glyph,
			Style:   style,
			Running: running,
			Jobs:    jobMarks(r.Jobs),
		})
	}
	return runs
}

// jobMarks keeps each job's recorded state. A nil job list stays nil so the
// run falls back to its aggregate mark; an explicitly empty list is recorded
// as zero jobs and also has nothing to draw beside the run.
func jobMarks(jobs []repo.Job) []JobMark {
	if jobs == nil {
		return nil
	}
	out := make([]JobMark, 0, len(jobs))
	for _, j := range jobs {
		glyph := runGlyph(j.Status, j.Conclusion)
		style := runStyle(j.Status, j.Conclusion)
		out = append(out, JobMark{Glyph: glyph, Style: style, Running: j.Running(), Failed: j.Failed()})
	}
	return out
}

// runDetail reports only recorded run fields. A nil job list says so rather
// than claiming a numeric zero; failing jobs are named so the detail panel
// explains a fail mark in the job strip.
func runDetail(r repo.WorkflowRun) string {
	jobs := "jobs not recorded"
	if r.Jobs != nil {
		jobs = fmt.Sprintf("%d jobs", len(r.Jobs))
		if failing := failingJobNames(r.Jobs); failing != "" {
			jobs += " · failing: " + failing
		}
	}
	return fmt.Sprintf("run %d · %s · %s · %s · %s", r.ID, r.Status, r.Conclusion, jobs, r.HeadSHA)
}

func failingJobNames(jobs []repo.Job) string {
	var names []string
	for _, j := range jobs {
		if !j.Failed() {
			continue
		}
		if j.Name == "" {
			names = append(names, "(unnamed)")
		} else {
			names = append(names, j.Name)
		}
	}
	return strings.Join(names, ", ")
}

// RunGlyph keeps success, failure, and other completed outcomes distinct for
// a run and for each recorded job.
func RunGlyph(conclusion string) rune {
	switch {
	case conclusion == "success":
		return '✓'
	case repo.FailedConclusion(conclusion):
		return '✕'
	default:
		return '−'
	}
}

// runGlyph keeps an unfinished run or job on the pulse glyph, whatever its
// recorded conclusion says.
func runGlyph(status, conclusion string) rune {
	if status != "completed" {
		return '◌'
	}
	return RunGlyph(conclusion)
}

// runStyle colours only what the snapshot recorded: a real failure is red,
// success green, and every other completed conclusion keeps the neutral
// palette of its − mark. Cancelled and skipped runs must not read as failures
// just because they are not successes. Aggregate runs and recorded jobs share
// this helper so the same conclusion cannot be coloured differently in the
// two places.
func runStyle(status, conclusion string) Style {
	switch {
	case status != "completed":
		return Pulse
	case repo.FailedConclusion(conclusion):
		return Fail
	case conclusion == "success":
		return Pass
	default:
		return Plain
	}
}

// pulseGlyph is the four-phase, eight-frame-per-phase running signal. Both
// Frame and Transition use it so a live settle and a still frame pulse alike.
func pulseGlyph(frame int) rune {
	cycle := []rune{'◌', '◔', '◑', '◕'}
	i := (((frame % 32) + 32) % 32) / 8
	return cycle[i]
}

// runCells returns the job cells for one run, in recorded order and capped at
// slots. A running job pulses on the frame index; a run that is forming or
// fading during a change dims its whole strip. When more jobs are recorded
// than fit before the next lane, the last cell carries a failure mark if a
// displaced job failed, otherwise the run's own outcome, so no failure is
// dropped silently.
func runCells(m Mark, frameIndex, slots int, selected bool) []Cell {
	if len(m.Jobs) == 0 || slots <= 0 {
		return nil
	}
	shown := min(len(m.Jobs), slots)
	cells := make([]Cell, 0, shown)
	for i := 0; i < shown; i++ {
		j := m.Jobs[i]
		glyph, style := j.Glyph, j.Style
		if j.Running {
			glyph = pulseGlyph(frameIndex)
			style = Pulse
			if m.Style == Faded || (((frameIndex%32)+32)%32 < 16 && !selected) {
				style = Faded
			}
		} else if m.Style == Faded {
			style = Faded
		}
		if selected {
			style = Selected
		}
		cells = append(cells, Cell{Rune: glyph, Style: style})
	}
	if len(m.Jobs) > shown {
		// The last slot is overwritten by the summary, so a failure in that
		// job or any later one would otherwise disappear.
		failedDropped := false
		for i := shown - 1; i < len(m.Jobs); i++ {
			failedDropped = failedDropped || m.Jobs[i].Failed
		}
		glyph, style := m.Glyph, m.Style
		if failedDropped {
			glyph, style = '✕', Fail
		} else if m.Running {
			glyph = pulseGlyph(frameIndex)
			if style != Faded && !selected && ((frameIndex%32)+32)%32 < 16 {
				style = Faded
			}
		}
		if m.Style == Faded {
			style = Faded
		}
		if selected {
			style = Selected
		}
		cells[len(cells)-1] = Cell{Rune: glyph, Style: style}
	}
	return cells
}

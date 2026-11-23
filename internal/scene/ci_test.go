package scene_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/brew0x/percolate/internal/repo"
	"github.com/brew0x/percolate/internal/scene"
)

func runSnapshot(jobs []repo.Job) repo.Snapshot {
	day := time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)
	return repo.Snapshot{
		Now:      day,
		Branches: []repo.Branch{{Name: "main"}},
		Commits:  []repo.Commit{{SHA: "abc", Branch: "main", Author: "robin", Time: day}},
		Runs:     []repo.WorkflowRun{{ID: 1, HeadSHA: "abc", Status: "completed", Conclusion: "failure", Jobs: jobs}},
	}
}

func jobMark(s repo.Snapshot) scene.Mark {
	for _, m := range scene.Build(s).Marks {
		if m.ID == "run:1" {
			return m
		}
	}
	panic("run mark missing")
}

// The reviewed "jobs" fixture keeps the mixed outcomes the golden frames
// show: a running job beside a completed one, a failure among successes, and
// a neutral skipped job that must not be coloured as a failure.
func TestJobsFixtureHasMixedOutcomes(t *testing.T) {
	s := fixture(t, "jobs")
	if len(s.Runs) != 3 {
		t.Fatalf("jobs fixture has %d runs", len(s.Runs))
	}
	running, failed := s.Runs[0], s.Runs[1]
	if running.Status == "completed" || failed.Conclusion != "failure" {
		t.Fatal("jobs fixture lost its demonstration runs")
	}
	if len(running.Jobs) < 2 || running.Jobs[0].Conclusion != "success" || running.Jobs[1].Status != "in_progress" {
		t.Fatalf("running run does not mix a finished job with a running one: %+v", running.Jobs)
	}
	if len(failed.Jobs) < 3 || failed.Jobs[1].Conclusion != "failure" || failed.Jobs[2].Conclusion != "skipped" {
		t.Fatalf("failed run does not show a failure beside other outcomes: %+v", failed.Jobs)
	}
}

// Every recorded job keeps its own state in the strip beside its run. A
// mixed run shows success, failure and a neutral completed job side by side,
// and the failure is the only one in the fail palette; the detail panel names
// exactly the failing jobs.
func TestJobStripShowsMixedOutcomes(t *testing.T) {
	s := runSnapshot([]repo.Job{
		{Name: "build", Status: "completed", Conclusion: "success"},
		{Name: "test", Status: "completed", Conclusion: "failure"},
		{Name: "deploy", Status: "completed", Conclusion: "skipped"},
	})
	g := scene.Frame(scene.Build(s), 0, 60, 20)
	want := []scene.Cell{
		{Rune: '✓', Style: scene.Pass},
		{Rune: '✕', Style: scene.Fail},
		{Rune: '−', Style: scene.Plain},
	}
	for i, w := range want {
		if got := g[4][5+i]; got != w {
			t.Fatalf("job %d = %q, want %q\n%s", i, got, w, g)
		}
	}

	detail := jobMark(s).Detail
	if !strings.Contains(detail, "3 jobs") {
		t.Fatalf("job count lost: %q", detail)
	}
	if !strings.Contains(detail, "failing: test") {
		t.Fatalf("failing job not named: %q", detail)
	}
	if strings.Contains(detail, "failing: build") || strings.Contains(detail, "failing: deploy") {
		t.Fatalf("non-failing job named as failing: %q", detail)
	}
}

// A running job pulses on the same frame index as a running run, while a
// completed job beside it keeps its recorded outcome. The first half of the
// cycle is dim; the second half carries the pulse palette.
func TestJobStripPulsesRunningJobsOnly(t *testing.T) {
	s := runSnapshot([]repo.Job{
		{Name: "build", Status: "completed", Conclusion: "success"},
		{Name: "test", Status: "in_progress"},
		{Name: "lint", Status: "queued"},
	})
	s.Runs[0].Status = "in_progress"
	s.Runs[0].Conclusion = ""

	first := scene.Frame(scene.Build(s), 0, 60, 20)
	if got := first[4][5]; got != (scene.Cell{Rune: '✓', Style: scene.Pass}) {
		t.Fatalf("completed job changed: %q", got)
	}
	if got := first[4][6]; got != (scene.Cell{Rune: '◌', Style: scene.Faded}) {
		t.Fatalf("running job did not start its pulse: %q", got)
	}
	if got := first[4][7]; got != (scene.Cell{Rune: '◌', Style: scene.Faded}) {
		t.Fatalf("queued job did not draw as unfinished: %q", got)
	}

	later := scene.Frame(scene.Build(s), 16, 60, 20)
	if got := later[4][6]; got != (scene.Cell{Rune: '◑', Style: scene.Pulse}) {
		t.Fatalf("running job pulse did not advance: %q", got)
	}
	if got := later[4][5]; got != (scene.Cell{Rune: '✓', Style: scene.Pass}) {
		t.Fatalf("still job joined the pulse: %q", got)
	}
}

// A finishing run pulses while its unfinished jobs pulse; a job that was
// already completed holds its recorded outcome. At the halfway frame every
// job draws its recorded outcome, so no failure is hidden by the settle.
func TestTransitionSettlesRunAndJobsTogether(t *testing.T) {
	prev := runSnapshot([]repo.Job{
		{Name: "build", Status: "completed", Conclusion: "success"},
		{Name: "test", Status: "in_progress"},
	})
	prev.Runs[0].Status, prev.Runs[0].Conclusion = "in_progress", ""
	next := runSnapshot([]repo.Job{
		{Name: "build", Status: "completed", Conclusion: "success"},
		{Name: "test", Status: "completed", Conclusion: "failure"},
	})
	next.Runs[0].Status, next.Runs[0].Conclusion = "completed", "failure"

	early := scene.Frame(scene.Transition(prev, next, 0), 0, 60, 20)
	if got := early[4][5]; got != (scene.Cell{Rune: '✓', Style: scene.Pass}) {
		t.Fatalf("completed job left its outcome during settle: %q\n%s", got, early)
	}
	if got := early[4][6]; got != (scene.Cell{Rune: '◌', Style: scene.Faded}) {
		t.Fatalf("unfinished job did not pulse during settle: %q\n%s", got, early)
	}
	late := scene.Frame(scene.Transition(prev, next, 24), 24, 60, 20)
	if got := late[4][5]; got != (scene.Cell{Rune: '✓', Style: scene.Pass}) {
		t.Fatalf("finished job did not settle: %q\n%s", got, late)
	}
	if got := late[4][6]; got != (scene.Cell{Rune: '✕', Style: scene.Fail}) {
		t.Fatalf("failed job did not settle on its outcome: %q\n%s", got, late)
	}
}

// When more jobs are recorded than fit before the next lane, the last slot
// carries the run's own outcome so a hidden failure is never invisible.
func TestJobStripSummarisesOversizedRun(t *testing.T) {
	s := runSnapshot(nil)
	s.Branches = []repo.Branch{{Name: "main"}, {Name: "w1"}, {Name: "w2"}, {Name: "w3"}}
	jobs := make([]repo.Job, 13)
	for i := range jobs {
		jobs[i] = repo.Job{Name: "check", Status: "completed", Conclusion: "success"}
	}
	jobs[len(jobs)-1].Conclusion = "failure"
	s.Runs[0].Jobs = jobs
	// Deliberately contradictory aggregate: the hidden job's own state wins,
	// so an inconsistent snapshot cannot hide a recorded failure.
	s.Runs[0].Conclusion = "success"

	// Four lanes at 60 columns leave 14 columns per lane; twelve job cells
	// fit between the run and the next lane.
	g := scene.Frame(scene.Build(s), 0, 60, 20)
	if got := g[4][5+11]; got != (scene.Cell{Rune: '✕', Style: scene.Fail}) {
		t.Fatalf("hidden failure not summarised: %q\n%s", got, g)
	}
}

// A completed run with a neutral conclusion keeps the plain palette of its −
// mark; it is neither a success nor a failure. Recorded jobs keep their own
// palettes: success is green, cancelled is neutral, neither is red.
func TestNeutralRunKeepsNeutralStyle(t *testing.T) {
	s := runSnapshot([]repo.Job{
		{Name: "build", Status: "completed", Conclusion: "success"},
		{Name: "test", Status: "completed", Conclusion: "cancelled"},
	})
	s.Runs[0].Conclusion = "skipped"

	mark := jobMark(s)
	if mark.Glyph != '−' || mark.Style != scene.Plain {
		t.Fatalf("neutral run mark = %q/%v, want −/Plain", mark.Glyph, mark.Style)
	}
	if got := mark.Jobs[0].Style; got != scene.Pass {
		t.Fatalf("successful job style = %v, want Pass", got)
	}
	if got := mark.Jobs[1]; got.Glyph != '−' || got.Style != scene.Plain {
		t.Fatalf("cancelled job = %q/%v, want −/Plain", got.Glyph, got.Style)
	}

	// A recorded job strip replaces the aggregate glyph in the frame, so the
	// cells themselves must carry each job's own palette.
	g := scene.Frame(scene.Build(s), 0, 60, 20)
	for col, want := range []scene.Cell{
		{Rune: '✓', Style: scene.Pass},
		{Rune: '−', Style: scene.Plain},
	} {
		if got := g[4][5+col]; got != want {
			t.Fatalf("cell %d = %q, want %q\n%s", col, got, want, g)
		}
	}
}

// A run with no recorded job list keeps its aggregate mark, and an explicitly
// empty list also draws nothing beside it; details still distinguish the two.
func TestRunWithoutJobsKeepsAggregateMark(t *testing.T) {
	missing := runSnapshot(nil)
	g := scene.Frame(scene.Build(missing), 0, 60, 20)
	if got := g[4][5]; got != (scene.Cell{Rune: '✕', Style: scene.Fail}) {
		t.Fatalf("aggregate run mark missing: %q\n%s", got, g)
	}
	if detail := jobMark(missing).Detail; !strings.Contains(detail, "jobs not recorded") {
		t.Fatalf("unrecorded jobs lost: %q", detail)
	}

	empty := runSnapshot([]repo.Job{})
	g = scene.Frame(scene.Build(empty), 0, 60, 20)
	if got := g[4][5]; got != (scene.Cell{Rune: '✕', Style: scene.Fail}) {
		t.Fatalf("empty job list should keep the run mark: %q\n%s", got, g)
	}
	if detail := jobMark(empty).Detail; !strings.Contains(detail, "0 jobs") {
		t.Fatalf("empty job list lost: %q", detail)
	}
}

// sharedAnchorSnapshot puts two runs on one commit, so both anchor to the
// same cell. The runs list is newest first, like GitHub's, so Runs[0] is the
// run a shared anchor must resolve to.
func sharedAnchorSnapshot(newest, oldest []repo.Job) repo.Snapshot {
	day := time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)
	return repo.Snapshot{
		Now:      day,
		Branches: []repo.Branch{{Name: "main"}},
		Commits:  []repo.Commit{{SHA: "abc", Branch: "main", Author: "robin", Time: day}},
		Runs: []repo.WorkflowRun{
			{ID: 2, HeadSHA: "abc", Status: "completed", Conclusion: "success", Jobs: newest},
			{ID: 1, HeadSHA: "abc", Status: "completed", Conclusion: "success", Jobs: oldest},
		},
	}
}

// Several runs can share one anchor cell: the unlinked lane collects every
// run whose commit the snapshot does not hold, and a compressed row can
// bring several runs to one cell. The anchor then shows exactly one run —
// the newest, painted last — and no older run's strip cells survive beside
// it, so the cells on screen never mix two runs' marks into one apparent
// job strip.
func TestSharedRunAnchorShowsNewestRunAlone(t *testing.T) {
	check := func(t *testing.T, s repo.Snapshot, frame int) (scene.Grid, scene.Owners) {
		t.Helper()
		g, owners := scene.FrameWithOwners(scene.Build(s), frame, 60, 20)
		for y := range g {
			for x := 1; x < len(g[y]); x++ {
				a, b := owners[y][x-1], owners[y][x]
				if strings.HasPrefix(a, "run:") && strings.HasPrefix(b, "run:") && a != b {
					t.Fatalf("cells %d,%d name two runs %q and %q\n%s", x, y, a, b, g)
				}
			}
		}
		return g, owners
	}
	strip := []repo.Job{
		{Name: "build", Status: "completed", Conclusion: "success"},
		{Name: "release", Status: "completed", Conclusion: "skipped"},
	}

	// A newer, shorter strip must not leave the older strip's tail beside
	// it: the anchor shows the newest run's own cells and nothing else.
	t.Run("newer shorter strip clears the older tail", func(t *testing.T) {
		g, owners := check(t, sharedAnchorSnapshot(strip[:1], strip), 0)
		if got := g[4][5]; got != (scene.Cell{Rune: '✓', Style: scene.Pass}) {
			t.Fatalf("newest run's job cell = %q, want ✓\n%s", got, g)
		}
		if got := g[4][6]; got != (scene.Cell{Rune: ' ', Style: scene.Plain}) {
			t.Fatalf("older run's strip cell survived: %q\n%s", got, g)
		}
		if owners[4][5] != "run:2" || owners[4][6] != "" {
			t.Fatalf("cells name %q and %q, want the newest run and no one\n%s", owners[4][5], owners[4][6], g)
		}
	})

	// A newest run with no recorded job list keeps its single aggregate mark,
	// and the older run's longer strip does not show through beside it.
	t.Run("newer aggregate clears the older strip", func(t *testing.T) {
		s := sharedAnchorSnapshot(nil, strip)
		g, owners := check(t, s, 0)
		if got := g[4][5]; got != (scene.Cell{Rune: '✓', Style: scene.Pass}) {
			t.Fatalf("newest run's aggregate = %q, want ✓\n%s", got, g)
		}
		if got := g[4][6]; got != (scene.Cell{Rune: ' ', Style: scene.Plain}) {
			t.Fatalf("older run's strip cell survived: %q\n%s", got, g)
		}
		if owners[4][5] != "run:2" || owners[4][6] != "" {
			t.Fatalf("cells name %q and %q, want the newest run and no one\n%s", owners[4][5], owners[4][6], g)
		}
	})

	// When the newest run holds the longer strip, it draws whole and every
	// cell of it names that one run.
	t.Run("newest strip draws whole", func(t *testing.T) {
		g, owners := check(t, sharedAnchorSnapshot(strip, strip[:1]), 0)
		for i, want := range []scene.Cell{
			{Rune: '✓', Style: scene.Pass},
			{Rune: '−', Style: scene.Plain},
		} {
			if got := g[4][5+i]; got != want {
				t.Fatalf("strip cell %d = %q, want %q\n%s", i, got, want, g)
			}
			if owners[4][5+i] != "run:2" {
				t.Fatalf("strip cell %d names %q, want run:2\n%s", i, owners[4][5+i], g)
			}
		}
	})

	// A selected older run still draws last so it stays inspectable: the
	// cells then name the selected run, and only that run.
	t.Run("selected older run keeps its cells", func(t *testing.T) {
		built := scene.Build(sharedAnchorSnapshot(strip[:1], strip))
		built.SelectedID = "run:1"
		g, owners := scene.FrameWithOwners(built, 0, 60, 20)
		for i, want := range []scene.Cell{
			{Rune: '✓', Style: scene.Selected},
			{Rune: '−', Style: scene.Selected},
		} {
			if got := g[4][5+i]; got != want {
				t.Fatalf("selected strip cell %d = %q, want %q\n%s", i, got, want, g)
			}
			if owners[4][5+i] != "run:1" {
				t.Fatalf("selected strip cell %d names %q, want run:1\n%s", i, owners[4][5+i], g)
			}
		}
	})
}

// The public jobs recording stacks 201 runs whose commits the snapshot does
// not hold onto the unlinked lane, so its goldens are the shared-anchor case
// at every replay size. The strip visible there must be one run's — the
// newest unlinked run's real job list, the mixed-conclusion pair the
// recording exists to show — and no cell in any frame may name a second run
// beside a run's own cells.
func TestRecordedJobsStripBelongsToOneRun(t *testing.T) {
	s := fixture(t, "recorded-jobs")
	known := map[string]bool{}
	for _, c := range s.Commits {
		known[c.SHA] = true
	}
	newestUnlinked := ""
	for _, r := range s.Runs {
		if !known[r.HeadSHA] {
			newestUnlinked = fmt.Sprintf("run:%d", r.ID)
			break
		}
	}
	if newestUnlinked == "" {
		t.Fatal("recording lost its unlinked runs")
	}
	for _, size := range [][2]int{{100, 30}, {60, 20}, {60, 7}} {
		g, owners := scene.FrameWithOwners(scene.Build(s), 0, size[0], size[1])
		for y := range g {
			for x := 1; x < len(g[y]); x++ {
				a, b := owners[y][x-1], owners[y][x]
				if strings.HasPrefix(a, "run:") && strings.HasPrefix(b, "run:") && a != b {
					t.Fatalf("%dx%d: cells %d,%d name two runs %q and %q", size[0], size[1], x, y, a, b)
				}
			}
		}
		var strip []int
		for y := range g {
			for x := range g[y] {
				if owners[y][x] == newestUnlinked {
					strip = append(strip, x)
				}
			}
		}
		if len(strip) != 2 || strip[1] != strip[0]+1 {
			t.Fatalf("%dx%d: newest unlinked run %s owns %v, want one two-cell strip",
				size[0], size[1], newestUnlinked, strip)
		}
		x, y := strip[0], 0
		for yy := range g {
			if owners[yy][x] == newestUnlinked {
				y = yy
			}
		}
		if g[y][x].Rune != '✓' || g[y][x+1].Rune != '−' {
			t.Fatalf("%dx%d: unlinked strip shows %q%q, want the recorded dry-run success beside the skipped release",
				size[0], size[1], g[y][x].Rune, g[y][x+1].Rune)
		}
	}
}

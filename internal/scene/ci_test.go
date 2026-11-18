package scene_test

import (
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

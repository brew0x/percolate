package scene_test

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/brew0x/percolate/internal/repo"
	"github.com/brew0x/percolate/internal/repo/fixturegen"
	"github.com/brew0x/percolate/internal/scene"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite reviewed golden frames")

func fixture(t testing.TB, name string) repo.Snapshot {
	t.Helper()
	data, err := os.ReadFile("../../testdata/fixtures/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var s repo.Snapshot
	if err = json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	return s
}
func TestGolden(t *testing.T) {
	// recorded-hivemq is a real public repository recording (see
	// testdata/fixtures/README.md); its frames are the replay proof for #3.
	// recorded-pull-details is a focused public recording whose PRs exercise
	// a long review history and an octopus head commit; its frames are the
	// replay proof for the pull-detail pagination work in #19.
	for _, name := range []string{"small", "busy", "scale", "jobs", "release", "recorded-hivemq", "recorded-pull-details"} {
		// 60x7 is a supported compact frame: its shelf shares the PR row, so
		// these frames review that the spring lifts clear of the diamond.
		for _, size := range [][2]int{{100, 30}, {60, 20}, {60, 7}} {
			for _, frame := range []int{0, 7, 30} {
				key := fmt.Sprintf("%s-%dx%d-%02d", name, size[0], size[1], frame)
				t.Run(key, func(t *testing.T) {
					got := scene.Frame(scene.Build(fixture(t, name)), frame, size[0], size[1]).String() + "\n"
					path := filepath.Join("../../testdata/golden", key+".txt")
					if *update {
						if err := os.WriteFile(path, []byte(got), 0644); err != nil {
							t.Fatal(err)
						}
					}
					want, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					if got != string(want) {
						t.Fatalf("frame differs from %s; inspect before make golden", path)
					}
				})
			}
		}
	}
}

// The busy replay frames are the proof that all three review marks are
// visible, not just the two decisive ones. Compressed rows suppress a mark at
// a convergence shared by flows with differing states, so a waiting ▽ is easy
// to lose from the goldens entirely; the seeded review classes must keep one
// visible at each replay size.
func TestBusyReplayShowsEveryReviewMark(t *testing.T) {
	s := scene.Build(fixture(t, "busy"))
	for _, size := range [][2]int{{100, 30}, {60, 20}} {
		frame := scene.Frame(s, 0, size[0], size[1]).String()
		for _, glyph := range []rune{'▽', '▼', '⊗'} {
			if !strings.ContainsRune(frame, glyph) {
				t.Fatalf("%dx%d replay is missing %q", size[0], size[1], glyph)
			}
		}
	}
}
func TestDeterminism(t *testing.T) {
	for _, name := range []string{"small", "busy", "scale", "jobs", "release", "recorded-hivemq", "recorded-pull-details"} {
		s := fixture(t, name)
		before, _ := json.Marshal(s)
		a := scene.Frame(scene.Build(s), 7, 100, 30)
		b := scene.Frame(scene.Build(s), 7, 100, 30)
		if !reflect.DeepEqual(a, b) {
			t.Fatal("cells or styles changed")
		}
		after, _ := json.Marshal(s)
		if string(before) != string(after) {
			t.Fatal("snapshot mutated")
		}
	}
}
func TestEveryPRRepresented(t *testing.T) {
	s := fixture(t, "busy")
	built := scene.Build(s)
	ids := map[string]int{}
	for _, m := range built.Marks {
		if m.Kind == "pr" {
			ids[m.ID]++
		}
	}
	if len(ids) != len(s.PullRequests) {
		t.Fatal("PRs lost")
	}
	for _, p := range s.PullRequests {
		if ids[fmt.Sprintf("pr:%d", p.Number)] != 1 {
			t.Fatalf("PR %d missing or duplicated", p.Number)
		}
	}
}
func TestRunOutcomesAndPulse(t *testing.T) {
	s := repo.Snapshot{Branches: []repo.Branch{{Name: "main"}}, Commits: []repo.Commit{{SHA: "a", Branch: "main"}}, Runs: []repo.WorkflowRun{{ID: 1, HeadSHA: "a", Status: "completed", Conclusion: "success"}}}
	pass := scene.Frame(scene.Build(s), 0, 60, 20).String()
	s.Runs[0].Conclusion = "failure"
	fail := scene.Frame(scene.Build(s), 0, 60, 20).String()
	if !strings.Contains(pass, "✓") || !strings.Contains(fail, "✕") || pass == fail {
		t.Fatal("run outcomes indistinguishable")
	}
	s.Runs[0].Status = "in_progress"
	built := scene.Build(s)
	if reflect.DeepEqual(scene.Frame(built, 0, 60, 20), scene.Frame(built, 30, 60, 20)) {
		t.Fatal("running run does not pulse")
	}
	if !reflect.DeepEqual(scene.Frame(built, 0, 60, 20), scene.Frame(built, 32, 60, 20)) {
		t.Fatal("pulse does not loop")
	}
}

// A run whose jobs were never recorded must not read as a numeric zero.
func TestRunJobDetails(t *testing.T) {
	s := repo.Snapshot{Runs: []repo.WorkflowRun{
		{ID: 1, HeadSHA: "a", Status: "completed", Conclusion: "success"},
		{ID: 2, HeadSHA: "a", Status: "completed", Conclusion: "success", Jobs: []repo.Job{{Name: "check", Status: "completed", Conclusion: "success"}}},
		{ID: 3, HeadSHA: "a", Status: "completed", Conclusion: "success", Jobs: []repo.Job{}},
	}}
	marks := scene.Build(s).Marks
	if len(marks) != 3 {
		t.Fatalf("expected three run marks, got %d", len(marks))
	}
	if want := "jobs not recorded"; !strings.Contains(marks[0].Detail, want) || strings.Contains(marks[0].Detail, "0 jobs") {
		t.Fatalf("unrecorded jobs rendered as data: %q", marks[0].Detail)
	}
	if !strings.Contains(marks[1].Detail, "1 job") || strings.Contains(marks[1].Detail, "1 jobs") {
		t.Fatalf("one recorded job was not singular: %q", marks[1].Detail)
	}
	if !strings.Contains(marks[2].Detail, "0 jobs") {
		t.Fatalf("explicitly empty job list lost: %q", marks[2].Detail)
	}
}

func TestBusyRenderBudget(t *testing.T) {
	s := scene.Build(fixture(t, "busy"))
	start := time.Now()
	for i := 0; i < 20; i++ {
		scene.Frame(s, i, 100, 30)
	}
	if average := time.Since(start) / 20; average > 100*time.Millisecond {
		t.Fatalf("average frame %s exceeds generous 100ms bound", average)
	}
}
func BenchmarkBusyFrame(b *testing.B) {
	s := scene.Build(fixture(b, "busy"))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scene.Frame(s, i, 100, 30)
	}
}

// The 500-PR landscape is the scale budget from #9: a frame must stay well
// under 5 ms so a live terminal keeps its cadence. The test below enforces
// that bound inside make check (and therefore CI); the benchmark reports the
// measured cost for the same scene.
func TestScaleRenderBudget(t *testing.T) {
	s := scene.Build(fixture(t, "scale"))
	const frames = 40
	start := time.Now()
	for i := 0; i < frames; i++ {
		scene.Frame(s, i, 100, 30)
	}
	if average := time.Since(start) / frames; average > 5*time.Millisecond {
		t.Fatalf("average scale frame %s exceeds 5 ms bound", average)
	}
}
func BenchmarkScaleFrame(b *testing.B) {
	s := scene.Build(fixture(b, "scale"))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scene.Frame(s, i, 100, 30)
	}
}
func TestNarrowFrames(t *testing.T) {
	s := scene.Build(fixture(t, "busy"))
	for _, size := range [][2]int{{0, 0}, {1, 1}, {4, 6}, {5, 7}, {60, 7}, {60, 20}} {
		g := scene.Frame(s, -1, size[0], size[1])
		if len(g) != size[1] {
			t.Fatal("height")
		}
		for _, row := range g {
			if len(row) != size[0] {
				t.Fatal("width")
			}
		}
	}
	// At height 7 the busy snapshot has a linked spring on a visible head
	// lane. Its shelf shares the PR row, so the diamond must survive and no
	// spring may be left on that row; the spring moves above the lane names.
	compact := scene.Frame(s, -1, 60, 7)
	diamonds, shelfSprings, liftedSprings := 0, 0, 0
	for x, c := range compact[3] {
		if c.Rune == '◇' {
			diamonds++
		}
		if c.Rune == '∿' {
			shelfSprings++
		}
		if compact[1][x].Rune == '∿' {
			liftedSprings++
		}
	}
	if diamonds == 0 || shelfSprings != 0 || liftedSprings == 0 {
		t.Fatalf("compact spring/PR collision:\n%s", compact.String())
	}
}

// Age is measured against recorded time, including the exact fade boundary.
func TestRecordedAge(t *testing.T) {
	now := time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC)
	snapshot := repo.Snapshot{Now: now, Branches: []repo.Branch{{Name: "main"}}, Commits: []repo.Commit{
		{SHA: "fresh", Branch: "main", Time: now.Add(-48 * time.Hour)},
		{SHA: "old", Branch: "main", Time: now.Add(-48*time.Hour - time.Second)},
	}}
	built := scene.Build(snapshot)
	if built.Marks[0].Style != scene.Fresh || built.Marks[1].Style != scene.Faded {
		t.Fatal("recorded fade threshold changed")
	}
	snapshot.Now = now.Add(-time.Hour)
	for _, m := range scene.Build(snapshot).Marks {
		if m.Style != scene.Fresh {
			t.Fatal("fade did not follow snapshot now")
		}
	}
}

func TestGeneratedFixture(t *testing.T) {
	recorded := fixture(t, "busy")
	if len(recorded.Branches) != 40 || len(recorded.Commits) != 300 || len(recorded.PullRequests) != 120 || len(recorded.Runs) != 200 {
		t.Fatal("busy fixture scale changed")
	}
	if !reflect.DeepEqual(recorded, fixturegen.Generate(42, 40, 300, 120, 200)) {
		t.Fatal("recorded fixture no longer matches its seed")
	}
	scale := fixture(t, "scale")
	if len(scale.Branches) != 40 || len(scale.Commits) != 300 || len(scale.PullRequests) != 500 || len(scale.Runs) != 200 {
		t.Fatal("scale fixture shape changed")
	}
	if !reflect.DeepEqual(scale, fixturegen.Generate(42, 40, 300, 500, 200)) {
		t.Fatal("scale fixture no longer matches its seed")
	}
	small := fixture(t, "small")
	if len(small.Branches) != 3 || len(small.Commits) != 12 || len(small.PullRequests) != 1 || len(small.Runs) != 2 || len(small.Releases) != 1 {
		t.Fatal("small fixture shape changed")
	}
	if small.PullRequests[0].State != "open" || small.PullRequests[0].Reviews[0].State != "approved" || small.Runs[0].Status != "in_progress" || small.Runs[1].Conclusion != "failure" {
		t.Fatal("small fixture lost its demonstration states")
	}
}

// A compact frame puts a pull request's number-label row on the lane names.
// A branch name is recorded data too: the number must not paint a hybrid that
// names no object. Taller frames still label the flow, and the compact frame
// keeps the diamond and its details.
func TestCompactFrameKeepsLaneNames(t *testing.T) {
	s := scene.Build(fixture(t, "small"))
	compact := scene.Frame(s, 0, 60, 7)
	names := strings.Split(compact.String(), "\n")[2]
	for _, name := range []string{"main", "flow-01", "flow-02"} {
		if !strings.Contains(names, name) {
			t.Fatalf("compact frame lost the lane name %q: %q", name, names)
		}
	}
	if strings.Contains(names, "#") {
		t.Fatalf("compact frame painted a PR number over a lane name: %q", names)
	}
	if !strings.ContainsRune(compact.String(), '◇') {
		t.Fatal("compact frame lost the PR diamond")
	}
	tall := scene.Frame(s, 0, 60, 20)
	if !strings.Contains(tall.String(), "#1") {
		t.Fatal("a frame with room lost the PR number label")
	}
}

// Every junction comes from a known parent, even with missing history and PRs.
func TestParentPaths(t *testing.T) {
	now := time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)
	s := repo.Snapshot{Now: now, Branches: []repo.Branch{{Name: "main"}, {Name: "topic"}}, Commits: []repo.Commit{
		{SHA: "root", Branch: "main", Time: now.Add(-3 * time.Hour)},
		{SHA: "fork", Branch: "topic", Parents: []string{"root"}, Time: now.Add(-2 * time.Hour)},
		{SHA: "base", Branch: "main", Parents: []string{"root", "missing"}, Time: now.Add(-time.Hour)},
		{SHA: "merge", Branch: "main", Parents: []string{"base", "fork"}, Time: now},
	}}
	built := scene.Build(s)
	if len(built.Links) != 4 {
		t.Fatalf("got %d parent paths", len(built.Links))
	}
	cross := 0
	for _, link := range built.Links {
		found := false
		for _, c := range s.Commits {
			for _, p := range c.Parents {
				if link.Child.ID == "commit:"+c.SHA && link.Parent.ID == "commit:"+p {
					found = true
				}
			}
		}
		if !found {
			t.Fatalf("invented junction: %+v", link)
		}
		if link.Child.Lane != link.Parent.Lane {
			cross++
		}
	}
	if cross != 2 {
		t.Fatalf("want fork and merge, got %d", cross)
	}
	g := scene.Frame(built, 0, 60, 20)
	// The fork bend points back up at its child and across at the parent;
	// the merge bend survives the through main-line vertical as a junction.
	if g[12][31].Rune != '╯' {
		t.Fatalf("fork corner points the wrong way: %q", g.String())
	}
	if g[9][3].Rune != '├' {
		t.Fatalf("merge corner lost to the lane vertical: %q", g.String())
	}
	for _, m := range built.Marks {
		if m.Lane == 1 && m.Style != scene.Faded {
			t.Fatal("merged tip lane did not fade")
		}
	}
	// Without parent evidence, separate lanes must never look connected.
	s.Commits[1].Parents = nil
	s.Commits[3].Parents = []string{"base"}
	if strings.ContainsAny(scene.Frame(scene.Build(s), 0, 60, 20).String(), "╭╮╰╯├┤") {
		t.Fatal("junction without cross-lane parent")
	}
}

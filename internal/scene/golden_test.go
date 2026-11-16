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
	for _, name := range []string{"small", "busy"} {
		for _, size := range [][2]int{{100, 30}, {60, 20}} {
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
func TestDeterminism(t *testing.T) {
	for _, name := range []string{"small", "busy"} {
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
func TestNarrowFrames(t *testing.T) {
	s := scene.Build(fixture(t, "busy"))
	for _, size := range [][2]int{{0, 0}, {1, 1}, {4, 6}, {5, 7}, {60, 20}} {
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
	small := fixture(t, "small")
	if len(small.Branches) != 3 || len(small.Commits) != 12 || len(small.PullRequests) != 1 || len(small.Runs) != 2 || len(small.Releases) != 1 {
		t.Fatal("small fixture shape changed")
	}
	if small.PullRequests[0].State != "open" || small.PullRequests[0].Reviews[0].State != "approved" || small.Runs[0].Status != "in_progress" || small.Runs[1].Conclusion != "failure" {
		t.Fatal("small fixture lost its demonstration states")
	}
}

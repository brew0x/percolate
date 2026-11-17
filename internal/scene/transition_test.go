package scene_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/brew0x/percolate/internal/repo"
	"github.com/brew0x/percolate/internal/scene"
)

// rowText is the visible runes of one frame row, without trailing padding.
func rowText(row []scene.Cell) string {
	var b strings.Builder
	for _, c := range row {
		b.WriteRune(c.Rune)
	}
	return strings.TrimSpace(b.String())
}

// Transition is pure over (prev, next, frame): repeated calls agree, two
// different recorded fixture pairs stay deterministic, and the final frame is
// exactly Build(next) so a finished transition leaves no residue.
func TestTransitionIsPureAndSettles(t *testing.T) {
	prev, next := fixture(t, "small"), fixture(t, "small-next")
	for _, frame := range []int{-5, 0, 1, 7, 16, 31, scene.TransitionFrames, 100} {
		if a, b := scene.Transition(prev, next, frame), scene.Transition(prev, next, frame); !reflect.DeepEqual(a, b) {
			t.Fatalf("frame %d is not pure", frame)
		}
	}
	if got, want := scene.Transition(prev, next, scene.TransitionFrames), scene.Build(next); !reflect.DeepEqual(got, want) {
		t.Fatal("final transition frame differs from Build(next)")
	}
	if got, want := scene.Transition(prev, next, 100), scene.Build(next); !reflect.DeepEqual(got, want) {
		t.Fatal("frame past the transition differs from Build(next)")
	}
	// A second, unrelated pair of recorded fixtures proves the function stays
	// deterministic on a large, dense scene too.
	a := scene.Transition(fixture(t, "busy"), fixture(t, "recorded-hivemq"), 7)
	b := scene.Transition(fixture(t, "busy"), fixture(t, "recorded-hivemq"), 7)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("busy -> recorded transition is not deterministic")
	}
}

func markByID(t *testing.T, s scene.Scene, id string) scene.Mark {
	t.Helper()
	for _, m := range s.Marks {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("mark %q not in scene", id)
	return scene.Mark{}
}

// The three promised changes are visible in the scene values themselves:
// drip, settle and fade are all frame-indexed and need no clock.
func TestTransitionDripSettleFade(t *testing.T) {
	prev, next := fixture(t, "small"), fixture(t, "small-next")
	const newSHA = "0000000d0000000000000000000000000000000d"

	// A new bead starts at the top of its lane and reaches its final rank at
	// the last frame; its parent path follows the moving bead.
	first := markByID(t, scene.Transition(prev, next, 0), "commit:"+newSHA)
	midway := markByID(t, scene.Transition(prev, next, 16), "commit:"+newSHA)
	final := markByID(t, scene.Transition(prev, next, scene.TransitionFrames), "commit:"+newSHA)
	if first.Rank != 0 || midway.Rank <= first.Rank || final.Rank <= midway.Rank {
		t.Fatalf("new commit did not drip down: %d -> %d -> %d", first.Rank, midway.Rank, final.Rank)
	}
	if first.Style != scene.Faded || final.Style != scene.Fresh {
		t.Fatal("new commit did not form into its final style")
	}
	linked := false
	for _, link := range scene.Transition(prev, next, 0).Links {
		if link.Child.ID == "commit:"+newSHA && link.Child.Rank == 0 {
			linked = true
		}
	}
	if !linked {
		t.Fatal("new commit's parent path stayed at its final row")
	}

	// A run that finished in next pulses early, then settles on its recorded
	// outcome glyph and style.
	early := markByID(t, scene.Transition(prev, next, 8), "run:1")
	late := markByID(t, scene.Transition(prev, next, 24), "run:1")
	if !early.Running || early.Glyph == 0 {
		t.Fatal("finishing run did not pulse before settling")
	}
	if late.Running || late.Glyph != '✓' || late.Style != scene.Pass {
		t.Fatalf("finishing run did not settle on success: %+v", late)
	}

	// Objects that disappeared since prev draw dim through the transition and
	// are gone on the last frame.
	for _, id := range []string{"pr:1", "issue:2"} {
		fading := markByID(t, scene.Transition(prev, next, 0), id)
		if fading.Style != scene.Faded {
			t.Fatalf("closed %s did not fade", id)
		}
		if _, ok := hasMark(scene.Transition(prev, next, scene.TransitionFrames), id); ok {
			t.Fatalf("closed %s lingered after the transition", id)
		}
	}
}

// A run that is still running in the new snapshot keeps pulsing through the
// whole transition; only a run completed by the new snapshot settles.
func TestTransitionKeepsRunningRunPulsing(t *testing.T) {
	snap := fixture(t, "small")
	if snap.Runs[0].Status != "in_progress" {
		t.Fatal("fixture setup changed")
	}
	for _, frame := range []int{0, 8, 16, 24, 31} {
		m := markByID(t, scene.Transition(snap, snap, frame), "run:1")
		if !m.Running || m.Glyph != '◌' {
			t.Fatalf("still-running run froze at frame %d: %+v", frame, m)
		}
	}
	// The drawn phase still advances in the second half of the transition.
	a := scene.Frame(scene.Transition(snap, snap, 20), 20, 60, 20)
	b := scene.Frame(scene.Transition(snap, snap, 28), 28, 60, 20)
	if a.String() == b.String() {
		t.Fatal("still-running run stopped pulsing mid-transition")
	}
}

// The common case of a new newest commit (final rank 0) drips in from above
// its lane rather than sitting motionless at rank 0 for every frame. The fall
// is a row offset, not a negative rank, so it stays clear of the lane name;
// CI anchored to the new commit follows it down.
func TestTransitionNewestCommitDrips(t *testing.T) {
	prev, next := fixture(t, "newest"), fixture(t, "newest-next")
	const newSHA = "0000000d0000000000000000000000000000000d"
	first := markByID(t, scene.Transition(prev, next, 0), "commit:"+newSHA)
	midway := markByID(t, scene.Transition(prev, next, 16), "commit:"+newSHA)
	final := markByID(t, scene.Transition(prev, next, scene.TransitionFrames), "commit:"+newSHA)
	if first.Drip == 0 || midway.Drip != 0 || final.Drip != 0 {
		t.Fatalf("newest commit did not fall into its lane: drip %d -> %d -> %d", first.Drip, midway.Drip, final.Drip)
	}
	if first.Rank != 0 || midway.Rank != 0 || final.Rank != 0 {
		t.Fatalf("newest commit rank changed: %d -> %d -> %d", first.Rank, midway.Rank, final.Rank)
	}

	// The bead starts in the gap above the commit region and lands in the top
	// row. Neither it nor its parent path ever cross the lane-name row.
	firstFrame := scene.Frame(scene.Transition(prev, next, 0), 0, 100, 30)
	if firstFrame[3][3].Rune != '●' {
		t.Fatalf("newest commit not staging above its lane:\n%s", firstFrame)
	}
	for _, frame := range []int{0, 8, 16, 24, 31, scene.TransitionFrames} {
		g := scene.Frame(scene.Transition(prev, next, frame), frame, 100, 30)
		if got := rowText(g[2]); got != "main" {
			t.Fatalf("frame %d corrupted the lane name: %q\n%s", frame, got, g)
		}
	}
	landed := scene.Frame(scene.Transition(prev, next, 16), 16, 100, 30)
	if landed[4][3].Rune != '●' {
		t.Fatalf("newest commit did not land in the top row:\n%s", landed)
	}

	linked := false
	for _, link := range scene.Transition(prev, next, 0).Links {
		if link.Child.ID == "commit:"+newSHA && link.Child.Drip == first.Drip {
			linked = true
		}
	}
	if !linked {
		t.Fatal("newest commit's parent path stayed at its final row")
	}

	// A run whose head is the entering commit keeps its row through the drip,
	// so CI stays two columns right of the bead it belongs to.
	for _, frame := range []int{0, 8, 16, 31} {
		s := scene.Transition(prev, next, frame)
		commit, run := markByID(t, s, "commit:"+newSHA), markByID(t, s, "run:2")
		if run.Rank != commit.Rank || run.Drip != commit.Drip {
			t.Fatalf("frame %d: run at %+v, commit at %+v", frame, run, commit)
		}
	}
}

// The very first commit of a repository has Total == 1, where Frame ignores
// Rank entirely. Its drip must still be carried by the row offset and must
// leave the lane name intact.
func TestTransitionFirstCommitDrips(t *testing.T) {
	day := time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)
	prev := repo.Snapshot{Now: day, Branches: []repo.Branch{{Name: "main"}}}
	next := repo.Snapshot{
		Now:      day.Add(time.Minute),
		Branches: []repo.Branch{{Name: "main"}},
		Commits:  []repo.Commit{{SHA: "first", Branch: "main", Author: "robin", Time: day}},
	}
	for _, frame := range []int{0, 8, 15} {
		s := scene.Transition(prev, next, frame)
		m := markByID(t, s, "commit:first")
		if m.Total != 1 || m.Drip == 0 {
			t.Fatalf("first commit did not drip at frame %d: %+v", frame, m)
		}
		g := scene.Frame(s, frame, 60, 20)
		if got := rowText(g[2]); got != "main" {
			t.Fatalf("frame %d corrupted the lane name: %q\n%s", frame, got, g)
		}
		if g[3][3].Rune != '●' {
			t.Fatalf("first commit not staging above its lane at frame %d:\n%s", frame, g)
		}
	}
	for _, frame := range []int{16, 31, scene.TransitionFrames} {
		s := scene.Transition(prev, next, frame)
		if m := markByID(t, s, "commit:first"); m.Drip != 0 {
			t.Fatalf("first commit still dripping at frame %d: %+v", frame, m)
		}
		if g := scene.Frame(s, frame, 60, 20); g[4][3].Rune != '●' {
			t.Fatalf("first commit did not land at frame %d:\n%s", frame, g)
		}
	}
}

// When a branch and its PR/commits disappear together, fading marks stay on
// that branch's lane instead of vanishing or landing on another branch.
func TestTransitionFadeKeepsRemovedLane(t *testing.T) {
	prev, next := fixture(t, "merged"), fixture(t, "merged-next")
	nextBuilt := scene.Build(next)
	if len(nextBuilt.Lanes) != 2 || nextBuilt.Lanes[0] != "main" || nextBuilt.Lanes[1] != "flow-02" {
		t.Fatal("fixture setup changed")
	}
	s := scene.Transition(prev, next, 16)
	kept := -1
	for i, name := range s.Lanes {
		if name == "flow-01" {
			kept = i
		}
	}
	if kept < 0 {
		t.Fatalf("removed lane not retained for fade: %v", s.Lanes)
	}
	for _, id := range []string{
		"commit:0000000200000000000000000000000000000002",
		"pr:1",
		"run:2",
	} {
		m := markByID(t, s, id)
		if m.Style != scene.Faded {
			t.Fatalf("%s did not fade", id)
		}
		if m.Lane != kept {
			t.Fatalf("%s faded on lane %d, want retained flow-01 lane %d", id, m.Lane, kept)
		}
	}
	settled := scene.Transition(prev, next, scene.TransitionFrames)
	if !reflect.DeepEqual(settled.Lanes, nextBuilt.Lanes) {
		t.Fatalf("removed lane lingered after the transition: %v", settled.Lanes)
	}
}

// An unchanged in-progress run must keep its pulse value across the second
// half; this pins the exact regression that the settle path froze it.
func TestTransitionStillRunningMatchesBuildThroughout(t *testing.T) {
	snap := fixture(t, "newest")
	for _, frame := range []int{0, 15, 16, 23, 31} {
		got := markByID(t, scene.Transition(snap, snap, frame), "run:1")
		want := markByID(t, scene.Build(snap), "run:1")
		if got.Running != want.Running || got.Glyph != want.Glyph || got.Style != want.Style {
			t.Fatalf("frame %d differs from Build for still-running run: %+v vs %+v", frame, got, want)
		}
	}
}

func hasMark(s scene.Scene, id string) (scene.Mark, bool) {
	for _, m := range s.Marks {
		if m.ID == id {
			return m, true
		}
	}
	return scene.Mark{}, false
}

// Reviewed transition frames: the new commit drips in, the run settles, and
// the closed PR/issue fade. Frames past the transition must equal the static
// Build(next) frame at the same index.
func TestTransitionGolden(t *testing.T) {
	for _, tc := range []struct{ prev, next, prefix string }{
		{"small", "small-next", "transition"},
		{"newest", "newest-next", "newest"},
		{"merged", "merged-next", "merged"},
	} {
		prev, next := fixture(t, tc.prev), fixture(t, tc.next)
		for _, frame := range []int{0, 8, 16, 24, 31, 32} {
			key := fmt.Sprintf("%s-%02d", tc.prefix, frame)
			got := scene.Frame(scene.Transition(prev, next, frame), frame, 100, 30).String() + "\n"
			path := filepath.Join("../../testdata/golden", key+".txt")
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
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
		}
	}
}

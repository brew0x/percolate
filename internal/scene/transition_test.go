package scene_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/brew0x/percolate/internal/scene"
)

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
	prev, next := fixture(t, "small"), fixture(t, "small-next")
	for _, frame := range []int{0, 8, 16, 24, 31, 32} {
		key := fmt.Sprintf("transition-%02d", frame)
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

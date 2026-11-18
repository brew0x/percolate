package scene

import (
	"fmt"
	"testing"
)

func TestLaneLayoutWeightsActivityAtScale(t *testing.T) {
	var s Scene
	for i := 0; i < 13; i++ {
		s.Lanes = append(s.Lanes, fmt.Sprintf("lane-%02d", i))
	}
	// One busy lane and one quiet lane inside a viewport of six. The rest
	// carry no marks at all, so the spare columns must go to the busy one.
	for i := 0; i < 20; i++ {
		s.Marks = append(s.Marks, Mark{Kind: "commit", Lane: 0})
	}
	s.Marks = append(s.Marks, Mark{Kind: "commit", Lane: 5})
	lay := newLaneLayout(s, 100, 30)
	if lay.count != VisibleLanes(100) || lay.start != 0 {
		t.Fatalf("viewport changed: %+v", lay)
	}
	sum := 0
	for _, w := range lay.widths {
		sum += w
		if w < bundleLane {
			t.Fatalf("quiet lane squeezed to %d columns", w)
		}
	}
	if sum != 100-laneReserveX {
		t.Fatalf("lane widths do not fill the viewport: %d", sum)
	}
	if lay.widths[0] <= lay.widths[5] {
		t.Fatalf("busy lane did not get more space: %v", lay.widths)
	}
	if got := lay.xpos(6); got != lay.right() {
		t.Fatalf("offscreen x is not at the viewport edge: %d", got)
	}
	if got := lay.xpos(7); got != lay.right()+lay.unit {
		t.Fatalf("second offscreen lane is not one unit further: %d", got)
	}
	if lay.xpos(1)-lay.xpos(0) != lay.widths[0] {
		t.Fatal("successive lanes drift apart")
	}
}

func TestLaneLayoutCountsHiddenLanes(t *testing.T) {
	var s Scene
	for i := 0; i < 40; i++ {
		s.Lanes = append(s.Lanes, fmt.Sprintf("lane-%02d", i))
	}
	count := VisibleLanes(60)
	lay := newLaneLayout(s, 60, 20)
	if lay.hiddenLeft != 0 || lay.hiddenRight != 40-count {
		t.Fatalf("first viewport hidden lanes: left=%d right=%d", lay.hiddenLeft, lay.hiddenRight)
	}
	s.FirstLane = 10
	lay = newLaneLayout(s, 60, 20)
	if lay.hiddenLeft != 10 || lay.hiddenRight != 40-10-count {
		t.Fatalf("middle viewport hidden lanes: left=%d right=%d", lay.hiddenLeft, lay.hiddenRight)
	}
	s.FirstLane = 39
	lay = newLaneLayout(s, 60, 20)
	if lay.hiddenLeft != 40-count || lay.hiddenRight != 0 {
		t.Fatalf("last viewport hidden lanes: left=%d right=%d", lay.hiddenLeft, lay.hiddenRight)
	}
	// A landscape that fits every lane hides none and draws no bundle mark.
	lay = newLaneLayout(s, 60*40, 20)
	if lay.hiddenLeft != 0 || lay.hiddenRight != 0 {
		t.Fatalf("fitting landscape hid lanes: left=%d right=%d", lay.hiddenLeft, lay.hiddenRight)
	}
}

func TestOffscreenLaneBundleMarks(t *testing.T) {
	var s Scene
	for i := 0; i < 40; i++ {
		s.Lanes = append(s.Lanes, fmt.Sprintf("lane-%02d", i))
	}
	width := 60
	g := Frame(s, 0, width, 20)
	if got := g[2][width-1]; got != (Cell{'»', Faded}) {
		t.Fatalf("right bundle mark missing: %q", got)
	}
	if got := g[2][0]; got.Rune == '«' {
		t.Fatalf("unexpected left bundle mark: %q", got)
	}
	s.FirstLane = 10
	g = Frame(s, 0, width, 20)
	if got := g[2][0]; got != (Cell{'«', Faded}) {
		t.Fatalf("left bundle mark missing: %q", got)
	}
	if got := g[2][width-1]; got != (Cell{'»', Faded}) {
		t.Fatalf("right bundle mark missing while scrolled: %q", got)
	}
	// When every lane fits, neither edge claims a lane it cannot show.
	g = Frame(s, 0, 60*40, 20)
	for x := range g[2] {
		if g[2][x].Rune == '«' || g[2][x].Rune == '»' {
			t.Fatalf("bundle mark drawn with no hidden lanes at x=%d", x)
		}
	}
}

func TestLaneLayoutStaysUniformBelowScale(t *testing.T) {
	var s Scene
	for i := 0; i < 3; i++ {
		s.Lanes = append(s.Lanes, fmt.Sprintf("lane-%d", i))
	}
	s.Marks = append(s.Marks, Mark{Lane: 0}, Mark{Lane: 0}, Mark{Lane: 0})
	lay := newLaneLayout(s, 100, 30)
	for _, w := range lay.widths {
		if w != lay.unit {
			t.Fatalf("small landscape lost its uniform lanes: %v", lay.widths)
		}
	}
}

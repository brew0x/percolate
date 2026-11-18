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

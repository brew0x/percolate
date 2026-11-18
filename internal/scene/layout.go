package scene

import "sort"

// Lane geometry lives here so every renderer, viewport and test reads the same
// rules. Lanes keep snapshot branch order; a narrow viewport shows a prefix of
// them. Below scaleThreshold every lane keeps the same width. At scale the
// spare columns are shared out by recorded activity: a lane carrying more
// marks gets a wider column for its beads, flows and labels, while a quiet
// lane keeps a narrow minimum. Quiet lanes therefore sit together as a soft
// bundle instead of stealing room from a busy neighbour. Lanes beyond the
// viewport are counted so Frame can stand the whole offscreen run as one edge
// mark rather than hinting at each hidden lane.
//
// No time, randomness or terminal state is consulted: activity is the count
// of marks already resolved by Build, so the same snapshot always lays out
// the same way at the same dimensions.
const (
	laneStartX     = 3  // first lane's bead column
	laneReserveX   = 4  // columns held back from lane widths
	readableLane   = 14 // minimum column for a small landscape
	bundleLane     = 10 // minimum column for a quiet lane at scale
	scaleThreshold = 12 // more lanes than this use activity-weighted widths
)

// VisibleLanes is shared by viewport navigation and rendering.
func VisibleLanes(width int) int { return max(1, (width-laneReserveX)/readableLane) }

type laneLayout struct {
	start, count int
	unit         int   // width used for lanes outside the viewport
	widths       []int // column count of each visible lane
	offsets      []int // bead column of each visible lane
	top, bottom  int   // commit region bounds
	hiddenLeft   int   // lanes before the viewport, bundled by «
	hiddenRight  int   // lanes after the viewport, bundled by »
}

func newLaneLayout(s Scene, width, height int) laneLayout {
	l := laneLayout{
		count:  min(VisibleLanes(width), len(s.Lanes)),
		top:    4,
		bottom: max(4, height*3/5),
	}
	l.start = max(0, min(s.FirstLane, max(0, len(s.Lanes)-l.count)))
	l.hiddenLeft = l.start
	l.hiddenRight = max(0, len(s.Lanes)-l.start-l.count)
	usable := max(0, width-laneReserveX)
	if l.count == 0 {
		l.unit = 1
		return l
	}
	l.unit = max(1, usable/l.count)
	if len(s.Lanes) <= scaleThreshold {
		l.widths = make([]int, l.count)
		for i := range l.widths {
			l.widths[i] = l.unit
		}
	} else {
		activity := make([]int, l.count)
		for _, m := range s.Marks {
			if m.Lane >= l.start && m.Lane < l.start+l.count {
				activity[m.Lane-l.start]++
			}
		}
		// Rank by recorded activity, breaking ties by lane order, then hand
		// out weights from busiest to quietest. Ranking rather than raw counts
		// keeps a small activity difference from rounding away to an even
		// split; the order is still exactly the recorded one.
		order := make([]struct{ index, marks int }, l.count)
		for i, a := range activity {
			order[i] = struct{ index, marks int }{i, a}
		}
		sort.SliceStable(order, func(i, j int) bool { return order[i].marks > order[j].marks })
		weights := make([]int, l.count)
		total := 0
		for rank, a := range order {
			weights[a.index] = l.count - rank
			total += weights[a.index]
		}
		// Give every lane the quiet-lane minimum first, then hand the spare
		// columns to the busiest lanes. Largest remainder with a lane-index
		// tie-break keeps the split deterministic and exact.
		base := min(bundleLane, l.unit)
		spare := max(0, usable-base*l.count)
		fractions := make([]struct{ index, rest int }, l.count)
		allocated := 0
		for i, w := range weights {
			quota := spare * w / total
			l.widths = append(l.widths, base+quota)
			allocated += quota
			fractions[i] = struct{ index, rest int }{i, spare*w - quota*total}
		}
		sort.SliceStable(fractions, func(i, j int) bool {
			return fractions[i].rest > fractions[j].rest
		})
		for i := 0; i < spare-allocated; i++ {
			l.widths[fractions[i].index]++
		}
	}
	l.offsets = make([]int, l.count)
	x := laneStartX
	for i, w := range l.widths {
		l.offsets[i] = x
		x += w
	}
	return l
}

func (l laneLayout) visible(lane int) bool {
	return lane >= l.start && lane < l.start+l.count
}

func (l laneLayout) xpos(lane int) int {
	if lane < l.start {
		return laneStartX + (lane-l.start)*l.unit
	}
	if lane >= l.start+l.count {
		return l.right() + (lane-l.start-l.count)*l.unit
	}
	return l.offsets[lane-l.start]
}

// right is the first column past the viewport, used when a base lane lies to
// the right of it. With weighted widths this is the true end of the last lane,
// which uniform arithmetic cannot give.
func (l laneLayout) right() int {
	if len(l.offsets) == 0 {
		return laneStartX
	}
	return l.offsets[len(l.offsets)-1] + l.widths[len(l.widths)-1]
}

// widthOf is a lane's column count, for label truncation and job strips.
func (l laneLayout) widthOf(lane int) int {
	if l.visible(lane) {
		return l.widths[lane-l.start]
	}
	return l.unit
}

// row places a mark by its recorded global rank. An entering mark's Drip
// lifts it into the gap above the commit region; clamping there keeps its
// fall, and every parent path following it, clear of the lane names. The
// offset belongs to the mark, so it also applies when Total is one.
func (l laneLayout) row(m Mark) int {
	y := l.top
	if m.Total > 1 {
		y += m.Rank * (l.bottom - l.top) / (m.Total - 1)
	}
	return max(l.top-1, y-m.Drip)
}

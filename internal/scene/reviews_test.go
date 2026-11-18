package scene_test

import (
	"strings"
	"testing"
	"time"

	"github.com/brew0x/percolate/internal/repo"
	"github.com/brew0x/percolate/internal/scene"
)

// The snapshot's review order is the recorded history: the last recorded
// review per author decides that reviewer's say. Commented, dismissed and
// pending latest reviews never decide the state, and reviewers who decided
// both ways leave the flow at changes requested.
func TestReviewStateResolution(t *testing.T) {
	cases := []struct {
		name    string
		reviews []repo.Review
		want    scene.ReviewState
	}{
		{"no recorded review", nil, scene.ReviewWaiting},
		{"commented only", []repo.Review{{Author: "sage", State: "commented"}}, scene.ReviewWaiting},
		{"dismissed latest", []repo.Review{{Author: "sage", State: "approved"}, {Author: "sage", State: "dismissed"}}, scene.ReviewWaiting},
		{"pending latest", []repo.Review{{Author: "sage", State: "pending"}}, scene.ReviewWaiting},
		{"approved", []repo.Review{{Author: "sage", State: "approved"}}, scene.ReviewApproved},
		{"changes requested", []repo.Review{{Author: "sage", State: "changes_requested"}}, scene.ReviewChanges},
		{"reviewer redecided to approve", []repo.Review{{Author: "sage", State: "changes_requested"}, {Author: "sage", State: "approved"}}, scene.ReviewApproved},
		{"reviewer redecided to request changes", []repo.Review{{Author: "sage", State: "approved"}, {Author: "sage", State: "changes_requested"}}, scene.ReviewChanges},
		{"later reviewer requested changes", []repo.Review{{Author: "sage", State: "approved"}, {Author: "pat", State: "changes_requested"}}, scene.ReviewChanges},
		{"earlier request stays later", []repo.Review{{Author: "pat", State: "changes_requested"}, {Author: "sage", State: "approved"}}, scene.ReviewChanges},
		// A null author is a deleted reviewer: there is no reviewer to hold a
		// say, so it never decides. Two of them must not share one slot where
		// the later approval would hide an earlier request.
		{"deleted reviewer approves", []repo.Review{{Author: "", State: "approved"}}, scene.ReviewWaiting},
		{"deleted reviewer requests changes", []repo.Review{{Author: "", State: "changes_requested"}}, scene.ReviewWaiting},
		{"two deleted reviewers redecide", []repo.Review{{Author: "", State: "changes_requested"}, {Author: "", State: "approved"}}, scene.ReviewWaiting},
		{"named reviewer still decides", []repo.Review{{Author: "", State: "changes_requested"}, {Author: "sage", State: "approved"}}, scene.ReviewApproved},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scene.ReviewStateOf(tc.reviews); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func reviewSnapshot() repo.Snapshot {
	// Four lanes fit the 60-column frame: main plus three heads, one pull
	// request per review state, all converging on main.
	mk := func(number int, state string) repo.PullRequest {
		p := repo.PullRequest{Number: number, Head: "w" + string(rune('0'+number)), Base: "main", State: "open"}
		if state != "" {
			p.Reviews = []repo.Review{{Author: "sage", State: state}}
		}
		return p
	}
	return repo.Snapshot{
		Branches: []repo.Branch{{Name: "main"}, {Name: "w1"}, {Name: "w2"}, {Name: "w3"}},
		PullRequests: []repo.PullRequest{
			mk(1, "approved"),
			mk(2, ""),
			mk(3, "changes_requested"),
		},
	}
}

// Each pull request's convergence point, where its flow meets the base lane,
// carries a distinct mark for its review state: solid arrow approved, open
// arrow waiting, crossed-out arrow changes requested.
func TestReviewMarksOnTheFlow(t *testing.T) {
	g := scene.Frame(scene.Build(reviewSnapshot()), 0, 60, 20)
	baseX := 3 // the main lane column at this width
	rows := map[string]int{"pr:1": 13, "pr:2": 14, "pr:3": 15}
	wantGlyph := map[string]rune{"pr:1": '▼', "pr:2": '▽', "pr:3": '⊗'}
	wantStyle := map[string]scene.Style{"pr:1": scene.Pass, "pr:2": scene.Fresh, "pr:3": scene.Fail}
	for id, y := range rows {
		if got := g[y][baseX]; got.Rune != wantGlyph[id] || got.Style != wantStyle[id] {
			t.Fatalf("%s convergence mark wrong: %q", id, got)
		}
	}
	// The diamond and the flow keep the pull request's own style; only the
	// convergence mark speaks for the reviews.
	if got := g[13][17]; got.Rune != '◇' || got.Style != scene.Fresh {
		t.Fatalf("approved flow restyled: %q", got)
	}
}

// Compressed rows can make several pull requests converge on the same base
// cell. When they resolve to different review states, the shared cell must not
// show one flow's mark at the end of another's still-visible path: both flows
// remain drawn, but neither is credited with the other's state.
func TestSharedConvergenceDoesNotMisattribute(t *testing.T) {
	mk := func(number int, head, state string) repo.PullRequest {
		p := repo.PullRequest{Number: number, Head: head, Base: "main", State: "open"}
		if state != "" {
			p.Reviews = []repo.Review{{Author: "sage", State: state}}
		}
		return p
	}
	s := repo.Snapshot{
		Branches: []repo.Branch{{Name: "main"}, {Name: "w1"}, {Name: "w2"}, {Name: "w3"}},
		// At height 20 ranks 0 and 4 share the compressed row 13. PR #1
		// (approved, head w2) and PR #5 (waiting, head w1) both converge on
		// main at the same cell. The waiting PR is drawn later and its shorter
		// path leaves the approved PR's far diamond visible: the compressed
		// row shape the safeguard has to survive.
		PullRequests: []repo.PullRequest{
			mk(1, "w2", "approved"),
			mk(2, "w1", ""),
			mk(3, "w2", ""),
			mk(4, "w3", ""),
			mk(5, "w1", ""),
		},
	}
	g := scene.Frame(scene.Build(s), 0, 60, 20)
	// Both flows are still visible: the diamonds at their head lanes survive.
	if got := g[13][31]; got.Rune != '◇' {
		t.Fatalf("approved flow diamond lost: %q", got)
	}
	if got := g[13][17]; got.Rune != '◇' {
		t.Fatalf("waiting flow diamond lost: %q", got)
	}
	// The shared base cell carries neither review mark, only the path run.
	if got := g[13][3]; got.Rune != '─' {
		t.Fatalf("shared convergence misattributed a review state: %q", got)
	}
	// Selection is no excuse to claim the shared cell: the selected waiting
	// PR draws last, but the cell still belongs to no single flow. Its own
	// diamond keeps the selected style so it stays inspectable.
	built := scene.Build(s)
	built.SelectedID = "pr:5"
	g = scene.Frame(built, 0, 60, 20)
	if got := g[13][3]; got.Rune != '─' {
		t.Fatalf("selected PR claimed the shared convergence: %q\n%s", got, g)
	}
	if got := g[13][17]; got.Rune != '◇' || got.Style != scene.Selected {
		t.Fatalf("selected waiting PR lost its diamond: %q\n%s", got, g)
	}
}

// Flows that agree on state at a shared convergence keep the mark: it is true
// for every flow ending there, so there is no attribution to resolve.
func TestAgreedConvergenceKeepsMark(t *testing.T) {
	mk := func(number int, head, state string) repo.PullRequest {
		p := repo.PullRequest{Number: number, Head: head, Base: "main", State: "open"}
		if state != "" {
			p.Reviews = []repo.Review{{Author: "sage", State: state}}
		}
		return p
	}
	s := repo.Snapshot{
		Branches: []repo.Branch{{Name: "main"}, {Name: "w1"}, {Name: "w2"}, {Name: "w3"}},
		PullRequests: []repo.PullRequest{
			mk(1, "w1", "approved"),
			mk(2, "w1", ""),
			mk(3, "w2", ""),
			mk(4, "w3", ""),
			mk(5, "w2", "approved"), // agrees with PR #1 at row 13
		},
	}
	g := scene.Frame(scene.Build(s), 0, 60, 20)
	if got := g[13][3]; got.Rune != '▼' || got.Style != scene.Pass {
		t.Fatalf("agreed convergence lost its approved mark: %q", got)
	}
}

// A selected pull request keeps the selected style on its whole flow,
// including the convergence mark.
func TestSelectedReviewMarkStaysSelected(t *testing.T) {
	built := scene.Build(reviewSnapshot())
	built.SelectedID = "pr:3"
	g := scene.Frame(built, 0, 60, 20)
	if got := g[15][3]; got.Rune != '⊗' || got.Style != scene.Selected {
		t.Fatalf("selected convergence mark not inspectable: %q", got)
	}
}

// A base lane beyond the viewport keeps the plain direction indicator; the
// review mark only replaces the convergence arrow the frame can draw.
func TestOffscreenBaseKeepsIndicator(t *testing.T) {
	s := reviewSnapshot()
	// The head stays visible on main while the recorded base lane sits
	// beyond the right edge of the viewport.
	s.Branches = append(s.Branches, repo.Branch{Name: "w4"})
	s.PullRequests = append(s.PullRequests, repo.PullRequest{Number: 4, Head: "main", Base: "w4", State: "open", Reviews: []repo.Review{{Author: "sage", State: "approved"}}})
	g := scene.Frame(scene.Build(s), 0, 60, 20)
	if got := g[16][59]; got.Rune != '›' {
		t.Fatalf("offscreen base indicator missing:\n%s", g.String())
	}
	// Scrolling one lane right leaves the base to the left of the viewport.
	built := scene.Build(s)
	built.FirstLane = 1
	g = scene.Frame(built, 0, 60, 20)
	found := false
	for y := range g {
		if got := g[y][0]; got.Rune == '‹' {
			found = true
		}
	}
	if !found {
		t.Fatalf("offscreen left indicator missing:\n%s", g.String())
	}
}

// The resolved review state is named in the details, next to the recorded
// reviews, so a selected pull request reports what its mark stands for.
func TestReviewStateInDetails(t *testing.T) {
	built := scene.Build(reviewSnapshot())
	byID := map[string]string{}
	for _, m := range built.Marks {
		if m.Kind == "pr" {
			byID[m.ID] = m.Detail
		}
	}
	if want := "reviews: approved"; !strings.Contains(byID["pr:1"], want) {
		t.Fatalf("approved detail lost: %q", byID["pr:1"])
	}
	if want := "reviews: waiting"; !strings.Contains(byID["pr:2"], want) {
		t.Fatalf("waiting detail lost: %q", byID["pr:2"])
	}
	if want := "reviews: changes requested"; !strings.Contains(byID["pr:3"], want) {
		t.Fatalf("changes detail lost: %q", byID["pr:3"])
	}
	if !strings.Contains(byID["pr:1"], "sage: approved") {
		t.Fatalf("recorded reviews lost from details: %q", byID["pr:1"])
	}
}

// A pull request that is forming in or fading out during a change keeps the
// dim style on its convergence mark. The pass and fail palettes colour only a
// settled flow; a mid-transition mark must fade with the diamond and path it
// belongs to, never stay bright while the rest of the flow dims.
func TestTransitionReviewMarkFadesWithFlow(t *testing.T) {
	day := time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)
	base := repo.Snapshot{Now: day, Branches: []repo.Branch{{Name: "main"}, {Name: "topic"}}}
	approvedPR := func() repo.Snapshot {
		s := base
		s.PullRequests = []repo.PullRequest{{Number: 1, Head: "topic", Base: "main", State: "open", Reviews: []repo.Review{{Author: "sage", State: "approved"}}}}
		return s
	}
	changesPR := func() repo.Snapshot {
		s := base
		s.PullRequests = []repo.PullRequest{{Number: 2, Head: "topic", Base: "main", State: "open", Reviews: []repo.Review{{Author: "sage", State: "changes_requested"}}}}
		return s
	}
	cellWithRune := func(g scene.Grid, r rune) scene.Cell {
		t.Helper()
		for _, row := range g {
			for _, c := range row {
				if c.Rune == r {
					return c
				}
			}
		}
		t.Fatalf("rune %q not found in frame", r)
		return scene.Cell{}
	}
	assertFadedFlow := func(g scene.Grid, glyph rune, name string) {
		t.Helper()
		if got := cellWithRune(g, glyph); got.Style != scene.Faded {
			t.Fatalf("%s convergence mark did not fade: %q", name, got)
		}
		if got := cellWithRune(g, '◇'); got.Style != scene.Faded {
			t.Fatalf("%s diamond did not fade: %q", name, got)
		}
	}

	assertFadedFlow(scene.Frame(scene.Transition(approvedPR(), base, 16), 16, 60, 20), '▼', "departing approved")
	assertFadedFlow(scene.Frame(scene.Transition(base, approvedPR(), 0), 0, 60, 20), '▼', "entering approved")
	assertFadedFlow(scene.Frame(scene.Transition(changesPR(), base, 16), 16, 60, 20), '⊗', "departing changes requested")
}

// A pull request on its way out still draws its diamond and path throughout
// the fade, so its state counts at a shared convergence cell. A settled flow
// meeting that cell must not wear the departing flow's different state: at
// 60x7 a changes-requested PR from w2 and an approved PR from w1 both converge
// on main at the same cell while the first is fading.
func TestDepartingFlowDoesNotLendItsReviewState(t *testing.T) {
	day := time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)
	branches := []repo.Branch{{Name: "main"}, {Name: "w1"}, {Name: "w2"}}
	approved := repo.PullRequest{Number: 2, Head: "w1", Base: "main", State: "open", Reviews: []repo.Review{{Author: "sage", State: "approved"}}}
	prev := repo.Snapshot{Now: day, Branches: branches, PullRequests: []repo.PullRequest{
		{Number: 1, Head: "w2", Base: "main", State: "open", Reviews: []repo.Review{{Author: "sage", State: "changes_requested"}}},
		approved,
	}}
	next := repo.Snapshot{Now: day.Add(time.Minute), Branches: branches, PullRequests: []repo.PullRequest{approved}}
	for _, frame := range []int{0, 16, 31} {
		g := scene.Frame(scene.Transition(prev, next, frame), frame, 60, 7)
		// The shared base cell keeps the plain path run: the departing request
		// never turns the survivor green, and the survivor never hides it.
		if got := g[3][3]; got.Rune != '─' {
			t.Fatalf("frame %d: shared convergence credited a state: %q\n%s", frame, got, g)
		}
		// The departing flow is still visibly drawn at this instant, which is
		// what makes the shared cell a misattribution rather than a harmless
		// leftover.
		if got := g[3][39]; got.Rune != '◇' || got.Style != scene.Faded {
			t.Fatalf("frame %d: departing diamond not visible: %q\n%s", frame, got, g)
		}
	}
	// When the transition settles the departing flow is gone and the surviving
	// approval keeps its mark at that same cell.
	g := scene.Frame(scene.Transition(prev, next, scene.TransitionFrames), scene.TransitionFrames, 60, 7)
	if got := g[3][3]; got.Rune != '▼' || got.Style != scene.Pass {
		t.Fatalf("settled convergence lost its approved mark: %q\n%s", got, g)
	}
}

// A later flow's path crossing an earlier flow's convergence on a compressed
// row must not erase that earlier review mark, even when the two flows meet
// at different base lanes (so no shared cell suppresses the arrow). Here an
// approved w3 -> w1 PR at rank 0 and a w2 -> main PR at rank 4 share row 13;
// the latter's path writes over the former's mark at w1.
func TestCrossingPathDoesNotEraseReviewMark(t *testing.T) {
	mk := func(number int, head, base, state string) repo.PullRequest {
		p := repo.PullRequest{Number: number, Head: head, Base: base, State: "open"}
		if state != "" {
			p.Reviews = []repo.Review{{Author: "sage", State: state}}
		}
		return p
	}
	s := repo.Snapshot{
		Branches: []repo.Branch{{Name: "main"}, {Name: "w1"}, {Name: "w2"}, {Name: "w3"}},
		PullRequests: []repo.PullRequest{
			mk(1, "w3", "w1", "approved"), // rank 0, mark at w1 (column 17, row 13)
			mk(2, "w1", "main", ""),
			mk(3, "w2", "main", ""),
			mk(4, "w3", "main", ""),
			mk(5, "w2", "main", ""), // rank 4, path crosses column 17 on row 13
		},
	}
	g := scene.Frame(scene.Build(s), 0, 60, 20)
	if got := g[13][17]; got.Rune != '▼' || got.Style != scene.Pass {
		t.Fatalf("crossing path erased the approved mark: %q\n%s", got, g)
	}
	// Selecting the crossing PR must not change that: its path is laid down
	// before any mark, while its own mark still draws last in the selected
	// style and reports its waiting state.
	built := scene.Build(s)
	built.SelectedID = "pr:5"
	g = scene.Frame(built, 0, 60, 20)
	if got := g[13][17]; got.Rune != '▼' || got.Style != scene.Pass {
		t.Fatalf("selected crossing path erased the approved mark: %q\n%s", got, g)
	}
	if got := g[13][3]; got.Rune != '▽' || got.Style != scene.Selected {
		t.Fatalf("selected PR lost its own waiting mark: %q\n%s", got, g)
	}
}

// A departing review mark draws beneath a surviving flow's path. At 60x7 a
// changes-requested w3 -> w1 PR fades out while an approved w2 -> main PR
// survives; the survivor's path crosses w1, and the dim request must not be
// painted over the middle of that path, selected or not. The fading flow is
// still drawn where nothing survives over it.
func TestDepartingMarkDoesNotCrossSurvivingPath(t *testing.T) {
	day := time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)
	branches := []repo.Branch{{Name: "main"}, {Name: "w1"}, {Name: "w2"}, {Name: "w3"}}
	changes := repo.PullRequest{Number: 1, Head: "w3", Base: "w1", State: "open", Reviews: []repo.Review{{Author: "sage", State: "changes_requested"}}}
	approved := repo.PullRequest{Number: 2, Head: "w2", Base: "main", State: "open", Reviews: []repo.Review{{Author: "sage", State: "approved"}}}
	prev := repo.Snapshot{Now: day, Branches: branches, PullRequests: []repo.PullRequest{changes, approved}}
	next := repo.Snapshot{Now: day.Add(time.Minute), Branches: branches, PullRequests: []repo.PullRequest{approved}}

	for _, selected := range []bool{false, true} {
		for _, frame := range []int{0, 16, 31} {
			built := scene.Transition(prev, next, frame)
			if selected {
				built.SelectedID = "pr:2"
			}
			g := scene.Frame(built, frame, 60, 7)
			// Row 3 is shared by every flow at this compact height. The
			// survivor's path runs from main (col 3) to w2 (col 31), crossing
			// w1 (col 17) where the departing request's arrow used to land.
			wantStyle := scene.Fresh
			if selected {
				wantStyle = scene.Selected
			}
			if got := g[3][17]; got.Rune != '─' || got.Style != wantStyle {
				t.Fatalf("selected=%v frame %d: departing mark crossed the surviving path: %q\n%s", selected, frame, got, g)
			}
			if got := g[3][45]; got.Rune != '◇' || got.Style != scene.Faded {
				t.Fatalf("selected=%v frame %d: departing diamond lost: %q\n%s", selected, frame, got, g)
			}
		}
	}
}

// A review mark converges where a flow meets its base lane. When that base is
// another pull request's head lane, the mark lands on that PR's diamond at a
// compact height: a later changes-requested w2 -> w1 flow must not paint its
// red circle over the earlier approved w1 -> main flow's diamond, whichever PR
// is selected. The diamond names its PR, so it stays; the converging PR keeps
// its state in its details instead.
func TestReviewMarkDoesNotReplaceHeadDiamond(t *testing.T) {
	s := repo.Snapshot{
		Branches: []repo.Branch{{Name: "main"}, {Name: "w1"}, {Name: "w2"}},
		PullRequests: []repo.PullRequest{
			{Number: 1, Head: "w1", Base: "main", State: "open", Reviews: []repo.Review{{Author: "sage", State: "approved"}}},
			{Number: 2, Head: "w2", Base: "w1", State: "open", Reviews: []repo.Review{{Author: "sage", State: "changes_requested"}}},
		},
	}
	// At 60x7 every flow draws on row 3; three lanes put main at column 3, w1
	// at 21 and w2 at 39. PR #1's diamond and PR #2's convergence share (3,21).
	for _, selected := range []string{"", "pr:1", "pr:2"} {
		built := scene.Build(s)
		built.SelectedID = selected
		g := scene.Frame(built, 0, 60, 7)

		diamondStyle := scene.Fresh
		approvedStyle := scene.Pass
		if selected == "pr:1" {
			diamondStyle, approvedStyle = scene.Selected, scene.Selected
		}
		if got := g[3][21]; got.Rune != '◇' || got.Style != diamondStyle {
			t.Fatalf("selected=%q: approved head diamond replaced by a review mark: %q\n%s", selected, got, g)
		}
		// The approved flow still converges on main with its approved mark.
		if got := g[3][3]; got.Rune != '▼' || got.Style != approvedStyle {
			t.Fatalf("selected=%q: approved convergence lost: %q\n%s", selected, got, g)
		}
		// The changes-requested flow keeps its own diamond at w2; its state is
		// reported in its details, not painted over the diamond it collides with.
		changesDiamondStyle := scene.Fresh
		if selected == "pr:2" {
			changesDiamondStyle = scene.Selected
		}
		if got := g[3][39]; got.Rune != '◇' || got.Style != changesDiamondStyle {
			t.Fatalf("selected=%q: changes-requested diamond lost: %q\n%s", selected, got, g)
		}
	}
}

// A departing head diamond suppresses a surviving flow's review mark only while
// the diamond is still visible. Once a surviving path paints over that cell,
// the cell belongs to the surviving flow and its convergence mark must return;
// the mark must not stay absent until the fade ends.
func TestDepartingHeadDiamondDoesNotSuppressCoveredReviewMark(t *testing.T) {
	day := time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)
	branches := []repo.Branch{{Name: "main"}, {Name: "w1"}, {Name: "w2"}}
	departing := repo.PullRequest{Number: 1, Head: "w1", Base: "main", State: "open"}
	surviving := repo.PullRequest{Number: 2, Head: "w2", Base: "w1", State: "open", Reviews: []repo.Review{{Author: "sage", State: "approved"}}}
	prev := repo.Snapshot{Now: day, Branches: branches, PullRequests: []repo.PullRequest{departing, surviving}}
	next := repo.Snapshot{Now: day.Add(time.Minute), Branches: branches, PullRequests: []repo.PullRequest{surviving}}
	for _, frame := range []int{0, 16, 31} {
		g := scene.Frame(scene.Transition(prev, next, frame), frame, 60, 7)
		// At 60x7 the flows share row 3 (main 3, w1 21, w2 39). The surviving
		// path runs w2 -> w1 over w1's column, so its approved convergence mark
		// there must be visible even while the departed diamond is still fading.
		if got := g[3][21]; got.Rune != '▼' || got.Style != scene.Pass {
			t.Fatalf("frame %d: surviving review mark suppressed: %q\n%s", frame, got, g)
		}
		if got := g[3][39]; got.Rune != '◇' || got.Style != scene.Fresh {
			t.Fatalf("frame %d: surviving diamond lost: %q\n%s", frame, got, g)
		}
	}
	g := scene.Frame(scene.Transition(prev, next, scene.TransitionFrames), scene.TransitionFrames, 60, 7)
	if got := g[3][21]; got.Rune != '▼' || got.Style != scene.Pass {
		t.Fatalf("settled convergence lost its approved mark: %q\n%s", got, g)
	}
}

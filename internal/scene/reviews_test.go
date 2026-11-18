package scene_test

import (
	"strings"
	"testing"

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
		{"deleted reviewer", []repo.Review{{Author: "", State: "approved"}}, scene.ReviewApproved},
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

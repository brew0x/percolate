package scene_test

import (
	"strings"
	"testing"
	"time"

	"github.com/brew0x/percolate/internal/repo"
	"github.com/brew0x/percolate/internal/scene"
)

// A closing reference is recorded on the issue (ClosedBy) and resolves only
// to a pull request that is actually in the snapshot. Nothing is inferred from
// issue text, and closed issues draw no spring.
func TestIssueLinksComeFromSnapshot(t *testing.T) {
	s := repo.Snapshot{
		Branches: []repo.Branch{{Name: "main"}},
		PullRequests: []repo.PullRequest{
			{Number: 12, Head: "topic", Base: "main", State: "open"},
		},
		Issues: []repo.Issue{
			{Number: 5, Title: "leak", State: "open", ClosedBy: 12},
			{Number: 7, Title: "polish", State: "open"},
			{Number: 9, Title: "stale", State: "closed", ClosedBy: 12},
			{Number: 11, Title: "ghost", State: "open", ClosedBy: 99},
		},
	}
	built := scene.Build(s)
	var issues []scene.Mark
	for _, m := range built.Marks {
		if m.Kind == "issue" {
			issues = append(issues, m)
		}
	}
	if len(issues) != 3 {
		t.Fatalf("closed issue drew a spring or open issue was lost: %d marks", len(issues))
	}
	byID := make(map[string]scene.Mark, len(issues))
	for _, m := range issues {
		byID[m.ID] = m
	}
	if !strings.Contains(byID["issue:5"].Detail, "closes in #12") {
		t.Fatalf("recorded closing reference lost: %q", byID["issue:5"].Detail)
	}
	if byID["issue:5"].Style != scene.Fresh || byID["issue:7"].Style != scene.Faded {
		t.Fatal("linked and unlinked springs must look different")
	}
	for _, id := range []string{"issue:7", "issue:11"} {
		if !strings.Contains(byID[id].Detail, "no recorded closing reference") {
			t.Fatalf("absent or dangling closing reference presented as a link: %q", byID[id].Detail)
		}
	}
	if _, ok := byID["issue:9"]; ok {
		t.Fatal("closed issue produced a spring")
	}
}

// The linked spring sits above the PR's head lane and feeds down it; the
// unlinked spring sits in the left margin without a feed. A commit in the feed
// column wins the shared cell, so the feed never overwrites recorded data.
func TestIssueSpringsAndFeed(t *testing.T) {
	s := repo.Snapshot{
		Branches: []repo.Branch{{Name: "main"}},
		Commits:  []repo.Commit{{SHA: "abc", Branch: "topic", Author: "robin"}},
		PullRequests: []repo.PullRequest{
			{Number: 12, Head: "topic", Base: "main", State: "open"},
		},
		Issues: []repo.Issue{
			{Number: 5, Title: "leak", State: "open", ClosedBy: 12},
			{Number: 7, Title: "polish", State: "open"},
		},
	}
	g := scene.Frame(scene.Build(s), 0, 60, 20)
	if got := g[3][31]; got.Rune != '∿' || got.Style != scene.Fresh {
		t.Fatalf("linked spring missing at head lane: %q", got)
	}
	if g[3][32].Rune != '#' || g[3][33].Rune != '5' {
		t.Fatal("issue number label missing on the spring")
	}
	if got := g[4][0]; got.Rune != '∿' || got.Style != scene.Faded {
		t.Fatalf("unlinked spring missing from the margin: %q", got)
	}
	if g[4][31].Rune != '●' {
		t.Fatal("feed overwrote a commit in its column")
	}
	feeds := 0
	for y := 5; y < 13; y++ {
		if g[y][31].Rune == '⋮' {
			feeds++
		}
	}
	if feeds == 0 || g[13][31].Rune != '◇' {
		t.Fatalf("closing reference does not feed the PR flow:\n%s", g.String())
	}
	for y := 0; y < 20; y++ {
		if g[y][0].Rune == '⋮' {
			t.Fatal("unlinked issue drew a feed")
		}
	}
}

// Selecting an unlinked issue still reveals its spring in the margin.
func TestSelectedIssueDrawsLast(t *testing.T) {
	built := scene.Build(repo.Snapshot{
		Branches: []repo.Branch{{Name: "main"}},
		Issues:   []repo.Issue{{Number: 7, Title: "polish", State: "open"}},
	})
	built.SelectedID = "issue:7"
	g := scene.Frame(built, 0, 60, 20)
	if got := g[3][0]; got.Rune != '∿' || got.Style != scene.Selected {
		t.Fatalf("selected issue not inspectable: %q", got)
	}
}

func springX(t *testing.T, row []scene.Cell) int {
	t.Helper()
	for x, c := range row {
		if c.Rune == '∿' {
			return x
		}
	}
	t.Fatal("no spring in row")
	return 0
}

// A pull request that disappears must not answer the rank lookup for the
// surviving pull request that moved into its rank. In this pair pr 1 (old rank
// 0) departs while pr 2 survives and holds rank 0; issue 5 is linked to pr 2.
// The survivor's feed runs down its own lane and stops above pr 2's diamond,
// and nothing feeds the departed PR on its retained lane.
func TestIssueFeedFollowsSurvivingPR(t *testing.T) {
	prev, next := fixture(t, "feed"), fixture(t, "feed-next")
	var g scene.Grid
	feeds := 0
	for _, frame := range []int{0, 8, 16, 24, 31} {
		g = scene.Frame(scene.Transition(prev, next, frame), frame, 100, 30)
		if got := g[3][35]; got.Rune != '∿' || got.Style != scene.Fresh {
			t.Fatalf("frame %d: survivor spring missing: %q\n%s", frame, got, g)
		}
		if g[3][36].Rune != '#' || g[3][37].Rune != '5' {
			t.Fatalf("frame %d: survivor spring label wrong:\n%s", frame, g)
		}
		feeds = 0
		for y := 4; y < 19; y++ {
			if g[y][35].Rune == '⋮' {
				feeds++
			}
		}
		if feeds == 0 || g[19][35].Rune != '◇' {
			t.Fatalf("frame %d: survivor feed does not reach pr 2:\n%s", frame, g)
		}
		if g[19][67].Rune != '◇' {
			t.Fatalf("frame %d: departed PR not fading on its retained lane:\n%s", frame, g)
		}
		for y := 0; y < 30; y++ {
			if g[y][67].Rune == '⋮' {
				t.Fatalf("frame %d: a feed points at the departed PR:\n%s", frame, g)
			}
		}
	}
	settled := scene.Transition(prev, next, scene.TransitionFrames)
	if len(settled.Lanes) != 2 {
		t.Fatalf("removed lane lingered: %v", settled.Lanes)
	}
	for _, m := range settled.Marks {
		if m.Style == scene.Faded {
			t.Fatalf("fading mark %s lingered after the transition", m.ID)
		}
	}
	g = scene.Frame(settled, 32, 100, 30)
	x := springX(t, g[3])
	feeds = 0
	for y := 4; y < 19; y++ {
		if g[y][x].Rune == '⋮' {
			feeds++
		}
	}
	if feeds == 0 || g[19][x].Rune != '◇' {
		t.Fatalf("survivor lost its feed after settling:\n%s", g)
	}
}

// A fading issue whose recorded closing reference names a pull request that
// stays must keep its feed for the whole fade. The link is recorded state:
// removing only the issue from the new snapshot must not silence its spring.
func TestDepartingIssueKeepsFeedToSurvivingPR(t *testing.T) {
	day := time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)
	prev := repo.Snapshot{
		Now:          day,
		Branches:     []repo.Branch{{Name: "main"}, {Name: "topic"}},
		PullRequests: []repo.PullRequest{{Number: 12, Head: "topic", Base: "main", State: "open"}},
		Issues:       []repo.Issue{{Number: 5, Title: "leak", State: "open", ClosedBy: 12}},
	}
	next := prev
	next.Now = day.Add(time.Minute)
	next.Issues = nil

	const width, height = 100, 30
	topicX := 3 + (width-4)/2 // two even lanes at this width
	for frame := 0; frame < scene.TransitionFrames; frame++ {
		g := scene.Frame(scene.Transition(prev, next, frame), frame, width, height)
		if got := g[3][topicX]; got.Rune != '∿' || got.Style != scene.Faded {
			t.Fatalf("frame %d: fading spring missing: %q\n%s", frame, got, g)
		}
		if g[3][topicX+1].Rune != '#' || g[3][topicX+2].Rune != '5' {
			t.Fatalf("frame %d: fading spring label wrong:\n%s", frame, g)
		}
		feeds := 0
		for y := 4; y < 19; y++ {
			if g[y][topicX].Rune == '⋮' {
				feeds++
			}
		}
		if feeds != 15 || g[19][topicX].Rune != '◇' {
			t.Fatalf("frame %d: feed does not reach the surviving PR (%d cells):\n%s", frame, feeds, g)
		}
	}
	settled := scene.Frame(scene.Transition(prev, next, scene.TransitionFrames), scene.TransitionFrames, width, height)
	if settled[19][topicX].Rune != '◇' {
		t.Fatalf("settled frame lost the surviving PR:\n%s", settled)
	}
	for y := 0; y < height; y++ {
		if r := settled[y][topicX].Rune; r == '⋮' || r == '∿' {
			t.Fatalf("fade left a spring or feed behind:\n%s", settled)
		}
	}
}

// When a departed PR's rank is reused by a survivor, each issue still follows
// the PR that held that rank in its own snapshot: the fading issue keeps its
// former PR, and the surviving issue keeps the one that stayed.
func TestFeedKeepsEachIssueAtACollidedRank(t *testing.T) {
	day := time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)
	prev := repo.Snapshot{
		Now:      day,
		Branches: []repo.Branch{{Name: "main"}, {Name: "topic"}, {Name: "base"}},
		PullRequests: []repo.PullRequest{
			{Number: 11, Head: "base", Base: "main", State: "open"},
			{Number: 12, Head: "topic", Base: "main", State: "open"},
		},
		Issues: []repo.Issue{
			{Number: 5, Title: "leaking", State: "open", ClosedBy: 11},
			{Number: 6, Title: "live", State: "open", ClosedBy: 12},
		},
	}
	next := prev
	next.Now = day.Add(time.Minute)
	next.Branches = []repo.Branch{{Name: "main"}, {Name: "topic"}}
	next.PullRequests = prev.PullRequests[1:]
	next.Issues = prev.Issues[1:]

	g := scene.Frame(scene.Transition(prev, next, 8), 8, 100, 30)
	for _, tc := range []struct {
		name, label string
		x           int
	}{
		{"fading issue, departed PR's former rank", "#5", 67}, // retained base lane
		{"surviving issue, reused rank", "#6", 35},            // topic lane
	} {
		feeds := 0
		for y := 4; y < 19; y++ {
			if g[y][tc.x].Rune == '⋮' {
				feeds++
			}
		}
		if feeds != 15 || g[19][tc.x].Rune != '◇' {
			t.Fatalf("%s feed does not reach its own PR:\n%s", tc.name, g)
		}
		if g[3][tc.x].Rune != '∿' || g[3][tc.x+1].Rune != '#' ||
			g[3][tc.x+2].Rune != rune(tc.label[1]) {
			t.Fatalf("%s spring label wrong:\n%s", tc.name, g)
		}
	}
}

// The linked spring's shelf row is also the row a new newest commit drips
// through. The issue layer draws last, but it may only fill cells that are
// empty or its own: the falling bead, the CI pulse beside it and the lane
// names survive every frame, and the spring reappears whole once the bead
// lands.
func TestIssueLayerKeepsEnteringCommitAndPulse(t *testing.T) {
	day := time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)
	prev := repo.Snapshot{
		Now:          day,
		Branches:     []repo.Branch{{Name: "main"}, {Name: "topic"}},
		Commits:      []repo.Commit{{SHA: "root", Branch: "main", Author: "robin", Time: day.Add(-time.Hour)}},
		PullRequests: []repo.PullRequest{{Number: 12, Head: "topic", Base: "main", State: "open"}},
		Issues:       []repo.Issue{{Number: 5, Title: "leak", State: "open", ClosedBy: 12}},
	}
	next := prev
	next.Now = day.Add(time.Minute)
	next.Commits = append([]repo.Commit{
		{SHA: "new", Branch: "topic", Author: "robin", Time: day, Parents: []string{"root"}},
	}, prev.Commits...)
	next.Runs = []repo.WorkflowRun{{ID: 3, HeadSHA: "new", Status: "in_progress"}}

	x := 3 + (60-4)/2 // the topic lane column at this width
	for _, frame := range []int{0, 4, 8, 12, 15} {
		g := scene.Frame(scene.Transition(prev, next, frame), frame, 60, 20)
		if got := g[3][x]; got.Rune != '●' || got.Style != scene.Faded {
			t.Fatalf("frame %d: entering bead not intact on the shelf row: %q\n%s", frame, got, g)
		}
		if got := g[3][x+2]; !strings.ContainsRune("◌◔◑◕", got.Rune) {
			t.Fatalf("frame %d: CI pulse erased by the issue layer: %q\n%s", frame, got, g)
		}
		if g[3][x+1].Rune != ' ' {
			t.Fatalf("frame %d: spring label left beside an occluded spring:\n%s", frame, g)
		}
		if got := rowText(g[2]); !strings.Contains(got, "topic") {
			t.Fatalf("frame %d: lane name corrupted: %q\n%s", frame, got, g)
		}
	}
	landed := scene.Frame(scene.Transition(prev, next, 16), 16, 60, 20)
	if landed[3][x].Rune != '∿' || landed[3][x+1].Rune != '#' || landed[3][x+2].Rune != '5' {
		t.Fatalf("linked spring did not reappear after the bead landed:\n%s", landed)
	}
	if landed[4][x].Rune != '●' {
		t.Fatalf("newest commit not landed in the top row:\n%s", landed)
	}
}

// At height 8 the shelf meets the PR's number-label row instead of its
// diamond row. The label must survive just as the diamond does at height 7.
func TestLinkedSpringKeepsPRLabelInCompactFrame(t *testing.T) {
	s := repo.Snapshot{
		Branches:     []repo.Branch{{Name: "main"}},
		PullRequests: []repo.PullRequest{{Number: 12, Head: "topic", Base: "main", State: "open"}},
		Issues:       []repo.Issue{{Number: 5, Title: "leak", State: "open", ClosedBy: 12}},
	}
	g := scene.Frame(scene.Build(s), 0, 60, 8)
	prX := -1
	for x, c := range g[4] {
		if c.Rune == '◇' {
			prX = x
			break
		}
	}
	if prX < 0 {
		t.Fatalf("PR diamond missing:\n%s", g.String())
	}
	if g[3][prX].Rune != ' ' || g[3][prX+1].Rune != '#' || g[3][prX+2].Rune != '1' || g[3][prX+3].Rune != '2' {
		t.Fatalf("PR number label erased by spring:\n%s", g.String())
	}
	if got := g[1][prX]; got.Rune != '∿' {
		t.Fatalf("linked spring not visible above compact PR: %q\n%s", got, g.String())
	}
}

// Several open issues may record closing references to the same pull request
// and share its head lane. A shorter label drawn over a longer one must clear
// the cells it replaces: #5 over #100 may never read as the phantom #500,
// whether the shorter issue is selected or not.
func TestOverlappingSpringLabels(t *testing.T) {
	s := repo.Snapshot{
		Branches:     []repo.Branch{{Name: "main"}},
		PullRequests: []repo.PullRequest{{Number: 12, Head: "topic", Base: "main", State: "open"}},
		Issues: []repo.Issue{
			{Number: 100, Title: "long", State: "open", ClosedBy: 12},
			{Number: 5, Title: "short", State: "open", ClosedBy: 12},
		},
	}
	cases := []struct {
		name, selected, want string
		style                scene.Style
	}{
		{"snapshot order", "", "#5", scene.Fresh},
		{"shorter selected", "issue:5", "#5", scene.Selected},
		{"longer selected", "issue:100", "#100", scene.Selected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			built := scene.Build(s)
			built.SelectedID = tc.selected
			g := scene.Frame(built, 0, 60, 20)
			x := springX(t, g[3])
			if got := g[3][x]; got.Rune != '∿' || got.Style != tc.style {
				t.Fatalf("spring at head lane wrong: %q", got)
			}
			for i, r := range []rune(tc.want) {
				if got := g[3][x+1+i]; got.Rune != r || got.Style != tc.style {
					t.Fatalf("spring label %q cell %d = %q", tc.want, i, got)
				}
			}
			// span-2 = 26 label cells at this size; every cell past the label
			// must be blank, with no tail left by the longer number.
			for xx := x + 1 + len(tc.want); xx <= x+26; xx++ {
				if got := g[3][xx]; got.Rune != ' ' {
					t.Fatalf("fabricated digit %q after %q", got.Rune, tc.want)
				}
			}
		})
	}
}

// A supported compact frame (height 7) puts the shelf on the PR row. The
// spring must lift clear of the diamond, including a selected one, instead of
// overwriting it; it still has to be visible and numbered.
func TestLinkedSpringDoesNotErasePRInCompactFrame(t *testing.T) {
	s := repo.Snapshot{
		Branches:     []repo.Branch{{Name: "main"}},
		PullRequests: []repo.PullRequest{{Number: 12, Head: "topic", Base: "main", State: "open"}},
		Issues:       []repo.Issue{{Number: 5, Title: "leak", State: "open", ClosedBy: 12}},
	}
	cases := []struct {
		name, selected  string
		prStyle, spring scene.Style
	}{
		{"plain", "", scene.Fresh, scene.Fresh},
		{"selected PR", "pr:12", scene.Selected, scene.Fresh},
		{"selected issue", "issue:5", scene.Fresh, scene.Selected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			built := scene.Build(s)
			built.SelectedID = tc.selected
			g := scene.Frame(built, 0, 60, 7)
			prX := -1
			for x, c := range g[3] {
				if c.Rune == '◇' {
					prX = x
					break
				}
			}
			if prX < 0 {
				t.Fatalf("PR diamond missing:\n%s", g.String())
			}
			if got := g[3][prX]; got.Style != tc.prStyle {
				t.Fatalf("PR diamond restyled by spring: %q\n%s", got, g.String())
			}
			if got := g[1][prX]; got.Rune != '∿' || got.Style != tc.spring {
				t.Fatalf("linked spring not visible above compact PR: %q\n%s", got, g.String())
			}
			if g[1][prX+1].Rune != '#' || g[1][prX+2].Rune != '5' {
				t.Fatalf("compact spring label wrong:\n%s", g.String())
			}
		})
	}
}

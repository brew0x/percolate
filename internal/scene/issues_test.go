package scene_test

import (
	"strings"
	"testing"

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

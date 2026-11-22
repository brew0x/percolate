package github

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/brew0x/percolate/internal/repo"
)

// These guards back the opt-in live capture probe in live_capture_test.go,
// which is excluded from make check because it needs a token. Keeping them in
// an untagged file lets CI verify both safety rails even though the probe
// itself can only run in an environment that has a GitHub token.
//
// captureRepoPublic refuses to record a repository the token can read but that
// GitHub does not serve publicly. captureGraphQLProof counts the fields
// fetchPullDetails fills from a live GraphQL response: a recording proves the
// review and head-commit selections only when both are present, because
// fetchPullDetails skips null PR aliases and head targets with no commit anchor
// instead of failing the read.
func captureRepoPublic(defaultBranch string, private bool, visibility string) error {
	if defaultBranch == "" {
		return errors.New("GitHub repository metadata had no default branch")
	}
	if private || visibility != "public" {
		return fmt.Errorf("not a public repository (visibility %q, private %t)", visibility, private)
	}
	return nil
}

func captureGraphQLProof(snap *repo.Snapshot, defaultBranch string) (reviews, anchors int) {
	lanes := map[string]bool{}
	for _, p := range snap.PullRequests {
		lanes[p.Head] = true
		for _, r := range p.Reviews {
			if r.Author != "" && r.State != "" {
				reviews++
			}
		}
	}
	for _, cm := range snap.Commits {
		if lanes[cm.Branch] && cm.Branch != defaultBranch && cm.SHA != "" &&
			cm.Author != "" && !cm.Time.IsZero() && len(cm.Parents) > 0 {
			anchors++
		}
	}
	return reviews, anchors
}

func TestCaptureRepoPublic(t *testing.T) {
	cases := []struct {
		name       string
		branch     string
		private    bool
		visibility string
		wantErr    string
	}{
		{"public", "master", false, "public", ""},
		{"no default branch", "", false, "public", "no default branch"},
		{"private flag", "master", true, "public", "not a public repository"},
		{"private visibility", "master", false, "private", "not a public repository"},
		{"internal visibility", "master", false, "internal", "not a public repository"},
		{"missing visibility", "master", false, "", "not a public repository"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := captureRepoPublic(tc.branch, tc.private, tc.visibility)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("public repository rejected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("got %v, want an error containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestCaptureGraphQLProof(t *testing.T) {
	head := "jfehrle:debugger_ltac2"
	when := time.Date(2026, 11, 21, 11, 27, 42, 0, time.UTC)
	anchor := repo.Commit{SHA: "7477", Branch: head, Author: "Jim Fehrle",
		Time: when, Parents: []string{"5393"}}
	pr := repo.PullRequest{Number: 64, Head: head,
		Reviews: []repo.Review{{Author: "ppedrot", State: "changes_requested"}}}
	defaultCommit := repo.Commit{SHA: "base", Branch: "master", Author: "Someone",
		Time: when, Parents: []string{"older"}}

	cases := []struct {
		name        string
		snap        repo.Snapshot
		wantReviews int
		wantAnchors int
	}{
		{
			name:        "review and head anchor",
			snap:        repo.Snapshot{PullRequests: []repo.PullRequest{pr}, Commits: []repo.Commit{defaultCommit, anchor}},
			wantReviews: 1, wantAnchors: 1,
		},
		{
			name: "review without author is not proof",
			snap: repo.Snapshot{
				PullRequests: []repo.PullRequest{{Number: 64, Head: head,
					Reviews: []repo.Review{{State: "changes_requested"}}}},
				Commits: []repo.Commit{anchor},
			},
			wantReviews: 0, wantAnchors: 1,
		},
		{
			name: "head commit without parent is not an anchor",
			snap: repo.Snapshot{
				PullRequests: []repo.PullRequest{pr},
				Commits:      []repo.Commit{{SHA: "7477", Branch: head, Author: "Jim Fehrle", Time: when}},
			},
			wantReviews: 1, wantAnchors: 0,
		},
		{
			name:        "no pull request means no proof",
			snap:        repo.Snapshot{Commits: []repo.Commit{anchor}},
			wantReviews: 0, wantAnchors: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap := tc.snap
			reviews, anchors := captureGraphQLProof(&snap, "master")
			if reviews != tc.wantReviews || anchors != tc.wantAnchors {
				t.Fatalf("got %d reviews and %d anchors, want %d and %d",
					reviews, anchors, tc.wantReviews, tc.wantAnchors)
			}
		})
	}
}

// Package fixturegen makes fictional, repeatable repository histories.
package fixturegen

import (
	"fmt"
	"github.com/brew0x/percolate/internal/repo"
	"math/rand"
	"time"
)

// Generate uses only the supplied seed and fixed fictional time.
func Generate(seed int64, branches, commits, prs, runs int) repo.Snapshot {
	r := rand.New(rand.NewSource(seed))
	s := repo.Snapshot{Now: time.Date(2026, 1, 20, 12, 0, 0, 0, time.UTC)}
	for i := 0; i < branches; i++ {
		name := fmt.Sprintf("flow-%02d", i)
		if i == 0 {
			name = "main"
		}
		s.Branches = append(s.Branches, repo.Branch{Name: name})
	}
	if branches == 0 {
		return s
	}
	last := make([]string, branches)
	for i := 0; i < commits; i++ {
		lane := i % branches
		sha := fmt.Sprintf("%08x%032x", i+1, i+1)
		var parents []string
		if last[lane] != "" {
			parents = []string{last[lane]}
		}
		// Seeded cross-lane history so golden frames exercise the parent
		// paths of issue #2: some commits fork off the neighbouring lane's
		// recorded tip, later commits merge one back, and the newest commit
		// merges the final tip of its neighbour so that lane ends merged.
		if i == commits-1 && branches > 2 {
			if tip := last[(lane+branches-1)%branches]; tip != "" && tip != last[lane] {
				parents = append(parents, tip)
			}
		} else if i >= branches && branches > 1 {
			if i%9 == 4 {
				if tip := last[(lane+branches-1)%branches]; tip != "" {
					parents = []string{tip}
				}
			} else if i%11 == 7 {
				if tip := last[(lane+1)%branches]; tip != "" && tip != last[lane] {
					parents = append(parents, tip)
				}
			}
		}
		last[lane] = sha
		s.Commits = append(s.Commits, repo.Commit{SHA: sha, Parents: parents, Branch: s.Branches[lane].Name, Author: "robin", Time: s.Now.Add(-time.Duration(commits-i) * time.Hour)})
	}
	for i := 0; i < prs; i++ {
		head := 0
		if branches > 1 {
			head = 1 + r.Intn(branches-1)
		}
		s.PullRequests = append(s.PullRequests, repo.PullRequest{Number: i + 1, Head: s.Branches[head].Name, Base: "main", State: "open", Reviews: []repo.Review{{Author: "sage", State: "approved"}}})
	}
	// Keep the first pull request on a lane visible at the default viewport so
	// replay frames exercise a linked spring feeding its flow even in a busy
	// landscape whose other heads land far to the right.
	if len(s.PullRequests) > 0 && len(s.Branches) > 1 {
		s.PullRequests[0].Head = s.Branches[1].Name
	}
	// A few fictional open issues so replay frames show both kinds of spring.
	// Every other issue carries a recorded closing reference to a pull request
	// that exists in this snapshot; the rest stay unlinked. Kept short so a
	// busy landscape stays calm.
	for i := 0; i < min(prs+1, 6); i++ {
		// GitHub numbers issues and pull requests from one sequence, so an
		// issue never shares a number with a pull request in the same repo.
		number := prs + i + 1
		issue := repo.Issue{Number: number, Title: fmt.Sprintf("issue %d", number), State: "open"}
		if i%2 == 0 && i/2 < prs {
			issue.ClosedBy = 2*(i/2) + 1
		}
		s.Issues = append(s.Issues, issue)
	}
	for i := 0; i < runs && commits > 0; i++ {
		status, conclusion := "completed", "success"
		if i%7 == 0 {
			status, conclusion = "in_progress", ""
		} else if i%5 == 0 {
			conclusion = "failure"
		}
		s.Runs = append(s.Runs, repo.WorkflowRun{ID: int64(i + 1), HeadSHA: s.Commits[(commits - 1 - i%commits)].SHA, Status: status, Conclusion: conclusion, Jobs: []repo.Job{{Name: "check", Status: status, Conclusion: conclusion}}})
	}
	if commits > 0 {
		s.Releases = []repo.Release{{Tag: "v0.1", HeadSHA: s.Commits[commits-1].SHA, Time: s.Now.Add(-time.Hour)}}
	}
	return s
}

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

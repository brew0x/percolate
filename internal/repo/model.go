// Package repo defines recorded repository state, independent of its source.
package repo

import (
	"strings"
	"time"
)

type Branch struct {
	Name string `json:"name"`
}
type Commit struct {
	SHA     string    `json:"sha"`
	Parents []string  `json:"parents"`
	Branch  string    `json:"branch"`
	Author  string    `json:"author"`
	Time    time.Time `json:"time"`
}
type Review struct {
	Author string `json:"author"`
	State  string `json:"state"`
}
type Issue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"`
	// ClosedBy is the pull request whose recorded closing reference names
	// this issue (the "closes #n" reference on that pull request). Zero means
	// the snapshot has no closing reference for it. The link is recorded by
	// the snapshot source; the scene never infers one from text or numbering.
	ClosedBy int `json:"closed_by"`
}
type PullRequest struct {
	Number  int      `json:"number"`
	Head    string   `json:"head"`
	Base    string   `json:"base"`
	State   string   `json:"state"`
	Reviews []Review `json:"reviews"`
}
type Job struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

// Running reports whether the recorded job has not completed. A missing
// status is treated as unfinished, like a run's status, so an old snapshot
// never freezes a job on a success glyph it did not record.
func (j Job) Running() bool { return j.Status != "completed" }

// FailedConclusion reports whether a recorded workflow conclusion is a
// failure. Cancelled, skipped and neutral conclusions are not failures; they
// keep the neutral mark. Runs and their recorded jobs share this so the same
// conclusion cannot mean two different things.
func FailedConclusion(conclusion string) bool {
	switch conclusion {
	case "failure", "timed_out", "startup_failure":
		return true
	}
	return false
}

// Failed reports whether the recorded conclusion is a failure. Cancelled,
// skipped and neutral jobs are not failures; they keep the neutral mark.
func (j Job) Failed() bool { return FailedConclusion(j.Conclusion) }

type WorkflowRun struct {
	ID         int64  `json:"id"`
	HeadSHA    string `json:"head_sha"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Jobs       []Job  `json:"jobs"`
}

// Failed reports whether the recorded conclusion is a failure. Cancelled,
// skipped and neutral runs are not failures; they keep the neutral mark.
func (r WorkflowRun) Failed() bool { return FailedConclusion(r.Conclusion) }

// Artifact is one recorded file published with a release. Only its name is
// recorded: no contents, size or download count is inferred from it.
type Artifact struct {
	Name string `json:"name"`
}

type Release struct {
	Tag       string     `json:"tag"`
	HeadSHA   string     `json:"head_sha"`
	Time      time.Time  `json:"time"`
	Artifacts []Artifact `json:"artifacts"`
}
type Deployment struct {
	ID          int64  `json:"id"`
	HeadSHA     string `json:"head_sha"`
	Environment string `json:"environment"`
	State       string `json:"state"`
}

// Succeeded reports whether the snapshot recorded a successful deployment.
func (d Deployment) Succeeded() bool { return strings.ToLower(d.State) == "success" }

// Failed reports whether the snapshot recorded a failed deployment. Cancelled,
// inactive and unknown states are not failures; they keep a neutral mark.
func (d Deployment) Failed() bool {
	switch strings.ToLower(d.State) {
	case "failure", "error":
		return true
	}
	return false
}

// Pending reports whether the snapshot recorded a deployment still in flight.
// An unrecorded state is not pending: no activity is implied by missing data.
func (d Deployment) Pending() bool {
	switch strings.ToLower(d.State) {
	case "queued", "pending", "in_progress":
		return true
	}
	return false
}

type Snapshot struct {
	Now          time.Time     `json:"now"`
	Branches     []Branch      `json:"branches"`
	Commits      []Commit      `json:"commits"`
	Issues       []Issue       `json:"issues"`
	PullRequests []PullRequest `json:"pull_requests"`
	Runs         []WorkflowRun `json:"runs"`
	Releases     []Release     `json:"releases"`
	Deployments  []Deployment  `json:"deployments"`
}

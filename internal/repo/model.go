// Package repo defines recorded repository state, independent of its source.
package repo

import "time"

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
type WorkflowRun struct {
	ID         int64  `json:"id"`
	HeadSHA    string `json:"head_sha"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Jobs       []Job  `json:"jobs"`
}
type Release struct {
	Tag     string    `json:"tag"`
	HeadSHA string    `json:"head_sha"`
	Time    time.Time `json:"time"`
}
type Deployment struct {
	ID          int64  `json:"id"`
	HeadSHA     string `json:"head_sha"`
	Environment string `json:"environment"`
	State       string `json:"state"`
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

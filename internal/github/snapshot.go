package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/brew0x/percolate/internal/repo"
)

// pageSize is the per-page count requested on every REST list read.
const pageSize = 100

// pullDetailBatch bounds the pull-request aliases per GraphQL query so query
// bodies stay small; 500 open PRs need 20 detail requests.
const pullDetailBatch = 25

// pullDetailPageSize is GitHub GraphQL's maximum connection page size. Reviews
// and head-commit parents are read in pages of this size, oldest first, so the
// snapshot carries the whole recorded history rather than a recent tail.
const pullDetailPageSize = 100

// pullDetailPages bounds the pages per connection: one first page plus one
// follow-up for pull requests whose reviews or parents do not fit. It keeps the
// worst-case detail read at two queries per batch, inside plannedRequests. A
// longer connection fails the whole snapshot instead of truncating silently.
const pullDetailPages = 2

// runJobsPerRunDefault and runJobsWindowDefault bound the per-run job reads.
// Each of the most recent runs costs exactly one request: GitHub's jobs
// envelope records the run's total job count, so a single page either holds
// the whole list or proves the run exceeds the per-run cap. A bigger run fails
// with one line rather than recording a truncated strip, and runs past the
// window keep a nil job list, so their details still say the truth.
const (
	runJobsPerRunDefault = pageSize
	runJobsWindowDefault = 10
)

// deploymentStatusWindowDefault bounds the per-deployment status reads. Each
// of the most recent deployments costs exactly one request: GitHub returns a
// deployment's statuses newest first, so the latest recorded status is the
// first item of the first page and no second page is ever needed.
const deploymentStatusWindowDefault = 10

// SnapshotOptions bounds one live repository read. Exceeding a limit is an
// error, never a partial snapshot: an incomplete read must not masquerade as
// repository state. Every list read enforces both its page count and its item
// cap, which is what keeps plannedRequests a true upper bound.
type SnapshotOptions struct {
	Owner, Repo     string
	MaxRequests     int
	MaxBranches     int
	MaxCommits      int
	MaxIssues       int
	MaxPullRequests int
	MaxRuns         int
	MaxReleases     int
	MaxDeployments  int
	// MaxRunsWithJobs bounds how many of the most recent runs (in the
	// recorded list order) get their job list read. Runs past the window
	// keep a nil Jobs slice, so their details still report `jobs not
	// recorded` rather than a fabricated count.
	MaxRunsWithJobs int
	// MaxJobsPerRun bounds the jobs recorded for one run. It must fit one
	// 100-item page, because each run's job list is read with a single
	// request whose total_count either confirms the page or fails the read.
	MaxJobsPerRun int
	// MaxDeploymentsWithStatuses bounds how many of the most recent
	// deployments (in the recorded list order) get their status list read.
	// A deployment past the window keeps an empty State, so its details
	// still report no recorded state rather than a guessed colour. Each
	// read is exactly one request, so the window adds at most this many
	// requests to any plan; closing it (0) reproduces the stateless
	// snapshot exactly.
	MaxDeploymentsWithStatuses int
}

// DefaultSnapshotOptions bounds one read to recent history: up to 1000 open
// issues and pull requests, 5000 default-branch commits, 5000 workflow runs and
// 1000 branches, releases and deployments, plus the job lists of the 10 most
// recent runs, at most 100 jobs each, and the latest recorded status of the 10
// most recent deployments. These are generous enough for an active repository;
// a read that still exceeds one is a clear error, never a partial snapshot.
// The worst case is 258 requests, inside the 300-request budget promised for
// #3; a short collection costs fewer because each list stops at its first
// short page. The limits hold for owner/repo in README.md.
func DefaultSnapshotOptions(owner, name string) SnapshotOptions {
	return SnapshotOptions{
		Owner:                      owner,
		Repo:                       name,
		MaxRequests:                300,
		MaxBranches:                10 * pageSize,
		MaxCommits:                 50 * pageSize,
		MaxIssues:                  10 * pageSize,
		MaxPullRequests:            10 * pageSize,
		MaxRuns:                    50 * pageSize,
		MaxReleases:                10 * pageSize,
		MaxDeployments:             10 * pageSize,
		MaxRunsWithJobs:            runJobsWindowDefault,
		MaxJobsPerRun:              runJobsPerRunDefault,
		MaxDeploymentsWithStatuses: deploymentStatusWindowDefault,
	}
}

// pages is the request count for a list cap: one page per 100 items plus the
// final short (possibly empty) page that confirms the end of the collection.
func pages(items int) int { return items/pageSize + 1 }

// plannedRequests is the exact upper bound on requests this configuration can
// issue: each list read is bounded by pages(item cap) requests, pull-request
// details never exceed pullDetailPages*ceil(MaxPullRequests/pullDetailBatch)
// queries (one first page plus at most one follow-up per batch), the run-job
// window adds exactly one request per run, because each run's job list must fit
// one page, and the deployment-status window adds one request per deployment,
// because the latest status is the first item of the first statuses page.
// BuildSnapshot rejects configurations whose plan exceeds MaxRequests, so a
// run can never drift past its budget mid-read.
func (o SnapshotOptions) plannedRequests() int {
	batches := (o.MaxPullRequests + pullDetailBatch - 1) / pullDetailBatch
	detail := pullDetailPages * batches
	return 1 + pages(o.MaxBranches) + pages(o.MaxCommits) + pages(o.MaxIssues) +
		pages(o.MaxPullRequests) + pages(o.MaxRuns) + pages(o.MaxReleases) +
		pages(o.MaxDeployments) + detail + o.MaxRunsWithJobs + o.MaxDeploymentsWithStatuses
}

// BuildSnapshot assembles repo.Snapshot from REST list reads and batched
// GraphQL pull-request details. Any list or detail failure returns an error
// and no snapshot; the one exception is the per-deployment status read,
// which leaves a deployment's State empty rather than failing the read, as
// fetchDeploymentStatuses documents. The result is a point-in-time view:
// objects can change between pages, and list ETags only deduplicate
// identical repeat reads.
func (c *Client) BuildSnapshot(ctx context.Context, opts SnapshotOptions) (repo.Snapshot, error) {
	if opts.Owner == "" || opts.Repo == "" || strings.Contains(opts.Owner, "/") || strings.Contains(opts.Repo, "/") {
		return repo.Snapshot{}, errors.New("repository must be owner/repo")
	}
	if opts.MaxRequests <= 0 {
		return repo.Snapshot{}, errors.New("request budget must be positive")
	}
	if opts.MaxRunsWithJobs < 0 {
		return repo.Snapshot{}, errors.New("run-job window must not be negative")
	}
	if opts.MaxDeploymentsWithStatuses < 0 {
		return repo.Snapshot{}, errors.New("deployment-status window must not be negative")
	}
	// One request per run can only hold a page of jobs, so the per-run cap
	// must fit a page: a larger cap could not be honoured by the read the
	// plan counts on.
	if opts.MaxRunsWithJobs > 0 && (opts.MaxJobsPerRun <= 0 || opts.MaxJobsPerRun > pageSize) {
		return repo.Snapshot{}, fmt.Errorf("per-run job limit must be between 1 and %d", pageSize)
	}
	if plan := opts.plannedRequests(); plan > opts.MaxRequests {
		return repo.Snapshot{}, fmt.Errorf("snapshot plan needs %d requests over the %d-request budget", plan, opts.MaxRequests)
	}
	snap := repo.Snapshot{Now: time.Now().UTC()}
	meta := struct {
		DefaultBranch string `json:"default_branch"`
	}{}
	if err := c.getPage(ctx, opts.repoPath(""), &meta); err != nil {
		return repo.Snapshot{}, err
	}
	if err := c.listBranches(ctx, opts, &snap); err != nil {
		return repo.Snapshot{}, err
	}
	if meta.DefaultBranch != "" {
		if err := c.listCommits(ctx, opts, meta.DefaultBranch, &snap); err != nil {
			return repo.Snapshot{}, err
		}
	}
	if err := c.listIssues(ctx, opts, &snap); err != nil {
		return repo.Snapshot{}, err
	}
	if err := c.listPullRequests(ctx, opts, &snap); err != nil {
		return repo.Snapshot{}, err
	}
	if err := c.listRuns(ctx, opts, &snap); err != nil {
		return repo.Snapshot{}, err
	}
	if err := c.fetchRunJobs(ctx, opts, &snap); err != nil {
		return repo.Snapshot{}, err
	}
	if err := c.listReleases(ctx, opts, &snap); err != nil {
		return repo.Snapshot{}, err
	}
	if err := c.listDeployments(ctx, opts, &snap); err != nil {
		return repo.Snapshot{}, err
	}
	c.fetchDeploymentStatuses(ctx, opts, &snap)
	if len(snap.PullRequests) > 0 {
		if err := c.fetchPullDetails(ctx, opts, &snap); err != nil {
			return repo.Snapshot{}, err
		}
	}
	return snap, nil
}

func (o SnapshotOptions) repoPath(suffix string) string {
	return fmt.Sprintf("/repos/%s/%s%s", url.PathEscape(o.Owner), url.PathEscape(o.Repo), suffix)
}

func (c *Client) getPage(ctx context.Context, path string, out any) error {
	body, err := c.ListPage(ctx, path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return errors.New("GitHub response was not the expected object")
	}
	return nil
}

// listInto decodes ListAll's raw items as one array of the expected shape.
func (c *Client) listInto(ctx context.Context, path string, maxPages int, out any) error {
	items, err := c.ListAll(ctx, path, maxPages)
	if err != nil {
		return err
	}
	combined := make([]byte, 0, 2)
	combined = append(combined, '[')
	for i, item := range items {
		if i > 0 {
			combined = append(combined, ',')
		}
		combined = append(combined, item...)
	}
	combined = append(combined, ']')
	if err := json.Unmarshal(combined, out); err != nil {
		return errors.New("GitHub list was not the expected shape")
	}
	return nil
}

func (c *Client) listBranches(ctx context.Context, opts SnapshotOptions, snap *repo.Snapshot) error {
	var src []struct {
		Name string `json:"name"`
	}
	if err := c.listInto(ctx, opts.repoPath("/branches"), pages(opts.MaxBranches), &src); err != nil {
		return err
	}
	if len(src) > opts.MaxBranches {
		return fmt.Errorf("GitHub branches exceed the %d-branch limit", opts.MaxBranches)
	}
	for _, b := range src {
		snap.Branches = append(snap.Branches, repo.Branch{Name: b.Name})
	}
	return nil
}

type gitPerson struct {
	Name *string    `json:"name"`
	Date *time.Time `json:"date"`
}

func (c *Client) listCommits(ctx context.Context, opts SnapshotOptions, branch string, snap *repo.Snapshot) error {
	var src []struct {
		SHA     string `json:"sha"`
		Parents []struct {
			SHA string `json:"sha"`
		} `json:"parents"`
		Commit struct {
			Author    *gitPerson `json:"author"`
			Committer *gitPerson `json:"committer"`
		} `json:"commit"`
	}
	if err := c.listInto(ctx, opts.repoPath("/commits"), pages(opts.MaxCommits), &src); err != nil {
		return err
	}
	if len(src) > opts.MaxCommits {
		return fmt.Errorf("GitHub commits exceed the %d-commit limit", opts.MaxCommits)
	}
	for _, cm := range src {
		person := cm.Commit.Author
		if person == nil {
			person = cm.Commit.Committer
		}
		author := ""
		when := time.Time{}
		if person != nil {
			if person.Name != nil {
				author = *person.Name
			}
			if person.Date != nil {
				when = *person.Date
			}
		}
		parents := make([]string, 0, len(cm.Parents))
		for _, p := range cm.Parents {
			parents = append(parents, p.SHA)
		}
		snap.Commits = append(snap.Commits, repo.Commit{SHA: cm.SHA, Parents: parents, Branch: branch, Author: author, Time: when})
	}
	return nil
}

// listIssues reads open issues from GitHub's issues endpoint. That endpoint
// shares its number sequence with pull requests and returns both: each pull
// request carries a pull_request object. Those objects are filtered before the
// cap check, so a PR neither becomes a spring nor consumes the issue budget.
// Only an issue GitHub reports as open enters the snapshot; a closing link is
// filled later from a real closing reference, never from title or numbering.
func (c *Client) listIssues(ctx context.Context, opts SnapshotOptions, snap *repo.Snapshot) error {
	var src []struct {
		Number      int             `json:"number"`
		Title       string          `json:"title"`
		State       string          `json:"state"`
		PullRequest json.RawMessage `json:"pull_request"`
	}
	if err := c.listInto(ctx, opts.repoPath("/issues?state=open"), pages(opts.MaxIssues), &src); err != nil {
		return err
	}
	open := src[:0]
	for _, issue := range src {
		if issue.State != "open" {
			continue
		}
		if len(issue.PullRequest) > 0 && strings.TrimSpace(string(issue.PullRequest)) != "null" {
			continue
		}
		open = append(open, issue)
	}
	if len(open) > opts.MaxIssues {
		return fmt.Errorf("GitHub open issues exceed the %d-issue limit", opts.MaxIssues)
	}
	for _, issue := range open {
		snap.Issues = append(snap.Issues, repo.Issue{Number: issue.Number, Title: issue.Title, State: issue.State})
	}
	return nil
}

func (c *Client) listPullRequests(ctx context.Context, opts SnapshotOptions, snap *repo.Snapshot) error {
	var src []struct {
		Number int    `json:"number"`
		State  string `json:"state"`
		Head   struct {
			Ref   string `json:"ref"`
			Label string `json:"label"`
			Repo  struct {
				FullName string `json:"full_name"`
			} `json:"repo"`
		} `json:"head"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
	}
	if err := c.listInto(ctx, opts.repoPath("/pulls?state=open"), pages(opts.MaxPullRequests), &src); err != nil {
		return err
	}
	if len(src) > opts.MaxPullRequests {
		return fmt.Errorf("GitHub open pull requests exceed the %d-pull-request limit", opts.MaxPullRequests)
	}
	upstream := opts.Owner + "/" + opts.Repo
	for _, p := range src {
		head := p.Head.Ref
		// A fork pull request's head branch lives in another repository and
		// can share a ref with the base branch (contributor/main -> main).
		// GitHub's head.label qualifies it with the fork owner, so use that
		// as the head lane: otherwise the fork's commit joins the upstream
		// branch lane and two forks with the same ref collapse into one.
		// Same-repository pulls keep the branch name, so recorded lanes for
		// them are unchanged.
		if p.Head.Repo.FullName != "" && p.Head.Label != "" &&
			!strings.EqualFold(p.Head.Repo.FullName, upstream) {
			head = p.Head.Label
		}
		snap.PullRequests = append(snap.PullRequests, repo.PullRequest{
			Number: p.Number, Head: head, Base: p.Base.Ref, State: p.State,
		})
	}
	return nil
}

// listRuns reads the workflow-runs object endpoint, which wraps its array and
// so cannot go through ListAll. Collection follows the same bounded rule:
// pages in order until a short page, and more items than MaxRuns is an error
// rather than a truncated history presented as repository state.
func (c *Client) listRuns(ctx context.Context, opts SnapshotOptions, snap *repo.Snapshot) error {
	var runs []repo.WorkflowRun
	for page := 1; page <= pages(opts.MaxRuns); page++ {
		var obj struct {
			WorkflowRuns []struct {
				ID         int64  `json:"id"`
				HeadSHA    string `json:"head_sha"`
				Status     string `json:"status"`
				Conclusion string `json:"conclusion"`
			} `json:"workflow_runs"`
		}
		path := fmt.Sprintf("%s?per_page=%d&page=%d", opts.repoPath("/actions/runs"), pageSize, page)
		if err := c.getPage(ctx, path, &obj); err != nil {
			return err
		}
		for _, r := range obj.WorkflowRuns {
			runs = append(runs, repo.WorkflowRun{ID: r.ID, HeadSHA: r.HeadSHA, Status: r.Status, Conclusion: r.Conclusion})
		}
		if len(obj.WorkflowRuns) < pageSize {
			break
		}
	}
	if len(runs) > opts.MaxRuns {
		return fmt.Errorf("GitHub workflow runs exceed the %d-run limit", opts.MaxRuns)
	}
	snap.Runs = runs
	return nil
}

// fetchRunJobs records the job lists of the most recent runs, in the order the
// run list was read (GitHub returns newest first). One request per run reads a
// full page of jobs, and the same response's total_count either confirms the
// page held the whole list or fails the read: a run with more jobs than
// MaxJobsPerRun, a page short of its own count, or a full page with no
// matching count is one clear error, never a partial strip recorded as
// complete. A run whose jobs were not read — past the window, or a snapshot
// with the window closed — keeps a nil Jobs slice, so details keep saying
// `jobs not recorded` rather than a fabricated zero. A run read within the
// window whose list is empty records an explicitly empty list, which is the
// honest statement `0 jobs`.
func (c *Client) fetchRunJobs(ctx context.Context, opts SnapshotOptions, snap *repo.Snapshot) error {
	reads := min(len(snap.Runs), opts.MaxRunsWithJobs)
	for i := 0; i < reads; i++ {
		run := &snap.Runs[i]
		var obj struct {
			TotalCount int `json:"total_count"`
			Jobs       []struct {
				Name       string `json:"name"`
				Status     string `json:"status"`
				Conclusion string `json:"conclusion"`
			} `json:"jobs"`
		}
		path := fmt.Sprintf("%s?per_page=%d", opts.repoPath(fmt.Sprintf("/actions/runs/%d/jobs", run.ID)), pageSize)
		if err := c.getPage(ctx, path, &obj); err != nil {
			return err
		}
		if obj.TotalCount > opts.MaxJobsPerRun {
			return fmt.Errorf("GitHub run %d jobs exceed the %d-job limit", run.ID, opts.MaxJobsPerRun)
		}
		// A page of fewer than pageSize jobs ends the list by GitHub's own
		// paging rule, but it must still carry every job total_count names;
		// a full page needs the count to prove nothing was left behind.
		if len(obj.Jobs) < obj.TotalCount || (len(obj.Jobs) == pageSize && len(obj.Jobs) != obj.TotalCount) {
			return fmt.Errorf("GitHub returned an unconfirmed job list for run %d (%d of %d)", run.ID, len(obj.Jobs), obj.TotalCount)
		}
		jobs := make([]repo.Job, 0, len(obj.Jobs))
		for _, j := range obj.Jobs {
			jobs = append(jobs, repo.Job{Name: j.Name, Status: j.Status, Conclusion: j.Conclusion})
		}
		run.Jobs = jobs
	}
	return nil
}

// isSHA reports whether a release target is a commit SHA rather than a branch
// name; only a SHA can anchor a release to a lane.
func isSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, r := range s {
		hex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
		if !hex {
			return false
		}
	}
	return true
}

func (c *Client) listReleases(ctx context.Context, opts SnapshotOptions, snap *repo.Snapshot) error {
	var src []struct {
		TagName         string     `json:"tag_name"`
		TargetCommitish string     `json:"target_commitish"`
		PublishedAt     *time.Time `json:"published_at"`
		Draft           bool       `json:"draft"`
		Assets          []struct {
			Name string `json:"name"`
		} `json:"assets"`
	}
	if err := c.listInto(ctx, opts.repoPath("/releases"), pages(opts.MaxReleases), &src); err != nil {
		return err
	}
	// GitHub returns drafts to callers with push access. A draft was never
	// published, so it must not draw a release mark or enter release details.
	// Filter before the cap check so drafts do not consume the release budget.
	published := src[:0]
	for _, r := range src {
		if !r.Draft {
			published = append(published, r)
		}
	}
	if len(published) > opts.MaxReleases {
		return fmt.Errorf("GitHub releases exceed the %d-release limit", opts.MaxReleases)
	}
	for _, r := range published {
		when := time.Time{}
		if r.PublishedAt != nil {
			when = *r.PublishedAt
		}
		head := ""
		if isSHA(r.TargetCommitish) {
			head = r.TargetCommitish
		}
		// The release list page already carries each release's assets
		// inline, so their names land in GitHub order without any
		// per-release request. A draft's assets never land, because drafts
		// were filtered above. A published release's asset list is recorded
		// even when it is empty — a release that published no assets is an
		// honest `0 artifacts`, never `artifacts not recorded` — while an
		// item whose page omits the field entirely was not read and keeps
		// a nil list.
		var artifacts []repo.Artifact
		if r.Assets != nil {
			artifacts = make([]repo.Artifact, 0, len(r.Assets))
			for _, a := range r.Assets {
				artifacts = append(artifacts, repo.Artifact{Name: a.Name})
			}
		}
		snap.Releases = append(snap.Releases, repo.Release{Tag: r.TagName, HeadSHA: head, Time: when, Artifacts: artifacts})
	}
	return nil
}

func (c *Client) listDeployments(ctx context.Context, opts SnapshotOptions, snap *repo.Snapshot) error {
	var src []struct {
		ID          int64  `json:"id"`
		SHA         string `json:"sha"`
		Environment string `json:"environment"`
	}
	if err := c.listInto(ctx, opts.repoPath("/deployments"), pages(opts.MaxDeployments), &src); err != nil {
		return err
	}
	if len(src) > opts.MaxDeployments {
		return fmt.Errorf("GitHub deployments exceed the %d-deployment limit", opts.MaxDeployments)
	}
	for _, d := range src {
		snap.Deployments = append(snap.Deployments, repo.Deployment{ID: d.ID, HeadSHA: d.SHA, Environment: d.Environment})
	}
	return nil
}

// fetchDeploymentStatuses records the latest status of the most recent
// deployments, in the order the deployment list was read (GitHub returns
// newest first). One request per deployment reads the first page of
// GET /repos/{owner}/{repo}/deployments/{id}/statuses; that list is newest
// first too, so the first item is the latest recorded status and no second
// page exists to need. A read that fails, a body that is not a status list,
// or an empty list leaves that deployment's State empty — never a guess —
// and the read moves on: the deployment itself was recorded completely by
// the list, and its state is the colour of a flow, not the identity of an
// object, so a refused page must not cost the whole snapshot. (A refusal
// caused by an exhausted request budget is not hidden for long: the next
// poll's first request fails that read and schedules the recorded pause.) A
// deployment past the window, or a snapshot with the window closed, keeps
// an empty State, which details report as `state: not recorded`. The whole
// fan-out is bounded by MaxDeploymentsWithStatuses and counted in
// plannedRequests, so it cannot drift past MaxRequests.
func (c *Client) fetchDeploymentStatuses(ctx context.Context, opts SnapshotOptions, snap *repo.Snapshot) {
	reads := min(len(snap.Deployments), opts.MaxDeploymentsWithStatuses)
	for i := 0; i < reads; i++ {
		d := &snap.Deployments[i]
		var statuses []struct {
			State string `json:"state"`
		}
		path := fmt.Sprintf("%s?per_page=%d", opts.repoPath(fmt.Sprintf("/deployments/%d/statuses", d.ID)), pageSize)
		if err := c.getPage(ctx, path, &statuses); err != nil {
			continue
		}
		if len(statuses) == 0 {
			continue // No recorded status: an honest empty, never a borrowed state.
		}
		d.State = statuses[0].State
	}
}

// detailPageInfo is the GraphQL cursor state for a reviews, closing-issue or
// parents connection. A cursor is only followed when hasNextPage is true.
type detailPageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type detailReview struct {
	Author *struct {
		Login string `json:"login"`
	} `json:"author"`
	State string `json:"state"`
}

type detailParents struct {
	Nodes []struct {
		OID string `json:"oid"`
	} `json:"nodes"`
	PageInfo detailPageInfo `json:"pageInfo"`
}

// closingIssueNode is one node of a pull request's closingIssuesReferences
// connection. A null node is not a reference and is skipped.
type closingIssueNode struct {
	Number int `json:"number"`
}

type pullDetail struct {
	Number  int `json:"number"`
	Reviews struct {
		Nodes    []detailReview `json:"nodes"`
		PageInfo detailPageInfo `json:"pageInfo"`
	} `json:"reviews"`
	ClosingIssues struct {
		Nodes    []*closingIssueNode `json:"nodes"`
		PageInfo detailPageInfo      `json:"pageInfo"`
	} `json:"closingIssuesReferences"`
	HeadRef *struct {
		Target json.RawMessage `json:"target"`
	} `json:"headRef"`
}

type commitDetail struct {
	OID           string     `json:"oid"`
	CommittedDate *time.Time `json:"committedDate"`
	Author        *struct {
		Name *string `json:"name"`
	} `json:"author"`
	Parents detailParents `json:"parents"`
}

// detailCursor is the follow-up state for one pull request in a batch. Empty
// flags mean the first page; a follow-up asks only for the connections that
// reported another page, carrying that connection's opaque cursor.
type detailCursor struct {
	review, parent, closing             string
	needReview, needParent, needClosing bool
	commitIndex                         int // where the head commit was appended, to extend its parents
}

// graphqlString quotes an opaque cursor as a GraphQL string literal. Cursors
// come from the API, never from user input, but quoting keeps control
// characters and escapes from changing the query.
func graphqlString(s string) string {
	quoted, _ := json.Marshal(s) // marshaling a string cannot fail
	return string(quoted)
}

// pullDetailQuery builds one batched query. The first page reads the review,
// closing-issue-reference and parent connections in full-page steps; a
// follow-up reads only the connections whose pageInfo asked for another page.
func pullDetailQuery(chunk []repo.PullRequest, cursors []detailCursor) string {
	var q strings.Builder
	q.WriteString("query($owner:String!,$name:String!){repository(owner:$owner,name:$name){")
	for i, p := range chunk {
		fmt.Fprintf(&q, "p%d:pullRequest(number:%d){", i, p.Number)
		if i < len(cursors) && (cursors[i].needReview || cursors[i].needParent || cursors[i].needClosing) {
			q.WriteString("number ")
			if cursors[i].needReview {
				fmt.Fprintf(&q, "reviews(first:%d,after:%s){nodes{author{login} state} pageInfo{hasNextPage endCursor}} ",
					pullDetailPageSize, graphqlString(cursors[i].review))
			}
			if cursors[i].needClosing {
				fmt.Fprintf(&q, "closingIssuesReferences(first:%d,after:%s){nodes{number} pageInfo{hasNextPage endCursor}} ",
					pullDetailPageSize, graphqlString(cursors[i].closing))
			}
			if cursors[i].needParent {
				// oid is requested on both the GitObject target and the Commit
				// fragment: the follow-up handler verifies that this is still
				// the head commit seen on the first page before extending its
				// parents, and a real GraphQL response only carries fields the
				// query asked for.
				fmt.Fprintf(&q, "headRef{target{oid ... on Commit{oid parents(first:%d,after:%s){nodes{oid} pageInfo{hasNextPage endCursor}}}}}",
					pullDetailPageSize, graphqlString(cursors[i].parent))
			}
		} else {
			fmt.Fprintf(&q, "number headRefOid headRefName baseRefName"+
				" reviews(first:%d){nodes{author{login} state} pageInfo{hasNextPage endCursor}}"+
				" closingIssuesReferences(first:%d){nodes{number} pageInfo{hasNextPage endCursor}}"+
				" headRef{target{oid ... on Commit{oid committedDate author{name}"+
				" parents(first:%d){nodes{oid} pageInfo{hasNextPage endCursor}}}}}",
				pullDetailPageSize, pullDetailPageSize, pullDetailPageSize)
		}
		q.WriteString("}")
	}
	q.WriteString("}}")
	return q.String()
}

// pullDetailAliases validates the repository envelope and returns its aliases.
func pullDetailAliases(data []byte) (map[string]json.RawMessage, error) {
	var out struct {
		Repository map[string]json.RawMessage `json:"repository"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, errors.New("GitHub GraphQL response was not the expected shape")
	}
	if out.Repository == nil {
		return nil, errors.New("GitHub GraphQL returned no repository")
	}
	return out.Repository, nil
}

// nextCursor turns pageInfo into a follow-up request. A missing cursor with a
// promised next page is an error, not a reason to stop early: stopping would
// present a truncated list as complete.
func nextCursor(info detailPageInfo, number int, what string) (string, bool, error) {
	if !info.HasNextPage {
		return "", false, nil
	}
	if info.EndCursor == "" {
		return "", false, fmt.Errorf("GitHub GraphQL omitted the %s cursor for pull request %d", what, number)
	}
	return info.EndCursor, true, nil
}

func appendReviews(p *repo.PullRequest, nodes []detailReview) {
	for _, n := range nodes {
		author := ""
		if n.Author != nil {
			author = n.Author.Login
		}
		p.Reviews = append(p.Reviews, repo.Review{Author: author, State: strings.ToLower(n.State)})
	}
}

// appendClosingReferences records issue numbers named by a pull request's
// closingIssuesReferences connection. A reference is the only source of an
// issue's ClosedBy: the first PR naming an issue in snapshot order owns the
// link, so a later duplicate cannot silently reassign it. Numbers with no
// matching open issue are ignored when the links are applied.
func appendClosingReferences(refs map[int]int, nodes []*closingIssueNode, prNumber int) {
	for _, node := range nodes {
		if node == nil || node.Number <= 0 {
			continue
		}
		if _, seen := refs[node.Number]; !seen {
			refs[node.Number] = prNumber
		}
	}
}

func appendParents(parents []string, nodes []struct {
	OID string `json:"oid"`
}) []string {
	for _, n := range nodes {
		parents = append(parents, n.OID)
	}
	return parents
}

// fetchPullDetails fills per-PR review state, closing issue references and
// head-commit anchors through batched aliased GraphQL queries. Each connection
// is read in pages of 100, oldest first, with at most one follow-up page per
// batch; the snapshot holds the complete history for every list GitHub returns
// within that bound. A longer list fails the whole read instead of being
// truncated, and the request count stays inside pullDetailPages per batch. A
// closing reference only fills ClosedBy on an open issue GitHub actually
// returned; a number with no matching issue stays unlinked. A PR that vanishes
// between list and detail keeps its lane but gains no details; the whole call
// fails on server errors.
func (c *Client) fetchPullDetails(ctx context.Context, opts SnapshotOptions, snap *repo.Snapshot) error {
	known := map[string]bool{}
	closingByIssue := map[int]int{}
	for _, cm := range snap.Commits {
		known[cm.SHA] = true
	}
	variables := map[string]any{"owner": opts.Owner, "name": opts.Repo}
	for start := 0; start < len(snap.PullRequests); start += pullDetailBatch {
		chunk := snap.PullRequests[start:min(start+pullDetailBatch, len(snap.PullRequests))]
		data, err := c.GraphQL(ctx, pullDetailQuery(chunk, nil), variables)
		if err != nil {
			return err
		}
		aliases, err := pullDetailAliases(data)
		if err != nil {
			return err
		}
		cursors := make([]detailCursor, len(chunk))
		pending := false
		for i := range chunk {
			raw, ok := aliases[fmt.Sprintf("p%d", i)]
			if !ok || string(raw) == "null" {
				continue // A PR that vanished between list and detail keeps its lane.
			}
			var d pullDetail
			if err := json.Unmarshal(raw, &d); err != nil {
				return errors.New("GitHub GraphQL pull-request detail was not the expected shape")
			}
			p := &snap.PullRequests[start+i]
			if d.Number != p.Number {
				return errors.New("GitHub GraphQL returned mismatched pull requests")
			}
			reviewCursor, needReview, err := nextCursor(d.Reviews.PageInfo, p.Number, "reviews")
			if err != nil {
				return err
			}
			cursors[i].review, cursors[i].needReview = reviewCursor, needReview
			if needReview {
				pending = true
			}
			appendReviews(p, d.Reviews.Nodes)
			closingCursor, needClosing, err := nextCursor(d.ClosingIssues.PageInfo, p.Number, "closing issue references")
			if err != nil {
				return err
			}
			cursors[i].closing, cursors[i].needClosing = closingCursor, needClosing
			if needClosing {
				pending = true
			}
			appendClosingReferences(closingByIssue, d.ClosingIssues.Nodes, p.Number)
			if d.HeadRef == nil || string(d.HeadRef.Target) == "null" {
				continue
			}
			var commit commitDetail
			if err := json.Unmarshal(d.HeadRef.Target, &commit); err != nil || commit.CommittedDate == nil || commit.OID == "" {
				continue // A non-commit target or unknown date cannot anchor a lane.
			}
			if known[commit.OID] {
				continue // The REST commit list already carries this commit's parents.
			}
			known[commit.OID] = true
			parentCursor, needParent, err := nextCursor(commit.Parents.PageInfo, p.Number, "head-commit parents")
			if err != nil {
				return err
			}
			cursors[i].parent, cursors[i].needParent = parentCursor, needParent
			if needParent {
				pending = true
			}
			parents := appendParents(nil, commit.Parents.Nodes)
			author := ""
			if commit.Author != nil && commit.Author.Name != nil {
				author = *commit.Author.Name
			}
			// The lane label comes from the REST list (fork-qualified for a
			// fork pull request) so head and lane always agree, even if the
			// branch is renamed between reads.
			cursors[i].commitIndex = len(snap.Commits)
			snap.Commits = append(snap.Commits, repo.Commit{
				SHA: commit.OID, Parents: parents, Branch: p.Head, Author: author, Time: *commit.CommittedDate,
			})
		}
		if !pending {
			continue
		}
		data, err = c.GraphQL(ctx, pullDetailQuery(chunk, cursors), variables)
		if err != nil {
			return err
		}
		aliases, err = pullDetailAliases(data)
		if err != nil {
			return err
		}
		for i, cur := range cursors {
			if !cur.needReview && !cur.needParent && !cur.needClosing {
				continue
			}
			p := &snap.PullRequests[start+i]
			raw, ok := aliases[fmt.Sprintf("p%d", i)]
			if !ok || string(raw) == "null" {
				return fmt.Errorf("GitHub GraphQL lost pull request %d between detail pages", p.Number)
			}
			var d pullDetail
			if err := json.Unmarshal(raw, &d); err != nil {
				return errors.New("GitHub GraphQL pull-request detail was not the expected shape")
			}
			if d.Number != p.Number {
				return errors.New("GitHub GraphQL returned mismatched pull requests")
			}
			if cur.needReview {
				appendReviews(p, d.Reviews.Nodes)
				if d.Reviews.PageInfo.HasNextPage {
					return fmt.Errorf("GitHub pull request %d reviews exceed the %d-review limit", p.Number, pullDetailPageSize*pullDetailPages)
				}
			}
			if cur.needClosing {
				appendClosingReferences(closingByIssue, d.ClosingIssues.Nodes, p.Number)
				if d.ClosingIssues.PageInfo.HasNextPage {
					return fmt.Errorf("GitHub pull request %d closing issue references exceed the %d-issue limit", p.Number, pullDetailPageSize*pullDetailPages)
				}
			}
			if cur.needParent {
				if d.HeadRef == nil || string(d.HeadRef.Target) == "null" {
					return fmt.Errorf("GitHub GraphQL lost pull request %d head commit between detail pages", p.Number)
				}
				var commit commitDetail
				if err := json.Unmarshal(d.HeadRef.Target, &commit); err != nil || commit.OID == "" {
					return errors.New("GitHub GraphQL pull-request head commit was not the expected shape")
				}
				if commit.OID != snap.Commits[cur.commitIndex].SHA {
					return errors.New("GitHub GraphQL returned a mismatched head commit")
				}
				snap.Commits[cur.commitIndex].Parents = appendParents(snap.Commits[cur.commitIndex].Parents, commit.Parents.Nodes)
				if commit.Parents.PageInfo.HasNextPage {
					return fmt.Errorf("GitHub pull request %d head parents exceed the %d-parent limit", p.Number, pullDetailPageSize*pullDetailPages)
				}
			}
		}
	}
	// A reference names an issue only when GitHub returned that issue as part
	// of this snapshot. A closed issue, a PR number from the shared sequence,
	// or a vanished issue has no spring and gains no invented link.
	for i := range snap.Issues {
		if pr, ok := closingByIssue[snap.Issues[i].Number]; ok {
			snap.Issues[i].ClosedBy = pr
		}
	}
	return nil
}

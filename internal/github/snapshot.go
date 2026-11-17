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

// SnapshotOptions bounds one live repository read. Exceeding a limit is an
// error, never a partial snapshot: an incomplete read must not masquerade as
// repository state. Every list read enforces both its page count and its item
// cap, which is what keeps plannedRequests a true upper bound.
type SnapshotOptions struct {
	Owner, Repo     string
	MaxRequests     int
	MaxBranches     int
	MaxCommits      int
	MaxPullRequests int
	MaxRuns         int
	MaxReleases     int
	MaxDeployments  int
}

// DefaultSnapshotOptions bounds one read to recent history: up to 1000 open
// pull requests, 5000 default-branch commits, 5000 workflow runs and 1000 of
// each other list. These are generous enough for an active repository; a read
// that still exceeds one is a clear error, never a partial snapshot. The worst
// case is 187 requests, under the 250-request budget (and the 300-request
// budget promised for #3); a short collection costs fewer because each list
// stops at its first short page. The limits hold for owner/repo in README.md.
func DefaultSnapshotOptions(owner, name string) SnapshotOptions {
	return SnapshotOptions{
		Owner:           owner,
		Repo:            name,
		MaxRequests:     250,
		MaxBranches:     10 * pageSize,
		MaxCommits:      50 * pageSize,
		MaxPullRequests: 10 * pageSize,
		MaxRuns:         50 * pageSize,
		MaxReleases:     10 * pageSize,
		MaxDeployments:  10 * pageSize,
	}
}

// pages is the request count for a list cap: one page per 100 items plus the
// final short (possibly empty) page that confirms the end of the collection.
func pages(items int) int { return items/pageSize + 1 }

// plannedRequests is the exact upper bound on requests this configuration can
// issue: each list read is bounded by pages(item cap) requests and, because
// every item cap is enforced, pull-request details never exceed
// ceil(MaxPullRequests/pullDetailBatch) queries. BuildSnapshot rejects
// configurations whose plan exceeds MaxRequests, so a run can never drift
// past its budget mid-read.
func (o SnapshotOptions) plannedRequests() int {
	detail := (o.MaxPullRequests + pullDetailBatch - 1) / pullDetailBatch
	return 1 + pages(o.MaxBranches) + pages(o.MaxCommits) + pages(o.MaxPullRequests) +
		pages(o.MaxRuns) + pages(o.MaxReleases) + pages(o.MaxDeployments) + detail
}

// BuildSnapshot assembles repo.Snapshot from REST list reads and batched
// GraphQL pull-request details. Any failure returns an error and no snapshot.
// The result is a point-in-time view: objects can change between pages, and
// list ETags only deduplicate identical repeat reads.
func (c *Client) BuildSnapshot(ctx context.Context, opts SnapshotOptions) (repo.Snapshot, error) {
	if opts.Owner == "" || opts.Repo == "" || strings.Contains(opts.Owner, "/") || strings.Contains(opts.Repo, "/") {
		return repo.Snapshot{}, errors.New("repository must be owner/repo")
	}
	if opts.MaxRequests <= 0 {
		return repo.Snapshot{}, errors.New("request budget must be positive")
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
	if err := c.listPullRequests(ctx, opts, &snap); err != nil {
		return repo.Snapshot{}, err
	}
	if err := c.listRuns(ctx, opts, &snap); err != nil {
		return repo.Snapshot{}, err
	}
	if err := c.listReleases(ctx, opts, &snap); err != nil {
		return repo.Snapshot{}, err
	}
	if err := c.listDeployments(ctx, opts, &snap); err != nil {
		return repo.Snapshot{}, err
	}
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
		snap.Releases = append(snap.Releases, repo.Release{Tag: r.TagName, HeadSHA: head, Time: when})
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
		// Per-deployment status reads stay out of this slice; state is left
		// empty rather than guessed from anything but a real status.
		snap.Deployments = append(snap.Deployments, repo.Deployment{ID: d.ID, HeadSHA: d.SHA, Environment: d.Environment})
	}
	return nil
}

// fetchPullDetails fills per-PR review state and head-commit anchors through
// batched aliased GraphQL queries. Each PR records at most the 10 most recent
// reviews (reviews(last:10), the tail of GitHub's oldest-first connection) and
// its head commit at most 3 parents; anything beyond those caps is absent from
// the snapshot, and the caps are stated here and in the package README until
// per-PR pagination reads the remainder. A PR that vanishes between list and
// detail keeps its lane but gains no reviews; the whole call fails on server
// errors.
func (c *Client) fetchPullDetails(ctx context.Context, opts SnapshotOptions, snap *repo.Snapshot) error {
	known := map[string]bool{}
	for _, cm := range snap.Commits {
		known[cm.SHA] = true
	}
	for start := 0; start < len(snap.PullRequests); start += pullDetailBatch {
		chunk := snap.PullRequests[start:min(start+pullDetailBatch, len(snap.PullRequests))]
		var q strings.Builder
		q.WriteString("query($owner:String!,$name:String!){repository(owner:$owner,name:$name){")
		for i, p := range chunk {
			fmt.Fprintf(&q, "p%d:pullRequest(number:%d){number headRefOid headRefName baseRefName"+
				" headRef{target{oid ... on Commit{oid committedDate author{name} parents(first:3){nodes{oid}}}}}"+
				" reviews(last:10){nodes{author{login} state}}}", i, p.Number)
		}
		q.WriteString("}}")
		data, err := c.GraphQL(ctx, q.String(), map[string]any{"owner": opts.Owner, "name": opts.Repo})
		if err != nil {
			return err
		}
		var out struct {
			Repository map[string]json.RawMessage `json:"repository"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return errors.New("GitHub GraphQL response was not the expected shape")
		}
		if out.Repository == nil {
			return errors.New("GitHub GraphQL returned no repository")
		}
		for i := range chunk {
			raw, ok := out.Repository[fmt.Sprintf("p%d", i)]
			if !ok || string(raw) == "null" {
				continue
			}
			var d struct {
				Number      int    `json:"number"`
				HeadRefOid  string `json:"headRefOid"`
				HeadRefName string `json:"headRefName"`
				HeadRef     *struct {
					Target json.RawMessage `json:"target"`
				} `json:"headRef"`
				Reviews struct {
					Nodes []struct {
						Author *struct {
							Login string `json:"login"`
						} `json:"author"`
						State string `json:"state"`
					} `json:"nodes"`
				} `json:"reviews"`
			}
			if err := json.Unmarshal(raw, &d); err != nil {
				return errors.New("GitHub GraphQL pull-request detail was not the expected shape")
			}
			p := &snap.PullRequests[start+i]
			if d.Number != p.Number {
				return errors.New("GitHub GraphQL returned mismatched pull requests")
			}
			for _, n := range d.Reviews.Nodes {
				author := ""
				if n.Author != nil {
					author = n.Author.Login
				}
				p.Reviews = append(p.Reviews, repo.Review{Author: author, State: strings.ToLower(n.State)})
			}
			if d.HeadRef == nil || string(d.HeadRef.Target) == "null" {
				continue
			}
			var commit struct {
				OID           string     `json:"oid"`
				CommittedDate *time.Time `json:"committedDate"`
				Author        *struct {
					Name *string `json:"name"`
				} `json:"author"`
				Parents struct {
					Nodes []struct {
						OID string `json:"oid"`
					} `json:"nodes"`
				} `json:"parents"`
			}
			if err := json.Unmarshal(d.HeadRef.Target, &commit); err != nil || commit.CommittedDate == nil || commit.OID == "" {
				continue // A non-commit target or unknown date cannot anchor a lane.
			}
			if known[commit.OID] {
				continue
			}
			known[commit.OID] = true
			parents := make([]string, 0, len(commit.Parents.Nodes))
			for _, n := range commit.Parents.Nodes {
				parents = append(parents, n.OID)
			}
			author := ""
			if commit.Author != nil && commit.Author.Name != nil {
				author = *commit.Author.Name
			}
			// The lane label comes from the REST list (fork-qualified for a
			// fork pull request) so head and lane always agree, even if the
			// branch is renamed between reads.
			snap.Commits = append(snap.Commits, repo.Commit{
				SHA: commit.OID, Parents: parents, Branch: p.Head, Author: author, Time: *commit.CommittedDate,
			})
		}
	}
	return nil
}

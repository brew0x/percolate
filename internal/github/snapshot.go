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
// case is 227 requests, under the 250-request budget (and the 300-request
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
// pullDetailPages*ceil(MaxPullRequests/pullDetailBatch) queries (one first page
// plus at most one follow-up per batch). BuildSnapshot rejects configurations
// whose plan exceeds MaxRequests, so a run can never drift past its budget
// mid-read.
func (o SnapshotOptions) plannedRequests() int {
	batches := (o.MaxPullRequests + pullDetailBatch - 1) / pullDetailBatch
	detail := pullDetailPages * batches
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

// detailPageInfo is the GraphQL cursor state for a reviews or parents
// connection. A cursor is only followed when hasNextPage is true.
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

type pullDetail struct {
	Number  int `json:"number"`
	Reviews struct {
		Nodes    []detailReview `json:"nodes"`
		PageInfo detailPageInfo `json:"pageInfo"`
	} `json:"reviews"`
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
	review, parent         string
	needReview, needParent bool
	commitIndex            int // where the head commit was appended, to extend its parents
}

// graphqlString quotes an opaque cursor as a GraphQL string literal. Cursors
// come from the API, never from user input, but quoting keeps control
// characters and escapes from changing the query.
func graphqlString(s string) string {
	quoted, _ := json.Marshal(s) // marshaling a string cannot fail
	return string(quoted)
}

// pullDetailQuery builds one batched query. The first page reads both review
// and parent connections in full-page steps; a follow-up reads only the
// connections whose pageInfo asked for another page.
func pullDetailQuery(chunk []repo.PullRequest, cursors []detailCursor) string {
	var q strings.Builder
	q.WriteString("query($owner:String!,$name:String!){repository(owner:$owner,name:$name){")
	for i, p := range chunk {
		fmt.Fprintf(&q, "p%d:pullRequest(number:%d){", i, p.Number)
		if i < len(cursors) && (cursors[i].needReview || cursors[i].needParent) {
			q.WriteString("number ")
			if cursors[i].needReview {
				fmt.Fprintf(&q, "reviews(first:%d,after:%s){nodes{author{login} state} pageInfo{hasNextPage endCursor}} ",
					pullDetailPageSize, graphqlString(cursors[i].review))
			}
			if cursors[i].needParent {
				fmt.Fprintf(&q, "headRef{target{... on Commit{parents(first:%d,after:%s){nodes{oid} pageInfo{hasNextPage endCursor}}}}}",
					pullDetailPageSize, graphqlString(cursors[i].parent))
			}
		} else {
			fmt.Fprintf(&q, "number headRefOid headRefName baseRefName"+
				" reviews(first:%d){nodes{author{login} state} pageInfo{hasNextPage endCursor}}"+
				" headRef{target{oid ... on Commit{oid committedDate author{name}"+
				" parents(first:%d){nodes{oid} pageInfo{hasNextPage endCursor}}}}}",
				pullDetailPageSize, pullDetailPageSize)
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

func appendParents(parents []string, nodes []struct {
	OID string `json:"oid"`
}) []string {
	for _, n := range nodes {
		parents = append(parents, n.OID)
	}
	return parents
}

// fetchPullDetails fills per-PR review state and head-commit anchors through
// batched aliased GraphQL queries. Each connection is read in pages of 100,
// oldest first, with at most one follow-up page per batch; the snapshot holds
// the complete history for every list GitHub returns within that bound. A
// longer list fails the whole read instead of being truncated, and the
// request count stays inside pullDetailPages per batch. A PR that vanishes
// between list and detail keeps its lane but gains no reviews; the whole call
// fails on server errors.
func (c *Client) fetchPullDetails(ctx context.Context, opts SnapshotOptions, snap *repo.Snapshot) error {
	known := map[string]bool{}
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
			if !cur.needReview && !cur.needParent {
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
	return nil
}

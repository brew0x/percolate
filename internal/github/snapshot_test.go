package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/brew0x/percolate/internal/repo"
)

func sha(i int) string { return fmt.Sprintf("%040x", i) }

func branchItem(i int) string { return fmt.Sprintf(`{"name":"branch-%d"}`, i) }

func commitItem(i int) string {
	parent := ""
	if i > 1 {
		parent = fmt.Sprintf(`,"parents":[{"sha":"%s"}]`, sha(i-1))
	}
	return fmt.Sprintf(`{"sha":"%s"%s,"commit":{"author":{"name":"robin","date":"2026-01-01T00:00:%02dZ"}}}`,
		sha(i), parent, i%60)
}

func pullItem(i int) string {
	return fmt.Sprintf(`{"number":%d,"state":"open","head":{"ref":"flow-%d"},"base":{"ref":"main"}}`, i, i)
}

// issue numbers live far from the fake PR numbers: GitHub issues and PRs share
// one sequence, so a link must come from a closing reference rather than from
// "nearby" numbers.
func issueNumber(i int) int { return 10000 + i }

func issueItem(i int) string {
	return fmt.Sprintf(`{"number":%d,"title":"issue %d","state":"open"}`, issueNumber(i), i)
}

func runItem(i int) string {
	return fmt.Sprintf(`{"id":%d,"head_sha":"%s","status":"completed","conclusion":"success"}`, i, sha(i))
}

func releaseItem(i int) string {
	return fmt.Sprintf(`{"tag_name":"v%d","target_commitish":"%s","published_at":"2026-01-02T00:00:00Z"}`, i, sha(i))
}

func deploymentItem(i int) string {
	return fmt.Sprintf(`{"id":%d,"sha":"%s","environment":"prod"}`, i, sha(i))
}

// fakeRepo answers every REST and GraphQL read for a repository with prs open
// pull requests and returns the number of HTTP requests it received.
func fakeRepo(t *testing.T, prs int) (*httptest.Server, *int) {
	t.Helper()
	calls := 0
	server := httptest.NewServer(fakeRepoHandler(t, prs, &calls))
	return server, &calls
}

// fakeRepoHandler is fakeRepo's transport without the server, so capture tests
// can wrap it with different repository metadata and count selected reads.
func fakeRepoHandler(t *testing.T, prs int, calls *int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		*calls = *calls + 1
		w.Header().Set("Content-Type", "application/json")
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		switch {
		case r.URL.Path == "/repos/acme/widgets":
			_, _ = w.Write([]byte(`{"default_branch":"main"}`))
		case r.URL.Path == "/repos/acme/widgets/branches":
			_, _ = w.Write([]byte(pageString(page, prs, branchItem)))
		case r.URL.Path == "/repos/acme/widgets/commits":
			_, _ = w.Write([]byte(pageString(page, prs, commitItem)))
		case r.URL.Path == "/repos/acme/widgets/issues":
			if r.URL.Query().Get("state") != "open" {
				t.Errorf("lost issue filter: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(pageString(page, prs+1, issueItem)))
		case r.URL.Path == "/repos/acme/widgets/pulls":
			if r.URL.Query().Get("state") != "open" {
				t.Errorf("lost pull request filter: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(pageString(page, prs, pullItem)))
		case r.URL.Path == "/repos/acme/widgets/actions/runs":
			items := pageList(page, prs, runItem)
			_, _ = fmt.Fprintf(w, `{"total_count":%d,"workflow_runs":[%s]}`, prs, strings.Join(items, ","))
		case strings.HasPrefix(r.URL.Path, "/repos/acme/widgets/actions/runs/") && strings.HasSuffix(r.URL.Path, "/jobs"):
			// The fake's runs all completed successfully, so their jobs are
			// two successes: a fake whose jobs contradicted the run list
			// would hide a mapping bug behind a lying shape.
			_, _ = w.Write([]byte(`{"total_count":2,"jobs":[` +
				`{"name":"build","status":"completed","conclusion":"success"},` +
				`{"name":"test","status":"completed","conclusion":"success"}]}`))
		case r.URL.Path == "/repos/acme/widgets/releases":
			_, _ = w.Write([]byte(pageString(page, 150, releaseItem)))
		case r.URL.Path == "/repos/acme/widgets/deployments":
			_, _ = w.Write([]byte(pageString(page, 100, deploymentItem)))
		case r.URL.Path == "/graphql":
			var payload struct {
				Query     string `json:"query"`
				Variables struct {
					Owner string `json:"owner"`
					Name  string `json:"name"`
				} `json:"variables"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload.Variables.Owner != "acme" || payload.Variables.Name != "widgets" {
				t.Errorf("lost variables: %+v", payload.Variables)
			}
			var aliases []string
			pattern := regexp.MustCompile(`p(\d+):pullRequest\(number:(\d+)\)`)
			for _, m := range pattern.FindAllStringSubmatch(payload.Query, -1) {
				n, _ := strconv.Atoi(m[2])
				// A real GraphQL response carries only selected fields. The
				// fake must do the same, or a typo in pullDetailQuery is
				// repaired by the response and passes CI until the first
				// live read of a repository with an open PR.
				// number is only returned when the alias selected it. A real
				// GraphQL response has no fields the query forgot to ask for,
				// and fetchPullDetails compares number with the REST PR.
				withNumber := strings.Contains(payload.Query, m[0]+"{number")
				aliases = append(aliases, fmt.Sprintf(`"p%s":%s`, m[1], fakePullDetail(payload.Query, n, withNumber)))
			}
			_, _ = fmt.Fprintf(w, `{"data":{"repository":{%s}}}`, strings.Join(aliases, ","))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}
}

// fakePullDetail builds one GraphQL PR payload from the fields the query
// actually selected. GitHub answers the same way: an unselected field is
// absent, never guessed. That is what lets the existing end-to-end assertions
// catch a wrong selection instead of silently reading a fake-filled value.
func fakePullDetail(query string, number int, withNumber bool) string {
	var fields []string
	if withNumber {
		fields = append(fields, fmt.Sprintf(`"number":%d`, number))
	}
	if strings.Contains(query, "headRefOid") {
		fields = append(fields, fmt.Sprintf(`"headRefOid":"%s"`, sha(1000+number)))
	}
	if strings.Contains(query, "headRefName") {
		fields = append(fields, fmt.Sprintf(`"headRefName":"flow-%d"`, number))
	}
	if strings.Contains(query, "baseRefName") {
		fields = append(fields, `"baseRefName":"main"`)
	}
	if strings.Contains(query, "reviews(first:") {
		fields = append(fields, fakeReviews(query))
	}
	if strings.Contains(query, "closingIssuesReferences(first:") {
		fields = append(fields, fakeClosingIssues(query, number))
	}
	if strings.Contains(query, "headRef{target{") {
		fields = append(fields, fakeHeadRef(query, number))
	}
	return "{" + strings.Join(fields, ",") + "}"
}

// fakeReviews answers the review selection, including the deleted reviewer
// whose author is null. Both node fields are omitted unless selected.
func fakeReviews(query string) string {
	withAuthor := strings.Contains(query, "author{login}")
	withState := strings.Contains(query, "state}")
	node := func(login, state string) string {
		var parts []string
		if withAuthor {
			if login == "" {
				parts = append(parts, `"author":null`)
			} else {
				parts = append(parts, fmt.Sprintf(`"author":{"login":%q}`, login))
			}
		}
		if withState {
			parts = append(parts, fmt.Sprintf(`"state":%q`, state))
		}
		return "{" + strings.Join(parts, ",") + "}"
	}
	fields := []string{fmt.Sprintf(`"nodes":[%s]`, strings.Join([]string{
		node("sage", "APPROVED"), node("", "COMMENTED"),
	}, ","))}
	if strings.Contains(query, "pageInfo{hasNextPage endCursor}") {
		fields = append(fields, `"pageInfo":{"hasNextPage":false,"endCursor":""}`)
	}
	return `"reviews":{` + strings.Join(fields, ",") + `}`
}

// fakeClosingIssues answers the closing-reference selection. The fake only
// returns the fields the query selected, so dropping nodes{number} or the
// connection itself leaves no link for the snapshot to pick up.
func fakeClosingIssues(query string, number int) string {
	var fields []string
	if strings.Contains(query, "nodes{number}") {
		fields = append(fields, fmt.Sprintf(`"nodes":[{"number":%d}]`, issueNumber(number)))
	}
	if strings.Contains(query, "pageInfo{hasNextPage endCursor}") {
		fields = append(fields, `"pageInfo":{"hasNextPage":false,"endCursor":""}`)
	}
	return `"closingIssuesReferences":{` + strings.Join(fields, ",") + `}`
}

// fakeHeadRef answers the head-commit selection. The oid is returned wherever
// asked, including the follow-up where the join verifies commit identity.
func fakeHeadRef(query string, number int) string {
	target := []string{}
	if strings.Contains(query, "target{oid") || strings.Contains(query, "Commit{oid") {
		target = append(target, fmt.Sprintf(`"oid":"%s"`, sha(1000+number)))
	}
	if strings.Contains(query, "committedDate") {
		target = append(target, `"committedDate":"2026-01-03T00:00:00Z"`)
	}
	if strings.Contains(query, "author{name}") {
		target = append(target, `"author":{"name":"robin"}`)
	}
	if strings.Contains(query, "parents(first:") {
		parents := []string{fmt.Sprintf(`"nodes":[{"oid":"%s"}]`, sha(number))}
		if strings.Contains(query, "pageInfo{hasNextPage endCursor}") {
			parents = append(parents, `"pageInfo":{"hasNextPage":false,"endCursor":""}`)
		}
		target = append(target, `"parents":{`+strings.Join(parents, ",")+`}`)
	}
	return `"headRef":{"target":{` + strings.Join(target, ",") + `}}`
}

// pageString builds one page of pageSize JSON items for item indexes that
// belong to that page, up to total items.
func pageString(page, total int, item func(int) string) string {
	items := pageList(page, total, item)
	return "[" + strings.Join(items, ",") + "]"
}

func pageList(page, total int, item func(int) string) []string {
	var out []string
	first := (page - 1) * pageSize
	for i := first + 1; i <= min(total, first+pageSize); i++ {
		out = append(out, item(i))
	}
	return out
}

func TestBuildSnapshotFiveHundredPullRequests(t *testing.T) {
	server, calls := fakeRepo(t, 500)
	defer server.Close()
	c, _ := NewClient("test-token")
	c.baseURL = server.URL
	snap, err := c.BuildSnapshot(context.Background(), DefaultSnapshotOptions("acme", "widgets"))
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Branches) != 500 || len(snap.Commits) != 1000 || len(snap.Issues) != 501 ||
		len(snap.PullRequests) != 500 || len(snap.Runs) != 500 || len(snap.Releases) != 150 ||
		len(snap.Deployments) != 100 {
		t.Fatalf("counts: %d branches, %d commits, %d issues, %d PRs, %d runs, %d releases, %d deployments",
			len(snap.Branches), len(snap.Commits), len(snap.Issues), len(snap.PullRequests),
			len(snap.Runs), len(snap.Releases), len(snap.Deployments))
	}
	if snap.Issues[2].Number != issueNumber(3) || snap.Issues[2].ClosedBy != 3 {
		t.Fatalf("closing reference not recorded: %+v", snap.Issues[2])
	}
	// The fake's extra issue has no closing reference in any PR detail.
	if snap.Issues[500].ClosedBy != 0 {
		t.Fatalf("unreferenced issue was linked: %+v", snap.Issues[500])
	}
	pr := snap.PullRequests[2]
	if pr.Number != 3 || len(pr.Reviews) != 2 || pr.Reviews[0].Author != "sage" || pr.Reviews[0].State != "approved" {
		t.Fatalf("reviews not mapped: %+v", pr)
	}
	if pr.Reviews[1].Author != "" {
		t.Fatalf("deleted reviewer not handled: %+v", pr.Reviews[1])
	}
	var tips, linked int
	for _, cm := range snap.Commits {
		if cm.Branch == "flow-7" {
			tips++
			if cm.SHA == sha(1007) && len(cm.Parents) == 1 && cm.Parents[0] == sha(7) &&
				cm.Author == "robin" && !cm.Time.IsZero() {
				linked++
			}
		}
	}
	if tips != 1 || linked != 1 {
		t.Fatalf("pull-request head anchor wrong: %d tips, %d linked", tips, linked)
	}
	// Six issue pages join the previous 49 requests; details still share the
	// same batched GraphQL queries rather than adding a request per PR. The
	// ten job pages for the ten most recent runs ride the same plan.
	// The most recent run records its two-job list; the run past the job
	// window keeps a nil list rather than a borrowed one.
	if len(snap.Runs[0].Jobs) != 2 || snap.Runs[0].Jobs[0].Name != "build" || snap.Runs[0].Jobs[1].Name != "test" {
		t.Fatalf("run jobs not recorded: %+v", snap.Runs[0].Jobs)
	}
	if snap.Runs[10].Jobs != nil {
		t.Fatalf("run past the job window recorded a job list: %+v", snap.Runs[10].Jobs)
	}
	if *calls != 65 || *calls >= 300 {
		t.Fatalf("requests=%d, want 65 and under 300", *calls)
	}
	if snap.Now.IsZero() {
		t.Fatal("missing snapshot time")
	}
}

// One live read records the job lists of the ten most recent runs: mixed
// outcomes land with their real names, statuses and conclusions, a run read
// with an empty list records that emptiness explicitly, and a run past the
// window keeps a nil list. The job reads are bounded by the window, never by
// the run count.
func TestBuildSnapshotRecordsRunJobs(t *testing.T) {
	inner, _ := fakeRepo(t, 0)
	defer inner.Close()
	innerHandler := inner.Config.Handler
	const runs = 12
	run := func(id int, status, conclusion string) string {
		return fmt.Sprintf(`{"id":%d,"head_sha":"%s","status":%q,"conclusion":%q}`, id, sha(id), status, conclusion)
	}
	list := []string{
		run(1, "completed", "failure"),
		run(2, "in_progress", ""),
		run(3, "completed", "success"),
	}
	for id := 4; id <= runs; id++ {
		list = append(list, run(id, "completed", "success"))
	}
	jobsByID := map[int]string{
		1: `{"total_count":2,"jobs":[` +
			`{"name":"build","status":"completed","conclusion":"success"},` +
			`{"name":"test","status":"completed","conclusion":"failure"}]}`,
		2: `{"total_count":2,"jobs":[` +
			`{"name":"check","status":"completed","conclusion":"success"},` +
			`{"name":"watch","status":"in_progress","conclusion":null}]}`,
		3: `{"total_count":0,"jobs":[]}`,
	}
	defaultJobs := `{"total_count":1,"jobs":[{"name":"check","status":"completed","conclusion":"success"}]}`
	jobReads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/repos/acme/widgets/actions/runs":
			_, _ = fmt.Fprintf(w, `{"total_count":%d,"workflow_runs":[%s]}`, runs, strings.Join(list, ","))
		case strings.HasPrefix(r.URL.Path, "/repos/acme/widgets/actions/runs/") && strings.HasSuffix(r.URL.Path, "/jobs"):
			jobReads++
			id, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/repos/acme/widgets/actions/runs/"), "/jobs"))
			if body, ok := jobsByID[id]; ok {
				_, _ = w.Write([]byte(body))
				return
			}
			_, _ = w.Write([]byte(defaultJobs))
		default:
			innerHandler.ServeHTTP(w, r)
		}
	}))
	defer server.Close()
	c, _ := NewClient("test-token")
	c.baseURL = server.URL
	opts := DefaultSnapshotOptions("acme", "widgets")
	snap, err := c.BuildSnapshot(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Runs) != runs {
		t.Fatalf("runs=%d, want %d", len(snap.Runs), runs)
	}
	// A failed run names its failing job; the strip keeps both outcomes.
	first := snap.Runs[0].Jobs
	if len(first) != 2 || first[0].Name != "build" || first[0].Conclusion != "success" ||
		first[1].Name != "test" || first[1].Conclusion != "failure" || first[1].Failed() != true {
		t.Fatalf("mixed outcomes not recorded: %+v", first)
	}
	// A running run holds a finished job beside an unfinished one, with no
	// conclusion invented for the unfinished job.
	second := snap.Runs[1].Jobs
	if len(second) != 2 || second[0].Status != "completed" || second[1].Status != "in_progress" ||
		second[1].Conclusion != "" || second[1].Running() != true {
		t.Fatalf("running job not recorded as unfinished: %+v", second)
	}
	// A read run with no jobs records an explicitly empty list.
	if snap.Runs[2].Jobs == nil || len(snap.Runs[2].Jobs) != 0 {
		t.Fatalf("empty job list not recorded explicitly: %+v", snap.Runs[2].Jobs)
	}
	// The window ends at the tenth run: the last run read keeps a list, the
	// first run past it keeps nil, and the read never asked for more.
	if snap.Runs[9].Jobs == nil {
		t.Fatal("the tenth run, inside the window, has no job list")
	}
	if snap.Runs[10].Jobs != nil || snap.Runs[11].Jobs != nil {
		t.Fatalf("runs past the window recorded job lists: %+v %+v", snap.Runs[10].Jobs, snap.Runs[11].Jobs)
	}
	if jobReads != opts.MaxRunsWithJobs {
		t.Fatalf("job reads=%d, want the %d-run window", jobReads, opts.MaxRunsWithJobs)
	}
}

// The per-run job cap and the page's own confirmation are hard bounds: a run
// with more jobs than the cap, a page short of its recorded total, or a full
// page with no matching total is one clear error with no snapshot, never a
// partial strip presented as complete.
func TestBuildSnapshotRunJobReadsFailClean(t *testing.T) {
	fullPage := "[" + strings.Repeat(`{"name":"job","status":"completed","conclusion":"success"},`, pageSize-1) +
		`{"name":"job","status":"completed","conclusion":"success"}]`
	for _, tc := range []struct {
		name, body, wantErr string
	}{
		{"over-cap", `{"total_count":101,"jobs":` + fullPage + "}", "exceed the 100-job limit"},
		{"short of total", `{"total_count":5,"jobs":[{"name":"build","status":"completed","conclusion":"success"}]}`, "unconfirmed job list"},
		{"full page without count", `{"total_count":0,"jobs":` + fullPage + "}", "unconfirmed job list"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, _ := fakeRepo(t, 5)
			defer server.Close()
			wrapped := server.Config.Handler
			server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/repos/acme/widgets/actions/runs/") && strings.HasSuffix(r.URL.Path, "/jobs") {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(tc.body))
					return
				}
				wrapped.ServeHTTP(w, r)
			})
			c, _ := NewClient("test-token")
			c.baseURL = server.URL
			snap, err := c.BuildSnapshot(context.Background(), DefaultSnapshotOptions("acme", "widgets"))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("accepted an unbounded job read: %v", err)
			}
			if !snap.Now.IsZero() || len(snap.Runs) != 0 {
				t.Fatal("partial snapshot returned")
			}
		})
	}
}

// An active repository with more than the old 500-item commit and run caps is
// an ordinary case, not an error: the raised default limits let it render.
func TestBuildSnapshotAboveOldCaps(t *testing.T) {
	const many = 600 // above the old 500 cap, below the new 5000 cap
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		switch {
		case r.URL.Path == "/repos/acme/widgets":
			_, _ = w.Write([]byte(`{"default_branch":"main"}`))
		case strings.HasSuffix(r.URL.Path, "/branches"):
			_, _ = w.Write([]byte(`[{"name":"main"}]`))
		case strings.HasSuffix(r.URL.Path, "/commits"):
			_, _ = w.Write([]byte(pageString(page, many, commitItem)))
		case strings.HasSuffix(r.URL.Path, "/issues"), strings.HasSuffix(r.URL.Path, "/pulls"):
			_, _ = w.Write([]byte(`[]`))
		case strings.HasSuffix(r.URL.Path, "/actions/runs"):
			_, _ = fmt.Fprintf(w, `{"total_count":%d,"workflow_runs":%s}`, many, pageString(page, many, runItem))
		case strings.HasPrefix(r.URL.Path, "/repos/acme/widgets/actions/runs/") && strings.HasSuffix(r.URL.Path, "/jobs"):
			_, _ = w.Write([]byte(`{"total_count":1,"jobs":[{"name":"check","status":"completed","conclusion":"success"}]}`))
		case strings.HasSuffix(r.URL.Path, "/releases"), strings.HasSuffix(r.URL.Path, "/deployments"):
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	c, _ := NewClient("test-token")
	c.baseURL = server.URL
	snap, err := c.BuildSnapshot(context.Background(), DefaultSnapshotOptions("acme", "widgets"))
	if err != nil {
		t.Fatalf("ordinary active repository rejected: %v", err)
	}
	if len(snap.Commits) != many || len(snap.Runs) != many {
		t.Fatalf("counts: %d commits, %d runs, want %d each", len(snap.Commits), len(snap.Runs), many)
	}
}

func TestBuildSnapshotFailsClean(t *testing.T) {
	for _, mode := range []string{"runs", "jobs", "graphql", "branches", "issues", "format"} {
		t.Run(mode, func(t *testing.T) {
			server, _ := fakeRepo(t, 5)
			defer server.Close()
			wrapped := server.Config.Handler
			server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "runs":
					if strings.HasSuffix(r.URL.Path, "/actions/runs") {
						w.WriteHeader(http.StatusForbidden)
						return
					}
				case "jobs":
					// A failed job-list read fails the whole snapshot: a
					// run without its jobs must not masquerade as one whose
					// job list was read and found empty.
					if strings.HasPrefix(r.URL.Path, "/repos/acme/widgets/actions/runs/") && strings.HasSuffix(r.URL.Path, "/jobs") {
						w.WriteHeader(http.StatusForbidden)
						return
					}
				case "graphql":
					if r.URL.Path == "/graphql" {
						_, _ = w.Write([]byte(`{"errors":[{"message":"boom"}]}`))
						return
					}
				case "branches":
					if strings.HasSuffix(r.URL.Path, "/branches") {
						// Every page is full, so the page budget runs out
						// before a short page can confirm the end.
						_, _ = w.Write([]byte(pageString(1, 100, func(i int) string { return `{"name":"b"}` })))
						return
					}
				case "issues":
					if strings.HasSuffix(r.URL.Path, "/issues") {
						w.WriteHeader(http.StatusForbidden)
						return
					}
				case "format":
					if strings.HasSuffix(r.URL.Path, "/commits") {
						_, _ = w.Write([]byte(`[{"sha":42}]`))
						return
					}
				}
				wrapped.ServeHTTP(w, r)
			})
			c, _ := NewClient("test-token")
			c.baseURL = server.URL
			snap, err := c.BuildSnapshot(context.Background(), DefaultSnapshotOptions("acme", "widgets"))
			if err == nil {
				t.Fatalf("accepted failure mode %q", mode)
			}
			if !snap.Now.IsZero() || len(snap.Commits)+len(snap.Issues)+len(snap.PullRequests)+len(snap.Runs)+
				len(snap.Branches)+len(snap.Releases)+len(snap.Deployments) != 0 {
				t.Fatalf("partial snapshot returned: %d objects", len(snap.Commits)+len(snap.Issues)+len(snap.PullRequests)+
					len(snap.Runs)+len(snap.Branches)+len(snap.Releases)+len(snap.Deployments))
			}
		})
	}
}

// Every list enforces its item cap: whatever its page budget allows, more
// items than the cap is an error, and no partial snapshot is returned.
func TestBuildSnapshotItemCaps(t *testing.T) {
	// Derive each cap from the defaults so this still guards the real limits
	// when they are raised, instead of silently testing stale numbers.
	opts := DefaultSnapshotOptions("acme", "widgets")
	for _, tt := range []struct {
		path, probe string
		cap         int
		item        func(int) string
	}{
		{"/branches", "branch", opts.MaxBranches, branchItem},
		{"/commits", "commit", opts.MaxCommits, commitItem},
		{"/issues", "issue", opts.MaxIssues, issueItem},
		{"/pulls", "pull request", opts.MaxPullRequests, pullItem},
		{"/actions/runs", "run", opts.MaxRuns, runItem},
		{"/releases", "release", opts.MaxReleases, releaseItem},
		{"/deployments", "deployment", opts.MaxDeployments, deploymentItem},
	} {
		t.Run(strings.Trim(tt.path, "/"), func(t *testing.T) {
			server, _ := fakeRepo(t, 5)
			defer server.Close()
			wrapped := server.Config.Handler
			server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, tt.path) {
					page, _ := strconv.Atoi(r.URL.Query().Get("page"))
					// One item past the cap: the last allowed page is short,
					// so the page budget itself is not what fails.
					body := pageString(page, tt.cap+1, tt.item)
					if tt.path == "/actions/runs" {
						body = fmt.Sprintf(`{"total_count":%d,"workflow_runs":%s}`, tt.cap+1, body)
					}
					_, _ = w.Write([]byte(body))
					return
				}
				wrapped.ServeHTTP(w, r)
			})
			c, _ := NewClient("test-token")
			c.baseURL = server.URL
			snap, err := c.BuildSnapshot(context.Background(), DefaultSnapshotOptions("acme", "widgets"))
			if err == nil || !strings.Contains(err.Error(), tt.probe) {
				t.Fatalf("accepted over-cap %s read: %v", tt.path, err)
			}
			if !snap.Now.IsZero() || len(snap.Commits)+len(snap.Issues)+len(snap.PullRequests)+len(snap.Runs)+
				len(snap.Branches)+len(snap.Releases)+len(snap.Deployments) != 0 {
				t.Fatal("partial snapshot returned")
			}
		})
	}
}

// Keep the worst-case request count in the docs checkable: 1 meta + 11 branch
// + 51 commit + 11 issue + 11 pull + 51 run + 11 release + 11 deployment pages
// + 80 detail queries (a first page and one follow-up for each of 40 batches)
// + 10 job pages for the ten most recent runs, all inside the 250-request
// budget. Closing references ride in those detail queries; they add no request
// per PR, and each run's job list is one page bounded by the per-run cap.
func TestDefaultPlannedRequests(t *testing.T) {
	opts := DefaultSnapshotOptions("acme", "widgets")
	if got := opts.plannedRequests(); got != 248 {
		t.Fatalf("plannedRequests()=%d, want the documented 248", got)
	}
	if 248 > opts.MaxRequests {
		t.Fatal("documented plan exceeds the default request budget")
	}
}

// paginatedDetails answers the first detail page and one follow-up for a
// single pull request with more than 100 reviews, more than three parents and
// two closing issue references. The values are generated, but they are served
// through the same GraphQL seam a real read uses: the test proves the client
// reads and joins both pages of each connection.
func paginatedDetails(t *testing.T, calls, graphqlCalls *int, mode string) http.HandlerFunc {
	t.Helper()
	inner, _ := fakeRepo(t, 1)
	defer inner.Close()
	serverHandler := inner.Config.Handler
	return func(w http.ResponseWriter, r *http.Request) {
		*calls++
		if r.URL.Path != "/graphql" {
			serverHandler.ServeHTTP(w, r)
			return
		}
		*graphqlCalls++
		body, _ := io.ReadAll(r.Body)
		query := string(body)
		follow := strings.Contains(query, "after:")
		if mode == "follow-error" && follow {
			_, _ = w.Write([]byte(`{"errors":[{"message":"boom"}]}`))
			return
		}
		var reviews, parents, closings []string
		more := !follow
		closingMore := !follow || mode == "closing-third-page"
		first, last := 0, 99
		parentFirst, parentLast := 0, 3
		if follow {
			first, last = 100, 102
			parentFirst, parentLast = 4, 5
			more = mode == "third-page"
			closings = []string{fmt.Sprintf(`{"number":%d}`, issueNumber(2))}
		} else {
			closings = []string{fmt.Sprintf(`{"number":%d}`, issueNumber(1))}
		}
		for i := first; i <= last; i++ {
			reviews = append(reviews, fmt.Sprintf(`{"author":{"login":"reviewer-%d"},"state":"APPROVED"}`, i))
		}
		for i := parentFirst; i <= parentLast; i++ {
			parents = append(parents, fmt.Sprintf(`{"oid":"%s"}`, sha(2000+i)))
		}
		cursor := ""
		if more {
			cursor = "next"
		}
		closingField := ""
		if strings.Contains(query, "closingIssuesReferences(first:") {
			closingField = fmt.Sprintf(`"closingIssuesReferences":{"nodes":[%s],"pageInfo":{"hasNextPage":%t,"endCursor":"closing-next"}},`,
				strings.Join(closings, ","), closingMore)
		}
		// A real GraphQL response carries only fields the query requests.
		// The follow-up handler verifies the head oid before extending its
		// parents, and the first page matches number against the REST PR,
		// so the fake must omit either field when the query forgets to ask
		// for it, or a regression there would pass unnoticed.
		oidField := ""
		if strings.Contains(query, "target{oid") || strings.Contains(query, "Commit{oid") {
			oidField = fmt.Sprintf(`"oid":"%s",`, sha(1001))
		}
		numberField := ""
		if strings.Contains(query, `p0:pullRequest(number:1){number`) {
			numberField = `"number":1,`
		}
		_, _ = fmt.Fprintf(w, `{"data":{"repository":{"p0":{%s`+
			`"reviews":{"nodes":[%s],"pageInfo":{"hasNextPage":%t,"endCursor":"%s"}},`+
			`%s`+
			`"headRef":{"target":{%s"committedDate":"2026-01-03T00:00:00Z","author":{"name":"robin"},`+
			`"parents":{"nodes":[%s],"pageInfo":{"hasNextPage":%t,"endCursor":"%s"}}}}}}}}`,
			numberField, strings.Join(reviews, ","), more, cursor, closingField,
			oidField, strings.Join(parents, ","), more, cursor)
	}
}

// These selections are the contract between pullDetailQuery and
// fetchPullDetails. The field-aware fakes can only catch a wrong field if the
// query asks for it, so pin the text too: a typo, a dropped argument or a
// changed alias fails here instead of on the first live GraphQL read of a
// repository with an open PR.
func TestPullDetailQuerySelections(t *testing.T) {
	prs := []repo.PullRequest{{Number: 7, Head: "flow"}}
	first := pullDetailQuery(prs, nil)
	for _, want := range []string{
		`query($owner:String!,$name:String!){repository(owner:$owner,name:$name){`,
		`p0:pullRequest(number:7){`,
		`p0:pullRequest(number:7){number `,
		`reviews(first:100){nodes{author{login} state} pageInfo{hasNextPage endCursor}}`,
		`closingIssuesReferences(first:100){nodes{number} pageInfo{hasNextPage endCursor}}`,
		`headRef{target{oid ... on Commit{oid committedDate author{name}`,
		`parents(first:100){nodes{oid} pageInfo{hasNextPage endCursor}}`,
	} {
		if !strings.Contains(first, want) {
			t.Errorf("first-page query lost %q:\n%s", want, first)
		}
	}
	if strings.Contains(first, "last:") {
		t.Errorf("reviews and parents must read oldest-first with first:\n%s", first)
	}

	// A follow-up asks only for connections that reported another page, but
	// still needs the head oid so the join can verify commit identity.
	follow := pullDetailQuery(prs, []detailCursor{{
		review: "reviews-cursor", needReview: true,
		closing: "closing-cursor", needClosing: true,
		parent: "parents-cursor", needParent: true,
	}})
	for _, want := range []string{
		`p0:pullRequest(number:7){number `,
		`reviews(first:100,after:"reviews-cursor"){nodes{author{login} state} pageInfo{hasNextPage endCursor}}`,
		`closingIssuesReferences(first:100,after:"closing-cursor"){nodes{number} pageInfo{hasNextPage endCursor}}`,
		`headRef{target{oid ... on Commit{oid parents(first:100,after:"parents-cursor"){nodes{oid} pageInfo{hasNextPage endCursor}}}}}`,
	} {
		if !strings.Contains(follow, want) {
			t.Errorf("follow-up query lost %q:\n%s", want, follow)
		}
	}
}

// A wrong selection must be visible without a token. With the field-aware
// fake, dropping the review "author" or "state" leaves the decoded review
// without that field, dropping the first-page "number" fails the REST/GraphQL
// PR match, and dropping a head anchor selection leaves that anchor absent or
// incomplete. None is repaired by a response that returns unselected fields.
// These are offline proofs that CI catches a wrong field; they are not a live
// capture.
func TestPullDetailFieldTypoIsVisibleLocally(t *testing.T) {
	for _, tc := range []struct {
		name, from, to, wantErr string
		wantEmptyReviewAuthor   bool
		wantEmptyReviewState    bool
		wantNoAnchor            bool
		wantEmptyAuthor         bool
		wantNoParents           bool
		wantNoClosing           bool
	}{
		{name: "review author", from: `author{login}`, to: `author{logn}`, wantEmptyReviewAuthor: true},
		{name: "review state", from: ` state}`, to: ` stat}`, wantEmptyReviewState: true},
		{name: "pull request number", from: `p0:pullRequest(number:1){number `, to: `p0:pullRequest(number:1){`,
			wantErr: "mismatched pull requests"},
		{name: "head commit date", from: ` committedDate`, to: ` commitDate`,
			wantNoAnchor: true},
		{name: "head commit author", from: `author{name}`, to: `author{login}`,
			wantEmptyAuthor: true},
		{name: "head commit parents", from: `parents(first:100)`, to: `parentz(first:100)`,
			wantNoParents: true},
		{name: "closing references", from: `closingIssuesReferences(first:100)`, to: `closingIssuez(first:100)`,
			wantNoClosing: true},
		{name: "closing issue number", from: `closingIssuesReferences(first:100){nodes{number}`,
			to: `closingIssuesReferences(first:100){nodes{numb}`, wantNoClosing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, _ := fakeRepo(t, 1)
			defer server.Close()
			wrapped := server.Config.Handler
			server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/graphql" {
					wrapped.ServeHTTP(w, r)
					return
				}
				var payload struct {
					Query     string         `json:"query"`
					Variables map[string]any `json:"variables"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Errorf("decode GraphQL request: %v", err)
					return
				}
				mutated := strings.Replace(payload.Query, tc.from, tc.to, 1)
				if mutated == payload.Query {
					t.Errorf("query no longer contains %q", tc.from)
				}
				payload.Query = mutated
				body, err := json.Marshal(payload)
				if err != nil {
					t.Errorf("encode GraphQL request: %v", err)
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(body))
				r.ContentLength = int64(len(body))
				wrapped.ServeHTTP(w, r)
			})
			c, _ := NewClient("test-token")
			c.baseURL = server.URL
			snap, err := c.BuildSnapshot(context.Background(), DefaultSnapshotOptions("acme", "widgets"))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("dropped %q was hidden: err=%v", tc.from, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			reviews := snap.PullRequests[0].Reviews
			if len(reviews) == 0 {
				t.Fatalf("field typo lost the reviews: %+v", reviews)
			}
			if tc.wantEmptyReviewAuthor {
				if reviews[0].Author != "" {
					t.Fatalf("dropped %q was hidden by the fake: %+v", tc.from, reviews)
				}
			} else if reviews[0].Author != "sage" {
				t.Fatalf("field typo was hidden by the fake: %+v", reviews)
			}
			if tc.wantEmptyReviewState && reviews[0].State != "" {
				t.Fatalf("dropped %q was hidden by the fake: %+v", tc.from, reviews)
			}
			if !tc.wantEmptyReviewState && reviews[0].State != "approved" {
				t.Fatalf("unexpected review state in %s: %+v", tc.name, reviews)
			}
			if len(snap.Issues) != 2 {
				t.Fatalf("open issues lost in %s: %+v", tc.name, snap.Issues)
			}
			wantClosedBy := 1
			if tc.wantNoClosing {
				wantClosedBy = 0
			}
			if snap.Issues[0].ClosedBy != wantClosedBy {
				t.Fatalf("dropped %q was hidden by the fake: issue=%+v", tc.from, snap.Issues[0])
			}
			if snap.Issues[1].ClosedBy != 0 {
				t.Fatalf("unreferenced issue gained a closing reference: %+v", snap.Issues[1])
			}
			var head *repo.Commit
			for i := range snap.Commits {
				if snap.Commits[i].Branch == "flow-1" {
					head = &snap.Commits[i]
				}
			}
			switch {
			case tc.wantNoAnchor:
				if head != nil {
					t.Fatalf("dropped %q was hidden by the fake: %+v", tc.from, *head)
				}
			case tc.wantEmptyAuthor:
				if head == nil || head.Author != "" {
					t.Fatalf("dropped %q was hidden by the fake: %+v", tc.from, head)
				}
			case tc.wantNoParents:
				if head == nil || len(head.Parents) != 0 {
					t.Fatalf("dropped %q was hidden by the fake: %+v", tc.from, head)
				}
			}
		})
	}
}

// A PR with 103 reviews, six head parents and two closing references reads both
// pages of each connection in order, joins them into complete lists and stays
// inside the documented request plan.
func TestBuildSnapshotPaginatesPullDetails(t *testing.T) {
	for _, mode := range []string{"ok", "follow-error", "third-page", "closing-third-page"} {
		t.Run(mode, func(t *testing.T) {
			var calls, graphqlCalls int
			server := httptest.NewServer(paginatedDetails(t, &calls, &graphqlCalls, mode))
			defer server.Close()
			c, _ := NewClient("test-token")
			c.baseURL = server.URL
			opts := DefaultSnapshotOptions("acme", "widgets")
			snap, err := c.BuildSnapshot(context.Background(), opts)
			if mode != "ok" {
				if err == nil {
					t.Fatal("accepted a failed follow-up page")
				}
				if mode == "closing-third-page" && !strings.Contains(err.Error(), "closing issue references exceed") {
					t.Fatalf("third closing-reference page was not rejected: %v", err)
				}
				if !snap.Now.IsZero() || len(snap.Commits)+len(snap.Issues)+len(snap.PullRequests) != 0 {
					t.Fatal("partial snapshot returned")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			pr := snap.PullRequests[0]
			if len(pr.Reviews) != 103 || pr.Reviews[0].Author != "reviewer-0" ||
				pr.Reviews[99].Author != "reviewer-99" || pr.Reviews[100].Author != "reviewer-100" ||
				pr.Reviews[102].State != "approved" {
				t.Fatalf("reviews not complete: %d, first=%+v last=%+v", len(pr.Reviews), pr.Reviews[0], pr.Reviews[102])
			}
			var head repo.Commit
			for _, cm := range snap.Commits {
				if cm.SHA == sha(1001) {
					head = cm
				}
			}
			if len(head.Parents) != 6 || head.Parents[0] != sha(2000) || head.Parents[5] != sha(2005) {
				t.Fatalf("parents not complete: %+v", head.Parents)
			}
			if len(snap.Issues) != 2 || snap.Issues[0].ClosedBy != 1 || snap.Issues[1].ClosedBy != 1 {
				t.Fatalf("closing references not joined across pages: %+v", snap.Issues)
			}
			if graphqlCalls != 2 {
				t.Fatalf("GraphQL requests=%d, want 2 detail pages", graphqlCalls)
			}
			if calls > opts.plannedRequests() || calls > opts.MaxRequests {
				t.Fatalf("requests=%d over plan %d or budget %d", calls, opts.plannedRequests(), opts.MaxRequests)
			}
		})
	}
}

func TestBuildSnapshotInput(t *testing.T) {
	server, calls := fakeRepo(t, 0)
	defer server.Close()
	c, _ := NewClient("test-token")
	c.baseURL = server.URL
	if _, err := c.BuildSnapshot(context.Background(), DefaultSnapshotOptions("", "widgets")); err == nil {
		t.Fatal("accepted empty owner")
	}
	small := DefaultSnapshotOptions("acme", "widgets")
	small.MaxRequests = 5
	if _, err := c.BuildSnapshot(context.Background(), small); err == nil {
		t.Fatal("accepted plan over budget")
	}
	negative := DefaultSnapshotOptions("acme", "widgets")
	negative.MaxRunsWithJobs = -1
	if _, err := c.BuildSnapshot(context.Background(), negative); err == nil || !strings.Contains(err.Error(), "run-job window") {
		t.Fatalf("accepted a negative job window: %v", err)
	}
	oversized := DefaultSnapshotOptions("acme", "widgets")
	oversized.MaxJobsPerRun = pageSize + 1
	if _, err := c.BuildSnapshot(context.Background(), oversized); err == nil || !strings.Contains(err.Error(), "job limit") {
		t.Fatalf("accepted a per-run job cap no page can honour: %v", err)
	}
	closed := DefaultSnapshotOptions("acme", "widgets")
	closed.MaxRunsWithJobs = 0
	if *calls != 0 {
		t.Fatalf("issued %d requests before validation", *calls)
	}
	// A closed job window is the one new configuration that must read: it
	// reproduces the pre-job snapshot, with every run keeping a nil list.
	snap, err := c.BuildSnapshot(context.Background(), closed)
	if err != nil {
		t.Fatalf("a closed job window must be a valid configuration: %v", err)
	}
	for _, r := range snap.Runs {
		if r.Jobs != nil {
			t.Fatalf("a closed job window recorded jobs: %+v", r.Jobs)
		}
	}
}

// A fork pull request often keeps the base branch's ref for its head
// (contributor/main -> main). GitHub qualifies it with the fork owner in
// head.label; that identity must reach the snapshot so the fork's commit is
// not drawn on the upstream main lane and two forks sharing a ref stay apart.
func TestBuildSnapshotForkPullsKeepIdentity(t *testing.T) {
	detail := func(author string, number, oid int) string {
		return fmt.Sprintf(`{"number":%d,"headRefOid":"%s","headRefName":"main","baseRefName":"main",`+
			`"headRef":{"target":{"oid":"%s","committedDate":"2026-01-03T00:00:00Z","author":{"name":"%s"},`+
			`"parents":{"nodes":[{"oid":"%s"}]}}},"reviews":{"nodes":[]}}`,
			number, sha(oid), sha(oid), author, sha(1))
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/repos/acme/widgets":
			_, _ = w.Write([]byte(`{"default_branch":"main"}`))
		case strings.HasSuffix(r.URL.Path, "/branches"):
			_, _ = w.Write([]byte(`[{"name":"main"}]`))
		case strings.HasSuffix(r.URL.Path, "/commits"):
			_, _ = fmt.Fprintf(w, `[{"sha":"%s","commit":{"author":{"name":"robin","date":"2026-01-01T00:00:00Z"}}}]`, sha(1))
		case strings.HasSuffix(r.URL.Path, "/issues"):
			_, _ = w.Write([]byte(`[]`))
		case strings.HasSuffix(r.URL.Path, "/pulls"):
			_, _ = fmt.Fprintf(w, `[
				{"number":1,"state":"open","head":{"ref":"main","label":"alice:main","repo":{"full_name":"alice/widgets"}},"base":{"ref":"main"}},
				{"number":2,"state":"open","head":{"ref":"main","label":"bob:main","repo":{"full_name":"bob/widgets"}},"base":{"ref":"main"}}
			]`)
		case strings.HasSuffix(r.URL.Path, "/actions/runs"):
			_, _ = w.Write([]byte(`{"total_count":0,"workflow_runs":[]}`))
		case strings.HasSuffix(r.URL.Path, "/releases"),
			strings.HasSuffix(r.URL.Path, "/deployments"):
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/graphql":
			_, _ = fmt.Fprintf(w, `{"data":{"repository":{"p0":%s,"p1":%s}}}`,
				detail("alice", 1, 101), detail("bob", 2, 102))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	c, _ := NewClient("test-token")
	c.baseURL = server.URL
	snap, err := c.BuildSnapshot(context.Background(), DefaultSnapshotOptions("acme", "widgets"))
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"alice:main", "bob:main"} {
		if got := snap.PullRequests[i].Head; got != want {
			t.Fatalf("PR %d head lane=%q, want %q", i+1, got, want)
		}
		if got := snap.PullRequests[i].Base; got != "main" {
			t.Fatalf("PR %d base=%q, want main", i+1, got)
		}
	}
	lanes := map[string]int{}
	for _, cm := range snap.Commits {
		lanes[cm.Branch]++
	}
	if len(snap.Commits) != 3 || lanes["alice:main"] != 1 || lanes["bob:main"] != 1 || lanes["main"] != 1 {
		t.Fatalf("commits=%d, lanes=%v, want one on alice:main, bob:main and main", len(snap.Commits), lanes)
	}
}

// GitHub returns draft releases to callers with push access. A draft was never
// published, so it must not appear as a release. The draft is filtered before
// the cap check and must not consume the release budget.
func TestBuildSnapshotSkipsDraftReleases(t *testing.T) {
	server, _ := fakeRepo(t, 2)
	defer server.Close()
	wrapped := server.Config.Handler
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases") {
			_, _ = fmt.Fprintf(w, `[
				{"tag_name":"v1","target_commitish":"%s","published_at":"2026-01-02T00:00:00Z","draft":false},
				{"tag_name":"v2-draft","target_commitish":"%s","published_at":null,"draft":true}
			]`, sha(1), sha(2))
			return
		}
		wrapped.ServeHTTP(w, r)
	})
	c, _ := NewClient("test-token")
	c.baseURL = server.URL
	opts := DefaultSnapshotOptions("acme", "widgets")
	opts.MaxReleases = 1 // one published release plus a draft fits only if drafts are filtered first
	snap, err := c.BuildSnapshot(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Releases) != 1 || snap.Releases[0].Tag != "v1" {
		t.Fatalf("releases=%+v, want only the published v1", snap.Releases)
	}
}

// GitHub serves pull requests from the issues endpoint and numbers both from
// one sequence. Only a closingIssuesReferences node may name a pull request as
// an issue's closer: neither a PR-shaped list item, a closed issue, an issue
// number near a PR number, nor a reference to a number that is not an open
// issue may become a link.
func TestBuildSnapshotReadsOpenIssuesAndRealClosingReferences(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/repos/acme/widgets":
			_, _ = w.Write([]byte(`{"default_branch":"main"}`))
		case strings.HasSuffix(r.URL.Path, "/branches"):
			_, _ = w.Write([]byte(`[{"name":"main"}]`))
		case strings.HasSuffix(r.URL.Path, "/commits"):
			_, _ = fmt.Fprintf(w, `[{"sha":"%s","commit":{"author":{"name":"robin","date":"2026-01-01T00:00:00Z"}}}]`, sha(1))
		case strings.HasSuffix(r.URL.Path, "/issues"):
			if r.URL.Query().Get("state") != "open" {
				t.Errorf("lost issue filter: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`[
				{"number":101,"title":"feed me","state":"open"},
				{"number":102,"title":"quiet","state":"open"},
				{"number":103,"title":"done","state":"closed"},
				{"number":104,"title":"a pull request","state":"open","pull_request":{"url":"https://api.github.com/repos/acme/widgets/pulls/104"}}
			]`))
		case strings.HasSuffix(r.URL.Path, "/pulls"):
			_, _ = w.Write([]byte(`[{"number":7,"state":"open","head":{"ref":"flow-7"},"base":{"ref":"main"}}]`))
		case strings.HasSuffix(r.URL.Path, "/actions/runs"):
			_, _ = w.Write([]byte(`{"total_count":0,"workflow_runs":[]}`))
		case strings.HasSuffix(r.URL.Path, "/releases"), strings.HasSuffix(r.URL.Path, "/deployments"):
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/graphql":
			body, _ := io.ReadAll(r.Body)
			query := string(body)
			if !strings.Contains(query, "closingIssuesReferences(first:100){nodes{number}") {
				t.Errorf("query did not select closing references: %s", query)
			}
			_, _ = w.Write([]byte(`{"data":{"repository":{"p0":{
				"number":7,
				"reviews":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":""}},
				"closingIssuesReferences":{"nodes":[{"number":101},{"number":7},{"number":999},null],"pageInfo":{"hasNextPage":false,"endCursor":""}},
				"headRef":{"target":{"oid":"` + sha(1007) + `","committedDate":"2026-01-03T00:00:00Z","author":{"name":"robin"},
					"parents":{"nodes":[{"oid":"` + sha(1) + `"}],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}
			}}}}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	c, _ := NewClient("test-token")
	c.baseURL = server.URL
	opts := DefaultSnapshotOptions("acme", "widgets")
	snap, err := c.BuildSnapshot(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Issues) != 2 {
		t.Fatalf("issues=%+v, want only the two open non-PR items", snap.Issues)
	}
	if snap.Issues[0].Number != 101 || snap.Issues[0].Title != "feed me" || snap.Issues[0].State != "open" || snap.Issues[0].ClosedBy != 7 {
		t.Fatalf("feeding issue not recorded from its closing reference: %+v", snap.Issues[0])
	}
	if snap.Issues[1].Number != 102 || snap.Issues[1].ClosedBy != 0 {
		t.Fatalf("unlinked issue gained a link: %+v", snap.Issues[1])
	}
	if calls > opts.plannedRequests() || calls > opts.MaxRequests {
		t.Fatalf("requests=%d over plan %d or budget %d", calls, opts.plannedRequests(), opts.MaxRequests)
	}
}

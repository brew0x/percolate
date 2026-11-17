package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		switch {
		case r.URL.Path == "/repos/acme/widgets":
			_, _ = w.Write([]byte(`{"default_branch":"main"}`))
		case r.URL.Path == "/repos/acme/widgets/branches":
			_, _ = w.Write([]byte(pageString(page, prs, branchItem)))
		case r.URL.Path == "/repos/acme/widgets/commits":
			_, _ = w.Write([]byte(pageString(page, prs, commitItem)))
		case r.URL.Path == "/repos/acme/widgets/pulls":
			if r.URL.Query().Get("state") != "open" {
				t.Errorf("lost pull request filter: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(pageString(page, prs, pullItem)))
		case r.URL.Path == "/repos/acme/widgets/actions/runs":
			items := pageList(page, prs, runItem)
			_, _ = fmt.Fprintf(w, `{"total_count":%d,"workflow_runs":[%s]}`, prs, strings.Join(items, ","))
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
				aliases = append(aliases, fmt.Sprintf(
					`"p%s":{"number":%d,"headRefOid":"%s","headRefName":"flow-%d","baseRefName":"main",`+
						`"headRef":{"target":{"oid":"%s","committedDate":"2026-01-03T00:00:00Z","author":{"name":"robin"},"parents":{"nodes":[{"oid":"%s"}]}}},`+
						`"reviews":{"nodes":[{"author":{"login":"sage"},"state":"APPROVED"},{"author":null,"state":"COMMENTED"}]}}`,
					m[1], n, sha(1000+n), n, sha(1000+n), sha(n)))
			}
			_, _ = fmt.Fprintf(w, `{"data":{"repository":{%s}}}`, strings.Join(aliases, ","))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	return server, &calls
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
	if len(snap.Branches) != 500 || len(snap.Commits) != 1000 || len(snap.PullRequests) != 500 ||
		len(snap.Runs) != 500 || len(snap.Releases) != 150 || len(snap.Deployments) != 100 {
		t.Fatalf("counts: %d branches, %d commits, %d PRs, %d runs, %d releases, %d deployments",
			len(snap.Branches), len(snap.Commits), len(snap.PullRequests), len(snap.Runs), len(snap.Releases), len(snap.Deployments))
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
			if cm.SHA == sha(1007) && len(cm.Parents) == 1 && cm.Parents[0] == sha(7) {
				linked++
			}
		}
	}
	if tips != 1 || linked != 1 {
		t.Fatalf("pull-request head anchor wrong: %d tips, %d linked", tips, linked)
	}
	if *calls != 49 || *calls >= 300 {
		t.Fatalf("requests=%d, want 49 and under 300", *calls)
	}
	if snap.Now.IsZero() {
		t.Fatal("missing snapshot time")
	}
}

func TestBuildSnapshotFailsClean(t *testing.T) {
	for _, mode := range []string{"runs", "graphql", "branches", "format"} {
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
				case "graphql":
					if r.URL.Path == "/graphql" {
						_, _ = w.Write([]byte(`{"errors":[{"message":"boom"}]}`))
						return
					}
				case "branches":
					if strings.HasSuffix(r.URL.Path, "/branches") {
						// Six full pages exceed the branch cap of six reads.
						_, _ = w.Write([]byte(pageString(1, 100, func(i int) string { return `{"name":"b"}` })))
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
			if !snap.Now.IsZero() || len(snap.Commits)+len(snap.PullRequests)+len(snap.Runs)+
				len(snap.Branches)+len(snap.Releases)+len(snap.Deployments) != 0 {
				t.Fatalf("partial snapshot returned: %d objects", len(snap.Commits)+len(snap.PullRequests)+
					len(snap.Runs)+len(snap.Branches)+len(snap.Releases)+len(snap.Deployments))
			}
		})
	}
}

// Every list enforces its item cap: whatever its page budget allows, more
// items than the cap is an error, and no partial snapshot is returned.
func TestBuildSnapshotItemCaps(t *testing.T) {
	for _, tt := range []struct {
		path, probe string
		cap         int
		item        func(int) string
	}{
		{"/branches", "branch", 5 * pageSize, branchItem},
		{"/commits", "commit", 5 * pageSize, commitItem},
		{"/pulls", "pull request", 5 * pageSize, pullItem},
		{"/actions/runs", "run", 5 * pageSize, runItem},
		{"/releases", "release", 2 * pageSize, releaseItem},
		{"/deployments", "deployment", 2 * pageSize, deploymentItem},
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
			if !snap.Now.IsZero() || len(snap.Commits)+len(snap.PullRequests)+len(snap.Runs)+
				len(snap.Branches)+len(snap.Releases)+len(snap.Deployments) != 0 {
				t.Fatal("partial snapshot returned")
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
	if *calls != 0 {
		t.Fatalf("issued %d requests before validation", *calls)
	}
}

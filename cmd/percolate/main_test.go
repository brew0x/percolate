package main

import (
	"bytes"
	tea "charm.land/bubbletea/v2"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brew0x/percolate/internal/repo"
	"github.com/brew0x/percolate/internal/scene"
)

var _ = flag.Bool("update", false, "accepted for repository golden updates")

func TestCLI(t *testing.T) {
	for _, tt := range []struct {
		name     string
		args     []string
		code     int
		contains string
	}{{"hint", nil, 2, "--fixture"}, {"version", []string{"version"}, 0, "0.1.0"}, {"render", []string{"render", "--fixture", "../../testdata/fixtures/small.json", "--frame", "7", "--size", "100x30", "--color=false"}, 0, "●"}, {"bad size", []string{"render", "--fixture", "x", "--size", "-1x2"}, 2, "size"}, {"missing", []string{"--fixture", "missing.json"}, 1, "missing.json"}} {
		t.Run(tt.name, func(t *testing.T) {
			var out, err bytes.Buffer
			code := run(tt.args, &out, &err)
			if code != tt.code {
				t.Fatalf("exit %d: %s", code, err.String())
			}
			all := out.String() + err.String()
			if !strings.Contains(all, tt.contains) {
				t.Fatalf("missing %q: %s", tt.contains, all)
			}
			if tt.name == "render" {
				if strings.Contains(out.String(), "\x1b") {
					t.Fatal("plain rendering contains ANSI")
				}
				if len(strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")) != 30 {
					t.Fatal("wrong rendered height")
				}
				var again bytes.Buffer
				run(tt.args, &again, &err)
				if again.String() != out.String() {
					t.Fatal("CLI not repeatable")
				}
			}
		})
	}
}
func TestNavigationDetailsResizeAndQuit(t *testing.T) {
	m := model{s: scene.Build(repo.Snapshot{Branches: []repo.Branch{{Name: "main"}}, Commits: []repo.Commit{{SHA: "abc", Branch: "main", Author: "robin"}, {SHA: "def", Branch: "main"}}}), width: 60, height: 20}
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = next.(model)
	if m.selected != 1 {
		t.Fatal("tab did not select next mark")
	}
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if !m.details || !strings.Contains(m.View().Content, "def") {
		t.Fatal("detail panel missing selected object")
	}
	next, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	m = next.(model)
	if m.width != 40 || m.height != 12 || !m.View().AltScreen {
		t.Fatal("resize or full screen failed")
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'q'})
	if cmd == nil {
		t.Fatal("quit not handled")
	}
}

// Enter on a run from a live fetch must state what is missing, never a
// fabricated count.
func TestRunDetailsWithoutJobs(t *testing.T) {
	m := model{s: scene.Build(repo.Snapshot{Runs: []repo.WorkflowRun{{ID: 7, HeadSHA: "abc", Status: "in_progress"}}}), width: 60, height: 20}
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	content := m.View().Content
	if !strings.Contains(content, "jobs not recorded") || strings.Contains(content, "0 jobs") {
		t.Fatalf("run details fabricated a job count: %q", content)
	}
}

func TestReorderArgs(t *testing.T) {
	for _, tt := range []struct {
		in, want []string
	}{
		{[]string{"acme/widgets", "--record", "r.json", "--color=false"}, []string{"--record", "r.json", "--color=false", "acme/widgets"}},
		{[]string{"--record", "r.json", "acme/widgets", "--frame", "3"}, []string{"--record", "r.json", "--frame", "3", "acme/widgets"}},
		{[]string{"--fixture", "-x", "acme/widgets"}, []string{"--fixture", "-x", "acme/widgets"}},
		{[]string{"acme/widgets"}, []string{"acme/widgets"}},
	} {
		got := reorderArgs(tt.in)
		if len(got) != len(tt.want) {
			t.Fatalf("reorder %v: %v", tt.in, got)
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Fatalf("reorder %v: got %v", tt.in, got)
			}
		}
	}
}

func TestWatchWithoutToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	// An unusable PATH guarantees the gh credential helper cannot help.
	t.Setenv("PATH", t.TempDir())
	var out, errOut bytes.Buffer
	code := run([]string{"brew0x/percolate"}, &out, &errOut)
	if code != 1 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	message := strings.TrimSuffix(errOut.String(), "\n")
	if !strings.Contains(message, "GitHub token required") || strings.Contains(message, "\n") {
		t.Fatalf("not a one-line token error: %q", message)
	}
	if out.String() != "" {
		t.Fatalf("wrote to stdout: %q", out.String())
	}
}

func TestWatchBadRepository(t *testing.T) {
	for _, arg := range []string{"acme", "acme/a/b", ""} {
		var out, errOut bytes.Buffer
		args := []string{arg}
		if arg == "" {
			args = []string{"--record", "out.json"}
		}
		if code := run(args, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "owner/repo") {
			t.Fatalf("arg %q: exit %d: %s", arg, code, errOut.String())
		}
	}
}

func sha(i int) string { return fmt.Sprintf("%040x", i) }

func fakeGitHub(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/acme/widgets":
			fmt.Fprint(w, `{"default_branch":"main"}`)
		case "/repos/acme/widgets/branches":
			fmt.Fprint(w, `[{"name":"main"},{"name":"flow-1"}]`)
		case "/repos/acme/widgets/commits":
			fmt.Fprint(w, fmt.Sprintf(`[{"sha":"%s","parents":[],"commit":{"author":{"name":"robin","date":"2026-01-01T00:00:00Z"}}},`+
				`{"sha":"%s","parents":[{"sha":"%s"}],"commit":{"author":{"name":"robin","date":"2026-01-02T00:00:00Z"}}}]`,
				sha(2), sha(3), sha(2)))
		case "/repos/acme/widgets/pulls":
			fmt.Fprint(w, `[{"number":1,"state":"open","head":{"ref":"flow-1"},"base":{"ref":"main"}}]`)
		case "/repos/acme/widgets/actions/runs":
			fmt.Fprint(w, fmt.Sprintf(`{"total_count":1,"workflow_runs":[{"id":5,"head_sha":"%s","status":"in_progress","conclusion":null}]}`, sha(2)))
		case "/repos/acme/widgets/releases":
			fmt.Fprint(w, fmt.Sprintf(`[{"tag_name":"v0.1","target_commitish":"%s","published_at":"2026-01-03T00:00:00Z"}]`, sha(2)))
		case "/repos/acme/widgets/deployments":
			fmt.Fprint(w, fmt.Sprintf(`[{"id":9,"sha":"%s","environment":"prod"}]`, sha(3)))
		case "/graphql":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "pullRequest(number:1)") {
				t.Errorf("unexpected GraphQL query: %s", body)
			}
			fmt.Fprint(w, fmt.Sprintf(`{"data":{"repository":{"p0":{"number":1,"headRefOid":"%s","headRefName":"flow-1","baseRefName":"main",`+
				`"headRef":{"target":{"oid":"%s","committedDate":"2026-01-04T00:00:00Z","author":{"name":"robin"},"parents":{"nodes":[{"oid":"%s"}]}}},`+
				`"reviews":{"nodes":[{"author":{"login":"sage"},"state":"APPROVED"}]}}}}}`, sha(4), sha(4), sha(2)))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
}

func TestWatchRecordsAndReplaysIdentically(t *testing.T) {
	server := fakeGitHub(t)
	defer server.Close()
	t.Setenv("GITHUB_TOKEN", "test-token")
	t.Setenv("PERCOLATE_GITHUB_URL", server.URL)
	record := filepath.Join(t.TempDir(), "recorded.json")
	var live, replay bytes.Buffer
	liveArgs := []string{"render", "acme/widgets", "--record", record, "--frame", "13", "--size", "60x20", "--color=false"}
	if code := run(liveArgs, &live, io.Discard); code != 0 {
		t.Fatalf("live exit %d", code)
	}
	replayArgs := []string{"render", "--fixture", record, "--frame", "13", "--size", "60x20", "--color=false"}
	if code := run(replayArgs, &replay, io.Discard); code != 0 {
		t.Fatalf("replay exit %d", code)
	}
	if live.String() != replay.String() || strings.Contains(live.String(), "\x1b") {
		t.Fatal("replay did not render the identical plain frame")
	}
	var again bytes.Buffer
	run(liveArgs, &again, io.Discard)
	if again.String() != live.String() {
		t.Fatal("live render not repeatable")
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	var snap repo.Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("recording is not a snapshot: %v", err)
	}
	if len(snap.PullRequests) != 1 || len(snap.PullRequests[0].Reviews) != 1 ||
		snap.PullRequests[0].Reviews[0].Author != "sage" {
		t.Fatalf("reviews missing from recording: %+v", snap.PullRequests)
	}
	if len(snap.Commits) != 3 || snap.Runs[0].Conclusion != "" || snap.Deployments[0].State != "" {
		t.Fatalf("unexpected objects: %+v", snap)
	}
}

func TestWatchLiveFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "9999999999")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	t.Setenv("GITHUB_TOKEN", "test-token")
	t.Setenv("PERCOLATE_GITHUB_URL", server.URL)
	var out, errOut bytes.Buffer
	if code := run([]string{"brew0x/percolate"}, &out, &errOut); code != 1 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	message := strings.TrimSuffix(errOut.String(), "\n")
	if !strings.Contains(message, "rate limit") || strings.Contains(message, "\n") || strings.Contains(message, "test-token") {
		t.Fatalf("not a safe one-line failure: %q", message)
	}
}

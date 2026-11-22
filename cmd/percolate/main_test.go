package main

import (
	"bytes"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/brew0x/percolate/internal/live"
	"github.com/brew0x/percolate/internal/repo"
	"github.com/brew0x/percolate/internal/scene"
)

var updateTUI = flag.Bool("update", false, "accepted for repository golden updates")

func TestCLI(t *testing.T) {
	for _, tt := range []struct {
		name     string
		args     []string
		code     int
		contains string
	}{{"hint", nil, 2, "--fixture"}, {"version", []string{"version"}, 0, "0.1.0"}, {"long version flag", []string{"--version"}, 0, "0.1.0"}, {"short version flag", []string{"-v"}, 0, "0.1.0"}, {"help", []string{"help"}, 0, "Usage:"}, {"long help flag", []string{"--help"}, 0, "-fixture"}, {"render help flag", []string{"render", "-h"}, 0, "Percolate draws"}, {"render", []string{"render", "--fixture", "../../testdata/fixtures/small.json", "--frame", "7", "--size", "100x30", "--color=false"}, 0, "●"}, {"bad size", []string{"render", "--fixture", "x", "--size", "-1x2"}, 2, "size"}, {"fixture with repo", []string{"--fixture", "../../testdata/fixtures/small.json", "acme/widgets"}, 2, "remove the repository argument"}, {"missing", []string{"--fixture", "missing.json"}, 1, "missing.json"}} {
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

// A newly installed binary is usually probed with --version/-v or help/-h.
// Every spelling answers on stdout with exit 0, and the printed flag list is
// the same definition the parser reads, so help cannot drift from the CLI.
func TestFirstRunAnswers(t *testing.T) {
	for _, arg := range []string{"version", "--version", "-v"} {
		var out, errOut bytes.Buffer
		code := run([]string{arg}, &out, &errOut)
		if code != 0 || out.String() != "percolate "+fallbackVersion+"\n" || errOut.Len() != 0 {
			t.Fatalf("%s: exit %d, stdout %q, stderr %q", arg, code, out.String(), errOut.String())
		}
	}
	var shared string
	for _, args := range [][]string{{"help"}, {"--help"}, {"-h"}, {"render", "-h"}} {
		var out, errOut bytes.Buffer
		if code := run(args, &out, &errOut); code != 0 {
			t.Fatalf("%v: exit %d", args, code)
		}
		if errOut.Len() != 0 || strings.Contains(out.String(), "\x1b") {
			t.Fatalf("%v: help leaked to stderr or carried ANSI: %q / %q", args, errOut.String(), out.String())
		}
		if shared == "" {
			shared = out.String()
		} else if out.String() != shared {
			t.Fatalf("%v: a help spelling printed a different usage", args)
		}
		// Help is where a first-time visitor learns the spellings that the
		// previous release added; do not make them guess again by leaving
		// only `percolate version` in the usage block.
		for _, want := range []string{"Usage:", "percolate owner/repo", "percolate --fixture", "percolate render", "percolate version", "percolate --version", "percolate -v", "percolate help", "percolate --help", "percolate -h", "-fixture", "-record", "-frame", "-size", "-color", "tab or arrows"} {
			if !strings.Contains(out.String(), want) {
				t.Fatalf("%v: help missing %q:\n%s", args, want, out.String())
			}
		}
	}
}

// The documented go-install route must report the version that was installed,
// not the source fallback: `go install ...@latest` injects no ldflags, so the
// tagged version comes from the build info Go records in the binary. A
// GoReleaser -ldflags tag still wins over build info.
func TestReleaseVersion(t *testing.T) {
	for _, tt := range []struct {
		name, injected, module, want string
	}{
		{"local build", fallbackVersion, "(devel)", fallbackVersion},
		{"no build info", fallbackVersion, "", fallbackVersion},
		{"tagged archive", "0.2.0", "(devel)", "0.2.0"},
		{"go install at a tag", fallbackVersion, "v0.2.0", "0.2.0"},
		{"go install at a pseudo-version", fallbackVersion, "v0.2.1-0.20261119-abcdef", "0.2.1-0.20261119-abcdef"},
		{"injected tag wins", "0.2.0", "v9.9.9", "0.2.0"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := releaseVersion(tt.injected, tt.module); got != tt.want {
				t.Fatalf("releaseVersion(%q, %q) = %q, want %q", tt.injected, tt.module, got, tt.want)
			}
		})
	}
}

// The first picture in the README is a still of the bundled small fixture. It
// must be a frame Percolate actually draws, not an old picture left behind by a
// renderer change: a release's storefront is held to the same golden proof as
// the program itself.
func TestREADMEFrameMatchesGolden(t *testing.T) {
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	block := regexp.MustCompile("(?s)```text\n(.*?)```").FindSubmatch(readme)
	if block == nil {
		t.Fatal("README has no text frame")
	}
	golden, err := os.ReadFile("../../testdata/golden/small-100x30-07.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(block[1]) != string(golden) {
		t.Fatalf("README frame is not the reviewed small frame 7:\n got:\n%s\nwant:\n%s", block[1], golden)
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

// Enter on a run with recorded jobs names the failing jobs in the detail
// panel, so the fail cell in the strip is explainable without colour.
func TestRunDetailsListFailingJobs(t *testing.T) {
	m := model{s: scene.Build(repo.Snapshot{Runs: []repo.WorkflowRun{{
		ID: 7, HeadSHA: "abc", Status: "completed", Conclusion: "failure",
		Jobs: []repo.Job{
			{Name: "build", Status: "completed", Conclusion: "success"},
			{Name: "test", Status: "completed", Conclusion: "failure"},
			{Name: "lint", Status: "completed", Conclusion: "timed_out"},
		},
	}}}), width: 200, height: 20}
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	content := m.View().Content
	if !strings.Contains(content, "failing: test, lint") {
		t.Fatalf("run details did not name failing jobs: %q", content)
	}
	if strings.Contains(content, "failing: build") {
		t.Fatalf("successful job named as failing: %q", content)
	}
}

func TestReorderArgs(t *testing.T) {
	fs := flagSet(io.Discard)
	defineFlags(fs)
	for _, tt := range []struct {
		in, want []string
	}{
		{[]string{"acme/widgets", "--record", "r.json", "--color=false"}, []string{"--record", "r.json", "--color=false", "acme/widgets"}},
		{[]string{"--record", "r.json", "acme/widgets", "--frame", "3"}, []string{"--record", "r.json", "--frame", "3", "acme/widgets"}},
		{[]string{"--fixture", "-x", "acme/widgets"}, []string{"--fixture", "-x", "acme/widgets"}},
		{[]string{"acme/widgets"}, []string{"acme/widgets"}},
	} {
		got := reorderArgs(tt.in, fs)
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

// The set of flags that consume a token comes from the parser's own FlagSet,
// not a parallel list. Adding a value flag or a boolean flag must change
// reorderArgs' behaviour without any other edit.
func TestReorderArgsFollowsFlagSet(t *testing.T) {
	fs := flag.NewFlagSet("extra", flag.ContinueOnError)
	fs.String("token", "", "a value flag added later")
	fs.Bool("switch", false, "a boolean flag added later")
	got := reorderArgs([]string{"acme/widgets", "--token", "abc", "--switch"}, fs)
	want := []string{"--token", "abc", "--switch", "acme/widgets"}
	if len(got) != len(want) {
		t.Fatalf("reorder: got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("reorder: got %v, want %v", got, want)
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

func fakeGitHub(t *testing.T, calls *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
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
		case "/repos/acme/widgets/issues":
			// The issues endpoint also returns PRs; only open non-PR items
			// may become springs. Issue 5 has a real closing reference below.
			fmt.Fprint(w, `[{"number":5,"title":"feed","state":"open"},`+
				`{"number":6,"title":"quiet","state":"open"},`+
				`{"number":7,"title":"a pull request","state":"open",`+
				`"pull_request":{"url":"https://api.github.com/repos/acme/widgets/pulls/7"}}]`)
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
			query := string(body)
			if !strings.Contains(query, "pullRequest(number:1)") {
				t.Errorf("unexpected GraphQL query: %s", body)
			}
			// This one PR's GraphQL response exceeds the old 10-review and
			// three-parent caps: the first page needs a follow-up for both
			// connections, so a recording must join the pages to stay complete.
			follow := strings.Contains(query, "after:")
			first, last, parentFirst, parentLast := 0, 99, 0, 3
			if follow {
				first, last, parentFirst, parentLast = 100, 102, 4, 5
			}
			var reviews, parents []string
			for i := first; i <= last; i++ {
				reviews = append(reviews, fmt.Sprintf(`{"author":{"login":"reviewer-%d"},"state":"APPROVED"}`, i))
			}
			for i := parentFirst; i <= parentLast; i++ {
				parents = append(parents, fmt.Sprintf(`{"oid":"%s"}`, sha(2000+i)))
			}
			more := !follow
			// Real GraphQL returns only requested fields. The follow-up
			// verifies the head oid before joining parents, so omit it when
			// the query did not ask for it: a query regression must fail here.
			oidField := ""
			if strings.Contains(query, "target{oid") || strings.Contains(query, "Commit{oid") {
				oidField = fmt.Sprintf(`"oid":"%s",`, sha(4))
			}
			closingField := ""
			if strings.Contains(query, "closingIssuesReferences(first:100)") {
				closingField = `"closingIssuesReferences":{"nodes":[{"number":5}],"pageInfo":{"hasNextPage":false,"endCursor":""}},`
			}
			fmt.Fprintf(w, `{"data":{"repository":{"p0":{"number":1,"headRefOid":"%s","headRefName":"flow-1","baseRefName":"main",`+
				`"reviews":{"nodes":[%s],"pageInfo":{"hasNextPage":%t,"endCursor":"next"}},`+
				`%s`+
				`"headRef":{"target":{%s"committedDate":"2026-01-04T00:00:00Z","author":{"name":"robin"},`+
				`"parents":{"nodes":[%s],"pageInfo":{"hasNextPage":%t,"endCursor":"next"}}}}}}}}`,
				sha(4), strings.Join(reviews, ","), more, closingField, oidField, strings.Join(parents, ","), more)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
}

func TestWatchRecordsAndReplaysIdentically(t *testing.T) {
	var calls int
	server := fakeGitHub(t, &calls)
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
	// The recording must hold both pages of the one long PR, not a tail: 103
	// reviews in order and the head commit's six parents in order.
	pr := snap.PullRequests[0]
	if len(snap.PullRequests) != 1 || len(pr.Reviews) != 103 ||
		pr.Reviews[0].Author != "reviewer-0" || pr.Reviews[99].Author != "reviewer-99" ||
		pr.Reviews[100].Author != "reviewer-100" || pr.Reviews[102].State != "approved" {
		t.Fatalf("reviews not complete in recording: %d, first=%+v last=%+v", len(pr.Reviews), pr.Reviews[0], pr.Reviews[102])
	}
	var head repo.Commit
	for _, cm := range snap.Commits {
		if cm.SHA == sha(4) {
			head = cm
		}
	}
	if len(head.Parents) != 6 || head.Parents[0] != sha(2000) || head.Parents[5] != sha(2005) {
		t.Fatalf("head parents not complete in recording: %+v", head.Parents)
	}
	if len(snap.Commits) != 3 || snap.Runs[0].Conclusion != "" || snap.Deployments[0].State != "" {
		t.Fatalf("unexpected objects: %+v", snap)
	}
	// The open issues travel through the same record→replay path: the PR-shaped
	// list item is excluded and only issue 5 has a recorded closing reference.
	if len(snap.Issues) != 2 || snap.Issues[0].Number != 5 || snap.Issues[0].ClosedBy != 1 ||
		snap.Issues[1].Number != 6 || snap.Issues[1].ClosedBy != 0 {
		t.Fatalf("issues or closing references lost in recording: %+v", snap.Issues)
	}
	// 238 is the documented worst-case plan for default options. This short
	// read costs one page per list plus two detail pages for the one batch.
	if calls > 238 || calls >= 250 {
		t.Fatalf("record→replay used %d requests, over the 238-request plan and 250-request budget", calls)
	}
}

// recordedFixtures are discovered so that a new public recording is covered by
// CLI replay and provenance checks as soon as it is checked in. The dedicated
// tests below still pin each recording's real fields; these catch the common
// failure of adding a file without its frames or its provenance section.
func recordedFixtures(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob("../../testdata/fixtures/recorded-*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no recorded public fixtures found")
	}
	names := make([]string, 0, len(paths))
	for _, path := range paths {
		names = append(names, strings.TrimSuffix(filepath.Base(path), ".json"))
	}
	return names
}

func TestAllRecordedFixturesReplayToGolden(t *testing.T) {
	for _, name := range recordedFixtures(t) {
		t.Run(name, func(t *testing.T) {
			fixturePath := "../../testdata/fixtures/" + name + ".json"
			for _, frame := range []int{0, 7, 30} {
				var out, errOut bytes.Buffer
				args := []string{"render", "--fixture", fixturePath,
					"--frame", fmt.Sprintf("%d", frame), "--size", "100x30", "--color=false"}
				if code := run(args, &out, &errOut); code != 0 {
					t.Fatalf("frame %d: exit %d: %s", frame, code, errOut.String())
				}
				goldenPath := fmt.Sprintf("../../testdata/golden/%s-100x30-%02d.txt", name, frame)
				if *updateTUI {
					if err := os.WriteFile(goldenPath, []byte(out.String()), 0644); err != nil {
						t.Fatal(err)
					}
				}
				golden, err := os.ReadFile(goldenPath)
				if err != nil {
					t.Fatal(err)
				}
				if out.String() != string(golden) {
					t.Fatalf("replay differs from golden frame %d", frame)
				}
				if strings.Contains(out.String(), "\x1b") {
					t.Fatal("replay rendered ANSI sequences with --color=false")
				}
			}
		})
	}
}

// Every recorded fixture needs its own provenance section whose SHA-256 matches
// the bytes being replayed. A later recording, including an authenticated
// GraphQL capture for #21, cannot silently lose its handoff or be edited
// without the note changing too.
func TestAllRecordedFixturesProvenance(t *testing.T) {
	readme, err := os.ReadFile("../../testdata/fixtures/README.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range recordedFixtures(t) {
		t.Run(name, func(t *testing.T) {
			var section string
			for _, part := range strings.Split(string(readme), "\n## ") {
				if strings.HasPrefix(part, "`"+name+".json`") {
					section = part
					break
				}
			}
			if section == "" {
				t.Fatalf("README has no provenance section for %s", name)
			}
			documented := regexp.MustCompile("(?m)\\*\\*SHA-256:\\*\\*\\s+`([0-9a-f]{64})`").
				FindStringSubmatch(section)
			if documented == nil {
				t.Fatalf("provenance for %s has no SHA-256", name)
			}
			data, err := os.ReadFile("../../testdata/fixtures/" + name + ".json")
			if err != nil {
				t.Fatal(err)
			}
			if actual := fmt.Sprintf("%x", sha256.Sum256(data)); actual != documented[1] {
				t.Fatalf("%s changed: documented %s, file hashes to %s", name, documented[1], actual)
			}
		})
	}
}

// The recording in testdata/fixtures/recorded-hivemq.json came from a real
// public GitHub repository (see that directory's README). Rendering it through
// the CLI must reproduce the reviewed golden frame byte for byte: this is the
// offline replay half of the public-recording acceptance for #3. The scene
// golden test covers every frame and size; this checks the rendered path people
// actually run, so an accidental header, color or frame-index change is caught.
func TestRecordedPublicFixtureReplaysToGolden(t *testing.T) {
	const fixturePath = "../../testdata/fixtures/recorded-hivemq.json"

	// First confirm this is a real recording, not an httptest-shaped fake.
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var snap repo.Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("recording is not a snapshot: %v", err)
	}
	if len(snap.Branches) != 1 || snap.Branches[0].Name != "master" ||
		len(snap.Commits) != 6 ||
		snap.Commits[0].SHA != "5e0b579833839b5f254b684049895d08142efa37" ||
		len(snap.Releases) != 1 || snap.Releases[0].Tag != "1.0.0" ||
		len(snap.PullRequests) != 0 || len(snap.Runs) != 0 {
		t.Fatalf("recording lost its real public data: %+v", snap)
	}

	for _, frame := range []int{0, 7, 30} {
		t.Run(fmt.Sprintf("frame%02d", frame), func(t *testing.T) {
			var out, errOut bytes.Buffer
			args := []string{"render", "--fixture", fixturePath,
				"--frame", fmt.Sprintf("%d", frame), "--size", "100x30", "--color=false"}
			if code := run(args, &out, &errOut); code != 0 {
				t.Fatalf("exit %d: %s", code, errOut.String())
			}
			golden, err := os.ReadFile(fmt.Sprintf("../../testdata/golden/recorded-hivemq-100x30-%02d.txt", frame))
			if err != nil {
				t.Fatal(err)
			}
			if out.String() != string(golden) {
				t.Fatalf("replay differs from golden frame %d:\n got:\n%s\nwant:\n%s", frame, out.String(), golden)
			}
			if strings.Contains(out.String(), "\x1b") {
				t.Fatal("replay rendered ANSI sequences with --color=false")
			}
		})
	}
}

// The public recording's provenance must stay checkable in CI: the SHA-256
// documented in testdata/fixtures/README.md has to match the bytes we replay.
// A silent edit to the recording (or a stale provenance note) fails here, so
// the public-recording acceptance for #3 cannot drift without a review.
func TestRecordedPublicFixtureProvenance(t *testing.T) {
	const (
		fixturePath = "../../testdata/fixtures/recorded-hivemq.json"
		readmePath  = "../../testdata/fixtures/README.md"
	)

	readme, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatal(err)
	}
	// Parse the documented hash from the README so provenance has one source
	// of truth: editing the fixture without updating the note fails this test.
	documented := regexp.MustCompile("(?m)\\*\\*SHA-256:\\*\\*\\s+`([0-9a-f]{64})`").
		FindSubmatch(readme)
	if documented == nil {
		t.Fatal("README has no documented SHA-256 for the public recording")
	}

	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	actual := fmt.Sprintf("%x", sha256.Sum256(data))
	if actual != string(documented[1]) {
		t.Fatalf("public recording changed: documented SHA-256 %s, file hashes to %s",
			documented[1], actual)
	}
}

// The second public recording covers the pull-detail pagination finding in #19:
// one real PR with more than ten reviews and another real PR whose head commit
// has more than three parents. Checking the shape first keeps the replay from
// silently passing on a fixture that lost those properties.
func TestRecordedPullDetailsFixtureReplaysToGolden(t *testing.T) {
	const fixturePath = "../../testdata/fixtures/recorded-pull-details.json"

	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var snap repo.Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("recording is not a snapshot: %v", err)
	}
	if len(snap.PullRequests) != 2 {
		t.Fatalf("recording has %d PRs, want the two public cases", len(snap.PullRequests))
	}
	byNumber := make(map[int]repo.PullRequest, len(snap.PullRequests))
	for _, pr := range snap.PullRequests {
		byNumber[pr.Number] = pr
	}
	long, ok := byNumber[8301]
	if !ok || len(long.Reviews) <= 10 || long.Reviews[0].Author != "jfehrle" ||
		long.Reviews[len(long.Reviews)-1].Author != "Zimmi48" ||
		long.Reviews[len(long.Reviews)-1].State != "approved" {
		t.Fatalf("PR 8301 lost its long review history: %+v", long)
	}
	octopus, ok := byNumber[11914]
	if !ok || octopus.Head != "Zimmi48:split-gallina-ext" {
		t.Fatalf("PR 11914 lost its head lane: %+v", octopus)
	}
	const headSHA = "cf978356dd0791cf6b4bc90dcc07a91cd57bd79e"
	var head repo.Commit
	found := false
	for _, cm := range snap.Commits {
		if cm.SHA == headSHA {
			head, found = cm, true
		}
	}
	if !found || head.Branch != octopus.Head || len(head.Parents) <= 3 ||
		head.Parents[0] != "ce6ad8544fe4bd6b5cf83d38424cd7877ff226cc" ||
		head.Parents[len(head.Parents)-1] != "545f2a76e84cea2d9bfecc62ff417e97b3652bd2" {
		t.Fatalf("PR 11914 head lost its five parents: %+v", head)
	}

	for _, frame := range []int{0, 7, 30} {
		t.Run(fmt.Sprintf("frame%02d", frame), func(t *testing.T) {
			var out, errOut bytes.Buffer
			args := []string{"render", "--fixture", fixturePath,
				"--frame", fmt.Sprintf("%d", frame), "--size", "100x30", "--color=false"}
			if code := run(args, &out, &errOut); code != 0 {
				t.Fatalf("exit %d: %s", code, errOut.String())
			}
			golden, err := os.ReadFile(fmt.Sprintf("../../testdata/golden/recorded-pull-details-100x30-%02d.txt", frame))
			if err != nil {
				t.Fatal(err)
			}
			if out.String() != string(golden) {
				t.Fatalf("replay differs from golden frame %d:\n got:\n%s\nwant:\n%s", frame, out.String(), golden)
			}
			if strings.Contains(out.String(), "\x1b") {
				t.Fatal("replay rendered ANSI sequences with --color=false")
			}
		})
	}
}

// The public pull-detail recording's SHA-256 is parsed from its own README
// section, so a stale note or an edited fixture fails here without making the
// earlier public recording's provenance ambiguous.
func TestRecordedPullDetailsFixtureProvenance(t *testing.T) {
	const (
		fixturePath = "../../testdata/fixtures/recorded-pull-details.json"
		readmePath  = "../../testdata/fixtures/README.md"
	)

	readme, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatal(err)
	}
	section := regexp.MustCompile("(?ms)^## `recorded-pull-details\\.json`.*?^- \\*\\*SHA-256:\\*\\*\\s+`([0-9a-f]{64})`").
		FindSubmatch(readme)
	if section == nil {
		t.Fatal("README has no documented SHA-256 for the pull-detail recording")
	}

	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	actual := fmt.Sprintf("%x", sha256.Sum256(data))
	if actual != string(section[1]) {
		t.Fatalf("pull-detail recording changed: documented SHA-256 %s, file hashes to %s",
			section[1], actual)
	}
}

// The open-pull-request recording is the replay-path proof for the fields #21
// needs (not the live GraphQL proof, which requires a token): it must carry one
// real open PR with a named reviewer state and a head commit anchored on the
// fork lane, and its CLI frames must match the reviewed goldens byte for byte.
func TestRecordedOpenPRFixtureReplaysToGolden(t *testing.T) {
	const fixturePath = "../../testdata/fixtures/recorded-open-pr.json"

	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var snap repo.Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("recording is not a snapshot: %v", err)
	}
	if len(snap.Branches) != 1 || snap.Branches[0].Name != "master" || len(snap.Commits) != 135 {
		t.Fatalf("recording lost its real shape: %+v, %d commits", snap.Branches, len(snap.Commits))
	}
	if len(snap.PullRequests) != 1 {
		t.Fatalf("recording has %d PRs, want the one public open PR", len(snap.PullRequests))
	}
	pr := snap.PullRequests[0]
	if pr.Number != 64 || pr.State != "open" || pr.Head != "jfehrle:debugger_ltac2" || pr.Base != "master" {
		t.Fatalf("recording lost the real open PR: %+v", pr)
	}
	if len(pr.Reviews) != 1 || pr.Reviews[0].Author != "ppedrot" || pr.Reviews[0].State != "changes_requested" {
		t.Fatalf("real review was not recorded: %+v", pr.Reviews)
	}
	const headSHA = "7477a535243e1ca7645c154c1b595320e74e9e3d"
	var head repo.Commit
	found := false
	for _, cm := range snap.Commits {
		if cm.SHA == headSHA {
			head, found = cm, true
		}
	}
	if !found || head.Branch != pr.Head || head.Author == "" || len(head.Parents) != 1 ||
		head.Parents[0] != "5393bb1adf13573b014a00753b691d73fc763367" {
		t.Fatalf("head-commit anchor was not recorded: found=%t %+v", found, head)
	}

	for _, frame := range []int{0, 7, 30} {
		t.Run(fmt.Sprintf("frame%02d", frame), func(t *testing.T) {
			var out, errOut bytes.Buffer
			args := []string{"render", "--fixture", fixturePath,
				"--frame", fmt.Sprintf("%d", frame), "--size", "100x30", "--color=false"}
			if code := run(args, &out, &errOut); code != 0 {
				t.Fatalf("exit %d: %s", code, errOut.String())
			}
			golden, err := os.ReadFile(fmt.Sprintf("../../testdata/golden/recorded-open-pr-100x30-%02d.txt", frame))
			if err != nil {
				t.Fatal(err)
			}
			if out.String() != string(golden) {
				t.Fatalf("replay differs from golden frame %d:\n got:\n%s\nwant:\n%s", frame, out.String(), golden)
			}
			if strings.Contains(out.String(), "\x1b") {
				t.Fatal("replay rendered ANSI sequences with --color=false")
			}
		})
	}
}

// The open-PR recording's SHA-256 lives in its own README section, so editing
// it without updating provenance fails here without touching other recordings.
// The same check keeps its honest REST-derived method in place: the missing
// authenticated GraphQL capture must never be implied by relabelling this file.
func TestRecordedOpenPRFixtureProvenance(t *testing.T) {
	const (
		fixturePath = "../../testdata/fixtures/recorded-open-pr.json"
		readmePath  = "../../testdata/fixtures/README.md"
	)

	readme, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatal(err)
	}
	section := regexp.MustCompile("(?ms)^## `recorded-open-pr\\.json`.*?^- \\*\\*SHA-256:\\*\\*\\s+`([0-9a-f]{64})`").
		FindSubmatch(readme)
	if section == nil {
		t.Fatal("README has no documented SHA-256 for the open-PR recording")
	}

	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	actual := fmt.Sprintf("%x", sha256.Sum256(data))
	if actual != string(section[1]) {
		t.Fatalf("open-PR recording changed: documented SHA-256 %s, file hashes to %s", section[1], actual)
	}

	record := string(section[0])
	restMethod := regexp.MustCompile(`(?s)\*\*Method:\*\*.*?unauthenticated REST`).MatchString(record)
	graphqlCaveat := regexp.MustCompile(`(?s)\*\*GraphQL caveat:\*\*.*?does \*\*not\*\* come from an authenticated\s+GraphQL call`).MatchString(record)
	if !restMethod || !graphqlCaveat {
		t.Fatal("open-PR provenance must keep saying it is REST-derived and not a GraphQL capture")
	}
}

// The local proof uses the same bounded read as the public recording, but its
// one synthetic PR carries both long lists in the same object. Keeping it in a
// helper lets the provenance test check the two public cases and the combined
// case against the same replay path without duplicating the fake server.
func recordedLocalPullDetailProof(t *testing.T) repo.Snapshot {
	t.Helper()
	var calls int
	server := fakeGitHub(t, &calls)
	defer server.Close()
	t.Setenv("GITHUB_TOKEN", "test-token")
	t.Setenv("PERCOLATE_GITHUB_URL", server.URL)
	record := filepath.Join(t.TempDir(), "local-proof.json")
	args := []string{"render", "acme/widgets", "--record", record, "--frame", "0", "--size", "60x20", "--color=false"}
	if code := run(args, io.Discard, io.Discard); code != 0 {
		t.Fatalf("local proof exit %d", code)
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	var snap repo.Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("local proof is not a snapshot: %v", err)
	}
	return snap
}

// The public fixture must keep both real connections, and the combined case
// must stay checkable locally: no public PR with both properties was found, so
// neither the fixture nor the proof may silently lose a bounded list.
func TestRecordedPullDetailsCoversBothPublicConnections(t *testing.T) {
	data, err := os.ReadFile("../../testdata/fixtures/recorded-pull-details.json")
	if err != nil {
		t.Fatal(err)
	}
	var public repo.Snapshot
	if err := json.Unmarshal(data, &public); err != nil {
		t.Fatal(err)
	}
	var longReviews, octopusHead bool
	for _, pr := range public.PullRequests {
		if len(pr.Reviews) > 10 {
			longReviews = true
		}
		for _, cm := range public.Commits {
			if cm.Branch == pr.Head && len(cm.Parents) > 3 {
				octopusHead = true
			}
		}
	}
	if !longReviews || !octopusHead {
		t.Fatalf("public fixture lost a bounded connection: longReviews=%t octopusHead=%t", longReviews, octopusHead)
	}

	local := recordedLocalPullDetailProof(t)
	if len(local.PullRequests) != 1 {
		t.Fatalf("local proof has %d PRs, want one", len(local.PullRequests))
	}
	pr := local.PullRequests[0]
	if len(pr.Reviews) != 103 {
		t.Fatalf("local proof has %d reviews, want 103", len(pr.Reviews))
	}
	var parents int
	for _, cm := range local.Commits {
		if cm.Branch == pr.Head {
			parents = len(cm.Parents)
		}
	}
	if parents != 6 {
		t.Fatalf("local proof head has %d parents, want 6", parents)
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

// A GraphQL error is server text. On a failed first read the CLI prints it to
// stderr before the TUI exists, so an escape or bidi control in that message
// must already have been neutralised at the transport boundary. No repository
// response may write a terminal instruction through the error path.
func TestWatchLiveGraphQLErrorIsSafeOnStderr(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/acme/widgets":
			fmt.Fprint(w, `{"default_branch":"main"}`)
		case "/repos/acme/widgets/branches":
			fmt.Fprint(w, `[{"name":"main"}]`)
		case "/repos/acme/widgets/actions/runs":
			fmt.Fprint(w, `{"total_count":0,"workflow_runs":[]}`)
		case "/repos/acme/widgets/commits",
			"/repos/acme/widgets/issues", "/repos/acme/widgets/releases",
			"/repos/acme/widgets/deployments":
			fmt.Fprint(w, `[]`)
		case "/repos/acme/widgets/pulls":
			fmt.Fprint(w, `[{"number":1,"state":"open","head":{"ref":"flow-1"},"base":{"ref":"main"}}]`)
		case "/graphql":
			fmt.Fprint(w, `{"errors":[{"message":"backend\u001b[2J\u202ebroke"}]}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	t.Setenv("GITHUB_TOKEN", "test-token")
	t.Setenv("PERCOLATE_GITHUB_URL", server.URL)
	var out, errOut bytes.Buffer
	if code := run([]string{"acme/widgets"}, &out, &errOut); code != 1 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	message := errOut.String()
	if !strings.Contains(message, "backend?[2J?broke") {
		t.Fatalf("server message lost its visible text: %q", message)
	}
	if strings.ContainsAny(message, "\x1b\u202e\x00") {
		t.Fatalf("live error wrote a control character to stderr: %q", message)
	}
}

// A live refresh that changes the snapshot starts a frame-indexed transition;
// an unchanged poll does not replay one, and the transition ends on its own.
func TestLiveRefreshStartsTransition(t *testing.T) {
	day1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	day2 := day1.Add(24 * time.Hour)
	day3 := day2.Add(24 * time.Hour)
	prev := repo.Snapshot{
		Now:      day3,
		Branches: []repo.Branch{{Name: "main"}},
		Commits: []repo.Commit{
			{SHA: sha(1), Branch: "main", Author: "robin", Time: day3},
			{SHA: sha(2), Branch: "main", Author: "robin", Time: day2, Parents: []string{sha(1)}},
			{SHA: sha(3), Branch: "main", Author: "robin", Time: day1, Parents: []string{sha(2)}},
		},
	}
	next := repo.Snapshot{
		Now:      day3.Add(time.Hour),
		Branches: []repo.Branch{{Name: "main"}},
		Commits: append([]repo.Commit{
			{SHA: sha(4), Branch: "main", Author: "robin", Time: day1.Add(12 * time.Hour), Parents: []string{sha(3)}},
		}, prev.Commits...),
	}
	m := model{s: scene.Build(prev), prev: prev, cur: prev, width: 100, height: 30}

	// A live poll re-reads the same state with a fresh capture timestamp; the
	// model must treat that as unchanged and keep the current scene.
	unchanged := prev
	unchanged.Now = prev.Now.Add(time.Hour)
	updated, _ := m.Update(snapshotMsg{snap: unchanged})
	m = updated.(model)
	if m.transiting {
		t.Fatal("an unchanged poll with a fresh timestamp replayed a transition")
	}

	updated, cmd := m.Update(snapshotMsg{snap: next})
	m = updated.(model)
	if !m.transiting || cmd == nil {
		t.Fatal("a changed snapshot did not start a transition")
	}
	if !live.Equal(m.cur, next) || !live.Equal(m.prev, prev) {
		t.Fatal("transition did not bracket the two snapshots")
	}

	m.frame = 0
	first := m.View().Content
	m.frame = scene.TransitionFrames // the settled scene differs from the start
	second := m.View().Content
	if first == second {
		t.Fatal("View did not render the transition")
	}
	for i := 0; i <= scene.TransitionFrames; i++ {
		updated, _ = m.Update(tickMsg{})
		m = updated.(model)
	}
	if m.transiting {
		t.Fatal("transition did not end after TransitionFrames")
	}
}

// An unchanged poll still carries a fresh capture time, and Build ages commit
// styles against it. The scene must refresh without starting a transition: a
// commit crossing the 48-hour threshold dims, and stillness stays still.
func TestUnchangedPollAgesCommits(t *testing.T) {
	now := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
	snap := repo.Snapshot{
		Now:      now,
		Branches: []repo.Branch{{Name: "main"}},
		Commits:  []repo.Commit{{SHA: sha(1), Branch: "main", Author: "robin", Time: now.Add(-47 * time.Hour)}},
	}
	m := model{s: scene.Build(snap), prev: snap, cur: snap, width: 60, height: 20}
	if m.s.Marks[0].Style != scene.Fresh {
		t.Fatal("test setup: the commit should start fresh")
	}
	later := snap
	later.Now = now.Add(2 * time.Hour) // the commit is now 49 hours old
	updated, cmd := m.Update(snapshotMsg{snap: later})
	m = updated.(model)
	if m.transiting {
		t.Fatal("an unchanged poll replayed a transition")
	}
	if cmd == nil {
		t.Fatal("an unchanged poll stopped polling")
	}
	if !m.cur.Now.Equal(later.Now) {
		t.Fatal("an unchanged poll did not take the fresh capture time")
	}
	if m.s.Marks[0].Style != scene.Faded {
		t.Fatal("an unchanged poll froze commit aging")
	}
}

// The drawn change must be reproducible from the two snapshots and the
// transition-relative frame alone: Frame takes the same index Transition does,
// so a running run's pulse phase never depends on when the poll arrived.
func TestTransitionViewIsTransitionRelative(t *testing.T) {
	now := time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC)
	snap := repo.Snapshot{
		Now:      now,
		Branches: []repo.Branch{{Name: "main"}},
		Commits:  []repo.Commit{{SHA: sha(1), Branch: "main", Author: "robin", Time: now}},
	}
	snap.Runs = []repo.WorkflowRun{{ID: 1, HeadSHA: sha(1), Status: "in_progress"}}
	view := func(start, frame int) string {
		m := model{
			s: scene.Build(snap), prev: snap, cur: snap, width: 60, height: 20,
			transiting: true, transitionStart: start, frame: frame,
		}
		return m.View().Content
	}
	same := view(0, 20)
	if view(10, 30) != same {
		t.Fatal("the same transition frame drew differently: Frame got the session-wide index")
	}
	if view(0, 28) == same {
		t.Fatal("different transition frames drew identically")
	}
}

// A refresh that inserts a new commit ahead of the selected one must keep the
// same object highlighted and keep its open details. Preserving only the
// numeric index would hand the selection to the newly inserted commit.
func TestRefreshKeepsSelectedObject(t *testing.T) {
	day1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	day2, day3 := day1.Add(24*time.Hour), day1.Add(48*time.Hour)
	prev := repo.Snapshot{
		Now:      day3,
		Branches: []repo.Branch{{Name: "main"}},
		Commits: []repo.Commit{
			{SHA: sha(11), Branch: "main", Author: "robin", Time: day3},
			{SHA: sha(22), Branch: "main", Author: "sage", Time: day1, Parents: []string{sha(11)}},
		},
	}
	next := prev
	next.Now = day3.Add(time.Hour)
	// The new commit sorts between the two originals, so numeric index 1
	// would now point at it instead of the selected sage commit.
	next.Commits = append([]repo.Commit{
		{SHA: sha(33), Branch: "main", Author: "newcomer", Time: day2, Parents: []string{sha(11)}},
	}, prev.Commits...)

	selected := scene.Build(prev)
	m := model{
		s: selected, prev: prev, cur: prev, width: 60, height: 20,
		selected: 1, selectedID: selected.Marks[1].ID, details: true,
	}
	if before := m.View().Content; !strings.Contains(before, "sage") {
		t.Fatalf("test setup lost the selected details:\n%s", before)
	}

	updated, _ := m.Update(snapshotMsg{snap: next})
	m = updated.(model)
	if got, want := m.s.Marks[m.selected].ID, "commit:"+sha(22); got != want {
		t.Fatalf("selected index points at %q, want %q", got, want)
	}
	after := m.View().Content
	if !strings.Contains(after, "sage") || strings.Contains(after, "newcomer") {
		t.Fatalf("refresh silently changed the selected object:\n%s", after)
	}
}

// A failed poll is a footer status that keeps the old scene and schedules a
// retry; it never draws a fabricated snapshot.
func TestFailedRefreshKeepsScene(t *testing.T) {
	prev := repo.Snapshot{Branches: []repo.Branch{{Name: "main"}}}
	m := model{s: scene.Build(prev), prev: prev, cur: prev, width: 60, height: 20}
	updated, cmd := m.Update(snapshotMsg{err: errors.New("temporary failure")})
	m = updated.(model)
	if m.refreshErr == "" || cmd == nil {
		t.Fatal("failed refresh was not noted or retried")
	}
	if !strings.Contains(m.View().Content, "refresh failed") {
		t.Fatal("failed refresh had no footer status")
	}
	if !live.Equal(m.cur, prev) || m.transiting {
		t.Fatal("failed refresh changed the scene")
	}
}

// The footer names the reason a live read failed, so someone leaving a window
// on a spare screen can tell a rate limit from an outage and report it. The
// reason is the failed read's own one-line message, never a guess, and a later
// success clears it. Server text cannot add rows or terminal instructions.
func TestFailedRefreshShowsReasonAndClearsIt(t *testing.T) {
	prev := repo.Snapshot{Branches: []repo.Branch{{Name: "main"}}}
	m := model{s: scene.Build(prev), prev: prev, cur: prev, width: 100, height: 20}
	updated, _ := m.Update(snapshotMsg{err: errors.New("GitHub rate limit exhausted\nresets in 3h20m0s")})
	m = updated.(model)
	key := strings.Join(m.footer(), "\n")
	for _, want := range []string{"refresh failed:", "GitHub rate limit exhausted", "resets in 3h20m0s", "; retrying"} {
		if !strings.Contains(key, want) {
			t.Fatalf("failure reason lost %q: %q", want, key)
		}
	}
	for _, row := range m.footer() {
		if strings.ContainsAny(row, "\n\r\x1b") {
			t.Fatalf("failure reason added a row or an escape: %q", row)
		}
	}

	updated, _ = m.Update(snapshotMsg{snap: prev})
	m = updated.(model)
	if m.refreshErr != "" || strings.Contains(strings.Join(m.footer(), "\n"), "refresh failed") {
		t.Fatalf("a successful poll kept a stale failure: %q", m.footer())
	}
}

// A rate-limit reset is the actionable part of a failed read: the framing may
// narrow, but it must yield the row before the reason is clipped. At a normal
// short-terminal width the whole reset time stays readable.
func TestFooterKeepsFailureReasonAtReviewWidth(t *testing.T) {
	const reason = "GitHub rate limit exhausted; resets in 3h20m0s"
	prev := repo.Snapshot{Branches: []repo.Branch{{Name: "main"}}}
	for _, tt := range []struct {
		width int
		want  string
	}{
		{100, "refresh failed: " + reason + "; retrying"},
		{60, "failed: " + reason},
	} {
		t.Run(fmt.Sprintf("width%d", tt.width), func(t *testing.T) {
			m := model{s: scene.Build(prev), prev: prev, cur: prev, width: tt.width, height: 20, refreshErr: reason}
			rows := m.footer()
			if !strings.Contains(strings.Join(rows, "\n"), tt.want) {
				t.Fatalf("width %d lost %q: %q", tt.width, tt.want, rows)
			}
			for _, row := range rows {
				if lipgloss.Width(row) > tt.width {
					t.Fatalf("width %d: row too wide: %q", tt.width, row)
				}
			}
		})
	}
}

// A long status or a narrow terminal must not hide the controls. The status is
// shortened first, then the hint wording; the full hints stay whenever they
// fit, and both spellings always include the quit and browser keys.
func TestFooterKeepsHintsAtNarrowWidth(t *testing.T) {
	prev := repo.Snapshot{Branches: []repo.Branch{{Name: "main"}}}
	for _, tt := range []struct {
		name   string
		width  int
		reason string
		notice string
	}{
		{"narrow with reason", 40, "GitHub rate limit exhausted; resets in 3h20m0s", ""},
		{"very narrow with reason", 24, "GitHub rate limit exhausted; resets in 3h20m0s", ""},
		{"long notice", 40, "", strings.Repeat("opened in browser and more", 3)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := model{s: scene.Build(prev), prev: prev, cur: prev, width: tt.width, height: 12, notice: tt.notice, refreshErr: tt.reason}
			rows := m.footer()
			key := strings.Join(rows, "\n")
			for _, row := range rows {
				if lipgloss.Width(row) > tt.width {
					t.Fatalf("footer row is %d cells for %d columns: %q", lipgloss.Width(row), tt.width, row)
				}
			}
			if !strings.HasSuffix(rows[len(rows)-1], "q") || !strings.Contains(key, "o") {
				t.Fatalf("footer hid the browser or quit key: %q", key)
			}
			if strings.Contains(tt.reason, "rate limit") && tt.width >= 40 && !strings.Contains(key, "rate limit") {
				t.Fatalf("footer hid the failure reason: %q", key)
			}
		})
	}
}

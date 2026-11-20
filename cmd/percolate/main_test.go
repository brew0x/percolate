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
	for _, arg := range []string{"help", "--help", "-h"} {
		var out, errOut bytes.Buffer
		if code := run([]string{arg}, &out, &errOut); code != 0 {
			t.Fatalf("%s: exit %d", arg, code)
		}
		if errOut.Len() != 0 || strings.Contains(out.String(), "\x1b") {
			t.Fatalf("%s: help leaked to stderr or carried ANSI: %q / %q", arg, errOut.String(), out.String())
		}
		for _, want := range []string{"Usage:", "percolate owner/repo", "percolate --fixture", "percolate render", "-fixture", "-record", "-frame", "-size", "-color", "tab or arrows"} {
			if !strings.Contains(out.String(), want) {
				t.Fatalf("%s: help missing %q:\n%s", arg, want, out.String())
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

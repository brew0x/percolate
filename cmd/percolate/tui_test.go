package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/brew0x/percolate/internal/repo"
	"github.com/brew0x/percolate/internal/scene"
)

func uiModel(snap repo.Snapshot) model {
	s := scene.Build(snap)
	return model{
		s: s, prev: snap, cur: snap, width: 100, height: 30,
		web: "https://github.com", slug: "acme/widgets",
	}
}

func markByID(t *testing.T, m model, id string) scene.Mark {
	t.Helper()
	i := markIndex(m.s, id)
	if i < 0 {
		t.Fatalf("no mark %q", id)
	}
	return m.s.Marks[i]
}

func panelText(m model, mark scene.Mark) string {
	return strings.Join(detailLines(m, mark), "\n") + "\n"
}

// The panel reports the snapshot's recorded fields, not a decorative summary:
// a missing state or job list says so, and the link names the real object.
func TestDetailPanelReportsRecordedFields(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	snap := repo.Snapshot{
		Branches: []repo.Branch{{Name: "main"}},
		Commits:  []repo.Commit{{SHA: "abc123", Branch: "main", Author: "robin", Parents: []string{"parent"}, Time: now}},
		PullRequests: []repo.PullRequest{{
			Number: 7, Head: "topic", Base: "main", State: "open",
			Reviews: []repo.Review{{Author: "sage", State: "approved"}},
		}},
		Runs: []repo.WorkflowRun{{
			ID: 9, HeadSHA: "abc123", Status: "completed", Conclusion: "failure",
			Jobs: []repo.Job{
				{Name: "build", Status: "completed", Conclusion: "success"},
				{Name: "test", Status: "completed", Conclusion: "failure"},
			},
		}},
	}
	m := uiModel(snap)

	commit := panelText(m, markByID(t, m, "commit:abc123"))
	for _, want := range []string{"sha", "abc123", "branch", "main", "author", "robin", "parents", "parent", "time", "2026-01-02T03:04:05Z", "https://github.com/acme/widgets/commit/abc123"} {
		if !strings.Contains(commit, want) {
			t.Fatalf("commit panel missing %q:\n%s", want, commit)
		}
	}

	pr := panelText(m, markByID(t, m, "pr:7"))
	for _, want := range []string{"number", "7", "head", "topic", "base", "main", "reviews", "sage: approved", "resolved", "approved", "https://github.com/acme/widgets/pull/7"} {
		if !strings.Contains(pr, want) {
			t.Fatalf("PR panel missing %q:\n%s", want, pr)
		}
	}

	run := panelText(m, markByID(t, m, "run:9"))
	for _, want := range []string{"id", "9", "conclusion", "failure", "jobs", "2 jobs", "failing: test", "https://github.com/acme/widgets/actions/runs/9"} {
		if !strings.Contains(run, want) {
			t.Fatalf("run panel missing %q:\n%s", want, run)
		}
	}
	if strings.Contains(run, "failing: build") {
		t.Fatalf("successful job named as failing:\n%s", run)
	}

	unknown := panelText(m, scene.Mark{ID: "run:77", Kind: "run", Label: "CI 77", Detail: "run 77 · status not in snapshot"})
	if !strings.Contains(unknown, "not in snapshot") || !strings.Contains(unknown, "https://github.com/acme/widgets/actions/runs/77") {
		t.Fatalf("departed object lost its recorded detail:\n%s", unknown)
	}
}

// A replay has no repository to link to, so the panel says so instead of
// inventing a URL from a fixture name or a mark label.
func TestDetailPanelReplayHasNoURL(t *testing.T) {
	snap := repo.Snapshot{Commits: []repo.Commit{{SHA: "abc"}}}
	m := uiModel(snap)
	m.slug = ""
	panel := panelText(m, markByID(t, m, "commit:abc"))
	if !strings.Contains(panel, "no URL: replay has no repository") || strings.Contains(panel, "https://") {
		t.Fatalf("replay panel invented a URL:\n%s", panel)
	}
	if cmd := openBrowserCmd(markURL(m, markByID(t, m, "commit:abc"))); cmd != nil {
		t.Fatal("a replay offered a browser command")
	}
}

// Repository-controlled text cannot add rows or terminal instructions to the
// panel. Newlines, escapes and bidi controls are replaced before measurement,
// so every field still occupies exactly one measured line.
func TestDetailPanelSanitizesRepositoryText(t *testing.T) {
	hostile := "safe\r\n  github     https://evil.example\x1b[31m\u202e"
	snap := repo.Snapshot{Issues: []repo.Issue{{Number: 5, Title: hostile, State: "open"}}}
	m := uiModel(snap)
	lines := detailLines(m, markByID(t, m, "issue:5"))
	if len(lines) != 6 {
		t.Fatalf("hostile title changed the panel shape: %d lines: %q", len(lines), lines)
	}
	if !strings.HasPrefix(lines[0], "issue  #5") {
		t.Fatalf("panel header changed: %q", lines[0])
	}
	for i, line := range lines[1:] {
		if !strings.HasPrefix(line, "  ") {
			t.Fatalf("line %d was spoofed into a panel row: %q", i+1, line)
		}
		if strings.ContainsAny(line, "\r\x1b\u202e") {
			t.Fatalf("control character survived in line %d: %q", i+1, line)
		}
	}
	github := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "  github") {
			github++
		}
	}
	if github != 1 || !strings.Contains(lines[5], "https://github.com/acme/widgets/issues/5") {
		t.Fatalf("panel lost its real URL:\n%s", strings.Join(lines, "\n"))
	}
	// Labels are sanitized too, not just values.
	header := detailLines(m, scene.Mark{ID: "deployment:7", Kind: "deployment", Label: "prod\n\x1b[31m"})[0]
	if strings.ContainsAny(header, "\n\x1b") || !strings.HasPrefix(header, "deployment  prod??") {
		t.Fatalf("hostile label changed the header: %q", header)
	}
}

func TestMarkURLUsesGitHubRoutes(t *testing.T) {
	m := uiModel(repo.Snapshot{})
	for _, tt := range []struct {
		mark scene.Mark
		want string
	}{
		{scene.Mark{ID: "commit:abc", Kind: "commit"}, "https://github.com/acme/widgets/commit/abc"},
		{scene.Mark{ID: "pr:12", Kind: "pr"}, "https://github.com/acme/widgets/pull/12"},
		{scene.Mark{ID: "issue:5", Kind: "issue"}, "https://github.com/acme/widgets/issues/5"},
		{scene.Mark{ID: "run:8", Kind: "run"}, "https://github.com/acme/widgets/actions/runs/8"},
		{scene.Mark{ID: "release:v1.0", Kind: "release"}, "https://github.com/acme/widgets/releases/tag/v1.0"},
		{scene.Mark{ID: "deployment:4", Kind: "deployment", Label: "prod/eu"}, "https://github.com/acme/widgets/deployments/4"},
	} {
		if got := markURL(m, tt.mark); got != tt.want {
			t.Fatalf("%s: got %q, want %q", tt.mark.ID, got, tt.want)
		}
	}
	if got := markURL(m, scene.Mark{ID: "unknown:1", Kind: "unknown"}); got != "" {
		t.Fatalf("unknown kind got a URL: %q", got)
	}
	// Two deployments to one environment are different recorded objects; their
	// links must differ even though their labels are identical.
	first := markURL(m, scene.Mark{ID: "deployment:41", Kind: "deployment", Label: "prod"})
	second := markURL(m, scene.Mark{ID: "deployment:42", Kind: "deployment", Label: "prod"})
	if first == second || !strings.HasSuffix(first, "/deployments/41") || !strings.HasSuffix(second, "/deployments/42") {
		t.Fatalf("same-environment deployments collapsed: %q and %q", first, second)
	}
}

func TestBrowserCommandAndOpenKey(t *testing.T) {
	rawURL := "https://github.com/acme/widgets/pull/7"
	t.Setenv("BROWSER", "/bin/echo -n")
	cmd := browserCommand(rawURL)
	if got, want := strings.Join(cmd.Args, "|"), "/bin/echo|-n|"+rawURL; got != want {
		t.Fatalf("browser args %q, want %q", got, want)
	}
	if openBrowserCmd("file:///etc/passwd") != nil || openBrowserCmd("") != nil {
		t.Fatal("a non-web URL was offered to the browser")
	}
	if openBrowserCmd("https://github.com/acme/widgets\nopen calc") != nil {
		t.Fatal("a URL with a control character was offered to the browser")
	}

	// No shell may parse a URL derived from repository data. The Windows
	// launcher calls the URL handler directly rather than cmd /c start.
	t.Setenv("BROWSER", "")
	win := browserCommandFor("windows", rawURL)
	if got, want := strings.Join(win.Args, "|"), "rundll32.exe|url.dll,FileProtocolHandler|"+rawURL; got != want {
		t.Fatalf("Windows browser args %q, want %q", got, want)
	}

	snap := repo.Snapshot{Commits: []repo.Commit{{SHA: "abc"}}}
	m := uiModel(snap)
	_, open := m.Update(tea.KeyPressMsg{Code: 'o'})
	if open == nil {
		t.Fatal("o did not offer to open the selected object")
	}
}

// Mouse and keyboard move the same cursor: a click picks the mark drawn under
// the terminal cell, and the wheel steps through the recorded marks.
func TestMouseMovesSelection(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	snap := repo.Snapshot{
		Branches: []repo.Branch{{Name: "main"}},
		Commits: []repo.Commit{
			{SHA: "aaa", Branch: "main", Time: now},
			{SHA: "bbb", Branch: "main", Time: now.Add(-time.Hour)},
			{SHA: "ccc", Branch: "main", Time: now.Add(-2 * time.Hour)},
		},
	}
	m := uiModel(snap)
	g := scene.Frame(m.s, 0, m.width, m.height-1)
	// The last bead is the furthest from the initial selection.
	x, y, found := -1, -1, false
	for rowIndex, row := range g {
		for col, cell := range row {
			if cell.Rune == '●' {
				x, y, found = col, rowIndex, true
			}
		}
	}
	if !found {
		t.Fatal("no commit drawn in the test frame")
	}
	next, _ := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m = next.(model)
	if want := "commit:ccc"; m.selectedID != want {
		t.Fatalf("click selected %q, want %q", m.selectedID, want)
	}

	// Hover over the first bead moves the same cursor; a wheel step moves on.
	x, y = -1, -1
	for rowIndex, row := range g {
		for col, cell := range row {
			if cell.Rune == '●' {
				x, y = col, rowIndex
				break
			}
		}
		if x >= 0 {
			break
		}
	}
	next, _ = m.Update(tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseNone})
	m = next.(model)
	if m.selectedID != "commit:aaa" {
		t.Fatalf("hover selected %q", m.selectedID)
	}
	next, _ = m.Update(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown})
	m = next.(model)
	if m.selectedID != "commit:bbb" {
		t.Fatalf("wheel selected %q", m.selectedID)
	}
	next, _ = m.Update(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelUp})
	m = next.(model)
	if m.selectedID != "commit:aaa" {
		t.Fatalf("wheel up selected %q", m.selectedID)
	}
}

// Deployments share a glyph and a row; each drawn cell must name its own
// recorded deployment, not the first one in the snapshot.
func TestMouseSelectsTheDrawnDeployment(t *testing.T) {
	data, err := os.ReadFile("../../testdata/fixtures/release.json")
	if err != nil {
		t.Fatal(err)
	}
	var snap repo.Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatal(err)
	}
	m := uiModel(snap)
	g := scene.Frame(m.s, 0, m.width, m.height-1)
	seen := map[string]bool{}
	cells := 0
	for y, row := range g {
		for x, cell := range row {
			if cell.Rune != '▣' {
				continue
			}
			cells++
			id := m.markAt(x, y)
			if !strings.HasPrefix(id, "deployment:") {
				t.Fatalf("deployment cell %d,%d named %q", x, y, id)
			}
			seen[id] = true
		}
	}
	if cells != len(snap.Deployments) || len(seen) != len(snap.Deployments) {
		t.Fatalf("deployment cells collapsed: %d cells, %d marks for %d deployments", cells, len(seen), len(snap.Deployments))
	}
}

// The detail panel is text a reviewer can read without a terminal: these two
// frames are the checked-in proof of what an open panel says on screen.
func TestTUIDetailGolden(t *testing.T) {
	data, err := os.ReadFile("../../testdata/fixtures/small.json")
	if err != nil {
		t.Fatal(err)
	}
	var snap repo.Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatal(err)
	}
	m := uiModel(snap)
	for _, tt := range []struct {
		name, id string
	}{
		{"commit", "commit:" + snap.Commits[0].SHA},
		{"run", fmt.Sprintf("run:%d", snap.Runs[0].ID)},
	} {
		got := panelText(m, markByID(t, m, tt.id))
		path := "../../testdata/golden/tui-detail-" + tt.name + ".txt"
		if *updateTUI {
			if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if got != string(want) {
			t.Fatalf("detail panel differs from %s:\n got:\n%s\nwant:\n%s", path, got, want)
		}
	}
}

// The same glyph on several lanes must name several objects. Every drawn PR
// diamond is tested, including a click that opens only that PR's own URL.
func TestMouseSelectsEachRepeatedGlyph(t *testing.T) {
	var branches []repo.Branch
	var prs []repo.PullRequest
	for i := 0; i < 3; i++ {
		head, base := fmt.Sprintf("topic-%d", i), fmt.Sprintf("base-%d", i)
		branches = append(branches, repo.Branch{Name: head}, repo.Branch{Name: base})
		prs = append(prs, repo.PullRequest{Number: i + 7, Head: head, Base: base})
	}
	m := uiModel(repo.Snapshot{Branches: branches, PullRequests: prs})
	s, frame := m.viewScene()
	g := scene.Frame(s, frame, m.width, m.height-m.reserve())
	seen := map[string]bool{}
	for y, row := range g {
		for x, cell := range row {
			if cell.Rune != '◇' {
				continue
			}
			id := m.markAt(x, y)
			if !strings.HasPrefix(id, "pr:") {
				t.Fatalf("PR cell %d,%d named %q", x, y, id)
			}
			seen[id] = true
			next, _ := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
			clicked := next.(model)
			if clicked.selectionID() != id {
				t.Fatalf("click at %d,%d selected %q, want %q", x, y, clicked.selectionID(), id)
			}
			want := "https://github.com/acme/widgets/pull/" + strings.TrimPrefix(id, "pr:")
			if got := markURL(clicked, markByID(t, clicked, id)); got != want {
				t.Fatalf("click at %d,%d opened %q, want %q", x, y, got, want)
			}
		}
	}
	if len(seen) != len(prs) {
		t.Fatalf("repeated diamonds collapsed to %d objects: %v", len(seen), seen)
	}
}

// Keyboard navigation can scroll a selected lane into view. A click after that
// must hit-test the viewport View actually draws, not the unscrolled scene.
func TestMouseHitTestsScrolledViewport(t *testing.T) {
	var branches []repo.Branch
	for i := 0; i < 8; i++ { // more lanes than a 100-column viewport shows
		branches = append(branches, repo.Branch{Name: fmt.Sprintf("b%d", i)})
	}
	snap := repo.Snapshot{
		Branches:     branches,
		PullRequests: []repo.PullRequest{{Number: 1, Head: "b0", Base: "b1"}, {Number: 8, Head: "b7", Base: "b6"}},
	}
	m := uiModel(snap)
	next, _ := m.Update(tea.KeyPressMsg{Code: '\t'})
	m = next.(model)
	if got := m.selectionID(); got != "pr:8" {
		t.Fatalf("tab selected %q, want pr:8", got)
	}
	s, frame := m.viewScene()
	if s.FirstLane == 0 {
		t.Fatal("keyboard selection did not bring its lane into view")
	}
	g := scene.Frame(s, frame, m.width, m.height-m.reserve())
	diamonds := 0
	for y, row := range g {
		for x, cell := range row {
			if cell.Rune != '◇' {
				continue
			}
			diamonds++
			if id := m.markAt(x, y); id != "pr:8" {
				t.Fatalf("scrolled cell %d,%d named %q, want pr:8", x, y, id)
			}
			clicked, _ := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
			if got := clicked.(model).selectionID(); got != "pr:8" {
				t.Fatalf("click in scrolled viewport selected %q", got)
			}
		}
	}
	if diamonds == 0 {
		t.Fatal("no PR diamond in the scrolled viewport")
	}
}

var sgrPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// plainView keeps the assembled view's exact rows and contents, dropping only
// the colour codes so a reviewer can read the golden as text.
func plainView(m model) string { return sgrPattern.ReplaceAllString(m.View().Content, "") }

// These goldens capture the whole rendered view, not detailLines alone, at a
// normal and a short terminal. They prove the URL and controls stay visible.
func TestTUIViewGolden(t *testing.T) {
	data, err := os.ReadFile("../../testdata/fixtures/small.json")
	if err != nil {
		t.Fatal(err)
	}
	var snap repo.Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatal(err)
	}
	base := uiModel(snap)
	base.details = true
	base.selectID("commit:" + snap.Commits[0].SHA)
	for _, tt := range []struct {
		name          string
		width, height int
	}{
		{"normal", 100, 30},
		{"short", 60, 8},
	} {
		m := base
		m.width, m.height = tt.width, tt.height
		got := plainView(m)
		if lines := strings.Count(got, "\n") + 1; lines > tt.height {
			t.Fatalf("%s view has %d lines for %d rows", tt.name, lines, tt.height)
		}
		for _, want := range []string{
			"https://github.com/acme/widgets/commit/",
			"tab / arrows / mouse  ·  enter details  ·  o browser  ·  q",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("%s view lost %q:\n%s", tt.name, want, got)
			}
		}
		path := "../../testdata/golden/tui-view-" + tt.name + ".txt"
		if *updateTUI {
			if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if got != string(want) {
			t.Fatalf("rendered view differs from %s:\n got:\n%s\nwant:\n%s", path, got, want)
		}
	}
}

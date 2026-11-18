package main

import (
	"encoding/json"
	"fmt"
	"os"
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
		{scene.Mark{ID: "deployment:4", Kind: "deployment", Label: "prod/eu"}, "https://github.com/acme/widgets/deployments/prod%2Feu"},
	} {
		if got := markURL(m, tt.mark); got != tt.want {
			t.Fatalf("%s: got %q, want %q", tt.mark.ID, got, tt.want)
		}
	}
	if got := markURL(m, scene.Mark{ID: "unknown:1", Kind: "unknown"}); got != "" {
		t.Fatalf("unknown kind got a URL: %q", got)
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

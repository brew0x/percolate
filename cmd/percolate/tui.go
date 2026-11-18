package main

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/brew0x/percolate/internal/live"
	"github.com/brew0x/percolate/internal/repo"
	"github.com/brew0x/percolate/internal/scene"
)

// The TUI owns selection, inspection and terminal input. It never invents a
// repository object: every field below is read from the current snapshot, and
// every link is built only from a known owner/repo and a recorded identity.

type tickMsg struct{}
type pollMsg struct{}
type browserMsg struct{ err error }

type snapshotMsg struct {
	snap repo.Snapshot
	err  error
}

func tick() tea.Cmd {
	return tea.Tick(125*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

// poll schedules the next live read on GitHub's poll interval.
func poll() tea.Cmd {
	return tea.Tick(live.Interval, func(time.Time) tea.Msg { return pollMsg{} })
}

type model struct {
	s                              scene.Scene
	prev, cur                      repo.Snapshot
	fetch                          live.Fetcher
	web, slug                      string
	transitionStart                int
	transiting, refreshFailed      bool
	frame, width, height, selected int
	selectedID                     string
	details                        bool
	notice                         string
}

// markIndex finds a mark by its recorded identity. A refresh can insert a
// commit that sorts ahead of the selected one, so a numeric index alone is not
// a stable identity for the highlight or an open detail panel.
func markIndex(s scene.Scene, id string) int {
	for i, m := range s.Marks {
		if m.ID == id {
			return i
		}
	}
	return -1
}

// selectionID is the identity currently selected. A caller that sets only the
// numeric index still selects the mark at that index, so older and test-built
// models keep their behaviour.
func (m model) selectionID() string {
	if m.selectedID != "" {
		return m.selectedID
	}
	if m.selected >= 0 && m.selected < len(m.s.Marks) {
		return m.s.Marks[m.selected].ID
	}
	return ""
}

// moveSelection advances the numeric index and its identity together.
func (m *model) moveSelection(delta int) {
	if len(m.s.Marks) == 0 {
		return
	}
	m.selected = (m.selected + delta + len(m.s.Marks)) % len(m.s.Marks)
	m.selectedID = m.s.Marks[m.selected].ID
}

// selectID points the cursor at an identity already in the drawn scene, so a
// mouse click or hover selects exactly the mark under the terminal cell.
func (m *model) selectID(id string) {
	s, _ := m.viewScene()
	if i := markIndex(s, id); i >= 0 {
		m.selected, m.selectedID = i, id
	}
}

// viewScene is the scene actually on screen at this frame. View, mouse hit
// testing and the detail panel all use it, so they cannot disagree about which
// object a cell or a selection names.
func (m model) viewScene() (scene.Scene, int) {
	s, frame := m.s, m.frame
	if m.transiting {
		frame = max(0, m.frame-m.transitionStart)
		s = scene.Transition(m.prev, m.cur, frame)
	}
	return s, frame
}

func (m model) selectedMark(s scene.Scene) (int, scene.Mark, bool) {
	selected := markIndex(s, m.selectedID)
	if selected < 0 {
		selected = min(m.selected, len(s.Marks)-1)
	}
	if selected < 0 || selected >= len(s.Marks) {
		return -1, scene.Mark{}, false
	}
	return selected, s.Marks[selected], true
}

// reserve is the footer height. The detail panel is a block of recorded fields
// above the key hints; the extra rows come out of the landscape, never out of
// a separate renderer that could disagree with the frame.
func (m model) reserve() int {
	if m.details {
		return 10
	}
	return 1
}

func (m model) Init() tea.Cmd {
	if m.fetch != nil {
		return tea.Batch(tick(), poll())
	}
	return tick()
}

// fetchCmd runs one bounded live read. It is nil for fixtures and renders, so
// replayed views never touch the network after their first load.
func (m model) fetchCmd() tea.Cmd {
	if m.fetch == nil {
		return nil
	}
	fetch := m.fetch
	return func() tea.Msg {
		snap, err := fetch(context.Background())
		return snapshotMsg{snap: snap, err: err}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tickMsg:
		m.frame++
		if m.transiting && m.frame-m.transitionStart >= scene.TransitionFrames {
			m.transiting = false
		}
		return m, tick()
	case pollMsg:
		return m, m.fetchCmd()
	case snapshotMsg:
		if v.err != nil {
			// A failed refresh is a status note, never a fabricated scene.
			m.refreshFailed = true
			return m, poll()
		}
		m.refreshFailed = false
		keep := m.selectionID()
		if !live.Equal(m.cur, v.snap) {
			m.prev, m.cur = m.cur, v.snap
			m.s = scene.Build(v.snap)
			m.transitionStart = m.frame
			m.transiting = true
			// Keep the same object selected when it still exists. Reconciling
			// by identity stops an inserted commit from silently taking over
			// the highlight and any open detail panel.
			if i := markIndex(m.s, keep); i >= 0 {
				m.selected, m.selectedID = i, keep
			} else if len(m.s.Marks) > 0 {
				m.selected, m.selectedID = 0, m.s.Marks[0].ID
			} else {
				m.selected, m.selectedID = 0, ""
			}
		} else if !v.snap.Now.Equal(m.cur.Now) {
			// Same recorded state, a fresh capture time. Build ages commit
			// styles against the recorded clock, so take the new snapshot
			// without starting a transition: stillness still means nothing
			// moved, but a commit crossing the age threshold still dims.
			m.cur = v.snap
			m.s = scene.Build(v.snap)
		}
		return m, poll()
	case browserMsg:
		if v.err != nil {
			m.notice = "could not open browser"
		} else {
			m.notice = "opened in browser"
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.width = v.Width
		m.height = v.Height
	case tea.KeyPressMsg:
		m.notice = ""
		switch v.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "tab", "right", "down":
			m.moveSelection(1)
		case "shift+tab", "left", "up":
			m.moveSelection(-1)
		case "enter":
			m.details = !m.details
		case "esc":
			m.details = false
		case "o":
			s, _ := m.viewScene()
			_, mark, ok := m.selectedMark(s)
			if !ok {
				return m, nil
			}
			u := markURL(m, mark)
			if u == "" {
				m.notice = "no GitHub URL in a replay"
				return m, nil
			}
			return m, openBrowserCmd(u)
		}
	case tea.MouseMsg:
		ev := v.Mouse() // zero-based terminal cell, (0,0) top-left
		m.notice = ""
		switch ev.Button {
		case tea.MouseWheelUp:
			m.moveSelection(-1)
			return m, nil
		case tea.MouseWheelDown:
			m.moveSelection(1)
			return m, nil
		}
		// A click or a hover selects the mark drawn nearest that cell. Empty
		// space leaves the cursor where it is; there is no invented mark.
		if id := m.markAt(ev.X, ev.Y); id != "" && id != m.selectionID() {
			m.selectID(id)
		}
	}
	return m, nil
}

// glyphMatches reports whether a drawn rune could be this mark. A running run
// cycles through pulse glyphs and a job strip uses outcome glyphs, so any of
// them still names that run.
func glyphMatches(mark scene.Mark, r rune) bool {
	if r == mark.Glyph {
		return true
	}
	if mark.Kind != "run" {
		return false
	}
	switch r {
	case '✓', '✕', '−', '◌', '◔', '◑', '◕':
		return true
	}
	return false
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// rowMatches uses the same rank-to-row rule as the renderer for marks that
// share a glyph. Without it every commit bead would name every other commit:
// the glyph is the same, only the recorded rank places it on a row.
func (m model) rowMatches(mark scene.Mark, y int) bool {
	if mark.Kind != "commit" && mark.Kind != "run" {
		return true
	}
	height := max(0, m.height-m.reserve())
	bottom := max(4, height*3/5)
	row := 4
	if mark.Total > 1 {
		row += mark.Rank * (bottom - 4) / (mark.Total - 1)
	}
	row = max(3, row-mark.Drip)
	return absInt(row-y) <= 2
}

// bandAt disambiguates the release band, where one glyph stands for each
// release or deployment in a row. Their number and left-to-right order come
// from the same scene the frame drew, so a click names the drawn object.
func bandAt(s scene.Scene, g scene.Grid, x, y int, r rune) string {
	if y >= len(g) {
		return ""
	}
	var releaseX []int
	for xx, c := range g[y] {
		if c.Rune == '◆' {
			releaseX = append(releaseX, xx)
		}
	}
	var releases []string
	for _, mark := range s.Marks {
		if mark.Kind == "release" {
			releases = append(releases, mark.ID)
		}
	}
	groupOf := func(cellX int) int {
		group := -1
		for _, rx := range releaseX {
			if rx < cellX {
				group++
			}
		}
		return group
	}
	if r == '◆' {
		for i, rx := range releaseX {
			if rx == x && i < len(releases) {
				return releases[i]
			}
		}
		return ""
	}
	group := groupOf(x)
	index := 0
	for xx, c := range g[y] {
		if xx >= x {
			break
		}
		if c.Rune == '▣' && groupOf(xx) == group {
			index++
		}
	}
	var grouped, standalone []string
	for _, mark := range s.Marks {
		if mark.Kind != "deployment" {
			continue
		}
		if mark.ReleaseTag == "" {
			standalone = append(standalone, mark.ID)
		} else if group >= 0 && group < len(releases) &&
			mark.ReleaseTag == strings.TrimPrefix(releases[group], "release:") {
			grouped = append(grouped, mark.ID)
		}
	}
	deployments := grouped
	if group < 0 {
		deployments = standalone
	} else if index >= len(grouped) {
		// Standalone deployments are drawn past the last release's branches.
		index -= len(grouped)
		deployments = standalone
	}
	if index >= 0 && index < len(deployments) {
		return deployments[index]
	}
	return ""
}

// markAt returns the identity drawn at or near a terminal cell. It reads the
// same frame View draws, so a click chooses what the person can actually see.
// The exact cell wins: when marks overlap, its rune belongs to the one drawn
// last. A small radius makes tiny glyphs clickable without inventing a mark.
func (m model) markAt(x, y int) string {
	if x < 0 || y < 0 {
		return ""
	}
	s, frame := m.viewScene()
	height := max(0, m.height-m.reserve())
	if m.width < 5 || height < 7 {
		return "" // the renderer only draws the title in a tiny frame
	}
	g := scene.Frame(s, frame, m.width, height)
	if y >= len(g) || x >= len(g[y]) {
		return ""
	}
	if r := g[y][x].Rune; r != ' ' {
		if r == '◆' || r == '▣' {
			if id := bandAt(s, g, x, y, r); id != "" {
				return id
			}
		}
		for _, mark := range s.Marks { // later marks are drawn on top
			if glyphMatches(mark, r) && m.rowMatches(mark, y) {
				return mark.ID
			}
		}
	}
	const radius = 2
	best, bestDist := "", radius+1
	for _, mark := range s.Marks {
		for dy := -radius; dy <= radius; dy++ {
			yy := y + dy
			if yy < 0 || yy >= len(g) {
				continue
			}
			for dx := -radius; dx <= radius; dx++ {
				xx := x + dx
				if xx < 0 || xx >= len(g[yy]) {
					continue
				}
				r := g[yy][xx].Rune
				if r != ' ' && glyphMatches(mark, r) && m.rowMatches(mark, y) {
					if d := absInt(dx) + absInt(dy); d < bestDist {
						best, bestDist = mark.ID, d
					}
				}
			}
		}
	}
	return best
}

// suffix returns the recorded identity part of a mark ID ("commit:abc" →
// "abc"). It is never used to fabricate data when that identity is missing
// from the snapshot.
func suffix(id string) string {
	_, v, _ := strings.Cut(id, ":")
	return v
}

func lookup(snap repo.Snapshot, mark scene.Mark) (any, bool) {
	v := suffix(mark.ID)
	switch mark.Kind {
	case "commit":
		for _, c := range snap.Commits {
			if c.SHA == v {
				return c, true
			}
		}
	case "pr":
		for _, p := range snap.PullRequests {
			if fmt.Sprint(p.Number) == v {
				return p, true
			}
		}
	case "issue":
		for _, i := range snap.Issues {
			if fmt.Sprint(i.Number) == v {
				return i, true
			}
		}
	case "run":
		for _, r := range snap.Runs {
			if fmt.Sprint(r.ID) == v {
				return r, true
			}
		}
	case "release":
		for _, r := range snap.Releases {
			if r.Tag == v {
				return r, true
			}
		}
	case "deployment":
		for _, d := range snap.Deployments {
			if fmt.Sprint(d.ID) == v {
				return d, true
			}
		}
	}
	return nil, false
}

type field struct{ name, value string }

func blank(s string) string {
	if s == "" {
		return "not recorded"
	}
	return s
}

func recordedTime(t time.Time) string {
	if t.IsZero() {
		return "not recorded"
	}
	return t.Format(time.RFC3339)
}

// fields reports the recorded fields of the selected object. A run without a
// recorded job list says so, and a missing state is never read as success.
func fields(mark scene.Mark, obj any) []field {
	switch v := obj.(type) {
	case repo.Commit:
		parents := "none recorded"
		if len(v.Parents) > 0 {
			parents = strings.Join(v.Parents, ", ")
		}
		return []field{{"sha", v.SHA}, {"branch", blank(v.Branch)}, {"author", blank(v.Author)}, {"parents", parents}, {"time", recordedTime(v.Time)}}
	case repo.PullRequest:
		reviews := "none recorded"
		if len(v.Reviews) > 0 {
			parts := make([]string, 0, len(v.Reviews))
			for _, r := range v.Reviews {
				parts = append(parts, r.Author+": "+r.State)
			}
			reviews = strings.Join(parts, ", ")
		}
		word := "waiting"
		switch scene.ReviewStateOf(v.Reviews) {
		case scene.ReviewApproved:
			word = "approved"
		case scene.ReviewChanges:
			word = "changes requested"
		}
		return []field{{"number", fmt.Sprint(v.Number)}, {"head", blank(v.Head)}, {"base", blank(v.Base)}, {"state", blank(v.State)}, {"reviews", reviews}, {"resolved", word}}
	case repo.Issue:
		closedBy := "none recorded"
		if v.ClosedBy != 0 {
			closedBy = fmt.Sprint(v.ClosedBy)
		}
		return []field{{"number", fmt.Sprint(v.Number)}, {"title", blank(v.Title)}, {"state", blank(v.State)}, {"closed_by", closedBy}}
	case repo.WorkflowRun:
		jobs := "jobs not recorded"
		if v.Jobs != nil {
			jobs = fmt.Sprintf("%d jobs", len(v.Jobs))
			var failing []string
			for _, j := range v.Jobs {
				if j.Failed() {
					failing = append(failing, j.Name)
				}
			}
			if len(failing) > 0 {
				jobs += "; failing: " + strings.Join(failing, ", ")
			}
		}
		return []field{{"id", fmt.Sprint(v.ID)}, {"status", blank(v.Status)}, {"conclusion", blank(v.Conclusion)}, {"head_sha", blank(v.HeadSHA)}, {"jobs", jobs}}
	case repo.Release:
		artifacts := "artifacts not recorded"
		if len(v.Artifacts) > 0 {
			parts := make([]string, 0, len(v.Artifacts))
			for _, a := range v.Artifacts {
				parts = append(parts, a.Name)
			}
			artifacts = strings.Join(parts, ", ")
		}
		return []field{{"tag", blank(v.Tag)}, {"head_sha", blank(v.HeadSHA)}, {"time", recordedTime(v.Time)}, {"artifacts", artifacts}}
	case repo.Deployment:
		release := "no matching release"
		if mark.ReleaseTag != "" {
			release = mark.ReleaseTag
		}
		return []field{{"id", fmt.Sprint(v.ID)}, {"environment", blank(v.Environment)}, {"state", blank(v.State)}, {"head_sha", blank(v.HeadSHA)}, {"release", release}}
	}
	// A departing object may no longer be in the current snapshot. Its scene
	// detail still records what was on screen, without claiming new fields.
	return []field{{"recorded", blank(mark.Detail)}}
}

// browserBase turns the configured GitHub API host into a web host. The
// default is github.com; an Enterprise base keeps its scheme and host, never
// a path, query or credential.
func browserBase() string {
	base := strings.TrimRight(os.Getenv("PERCOLATE_GITHUB_URL"), "/")
	if base == "" {
		return "https://github.com"
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "https://github.com"
	}
	if u.Host == "api.github.com" {
		return "https://github.com"
	}
	return u.Scheme + "://" + u.Host
}

// slugFromArgs is the owner/repo the live view fetches. A fixture has no
// repository of its own, so it stays empty and never produces a guessed URL.
func slugFromArgs(args []string) string {
	if len(args) == 1 && args[0] != "" {
		return args[0]
	}
	return ""
}

// markURL is a web URL for a recorded identity, built only when a repository
// is known. Each path is GitHub's own route for that object kind.
func markURL(m model, mark scene.Mark) string {
	if m.web == "" || m.slug == "" {
		return ""
	}
	base := strings.TrimRight(m.web, "/") + "/" + m.slug + "/"
	v := suffix(mark.ID)
	switch mark.Kind {
	case "commit":
		return base + "commit/" + url.PathEscape(v)
	case "pr":
		return base + "pull/" + url.PathEscape(v)
	case "issue":
		return base + "issues/" + url.PathEscape(v)
	case "run":
		return base + "actions/runs/" + url.PathEscape(v)
	case "release":
		return base + "releases/tag/" + url.PathEscape(v)
	case "deployment":
		return base + "deployments/" + url.PathEscape(mark.Label)
	}
	return ""
}

// fit truncates a panel line to the terminal width, so a long SHA or URL
// cannot wrap into the key hints below it.
func fit(s string, width int) string {
	r := []rune(s)
	if width < 1 {
		return ""
	}
	if len(r) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return string(r[:width-1]) + "…"
}

// detailLines is the panel text: the object's recorded fields followed by its
// GitHub URL, or an explicit note when a replay has no repository to link to.
func detailLines(m model, mark scene.Mark) []string {
	obj, ok := lookup(m.cur, mark)
	if !ok {
		obj, ok = lookup(m.prev, mark)
	}
	fs := fields(mark, obj)
	if len(fs) > 6 {
		fs = fs[:6] // the panel reserves six field rows
	}
	lines := []string{mark.Kind + "  " + mark.Label}
	for _, f := range fs {
		lines = append(lines, fmt.Sprintf("  %-10s %s", f.name, f.value))
	}
	if u := markURL(m, mark); u != "" {
		lines = append(lines, fmt.Sprintf("  %-10s %s", "github", u))
	} else {
		lines = append(lines, "  github     no URL: replay has no repository")
	}
	width := max(12, m.width-2)
	for i, line := range lines {
		lines[i] = fit(line, width)
	}
	return lines
}

// browserCommand picks a launcher without a shell interpreting the URL.
// BROWSER, when set, is split into an executable and its arguments.
func browserCommand(rawURL string) *exec.Cmd {
	if b := strings.Fields(os.Getenv("BROWSER")); len(b) > 0 {
		args := append(append([]string{}, b[1:]...), rawURL)
		return exec.Command(b[0], args...)
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", rawURL)
	case "windows":
		return exec.Command("cmd", "/c", "start", "", rawURL)
	default:
		return exec.Command("xdg-open", rawURL)
	}
}

func openBrowserCmd(rawURL string) tea.Cmd {
	if rawURL == "" {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil
	}
	return func() tea.Msg {
		if err := browserCommand(rawURL).Start(); err != nil {
			return browserMsg{err: err}
		}
		return browserMsg{}
	}
}

func (m model) View() tea.View {
	s, frame := m.viewScene()
	_, mark, hasSelection := m.selectedMark(s)
	if hasSelection {
		s.SelectedID = mark.ID
		count := scene.VisibleLanes(m.width)
		if mark.Lane < s.FirstLane || mark.Lane >= s.FirstLane+count {
			s.FirstLane = mark.Lane
		}
	}
	reserve := m.reserve()
	height := max(0, m.height-reserve)
	text := paint(scene.Frame(s, frame, m.width, height), true)
	footer := "tab / arrows / mouse  ·  enter details  ·  o browser  ·  q"
	if m.notice != "" {
		footer = m.notice + "  ·  " + footer
	}
	if m.refreshFailed {
		footer = "refresh failed; retrying  ·  " + footer
	}
	if m.details && hasSelection {
		footer = strings.Join(detailLines(m, mark), "\n") + "\n\n" + footer
	}
	footer = lipgloss.NewStyle().Width(max(0, m.width)).MaxWidth(max(0, m.width)).MaxHeight(reserve).Render(footer)
	if height > 0 {
		text += "\n"
	}
	view := tea.NewView(text + footer)
	view.AltScreen = true
	view.MouseMode = tea.MouseModeAllMotion
	return view
}

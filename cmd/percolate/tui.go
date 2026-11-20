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
	"unicode"

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
	transiting                     bool
	frame, width, height, selected int
	selectedID                     string
	details                        bool
	notice                         string
	// refreshErr is the last failed live read's one-line reason. Empty means
	// the last read succeeded; there is no separate flag that could disagree.
	refreshErr string
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
	if i := markIndex(m.s, id); i >= 0 {
		m.selected, m.selectedID = i, id
		return
	}
	// A departing mark exists only in the transition scene, not in m.s; its
	// identity is still enough to keep it selected while it fades.
	if s, _ := m.viewScene(); markIndex(s, id) >= 0 {
		m.selectedID = id
	}
}

// viewScene is the scene actually on screen at this frame: the transition
// scene, the selection highlight and the viewport that keeps the selected mark
// visible. View, mouse hit testing and the detail panel all use it, so they
// cannot disagree about which object a cell or a selection names.
func (m model) viewScene() (scene.Scene, int) {
	s, frame := m.s, m.frame
	if m.transiting {
		frame = max(0, m.frame-m.transitionStart)
		s = scene.Transition(m.prev, m.cur, frame)
	}
	_, mark, ok := m.selectedMark(s)
	if ok {
		s.SelectedID = mark.ID
		count := scene.VisibleLanes(m.width)
		if mark.Lane < s.FirstLane || mark.Lane >= s.FirstLane+count {
			s.FirstLane = mark.Lane
		}
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

// keyHints teaches a new visitor every action. The shorter spellings keep the
// same actions reachable when the terminal itself is too narrow for the full
// wording; the status never displaces them.
const (
	keyHints        = "tab / arrows / mouse  ·  enter details  ·  o browser  ·  q"
	compactKeyHints = "tab/arrows  ·  enter  ·  o  ·  q"
	tinyKeyHints    = "o q"
)

// failureReason is the failed read's own message, flattened to one printable
// line. Repository or server text cannot add a footer row or a terminal
// instruction, and a long reason cannot push the controls out of the frame.
func failureReason(err error) string {
	if err == nil {
		return ""
	}
	s := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, err.Error())
	return fit(clean(strings.TrimSpace(s)), 80)
}

// failureStatus builds the failed-read row. The reason is data; "refresh
// failed" and the retry note are framing. On a narrow row the framing goes
// first, so an actionable detail such as a rate-limit reset time survives
// instead of being clipped off the end of the line.
func failureStatus(reason string, width int) string {
	for _, s := range []string{
		"refresh failed: " + reason + "; retrying",
		"failed: " + reason,
		reason,
	} {
		if lipgloss.Width(s) <= width {
			return s
		}
	}
	return fit(reason, width)
}

// footerKey returns the status and hint rows below the landscape. A long
// status gets its own row rather than eating the controls: the old single row
// let one notice hide `o` and `q` exactly when someone needed them to inspect
// or leave a live window during a failed poll.
func (m model) footerKey() []string {
	hint := keyHints
	if lipgloss.Width(hint) > m.width {
		hint = compactKeyHints
	}
	if lipgloss.Width(hint) > m.width {
		hint = tinyKeyHints
	}
	hint = fit(clean(hint), m.width)
	status := ""
	if m.refreshErr != "" {
		// A failure outranks a transient browser notice: both compete for the
		// same row, and the reason is what someone needs to report it.
		status = failureStatus(m.refreshErr, m.width)
	} else {
		status = fit(clean(m.notice), m.width)
	}
	if status == "" {
		return []string{hint}
	}
	const sep = "  ·  "
	if lipgloss.Width(status)+lipgloss.Width(sep)+lipgloss.Width(hint) <= m.width {
		return []string{status + sep + hint}
	}
	return []string{status, hint}
}

// footer is the exact text below the landscape. It keeps the key hints last
// and trims detail rows from the top when the terminal is short, so neither
// the URL nor the controls fall below the visible area. The landscape gets
// every remaining row; no other renderer can disagree with it.
func (m model) footer() []string {
	key := m.footerKey()
	if !m.details {
		return key
	}
	s, _ := m.viewScene()
	_, mark, ok := m.selectedMark(s)
	if !ok {
		return key
	}
	detail := detailLines(m, mark)
	lines := append(append([]string{}, detail...), "")
	lines = append(lines, key...)
	budget := max(1, m.height)
	if len(lines) <= budget {
		return lines
	}
	// Drop the blank separator, then detail fields from the top. The recorded
	// URL and the key rows survive whenever there is room for them.
	lines = append(append([]string{}, detail...), key...)
	for len(lines) > budget && len(lines) > len(key) {
		lines = lines[1:]
	}
	if len(lines) > budget {
		lines = lines[len(lines)-budget:]
	}
	return lines
}

// reserve is the footer height, bounded by the terminal so the whole view fits.
func (m model) reserve() int { return len(m.footer()) }

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
			// A failed refresh is a status note with its own reason, never a
			// fabricated scene and never a stale reason from an earlier poll.
			m.refreshErr = failureReason(v.err)
			return m, poll()
		}
		m.refreshErr = ""
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

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// markAt returns the identity drawn at or near a terminal cell. The scene
// reports the owner of each cell in the very frame View draws, using the same
// viewport and selection, so a repeated glyph in another lane or an offscreen
// lane cannot answer for this one. The exact cell wins; a small radius forgives
// tiny glyphs without inventing a mark on empty space far from any drawing.
func (m model) markAt(x, y int) string {
	if x < 0 || y < 0 {
		return ""
	}
	s, frame := m.viewScene()
	height := max(0, m.height-m.reserve())
	if m.width < 5 || height < 7 {
		return "" // the renderer only draws the title in a tiny frame
	}
	_, owners := scene.FrameWithOwners(s, frame, m.width, height)
	if y >= len(owners) || x >= len(owners[y]) {
		return ""
	}
	if id := owners[y][x]; id != "" {
		return id
	}
	const radius = 2
	best, bestDist := "", radius+1
	for dy := -radius; dy <= radius; dy++ {
		yy := y + dy
		if yy < 0 || yy >= len(owners) {
			continue
		}
		for dx := -radius; dx <= radius; dx++ {
			xx := x + dx
			if xx < 0 || xx >= len(owners[yy]) {
				continue
			}
			d := absInt(dx) + absInt(dy)
			if d == 0 || d >= bestDist {
				continue
			}
			if id := owners[yy][xx]; id != "" {
				best, bestDist = id, d
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
		// The recorded deployment ID, not its environment name: two
		// deployments to the same environment must open different objects.
		return base + "deployments/" + url.PathEscape(v)
	}
	return ""
}

// clean replaces every non-printable rune with a visible question mark before
// a recorded value is measured or drawn. Repository text can hold newlines,
// escapes or bidi controls; none of them may become a panel row or a terminal
// instruction. It is applied to labels and field names as well as values.
func clean(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsPrint(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune('?')
		}
	}
	return b.String()
}

// fit truncates a panel line to the terminal's display width, so a long URL or
// a wide CJK/emoji glyph cannot wrap into the key hints below it. Lip gloss
// wraps by terminal cells, not runes: measuring in runes would let one wide
// character push a row into the next and hide the URL or the controls.
func fit(s string, width int) string {
	if width < 1 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		w := lipgloss.Width(string(r))
		if used+w > width-1 {
			break
		}
		b.WriteRune(r)
		used += w
	}
	return b.String() + "…"
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
	lines := []string{clean(mark.Kind) + "  " + clean(mark.Label)}
	for _, f := range fs {
		lines = append(lines, fmt.Sprintf("  %-10s %s", clean(f.name), clean(f.value)))
	}
	if u := markURL(m, mark); u != "" {
		lines = append(lines, fmt.Sprintf("  %-10s %s", "github", clean(u)))
	} else {
		lines = append(lines, "  github     no URL: replay has no repository")
	}
	width := max(1, m.width-2)
	for i, line := range lines {
		lines[i] = fit(line, width)
	}
	return lines
}

// browserCommand picks a launcher without a shell interpreting the URL.
// BROWSER, when set, is split into an executable and its arguments.
func browserCommand(rawURL string) *exec.Cmd {
	return browserCommandFor(runtime.GOOS, rawURL)
}

// browserCommandFor makes the Windows launcher testable. rundll32 calls the
// URL handler directly; cmd /c start would hand repository-derived text to a
// shell, where metacharacters could be parsed as another command.
func browserCommandFor(goos, rawURL string) *exec.Cmd {
	if b := strings.Fields(os.Getenv("BROWSER")); len(b) > 0 {
		args := append(append([]string{}, b[1:]...), rawURL)
		return exec.Command(b[0], args...)
	}
	switch goos {
	case "darwin":
		return exec.Command("open", rawURL)
	case "windows":
		return exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", rawURL)
	default:
		return exec.Command("xdg-open", rawURL)
	}
}

func openBrowserCmd(rawURL string) tea.Cmd {
	if rawURL == "" {
		return nil
	}
	// Only a plain web URL is ever handed to a launcher. Whitespace, control
	// characters and credentials have no place in one of our recorded links.
	// Unicode whitespace and C1 controls are rejected too, not just ASCII ones.
	if strings.IndexFunc(rawURL, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) >= 0 {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
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
	lines := m.footer()
	height := max(0, m.height-len(lines))
	text := paint(scene.Frame(s, frame, m.width, height), true)
	footer := lipgloss.NewStyle().Width(max(0, m.width)).MaxWidth(max(0, m.width)).MaxHeight(len(lines)).Render(strings.Join(lines, "\n"))
	if height > 0 {
		text += "\n"
	}
	view := tea.NewView(text + footer)
	view.AltScreen = true
	view.MouseMode = tea.MouseModeAllMotion
	return view
}

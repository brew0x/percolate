// Package scene turns recorded state into deterministic terminal landscapes.
package scene

import (
	"fmt"
	"github.com/brew0x/percolate/internal/repo"
	"sort"
	"strings"
	"time"
)

type Style uint8

const (
	Plain Style = iota
	LaneStyle
	Fresh
	Faded
	Pass
	Fail
	Pulse
	ReleaseStyle
	Selected
)

type Cell struct {
	Rune  rune
	Style Style
}
type Grid [][]Cell

func (g Grid) String() string {
	var b strings.Builder
	for y, row := range g {
		if y > 0 {
			b.WriteByte('\n')
		}
		for _, c := range row {
			b.WriteRune(c.Rune)
		}
	}
	return b.String()
}

type Mark struct {
	ID, Kind, Label, Detail string
	Lane, Base, Rank, Total int
	// Drip is how many rows above its resting row a mark draws while it is
	// entering during a transition, counting down to zero as it settles. A
	// newest commit uses it instead of a negative rank so its fall, and the
	// parent paths and anchored CI that follow it, stay clear of the lane
	// names. Frame applies it even when Total is one, where Rank is ignored.
	Drip int
	// Departing marks were present in the previous snapshot and are absent
	// from the new one; Transition appends them while they fade. Keeping them
	// identifiable lets the issue layer resolve closing references against
	// only the pull requests the new snapshot still holds, so a departed PR
	// never answers a surviving issue's rank lookup.
	Departing bool
	Glyph     rune
	Style     Style
	Running   bool
	// Review is the flow-level review state of a pull request, resolved by
	// Build from its recorded reviews; other kinds carry the zero value
	// (waiting), which only the pull-request drawing consults.
	Review ReviewState
}

// Link is a recorded commit-parent edge; missing parents never invent junctions.
type Link struct {
	Child, Parent Mark
}

type Scene struct {
	Links      []Link
	Lanes      []string
	Marks      []Mark
	FirstLane  int
	SelectedID string
}

// Build owns its slices and never changes the supplied snapshot.
func Build(s repo.Snapshot) Scene {
	out := Scene{}
	lanes := map[string]int{}
	addLane := func(name string) int {
		if i, ok := lanes[name]; ok {
			return i
		}
		i := len(out.Lanes)
		lanes[name] = i
		out.Lanes = append(out.Lanes, name)
		return i
	}
	for _, b := range s.Branches {
		addLane(b.Name)
	}
	commits := append([]repo.Commit(nil), s.Commits...)
	sort.SliceStable(commits, func(i, j int) bool {
		if commits[i].Time.Equal(commits[j].Time) {
			return commits[i].SHA < commits[j].SHA
		}
		return commits[i].Time.After(commits[j].Time)
	})
	for _, c := range commits {
		addLane(c.Branch)
	}
	bySHA := map[string]Mark{}
	for rank, c := range commits {
		style := Fresh
		if s.Now.Sub(c.Time) > 48*time.Hour {
			style = Faded
		}
		m := Mark{ID: "commit:" + c.SHA, Kind: "commit", Label: short(c.SHA), Detail: fmt.Sprintf("%s · %s · %s", c.SHA, c.Author, c.Time.Format(time.RFC3339)), Lane: lanes[c.Branch], Rank: rank, Total: len(commits), Glyph: '●', Style: style}
		out.Marks = append(out.Marks, m)
		bySHA[c.SHA] = m
	}
	// A secondary parent on another lane is evidence of a merge, unlike a
	// pull request state. Fade that lane only when its recorded tip was merged.
	tips := map[int]string{}
	for _, c := range commits {
		m := bySHA[c.SHA]
		if _, ok := tips[m.Lane]; !ok {
			tips[m.Lane] = c.SHA
		}
	}
	merged := map[int]bool{}
	for _, c := range commits {
		for _, sha := range c.Parents[min(1, len(c.Parents)):] {
			if p, ok := bySHA[sha]; ok && p.Lane != bySHA[c.SHA].Lane && tips[p.Lane] == sha {
				merged[p.Lane] = true
			}
		}
	}
	for i, m := range out.Marks {
		if merged[m.Lane] {
			m.Style = Faded
			out.Marks[i] = m
			bySHA[strings.TrimPrefix(m.ID, "commit:")] = m
		}
	}
	for _, c := range commits {
		for _, parent := range c.Parents {
			if p, ok := bySHA[parent]; ok {
				out.Links = append(out.Links, Link{Child: bySHA[c.SHA], Parent: p})
			}
		}
	}
	for i, p := range s.PullRequests {
		review := ReviewStateOf(p.Reviews)
		reviews := make([]string, 0, len(p.Reviews))
		for _, r := range p.Reviews {
			reviews = append(reviews, r.Author+": "+r.State)
		}
		detail := fmt.Sprintf("#%d %s → %s · %s", p.Number, p.Head, p.Base, p.State)
		if len(reviews) > 0 {
			detail += " · " + strings.Join(reviews, ", ")
		}
		detail += " · " + reviewWord(review)
		out.Marks = append(out.Marks, Mark{ID: fmt.Sprintf("pr:%d", p.Number), Kind: "pr", Label: fmt.Sprintf("#%d", p.Number), Detail: detail, Lane: addLane(p.Head), Base: addLane(p.Base), Rank: i, Total: len(s.PullRequests), Glyph: '◇', Review: review, Style: Fresh})
	}
	// Issues follow PRs so a closing reference resolves to the same head lane.
	out.Marks = append(out.Marks, buildIssues(s, addLane)...)
	for _, r := range s.Runs {
		anchor, ok := bySHA[r.HeadSHA]
		if !ok {
			anchor = Mark{Lane: addLane("unlinked"), Total: 1}
		}
		glyph, style := RunGlyph(r.Conclusion), Pass
		if r.Conclusion != "success" {
			style = Fail
		}
		running := r.Status != "completed"
		if running {
			glyph, style = '◌', Pulse
		}
		// A nil Jobs slice means the snapshot source never recorded jobs; a
		// numeric zero would state a fact the snapshot does not contain.
		jobs := "jobs not recorded"
		if r.Jobs != nil {
			jobs = fmt.Sprintf("%d jobs", len(r.Jobs))
		}
		out.Marks = append(out.Marks, Mark{ID: fmt.Sprintf("run:%d", r.ID), Kind: "run", Label: fmt.Sprintf("CI %d", r.ID), Detail: fmt.Sprintf("run %d · %s · %s · %s · %s", r.ID, r.Status, r.Conclusion, jobs, r.HeadSHA), Lane: anchor.Lane, Rank: anchor.Rank, Total: anchor.Total, Glyph: glyph, Style: style, Running: running})
	}
	for _, r := range s.Releases {
		anchor, ok := bySHA[r.HeadSHA]
		if !ok {
			anchor.Lane = addLane("unlinked")
		}
		out.Marks = append(out.Marks, Mark{ID: "release:" + r.Tag, Kind: "release", Label: r.Tag, Detail: r.Tag + " · " + r.HeadSHA, Lane: anchor.Lane, Glyph: '◆', Style: ReleaseStyle})
	}
	for _, d := range s.Deployments {
		anchor, ok := bySHA[d.HeadSHA]
		if !ok {
			anchor.Lane = addLane("unlinked")
		}
		out.Marks = append(out.Marks, Mark{ID: fmt.Sprintf("deployment:%d", d.ID), Kind: "deployment", Label: d.Environment, Detail: fmt.Sprintf("%s · %s · %s", d.Environment, d.State, d.HeadSHA), Lane: anchor.Lane, Glyph: '▣', Style: ReleaseStyle})
	}
	return out
}
func short(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

// RunGlyph keeps success, failure, and other completed outcomes distinct.
func RunGlyph(conclusion string) rune {
	switch conclusion {
	case "success":
		return '✓'
	case "failure", "timed_out", "startup_failure":
		return '✕'
	default:
		return '−'
	}
}

// pulseGlyph is the four-phase, eight-frame-per-phase running signal. Both
// Frame and Transition use it so a live settle and a still frame pulse alike.
func pulseGlyph(frame int) rune {
	cycle := []rune{'◌', '◔', '◑', '◕'}
	i := (((frame % 32) + 32) % 32) / 8
	return cycle[i]
}

// VisibleLanes is shared by viewport navigation and rendering.
func VisibleLanes(width int) int { return max(1, (width-4)/14) }

// Frame uses no time, I/O, randomness, or terminal state. Path cells show the
// union of the recorded segments through them; mark overlaps resolve in
// snapshot order, and the selected object is drawn last so it is inspectable.
func Frame(s Scene, frameIndex, width, height int) Grid {
	width = max(0, width)
	height = max(0, height)
	g := make(Grid, height)
	for y := range g {
		g[y] = make([]Cell, width)
		for x := range g[y] {
			g[y][x] = Cell{' ', Plain}
		}
	}
	put := func(x, y int, r rune, style Style) {
		if y >= 0 && y < height && x >= 0 && x < width {
			g[y][x] = Cell{r, style}
		}
	}
	label := func(x, y int, t string, limit int, style Style) {
		for i, r := range []rune(t) {
			if i >= limit {
				break
			}
			put(x+i, y, r, style)
		}
	}
	if width < 5 || height < 7 {
		label(0, 0, "percolate", width, Fresh)
		return g
	}
	count := min(VisibleLanes(width), len(s.Lanes))
	start := max(0, min(s.FirstLane, max(0, len(s.Lanes)-count)))
	span := max(1, (width-4)/max(1, count))
	xpos := func(lane int) int { return 3 + (lane-start)*span }
	visible := func(lane int) bool { return lane >= start && lane < start+count }
	label(2, 0, fmt.Sprintf("PERCOLATE  %d–%d/%d", min(start+1, len(s.Lanes)), start+count, len(s.Lanes)), width-2, Faded)
	for i := start; i < start+count; i++ {
		x := xpos(i)
		label(x, 2, s.Lanes[i], span-2, Fresh)
	}
	top, bottom := 4, max(4, height*3/5)
	// An entering mark rests above the commit region, in the gap between the
	// lane names and the first bead. Clamping there keeps its drip, and every
	// parent path following it, out of the lane-name row. The offset is part
	// of the mark, so this still applies when Total is one and Rank is unused.
	row := func(m Mark) int {
		y := top
		if m.Total > 1 {
			y += m.Rank * (bottom - top) / (m.Total - 1)
		}
		return max(top-1, y-m.Drip)
	}
	// Draw only recorded parent paths. Each path cell shows the union of the
	// recorded segments through it, so a bend never hides a through path and
	// draw order cannot erase a turn. Missing parents draw nothing; clipping
	// retains offscreen edges without inventing endpoints.
	type dirs struct{ up, down, left, right bool }
	type pathCell struct {
		x, y     int
		dirs     dirs
		allFaded bool
	}
	cells := map[[2]int]*pathCell{}
	segment := func(x, y int, d dirs, faded bool) {
		c, ok := cells[[2]int{x, y}]
		if !ok {
			c = &pathCell{x: x, y: y, allFaded: true}
			cells[[2]int{x, y}] = c
		}
		c.dirs.up = c.dirs.up || d.up
		c.dirs.down = c.dirs.down || d.down
		c.dirs.left = c.dirs.left || d.left
		c.dirs.right = c.dirs.right || d.right
		c.allFaded = c.allFaded && faded
	}
	for _, link := range s.Links {
		x, px := xpos(link.Child.Lane), xpos(link.Parent.Lane)
		y, py := row(link.Child), row(link.Parent)
		faded := link.Child.Style == Faded || link.Parent.Style == Faded
		if visible(link.Child.Lane) {
			lo, hi := min(y, py), max(y, py)
			for yy := lo; yy <= hi; yy++ {
				segment(x, yy, dirs{up: yy > lo, down: yy < hi}, faded)
			}
		}
		if x != px {
			lo, hi := max(0, min(x, px)), min(width-1, max(x, px))
			for xx := lo; xx <= hi; xx++ {
				segment(xx, py, dirs{left: xx > min(x, px), right: xx < max(x, px)}, faded)
			}
		}
	}
	for _, c := range cells {
		glyph := '─'
		switch {
		case c.dirs.up && c.dirs.down && c.dirs.left && c.dirs.right:
			glyph = '┼'
		case c.dirs.up && c.dirs.down && c.dirs.right:
			glyph = '├'
		case c.dirs.up && c.dirs.down && c.dirs.left:
			glyph = '┤'
		case c.dirs.down && c.dirs.left && c.dirs.right:
			glyph = '┬'
		case c.dirs.up && c.dirs.left && c.dirs.right:
			glyph = '┴'
		case c.dirs.up && c.dirs.right:
			glyph = '╰'
		case c.dirs.up && c.dirs.left:
			glyph = '╯'
		case c.dirs.down && c.dirs.right:
			glyph = '╭'
		case c.dirs.down && c.dirs.left:
			glyph = '╮'
		case c.dirs.up || c.dirs.down:
			glyph = '│'
		}
		style := LaneStyle
		if c.allFaded {
			style = Faded
		}
		put(c.x, c.y, glyph, style)
	}
	draw := func(m Mark, selected bool) {
		if m.Kind == "issue" {
			return // springs and closing-reference feeds are drawn by drawIssues
		}
		if !visible(m.Lane) {
			return
		}
		style := m.Style
		if selected {
			style = Selected
		}
		x := xpos(m.Lane)
		y := row(m)
		switch m.Kind {
		case "pr":
			y = min(height-4, bottom+1+m.Rank%max(1, height-bottom-4))
			bx := xpos(m.Base)
			bx = max(0, min(width-1, bx))
			for xx := min(x, bx); xx <= max(x, bx); xx++ {
				put(xx, y, '─', style)
			}
			arrow := convergenceGlyph(m.Review)
			if m.Base < start {
				arrow = '‹'
			} else if m.Base >= start+count {
				arrow = '›'
			}
			// The review state colors the convergence mark; an offscreen base
			// keeps the plain indicator, and a selected PR keeps the selected
			// style on everything it draws.
			arrowStyle := style
			if !selected && arrow != '‹' && arrow != '›' {
				arrowStyle = reviewStyle(m.Review, style)
			}
			put(bx, y, arrow, arrowStyle)
			put(x, y, m.Glyph, style)
			label(x+1, y-1, m.Label, span-2, style)
		case "release", "deployment":
			y = height - 2
			put(x, y, m.Glyph, style)
			label(x+2, y, m.Label, span-3, style)
		case "run":
			glyph := m.Glyph
			if m.Running {
				glyph = pulseGlyph(frameIndex)
				if ((frameIndex%32)+32)%32 < 16 && !selected {
					style = Faded
				}
			}
			put(x+2, y, glyph, style)
		default:
			put(x, y, m.Glyph, style)
		}
	}
	for _, m := range s.Marks {
		draw(m, false)
	}
	for _, m := range s.Marks {
		if m.ID == s.SelectedID {
			draw(m, true)
		}
	}
	drawIssues(g, s, issueView{width: width, height: height, top: top, bottom: bottom, span: span, xpos: xpos, visible: visible})
	return g
}

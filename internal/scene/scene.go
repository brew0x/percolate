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
	Glyph                   rune
	Style                   Style
	Running                 bool
}
type Scene struct {
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
	totals := map[string]int{}
	for _, c := range commits {
		addLane(c.Branch)
		totals[c.Branch]++
	}
	ranks := map[string]int{}
	bySHA := map[string]Mark{}
	for _, c := range commits {
		style := Fresh
		if s.Now.Sub(c.Time) > 48*time.Hour {
			style = Faded
		}
		m := Mark{ID: "commit:" + c.SHA, Kind: "commit", Label: short(c.SHA), Detail: fmt.Sprintf("%s · %s · %s", c.SHA, c.Author, c.Time.Format(time.RFC3339)), Lane: lanes[c.Branch], Rank: ranks[c.Branch], Total: totals[c.Branch], Glyph: '●', Style: style}
		ranks[c.Branch]++
		out.Marks = append(out.Marks, m)
		bySHA[c.SHA] = m
	}
	for i, p := range s.PullRequests {
		reviews := make([]string, 0, len(p.Reviews))
		for _, r := range p.Reviews {
			reviews = append(reviews, r.Author+": "+r.State)
		}
		out.Marks = append(out.Marks, Mark{ID: fmt.Sprintf("pr:%d", p.Number), Kind: "pr", Label: fmt.Sprintf("#%d", p.Number), Detail: fmt.Sprintf("#%d %s → %s · %s · %s", p.Number, p.Head, p.Base, p.State, strings.Join(reviews, ", ")), Lane: addLane(p.Head), Base: addLane(p.Base), Rank: i, Total: len(s.PullRequests), Glyph: '◇', Style: Fresh})
	}
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
		out.Marks = append(out.Marks, Mark{ID: fmt.Sprintf("run:%d", r.ID), Kind: "run", Label: fmt.Sprintf("CI %d", r.ID), Detail: fmt.Sprintf("run %d · %s · %s · %d jobs · %s", r.ID, r.Status, r.Conclusion, len(r.Jobs), r.HeadSHA), Lane: anchor.Lane, Rank: anchor.Rank, Total: anchor.Total, Glyph: glyph, Style: style, Running: running})
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

// VisibleLanes is shared by viewport navigation and rendering.
func VisibleLanes(width int) int { return max(1, (width-4)/14) }

// Frame uses no time, I/O, randomness, or terminal state. Overlaps are resolved
// in snapshot order; the selected object is drawn last so it is always inspectable.
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
		for y := 3; y < height-2; y++ {
			put(x, y, '│', LaneStyle)
		}
		put(x, height-2, '╵', LaneStyle)
	}
	top, bottom := 4, max(4, height*3/5)
	draw := func(m Mark, selected bool) {
		if !visible(m.Lane) {
			return
		}
		style := m.Style
		if selected {
			style = Selected
		}
		x := xpos(m.Lane)
		y := top
		if m.Total > 1 {
			y += m.Rank * (bottom - top) / (m.Total - 1)
		}
		switch m.Kind {
		case "pr":
			y = min(height-4, bottom+1+m.Rank%max(1, height-bottom-4))
			bx := xpos(m.Base)
			bx = max(0, min(width-1, bx))
			for xx := min(x, bx); xx <= max(x, bx); xx++ {
				put(xx, y, '─', style)
			}
			arrow := '▼'
			if m.Base < start {
				arrow = '‹'
			} else if m.Base >= start+count {
				arrow = '›'
			}
			put(bx, y, arrow, style)
			put(x, y, m.Glyph, style)
			label(x+1, y-1, m.Label, span-2, style)
		case "release", "deployment":
			y = height - 2
			put(x, y, m.Glyph, style)
			label(x+2, y, m.Label, span-3, style)
		case "run":
			glyph := m.Glyph
			if m.Running {
				cycle := []rune{'◌', '◔', '◑', '◕'}
				glyph = cycle[((frameIndex%32)+32)%32/8]
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
	return g
}

// Package scene turns recorded state into deterministic terminal landscapes.
package scene

import (
	"fmt"
	"github.com/brew0x/percolate/internal/repo"
	"sort"
	"strings"
	"time"
)

// laneNameRow is the row the lane names occupy. A PR number label normally
// sits one row above its diamond; in a compact frame that row can land here,
// where drawing it would paint a hybrid of a branch name and a PR number.
const laneNameRow = 2

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
	// PrevRank is the rank a pull request held in the previous snapshot.
	// Build sets it to Rank for its own snapshot; Transition replaces it for
	// PRs that survive a change, so a fading issue can resolve its recorded
	// closing reference in the rank space it was recorded in even when the
	// named PR moved. A PR new in the next snapshot carries -1: it was not
	// present before the change and never answers a fading issue's lookup.
	PrevRank int
	// Drip is how many rows above its resting row a mark draws while it is
	// entering during a transition, counting down to zero as it settles. A
	// newest commit uses it instead of a negative rank so its fall, and the
	// parent paths and anchored CI that follow it, stay clear of the lane
	// names. Frame applies it even when Total is one, where Rank is ignored.
	Drip int
	// Departing marks were present in the previous snapshot and are absent
	// from the new one; Transition appends them while they fade. Keeping them
	// identifiable lets the issue layer keep every PR from that snapshot in
	// view while a fading issue resolves its closing reference: a survivor
	// that moved rank still answers, and a departed PR never answers a
	// surviving issue's lookup.
	Departing bool
	Glyph     rune
	Style     Style
	Running   bool
	// Review is the flow-level review state of a pull request, resolved by
	// Build from its recorded reviews; other kinds carry the zero value
	// (waiting), which only the pull-request drawing consults.
	Review ReviewState
	// Jobs holds one mark per recorded workflow job. It is empty when the
	// snapshot has no job list, so Frame falls back to the run's aggregate
	// glyph; an explicitly empty list also draws no job cells.
	Jobs []JobMark
	// Artifacts holds the recorded names of a release's published files, in
	// snapshot order. Other kinds leave it empty.
	Artifacts []string
	// ReleaseTag names the release a deployment flows out of, matched by the
	// recorded head commit. Empty when no release records that commit, so the
	// deployment stands alone instead of borrowing a flow.
	ReleaseTag string
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
		out.Marks = append(out.Marks, Mark{ID: fmt.Sprintf("pr:%d", p.Number), Kind: "pr", Label: fmt.Sprintf("#%d", p.Number), Detail: detail, Lane: addLane(p.Head), Base: addLane(p.Base), Rank: i, PrevRank: i, Total: len(s.PullRequests), Glyph: '◇', Review: review, Style: Fresh})
	}
	// Issues follow PRs so a closing reference resolves to the same head lane.
	out.Marks = append(out.Marks, buildIssues(s, addLane)...)
	out.Marks = append(out.Marks, buildRuns(s, bySHA, addLane)...)
	out.Marks = append(out.Marks, buildReleases(s, addLane, bySHA)...)
	return out
}
func short(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

// Owners names the mark that drew each rendered cell. Paths, labels written by
// the header and other decoration carry no owner. It is built in the same pass
// as the visible frame, so input can hit-test what is actually on screen
// instead of guessing from a glyph that repeats across lanes and rows. Drawing
// a new glyph clears the previous owner first, so a cell repainted by an
// unowned path never keeps naming a mark that has been covered or removed.
type Owners [][]string

// Frame uses no time, I/O, randomness, or terminal state. Path cells show the
// union of the recorded segments through them; mark overlaps resolve in
// snapshot order, and the selected object is drawn last so it is inspectable.
func Frame(s Scene, frameIndex, width, height int) Grid {
	g, _ := FrameWithOwners(s, frameIndex, width, height)
	return g
}

// FrameWithOwners returns the frame and its cell ownership from one render.
// Overlaps go to the mark drawn last, matching the visible glyph, and an empty
// cell has no owner. A tiny frame draws only the title and owns nothing.
func FrameWithOwners(s Scene, frameIndex, width, height int) (Grid, Owners) {
	width = max(0, width)
	height = max(0, height)
	g := make(Grid, height)
	owners := make(Owners, height)
	for y := range g {
		g[y] = make([]Cell, width)
		owners[y] = make([]string, width)
		for x := range g[y] {
			g[y][x] = Cell{' ', Plain}
		}
	}
	own := func(x, y int, id string) {
		if y >= 0 && y < height && x >= 0 && x < width {
			owners[y][x] = id
		}
	}
	put := func(x, y int, r rune, style Style) {
		if y >= 0 && y < height && x >= 0 && x < width {
			g[y][x] = Cell{r, style}
			// Every repaint drops the previous owner. Callers that draw an
			// inspectable mark set it again with own immediately afterwards;
			// paths, arrows and other unowned glyphs leave no identity behind.
			// Without this, a surviving flow painted over a departing mark
			// would keep naming a mark no longer drawn at that cell, so a
			// click could select an object that is not there.
			owners[y][x] = ""
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
		return g, owners
	}
	lay := newLaneLayout(s, width, height)
	start, count := lay.start, lay.count
	xpos := lay.xpos
	visible := lay.visible
	row := lay.row
	top, bottom := lay.top, lay.bottom
	label(2, 0, fmt.Sprintf("PERCOLATE  %d–%d/%d", min(start+1, len(s.Lanes)), start+count, len(s.Lanes)), width-2, Faded)
	for i := start; i < start+count; i++ {
		x := xpos(i)
		label(x, laneNameRow, s.Lanes[i], lay.widthOf(i)-2, Fresh)
	}
	// One dim mark on each viewport edge stands for the whole run of
	// offscreen lanes on that side, so a crowded landscape reads as a soft
	// bundle rather than a hint at every hidden lane. The edge columns are
	// reserved (lanes begin at laneStartX and end before the right margin),
	// so this never covers a lane name; the header above gives the exact
	// count. No lane is invented and no object is aggregated.
	if lay.hiddenLeft > 0 {
		put(0, laneNameRow, '«', Faded)
	}
	if lay.hiddenRight > 0 {
		put(width-1, laneNameRow, '»', Faded)
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
	// Compressed rows can bring several pull requests to the same base cell.
	// A single review arrow there would describe only the last PR drawn while
	// an earlier PR's diamond and path still visibly end at that cell, so one
	// flow's state would read as another's. When the flows meeting at a cell
	// resolved to different states, no arrow speaks for the convergence: the
	// cell keeps its plain path run and each state stays in its PR's details.
	// Flows that agree at a cell keep the shared arrow, which is true for every
	// one ending there. A departing PR still draws its diamond and path through
	// the 32-frame fade, so it counts here too: a settled flow must not wear a
	// different state's mark just because the flow it shares the cell with is
	// on its way out. An offscreen base carries a direction indicator, not a
	// review mark.
	prEndpoint := func(m Mark) (int, int) {
		y := min(height-4, bottom+1+m.Rank%max(1, height-bottom-4))
		bx := max(0, min(width-1, xpos(m.Base)))
		return bx, y
	}
	endpointStates := map[[2]int]map[ReviewState]bool{}
	for _, m := range s.Marks {
		if m.Kind != "pr" || !visible(m.Lane) {
			continue
		}
		if m.Base < start || m.Base >= start+count {
			continue
		}
		bx, y := prEndpoint(m)
		if endpointStates[[2]int{bx, y}] == nil {
			endpointStates[[2]int{bx, y}] = map[ReviewState]bool{}
		}
		endpointStates[[2]int{bx, y}][m.Review] = true
	}
	// A convergence mark must not replace another pull request's head diamond.
	// At a compact height every flow shares a row, so a PR whose base lane is a
	// different PR's head lands its review arrow exactly on that PR's diamond.
	// The diamond names its pull request, so it stays and the converging PR's
	// state lives in its details instead — just like a disputed convergence.
	// A surviving head suppresses every mark. A departing head suppresses only
	// marks in its own fading layer: those are drawn before the surviving paths,
	// so the diamond is still visible then. A surviving convergence mark cannot
	// share a departing diamond's cell, because its own flow's path has already
	// painted over that base cell; suppressing it would leave the cell as a
	// plain run until the fade ended.
	headCells := map[[2]int]bool{}
	departingHeadCells := map[[2]int]bool{}
	for _, m := range s.Marks {
		if m.Kind != "pr" || !visible(m.Lane) {
			continue
		}
		_, y := prEndpoint(m)
		cell := [2]int{xpos(m.Lane), y}
		if m.Departing {
			departingHeadCells[cell] = true
		} else {
			headCells[cell] = true
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
	// draw paints a mark. For a PR, path paints only its horizontal flow and
	// returns; otherwise it paints the convergence mark, diamond and label.
	// Splitting the two lets every path settle before any mark is drawn, so a
	// later flow cannot write '─' over an earlier flow's arrow at a different
	// base that its path merely crosses on a compressed row.
	draw := func(m Mark, selected, path bool) {
		if m.Kind == "issue" || m.Kind == "release" || m.Kind == "deployment" {
			return // drawn by drawIssues and drawReleases after the other marks
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
			bx, by := prEndpoint(m)
			if path {
				// A shared flow belongs to no single pull request, so it stays
				// unowned; only the diamond and number name a PR.
				for xx := min(x, bx); xx <= max(x, bx); xx++ {
					put(xx, by, '─', style)
				}
				return
			}
			arrow := convergenceGlyph(m.Review)
			onScreenBase := m.Base >= start && m.Base < start+count
			if !onScreenBase {
				if m.Base < start {
					arrow = '‹'
				} else {
					arrow = '›'
				}
			}
			// The review state colors the convergence mark of a settled flow;
			// a fading flow (entering or departing during a change) keeps its
			// dim style so the mark fades with the diamond and path it belongs
			// to. An offscreen base keeps the plain indicator, and a selected
			// PR keeps the selected style on everything it draws.
			arrowStyle := style
			if !selected && style != Faded && onScreenBase {
				arrowStyle = reviewStyle(m.Review, style)
			}
			// A cell joining flows with differing states credits no single PR:
			// leave the plain path run instead of capping an earlier flow with
			// a later or departing flow's mark. endpointStates counts every
			// visible flow, including ones fading out, because their diamond
			// and path still visibly end at that cell. The same cell may instead
			// be another PR's head: the diamond names that PR, so a review mark
			// must not cover it whichever flow is selected. In both cases the
			// converging PR's state stays in its details; its diamond, path and
			// number keep their own (or the selected) style.
			sharedDisagreement := onScreenBase && len(endpointStates[[2]int{bx, by}]) > 1
			headCollision := onScreenBase && (headCells[[2]int{bx, by}] || (m.Departing && departingHeadCells[[2]int{bx, by}]))
			if !sharedDisagreement && !headCollision {
				put(bx, by, arrow, arrowStyle)
			}
			put(x, by, m.Glyph, style)
			own(x, by, m.ID)
			// A compact frame can put the label row on the lane names. A
			// number painted there would read as part of a branch name and
			// name no recorded object, so leave the lane name whole; the
			// diamond, flow and details still identify the pull request.
			if by-1 != laneNameRow {
				label(x+1, by-1, m.Label, lay.widthOf(m.Lane)-2, style)
				for i := range []rune(m.Label) {
					if i >= lay.widthOf(m.Lane)-2 {
						break
					}
					own(x+1+i, by-1, m.ID)
				}
			}
		case "run":
			if cells := runCells(m, frameIndex, max(1, lay.widthOf(m.Lane)-2), selected); len(cells) > 0 {
				for i, c := range cells {
					put(x+2+i, y, c.Rune, c.Style)
					own(x+2+i, y, m.ID)
				}
				return
			}
			glyph := m.Glyph
			if m.Running {
				glyph = pulseGlyph(frameIndex)
				if ((frameIndex%32)+32)%32 < 16 && !selected {
					style = Faded
				}
			}
			put(x+2, y, glyph, style)
			own(x+2, y, m.ID)
		default:
			put(x, y, m.Glyph, style)
			own(x, y, m.ID)
		}
	}
	// Draw in two layers, fading first and surviving second, so everything
	// leaving the scene sits beneath everything that stays. Within a layer,
	// every PR path goes down before any mark, so a crossing path cannot erase
	// another flow's arrow, diamond or number label; the selected path goes
	// after the other paths but still before every mark, so its whole flow
	// keeps the selected style without covering another flow's mark. The
	// layering matters for a departing PR whose convergence mark sits on a
	// surviving flow's path: drawing the fading layer first lets that path
	// keep the cell instead of being crossed out by a dim arrow that is on
	// its way out.
	drawLayer := func(departing bool) {
		for _, m := range s.Marks {
			if m.Kind == "pr" && m.Departing == departing && m.ID != s.SelectedID {
				draw(m, false, true)
			}
		}
		for _, m := range s.Marks {
			if m.Kind == "pr" && m.Departing == departing && m.ID == s.SelectedID {
				draw(m, true, true)
			}
		}
		for _, m := range s.Marks {
			if m.Departing == departing && m.ID != s.SelectedID {
				draw(m, false, false)
			}
		}
		for _, m := range s.Marks {
			if m.Departing == departing && m.ID == s.SelectedID {
				draw(m, true, false)
			}
		}
	}
	drawLayer(true)
	drawLayer(false)
	drawReleases(g, s, releaseView{
		width: width, height: height, bottom: bottom,
		widthOf: lay.widthOf, xpos: xpos, visible: visible,
	}, own)
	drawIssues(g, s, issueView{width: width, height: height, top: top, bottom: bottom, widthOf: lay.widthOf, xpos: xpos, visible: visible}, own)
	return g, owners
}

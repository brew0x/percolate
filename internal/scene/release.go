package scene

import (
	"fmt"
	"strings"

	"github.com/brew0x/percolate/internal/repo"
)

// buildReleases resolves recorded releases and deployments into marks. A
// deployment flows out of the release that records the same head commit: both
// facts come from the snapshot, so no flow is guessed from a shared branch or
// environment name. The first release in snapshot order owns a shared commit;
// a commit with no recorded SHA owns no release at all.
func buildReleases(s repo.Snapshot, addLane func(string) int, bySHA map[string]Mark) []Mark {
	tagBySHA := make(map[string]string, len(s.Releases))
	for _, r := range s.Releases {
		if r.HeadSHA == "" {
			continue // two unlinked releases must never share a flow
		}
		if _, exists := tagBySHA[r.HeadSHA]; !exists {
			tagBySHA[r.HeadSHA] = r.Tag
		}
	}
	anchorLane := func(sha string) int {
		if m, ok := bySHA[sha]; ok {
			return m.Lane
		}
		return addLane("unlinked")
	}

	var out []Mark
	for _, r := range s.Releases {
		names := make([]string, 0, len(r.Artifacts))
		for _, a := range r.Artifacts {
			names = append(names, a.Name)
		}
		detail := r.Tag + " · " + r.HeadSHA
		if len(names) == 0 {
			detail += " · artifacts not recorded"
		} else {
			detail += " · artifacts: " + strings.Join(names, ", ")
		}
		out = append(out, Mark{
			ID: "release:" + r.Tag, Kind: "release", Label: r.Tag, Detail: detail,
			Lane: anchorLane(r.HeadSHA), Artifacts: names, Glyph: '◆', Style: ReleaseStyle,
		})
	}
	for _, d := range s.Deployments {
		out = append(out, Mark{
			ID: fmt.Sprintf("deployment:%d", d.ID), Kind: "deployment", Label: d.Environment,
			Detail: fmt.Sprintf("%s · %s · %s", d.Environment, d.State, d.HeadSHA),
			Lane:   anchorLane(d.HeadSHA), ReleaseTag: tagBySHA[d.HeadSHA],
			Glyph: '▣', Style: deploymentStyle(d),
		})
	}
	return out
}

// deploymentStyle maps a recorded state to a palette, never to a guess. An
// unrecorded or inactive state keeps the neutral deployment palette; only a
// recorded success or failure takes the pass or fail palette. Transition
// overrides it with Faded while a deployment is forming or leaving.
func deploymentStyle(d repo.Deployment) Style {
	switch {
	case d.Failed():
		return Fail
	case d.Succeeded():
		return Pass
	case d.Pending():
		return Pulse
	default:
		return ReleaseStyle
	}
}

// releaseView is the geometry drawReleases needs, shared with Frame's layout.
type releaseView struct {
	width, height int
	bottom        int
	xpos          func(int) int
	visible       func(int) bool
	widthOf       func(int) int
}

type releaseCell struct {
	rune  rune
	style Style
	owner string
}

// drawReleases paints the bottom band: releases with their artifacts, then the
// deployments flowing out of each release. It runs after the other marks and
// before the issue layer, so a PR flow cannot be crossed out and an issue feed
// only fills what is free. Fading marks draw first, settled ones next and the
// selected object last, matching the rest of the frame.
func drawReleases(g Grid, s Scene, v releaseView, own func(int, int, string)) {
	row := v.height - 2
	put := func(x, y int, c releaseCell) {
		if x >= 0 && x < v.width && y >= 0 && y < v.height {
			g[y][x] = Cell{c.rune, c.style}
			own(x, y, c.owner)
		}
	}
	// reserved is the bottom band's single claim map: the cell holds the pass
	// that last wrote it, plus one (zero means free). Every part of the band
	// uses it — release rows, overflow marks, standalone deployments and
	// artifact rows alike — so a fading mark can never take a cell from one
	// that stays. Paths and flows are drawn earlier and never carry a claim,
	// so they still keep their cells: a claim only frees a cell another mark
	// in this band wrote, not recorded data from another layer.
	reserved := map[[2]int]int{}
	claim := func(pass int) int { return pass + 1 }
	free := func(x, y, pass int) bool {
		if x < 0 || x >= v.width || y < 0 || y >= v.height {
			return false
		}
		prior := reserved[[2]int{x, y}]
		if prior > claim(pass) {
			return false
		}
		return g[y][x].Rune == ' ' || prior > 0
	}
	// clearArtifacts erases an earlier run on the same row before a new one is
	// drawn, so a shorter surviving name cannot leave the tail of a longer
	// departing name behind. It only clears claims from this pass or earlier:
	// a surviving artifact is never erased by something on its way out.
	clearArtifacts := func(x, y, upto, pass int) {
		for xx := x; xx < upto && xx < v.width; xx++ {
			key := [2]int{xx, y}
			if prior := reserved[key]; prior > 0 && prior <= claim(pass) {
				g[y][xx] = Cell{' ', Plain}
				own(xx, y, "")
				delete(reserved, key)
			}
		}
	}
	// overflow keeps a hidden failure at a shared cell: a later release whose
	// own omissions are all healthy must not cover an earlier failure's mark.
	// The pass travels with the mark so a settled owner also wins over a
	// selected mark that is only fading out.
	type overflowMark struct {
		style Style
		owner string
		pass  int
	}
	overflow := map[[2]int]overflowMark{}

	linked := map[string][]Mark{}
	var alone []Mark
	for _, m := range s.Marks {
		if m.Kind != "deployment" || !v.visible(m.Lane) {
			continue
		}
		if m.ReleaseTag == "" {
			alone = append(alone, m)
		} else {
			linked["release:"+m.ReleaseTag] = append(linked["release:"+m.ReleaseTag], m)
		}
	}
	// Pass order follows the rest of the frame: everything leaving draws in
	// pass 0, survivors in pass 1 and a selected survivor in pass 2. Selection
	// orders a mark inside its own layer; it never promotes a departing mark
	// above one that stays. Each pass draws non-selected marks first, so the
	// selected one still draws last where it can be inspected.
	passOf := func(m Mark) int {
		switch {
		case m.Departing:
			return 0
		case m.ID == s.SelectedID:
			return 2
		default:
			return 1
		}
	}

	// One release and the deployments that flow out of it share a single row,
	// so the flow reads as a continuous stream from ◆ to each ▣. Each branch
	// carries its own recorded status palette. If the named strip does not fit,
	// names are dropped first so every environment keeps a status-bearing glyph;
	// only when even those do not fit are deployments omitted, with a marker
	// below the release so a hidden failure is never invisible.
	drawReleaseRow := func(m Mark, pass int) {
		if passOf(m) != pass {
			return
		}
		x := v.xpos(m.Lane)
		style := m.Style
		if m.ID == s.SelectedID {
			style = Selected
		}
		deps := linked[m.ID]
		limit := max(0, v.widthOf(m.Lane)-1)
		build := func(withNames bool) []releaseCell {
			cs := []releaseCell{{m.Glyph, style, m.ID}, {' ', style, ""}}
			for _, r := range m.Label {
				cs = append(cs, releaseCell{r, style, m.ID})
			}
			for _, d := range deps {
				ds := d.Style
				if d.ID == s.SelectedID {
					ds = Selected
				}
				cs = append(cs, releaseCell{' ', ds, ""}, releaseCell{'─', ds, d.ID}, releaseCell{d.Glyph, ds, d.ID})
				if withNames {
					for _, r := range d.Label {
						cs = append(cs, releaseCell{r, ds, d.ID})
					}
				}
			}
			return cs
		}
		cells := build(true)
		if len(cells) > limit {
			cells = build(false)
		}
		omitted := 0
		if len(cells) > limit {
			// Names are gone here, so every branch is a space, a connector
			// and a glyph. Drop a branch whose glyph does not fit as a whole:
			// a cut between '─' and '▣' would name no deployment at all.
			cut := limit
			for i, c := range cells {
				if c.rune != '▣' || i < limit {
					continue
				}
				if omitted == 0 {
					cut = i - 2 // the branch's leading space
				}
				omitted++
			}
			cells = cells[:max(0, min(cut, limit))]
		}
		for i, c := range cells {
			put(x+i, row, c)
			if pass > 0 {
				reserved[[2]int{x + i, row}] = claim(pass)
			}
		}
		if omitted > 0 {
			mark := overflowMark{Faded, m.ID, claim(pass)}
			for _, d := range deps[len(deps)-omitted:] {
				if d.Style == Fail {
					mark = overflowMark{Fail, d.ID, claim(pass)}
					break
				}
			}
			cell := [2]int{x, row + 1}
			// Several releases on one lane may overflow into the same cell
			// during a fade. A recorded failure keeps its mark and the
			// deployment that names it, whatever pass it came from; between
			// two healthy omissions a surviving mark keeps the cell so a
			// selected mark on its way out cannot cover it.
			if prior, ok := overflow[cell]; ok {
				if prior.style == Fail && mark.style != Fail || prior.pass > claim(pass) && mark.style != Fail {
					mark = prior
				}
			}
			if prior, ok := overflow[cell]; !ok || mark.style == Fail || mark.pass >= prior.pass {
				overflow[cell] = mark
			}
			reserved[cell] = overflow[cell].pass
			put(x, row+1, releaseCell{'!', mark.style, mark.owner})
		}
	}

	// Artifacts stack in the free rows above their release, one per row in
	// snapshot order. Rows at or above the commit region belong to the commits
	// and PR flows, so they are never claimed. An artifact draws only when its
	// whole run is free of recorded marks: a path or flow keeps its cell and no
	// half-overwritten name can be read as a different artifact. Claiming cells
	// in the shared map lets a surviving release take its row from one on its
	// way out while keeping a cell another survivor already owns. The full list
	// is in details.
	drawArtifacts := func(m Mark, pass int) {
		if passOf(m) != pass || len(m.Artifacts) == 0 {
			return
		}
		style := m.Style
		if m.ID == s.SelectedID {
			style = Selected
		}
		x := v.xpos(m.Lane)
		limit := max(0, v.widthOf(m.Lane)-3)
		for i, name := range m.Artifacts {
			y := row - 1 - i
			if y <= v.bottom || y < 0 {
				break
			}
			run := []releaseCell{{'▤', style, m.ID}, {' ', style, ""}}
			for _, r := range []rune(name) {
				if len(run) >= limit {
					break
				}
				run = append(run, releaseCell{r, style, m.ID})
			}
			wholeRunFree := true
			for j := range run {
				if !free(x+j, y, pass) {
					wholeRunFree = false
					break
				}
			}
			if !wholeRunFree {
				continue
			}
			clearArtifacts(x, y, x+v.widthOf(m.Lane), pass)
			for j, c := range run {
				put(x+j, y, c)
				reserved[[2]int{x + j, y}] = claim(pass)
			}
		}
	}

	// A deployment with no recorded release has no flow to draw. It keeps the
	// old standalone mark at the lane bottom, but only where the release row is
	// free, so a release in the same lane is never overwritten by it.
	drawAlone := func(d Mark, pass int) {
		if passOf(d) != pass {
			return
		}
		x := v.xpos(d.Lane)
		style := d.Style
		if d.ID == s.SelectedID {
			style = Selected
		}
		if reserved[[2]int{x, row}] > 0 {
			return
		}
		put(x, row, releaseCell{d.Glyph, style, d.ID})
		reserved[[2]int{x, row}] = claim(pass)
		if reserved[[2]int{x + 1, row}] == 0 {
			put(x+1, row, releaseCell{' ', style, ""})
			reserved[[2]int{x + 1, row}] = claim(pass)
		}
		limit := max(0, v.widthOf(d.Lane)-3)
		for i, r := range d.Label {
			if i >= limit || reserved[[2]int{x + 2 + i, row}] > 0 {
				break
			}
			put(x+2+i, row, releaseCell{r, style, d.ID})
			reserved[[2]int{x + 2 + i, row}] = claim(pass)
		}
	}

	for pass := 0; pass < 3; pass++ {
		// Within a pass the selected mark draws last, matching the rest of
		// Frame. It is still in its own layer: a selected departure cannot
		// outrank a survivor just because it is selected.
		for _, selected := range []bool{false, true} {
			for _, m := range s.Marks {
				if m.Kind != "release" || !v.visible(m.Lane) || (m.ID == s.SelectedID) != selected {
					continue
				}
				drawReleaseRow(m, pass)
				drawArtifacts(m, pass)
			}
		}
	}
	for pass := 0; pass < 3; pass++ {
		for _, selected := range []bool{false, true} {
			for _, d := range alone {
				if (d.ID == s.SelectedID) == selected {
					drawAlone(d, pass)
				}
			}
		}
	}
}

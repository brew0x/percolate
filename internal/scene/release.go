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
	// artifactCells holds artifact runs written this frame. A later pass may
	// claim one of these cells — a surviving release wins over a fading one —
	// but a path, flow or any other recorded mark keeps its cell.
	artifactCells := map[[2]int]bool{}
	artifactFree := func(x, y int) bool {
		if x < 0 || x >= v.width || y < 0 || y >= v.height {
			return false
		}
		return g[y][x].Rune == ' ' || artifactCells[[2]int{x, y}]
	}
	// clearArtifacts erases an earlier run on the same row before a new one is
	// drawn, so a shorter surviving name cannot leave the tail of a longer
	// departing name behind.
	clearArtifacts := func(x, y, upto int) {
		for xx := x; xx < upto && xx < v.width; xx++ {
			key := [2]int{xx, y}
			if artifactCells[key] {
				g[y][xx] = Cell{' ', Plain}
				own(xx, y, "")
				delete(artifactCells, key)
			}
		}
	}
	// reserved holds cells owned by a surviving mark. A departing release may
	// be covered by a deployment that stays, but nothing that stays may be
	// covered by something on its way out.
	reserved := map[[2]int]bool{}
	// overflow keeps a hidden failure at a shared cell: a later release whose
	// own omissions are all healthy must not cover an earlier failure's mark.
	type overflowMark struct {
		style Style
		owner string
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
	passOf := func(m Mark) int {
		switch {
		case m.ID == s.SelectedID:
			return 2
		case m.Departing:
			return 0
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
				reserved[[2]int{x + i, row}] = true
			}
		}
		if omitted > 0 {
			mark := overflowMark{Faded, m.ID}
			for _, d := range deps[len(deps)-omitted:] {
				if d.Style == Fail {
					mark = overflowMark{Fail, d.ID}
					break
				}
			}
			cell := [2]int{x, row + 1}
			// Several releases on one lane may overflow into the same cell
			// during a fade. A recorded failure keeps its mark and the
			// deployment that names it; only healthy omissions may be covered.
			if prior, ok := overflow[cell]; ok && prior.style == Fail && mark.style != Fail {
				mark = prior
			} else if mark.style == Fail {
				overflow[cell] = mark
			}
			put(x, row+1, releaseCell{'!', mark.style, mark.owner})
		}
	}

	// Artifacts stack in the free rows above their release, one per row in
	// snapshot order. Rows at or above the commit region belong to the commits
	// and PR flows, so they are never claimed. An artifact draws only when its
	// whole run is free of recorded marks: a path or flow keeps its cell and no
	// half-overwritten name can be read as a different artifact. Another
	// artifact's run from an earlier pass may be replaced, so a surviving
	// release wins its row over one on its way out. The full list is in details.
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
			free := true
			for j := range run {
				if !artifactFree(x+j, y) {
					free = false
					break
				}
			}
			if !free {
				continue
			}
			clearArtifacts(x, y, x+v.widthOf(m.Lane))
			for j, c := range run {
				put(x+j, y, c)
				artifactCells[[2]int{x + j, y}] = true
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
		if reserved[[2]int{x, row}] {
			return
		}
		put(x, row, releaseCell{d.Glyph, style, d.ID})
		reserved[[2]int{x, row}] = true
		if !reserved[[2]int{x + 1, row}] {
			put(x+1, row, releaseCell{' ', style, ""})
			reserved[[2]int{x + 1, row}] = true
		}
		limit := max(0, v.widthOf(d.Lane)-3)
		for i, r := range d.Label {
			if i >= limit || reserved[[2]int{x + 2 + i, row}] {
				break
			}
			put(x+2+i, row, releaseCell{r, style, d.ID})
			reserved[[2]int{x + 2 + i, row}] = true
		}
	}

	for pass := 0; pass < 3; pass++ {
		for _, m := range s.Marks {
			if m.Kind == "release" && v.visible(m.Lane) {
				drawReleaseRow(m, pass)
				drawArtifacts(m, pass)
			}
		}
	}
	for pass := 0; pass < 3; pass++ {
		for _, d := range alone {
			drawAlone(d, pass)
		}
	}
}

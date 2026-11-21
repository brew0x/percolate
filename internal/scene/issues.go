package scene

import (
	"fmt"
	"strconv"

	"github.com/brew0x/percolate/internal/repo"
)

const (
	issueSpring = '∿'
	issueFeed   = '⋮'
)

// buildIssues adds one mark per open issue. ClosedBy is the only source of a
// link: it is recorded from a pull request's closing reference, never guessed
// from a title, a number, or a branch name. An issue whose ClosedBy names a
// pull request absent from the snapshot stays unlinked, so no link is invented.
//
// For a linked issue, Base holds the pull request's head lane and Rank holds
// the pull request's index in the snapshot that recorded the link (the Rank
// that PR mark used there). For an unlinked issue, Base is negative and Rank
// is its margin row offset.
// Lane points at the first lane for an unlinked issue so navigation keeps the
// margin in view.
func buildIssues(s repo.Snapshot, addLane func(string) int) []Mark {
	prLane := make(map[int]int, len(s.PullRequests))
	prRank := make(map[int]int, len(s.PullRequests))
	for i, p := range s.PullRequests {
		prLane[p.Number] = addLane(p.Head)
		prRank[p.Number] = i
	}
	var marks []Mark
	for _, issue := range s.Issues {
		if issue.State != "open" {
			continue
		}
		detail := fmt.Sprintf("#%d %s · %s", issue.Number, issue.Title, issue.State)
		lane, linked := prLane[issue.ClosedBy]
		base, rank := -1, len(marks)
		if linked {
			base, rank = lane, prRank[issue.ClosedBy]
			detail += fmt.Sprintf(" · closes in #%d", issue.ClosedBy)
		} else {
			detail += " · no recorded closing reference"
			lane = 0
		}
		style := Faded
		if linked {
			style = Fresh
		}
		marks = append(marks, Mark{
			ID:     fmt.Sprintf("issue:%d", issue.Number),
			Kind:   "issue",
			Label:  "#" + strconv.Itoa(issue.Number),
			Detail: detail,
			Lane:   lane,
			Base:   base,
			Rank:   rank,
			Glyph:  issueSpring,
			Style:  style,
		})
	}
	return marks
}

// issueView is the part of Frame's geometry the issue layer needs. Springs
// share the lane x positions with commits, but live on the shelf above them.
type issueView struct {
	width, height, top, bottom int
	widthOf                    func(int) int
	xpos                       func(int) int
	visible                    func(int) bool
}

// drawIssues draws the issue layer last. Linked springs sit above the head
// lane of the pull request whose recorded closing reference names them, with a
// dotted feed down that lane into the PR flow. Unlinked springs sit quietly in
// the far-left margin and draw no feed. The layer only fills cells that are
// empty or that it wrote itself this frame, so commits, runs, recorded paths
// and a dripping bead always win the shared shelf row (occlusion, not
// aggregation); labels clear only cells the layer owns, so a shorter number
// still erases a longer one without erasing recorded data. A surviving spring
// draws after every departing one, so the cell belongs to what stays: even a
// selected departure cannot hide a survivor on the same head lane. Within each
// layer the selected spring draws last so it stays inspectable.
//
// Closing references resolve by the pull request's snapshot rank. A fading
// issue's rank came from the previous snapshot, so it resolves against every
// PR that snapshot held: Transition keeps each PR's rank there, whether the PR
// stays or fades, and the feed follows the PR actually drawn at that rank. A
// surviving issue ranks only against pull requests that stay, so a departed PR
// must never satisfy the lookup for a surviving issue that moved into its rank.
func drawIssues(g Grid, s Scene, v issueView, own func(int, int, string)) {
	prByRank := make(map[int]Mark, len(s.Marks))   // PRs that stay, at their drawn rank
	prevByRank := make(map[int]Mark, len(s.Marks)) // every PR from prev, at its rank there
	for _, m := range s.Marks {
		if m.Kind != "pr" {
			continue
		}
		// Ranks are unique within one snapshot, so a survivor's previous rank
		// and a departed PR's rank cannot collide. PRs new in next have no
		// previous rank and stay out of this map.
		if m.PrevRank >= 0 {
			prevByRank[m.PrevRank] = m
		}
		if !m.Departing {
			prByRank[m.Rank] = m
		}
	}
	// written holds every cell this layer claimed in the current frame, so a
	// later issue can replace an earlier one's label (and the selected spring
	// can redraw) while recorded marks stay untouchable.
	written := make(map[[2]int]bool)
	put := func(x, y int, r rune, style Style) bool {
		if y < 0 || y >= v.height || x < 0 || x >= v.width {
			return false
		}
		key := [2]int{x, y}
		if !written[key] && g[y][x].Rune != ' ' {
			return false
		}
		g[y][x] = Cell{r, style}
		own(x, y, "") // a space or a replacement erases the previous owner
		written[key] = true
		return true
	}
	drawOne := func(m Mark) {
		style := m.Style
		if m.ID == s.SelectedID {
			style = Selected
		}
		if m.Base < 0 {
			y := v.top - 1 + m.Rank
			if m.ID == s.SelectedID {
				y = min(y, v.height-1)
			}
			if put(0, y, issueSpring, style) {
				own(0, y, m.ID)
			}
			return
		}
		if !v.visible(m.Lane) {
			return
		}
		x := v.xpos(m.Lane)
		y := v.top - 1
		ranks := prByRank
		if m.Departing {
			ranks = prevByRank
		}
		pr, ok := ranks[m.Rank]
		prRow := -1
		if ok {
			prRow = min(v.height-4, v.bottom+1+pr.Rank%max(1, v.height-v.bottom-4))
			if y == prRow || y == prRow-1 {
				// Compact frame: the shelf shares the PR's diamond row or its
				// number-label row. Lift the spring above the lane names so it
				// never overwrites a PR mark, including a selected one. No row
				// is left for a feed between the lift and the PR, so omit it.
				y = 1
			}
		}
		if !put(x, y, issueSpring, style) {
			// A recorded mark holds the shelf cell — a commit dripping through
			// it during a transition, for example. The spring stays hidden and
			// draws nothing around it; it reappears whole once the cell frees.
			return
		}
		own(x, y, m.ID)
		if labelWidth := v.widthOf(m.Lane) - 2; labelWidth >= 3 {
			// Clear the label field the layer owns before writing. Several
			// issues may link to the same head lane; a short label drawn over
			// a longer one would otherwise leave fabricated digits behind (#5
			// over #100 reads #500), and selection draws an issue a second
			// time.
			for i := 0; i < labelWidth; i++ {
				put(x+1+i, y, ' ', Plain)
			}
			for i, r := range []rune(m.Label) {
				if i >= labelWidth {
					break
				}
				if !put(x+1+i, y, r, style) {
					break
				}
				own(x+1+i, y, m.ID)
			}
		}
		if !ok {
			return
		}
		for yy := v.top; yy < prRow && yy < v.height; yy++ {
			if g[yy][x].Rune == ' ' {
				g[yy][x] = Cell{issueFeed, LaneStyle}
				own(x, yy, m.ID)
				written[[2]int{x, yy}] = true
			}
		}
	}
	// Four passes keep the documented layering: every fading spring draws
	// before every surviving one, and selection only orders marks inside their
	// own layer. A selected departure would otherwise be drawn after a
	// surviving spring and hide the issue that is actually still open.
	for _, m := range s.Marks {
		if m.Kind == "issue" && m.Departing && m.ID != s.SelectedID {
			drawOne(m)
		}
	}
	for _, m := range s.Marks {
		if m.Kind == "issue" && m.Departing && m.ID == s.SelectedID {
			drawOne(m)
		}
	}
	for _, m := range s.Marks {
		if m.Kind == "issue" && !m.Departing && m.ID != s.SelectedID {
			drawOne(m)
		}
	}
	for _, m := range s.Marks {
		if m.Kind == "issue" && !m.Departing && m.ID == s.SelectedID {
			drawOne(m)
		}
	}
}

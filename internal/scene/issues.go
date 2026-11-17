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
// the pull request's index in snapshot order (the same Rank the PR mark uses).
// For an unlinked issue, Base is negative and Rank is its margin row offset.
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
	width, height, top, bottom, span int
	xpos                             func(int) int
	visible                          func(int) bool
}

// drawIssues draws the issue layer last. Linked springs sit above the head
// lane of the pull request whose recorded closing reference names them, with a
// dotted feed down that lane into the PR flow. Unlinked springs sit quietly in
// the far-left margin and draw no feed. Feed cells are written only where the
// frame is empty, so commits and recorded paths always win (occlusion, not
// aggregation). The selected spring draws last so it stays inspectable.
func drawIssues(g Grid, s Scene, v issueView) {
	prByRank := make(map[int]Mark, len(s.Marks))
	for _, m := range s.Marks {
		if m.Kind == "pr" {
			prByRank[m.Rank] = m
		}
	}
	put := func(x, y int, r rune, style Style) {
		if y >= 0 && y < v.height && x >= 0 && x < v.width {
			g[y][x] = Cell{r, style}
		}
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
			put(0, y, issueSpring, style)
			return
		}
		if !v.visible(m.Lane) {
			return
		}
		x, y := v.xpos(m.Lane), v.top-1
		put(x, y, issueSpring, style)
		if v.span >= 5 {
			for i, r := range []rune(m.Label) {
				if i >= v.span-2 {
					break
				}
				put(x+1+i, y, r, style)
			}
		}
		pr, ok := prByRank[m.Rank]
		if !ok {
			return
		}
		prRow := min(v.height-4, v.bottom+1+pr.Rank%max(1, v.height-v.bottom-4))
		for yy := v.top; yy < prRow && yy < v.height; yy++ {
			if g[yy][x].Rune == ' ' {
				put(x, yy, issueFeed, LaneStyle)
			}
		}
	}
	for _, m := range s.Marks {
		if m.Kind == "issue" && m.ID != s.SelectedID {
			drawOne(m)
		}
	}
	for _, m := range s.Marks {
		if m.Kind == "issue" && m.ID == s.SelectedID {
			drawOne(m)
		}
	}
}

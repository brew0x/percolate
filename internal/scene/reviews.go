package scene

import (
	"github.com/brew0x/percolate/internal/repo"
)

// ReviewState is the flow-level review state a pull request's recorded
// reviews resolve to, drawn at its convergence point.
type ReviewState uint8

const (
	// ReviewWaiting: no recorded review, or no reviewer's latest recorded
	// review approves the pull request or requests changes.
	ReviewWaiting ReviewState = iota
	ReviewApproved
	ReviewChanges
)

// ReviewStateOf resolves a recorded review list. The snapshot's review order
// is the recorded history: the last recorded review per author decides that
// reviewer's say, and no wall clock is consulted. A pull request whose
// reviewers have decided both ways sits at changes requested; a later
// reviewer's request outweighs an earlier approval. A review with no recorded
// author (GitHub's deleted reviewer) has no reviewer to hold a say, so it
// never decides the state; collapsing those reviews into one map slot would
// let whichever came last impersonate a named reviewer.
func ReviewStateOf(reviews []repo.Review) ReviewState {
	latest := make(map[string]string, len(reviews))
	for _, r := range reviews {
		if r.Author == "" {
			continue
		}
		latest[r.Author] = r.State
	}
	changes, approved := false, false
	for _, state := range latest {
		switch state {
		case "changes_requested":
			changes = true
		case "approved":
			approved = true
		}
	}
	if changes {
		return ReviewChanges
	}
	if approved {
		return ReviewApproved
	}
	return ReviewWaiting
}

// convergenceGlyph is the review mark drawn at a pull request's convergence
// point, where its flow meets its base lane. The arrow keeps pointing into
// the base; its fill says what the recorded reviews did to the flow:
//
//	▽ waiting — an open arrow: no recorded decision
//	▼ approved — a solid arrow: the flow may proceed
//	⊗ changes requested — the convergence is crossed out and sent back
func convergenceGlyph(st ReviewState) rune {
	switch st {
	case ReviewApproved:
		return '▼'
	case ReviewChanges:
		return '⊗'
	default:
		return '▽'
	}
}

// reviewStyle colors the convergence mark in the TUI: approved flows use the
// pass palette, requested changes the fail one, and a waiting flow keeps the
// pull request's own style. Plain rendering preserves the glyph instead.
func reviewStyle(st ReviewState, plain Style) Style {
	switch st {
	case ReviewApproved:
		return Pass
	case ReviewChanges:
		return Fail
	default:
		return plain
	}
}

// reviewWord names the resolved state in a pull request's details.
func reviewWord(st ReviewState) string {
	switch st {
	case ReviewApproved:
		return "reviews: approved"
	case ReviewChanges:
		return "reviews: changes requested"
	default:
		return "reviews: waiting"
	}
}

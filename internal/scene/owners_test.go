package scene

import "testing"

// Cell ownership comes from the same pass as the visible frame. Repeated
// diamonds on different lanes must name different pull requests, and an
// overlap must name the mark drawn on top.
func TestFrameWithOwnersNamesDrawnMarks(t *testing.T) {
	s := Scene{
		Lanes: []string{"a", "b"},
		Marks: []Mark{
			{ID: "pr:1", Kind: "pr", Label: "#1", Lane: 0, Base: 1, Rank: 0, Total: 1, Glyph: '◇', Style: Fresh},
			{ID: "pr:2", Kind: "pr", Label: "#2", Lane: 1, Base: 0, Rank: 0, Total: 1, Glyph: '◇', Style: Fresh},
		},
	}
	g, owners := FrameWithOwners(s, 0, 60, 20)
	seen := map[string]bool{}
	for y, row := range g {
		for x, cell := range row {
			if cell.Rune == '◇' {
				if owners[y][x] == "" {
					t.Fatalf("drawn diamond at %d,%d has no owner", x, y)
				}
				seen[owners[y][x]] = true
			}
		}
	}
	if len(seen) != 2 || !seen["pr:1"] || !seen["pr:2"] {
		t.Fatalf("repeated diamonds named %v", seen)
	}

	overlap := Scene{
		Lanes: []string{"a"},
		Marks: []Mark{
			{ID: "commit:1", Kind: "commit", Lane: 0, Rank: 0, Total: 1, Glyph: '●', Style: Fresh},
			{ID: "commit:2", Kind: "commit", Lane: 0, Rank: 0, Total: 1, Glyph: '●', Style: Fresh},
		},
		SelectedID: "commit:2",
	}
	_, owned := FrameWithOwners(overlap, 0, 60, 20)
	cells := 0
	for _, row := range owned {
		for _, id := range row {
			if id == "" {
				continue
			}
			cells++
			if id != "commit:2" {
				t.Fatalf("overlap named %q, want the mark drawn last", id)
			}
		}
	}
	if cells != 1 {
		t.Fatalf("overlap owned %d cells, want one", cells)
	}
}

// A surviving flow's path is drawn after the fading layer and carries no
// owner. Where it repaints a departing mark's cell, that cell must stop naming
// the departed object: a click can only select what is actually drawn there.
func TestFrameWithOwnersClearsOwnerOnUnownedRedraw(t *testing.T) {
	s := Scene{
		Lanes: []string{"a", "b"},
		Marks: []Mark{
			{ID: "pr:1", Kind: "pr", Label: "#1", Lane: 0, Base: 1, Rank: 0, Total: 1, Glyph: '◇', Departing: true, Style: Faded},
			{ID: "pr:2", Kind: "pr", Label: "#2", Lane: 1, Base: 0, Rank: 0, Total: 1, Glyph: '◇', Style: Fresh},
		},
	}
	g, owners := FrameWithOwners(s, 0, 60, 20)
	// The departing diamond sits at lane 0's column on the shared flow row;
	// the surviving pull request's horizontal path repaints that same cell.
	const x, y = 3, 13
	if g[y][x].Rune == '◇' {
		t.Fatalf("test setup: the departing diamond was not repainted: %q", g[y][x].Rune)
	}
	if got := owners[y][x]; got != "" {
		t.Fatalf("repainted flow cell still names %q", got)
	}
	// Its own number label is still drawn and still names it; only the cell
	// taken over by the surviving flow has lost the stale identity.
	labels := 0
	for _, cells := range owners {
		for _, id := range cells {
			if id == "pr:1" {
				labels++
			}
		}
	}
	if labels == 0 {
		t.Fatal("the departing mark lost its own label ownership too")
	}
}

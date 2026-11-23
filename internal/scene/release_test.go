package scene_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/brew0x/percolate/internal/repo"
	"github.com/brew0x/percolate/internal/scene"
)

func TestReleaseArtifactsAndDeploymentLinks(t *testing.T) {
	built := scene.Build(fixture(t, "release"))
	marks := map[string]scene.Mark{}
	for _, m := range built.Marks {
		marks[m.ID] = m
	}
	v1 := marks["release:v1.0"]
	if got := strings.Join(v1.Artifacts, ","); got != "dist.tar.gz,checksums.txt" {
		t.Fatalf("release artifacts = %q", got)
	}
	if !strings.Contains(v1.Detail, "artifacts: dist.tar.gz, checksums.txt") {
		t.Fatalf("release detail lost artifacts: %q", v1.Detail)
	}
	v09 := marks["release:v0.9"]
	if got := strings.Join(v09.Artifacts, ","); got != "lib.zip" {
		t.Fatalf("second release artifacts = %q", got)
	}
	for _, tc := range []struct {
		id, release string
		style       scene.Style
	}{
		{"deployment:1", "v1.0", scene.Pass},
		{"deployment:2", "v1.0", scene.Pulse},
		{"deployment:3", "v1.0", scene.Fail},
		{"deployment:4", "v0.9", scene.Pass},
		{"deployment:5", "", scene.Fail}, // no release records its commit
	} {
		m, ok := marks[tc.id]
		if !ok {
			t.Fatalf("missing %s", tc.id)
		}
		if m.ReleaseTag != tc.release || m.Style != tc.style {
			t.Fatalf("%s: release=%q style=%d, want %q/%d", tc.id, m.ReleaseTag, m.Style, tc.release, tc.style)
		}
	}
}

func TestDeploymentStatePalettes(t *testing.T) {
	for _, tc := range []struct {
		state string
		want  scene.Style
	}{
		{"success", scene.Pass},
		{"SUCCESS", scene.Pass},
		{"failure", scene.Fail},
		{"error", scene.Fail},
		{"in_progress", scene.Pulse},
		{"queued", scene.Pulse},
		{"pending", scene.Pulse},
		{"inactive", scene.ReleaseStyle},
		{"cancelled", scene.ReleaseStyle},
		{"", scene.ReleaseStyle},
	} {
		s := repo.Snapshot{Deployments: []repo.Deployment{{ID: 1, State: tc.state}}}
		got := scene.Build(s).Marks[0].Style
		if got != tc.want {
			t.Fatalf("state %q: style %d, want %d", tc.state, got, tc.want)
		}
	}
}

// The reviewed frame must show two releases collecting at the bottom, their
// artifacts stacked above them, and one coloured branch per environment. The
// flow is drawn as one row so each status is legible without opening details.
func TestReleaseFrameShowsFlowAndArtifacts(t *testing.T) {
	g := scene.Frame(scene.Build(fixture(t, "release")), 0, 100, 30)
	row := rowText(g[28])
	if want := "◆ v1.0 ─▣prod ─▣staging ─▣dev"; !strings.Contains(row, want) {
		t.Fatalf("main release row = %q, want %q", row, want)
	}
	if want := "◆ v0.9 ─▣preview"; !strings.Contains(row, want) {
		t.Fatalf("flow-1 release row = %q, want %q", row, want)
	}
	if !strings.Contains(row, "▣ canary") {
		t.Fatalf("unlinked deployment lost: %q", row)
	}
	for _, want := range []string{"▤ dist.tar.gz", "▤ checksums.txt"} {
		if !strings.Contains(rowText(g[27])+rowText(g[26]), want) {
			t.Fatalf("artifact %q missing from frame:\n%s", want, g.String())
		}
	}
	if !strings.Contains(rowText(g[27]), "▤ lib.zip") {
		t.Fatalf("second release's artifact missing:\n%s", g.String())
	}
	// Status colours sit on the branch cells, not just in details.
	for x, want := range map[int]scene.Style{11: scene.Pass, 18: scene.Pulse, 28: scene.Fail} {
		if got := g[28][x].Style; got != want {
			t.Fatalf("cell %d style = %d, want %d", x, got, want)
		}
	}
}

// When names do not fit, every environment keeps its own status-bearing glyph;
// no failed deployment disappears just because its label was too long.
func TestNarrowStripKeepsEveryStatus(t *testing.T) {
	g := scene.Frame(scene.Build(fixture(t, "release")), 0, 60, 20)
	row := rowText(g[18])
	if !strings.Contains(row, "◆ v1.0 ─▣ ─▣ ─▣") || strings.Contains(row, "prod") {
		t.Fatalf("narrow release strip = %q", row)
	}
}

// At an extreme width even glyph-only branches overflow. The marker below the
// release must take the fail palette when a hidden deployment failed.
func TestOverflowMarkerReportsHiddenFailure(t *testing.T) {
	sha := "1111111111111111111111111111111111111111"
	s := repo.Snapshot{Commits: []repo.Commit{{SHA: sha, Branch: "main"}}}
	for i := 0; i < 12; i++ {
		s.Branches = append(s.Branches, repo.Branch{Name: fmt.Sprintf("lane-%d", i)})
	}
	s.Branches[0].Name = "main"
	s.Releases = []repo.Release{{Tag: "v1", HeadSHA: sha, Artifacts: []repo.Artifact{{Name: "a.bin"}}}}
	for i := 1; i <= 4; i++ {
		state := "success"
		if i == 4 {
			state = "failure"
		}
		s.Deployments = append(s.Deployments, repo.Deployment{ID: int64(i), HeadSHA: sha, Environment: "env", State: state})
	}
	g := scene.Frame(scene.Build(s), 0, 60, 20)
	if g[19][3].Rune != '!' || g[19][3].Style != scene.Fail {
		t.Fatalf("overflow marker = %+v, want ! in fail palette", g[19][3])
	}
}

// A deployment with no matching release keeps its standalone mark but never
// takes the cell of a release on the same lane.
func TestOrphanDeploymentDoesNotOverwriteRelease(t *testing.T) {
	sha := "1111111111111111111111111111111111111111"
	older := "2222222222222222222222222222222222222222"
	s := repo.Snapshot{
		Branches: []repo.Branch{{Name: "main"}},
		Commits: []repo.Commit{
			{SHA: sha, Branch: "main"},
			{SHA: older, Branch: "main", Parents: []string{sha}},
		},
		Releases:    []repo.Release{{Tag: "v1", HeadSHA: sha}},
		Deployments: []repo.Deployment{{ID: 1, HeadSHA: older, Environment: "edge", State: "success"}},
	}
	g := scene.Frame(scene.Build(s), 0, 60, 20)
	row := rowText(g[18])
	if !strings.HasPrefix(row, "◆ v1") || strings.Contains(row, "▣ edge") {
		t.Fatalf("orphan overwrote the release row: %q", row)
	}
}

// A release leaving a refresh must not hide a deployment that stays on the
// same lane: once the release is gone the deployment stands alone, and it wins
// the shared cell throughout the fade.
func TestRemovingReleaseLetsDeploymentStay(t *testing.T) {
	sha := "1111111111111111111111111111111111111111"
	prev := repo.Snapshot{
		Branches:    []repo.Branch{{Name: "main"}},
		Commits:     []repo.Commit{{SHA: sha, Branch: "main"}},
		Releases:    []repo.Release{{Tag: "v1", HeadSHA: sha}},
		Deployments: []repo.Deployment{{ID: 1, HeadSHA: sha, Environment: "prod", State: "success"}},
	}
	next := prev
	next.Releases = nil
	g := scene.Frame(scene.Transition(prev, next, 0), 0, 60, 20)
	if row := rowText(g[18]); !strings.HasPrefix(row, "▣ prod") {
		t.Fatalf("surviving deployment hidden by fading release: %q", row)
	}
}

// A release's artifact wording follows its snapshot, the same line the job
// list draws: a list that was never read says so rather than a numeric zero,
// a recorded empty list is the honest count `0 artifacts`, and recorded
// names are listed. Nothing is invented to fill the strip, and the empty
// list survives a recorded snapshot's JSON as recorded, not unread.
func TestReleaseArtifactDetails(t *testing.T) {
	s := repo.Snapshot{Releases: []repo.Release{
		{Tag: "v1", HeadSHA: "abc"},
		{Tag: "v2", HeadSHA: "abc", Artifacts: []repo.Artifact{}},
		{Tag: "v3", HeadSHA: "abc", Artifacts: []repo.Artifact{{Name: "a.bin"}}},
	}}
	marks := scene.Build(s).Marks
	if len(marks) != 3 {
		t.Fatalf("expected three release marks, got %d", len(marks))
	}
	if !strings.Contains(marks[0].Detail, "artifacts not recorded") || strings.Contains(marks[0].Detail, "0 artifacts") {
		t.Fatalf("unread artifact list rendered as data: %q", marks[0].Detail)
	}
	if !strings.Contains(marks[1].Detail, "0 artifacts") || strings.Contains(marks[1].Detail, "artifacts not recorded") {
		t.Fatalf("explicitly empty artifact list misread: %q", marks[1].Detail)
	}
	if !strings.Contains(marks[2].Detail, "artifacts: a.bin") {
		t.Fatalf("recorded artifact names lost: %q", marks[2].Detail)
	}
	for _, m := range marks[:2] {
		if len(m.Artifacts) != 0 {
			t.Fatalf("invented artifacts: %+v", m.Artifacts)
		}
	}
	var replay repo.Snapshot
	if err := json.Unmarshal([]byte(`{"releases":[{"tag":"v4","head_sha":"abc","artifacts":[]}]}`), &replay); err != nil {
		t.Fatal(err)
	}
	if detail := scene.Build(replay).Marks[0].Detail; !strings.Contains(detail, "0 artifacts") {
		t.Fatalf("recorded empty asset list lost through JSON: %q", detail)
	}
}

// A release leaving a refresh must not hold its artifact row hostage. The
// replacement writes that row as soon as it draws, on the same surviving-wins
// rule the release row already follows, throughout the fade.
func TestSurvivingArtifactReplacesFadingArtifact(t *testing.T) {
	const sha = "1111111111111111111111111111111111111111"
	prev := repo.Snapshot{
		Branches: []repo.Branch{{Name: "main"}},
		Commits:  []repo.Commit{{SHA: sha, Branch: "main"}},
		Releases: []repo.Release{{Tag: "v1", HeadSHA: sha, Artifacts: []repo.Artifact{{Name: "old.bin"}}}},
	}
	next := prev
	next.Releases = []repo.Release{{Tag: "v2", HeadSHA: sha, Artifacts: []repo.Artifact{{Name: "new.bin"}}}}
	for _, frame := range []int{0, 8, 16, 31} {
		g := scene.Frame(scene.Transition(prev, next, frame), frame, 60, 20)
		if got := rowText(g[17]); got != "▤ new.bin" {
			t.Fatalf("frame %d artifact row = %q, want %q\n%s", frame, got, "▤ new.bin", g.String())
		}
	}
}

// Selection orders a mark inside its layer, not across layers. A selected
// release that is fading out must still yield its row to the replacement, just
// as an unselected departure does; otherwise the old artifact and tag stay on
// screen for the whole fade and name a release that is already gone.
func TestSelectedDepartingReleaseDoesNotHideSurvivor(t *testing.T) {
	const sha = "1111111111111111111111111111111111111111"
	prev := repo.Snapshot{
		Branches: []repo.Branch{{Name: "main"}},
		Commits:  []repo.Commit{{SHA: sha, Branch: "main"}},
		Releases: []repo.Release{{Tag: "v1", HeadSHA: sha, Artifacts: []repo.Artifact{{Name: "old-long-name.bin"}}}},
	}
	next := prev
	next.Releases = []repo.Release{{Tag: "v2", HeadSHA: sha, Artifacts: []repo.Artifact{{Name: "new.bin"}}}}
	sc := scene.Transition(prev, next, 8)
	sc.SelectedID = "release:v1"
	g := scene.Frame(sc, 8, 60, 20)
	if got := rowText(g[17]); got != "▤ new.bin" {
		t.Fatalf("frame 8 artifact row = %q, want %q\n%s", got, "▤ new.bin", g.String())
	}
	if got := rowText(g[18]); !strings.HasPrefix(got, "◆ v2") || strings.Contains(got, "v1") {
		t.Fatalf("frame 8 release row = %q, want the survivor\n%s", got, g.String())
	}
}

// A shorter surviving name must clear the tail of a longer departing one: a
// half-overwritten filename would cite no recorded artifact.
func TestSurvivingArtifactClearsLongerFadingName(t *testing.T) {
	const sha = "1111111111111111111111111111111111111111"
	prev := repo.Snapshot{
		Branches: []repo.Branch{{Name: "main"}},
		Commits:  []repo.Commit{{SHA: sha, Branch: "main"}},
		Releases: []repo.Release{{Tag: "v1", HeadSHA: sha, Artifacts: []repo.Artifact{{Name: "old-long-name.bin"}}}},
	}
	next := prev
	next.Releases = []repo.Release{{Tag: "v2", HeadSHA: sha, Artifacts: []repo.Artifact{{Name: "new.bin"}}}}
	g := scene.Frame(scene.Transition(prev, next, 8), 8, 60, 20)
	if got := rowText(g[17]); got != "▤ new.bin" {
		t.Fatalf("artifact row = %q, want %q\n%s", got, "▤ new.bin", g.String())
	}
}

// Two releases can share a lane when their head commits sit on one branch. On
// a weighted narrow column both strips overflow into the same cell below them:
// a later release with only healthy omissions must not cover an earlier real
// failure, and the mark must name the deployment it still reports.
func TestOverflowMarkKeepsFailureAcrossReleases(t *testing.T) {
	const oldSHA, newSHA = "1111111111111111111111111111111111111111", "2222222222222222222222222222222222222222"
	branches := []repo.Branch{{Name: "main"}}
	for i := 1; i < 13; i++ {
		branches = append(branches, repo.Branch{Name: fmt.Sprintf("lane-%d", i)})
	}
	prev := repo.Snapshot{
		Branches: branches,
		Commits:  []repo.Commit{{SHA: oldSHA, Branch: "main"}},
		Releases: []repo.Release{{Tag: "v1", HeadSHA: oldSHA}},
	}
	for i := int64(1); i <= 4; i++ {
		state := "success"
		if i == 4 {
			state = "failure"
		}
		prev.Deployments = append(prev.Deployments, repo.Deployment{ID: i, HeadSHA: oldSHA, Environment: "env", State: state})
	}
	next := prev
	next.Commits = append([]repo.Commit(nil), prev.Commits...)
	next.Commits = append(next.Commits, repo.Commit{SHA: newSHA, Branch: "main", Parents: []string{oldSHA}})
	next.Releases = append([]repo.Release(nil), prev.Releases...)
	next.Releases = append(next.Releases, repo.Release{Tag: "v2", HeadSHA: newSHA})
	next.Deployments = append([]repo.Deployment(nil), prev.Deployments...)
	for i := int64(5); i <= 8; i++ {
		next.Deployments = append(next.Deployments, repo.Deployment{ID: i, HeadSHA: newSHA, Environment: "env", State: "success"})
	}

	g, owners := scene.FrameWithOwners(scene.Transition(prev, next, 8), 8, 60, 20)
	marks := 0
	for y, row := range g {
		for x, c := range row {
			if c.Rune != '!' {
				continue
			}
			marks++
			if c.Style != scene.Fail || owners[y][x] != "deployment:4" {
				t.Fatalf("overflow mark = %+v, owner %q, want fail palette naming deployment:4\n%s", c, owners[y][x], g.String())
			}
		}
	}
	if marks != 1 {
		t.Fatalf("got %d overflow marks, want one\n%s", marks, g.String())
	}
}

// A branch cut off by the strip is omitted whole. Leaving its connector would
// draw a '─' that names no deployment at all.
func TestTruncatedBranchKeepsConnectorWithGlyph(t *testing.T) {
	const sha = "1111111111111111111111111111111111111111"
	s := repo.Snapshot{
		Branches:    []repo.Branch{{Name: "main"}},
		Commits:     []repo.Commit{{SHA: sha, Branch: "main"}},
		Releases:    []repo.Release{{Tag: "v1", HeadSHA: sha}},
		Deployments: make([]repo.Deployment, 0, 4),
	}
	for i := int64(1); i <= 4; i++ {
		s.Deployments = append(s.Deployments, repo.Deployment{ID: i, HeadSHA: sha, Environment: "env", State: "success"})
	}
	g := scene.Frame(scene.Build(s), 0, 20, 20)
	row := rowText(g[18])
	if strings.HasSuffix(row, "─") || strings.Count(row, "─") != strings.Count(row, "▣") {
		t.Fatalf("dangling connector in %q\n%s", row, g.String())
	}
}

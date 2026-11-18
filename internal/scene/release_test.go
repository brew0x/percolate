package scene_test

import (
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

// A snapshot that records no artifacts must say so rather than drawing a
// citation for an empty list, just as an unrecorded job list does.
func TestReleaseWithoutArtifactsSaysSo(t *testing.T) {
	s := repo.Snapshot{Releases: []repo.Release{{Tag: "v1", HeadSHA: "abc"}}}
	m := scene.Build(s).Marks[0]
	if !strings.Contains(m.Detail, "artifacts not recorded") {
		t.Fatalf("detail = %q", m.Detail)
	}
	if len(m.Artifacts) != 0 {
		t.Fatalf("invented artifacts: %+v", m.Artifacts)
	}
}

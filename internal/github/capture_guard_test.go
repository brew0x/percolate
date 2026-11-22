package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brew0x/percolate/internal/repo"
)

// These helpers back the opt-in live capture probe in live_capture_test.go,
// which is excluded from make check because it needs a token. Keeping them in
// an untagged file lets CI verify the safety rails, the guard ordering and the
// private write even though the probe itself can only run where a token exists.
//
// captureRepoPublic refuses to record a repository the token can read but that
// GitHub does not serve publicly. captureGraphQLProof counts a live recording
// only when one open pull request carries both fields fetchPullDetails fills
// from GraphQL: a named review state and a head commit on a PR-only lane with
// an author, date and parent. Reviewing one PR and anchoring another is not
// proof that the same detail query decoded both selections, because
// fetchPullDetails skips null aliases and head targets without a commit anchor
// instead of failing the read. The review and anchor counts are diagnostics;
// only proofs decides whether a capture passes. recordPublicCapture runs these
// guards in the same order as the probe and writes through writeCapture.
func captureRepoPublic(defaultBranch string, private bool, visibility string) error {
	if defaultBranch == "" {
		return errors.New("GitHub repository metadata had no default branch")
	}
	if private || visibility != "public" {
		return fmt.Errorf("not a public repository (visibility %q, private %t)", visibility, private)
	}
	return nil
}

func captureGraphQLProof(snap *repo.Snapshot, defaultBranch string) (proofs, reviews, anchors int) {
	heads, anchored := map[string]bool{}, map[string]bool{}
	for _, p := range snap.PullRequests {
		heads[p.Head] = true
	}
	for _, cm := range snap.Commits {
		if heads[cm.Branch] && cm.Branch != defaultBranch && cm.SHA != "" && cm.Author != "" &&
			!cm.Time.IsZero() && len(cm.Parents) > 0 {
			anchored[cm.Branch] = true
			anchors++
		}
	}
	for _, p := range snap.PullRequests {
		reviewed := false
		for _, r := range p.Reviews {
			if r.Author != "" && r.State != "" {
				reviewed = true
				reviews++
				break
			}
		}
		if reviewed && anchored[p.Head] {
			proofs++
		}
	}
	return proofs, reviews, anchors
}

// writeCapture writes a recording through a 0600 temp file and an atomic
// rename, so public snapshot data is never world-readable on disk, not even
// briefly. The live probe and the offline tests share this exact path.
func writeCapture(out string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(out), ".percolate-capture-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, out); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// recordPublicCapture is the body of the opt-in live GraphQL probe. Only token
// resolution stays in the tagged file; the visibility guard, the proof check
// and the private write live here so make check exercises their order and
// result with a local server. The guard runs before any snapshot is built, so
// a private repository the token can read is never assembled or written.
func recordPublicCapture(ctx context.Context, client *Client, owner, repoName, out string) (data []byte, prs, proofs, reviews, anchors int, err error) {
	body, err := client.ListPage(ctx, fmt.Sprintf("/repos/%s/%s", owner, repoName))
	if err != nil {
		return nil, 0, 0, 0, 0, fmt.Errorf("read repository metadata: %w", err)
	}
	var meta struct {
		DefaultBranch string `json:"default_branch"`
		Private       bool   `json:"private"`
		Visibility    string `json:"visibility"`
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		return nil, 0, 0, 0, 0, fmt.Errorf("decode repository metadata: %w", err)
	}
	if err := captureRepoPublic(meta.DefaultBranch, meta.Private, meta.Visibility); err != nil {
		return nil, 0, 0, 0, 0, err
	}
	snap, err := client.BuildSnapshot(ctx, DefaultSnapshotOptions(owner, repoName))
	if err != nil {
		return nil, 0, 0, 0, 0, fmt.Errorf("live read failed (a failed GraphQL detail query rejects the whole snapshot): %w", err)
	}
	prs = len(snap.PullRequests)
	proofs, reviews, anchors = captureGraphQLProof(&snap, meta.DefaultBranch)
	if prs == 0 || proofs == 0 {
		return nil, prs, proofs, reviews, anchors, fmt.Errorf("no open PR with both a GraphQL-filled review and a head-commit anchor in %s/%s (%d PRs, %d named reviews, %d head anchors); choose another public repository", owner, repoName, prs, reviews, anchors)
	}
	data, err = json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return nil, prs, proofs, reviews, anchors, err
	}
	data = append(data, '\n')
	if err := writeCapture(out, data); err != nil {
		return nil, prs, proofs, reviews, anchors, err
	}
	return data, prs, proofs, reviews, anchors, nil
}

// captureWrappedServer serves fakeRepo's transport with caller-chosen metadata
// and counts GraphQL detail reads, so a test can tell whether BuildSnapshot
// ran at all.
func captureWrappedServer(t *testing.T, metadata string, graphql *int) *httptest.Server {
	t.Helper()
	inner := fakeRepoHandler(t, 1, new(int))
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widgets" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(metadata))
			return
		}
		if r.URL.Path == "/graphql" {
			*graphql = *graphql + 1
		}
		inner(w, r)
	}))
}

func TestWriteCaptureIsPrivate(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "recording.json")
	// An existing world-readable recording must be replaced by a 0600 inode,
	// not left in place or appended to.
	if err := os.WriteFile(out, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(out, 0644); err != nil {
		t.Fatal(err)
	}
	data := []byte("{\"public\":true}\n")
	if err := writeCapture(out, data); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Fatalf("content %q, want %q", got, data)
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("mode %04o, want 0600", perm)
	}
	for _, e := range entries(t, dir) {
		if strings.HasPrefix(e, ".percolate-capture-") {
			t.Fatalf("temp file %s left behind", e)
		}
	}
	// A failed rename must also clean up its temp file.
	if err := writeCapture(filepath.Join(dir, "missing", "recording.json"), data); err == nil {
		t.Fatal("write into a missing directory should fail")
	}
	for _, e := range entries(t, dir) {
		if strings.HasPrefix(e, ".percolate-capture-") {
			t.Fatalf("temp file %s left behind after failed rename", e)
		}
	}
}

func entries(t *testing.T, dir string) []string {
	t.Helper()
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, f.Name())
	}
	return names
}

func TestRecordPublicCaptureGuardsBeforeBuild(t *testing.T) {
	for _, tc := range []struct {
		name     string
		metadata string
	}{
		{"private", `{"default_branch":"main","private":true,"visibility":"public"}`},
		{"internal", `{"default_branch":"main","private":false,"visibility":"internal"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			graphql := 0
			server := captureWrappedServer(t, tc.metadata, &graphql)
			defer server.Close()
			c, _ := NewClient("test-token")
			c.baseURL = server.URL
			out := filepath.Join(t.TempDir(), "recording.json")
			_, _, _, _, _, err := recordPublicCapture(context.Background(), c, "acme", "widgets", out)
			if err == nil || !strings.Contains(err.Error(), "not a public repository") {
				t.Fatalf("got %v, want a public-repository refusal", err)
			}
			if graphql != 0 {
				t.Fatalf("GraphQL detail read ran %d times before the visibility guard", graphql)
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatalf("recording was written after the guard failed: %v", err)
			}
		})
	}
}

func TestRecordPublicCaptureProofAndPrivateWrite(t *testing.T) {
	graphql := 0
	server := captureWrappedServer(t, `{"default_branch":"main","private":false,"visibility":"public"}`, &graphql)
	defer server.Close()
	c, _ := NewClient("test-token")
	c.baseURL = server.URL
	out := filepath.Join(t.TempDir(), "recording.json")
	data, prs, proofs, reviews, anchors, err := recordPublicCapture(context.Background(), c, "acme", "widgets", out)
	if err != nil {
		t.Fatal(err)
	}
	if graphql == 0 || prs != 1 || proofs != 1 || reviews == 0 || anchors == 0 {
		t.Fatalf("live proof missing: graphql=%d prs=%d proofs=%d reviews=%d anchors=%d", graphql, prs, proofs, reviews, anchors)
	}
	if !json.Valid(data) {
		t.Fatal("recording is not valid JSON")
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("mode %04o, want 0600", perm)
	}
}

func TestCaptureRepoPublic(t *testing.T) {
	cases := []struct {
		name       string
		branch     string
		private    bool
		visibility string
		wantErr    string
	}{
		{"public", "master", false, "public", ""},
		{"no default branch", "", false, "public", "no default branch"},
		{"private flag", "master", true, "public", "not a public repository"},
		{"private visibility", "master", false, "private", "not a public repository"},
		{"internal visibility", "master", false, "internal", "not a public repository"},
		{"missing visibility", "master", false, "", "not a public repository"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := captureRepoPublic(tc.branch, tc.private, tc.visibility)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("public repository rejected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("got %v, want an error containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestCaptureGraphQLProof(t *testing.T) {
	head := "jfehrle:debugger_ltac2"
	other := "other:topic"
	when := time.Date(2026, 11, 21, 11, 27, 42, 0, time.UTC)
	anchor := repo.Commit{SHA: "7477", Branch: head, Author: "Jim Fehrle",
		Time: when, Parents: []string{"5393"}}
	otherAnchor := repo.Commit{SHA: "8888", Branch: other, Author: "Someone Else",
		Time: when, Parents: []string{"5393"}}
	pr := repo.PullRequest{Number: 64, Head: head,
		Reviews: []repo.Review{{Author: "ppedrot", State: "changes_requested"}}}
	defaultCommit := repo.Commit{SHA: "base", Branch: "master", Author: "Someone",
		Time: when, Parents: []string{"older"}}

	cases := []struct {
		name                                 string
		snap                                 repo.Snapshot
		wantProofs, wantReviews, wantAnchors int
	}{
		{
			name:       "review and head anchor",
			snap:       repo.Snapshot{PullRequests: []repo.PullRequest{pr}, Commits: []repo.Commit{defaultCommit, anchor}},
			wantProofs: 1, wantReviews: 1, wantAnchors: 1,
		},
		{
			name: "review without author is not proof",
			snap: repo.Snapshot{
				PullRequests: []repo.PullRequest{{Number: 64, Head: head,
					Reviews: []repo.Review{{State: "changes_requested"}}}},
				Commits: []repo.Commit{anchor},
			},
			wantProofs: 0, wantReviews: 0, wantAnchors: 1,
		},
		{
			name: "head commit without parent is not an anchor",
			snap: repo.Snapshot{
				PullRequests: []repo.PullRequest{pr},
				Commits:      []repo.Commit{{SHA: "7477", Branch: head, Author: "Jim Fehrle", Time: when}},
			},
			wantProofs: 0, wantReviews: 1, wantAnchors: 0,
		},
		{
			name: "review and anchor on different PRs are not proof",
			snap: repo.Snapshot{
				PullRequests: []repo.PullRequest{{Number: 64, Head: "reviewer:topic",
					Reviews: []repo.Review{{Author: "ppedrot", State: "changes_requested"}}},
					{Number: 65, Head: other}},
				Commits: []repo.Commit{otherAnchor},
			},
			wantProofs: 0, wantReviews: 1, wantAnchors: 1,
		},
		{
			name:       "no pull request means no proof",
			snap:       repo.Snapshot{Commits: []repo.Commit{anchor}},
			wantProofs: 0, wantReviews: 0, wantAnchors: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap := tc.snap
			proofs, reviews, anchors := captureGraphQLProof(&snap, "master")
			if proofs != tc.wantProofs || reviews != tc.wantReviews || anchors != tc.wantAnchors {
				t.Fatalf("got %d proofs, %d reviews and %d anchors, want %d, %d and %d",
					proofs, reviews, anchors, tc.wantProofs, tc.wantReviews, tc.wantAnchors)
			}
		})
	}
}

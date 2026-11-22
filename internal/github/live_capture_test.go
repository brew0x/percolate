//go:build graphqlcapture

// This opt-in probe is the live GraphQL acceptance check for #21. It is
// excluded from make check so the normal suite stays offline. Run it only in an
// environment with a GitHub token, against a public repository that has an open
// pull request with at least one review:
//
//	GITHUB_TOKEN=... go test -tags graphqlcapture ./internal/github \
//	  -run TestLiveGraphQLCapture -v \
//	  -args -capture-out=/tmp/open-pr-graphql.json -capture-repo=rocq-prover/rfcs
//
// The repository's visibility is checked before any snapshot is built: private
// and internal repositories are refused, and the recording is written with mode
// 0600 through a temp file and rename. Private snapshot data can therefore
// never land in a world-readable file before provenance review. The token is
// never printed or persisted. A pass shows the real GraphQL selections for
// reviews and head-commit anchors decoded into a snapshot, and fails if either
// field is absent, because fetchPullDetails skips null aliases rather than
// erroring. The proof is complete only once someone with a token runs this and
// the resulting recording, provenance and goldens are reviewed and checked in.
// This probe does not by itself close #21.
package github

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var (
	captureOut  = flag.String("capture-out", "", "scratch path outside the repository for the public snapshot")
	captureRepo = flag.String("capture-repo", "rocq-prover/rfcs", "public owner/repo with a reviewed open pull request")
)

func TestLiveGraphQLCapture(t *testing.T) {
	if *captureOut == "" {
		t.Fatal("set -capture-out to a scratch path outside the repository (public snapshot data only)")
	}
	parts := strings.Split(*captureRepo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		t.Fatalf("-capture-repo must be owner/repo, got %q", *captureRepo)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	token, err := ResolveToken(ctx)
	if err != nil {
		t.Fatalf("no GitHub token here: %v (unauthenticated POST /graphql returns 403)", err)
	}
	client, err := NewClient(token)
	if err != nil {
		t.Fatal(err)
	}

	meta := struct {
		DefaultBranch string `json:"default_branch"`
		Private       bool   `json:"private"`
		Visibility    string `json:"visibility"`
	}{}
	body, err := client.ListPage(ctx, fmt.Sprintf("/repos/%s/%s", parts[0], parts[1]))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		t.Fatal(err)
	}
	// The token can read private repositories; never build or write a snapshot
	// from one. Guarded by TestCaptureRepoPublic in the offline suite.
	if err := captureRepoPublic(meta.DefaultBranch, meta.Private, meta.Visibility); err != nil {
		t.Fatalf("refusing to record %s: %v", *captureRepo, err)
	}
	snap, err := client.BuildSnapshot(ctx, DefaultSnapshotOptions(parts[0], parts[1]))
	if err != nil {
		t.Fatalf("live read failed: %v (a failed GraphQL detail query rejects the whole snapshot)", err)
	}

	// fetchPullDetails skips null PR aliases and head targets with no commit
	// anchor instead of failing the read, so a clean record is not proof. These
	// counts are also checked against local snapshots by TestCaptureGraphQLProof.
	reviews, anchors := captureGraphQLProof(&snap, meta.DefaultBranch)
	if len(snap.PullRequests) == 0 || reviews == 0 {
		t.Fatalf("no open PR with a GraphQL-filled review in %s (%d PRs, %d reviews); choose another public repository",
			*captureRepo, len(snap.PullRequests), reviews)
	}
	if anchors == 0 {
		t.Fatalf("no GraphQL-filled head-commit anchor in %s; choose another public repository", *captureRepo)
	}

	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	// CreateTemp creates the file with 0600 and the rename replaces the
	// destination inode, so the recording is never world-readable on disk.
	tmp, err := os.CreateTemp(filepath.Dir(*captureOut), ".percolate-capture-*")
	if err != nil {
		t.Fatal(err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		t.Fatal(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		t.Fatal(err)
	}
	if err := os.Rename(tmpName, *captureOut); err != nil {
		os.Remove(tmpName)
		t.Fatal(err)
	}
	t.Logf("recorded %s: %d open PRs, %d reviews, %d head-commit anchors", *captureRepo, len(snap.PullRequests), reviews, anchors)
	t.Logf("recording: %s", *captureOut)
	t.Logf("SHA-256: %x", sha256.Sum256(data))
}

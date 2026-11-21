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
// It writes only public snapshot data to -capture-out and prints that file's
// SHA-256 for the README; the token is never printed or persisted. A pass shows
// the real GraphQL selections for reviews and head-commit anchors decoded into
// a snapshot. The proof is complete only once someone with a token runs this,
// and the resulting recording, provenance and goldens are reviewed and checked
// in. This probe does not by itself close #21.
package github

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"os"
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
	}{}
	body, err := client.ListPage(ctx, fmt.Sprintf("/repos/%s/%s", parts[0], parts[1]))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &meta); err != nil || meta.DefaultBranch == "" {
		t.Fatal("GitHub repository metadata had no default branch")
	}
	snap, err := client.BuildSnapshot(ctx, DefaultSnapshotOptions(parts[0], parts[1]))
	if err != nil {
		t.Fatalf("live read failed: %v (a failed GraphQL detail query rejects the whole snapshot)", err)
	}

	prLanes := map[string]bool{}
	reviews := 0
	for _, p := range snap.PullRequests {
		prLanes[p.Head] = true
		for _, r := range p.Reviews {
			if r.Author != "" && r.State != "" {
				reviews++
			}
		}
	}
	if len(snap.PullRequests) == 0 || reviews == 0 {
		t.Fatalf("no open PR with a GraphQL-filled review in %s (%d PRs, %d reviews); choose another public repository",
			*captureRepo, len(snap.PullRequests), reviews)
	}
	// The REST commit list only carries the default branch. An anchor on a
	// different PR head lane, with an author, date and parent, can only have
	// come from the live headRef/target Commit selection in fetchPullDetails.
	anchors := 0
	for _, cm := range snap.Commits {
		if prLanes[cm.Branch] && cm.Branch != meta.DefaultBranch && cm.SHA != "" &&
			cm.Author != "" && !cm.Time.IsZero() && len(cm.Parents) > 0 {
			anchors++
		}
	}
	if anchors == 0 {
		t.Fatalf("no GraphQL-filled head-commit anchor in %s; choose another public repository", *captureRepo)
	}

	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(*captureOut, data, 0644); err != nil {
		t.Fatal(err)
	}
	t.Logf("recorded %s: %d open PRs, %d reviews, %d head-commit anchors", *captureRepo, len(snap.PullRequests), reviews, anchors)
	t.Logf("recording: %s", *captureOut)
	t.Logf("SHA-256: %x", sha256.Sum256(data))
}

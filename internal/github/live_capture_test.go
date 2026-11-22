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
// 0600 through a temp file and rename. Those guards, the proof check and the
// private write live in the untagged capture_guard_test.go so make check tests
// their order and mode with a local server; only token resolution and logging
// stay here. The token is never printed or persisted. A pass shows the real
// GraphQL selections for reviews and head-commit anchors decoded into a
// snapshot, and fails if either field is absent, because fetchPullDetails skips
// null aliases rather than erroring. The proof is complete only once someone
// with a token runs this and the resulting recording, provenance and goldens
// are reviewed and checked in. This probe does not by itself close #21.
package github

import (
	"context"
	"crypto/sha256"
	"flag"
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

	// The shared helper checks that the token can read a public repository
	// before any snapshot is built, then requires a live GraphQL review and
	// head-commit anchor before writing 0600. Offline tests cover that order.
	data, prs, reviews, anchors, err := recordPublicCapture(ctx, client, parts[0], parts[1], *captureOut)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("recorded %s: %d open PRs, %d reviews, %d head-commit anchors", *captureRepo, prs, reviews, anchors)
	t.Logf("recording: %s", *captureOut)
	t.Logf("SHA-256: %x", sha256.Sum256(data))
}

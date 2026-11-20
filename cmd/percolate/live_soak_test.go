package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/brew0x/percolate/internal/live"
	"github.com/brew0x/percolate/internal/scene"
)

// watchServer is a stateful local GitHub fake for the live-read path. It
// answers every endpoint BuildSnapshot reads, honours If-None-Match with a
// versioned ETag so repeated polls take the same 304 path a long watch takes,
// and lets the test change the recorded state or fail one poll between cycles.
// It never draws: rendering stays pure over the snapshots it assembles.
type watchServer struct {
	t           *testing.T
	mu          sync.Mutex
	version     int
	commits     string
	run         string
	fail        bool
	notModified int
	srv         *httptest.Server
}

func shaJSON(n int, branch, when, parents string) string {
	return fmt.Sprintf(`{"sha":"%s","parents":%s,"commit":{"author":{"name":"robin","date":"%s"}},"branch":"%s"}`,
		sha(n), parents, when, branch)
}

func newWatchServer(t *testing.T) *watchServer {
	t.Helper()
	s := &watchServer{
		t: t,
		commits: "[" + shaJSON(2, "main", "2026-01-02T00:00:00Z", "[]") + "," +
			shaJSON(1, "main", "2026-01-01T00:00:00Z", fmt.Sprintf(`[{"sha":"%s"}]`, sha(2))) + "]",
		run: fmt.Sprintf(`{"total_count":1,"workflow_runs":[{"id":5,"head_sha":"%s","status":"in_progress","conclusion":null}]}`, sha(2)),
	}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

func (s *watchServer) Close()      { s.srv.Close() }
func (s *watchServer) URL() string { return s.srv.URL }

// change inserts a newer commit and finishes the running run: a real recorded
// change that must animate once and then settle.
func (s *watchServer) change() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version++
	items := strings.TrimSuffix(strings.TrimPrefix(s.commits, "["), "]")
	s.commits = "[" + shaJSON(3, "main", "2026-01-04T00:00:00Z", "[]") + "," + items + "]"
	s.run = fmt.Sprintf(`{"total_count":1,"workflow_runs":[{"id":5,"head_sha":"%s","status":"completed","conclusion":"failure"}]}`, sha(2))
}

// failNext makes the next repository-meta read return 500 once, emulating a
// transient GitHub error between polls.
func (s *watchServer) failNext() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail = true
}

func (s *watchServer) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/graphql" {
		// The PR's head commit is already on the default branch, so the real
		// detail query adds no commit; it still runs on every poll.
		fmt.Fprintf(w, `{"data":{"repository":{"p0":{"number":1,"headRefOid":"%s",`+
			`"headRefName":"flow-1","baseRefName":"main",`+
			`"headRef":{"target":{"oid":"%s","committedDate":"2026-01-02T00:00:00Z",`+
			`"author":{"name":"robin"},"parents":{"nodes":[]}}},`+
			`"reviews":{"nodes":[{"author":{"login":"sage"},"state":"APPROVED"}]}}}}}`, sha(2), sha(2))
		return
	}

	s.mu.Lock()
	version, fail := s.version, s.fail
	if fail && r.URL.Path == "/repos/acme/widgets" {
		s.fail = false
		s.mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	var body string
	switch r.URL.Path {
	case "/repos/acme/widgets":
		body = `{"default_branch":"main"}`
	case "/repos/acme/widgets/branches":
		body = `[{"name":"main"},{"name":"flow-1"}]`
	case "/repos/acme/widgets/commits":
		body = s.commits
	case "/repos/acme/widgets/pulls":
		body = `[{"number":1,"state":"open","head":{"ref":"flow-1"},"base":{"ref":"main"}}]`
	case "/repos/acme/widgets/actions/runs":
		body = s.run
	case "/repos/acme/widgets/releases", "/repos/acme/widgets/deployments":
		body = `[]`
	default:
		s.t.Errorf("unexpected live-read path %s", r.URL.Path)
		s.mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
		return
	}
	s.mu.Unlock()

	etag := fmt.Sprintf(`"v%d"`, version)
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		s.mu.Lock()
		s.notModified++
		s.mu.Unlock()
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, body)
}

// The live-watch acceptance asks for a public repository watched for ten
// minutes without errors. GitHub access is not available here, but its real
// read path is: this test drives watch() -> github.BuildSnapshot -> the TUI
// through ten consecutive polls of a local fake, including the ETag/304 path,
// one recorded change, and one transient error. It proves the window keeps
// polling, animates the change once, notes and recovers from a failed read,
// and still renders exactly what the final snapshot builds.
func TestLiveWatchTenPolls(t *testing.T) {
	srv := newWatchServer(t)
	defer srv.Close()
	t.Setenv("GITHUB_TOKEN", "test-token")
	t.Setenv("PERCOLATE_GITHUB_URL", srv.URL())

	snap, fetch, err := watch(context.Background(), "", []string{"acme/widgets"})
	if err != nil {
		t.Fatal(err)
	}
	m := model{s: scene.Build(snap), prev: snap, cur: snap, fetch: fetch, width: 80, height: 24}
	if fetch == nil {
		t.Fatal("test setup: live view has no fetcher")
	}

	const polls = 10
	transitions := 0
	for poll := 1; poll <= polls; poll++ {
		switch poll {
		case 4: // the repository changes between polls 3 and 4
			srv.change()
		case 8: // one transient GitHub failure, then a normal retry
			srv.failNext()
		}

		msg, ok := m.fetchCmd()().(snapshotMsg)
		if !ok {
			t.Fatalf("poll %d: fetch did not return a snapshot message", poll)
		}
		before := m.cur
		updated, cmd := m.Update(msg)
		m = updated.(model)
		if cmd == nil {
			t.Fatalf("poll %d: live view stopped polling", poll)
		}
		if poll == 8 {
			if msg.err == nil || m.refreshErr == "" {
				t.Fatalf("poll %d: transient error was not noted", poll)
			}
			if !live.Equal(m.cur, before) {
				t.Fatalf("poll %d: a failed read changed the scene", poll)
			}
		} else {
			if msg.err != nil || m.refreshErr != "" {
				t.Fatalf("poll %d: unexpected error %v", poll, msg.err)
			}
			if m.transiting {
				transitions++
			}
		}

		// Let the animation run to completion before the next poll, as the
		// 125 ms tick does on screen over the four seconds after a change.
		for i := 0; i <= scene.TransitionFrames; i++ {
			updated, _ = m.Update(tickMsg{})
			m = updated.(model)
		}
		if m.transiting {
			t.Fatalf("poll %d: transition did not settle", poll)
		}
	}

	if transitions != 1 {
		t.Fatalf("ten polls animated %d times, want exactly one", transitions)
	}
	srv.mu.Lock()
	reused := srv.notModified
	srv.mu.Unlock()
	if reused == 0 {
		t.Fatal("ten polls never reused an ETag-cached page")
	}
	// The final window must be the pure build of its last snapshot, not state
	// accumulated across polls: frame 0 of both is byte-for-byte identical.
	want := scene.Frame(scene.Build(m.cur), 0, m.width, m.height-1).String()
	got := scene.Frame(m.s, 0, m.width, m.height-1).String()
	if got != want {
		t.Fatal("final live scene is not the build of its last snapshot")
	}
	if !strings.Contains(got, "●") {
		t.Fatal("final live scene lost its commit beads")
	}
}

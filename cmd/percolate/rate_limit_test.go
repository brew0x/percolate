package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/brew0x/percolate/internal/live"
	"github.com/brew0x/percolate/internal/repo"
	"github.com/brew0x/percolate/internal/scene"
)

// A refused read with a recorded reset pauses the live window: the footer says
// so, no read is issued before the reset, and a read after it succeeds and
// returns to the ordinary cadence. This drives the real CLI fetch path through
// an httptest GitHub fake, including its 403 and reset headers.
func TestLiveRefreshPausesUntilRateLimitReset(t *testing.T) {
	srv := newWatchServer(t)
	defer srv.Close()
	t.Setenv("GITHUB_TOKEN", "test-token")
	t.Setenv("PERCOLATE_GITHUB_URL", srv.URL())

	snap, fetch, err := watch(context.Background(), "", []string{"acme/widgets"})
	if err != nil {
		t.Fatal(err)
	}
	// The recorder keeps every wait the model schedules and still returns the
	// real timer, so the test proves both the wait it chose and the behaviour
	// that follows, including the request instants below.
	var waits []time.Duration
	record := func(d time.Duration) tea.Cmd {
		waits = append(waits, d)
		return pollAfter(d)
	}
	m := model{s: scene.Build(snap), prev: snap, cur: snap, fetch: fetch, width: 100, height: 20, schedule: record}

	// X-RateLimit-Reset is a Unix second. Three seconds leaves room for the
	// no-fetch check below whatever the sub-second phase of the clock is.
	reset := time.Now().Add(3 * time.Second).Unix()
	srv.limitNext(reset)
	msg, ok := m.fetchCmd()().(snapshotMsg)
	if !ok {
		t.Fatal("rate-limited fetch did not return a snapshot message")
	}
	if msg.err == nil {
		t.Fatal("expected a rate-limit refusal")
	}
	wantReset := time.Unix(reset, 0).UTC()
	if got := live.ResetTimeOf(msg.err); !got.Equal(wantReset) {
		t.Fatalf("error carried reset %s, want %s", got, wantReset)
	}

	updated, retry := m.Update(msg)
	m = updated.(model)
	if retry == nil {
		t.Fatal("no retry scheduled after the refusal")
	}
	// The refusal handed the scheduler a wait that reaches exactly to the
	// recorded reset. It was chosen before this line ran, so from here it
	// cannot be any shorter.
	if len(waits) == 0 {
		t.Fatal("the refusal scheduled no wait")
	}
	if w := waits[len(waits)-1]; w < wantReset.Sub(time.Now()) {
		t.Fatalf("refused read scheduled a %s wait, short of the recorded reset",
			w.Round(time.Millisecond))
	}
	footer := strings.Join(m.footer(), "\n")
	for _, want := range []string{"refresh failed:", "resets in", "paused until reset"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("footer did not report the pause (%q): %q", want, footer)
		}
	}

	// The scheduled command is the only thing that can issue another read.
	// Run it for real: it must stay pending, and the fake must stay untouched,
	// until the recorded reset has passed.
	before := srv.requestCount()
	fired := make(chan tea.Msg, 1)
	go func() { fired <- retry() }()
	select {
	case <-fired:
		t.Fatal("retry fired before the recorded reset")
	case <-time.After(400 * time.Millisecond):
	}
	if got := srv.requestCount(); got != before {
		t.Fatalf("%d extra fetches before the reset", got-before)
	}
	var poll tea.Msg
	select {
	case poll = <-fired:
	case <-time.After(4 * time.Second):
		t.Fatal("retry did not fire after the reset")
	}
	if _, ok := poll.(pollMsg); !ok {
		t.Fatalf("retry delivered %T, want a poll message", poll)
	}

	// Driving the delivered poll through Update issues the read, exactly as
	// the running program would. Starting the read ends the pause, so the
	// footer is read here, before the fetch returns and while the request is
	// in flight: it reports the failed read and an ordinary retry, never a
	// window claiming to wait while a request is already out.
	updated, read := m.Update(poll)
	m = updated.(model)
	if m.paused {
		t.Fatal("the pause outlived the wait that scheduled the read")
	}
	inFlight := strings.Join(m.footer(), "\n")
	if strings.Contains(inFlight, "paused until reset") {
		t.Fatalf("footer still claims a pause after the read started: %q", inFlight)
	}
	for _, want := range []string{"refresh failed:", "retrying"} {
		if !strings.Contains(inFlight, want) {
			t.Fatalf("in-flight footer lost %q: %q", want, inFlight)
		}
	}
	success, ok := read().(snapshotMsg)
	if !ok || success.err != nil {
		t.Fatalf("fetch after the reset failed: %v", success.err)
	}
	// Every request must land at or after the recorded reset: a retry that
	// fires early still reaches the fake here, so a fire after a few hundred
	// milliseconds cannot slip through. The grace only absorbs microscopic
	// clock slew between the timer's clock and the wall clock; it is far
	// shorter than any scheduling shortcut.
	const grace = 100 * time.Millisecond
	for _, at := range srv.requestTimes()[before:] {
		if at.Before(wantReset.Add(-grace)) {
			t.Fatalf("read issued at %s, before the recorded reset %s",
				at.UTC().Format(time.RFC3339Nano), wantReset.Format(time.RFC3339Nano))
		}
	}
	updated, next := m.Update(success)
	m = updated.(model)
	if m.paused || strings.Contains(strings.Join(m.footer(), "\n"), "paused until reset") {
		t.Fatalf("successful refresh kept the pause: %q", m.footer())
	}
	if next == nil {
		t.Fatal("refresh after the reset stopped polling")
	}
	// A success after the reset returns to the documented ordinary cadence:
	// the wait it schedules is read from the model's own scheduler, exactly,
	// so a regression that polls every few seconds cannot pass the way a
	// short watch of the clock would let it.
	if len(waits) != 2 {
		t.Fatalf("the window scheduled %d waits, want the pause and the resumption", len(waits))
	}
	if waits[1] != live.Interval {
		t.Fatalf("successful refresh resumed a %s cadence, want the ordinary %s interval",
			waits[1].Round(time.Millisecond), live.Interval)
	}
}

// The paused-until-reset behaviour of the running window cannot protect the
// first read: it happens before the TUI exists, and a rate-limited launch used
// to print one line and exit, leaving a window meant to be left running
// unstarted until someone relaunched it by hand. This drives the whole CLI
// against the local GitHub fake, whose first answer is a 403 with reset
// headers a few seconds out. Render mode keeps the exact read path a live
// launch takes while skipping the TUI, which a test cannot open honestly; the
// window itself is checked afterwards from the model run builds.
func TestWatchWaitsForFirstReadRateLimitReset(t *testing.T) {
	srv := newWatchServer(t)
	defer srv.Close()
	t.Setenv("GITHUB_TOKEN", "test-token")
	t.Setenv("PERCOLATE_GITHUB_URL", srv.URL())
	record := filepath.Join(t.TempDir(), "recorded.json")

	// X-RateLimit-Reset is a Unix second. Three seconds leaves room for the
	// not-exited check below whatever the sub-second phase of the clock is.
	reset := time.Now().Add(3 * time.Second).Unix()
	wantReset := time.Unix(reset, 0).UTC()
	srv.limitNext(reset)

	var out, errOut bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- run([]string{"render", "acme/widgets", "--record", record,
			"--frame", "0", "--size", "60x20", "--color=false"}, &out, &errOut)
	}()

	// While the budget is still exhausted the program stays alive...
	select {
	case code := <-done:
		t.Fatalf("the program exited %d before the recorded reset: %s", code, errOut.String())
	case <-time.After(400 * time.Millisecond):
	}
	// ...and neither reads again nor records anything it did not read.
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Fatalf("a refused first read recorded a snapshot: %v", err)
	}
	before := srv.requestCount()
	if before != 1 {
		t.Fatalf("%d requests before the reset, want only the refused meta read", before)
	}

	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("exit %d after the reset: %s", code, errOut.String())
		}
	case <-time.After(8 * time.Second):
		t.Fatal("the program did not finish after the recorded reset")
	}
	if errOut.Len() != 0 {
		t.Fatalf("a waited-out refusal still wrote to stderr: %q", errOut.String())
	}
	// Every request of the retry lands at or after the recorded reset. The
	// grace only absorbs microscopic clock slew between the timer's clock and
	// the wall clock the fake records; it is far shorter than any scheduling
	// shortcut, exactly as in the paused-window test above.
	const grace = 100 * time.Millisecond
	for _, at := range srv.requestTimes()[before:] {
		if at.Before(wantReset.Add(-grace)) {
			t.Fatalf("read issued at %s, before the recorded reset %s",
				at.UTC().Format(time.RFC3339Nano), wantReset.Format(time.RFC3339Nano))
		}
	}

	// Only a snapshot actually read may be recorded, and it is the ordinary
	// state the fake serves, not a refusal-shaped stub.
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	var snap repo.Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("recording is not a snapshot: %v", err)
	}
	if len(snap.Commits) != 2 || len(snap.PullRequests) != 1 ||
		len(snap.Runs) != 1 || snap.Runs[0].Status != "in_progress" {
		t.Fatalf("recording is not the state the fake serves: %+v", snap)
	}

	// The window that opens is the ordinary one. The frame drawn after the
	// wait is exactly the replay of the snapshot that was read, and a model
	// built the way run builds it carries no trace of the refusal that was
	// waited out: no failed-refresh reason, no pause, the read repository.
	var replay bytes.Buffer
	if code := run([]string{"render", "--fixture", record, "--frame", "0",
		"--size", "60x20", "--color=false"}, &replay, &errOut); code != 0 {
		t.Fatalf("replay exit %d: %s", code, errOut.String())
	}
	if replay.String() != out.String() || strings.Contains(out.String(), "\x1b") {
		t.Fatal("the frame after the reset is not the ordinary frame of the read snapshot")
	}
	s := scene.Build(snap)
	m := model{s: s, width: 100, height: 30, prev: snap, cur: snap,
		web: browserBase(), slug: "acme/widgets"}
	if len(s.Marks) > 0 {
		m.selectedID = s.Marks[0].ID
	}
	key := strings.Join(m.footer(), "\n")
	for _, trace := range []string{"refresh failed", "paused", "retrying"} {
		if strings.Contains(key, trace) {
			t.Fatalf("the first window's footer carried %q from the waited-out refusal: %q", trace, key)
		}
	}
	if !strings.Contains(m.View().Content, "●") {
		t.Fatal("the first window did not draw the read repository")
	}
}

// A plain first-read error is not a pause: today's one line, exit 1, and
// nothing recorded, because no snapshot was read. Only a refusal whose reset
// the transport recorded still ahead is worth waiting out.
func TestWatchFirstReadPlainErrorExitsAsBefore(t *testing.T) {
	srv := newWatchServer(t)
	defer srv.Close()
	t.Setenv("GITHUB_TOKEN", "test-token")
	t.Setenv("PERCOLATE_GITHUB_URL", srv.URL())
	srv.failNext()
	record := filepath.Join(t.TempDir(), "unwritten.json")

	var out, errOut bytes.Buffer
	code := run([]string{"render", "acme/widgets", "--record", record,
		"--frame", "0", "--size", "60x20", "--color=false"}, &out, &errOut)
	if code != 1 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	message := strings.TrimSuffix(errOut.String(), "\n")
	if !strings.Contains(message, "HTTP 500") || strings.Contains(message, "\n") {
		t.Fatalf("not a one-line first-read failure: %q", message)
	}
	if strings.Contains(message, "test-token") {
		t.Fatalf("first-read failure leaked the token: %q", message)
	}
	if out.Len() != 0 {
		t.Fatalf("a failed first read wrote to stdout: %q", out.String())
	}
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Fatalf("a failed first read recorded a snapshot: %v", err)
	}
}

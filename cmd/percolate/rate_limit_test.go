package main

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/brew0x/percolate/internal/live"
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

package live

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brew0x/percolate/internal/repo"
)

// resetError stands in for github.RateLimitError: the retry policy depends on
// the recorded reset time, not on the transport's concrete error type.
type resetError struct {
	error
	reset time.Time
}

func (e resetError) ResetTime() time.Time { return e.reset }
func (e resetError) Unwrap() error        { return e.error }

var _ = flag.Bool("update", false, "accepted for repository golden updates")

func TestEqual(t *testing.T) {
	a := repo.Snapshot{Now: time.Unix(0, 0).UTC(), Branches: []repo.Branch{{Name: "main"}}}
	b := repo.Snapshot{Now: time.Unix(0, 0).UTC(), Branches: []repo.Branch{{Name: "main"}}}
	if !Equal(a, b) {
		t.Fatal("identical snapshots compared unequal")
	}
	// A fresh capture timestamp is not repository state: a live poll stamps
	// every read with a new Now, which must not count as a change.
	b.Now = a.Now.Add(time.Hour)
	if !Equal(a, b) {
		t.Fatal("same state with a fresh capture timestamp compared unequal")
	}
	b.Branches[0].Name = "topic"
	if Equal(a, b) {
		t.Fatal("different snapshots compared equal")
	}
}

func TestRetryDelay(t *testing.T) {
	now := time.Date(2026, time.November, 20, 12, 0, 0, 0, time.UTC)
	reset := now.Add(3 * time.Minute)
	boom := errors.New("temporary failure")
	for _, tt := range []struct {
		name      string
		err       error
		want      time.Duration
		wantReset time.Time
	}{
		{"success", nil, Interval, time.Time{}},
		{"ordinary failure", boom, Interval, time.Time{}},
		{"recorded reset", resetError{boom, reset}, 3 * time.Minute, reset},
		{"wrapped reset", fmt.Errorf("read failed: %w", resetError{boom, reset}), 3 * time.Minute, reset},
		{"reset already past", resetError{boom, now.Add(-time.Minute)}, Interval, now.Add(-time.Minute)},
		{"zero reset", resetError{boom, time.Time{}}, Interval, time.Time{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := RetryDelay(tt.err, now, Interval); got != tt.want {
				t.Fatalf("RetryDelay = %s, want %s", got, tt.want)
			}
			if got := ResetTimeOf(tt.err); !got.Equal(tt.wantReset) {
				t.Fatalf("ResetTimeOf = %s, want %s", got, tt.wantReset)
			}
		})
	}
}

// recv drains either channel until n values arrive or the deadline passes.
func recvSnaps(t *testing.T, ch <-chan repo.Snapshot, n int) []repo.Snapshot {
	t.Helper()
	var got []repo.Snapshot
	deadline := time.After(2 * time.Second)
	for len(got) < n {
		select {
		case snap := <-ch:
			got = append(got, snap)
		case <-deadline:
			t.Fatalf("timed out after %d of %d snapshots", len(got), n)
		}
	}
	return got
}

// recvTimes drains the read-start channel until n poll starts arrive or the
// deadline passes.
func recvTimes(t *testing.T, ch <-chan time.Time, n int) []time.Time {
	t.Helper()
	var got []time.Time
	deadline := time.After(2 * time.Second)
	for len(got) < n {
		select {
		case at := <-ch:
			got = append(got, at)
		case <-deadline:
			t.Fatalf("timed out after %d of %d reads", len(got), n)
		}
	}
	return got
}

func TestWatcherPollsAndSkipsUnchanged(t *testing.T) {
	s1 := repo.Snapshot{Branches: []repo.Branch{{Name: "main"}}}
	s2 := repo.Snapshot{Branches: []repo.Branch{{Name: "topic"}}}
	sequence := []repo.Snapshot{s1, s1, s2}
	calls := 0
	fetch := func(context.Context) (repo.Snapshot, error) {
		i := min(calls, len(sequence)-1)
		calls++
		return sequence[i], nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	snaps, errs := make(chan repo.Snapshot), make(chan error)
	done := make(chan error, 1)
	go func() { done <- Watcher{Interval: 5 * time.Millisecond, Fetch: fetch}.Run(ctx, snaps, errs) }()

	got := recvSnaps(t, snaps, 2)
	if !Equal(got[0], s1) || !Equal(got[1], s2) {
		t.Fatalf("unexpected snapshot sequence: %+v", got)
	}
	cancel()
	<-done
	if _, open := <-snaps; open {
		t.Fatal("snapshots channel left open")
	}
	if _, open := <-errs; open {
		t.Fatal("errors channel left open")
	}
}

// The real fetch path stamps every successful read with a fresh Now, as
// github.BuildSnapshot does. Watcher must still skip a poll whose recorded
// state is unchanged, rather than emitting one snapshot per interval.
func TestWatcherIgnoresFreshCaptureTimestamps(t *testing.T) {
	base := repo.Snapshot{Branches: []repo.Branch{{Name: "main"}}}
	changed := repo.Snapshot{Branches: []repo.Branch{{Name: "main"}, {Name: "topic"}}}
	calls := 0
	fetch := func(context.Context) (repo.Snapshot, error) {
		calls++
		snap := base
		if calls > 2 {
			snap = changed
		}
		snap.Now = time.Now().UTC()
		return snap, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	snaps, errs := make(chan repo.Snapshot), make(chan error)
	done := make(chan error, 1)
	go func() { done <- Watcher{Interval: 5 * time.Millisecond, Fetch: fetch}.Run(ctx, snaps, errs) }()

	got := recvSnaps(t, snaps, 2)
	if !Equal(got[0], base) || !Equal(got[1], changed) {
		t.Fatalf("unexpected snapshot sequence: %+v", got)
	}
	// Several more polls have elapsed by now; none may have been replayed.
	select {
	case extra := <-snaps:
		t.Fatalf("unchanged poll replayed a snapshot: %+v", extra)
	case <-time.After(30 * time.Millisecond):
	}
	cancel()
	<-done
}

func TestWatcherSurfacesErrorsAndKeepsPolling(t *testing.T) {
	boom := errors.New("temporary failure")
	s1 := repo.Snapshot{Branches: []repo.Branch{{Name: "main"}}}
	calls := 0
	fetch := func(context.Context) (repo.Snapshot, error) {
		calls++
		if calls == 1 {
			return repo.Snapshot{}, boom
		}
		return s1, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	snaps, errs := make(chan repo.Snapshot), make(chan error)
	done := make(chan error, 1)
	go func() { done <- Watcher{Interval: 5 * time.Millisecond, Fetch: fetch}.Run(ctx, snaps, errs) }()

	select {
	case err := <-errs:
		if !errors.Is(err, boom) {
			t.Fatalf("wrong error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("error was not surfaced")
	}
	got := recvSnaps(t, snaps, 1)
	if !Equal(got[0], s1) {
		t.Fatal("polling stopped after a transient error")
	}
	cancel()
	<-done
}

// A rate-limit refusal must not be retried on the ordinary (here 20 ms)
// cadence. The reset window opens when the fake issues the refusal, not when
// the watcher starts, so a busy runner that spends early milliseconds on setup
// and error delivery can neither starve a correct watcher nor hide an early
// retry: the first read after the refusal is compared directly with the
// deadline recorded in the refusal.
func TestWatcherWaitsForRecordedReset(t *testing.T) {
	first := repo.Snapshot{Branches: []repo.Branch{{Name: "main"}}}
	next := repo.Snapshot{Branches: []repo.Branch{{Name: "topic"}}}
	const window = 90 * time.Millisecond
	boom := errors.New("GitHub rate limit exhausted")
	var calls atomic.Int32
	// The refusal and the first read after it both happen in the watcher's
	// goroutine, so refusedAt is written and read sequentially there; waited
	// is atomic because this test reads it.
	var refusedAt time.Time
	var waited atomic.Int64
	fetch := func(context.Context) (repo.Snapshot, error) {
		switch n := calls.Add(1); n {
		case 1:
			return first, nil
		case 2:
			// The deadline is recorded here, when the refusal is issued.
			refusedAt = time.Now()
			return repo.Snapshot{}, resetError{boom, refusedAt.Add(window)}
		default:
			// Only the first read after the refusal is the evidence; reads
			// after it run on the ordinary cadence and must not overwrite it.
			if n == 3 {
				waited.Store(int64(time.Since(refusedAt)))
			}
			return next, nil
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	snaps, errs := make(chan repo.Snapshot), make(chan error)
	done := make(chan error, 1)
	go func() { done <- Watcher{Interval: 20 * time.Millisecond, Fetch: fetch}.Run(ctx, snaps, errs) }()

	recvSnaps(t, snaps, 1)
	select {
	case err := <-errs:
		if !errors.Is(err, boom) {
			t.Fatalf("wrong error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("rate-limit error was not surfaced")
	}
	got := recvSnaps(t, snaps, 1)
	if !Equal(got[0], next) {
		t.Fatalf("snapshot after the reset was not the next read: %+v", got[0])
	}
	if gap := (time.Duration(waited.Load())).Round(time.Millisecond); gap < window {
		t.Fatalf("read issued %s after the refusal, before the recorded reset %s after it",
			gap, window)
	}
	cancel()
	<-done
}

// A read that outlasts most of the interval must not stretch the cadence:
// the next poll starts on the ordinary schedule, anchored to poll starts, not
// a full interval after the slow read returned. Scheduling from each read's
// return would start it 120 ms late here; starting it early is the other
// failure, so both ends of the gap are asserted.
func TestWatcherKeepsCadenceWhenAReadIsSlow(t *testing.T) {
	snap := repo.Snapshot{Branches: []repo.Branch{{Name: "main"}}}
	const interval = 200 * time.Millisecond
	const slow = 120 * time.Millisecond
	var calls atomic.Int32
	starts := make(chan time.Time, 16)
	fetch := func(context.Context) (repo.Snapshot, error) {
		// Record the instant the read starts, before it spends its time.
		starts <- time.Now()
		if calls.Add(1) == 2 {
			time.Sleep(slow)
		}
		return snap, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	snaps, errs := make(chan repo.Snapshot), make(chan error)
	done := make(chan error, 1)
	go func() { done <- Watcher{Interval: interval, Fetch: fetch}.Run(ctx, snaps, errs) }()

	recvSnaps(t, snaps, 1) // the immediate first read
	at := recvTimes(t, starts, 3)
	gap := at[2].Sub(at[1])
	const slack = slow / 2
	if gap < interval-slack || gap > interval+slack {
		t.Fatalf("slow read stretched the cadence: next poll started %s after the last, want ≈%s",
			gap.Round(time.Millisecond), interval)
	}
	cancel()
	<-done
}

func TestWatcherStopsOnCancel(t *testing.T) {
	fetch := func(context.Context) (repo.Snapshot, error) {
		return repo.Snapshot{Branches: []repo.Branch{{Name: "main"}}}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	snaps, errs := make(chan repo.Snapshot), make(chan error)
	done := make(chan error, 1)
	go func() { done <- Watcher{Interval: time.Hour, Fetch: fetch}.Run(ctx, snaps, errs) }()

	recvSnaps(t, snaps, 1)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("wrong stop error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not stop on cancel")
	}
}

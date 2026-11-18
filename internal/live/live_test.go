package live

import (
	"context"
	"errors"
	"flag"
	"testing"
	"time"

	"github.com/brew0x/percolate/internal/repo"
)

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

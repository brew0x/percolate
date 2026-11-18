// Package live owns live-mode refresh policy. It never renders: its only
// output is point-in-time snapshots that the scene renderer consumes, keeping
// the network boundary in internal/github and the drawing pure over snapshots.
//
// The terminal UI drives the same policy through Bubble Tea commands using
// Fetcher, Equal and Interval; Watcher is the channel-based form for stream
// consumers (for example, recording a sequence of snapshots).
package live

import (
	"context"
	"encoding/json"
	"time"

	"github.com/brew0x/percolate/internal/repo"
)

// Interval is how often live mode refreshes its view of the repository. One
// poll assembles one bounded snapshot; a longer cadence keeps an always-on
// window well inside GitHub's read budget while still showing activity as it
// happens.
const Interval = 60 * time.Second

// Fetcher assembles one point-in-time snapshot. The only network boundary is
// internal/github; the CLI supplies a Fetcher that calls it.
type Fetcher func(context.Context) (repo.Snapshot, error)

// Equal reports whether two snapshots carry identical recorded state. Now is
// the time a snapshot was captured, not repository state: every successful
// BuildSnapshot stamps a fresh Now, so including it would make every unchanged
// poll look like a change and replay its transition. Comparing the rest keeps
// the ADR 0003 promise that unchanged polls do not replay. Callers pass by
// value, so clearing Now here cannot change either snapshot.
func Equal(a, b repo.Snapshot) bool {
	a.Now = time.Time{}
	b.Now = time.Time{}
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return string(aj) == string(bj)
}

// Watcher polls a Fetcher on Interval and streams each changed snapshot.
type Watcher struct {
	Interval time.Duration
	Fetch    Fetcher
}

// Run fetches once immediately, then once per Interval, until ctx is done.
// Every successful snapshot that differs from the previous one is sent on
// snaps; every failure is sent on errs and polling continues on the next tick,
// so a transient GitHub error never freezes the window. Both channels are
// closed when Run returns. Run keeps no scene or frame state: callers own the
// transition between the snapshots they receive.
func (w Watcher) Run(ctx context.Context, snaps chan<- repo.Snapshot, errs chan<- error) error {
	defer close(snaps)
	defer close(errs)
	if w.Fetch == nil {
		return nil
	}
	interval := w.Interval
	if interval <= 0 {
		interval = Interval
	}

	var last repo.Snapshot
	have := false
	fetch := func() {
		snap, err := w.Fetch(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			select {
			case errs <- err:
			case <-ctx.Done():
			}
			return
		}
		if have && Equal(last, snap) {
			return
		}
		select {
		case snaps <- snap:
			last, have = snap, true
		case <-ctx.Done():
		}
	}

	fetch()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			fetch()
		}
	}
}

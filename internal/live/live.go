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
	"errors"
	"time"

	"github.com/brew0x/percolate/internal/repo"
)

// Interval is how often live mode refreshes its view of the repository. One
// poll assembles one bounded snapshot; a longer cadence keeps an always-on
// window well inside GitHub's read budget while still showing activity as it
// happens.
const Interval = 60 * time.Second

// ResetTimer is implemented by transport errors that record the instant a
// retry may resume. Keeping the interface here means refresh policy depends on
// recorded reset data, not on the network client's concrete error type.
type ResetTimer interface {
	error
	ResetTime() time.Time
}

// ResetTimeOf returns the recorded reset instant of a rate-limit refusal, or
// the zero time for any other error. Wrapped errors are unwrapped, so a caller
// adding context to a failed read cannot lose the reset time.
func ResetTimeOf(err error) time.Time {
	var timed ResetTimer
	if errors.As(err, &timed) {
		return timed.ResetTime()
	}
	return time.Time{}
}

// RetryDelay returns how long to wait before the next read. An ordinary
// failure keeps the caller's ordinary interval; a rate-limit refusal waits
// until its recorded reset. A reset already past keeps the ordinary cadence,
// so a refreshed budget does not leave the window stuck on a longer pause.
// Now is supplied by the caller's scheduler, never by rendering.
func RetryDelay(err error, now time.Time, ordinary time.Duration) time.Duration {
	if reset := ResetTimeOf(err); reset.After(now) {
		return reset.Sub(now)
	}
	return ordinary
}

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

// Watcher polls a Fetcher on Interval and streams each changed snapshot. A
// rate-limit refusal pauses the next poll until its recorded reset.
type Watcher struct {
	Interval time.Duration
	Fetch    Fetcher
}

// Run fetches once immediately, then once per Interval, until ctx is done.
// Every successful snapshot that differs from the previous one is sent on
// snaps; every failure is sent on errs and polling continues afterward, so a
// transient GitHub error never freezes the window. A rate-limit refusal waits
// for the reset time carried by its error instead of polling on Interval.
// Both channels are closed when Run returns. Run keeps no scene or frame state:
// callers own the transition between the snapshots they receive.
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
	fetch := func() error {
		snap, err := w.Fetch(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return err
			}
			select {
			case errs <- err:
			case <-ctx.Done():
			}
			return err
		}
		if have && Equal(last, snap) {
			return nil
		}
		select {
		case snaps <- snap:
			last, have = snap, true
		case <-ctx.Done():
		}
		return nil
	}

	err := fetch()
	for ctx.Err() == nil {
		// Schedule from the last outcome: a rate-limit refusal waits for its
		// recorded reset, every other read keeps the ordinary interval.
		timer := time.NewTimer(RetryDelay(err, time.Now(), interval))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
			err = fetch()
		}
	}
	return ctx.Err()
}

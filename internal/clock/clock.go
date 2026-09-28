// Package clock — real and fake clocks (F1).
// FaultLab lifecycle waits (TTL expiry, abort windows, recovery deadlines)
// must be testable without sleeping: production code takes a Clock and
// tests inject a FakeClock with manual advancement.
package clock

import (
	"sync"
	"time"
)

// Clock abstracts time.
type Clock interface {
	Now() time.Time
	Sleep(d time.Duration)
	After(d time.Duration) <-chan time.Time
}

// RealClock uses the system clock.
type RealClock struct{}

func (RealClock) Now() time.Time                         { return time.Now() }
func (RealClock) Sleep(d time.Duration)                  { time.Sleep(d) }
func (RealClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// waiter is one blocked Sleep/After call on a FakeClock.
type waiter struct {
	at time.Time
	ch chan time.Time
}

// FakeClock advances only via Advance. Sleep/After block until the fake
// time reaches their deadline; zero/negative durations return immediately.
type FakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []*waiter
}

// NewFake creates a fake clock at t.
func NewFake(t time.Time) *FakeClock { return &FakeClock{now: t} }

func (f *FakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Advance moves time forward by d, waking expired waiters in deadline order.
func (f *FakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	var ready []*waiter
	var pending []*waiter
	for _, w := range f.waiters {
		if !w.at.After(f.now) {
			ready = append(ready, w)
		} else {
			pending = append(pending, w)
		}
	}
	f.waiters = pending
	f.mu.Unlock()
	for _, w := range ready {
		w.ch <- f.now
	}
}

func (f *FakeClock) waitUntil(deadline time.Time) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan time.Time, 1)
	if !deadline.After(f.now) {
		ch <- f.now
		return ch
	}
	f.waiters = append(f.waiters, &waiter{at: deadline, ch: ch})
	return ch
}

func (f *FakeClock) Sleep(d time.Duration) { <-f.waitUntil(f.Now().Add(d)) }

func (f *FakeClock) After(d time.Duration) <-chan time.Time {
	return f.waitUntil(f.Now().Add(d))
}

// Waiting reports the number of blocked sleepers (test introspection).
func (f *FakeClock) Waiting() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.waiters)
}

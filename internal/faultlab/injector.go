// injector.go — fault actuation boundary (F1: interface + fake; F2 adds
// the gateway implementation). The runner persists intent via the journal
// BEFORE calling Apply, and records cleanup targets the same way.
package faultlab

import (
	"context"
	"fmt"
	"sync"
)

// Injection describes one bounded fault application.
type Injection struct {
	ID       string
	Kind     string // gateway_delay | conn_fail | dependency_failure
	Slot     string
	DelayMs  int64
	Fraction float64
	TTL      int64 // seconds; gateway enforces expiry independently
}

// Injector applies and removes faults.
type Injector interface {
	// Apply persists nothing itself; the caller records intent first.
	Apply(ctx context.Context, in Injection) error
	// Clear removes one fault; idempotent (unknown IDs succeed).
	Clear(ctx context.Context, id string) error
	// ClearAll removes every known fault (emergency path); idempotent.
	ClearAll(ctx context.Context) error
	// Active lists currently applied fault IDs (for verified cleanup).
	Active(ctx context.Context) ([]string, error)
}

// FakeInjector records calls for unit tests; scripted errors fail
// specific operations. No network, no cluster.
type FakeInjector struct {
	mu      sync.Mutex
	Applied []Injection
	Cleared []string
	Clears  int // ClearAll call count
	Live    map[string]Injection
	FailOn  map[string]error // "apply:<id>" | "clear:<id>" | "clearall"
}

// NewFakeInjector builds an empty fake.
func NewFakeInjector() *FakeInjector {
	return &FakeInjector{Live: map[string]Injection{}, FailOn: map[string]error{}}
}

func (f *FakeInjector) Apply(_ context.Context, in Injection) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.FailOn["apply:"+in.ID]; ok {
		return err
	}
	if _, dup := f.Live[in.ID]; dup {
		return fmt.Errorf("fault %s already applied", in.ID)
	}
	f.Applied = append(f.Applied, in)
	f.Live[in.ID] = in
	return nil
}

func (f *FakeInjector) Clear(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.FailOn["clear:"+id]; ok {
		return err
	}
	f.Cleared = append(f.Cleared, id)
	delete(f.Live, id) // idempotent: unknown IDs succeed silently
	return nil
}

func (f *FakeInjector) ClearAll(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.FailOn["clearall"]; ok {
		return err
	}
	f.Clears++
	f.Live = map[string]Injection{}
	return nil
}

// Active lists live fault IDs.
func (f *FakeInjector) Active(_ context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for id := range f.Live {
		out = append(out, id)
	}
	return out, nil
}

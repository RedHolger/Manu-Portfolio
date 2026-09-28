package clock

import (
	"testing"
	"time"
)

func TestFakeAdvanceWakes(t *testing.T) {
	f := NewFake(time.Unix(1000, 0))
	done := make(chan time.Time, 1)
	go func() {
		f.Sleep(60 * time.Second)
		done <- f.Now()
	}()
	time.Sleep(20 * time.Millisecond)
	if f.Waiting() != 1 {
		t.Fatalf("waiting=%d, want 1", f.Waiting())
	}
	f.Advance(30 * time.Second)
	select {
	case <-done:
		t.Fatal("woke after only 30s of a 60s sleep")
	default:
	}
	f.Advance(30 * time.Second)
	select {
	case at := <-done:
		if !at.Equal(time.Unix(1060, 0)) {
			t.Fatalf("woke at %v, want 1060", at)
		}
	case <-time.After(time.Second):
		t.Fatal("still asleep after full advance")
	}
	if f.Waiting() != 0 {
		t.Fatal("waiter leaked")
	}
}

func TestFakePastDeadlineReturns(t *testing.T) {
	f := NewFake(time.Unix(1000, 0))
	select {
	case <-f.After(-time.Second):
	default:
		t.Fatal("past deadline must return immediately")
	}
	select {
	case <-f.After(0):
	default:
		t.Fatal("zero deadline must return immediately")
	}
}

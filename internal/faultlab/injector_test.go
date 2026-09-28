package faultlab

import (
	"context"
	"errors"
	"testing"
)

func TestFakeInjectorLifecycle(t *testing.T) {
	f := NewFakeInjector()
	ctx := context.Background()
	in := Injection{ID: "f1", Kind: FaultDelay, Slot: "stable", DelayMs: 500, Fraction: 0.5, TTL: 75}
	if err := f.Apply(ctx, in); err != nil {
		t.Fatal(err)
	}
	if err := f.Apply(ctx, in); err == nil {
		t.Fatal("double apply accepted")
	}
	live, _ := f.Active(ctx)
	if len(live) != 1 {
		t.Fatalf("live=%v", live)
	}
	if err := f.Clear(ctx, "nope"); err != nil {
		t.Fatalf("unknown clear must be idempotent: %v", err)
	}
	if err := f.Clear(ctx, "f1"); err != nil {
		t.Fatal(err)
	}
	if live, _ := f.Active(ctx); len(live) != 0 {
		t.Fatalf("live=%v after clear", live)
	}
	if err := f.ClearAll(ctx); err != nil {
		t.Fatal(err)
	}
	if f.Clears != 1 {
		t.Fatalf("clears=%d", f.Clears)
	}
}

func TestFakeInjectorScriptedErrors(t *testing.T) {
	f := NewFakeInjector()
	boom := errors.New("boom")
	f.FailOn["apply:f1"] = boom
	if err := f.Apply(context.Background(), Injection{ID: "f1"}); !errors.Is(err, boom) {
		t.Fatalf("err=%v", err)
	}
	f.FailOn["clearall"] = boom
	if err := f.ClearAll(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("err=%v", err)
	}
}

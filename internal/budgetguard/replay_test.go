// replay_test.go — B3: fixture expectations + byte-identical determinism.
package budgetguard

import (
	"os"
	"testing"
	"time"
)

func TestFixtureDecisions(t *testing.T) {
	cfg := mustConfig(t)
	end, _ := time.Parse(time.RFC3339, "2026-09-27T12:05:00Z")
	cases := map[string]string{
		"healthy.json": "PASS",
		"error.json":   "FAIL",
		"slow.json":    "FAIL",
		"thin.json":    "INCONCLUSIVE",
	}
	for fx, want := range cases {
		raw, err := os.ReadFile("../../tests/fixtures/releases/" + fx)
		if err != nil {
			t.Fatal(err)
		}
		res, err := ReplayFixture(cfg, end, raw)
		if err != nil {
			t.Fatal(err)
		}
		if res.Decision != want {
			t.Fatalf("%s: got %s (%v), want %s", fx, res.Decision, res.Reasons, want)
		}
		if res.HistoryComplete {
			t.Fatalf("%s: lab must report history_complete=false", fx)
		}
	}
}

func TestReplayDeterministic(t *testing.T) {
	cfg := mustConfig(t)
	end, _ := time.Parse(time.RFC3339, "2026-09-27T12:05:00Z")
	raw, _ := os.ReadFile("../../tests/fixtures/releases/error.json")
	a, err := ReplayFixture(cfg, end, raw)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ReplayFixture(cfg, end, raw)
	if err != nil {
		t.Fatal(err)
	}
	if ConfigHash(cfg) != a.PolicyHash || a.PolicyHash != b.PolicyHash {
		t.Fatal("policy hash unstable")
	}
	if a.Decision != b.Decision || len(a.Reasons) != len(b.Reasons) {
		t.Fatal("replay not deterministic")
	}
}

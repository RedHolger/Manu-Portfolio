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

// C regression: malformed fixtures must be REJECTED, never decided.
func TestReplayRejectsMalformedFixtures(t *testing.T) {
	cfg := mustConfig(t)
	end := time.Now().UTC()
	cases := map[string]string{
		"negative bad":         `{"stable":{"eligible":1000,"bad":-5,"slow_or_bad":10},"candidate":{"eligible":1000,"bad":0,"slow_or_bad":0}}`,
		"negative eligible":    `{"stable":{"eligible":-1,"bad":0,"slow_or_bad":0},"candidate":{"eligible":1000,"bad":0,"slow_or_bad":0}}`,
		"missing bad":          `{"stable":{"eligible":1000,"slow_or_bad":10},"candidate":{"eligible":1000,"bad":0,"slow_or_bad":0}}`,
		"missing candidate":    `{"stable":{"eligible":1000,"bad":0,"slow_or_bad":10}}`,
		"missing stable":       `{"candidate":{"eligible":1000,"bad":0,"slow_or_bad":0}}`,
		"bad over slow_or_bad": `{"stable":{"eligible":1000,"bad":50,"slow_or_bad":10},"candidate":{"eligible":1000,"bad":0,"slow_or_bad":0}}`,
		"slow over eligible":   `{"stable":{"eligible":100,"bad":1,"slow_or_bad":200},"candidate":{"eligible":1000,"bad":0,"slow_or_bad":0}}`,
		"non-numeric":          `{"stable":{"eligible":"1000","bad":0,"slow_or_bad":10},"candidate":{"eligible":1000,"bad":0,"slow_or_bad":0}}`,
		"overflow":             `{"stable":{"eligible":1e400,"bad":0,"slow_or_bad":10},"candidate":{"eligible":1000,"bad":0,"slow_or_bad":0}}`,
		"not an object":        `{"stable":[]}`,
		"truncated json":       `{"stable":{"eligible":1000,`,
	}
	for name, raw := range cases {
		res, err := ReplayFixture(cfg, end, []byte(raw))
		if err == nil {
			t.Fatalf("%s: expected rejection, got decision %s (%v)", name, res.Decision, res.Reasons)
		}
		t.Logf("%s → rejected: %v", name, err)
	}
}

// C regression: fractional counts are preserved for gating. Rounding
// 60.0001 to 60 first would make 60/6000 == 0.01 pass the 1% error gate;
// the raw fraction must FAIL.
func TestReplayPreservesFractionalCounts(t *testing.T) {
	cfg := mustConfig(t)
	end, _ := time.Parse(time.RFC3339, "2026-10-05T12:00:00Z")
	raw := []byte(`{"stable":{"eligible":24000,"bad":12,"slow_or_bad":200},` +
		`"candidate":{"eligible":6000,"bad":60.0001,"slow_or_bad":60.0001}}`)
	res, err := ReplayFixture(cfg, end, raw)
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision != "FAIL" {
		t.Fatalf("decision=%s (%v), want FAIL on the raw fraction", res.Decision, res.Reasons)
	}
	if !res.Estimated {
		t.Fatal("fractional fixture counts must be flagged estimated_counts")
	}
	if res.Candidate.Bad != 60 {
		t.Fatalf("display bad=%d, want rounded 60 (report edge only)", res.Candidate.Bad)
	}
	// Deterministic replay of the same fixture.
	again, err := ReplayFixture(cfg, end, raw)
	if err != nil {
		t.Fatal(err)
	}
	if again.Decision != res.Decision || again.Estimated != res.Estimated {
		t.Fatal("fractional replay not deterministic")
	}
}

// C regression: integer fixtures stay byte-identical and unflagged.
func TestReplayIntegerFixturesUnestimated(t *testing.T) {
	cfg := mustConfig(t)
	end, _ := time.Parse(time.RFC3339, "2026-09-27T12:05:00Z")
	raw, err := os.ReadFile("../../tests/fixtures/releases/healthy.json")
	if err != nil {
		t.Fatal(err)
	}
	res, err := ReplayFixture(cfg, end, raw)
	if err != nil {
		t.Fatal(err)
	}
	if res.Estimated {
		t.Fatal("integer fixture must not be flagged estimated")
	}
}

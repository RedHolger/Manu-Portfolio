// D1 regression: writeResult must propagate file errors instead of
// reporting success while saving nothing.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"sre-portfolio/internal/budgetguard"
)

func TestWriteResultPropagatesError(t *testing.T) {
	res := budgetguard.Result{SchemaVersion: 1, Decision: "PASS"}
	if err := writeResult("/nonexistent-dir-xyz/replay.json", res); err == nil {
		t.Fatal("expected error for unwritable path")
	}
}

func TestWriteResultRoundTrip(t *testing.T) {
	res := budgetguard.Result{SchemaVersion: 1, Decision: "FAIL",
		Reasons: []string{"candidate_error_rate_exceeded"}}
	out := filepath.Join(t.TempDir(), "replay.json")
	if err := writeResult(out, res); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var back budgetguard.Result
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Decision != "FAIL" {
		t.Fatalf("round trip: %v", back.Decision)
	}
}

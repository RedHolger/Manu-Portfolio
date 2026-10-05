// D1 regression: writeResult must propagate file errors instead of
// reporting success while saving nothing.
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

// B regression: a telemetry failure must still record the decision window
// that could not be observed — an INCONCLUSIVE without timestamps loses the
// evidence of WHICH window went dark.
func TestEvaluateTelemetryErrorKeepsWindow(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"status":"error","errorType":"internal","error":"boom"}`))
	}))
	defer s.Close()
	out := filepath.Join(t.TempDir(), "decision.json")
	code, err := evaluate([]string{
		"--config", "../../configs/slos/reservations.yaml",
		"--prometheus", s.URL,
		"--end", "2026-10-05T00:05:00Z",
		"--out", out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if code != 3 {
		t.Fatalf("exit=%d, want 3 (INCONCLUSIVE)", code)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var res budgetguard.Result
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	if res.Decision != "INCONCLUSIVE" {
		t.Fatalf("decision=%q", res.Decision)
	}
	if res.Window.End != "2026-10-05T00:05:00Z" || res.Window.Start != "2026-10-05T00:00:00Z" {
		t.Fatalf("window=%+v, want 00:00:00Z..00:05:00Z", res.Window)
	}
	if len(res.Reasons) == 0 || !strings.HasPrefix(res.Reasons[0], "telemetry_error:") {
		t.Fatalf("reasons=%v", res.Reasons)
	}
}

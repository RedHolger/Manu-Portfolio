package faultlab

import (
	"strings"
	"testing"
)

func TestRenderPreservesAllOutcomes(t *testing.T) {
	out := Render(SuiteReport{
		Title:  "demo",
		Window: "full-window",
		Runs: []RunEvidence{
			{RunID: "h-1", Class: "healthy", Decision: "PASS", Detail: "1200/0/0"},
			{RunID: "e-222", Class: "error", Decision: "CONTAMINATED",
				Reasons: []string{"disk-stall crawl"}, Detail: "excluded from benchmarks"},
			{RunID: "t-1", Class: "error", Decision: "INCONCLUSIVE",
				Reasons: []string{"insufficient_traffic"}, Detail: "980<1000"},
		},
		Oracles: []OracleSection{
			{RunID: "h-1", Clean: true, Info: []string{"1 ambiguous reconciled"}},
			{RunID: "e-9", Clean: false, Violations: []string{"duplicate key \"k1\": 2 rows"}},
		},
	})
	for _, want := range []string{
		"# demo", "window: full-window",
		"| e-222 | CONTAMINATED | disk-stall crawl | excluded from benchmarks |",
		"| t-1 | INCONCLUSIVE | insufficient_traffic | 980<1000 |",
		"- e-9: **VIOLATED**", "duplicate key",
		"- h-1: **CLEAN**", "1 ambiguous reconciled",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("report missing %q\n%s", want, out)
		}
	}
}

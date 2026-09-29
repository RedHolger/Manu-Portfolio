// report.go — F4 Markdown reports preserving every outcome class.
// Failed, invalid, and unresolved runs are listed with reasons — never
// dropped to make a suite look green.
package faultlab

import (
	"fmt"
	"strings"
	"time"
)

// RunEvidence is one experiment's preserved outcome.
type RunEvidence struct {
	RunID    string
	Class    string // healthy | error | slow | clienterror | fault
	Decision string // PASS | FAIL | INCONCLUSIVE | CONTAMINATED | INVALID
	Reasons  []string
	Detail   string // eligible/bad/slow counts or note
}

// OracleSection carries correctness-oracle findings for a run.
type OracleSection struct {
	RunID      string
	Clean      bool
	Violations []string
	Info       []string
}

// SuiteReport aggregates one results directory.
type SuiteReport struct {
	Title     string
	Window    string // e.g. "full-window" | "preliminary short-window"
	Generated string
	Runs      []RunEvidence
	Oracles   []OracleSection
}

// Render produces the Markdown report.
func Render(r SuiteReport) string {
	if r.Generated == "" {
		r.Generated = time.Now().UTC().Format(time.RFC3339)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", r.Title)
	fmt.Fprintf(&b, "window: %s · generated: %s · runs: %d\n\n", r.Window, r.Generated, len(r.Runs))
	byClass := map[string][]RunEvidence{}
	var classes []string
	for _, e := range r.Runs {
		if _, ok := byClass[e.Class]; !ok {
			classes = append(classes, e.Class)
		}
		byClass[e.Class] = append(byClass[e.Class], e)
	}
	for _, c := range classes {
		fmt.Fprintf(&b, "## %s\n\n", c)
		fmt.Fprintln(&b, "| run | decision | reasons | detail |")
		fmt.Fprintln(&b, "|---|---|---|---|")
		for _, e := range byClass[c] {
			fmt.Fprintf(&b, "| %s | %s | %s | %s |\n",
				e.RunID, e.Decision, strings.Join(e.Reasons, "; "), e.Detail)
		}
		b.WriteString("\n")
	}
	if len(r.Oracles) > 0 {
		b.WriteString("## Correctness oracle\n\n")
		for _, o := range r.Oracles {
			status := "CLEAN"
			if !o.Clean {
				status = "VIOLATED"
			}
			fmt.Fprintf(&b, "- %s: **%s**", o.RunID, status)
			for _, v := range o.Violations {
				fmt.Fprintf(&b, "\n  - violation: %s", v)
			}
			for _, n := range o.Info {
				fmt.Fprintf(&b, "\n  - note: %s", n)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// cmd/faultlab — F1: validate + plan. Live commands (run, status,
// cleanup, reconcile, report) arrive with the F2 runner; the dispatcher
// only registers what exists — no stub subcommands.
package main

import (
	"crypto/sha256"
	"fmt"
	"os"

	"sre-portfolio/internal/faultlab"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	var err error
	switch os.Args[1] {
	case "validate":
		err = validate(os.Args[2:])
	case "plan":
		err = plan(os.Args[2:])
	default:
		usage()
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `faultlab validate --scenario FILE
faultlab plan --scenario FILE`)
}

func loadScenario(args []string) (faultlab.Scenario, string, string) {
	var path string
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--scenario" {
			path = args[i+1]
		}
	}
	if path == "" {
		fmt.Fprintln(os.Stderr, "--scenario required")
		os.Exit(1)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cfg, err := faultlab.ParseScenario(string(raw))
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid scenario:", err)
		os.Exit(1)
	}
	sum := fmt.Sprintf("%x", sha256.Sum256(raw))
	return cfg, path, sum
}

func validate(args []string) error {
	_, path, sum := loadScenario(args)
	fmt.Printf("valid: %s (sha256 %.12s)\n", path, sum)
	return nil
}

func plan(args []string) error {
	c, _, sum := loadScenario(args)
	fmt.Printf(`faultlab plan %s (config sha256 %.12s)
target:     service=%s slot=%s (context %s, namespace %s)
workload:   %.0f rps, seed %d, %ds timeout, correctness-checked writes
phases:     baseline %ds → fault %ds → recovery %ds
fault:      %s delay=%dms fraction=%.2f ttl=%ds
abort:      >%.0f%% failures over %dx%ds windows → cleanup + mark
cleanup:     DELETE /admin/faults/%s-* (idempotent) + verify zero active;
            TTL expiry is gateway-enforced and independent of this runner
permissions: admin token on lab gateway only; no pod/node/cluster actions (F1/F2)
`,
		c.Name, sum,
		c.Service, c.Slot, c.Context, c.Namespace,
		c.Rate, c.Seed, c.Timeout,
		c.Baseline, c.FaultSecs, c.Recover,
		c.FaultKind, c.DelayMs, c.Fraction, c.TTL,
		c.AbortMax*100, c.AbortN, c.AbortWin, c.Name)
	return nil
}

// Regression tests for the FlagSet subcommand bug: passing os.Args[1:]
// (starting with "run") into a FlagSet silently stops parsing at the first
// non-flag arg, forcing ALL defaults (once turned --rate 50 --duration 150s
// into 100rps/300s = 30000 ops). main must strip the subcommand first.
package main

import (
	"testing"
	"time"
)

func TestSplitSubcommand(t *testing.T) {
	cmd, rest := splitSubcommand([]string{"run", "--rate", "50", "--duration", "150s"})
	if cmd != "run" || len(rest) != 4 || rest[0] != "--rate" {
		t.Fatalf("got %q %v", cmd, rest)
	}
	cmd, rest = splitSubcommand([]string{"smoke", "--seed", "1"})
	if cmd != "smoke" || len(rest) != 2 {
		t.Fatalf("got %q %v", cmd, rest)
	}
	cmd, rest = splitSubcommand([]string{"--rate", "5"})
	if cmd != "run" || len(rest) != 2 {
		t.Fatalf("bare flags default to run: %q %v", cmd, rest)
	}
}

func TestParseRunOptionsHonorsFlags(t *testing.T) {
	o, err := parseRunOptions([]string{"--rate", "50", "--duration", "150s", "--seed", "42"})
	if err != nil {
		t.Fatal(err)
	}
	if o.rate != 50 || o.dur != 150*time.Second || o.seed != 42 {
		t.Fatalf("flags ignored: %+v", o)
	}
	// The old buggy call path passed the subcommand into the FlagSet.
	// Parsing must REJECT (not silently default) a leading non-flag token.
	if _, err := parseRunOptions([]string{"run", "--rate", "50"}); err == nil {
		t.Fatal("leading subcommand must error, not silently default")
	}
}

func TestParseRunOptionsRejectsNonPositive(t *testing.T) {
	if _, err := parseRunOptions([]string{"--rate", "0"}); err == nil {
		t.Fatal("rate 0 must be rejected")
	}
	if _, err := parseRunOptions([]string{"--duration", "0s"}); err == nil {
		t.Fatal("duration 0 must be rejected")
	}
}

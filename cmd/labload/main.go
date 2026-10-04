// cmd/labload — open-loop load generator CLI (§4.3).
// Usage: labload run --rate 100 --duration 300s --seed 42 --output results/run.jsonl
//
//	labload smoke --seed 1   (single reservation round-trip check)
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"sre-portfolio/internal/loadgen"
)

func main() {
	// NOTE: strip the subcommand before FlagSet parsing — Go's flag
	// package stops at the first non-flag arg, so passing os.Args[1:]
	// (which starts with "run"/"smoke") silently forces ALL defaults.
	// That bug once turned --rate 50 --duration 150s into 100rps/300s.
	cmd, rest := splitSubcommand(os.Args[1:])
	switch cmd {
	case "smoke":
		smoke(rest)
	default:
		run(rest)
	}
}

// splitSubcommand separates "run"/"smoke" from flags. Bare flags default
// to the run subcommand.
func splitSubcommand(argv []string) (string, []string) {
	if len(argv) > 0 && (argv[0] == "run" || argv[0] == "smoke") {
		return argv[0], argv[1:]
	}
	return "run", argv
}

type runOptions struct {
	base      string
	rate      float64
	dur       time.Duration
	seed      int64
	out       string
	keyPrefix string
	sumOut    string
	timeout   time.Duration
	correct   bool
	invalid   float64
}

func parseRunOptions(args []string) (runOptions, error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	var o runOptions
	fs.StringVar(&o.base, "gateway", "http://127.0.0.1:8080", "gateway base URL")
	fs.Float64Var(&o.rate, "rate", 100, "ops/sec")
	fs.DurationVar(&o.dur, "duration", 300*time.Second, "run duration")
	fs.Int64Var(&o.seed, "seed", 42, "workload seed")
	fs.StringVar(&o.keyPrefix, "key-prefix", "", "unique run/arm namespace for idempotency keys")
	fs.StringVar(&o.out, "output", "results/run.jsonl", "JSONL attempts path")
	fs.StringVar(&o.sumOut, "summary", "", "summary JSON path")
	fs.DurationVar(&o.timeout, "timeout", 2*time.Second, "per-request timeout")
	fs.BoolVar(&o.correct, "correctness-profile", false, "retry 503/timeout/transport once with same key")
	fs.Float64Var(&o.invalid, "invalid-fraction", 0, "fraction of ops with unknown SKU (400s, SLI-excluded)")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	// A leading subcommand ("run") would otherwise stop parsing and
	// silently force defaults — reject leftover positionals loudly.
	if fs.NArg() > 0 {
		return o, fmt.Errorf("unexpected positional args %v (did main strip the subcommand?)", fs.Args())
	}
	if o.rate <= 0 || o.dur <= 0 {
		return o, fmt.Errorf("rate and duration must be positive")
	}
	return o, nil
}

func run(args []string) {
	o, err := parseRunOptions(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "run:", err)
		os.Exit(2)
	}

	f, err := os.Create(o.out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "output:", err)
		os.Exit(1)
	}
	defer f.Close()
	rn := &loadgen.Runner{BaseURL: o.base, Out: f}
	sum, err := rn.Run(context.Background(), loadgen.Config{
		Rate: o.rate, Duration: o.dur, Seed: o.seed, KeyPrefix: o.keyPrefix,
		Timeout: o.timeout, CorrectnessProfile: o.correct,
		InvalidFraction: o.invalid,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "run:", err)
		os.Exit(1)
	}
	sp := o.sumOut
	if sp == "" {
		sp = o.out + ".summary.json"
	}
	sf, err := os.Create(sp)
	if err != nil {
		fmt.Fprintln(os.Stderr, "summary:", err)
		os.Exit(1)
	}
	defer sf.Close()
	_ = json.NewEncoder(sf).Encode(sum)
	fmt.Printf("offered=%d launched=%d completed=%d dropped=%d transport=%d p99=%.1fms valid=%v %s\n",
		sum.Offered, sum.Launched, sum.Completed, sum.Dropped,
		sum.TransportErrors, sum.LatP99Ms, sum.Valid, sum.InvalidReason)
	if !sum.Valid {
		os.Exit(2) // saturation: performance comparison INVALID (§4.3)
	}
}

func smoke(args []string) {
	os.Exit(runSmoke(args))
}

// runSmoke returns the smoke exit code (H7: completed attempts are not
// enough — at least one must SUCCEED, or an all-failing service smokes OK).
func runSmoke(args []string) int {
	fs := flag.NewFlagSet("smoke", flag.ContinueOnError)
	base := fs.String("gateway", "http://127.0.0.1:8080", "gateway base URL")
	seed := fs.Int64("seed", 1, "seed")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "smoke: unexpected args", fs.Args())
		return 2
	}
	rn := &loadgen.Runner{BaseURL: *base, Out: os.Stdout}
	sum, err := rn.Run(context.Background(), loadgen.Config{
		Rate: 2, Duration: 3 * time.Second, Seed: *seed, Timeout: 2 * time.Second,
	})
	if err != nil || sum.Successful == 0 {
		fmt.Fprintln(os.Stderr, "smoke FAILED")
		return 1
	}
	fmt.Printf("smoke OK successful=%d p50=%.1fms\n", sum.Successful, sum.LatP50Ms)
	return 0
}

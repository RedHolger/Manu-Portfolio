// cmd/budgetguard — validate|compile|status|evaluate|replay (§5.2).
// Exit codes: 0 PASS, 2 FAIL, 3 INCONCLUSIVE, 1 invalid/internal.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"sre-portfolio/internal/budgetguard"
	"sre-portfolio/internal/telemetry"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	var code int
	var err error
	switch os.Args[1] {
	case "validate":
		err = validate(os.Args[2:])
	case "compile":
		err = compile(os.Args[2:])
	case "status":
		code, err = status(os.Args[2:])
	case "evaluate":
		code, err = evaluate(os.Args[2:])
	case "replay":
		code, err = replay(os.Args[2:])
	default:
		usage()
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	os.Exit(code)
}

func usage() {
	fmt.Fprintln(os.Stderr, `budgetguard validate --config F
budgetguard compile --config F --out monitoring/generated-rules.yaml [--demo-out FILE]
budgetguard status --config F --prometheus URL
budgetguard evaluate --config F --start RFC3339 --end RFC3339 --out results/release.json
budgetguard replay --fixture FILE --config F --end RFC3339 --out results/replay.json`)
}

func loadConfig(args []string) (budgetguard.ServiceSLO, string) {
	var path string
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--config" {
			path = args[i+1]
		}
	}
	if path == "" {
		fmt.Fprintln(os.Stderr, "--config required")
		os.Exit(1)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cfg, err := budgetguard.ParseConfig(string(raw))
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid config:", err)
		os.Exit(1)
	}
	return cfg, path
}

func validate(args []string) error {
	_, path := loadConfig(args)
	fmt.Println("valid:", path)
	return nil
}

func compile(args []string) error {
	cfg, _ := loadConfig(args)
	var out, demoOut string
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "--out":
			out = args[i+1]
		case "--demo-out":
			demoOut = args[i+1]
		}
	}
	if out == "" {
		return fmt.Errorf("--out required")
	}
	if err := os.WriteFile(out, []byte(budgetguard.CompileRules(cfg)), 0o644); err != nil {
		return err
	}
	if demoOut != "" {
		if err := os.WriteFile(demoOut, []byte(budgetguard.CompileDemoRules(cfg)), 0o644); err != nil {
			return err
		}
	}
	fmt.Println("wrote", out)
	return nil
}

func status(args []string) (int, error) {
	cfg, _ := loadConfig(args)
	var prom string
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--prometheus" {
			prom = args[i+1]
		}
	}
	if prom == "" {
		return 1, fmt.Errorf("--prometheus required")
	}
	end := time.Now().UTC()
	cl := telemetry.New(prom)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f, err := budgetguard.FetchCounts(ctx, cl, cfg, end)
	if err != nil {
		fmt.Println("INCONCLUSIVE:", err)
		return 3, nil
	}
	res := budgetguard.DecideCounts(cfg, end, f.Stable, f.Candidate,
		f.DisplayStable, f.DisplayCandidate, f.Estimated)
	raw, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(raw))
	if len(f.Notes) > 0 {
		fmt.Println("notes:", f.Notes)
	}
	return exitFor(res.Decision), nil
}

func evaluate(args []string) (int, error) {
	cfg, _ := loadConfig(args)
	var startS, endS, out string
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "--start":
			startS = args[i+1]
		case "--end":
			endS = args[i+1]
		case "--out":
			out = args[i+1]
		}
	}
	end, err := time.Parse(time.RFC3339, endS)
	if err != nil {
		return 1, fmt.Errorf("bad --end: %w", err)
	}
	_ = startS // window derived from end − observationSeconds (explicit end time)
	var prom string
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--prometheus" {
			prom = args[i+1]
		}
	}
	if prom == "" {
		return 1, fmt.Errorf("--prometheus required")
	}
	cl := telemetry.New(prom)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	f, err := budgetguard.FetchCounts(ctx, cl, cfg, end)
	if err != nil {
		res := budgetguard.Result{SchemaVersion: 1, Decision: "INCONCLUSIVE",
			Reasons:         []string{"telemetry_error:" + err.Error()},
			HistoryComplete: false, PolicyHash: budgetguard.ConfigHash(cfg)}
		if werr := writeResult(out, res); werr != nil {
			return 1, werr
		}
		return 3, nil
	}
	res := budgetguard.DecideCounts(cfg, end, f.Stable, f.Candidate,
		f.DisplayStable, f.DisplayCandidate, f.Estimated)
	if werr := writeResult(out, res); werr != nil {
		return 1, werr
	}
	return exitFor(res.Decision), nil
}

func replay(args []string) (int, error) {
	var fixture, out, endS string
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "--fixture":
			fixture = args[i+1]
		case "--out":
			out = args[i+1]
		case "--end":
			endS = args[i+1]
		}
	}
	if fixture == "" {
		return 1, fmt.Errorf("--fixture required")
	}
	cfg, _ := loadConfig(args)
	end := time.Now().UTC()
	if endS != "" {
		var err error
		end, err = time.Parse(time.RFC3339, endS)
		if err != nil {
			return 1, err
		}
	}
	raw, err := os.ReadFile(fixture)
	if err != nil {
		return 1, err
	}
	res, err := budgetguard.ReplayFixture(cfg, end, raw)
	if err != nil {
		return 1, err
	}
	if werr := writeResult(out, res); werr != nil {
		return 1, werr
	}
	return exitFor(res.Decision), nil
}

func writeResult(out string, res budgetguard.Result) error {
	raw, _ := json.MarshalIndent(res, "", "  ")
	if out == "" {
		fmt.Println(string(raw))
		return nil
	}
	// D1: file errors propagate — a decision printed to stdout while the
	// file silently fails would corrupt the evidence chain.
	if err := os.WriteFile(out, append(raw, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", out, err)
	}
	fmt.Println("decision:", res.Decision, "→", out)
	return nil
}

func exitFor(d string) int {
	switch d {
	case "PASS":
		return 0
	case "FAIL":
		return 2
	default:
		return 3
	}
}

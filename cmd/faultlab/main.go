// cmd/faultlab — F1: validate + plan. F2: run, status, cleanup,
// reconcile, report against the lab gateway (admin) + journal.
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"sre-portfolio/internal/clock"
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
	case "run":
		err = run(os.Args[2:])
	case "status":
		err = status(os.Args[2:])
	case "cleanup":
		err = cleanup(os.Args[2:])
	case "reconcile":
		err = reconcile(os.Args[2:])
	case "report":
		err = report(os.Args[2:])
	case "pod-delete":
		err = podDelete(os.Args[2:])
	case "dep-fault":
		err = depFault(os.Args[2:])
	case "oracle-check":
		err = oracleCheck(os.Args[2:])
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
faultlab plan --scenario FILE
faultlab run --scenario FILE --out DIR [--db FILE] [--gateway URL] [--metrics URL]
  [--kubeconfig PATH] [--api-admin URL] [--pg-dsn DSN] [--sku SKU]
faultlab status --run-id ID [--db FILE]
faultlab cleanup --run-id ID [--db FILE] [--gateway URL]
faultlab reconcile [--db FILE] [--gateway URL]
faultlab report --run-id ID [--db FILE]
faultlab pod-delete --deployment NAME [--kubeconfig PATH]
faultlab dep-fault --api-admin URL (--fail [--ttl N] | --clear)
faultlab oracle-check --ops FILE --pg-dsn DSN --sku SKU --out DIR`)
}

// podsFromKubeconfig builds the namespace-enforced deleter.
func podsFromKubeconfig(path string) (*faultlab.PodDeleter, error) {
	client, err := kubeClient(path)
	if err != nil {
		return nil, err
	}
	return faultlab.NewPodDeleter(client, "sre-lab")
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
recovery:    recoverySeconds load after cleanup; <=%.0f%% failures with >=%d successful (2xx) requests, then PASSED
permissions: admin token on lab gateway only; no pod/node/cluster actions (F1/F2)
`,
		c.Name, sum,
		c.Service, c.Slot, c.Context, c.Namespace,
		c.Rate, c.Seed, c.Timeout,
		c.Baseline, c.FaultSecs, c.Recover,
		c.FaultKind, c.DelayMs, c.Fraction, c.TTL,
		c.AbortMax*100, c.AbortN, c.AbortWin, c.Name,
		faultlab.MaxRecoveryFailureRatio*100, faultlab.MinRecoverySamples)
	switch {
	case !c.AssertsDeclared:
		fmt.Println("oracle:      no spec.assertions.* declared (ledger not required)")
	case c.AssertDuplicates || c.AssertNegativeInv:
		fmt.Println("oracle:      declared (requires --pg-dsn; assertions fail closed without a ledger)")
	default:
		fmt.Println("oracle:      assertions block present (requires --pg-dsn; checked against the ledger)")
	}
	return nil
}

// ---- F2 live commands ----

type liveFlags struct {
	db         string
	out        string
	gateway    string // admin base URL
	metrics    string // public base URL (load + /metrics)
	runID      string
	kubeconfig string // enables pod_delete faults (empty = unsupported)
	apiAdmin   string // enables dependency_failure faults (empty = unsupported)
	pgdsn      string // ledger for spec.assertions.* (required when declared)
	sku        string // oracle SKU override (default faultlab.DefaultSKU)
}

func parseLive(args []string) liveFlags {
	f := liveFlags{db: "faultlab.db", gateway: "http://127.0.0.1:8082", metrics: "http://127.0.0.1:8080"}
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "--db":
			f.db = args[i+1]
		case "--out":
			f.out = args[i+1]
		case "--gateway":
			f.gateway = args[i+1]
		case "--metrics":
			f.metrics = args[i+1]
		case "--run-id":
			f.runID = args[i+1]
		case "--kubeconfig":
			f.kubeconfig = args[i+1]
		case "--api-admin":
			f.apiAdmin = args[i+1]
		case "--pg-dsn":
			f.pgdsn = args[i+1]
		case "--sku":
			f.sku = args[i+1]
		}
	}
	return f
}

func adminToken() string {
	tok := os.Getenv("LAB_ADMIN_TOKEN")
	if tok == "" {
		fmt.Fprintln(os.Stderr, "LAB_ADMIN_TOKEN must be set (uncommitted secret)")
		os.Exit(1)
	}
	return tok
}

// defaultCheckContext shells kubectl (H4 applies to the binary too).
func defaultCheckContext(ctx context.Context) error {
	out, err := kubectlCurrentContext(ctx)
	if err != nil {
		return err
	}
	if out != "kind-sre-lab" {
		return fmt.Errorf("context %q != dedicated kind-sre-lab", out)
	}
	return nil
}

func kubectlCurrentContext(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "kubectl", "config", "current-context")
	raw, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("kubectl current-context: %w", err)
	}
	return strings.TrimSpace(string(raw)), nil
}

// openRunner builds the live runner. The returned cleanup function closes
// the ledger connection (always safe to call, never nil).
func openRunner(f liveFlags) (*faultlab.Runner, *faultlab.Journal, func(), error) {
	noDB := func() {}
	j, err := faultlab.Open(f.db)
	if err != nil {
		return nil, nil, noDB, err
	}
	inj := faultlab.NewGatewayInjector(f.gateway, adminToken(), "kind-sre-lab")
	r := &faultlab.Runner{
		Journal: j, Injector: inj, Clock: clock.RealClock{},
		Gateway: f.metrics, OutDir: f.out,
		CheckContext: defaultCheckContext,
		LoadTimeout:  10 * time.Second, CleanupCap: 60 * time.Second,
	}
	if f.pgdsn != "" {
		db, derr := sql.Open("pgx", f.pgdsn)
		if derr != nil {
			_ = j.Close()
			return nil, nil, noDB, fmt.Errorf("ledger: %w", derr)
		}
		pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		perr := db.PingContext(pingCtx)
		cancel()
		if perr != nil {
			_ = db.Close()
			_ = j.Close()
			return nil, nil, noDB, fmt.Errorf("ledger unreachable: %w", perr)
		}
		r.Ledger = faultlab.NewPGLedger(db)
		r.SKU = f.sku
		noDB = func() { db.Close() }
	}
	if f.kubeconfig != "" {
		pods, err := podsFromKubeconfig(f.kubeconfig)
		if err != nil {
			_ = j.Close()
			noDB()
			return nil, nil, noDB, err
		}
		r.Pods = pods
	}
	if f.apiAdmin != "" {
		r.Dep = faultlab.NewHTTPDepFault(f.apiAdmin, adminToken())
	}
	return r, j, noDB, nil
}

func run(args []string) error {
	cfg, _, _ := loadScenario(args)
	f := parseLive(args)
	if f.out == "" {
		return fmt.Errorf("--out required")
	}
	// Assertions need the ledger; without it the run would fail closed at
	// VERIFYING. Refuse before any journal row or mutation.
	if cfg.AssertsDeclared && f.pgdsn == "" {
		return fmt.Errorf("scenario declares spec.assertions.* but --pg-dsn was not provided (assertions fail closed)")
	}
	if err := os.MkdirAll(f.out, 0o755); err != nil {
		return err
	}
	r, j, closeDB, err := openRunner(f)
	if err != nil {
		return err
	}
	defer j.Close()
	defer closeDB()
	runID := fmt.Sprintf("%s-%d", cfg.Name, time.Now().UTC().Unix())
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	term, rerr := r.Run(ctx, cfg, runID)
	row, journalErr := j.GetRun(runID)
	if journalErr != nil {
		return fmt.Errorf("run error %v; read result: %w", rerr, journalErr)
	}
	raw, marshalErr := json.Marshal(map[string]any{"run": runID, "terminal": term, "state": row.State})
	if marshalErr != nil {
		return marshalErr
	}
	if writeErr := os.WriteFile(f.out+"/result.json", append(raw, '\n'), 0o644); writeErr != nil {
		return fmt.Errorf("run error %v; save result: %w", rerr, writeErr)
	}
	fmt.Printf("run %s -> %s\n", runID, term)
	return rerr
}

func status(args []string) error {
	f := parseLive(args)
	if f.runID == "" {
		return fmt.Errorf("--run-id required")
	}
	j, err := faultlab.Open(f.db)
	if err != nil {
		return err
	}
	defer j.Close()
	row, err := j.GetRun(f.runID)
	if err != nil {
		return err
	}
	fl, _ := j.Faults(f.runID)
	n, _ := j.EventCount(f.runID)
	raw, _ := json.MarshalIndent(map[string]any{
		"run": row, "faults": fl, "events": n,
	}, "", "  ")
	fmt.Println(string(raw))
	return nil
}

func cleanup(args []string) error {
	f := parseLive(args)
	if f.runID == "" {
		return fmt.Errorf("--run-id required")
	}
	r, j, closeDB, err := openRunner(f)
	if err != nil {
		return err
	}
	defer j.Close()
	defer closeDB()
	row, err := j.GetRun(f.runID)
	if err != nil {
		return err
	}
	if faultlab.Terminal(row.State) {
		// Already terminal: verify only (duplicate cleanup is harmless).
		live, err := r.Injector.Active(context.Background())
		fmt.Printf("terminal %s, live faults: %v (err=%v)\n", row.State, live, err)
		return err
	}
	fresh, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	done, err := r.Reconcile(fresh)
	fmt.Printf("reconciled: %v err=%v\n", done, err)
	return err
}

func reconcile(args []string) error {
	f := parseLive(args)
	r, j, closeDB, err := openRunner(f)
	if err != nil {
		return err
	}
	defer j.Close()
	defer closeDB()
	fresh, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	done, err := r.Reconcile(fresh)
	fmt.Printf("reconciled: %v err=%v\n", done, err)
	return err
}

func report(args []string) error {
	return status(args) // F2 report = journaled state dump; F4 adds analysis
}

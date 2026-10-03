// cmd/recoverops — RecoverOps CLI (R1): serve, policy validate,
// incident show, replay, register-good, mode. Every subcommand parses its
// own flags; unknown commands and bad invocations exit 1 (never conflate
// with domain outcomes).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"sre-portfolio/internal/recoverops"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

var kubeClient = func(kubeconfig string) (kubernetes.Interface, error) {
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(cfg)
}

func execCommand(name string, args ...string) (string, error) {
	var out bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(os.Args[2:])
	case "execute":
		err = execute(os.Args[2:])
	case "verify":
		err = verify(os.Args[2:])
	case "policy":
		err = policy(os.Args[2:])
	case "incident":
		err = incident(os.Args[2:])
	case "replay":
		err = replay(os.Args[2:])
	case "register-good":
		err = registerGood(os.Args[2:])
	case "mode":
		err = mode(os.Args[2:])
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
	fmt.Fprintln(os.Stderr, `recoverops serve --db FILE [--addr :8089] [--policy FILE] [--mode observe|enforce-lab]
  token via RECOVEROPS_TOKEN env (required)
recoverops execute --db FILE --incident ID [--kubeconfig PATH] [--policy FILE]
  single conditional rollback (enforce-lab only, kind-sre-lab only)
recoverops policy validate --policy FILE
recoverops incident show --db FILE --id ID [--events]
recoverops replay --db FILE --policy FILE --file BATCH.json
recoverops register-good --db FILE --target-uid UID --template FILE
recoverops mode [--db FILE] [observe|enforce-lab]`)
}

func tokenFromEnv() (string, error) {
	t := os.Getenv("RECOVEROPS_TOKEN")
	if t == "" {
		return "", fmt.Errorf("RECOVEROPS_TOKEN must be set (uncommitted secret)")
	}
	return t, nil
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", ":8089", "HTTP listen address")
	dbPath := fs.String("db", "", "SQLite path")
	polPath := fs.String("policy", "configs/policies/lab-rollback.yaml", "policy file")
	mode := fs.String("mode", "observe", "observe|enforce-lab")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected positional args %v", fs.Args())
	}
	tok, err := tokenFromEnv()
	if err != nil {
		return err
	}
	cfg, err := recoverops.LoadConfig(*addr, *dbPath, *polPath, tok, *mode)
	if err != nil {
		return err
	}
	st, err := recoverops.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.SetMeta("mode", cfg.Mode); err != nil {
		return err
	}
	srv := recoverops.NewServer(cfg, st)
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	log.Info("recoverops listening", "addr", cfg.Addr, "mode", cfg.Mode,
		"policy", cfg.Policy.Name, "policy_hash", cfg.Policy.Hash[:12])
	httpSrv := &http.Server{
		Addr: cfg.Addr, Handler: srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shut)
	}()
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func policy(args []string) error {
	if len(args) < 1 || args[0] != "validate" {
		return fmt.Errorf("usage: recoverops policy validate --policy FILE")
	}
	fs := flag.NewFlagSet("policy-validate", flag.ContinueOnError)
	path := fs.String("policy", "", "policy file")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	pol, err := recoverops.LoadPolicy(*path)
	if err != nil {
		return err
	}
	fmt.Printf("valid: %s target=%s/%s action=%s limits=%d/%ds/%d/h hash=%.12s\n",
		pol.Name, pol.Namespace, pol.Deployment, pol.Action,
		pol.PerIncident, pol.CooldownSecs, pol.PerHour, pol.Hash)
	return nil
}

func openStore(dbPath string) (*recoverops.Store, error) {
	if dbPath == "" {
		return nil, fmt.Errorf("--db required")
	}
	return recoverops.Open(dbPath)
}

func incident(args []string) error {
	if len(args) < 1 || args[0] != "show" {
		return fmt.Errorf("usage: recoverops incident show --db FILE --id ID [--events]")
	}
	fs := flag.NewFlagSet("incident-show", flag.ContinueOnError)
	dbPath := fs.String("db", "", "SQLite path")
	id := fs.String("id", "", "incident ID")
	withEvents := fs.Bool("events", false, "include journaled events")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("--id required")
	}
	st, err := openStore(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	in, err := st.GetIncident(*id)
	if err != nil {
		return fmt.Errorf("no such incident: %s", *id)
	}
	out := map[string]interface{}{"incident": in}
	if *withEvents {
		evs, err := st.Events(*id)
		if err != nil {
			return err
		}
		if evs == nil {
			evs = []recoverops.Event{}
		}
		out["events"] = evs
	}
	acts, err := st.ActionsFor(*id)
	if err != nil {
		return err
	}
	if acts == nil {
		acts = []recoverops.ActionRow{}
	}
	out["actions"] = acts
	raw, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(raw))
	return nil
}

func replay(args []string) error {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	dbPath := fs.String("db", "", "SQLite path")
	polPath := fs.String("policy", "", "policy file")
	file := fs.String("file", "", "saved delivery batch (JSON array)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected positional args %v", fs.Args())
	}
	if *file == "" {
		return fmt.Errorf("--file required")
	}
	pol, err := recoverops.LoadPolicy(*polPath)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	var batch []json.RawMessage
	if err := json.Unmarshal(raw, &batch); err != nil {
		return fmt.Errorf("batch must be a JSON array: %w", err)
	}
	st, err := openStore(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	out, err := recoverops.IngestBatch(st, pol, batch)
	if err != nil {
		return err
	}
	rawOut, _ := json.MarshalIndent(map[string]interface{}{"results": out}, "", "  ")
	fmt.Println(string(rawOut))
	return nil
}

func registerGood(args []string) error {
	fs := flag.NewFlagSet("register-good", flag.ContinueOnError)
	dbPath := fs.String("db", "", "SQLite path")
	target := fs.String("target-uid", "", "target UID (namespace/deployment)")
	tmpl := fs.String("template", "", "pod template JSON file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected positional args %v", fs.Args())
	}
	if *target == "" || *tmpl == "" {
		return fmt.Errorf("--target-uid and --template are required")
	}
	raw, err := os.ReadFile(*tmpl)
	if err != nil {
		return err
	}
	st, err := openStore(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	hash, err := st.RegisterGood(*target, string(raw))
	if err != nil {
		return err
	}
	fmt.Printf("registered %s hash=%.12s\n", *target, hash)
	return nil
}

func mode(args []string) error {
	fs := flag.NewFlagSet("mode", flag.ContinueOnError)
	dbPath := fs.String("db", "", "SQLite path (required for get/set)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if *dbPath == "" && len(rest) == 0 {
		return fmt.Errorf("usage: recoverops mode [--db FILE] [observe|enforce-lab]")
	}
	if len(rest) == 0 {
		// get: persisted mode, defaulting to observe on a fresh DB.
		st, err := openStore(*dbPath)
		if err != nil {
			return err
		}
		defer st.Close()
		m, err := st.GetMeta("mode")
		if err != nil {
			fmt.Println("observe")
			return nil
		}
		fmt.Println(m)
		return nil
	}
	if len(rest) != 1 || (rest[0] != "observe" && rest[0] != "enforce-lab") {
		return fmt.Errorf("mode must be observe|enforce-lab")
	}
	if *dbPath == "" {
		return fmt.Errorf("--db required to set mode")
	}
	st, err := openStore(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.SetMeta("mode", rest[0]); err != nil {
		return err
	}
	fmt.Printf("mode=%s (applies at next serve start)\n", rest[0])
	return nil
}

func execute(args []string) error {
	fs := flag.NewFlagSet("execute", flag.ContinueOnError)
	dbPath := fs.String("db", "", "SQLite path")
	incident := fs.String("incident", "", "incident ID")
	kubeconfig := fs.String("kubeconfig", "", "kubeconfig path (default ~/.kube/config)")
	polPath := fs.String("policy", "configs/policies/lab-rollback.yaml", "policy file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" || *incident == "" {
		return fmt.Errorf("--db and --incident are required")
	}
	if *kubeconfig == "" {
		home, _ := os.UserHomeDir()
		*kubeconfig = home + "/.kube/config"
	}
	if out, err := execCommand("kubectl", "config", "current-context"); err != nil || out != "kind-sre-lab" {
		return fmt.Errorf("refusing: context %q != dedicated kind-sre-lab", out)
	}
	pol, err := recoverops.LoadPolicy(*polPath)
	if err != nil {
		return err
	}
	st, err := openStore(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	client, err := kubeClient(*kubeconfig)
	if err != nil {
		return err
	}
	p, err := recoverops.NewLivePatcher(client, pol.Namespace)
	if err != nil {
		return err
	}
	res, err := recoverops.ExecuteOnce(st, p, pol, *incident)
	raw, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(raw))
	return err
}

func verify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	file := fs.String("file", "", "VerifyInput JSON file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" {
		return fmt.Errorf("--file required")
	}
	raw, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	var in recoverops.VerifyInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return fmt.Errorf("bad input: %w", err)
	}
	out := recoverops.VerifyRecovery(in)
	rawOut, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(rawOut))
	if !out.Recovered {
		os.Exit(2)
	}
	return nil
}

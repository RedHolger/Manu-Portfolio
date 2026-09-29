// connect.go — F3/F4 CLI connectors: pod-delete, dep-fault, oracle-check.
// These invoke the standalone adapters against live systems; the phased
// runner additionally dispatches pod_delete (via --kubeconfig) and
// dependency_failure (via --api-admin) with journaled intent.
package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"sre-portfolio/internal/faultlab"

	_ "github.com/jackc/pgx/v5/stdlib"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func podDelete(args []string) error {
	var deployment, kubeconfig string
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "--deployment":
			deployment = args[i+1]
		case "--kubeconfig":
			kubeconfig = args[i+1]
		}
	}
	if deployment == "" {
		return fmt.Errorf("--deployment required")
	}
	if kubeconfig == "" {
		kubeconfig = filepath.Join(os.Getenv("HOME"), ".kube", "config")
	}
	if err := defaultCheckContext(context.Background()); err != nil {
		return err
	}
	client, err := kubeClient(kubeconfig)
	if err != nil {
		return err
	}
	pd, err := faultlab.NewPodDeleter(client, "sre-lab")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	target, err := pd.PickTarget(ctx, deployment)
	if err != nil {
		return err
	}
	fmt.Printf("target: %s uid=%s\n", target.PodName, target.UID)
	if err := pd.Delete(ctx, target); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	if err := pd.VerifyGone(ctx, target); err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	raw, _ := json.Marshal(map[string]string{
		"deployment": target.Deployment, "pod": target.PodName,
		"uid": target.UID, "result": "deleted-verified",
	})
	fmt.Println(string(raw))
	return waitReady(ctx, client, deployment)
}

// kubeClient builds a clientset from kubeconfig (overridable for tests).
var kubeClient = func(kubeconfig string) (kubernetes.Interface, error) {
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(cfg)
}

// waitReady blocks until the Deployment reports its desired ready replicas
// (replacement readiness) or the context times out.
func waitReady(ctx context.Context, client kubernetes.Interface, deployment string) error {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		dep, err := client.AppsV1().Deployments("sre-lab").Get(ctx, deployment,
			metav1.GetOptions{})
		if err != nil {
			return err
		}
		want := int32(1)
		if dep.Spec.Replicas != nil {
			want = *dep.Spec.Replicas
		}
		if dep.Status.ReadyReplicas >= want && dep.Status.UpdatedReplicas >= want {
			fmt.Printf("replacement ready: %d/%d\n", dep.Status.ReadyReplicas, want)
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("replacement not ready in time (ready=%d want=%d)",
				dep.Status.ReadyReplicas, want)
		case <-tick.C:
		}
	}
}

func depFault(args []string) error {
	var apiAdmin string
	var fail, clear bool
	var ttl int64 = 60
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--api-admin":
			i++
			if i < len(args) {
				apiAdmin = args[i]
			}
		case "--fail":
			fail = true
		case "--clear":
			clear = true
		case "--ttl":
			i++
			if i < len(args) {
				fmt.Sscanf(args[i], "%d", &ttl)
			}
		}
	}
	if apiAdmin == "" {
		return fmt.Errorf("--api-admin required")
	}
	if fail == clear {
		return fmt.Errorf("exactly one of --fail/--clear required")
	}
	body := map[string]any{"fail": fail, "ttlSeconds": ttl}
	if clear {
		body = map[string]any{"fail": false, "ttlSeconds": 0}
	}
	if err := depFaultBody(apiAdmin, adminToken(), body); err != nil {
		return err
	}
	fmt.Printf("depfault fail=%v ttl=%ds ok\n", fail, ttl)
	return nil
}

// depFaultBody PUTs one admin body (testable without env/flags).
func depFaultBody(apiAdmin, token string, body map[string]any) error {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest("PUT", apiAdmin+"/admin/depfault", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("depfault: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != 200 {
		return fmt.Errorf("depfault status %d", resp.StatusCode)
	}
	return nil
}

func oracleCheck(args []string) error {
	var opsFile, dsn, sku, out string
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "--ops":
			opsFile = args[i+1]
		case "--pg-dsn":
			dsn = args[i+1]
		case "--sku":
			sku = args[i+1]
		case "--out":
			out = args[i+1]
		}
	}
	if opsFile == "" || dsn == "" || sku == "" || out == "" {
		return fmt.Errorf("--ops, --pg-dsn, --sku, --out all required")
	}
	return runOracleCheck(opsFile, dsn, sku, out)
}

// runOracleCheck is the testable core: history file + Ledger + report dir.
func runOracleCheck(opsFile, dsn, sku, out string) error {
	raw, err := os.ReadFile(opsFile)
	if err != nil {
		return err
	}
	var ops []faultlab.OpRecord
	if err := json.Unmarshal(raw, &ops); err != nil {
		return err
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	return checkAndReport(faultlab.NewPGLedger(db), sku, opsFile, out)
}

// checkAndReport runs the oracle over any Ledger and writes the report.
func checkAndReport(ledger faultlab.Ledger, sku, opsFile, out string) error {
	raw, err := os.ReadFile(opsFile)
	if err != nil {
		return err
	}
	var ops []faultlab.OpRecord
	if err := json.Unmarshal(raw, &ops); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	findings, err := faultlab.Check(ctx, ledger, sku, ops)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	verdict := "CLEAN"
	if !findings.Clean() {
		verdict = "VIOLATED"
	}
	rep := faultlab.Render(faultlab.SuiteReport{
		Title: "oracle-check " + sku, Window: "live",
		Runs: []faultlab.RunEvidence{
			{RunID: sku, Class: "oracle", Decision: verdict,
				Reasons: findings.Violations,
				Detail:  fmt.Sprintf("committed=%d acked=%d ambiguous=%d", findings.Committed, findings.Acked, findings.Ambiguous)},
		},
		Oracles: []faultlab.OracleSection{
			{RunID: sku, Clean: findings.Clean(),
				Violations: findings.Violations, Info: findings.Info},
		},
	})
	if err := os.WriteFile(out+"/report.md", []byte(rep), 0o644); err != nil {
		return err
	}
	fmt.Printf("oracle %s: committed=%d acked=%d ambiguous=%d violations=%d\n",
		verdict, findings.Committed, findings.Acked, findings.Ambiguous, len(findings.Violations))
	if !findings.Clean() {
		return fmt.Errorf("oracle violations: %v", findings.Violations)
	}
	return nil
}

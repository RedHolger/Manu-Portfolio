// connect_test.go — connector units with fakes (no cluster, no PG).
package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sre-portfolio/internal/faultlab"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestDepFaultBodyAuthAndShape(t *testing.T) {
	var gotAuth, gotBody string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		if r.URL.Path != "/admin/depfault" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer s.Close()
	if err := depFaultBody(s.URL, "tok", map[string]any{"fail": true, "ttlSeconds": 60}); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer tok" {
		t.Fatalf("auth=%q", gotAuth)
	}
	var b map[string]any
	if err := json.Unmarshal([]byte(gotBody), &b); err != nil {
		t.Fatal(err)
	}
	if b["fail"] != true || b["ttlSeconds"] != float64(60) {
		t.Fatalf("body=%v", b)
	}
	if err := depFaultBody(s.URL+"/nope", "tok", map[string]any{}); err == nil {
		t.Fatal("expected error on 404")
	}
}

func TestWaitReadyReadyAndTimeout(t *testing.T) {
	replicas := int32(2)
	mkdep := func(ready int32) *appsv1.Deployment {
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "api-stable", Namespace: "sre-lab"},
			Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
			Status:     appsv1.DeploymentStatus{ReadyReplicas: ready, UpdatedReplicas: ready},
		}
	}
	cs := fake.NewSimpleClientset(mkdep(2))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := waitReady(ctx, cs, "api-stable"); err != nil {
		t.Fatalf("ready dep: %v", err)
	}
	cs2 := fake.NewSimpleClientset(mkdep(0))
	ctx2, cancel2 := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel2()
	if err := waitReady(ctx2, cs2, "api-stable"); err == nil {
		t.Fatal("unready dep must time out, not succeed")
	}
}

func TestCheckAndReportCleanAndViolated(t *testing.T) {
	led := &fakeLedger{init: 10, avail: 8, rows: []faultlab.ReservationRow{
		{ID: "r1", Key: "k1", SKU: "s", Qty: 2},
	}}
	ops := []faultlab.OpRecord{{OpID: "o1", Key: "k1", SKU: "s", Qty: 2, Acked: true, ResvID: "r1"}}
	dir := t.TempDir()
	opsFile := filepath.Join(dir, "ops.json")
	raw, _ := json.Marshal(ops)
	_ = os.WriteFile(opsFile, raw, 0o644)
	if err := checkAndReport(led, "s", opsFile, filepath.Join(dir, "out")); err != nil {
		t.Fatalf("clean: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "out", "report.md")); err != nil {
		t.Fatalf("report missing: %v", err)
	}
	led.rows = nil // ledger loses the row: lost write
	if err := checkAndReport(led, "s", opsFile, filepath.Join(dir, "out2")); err == nil {
		t.Fatal("violated ledger must error")
	}
}

type fakeLedger struct {
	init  int64
	avail int64
	rows  []faultlab.ReservationRow
}

func (f *fakeLedger) Inventory(_ context.Context, _ string) (int64, int64, error) {
	return f.init, f.avail, nil
}

func (f *fakeLedger) Reservations(_ context.Context, _ string) ([]faultlab.ReservationRow, error) {
	return f.rows, nil
}

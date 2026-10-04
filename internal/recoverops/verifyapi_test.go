package recoverops

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// promStub serves canned increase() vectors: eligible/success/fast per query.
func promStub(t *testing.T, eligible, success, fast float64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		v := eligible
		switch {
		case strings.Contains(q, "le=\"0.3\""):
			v = fast
		case strings.Contains(q, "result=\"success\""):
			v = success
		}
		if strings.Contains(q, "min(timestamp(") {
			v, _ = strconv.ParseFloat(r.URL.Query().Get("time"), 64)
		}
		if strings.Contains(q, "count_over_time") {
			v = 2
		}
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[0,"%v"]}]}}`, v)
	}))
}

func testServerWithPatcher(t *testing.T, promURL string) (*Server, *Store, *LivePatcher) {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	pol, err := LoadPolicy("../../configs/policies/lab-rollback.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Policy: pol, Token: "sekret", Mode: "enforce-lab",
		MaxBody: DefaultMaxBody, PrometheusURL: promURL}
	srv := NewServer(cfg, st)
	dep := fakeDeployment("uid-v", "rv-1", "good:1", 2)
	dep.Generation = 2
	dep.Status.ObservedGeneration = 2
	dep.Status.UpdatedReplicas = 2
	dep.Status.AvailableReplicas = 2
	dep.Status.Replicas = 2
	dep.Status.ReadyReplicas = 2
	lp, err := NewLivePatcher(fake.NewSimpleClientset(dep), WantNamespace)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := TemplateHash(dep.Spec.Template)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RegisterGood(TargetUID(pol), raw); err != nil {
		t.Fatal(err)
	}
	srv.SetPatcher(lp)
	return srv, st, lp
}

// verifyingIncidentWithExec runs ExecuteOnce against the fake cluster so the
// incident carries a real claim (RV) + EXECUTED action, then bumps the fake
// RV to model the API-server revision advance on update.
func verifyingIncidentWithExec(t *testing.T, st *Store, lp *LivePatcher, pol Policy) string {
	t.Helper()
	id := executionFixture(t, st, lp, pol, "occ-v")
	if _, err := ExecuteOnce(st, lp, pol, id); err != nil {
		t.Fatal(err)
	}
	dep, err := lp.Client.AppsV1().Deployments(WantNamespace).Get(t.Context(), WantDeployment, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	dep.ResourceVersion = "rv-2" // model the API-server revision advance
	dep.Generation = 2
	dep.Status.ObservedGeneration = 2
	dep.Status.UpdatedReplicas = 2
	dep.Status.AvailableReplicas = 2
	dep.Status.Replicas = 2
	dep.Status.ReadyReplicas = 2
	if _, err := lp.Client.AppsV1().Deployments(WantNamespace).Update(t.Context(), dep, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	// Windows must be fully post-action: backdate the action 60s (the live
	// driver posts verify minutes after patching).
	past := time.Now().Add(-60 * time.Second).UTC().Format(time.RFC3339Nano)
	if _, err := st.db.Exec(`UPDATE actions SET created_at=$1, updated_at=$1 WHERE incident_id=$2`, past, id); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE execution_intents SET executed_at=? WHERE incident_id=?`, past, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func postVerify(t *testing.T, srv *Server, id, token string) (int, map[string]interface{}) {
	t.Helper()
	req := httptest.NewRequest("POST", "/v1/incidents/"+id+"/verify", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var out map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestVerifyEndpointPersistsResolved(t *testing.T) {
	prom := promStub(t, 150, 150, 150)
	defer prom.Close()
	srv, st, lp := testServerWithPatcher(t, prom.URL)
	pol, _ := LoadPolicy("../../configs/policies/lab-rollback.yaml")
	id := verifyingIncidentWithExec(t, st, lp, pol)
	code, out := postVerify(t, srv, id, "sekret")
	if code != 200 || out["recovered"] != true {
		t.Fatalf("code=%d out=%v", code, out)
	}
	in, _ := st.GetIncident(id)
	if in.State != StResolved {
		t.Fatalf("state=%s, want RESOLVED", in.State)
	}
	evs, _ := st.Events(id)
	if !hasKind(evs, "verified") {
		t.Fatal("no verified event journaled")
	}
}

func TestVerifyEndpointRejectsThinTraffic(t *testing.T) {
	prom := promStub(t, 50, 50, 50)
	defer prom.Close()
	srv, st, lp := testServerWithPatcher(t, prom.URL)
	pol, _ := LoadPolicy("../../configs/policies/lab-rollback.yaml")
	id := verifyingIncidentWithExec(t, st, lp, pol)
	code, out := postVerify(t, srv, id, "sekret")
	if code != 422 || out["recovered"] != false {
		t.Fatalf("code=%d out=%v", code, out)
	}
	if in, _ := st.GetIncident(id); in.State != StVerifying {
		t.Fatalf("state=%s, want VERIFYING (still open)", in.State)
	}
}

func TestVerifyEndpointRequiresVerifying(t *testing.T) {
	prom := promStub(t, 150, 150, 150)
	defer prom.Close()
	srv, st, _ := testServerWithPatcher(t, prom.URL)
	in, _, _ := st.CreateIncident("occ-r", TargetUID(Policy{Namespace: WantNamespace, Deployment: WantDeployment}), "pol", "{}")
	if code, _ := postVerify(t, srv, in.ID, "sekret"); code != 409 {
		t.Fatalf("code=%d, want 409", code)
	}
}

func TestVerifyEndpointNeedsPatcher(t *testing.T) {
	srv, st := testServer(t)
	in, _, _ := st.CreateIncident("occ-p", TargetUID(Policy{Namespace: WantNamespace, Deployment: WantDeployment}), "pol", "{}")
	for _, s := range []string{StObserved, StExecuting, StVerifying} {
		_, _ = st.Transition(in.ID, s, "{}")
	}
	if code, _ := postVerify(t, srv, in.ID, "sekret"); code != 503 {
		t.Fatalf("code=%d, want 503", code)
	}
}

func TestVerifyEndpointAuth(t *testing.T) {
	prom := promStub(t, 150, 150, 150)
	defer prom.Close()
	srv, _, _ := testServerWithPatcher(t, prom.URL)
	if code, _ := postVerify(t, srv, "x", ""); code != 401 {
		t.Fatalf("code=%d, want 401", code)
	}
}

func TestVerificationRequiresObservedGenerationAndSameUID(t *testing.T) {
	for _, kind := range []string{"generation", "uid", "replicas"} {
		t.Run(kind, func(t *testing.T) {
			prom := promStub(t, 150, 150, 150)
			defer prom.Close()
			srv, st, lp := testServerWithPatcher(t, prom.URL)
			pol := testPolicy(t)
			id := verifyingIncidentWithExec(t, st, lp, pol)
			dep, _ := lp.Client.AppsV1().Deployments(WantNamespace).Get(t.Context(), WantDeployment, metav1.GetOptions{})
			switch kind {
			case "generation":
				dep.Status.ObservedGeneration = 1
			case "uid":
				dep.UID = "new"
			case "replicas":
				dep.Status.UpdatedReplicas = 1
			}
			lp.Client.AppsV1().Deployments(WantNamespace).Update(t.Context(), dep, metav1.UpdateOptions{})
			if code, _ := postVerify(t, srv, id, "sekret"); code != 422 {
				t.Fatalf("status=%d", code)
			}
		})
	}
}
func TestVerificationRejectsStaleSourceAndMissingBucket(t *testing.T) {
	for _, kind := range []string{"stale", "missing-bucket"} {
		t.Run(kind, func(t *testing.T) {
			prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query().Get("query")
				v := 150.0
				if strings.Contains(q, "timestamp(") {
					v, _ = strconv.ParseFloat(r.URL.Query().Get("time"), 64)
					if kind == "stale" {
						v -= 21
					}
				}
				if strings.Contains(q, "count_over_time") {
					v = 2
				}
				if kind == "missing-bucket" && strings.HasPrefix(q, "lab_request_duration") {
					fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[]}}`)
					return
				}
				fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[0,"%v"]}]}}`, v)
			}))
			defer prom.Close()
			_, err := measureWindows(prom.URL, time.Now().Add(-time.Minute), time.Now())
			if err == nil {
				t.Fatal("invalid telemetry accepted")
			}
		})
	}
}

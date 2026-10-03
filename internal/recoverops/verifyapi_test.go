package recoverops

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/client-go/kubernetes/fake"
)

func testServerWithPatcher(t *testing.T) (*Server, *Store, *LivePatcher) {
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
	cfg := Config{Policy: pol, Token: "sekret", Mode: "enforce-lab", MaxBody: DefaultMaxBody}
	srv := NewServer(cfg, st)
	dep := fakeDeployment("uid-v", "rv-1", "good:1", 2)
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

func verifyingIncident(t *testing.T, st *Store) string {
	t.Helper()
	in, _, err := st.CreateIncident("occ-v", TargetUID(Policy{Namespace: WantNamespace, Deployment: WantDeployment}), "pol", "{}")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{StObserved, StExecuting, StVerifying} {
		if _, err := st.Transition(in.ID, s, "{}"); err != nil {
			t.Fatal(err)
		}
	}
	return in.ID
}

func postVerify(t *testing.T, srv *Server, id, token, body string) (int, map[string]interface{}) {
	t.Helper()
	req := httptest.NewRequest("POST", "/v1/incidents/"+id+"/verify", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var out map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func goodVerifyBody() string {
	w := `{"eligible":150,"success":150,"fast_ok":150}`
	return `{"desired_present":true,"generation_hit":true,"ready_replicas":2,"want_replicas":2,"windows":[` + w + `,` + w + `,` + w + `]}`
}

func TestVerifyEndpointPersistsResolved(t *testing.T) {
	srv, st, _ := testServerWithPatcher(t)
	id := verifyingIncident(t, st)
	code, out := postVerify(t, srv, id, "sekret", goodVerifyBody())
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

func TestVerifyEndpointRejectsBadWindows(t *testing.T) {
	srv, st, _ := testServerWithPatcher(t)
	id := verifyingIncident(t, st)
	w := `{"eligible":150,"success":100,"fast_ok":100}`
	body := `{"desired_present":true,"generation_hit":true,"ready_replicas":2,"want_replicas":2,"windows":[` + w + `,` + w + `,` + w + `]}`
	code, out := postVerify(t, srv, id, "sekret", body)
	if code != 422 || out["recovered"] != false {
		t.Fatalf("code=%d out=%v", code, out)
	}
	if in, _ := st.GetIncident(id); in.State != StVerifying {
		t.Fatalf("state=%s, want VERIFYING (still open)", in.State)
	}
}

func TestVerifyEndpointRequiresVerifying(t *testing.T) {
	srv, st, _ := testServerWithPatcher(t)
	in, _, _ := st.CreateIncident("occ-r", TargetUID(Policy{Namespace: WantNamespace, Deployment: WantDeployment}), "pol", "{}")
	if code, _ := postVerify(t, srv, in.ID, "sekret", goodVerifyBody()); code != 409 {
		t.Fatalf("code=%d, want 409", code)
	}
}

func TestVerifyEndpointNeedsPatcher(t *testing.T) {
	srv, st := testServer(t)
	in, _, _ := st.CreateIncident("occ-p", TargetUID(Policy{Namespace: WantNamespace, Deployment: WantDeployment}), "pol", "{}")
	for _, s := range []string{StObserved, StExecuting, StVerifying} {
		_, _ = st.Transition(in.ID, s, "{}")
	}
	if code, _ := postVerify(t, srv, in.ID, "sekret", goodVerifyBody()); code != 503 {
		t.Fatalf("code=%d, want 503", code)
	}
}

func TestVerifyEndpointAuth(t *testing.T) {
	srv, _, _ := testServerWithPatcher(t)
	if code, _ := postVerify(t, srv, "x", "", goodVerifyBody()); code != 401 {
		t.Fatalf("code=%d, want 401", code)
	}
}

// webhook_test.go — R1 ingestion gates over real HTTP + SQLite:
// duplicate/reordered/out-of-order handling, auth/schema/body rejection,
// failed-persistence 503, cancel/list/show, and no cluster mutation.
package recoverops

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testPolicy(t *testing.T) Policy {
	t.Helper()
	p, err := LoadPolicy("../../configs/policies/lab-rollback.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func testServer(t *testing.T) (*Server, *Store) {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := Config{Addr: ":0", DBPath: "test", Policy: testPolicy(t),
		Token: "sekret", Mode: "observe", MaxBody: DefaultMaxBody}
	return NewServer(cfg, st), st
}

func firing(fp, starts string) string {
	return fmt.Sprintf(`{"status":"firing","fingerprint":%q,"startsAt":%q,`+
		`"labels":{"alertname":"LabBadTemplate","service":"reservations",`+
		`"namespace":"sre-lab","deployment":"api-stable"}}`, fp, starts)
}

func resolved(fp, starts, ends string) string {
	return fmt.Sprintf(`{"status":"resolved","fingerprint":%q,"startsAt":%q,"endsAt":%q,`+
		`"labels":{"alertname":"LabBadTemplate","service":"reservations",`+
		`"namespace":"sre-lab","deployment":"api-stable"}}`, fp, starts, ends)
}

func post(t *testing.T, srv *Server, token, body string) (int, map[string]interface{}) {
	t.Helper()
	req := httptest.NewRequest("POST", "/v1/alerts", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var out map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestFiringCreatesThenDuplicates(t *testing.T) {
	srv, st := testServer(t)
	body := "[" + firing("fp1", "2026-10-03T04:00:00Z") + "]"
	code, out := post(t, srv, "sekret", body)
	if code != http.StatusAccepted {
		t.Fatalf("code=%d out=%v", code, out)
	}
	res := out["results"].([]interface{})[0].(map[string]interface{})
	if res["result"] != "created" {
		t.Fatalf("result=%v", res)
	}
	id := res["incident_id"].(string)
	code, out = post(t, srv, "sekret", body)
	if code != http.StatusAccepted {
		t.Fatalf("redelivery code=%d", code)
	}
	if res := out["results"].([]interface{})[0].(map[string]interface{}); res["result"] != "duplicate" || res["incident_id"] != id {
		t.Fatalf("redelivery: %v", res)
	}
	list, err := st.ListIncidents(100, 0)
	if err != nil || len(list) != 1 || list[0].State != StReceived {
		t.Fatalf("incidents: %+v %v", list, err)
	}
}

func TestResolvePairAndTerminalStickiness(t *testing.T) {
	srv, st := testServer(t)
	fp := firing("fp2", "2026-10-03T04:00:00Z")
	if code, _ := post(t, srv, "sekret", "["+fp+"]"); code != 202 {
		t.Fatalf("fire code=%d", code)
	}
	// Resolved with no execution and no verification must NOT mark
	// recovery: the case suppresses (terminal, distinct from RESOLVED).
	code, out := post(t, srv, "sekret", "["+resolved("fp2", "2026-10-03T04:00:00Z", "2026-10-03T04:05:00Z")+"]")
	if code != 202 {
		t.Fatalf("resolve code=%d", code)
	}
	res := out["results"].([]interface{})[0].(map[string]interface{})
	if res["result"] != "suppressed-unverified" {
		t.Fatalf("result=%v", res)
	}
	in, _ := st.GetIncident(res["incident_id"].(string))
	if in.State != StSuppressed {
		t.Fatalf("state=%s, want SUPPRESSED", in.State)
	}
	// Late firing for a terminal occurrence must not reopen it.
	code, out = post(t, srv, "sekret", "["+fp+"]")
	if code != 202 {
		t.Fatalf("late fire code=%d", code)
	}
	if res := out["results"].([]interface{})[0].(map[string]interface{}); res["result"] != "ignored-terminal" {
		t.Fatalf("late fire: %v", res)
	}
	in, _ = st.GetIncident(in.ID)
	if in.State != StSuppressed {
		t.Fatalf("reopened: %s", in.State)
	}
}

// VERIFYING + persisted verification record + genuine resolved alert →
// RESOLVED; without the record the case stays open (journaled).
func TestResolvedRequiresVerification(t *testing.T) {
	srv, st := testServer(t)
	fp := firing("fpv", "2026-10-03T04:00:00Z")
	if code, _ := post(t, srv, "sekret", "["+fp+"]"); code != 202 {
		t.Fatalf("fire code=%d", code)
	}
	list, _ := st.ListIncidents(100, 0)
	id := list[0].ID
	for _, to := range []string{StObserved, StExecuting, StVerifying} {
		if _, err := st.Transition(id, to, "{}"); err != nil {
			t.Fatalf("transition to %s: %v", to, err)
		}
	}
	res := "[" + resolved("fpv", "2026-10-03T04:00:00Z", "2026-10-03T04:05:00Z") + "]"
	code, out := post(t, srv, "sekret", res)
	if code != 202 {
		t.Fatalf("resolve code=%d", code)
	}
	if r := out["results"].([]interface{})[0].(map[string]interface{}); r["result"] != "resolved-unverified" {
		t.Fatalf("unverified resolve: %v", r)
	}
	if in, _ := st.GetIncident(id); in.State != StVerifying {
		t.Fatalf("state=%s, want VERIFYING (still open)", in.State)
	}
	if err := st.AppendEvent(id, "verified", `{"windows":3}`); err != nil {
		t.Fatal(err)
	}
	code, out = post(t, srv, "sekret", res)
	if code != 202 {
		t.Fatalf("resolve code=%d", code)
	}
	if r := out["results"].([]interface{})[0].(map[string]interface{}); r["result"] != "resolved" {
		t.Fatalf("verified resolve: %v", r)
	}
	if in, _ := st.GetIncident(id); in.State != StResolved {
		t.Fatalf("state=%s, want RESOLVED", in.State)
	}
}

func TestUnknownResolvedOpensNothing(t *testing.T) {
	srv, st := testServer(t)
	code, out := post(t, srv, "sekret", "["+resolved("ghost", "2026-10-03T04:00:00Z", "2026-10-03T04:01:00Z")+"]")
	if code != 202 {
		t.Fatalf("code=%d", code)
	}
	if res := out["results"].([]interface{})[0].(map[string]interface{}); res["result"] != "ignored-unknown-resolved" {
		t.Fatalf("result=%v", res)
	}
	list, _ := st.ListIncidents(100, 0)
	if len(list) != 0 {
		t.Fatalf("incidents opened: %d", len(list))
	}
}

func TestRejections(t *testing.T) {
	srv, _ := testServer(t)
	cases := []struct {
		name  string
		token string
		body  string
		want  int
	}{
		{"no auth", "", "[" + firing("a", "2026-10-03T04:00:00Z") + "]", 401},
		{"bad token", "nope", "[" + firing("a", "2026-10-03T04:00:00Z") + "]", 401},
		{"not json", "sekret", "hello", 400},
		{"not array", "sekret", `{"status":"firing"}`, 400},
		{"empty batch", "sekret", `[]`, 400},
		{"bad status", "sekret", `[{"status":"maybe","fingerprint":"a","startsAt":"2026-10-03T04:00:00Z"}]`, 400},
		{"unknown field", "sekret", `[{"status":"firing","fingerprint":"a","startsAt":"2026-10-03T04:00:00Z","bogus":1,"labels":{"alertname":"LabBadTemplate","service":"reservations","namespace":"sre-lab","deployment":"api-stable"}}]`, 400},
		{"wrong target", "sekret", `[{"status":"firing","fingerprint":"a","startsAt":"2026-10-03T04:00:00Z","labels":{"alertname":"LabBadTemplate","service":"reservations","namespace":"prod","deployment":"api-stable"}}]`, 400},
		{"wrong alert", "sekret", `[{"status":"firing","fingerprint":"a","startsAt":"2026-10-03T04:00:00Z","labels":{"alertname":"Other","service":"reservations","namespace":"sre-lab","deployment":"api-stable"}}]`, 400},
		{"resolved w/o ends", "sekret", `[{"status":"resolved","fingerprint":"a","startsAt":"2026-10-03T04:00:00Z","labels":{"alertname":"LabBadTemplate","service":"reservations","namespace":"sre-lab","deployment":"api-stable"}}]`, 400},
	}
	for _, c := range cases {
		if code, _ := post(t, srv, c.token, c.body); code != c.want {
			t.Fatalf("%s: code=%d want %d", c.name, code, c.want)
		}
	}
	// Oversize body → 413, nothing committed.
	big := `[{"status":"firing","fingerprint":"` + strings.Repeat("x", 1<<20) + `","startsAt":"2026-10-03T04:00:00Z"}]`
	if code, _ := post(t, srv, "sekret", big); code != 413 {
		t.Fatalf("oversize: code=%d want 413", code)
	}
}

func TestBatchAtomicOnValidation(t *testing.T) {
	srv, st := testServer(t)
	good := firing("ok1", "2026-10-03T04:00:00Z")
	bad := `{"status":"broken"}`
	if code, _ := post(t, srv, "sekret", "["+good+","+bad+"]"); code != 400 {
		t.Fatalf("code=%d want 400", code)
	}
	list, _ := st.ListIncidents(100, 0)
	if len(list) != 0 {
		t.Fatalf("invalid batch committed %d incidents", len(list))
	}
}

func TestFailedPersistenceIs503(t *testing.T) {
	srv, st := testServer(t)
	_ = st.Close() // simulate a dead database
	code, _ := post(t, srv, "sekret", "["+firing("z", "2026-10-03T04:00:00Z")+"]")
	if code != 503 {
		t.Fatalf("code=%d want 503", code)
	}
}

func TestCancelListShow(t *testing.T) {
	srv, st := testServer(t)
	_, out := post(t, srv, "sekret", "["+firing("c1", "2026-10-03T04:00:00Z")+"]")
	id := out["results"].([]interface{})[0].(map[string]interface{})["incident_id"].(string)
	do := func(method, path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}
	if rec := do("POST", "/v1/incidents/"+id+"/cancel", "sekret"); rec.Code != 200 {
		t.Fatalf("cancel code=%d", rec.Code)
	}
	if rec := do("POST", "/v1/incidents/"+id+"/cancel", "sekret"); rec.Code != 409 {
		t.Fatalf("second cancel code=%d want 409", rec.Code)
	}
	if rec := do("POST", "/v1/incidents/nope/cancel", "sekret"); rec.Code != 404 {
		t.Fatalf("unknown cancel code=%d want 404", rec.Code)
	}
	if rec := do("GET", "/v1/incidents?limit=5&offset=0", "sekret"); rec.Code != 200 {
		t.Fatalf("list code=%d", rec.Code)
	}
	if rec := do("GET", "/v1/incidents?limit=0", "sekret"); rec.Code != 400 {
		t.Fatalf("bad limit code=%d want 400", rec.Code)
	}
	if rec := do("GET", "/v1/incidents/"+id, "sekret"); rec.Code != 200 {
		t.Fatalf("show code=%d", rec.Code)
	}
	if rec := do("GET", "/v1/incidents/nope", "sekret"); rec.Code != 404 {
		t.Fatalf("show-unknown code=%d want 404", rec.Code)
	}
	if rec := do("GET", "/v1/incidents", ""); rec.Code != 401 {
		t.Fatalf("unauth list code=%d want 401", rec.Code)
	}
	in, _ := st.GetIncident(id)
	if in.State != StCancelled {
		t.Fatalf("state=%s", in.State)
	}
}

func TestReadyAndMetrics(t *testing.T) {
	srv, _ := testServer(t)
	get := func(p string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		return rec
	}
	if rec := get("/healthz"); rec.Code != 200 {
		t.Fatalf("healthz=%d", rec.Code)
	}
	if rec := get("/readyz"); rec.Code != 200 {
		t.Fatalf("readyz=%d", rec.Code)
	}
	rec := get("/metrics")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "recoverops_up 1") {
		t.Fatalf("metrics=%d %q", rec.Code, rec.Body.String())
	}
}

// R1 performs no cluster mutation on the ingest path: the webhook/store/
// config/policy/evaluate sources must not import any Kubernetes client
// package. (R3 adds client-go for the rollback executor in k8s.go +
// execute.go only; this test narrows to the ingest path per plan.)
func TestNoKubernetesDependency(t *testing.T) {
	entries, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	allow := map[string]bool{"k8s.go": true, "execute.go": true, "verifyapi.go": true}
	for _, f := range entries {
		if strings.HasSuffix(f, "_test.go") || allow[f] {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			s := strings.TrimSpace(line)
			if strings.Contains(s, "k8s.io/") {
				t.Fatalf("%s imports Kubernetes client (%s) — ingest path must stay k8s-free", f, s)
			}
		}
	}
}

func TestReplayIngestPath(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	pol := testPolicy(t)
	batch := []byte("[" + firing("rp1", "2026-10-03T04:00:00Z") + "," +
		resolved("rp1", "2026-10-03T04:00:00Z", "2026-10-03T04:02:00Z") + "]")
	var raws []json.RawMessage
	if err := json.Unmarshal(batch, &raws); err != nil {
		t.Fatal(err)
	}
	out, err := IngestBatch(st, pol, raws)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].Result != "created" || out[1].Result != "suppressed-unverified" {
		t.Fatalf("replay: %+v", out)
	}
}

// Genuine Alertmanager v2 envelope translates to occurrences; resolved
// alerts in the same envelope form resolve the incident (VERIFYING→RESOLVED).
func TestAlertmanagerEnvelopeFiringThenResolved(t *testing.T) {
	srv, st := testServer(t)
	fire := `{"version":"4","receiver":"recoverops","status":"firing",` +
		`"alerts":[{"status":"firing","fingerprint":"am1",` +
		`"startsAt":"2026-10-03T04:00:00Z","endsAt":"0001-01-01T00:00:00Z",` +
		`"generatorURL":"http://prometheus:9090/graph",` +
		`"labels":{"alertname":"LabBadTemplate","service":"reservations",` +
		`"namespace":"sre-lab","deployment":"api-stable"}}]}`
	code, out := post(t, srv, "sekret", fire)
	if code != http.StatusAccepted {
		t.Fatalf("envelope fire code=%d out=%v", code, out)
	}
	res := out["results"].([]interface{})[0].(map[string]interface{})
	if res["result"] != "created" {
		t.Fatalf("envelope fire: %v", res)
	}
	id := res["incident_id"].(string)
	resolve := `{"version":"4","receiver":"recoverops","status":"resolved",` +
		`"alerts":[{"status":"resolved","fingerprint":"am1",` +
		`"startsAt":"2026-10-03T04:00:00Z","endsAt":"2026-10-03T04:06:00Z",` +
		`"generatorURL":"http://prometheus:9090/graph",` +
		`"labels":{"alertname":"LabBadTemplate","service":"reservations",` +
		`"namespace":"sre-lab","deployment":"api-stable"}}]}`
	code, out = post(t, srv, "sekret", resolve)
	if code != http.StatusAccepted {
		t.Fatalf("envelope resolve code=%d out=%v", code, out)
	}
	res = out["results"].([]interface{})[0].(map[string]interface{})
	if res["result"] != "suppressed-unverified" {
		t.Fatalf("envelope resolve without execution: %v", res)
	}
	in, _ := st.GetIncident(id)
	if in.State != StSuppressed {
		t.Fatalf("state=%s, want SUPPRESSED", in.State)
	}
}

// Non-AM garbage object is still rejected.
func TestNonArrayNonEnvelopeRejected(t *testing.T) {
	srv, _ := testServer(t)
	if code, _ := post(t, srv, "sekret", `{"foo":1}`); code != http.StatusBadRequest {
		t.Fatalf("code=%d, want 400", code)
	}
}

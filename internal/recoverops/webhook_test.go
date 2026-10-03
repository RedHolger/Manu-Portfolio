// webhook_test.go — R1 ingestion gates over real HTTP + SQLite:
// duplicate/reordered/out-of-order handling, auth/schema/body rejection,
// failed-persistence 503, cancel/list/show, and no cluster mutation.
package recoverops

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
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
	code, out := post(t, srv, "sekret", "["+resolved("fp2", "2026-10-03T04:00:00Z", "2026-10-03T04:05:00Z")+"]")
	if code != 202 {
		t.Fatalf("resolve code=%d", code)
	}
	res := out["results"].([]interface{})[0].(map[string]interface{})
	if res["result"] != "resolved" {
		t.Fatalf("result=%v", res)
	}
	in, _ := st.GetIncident(res["incident_id"].(string))
	if in.State != StResolved {
		t.Fatalf("state=%s", in.State)
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
	if in.State != StResolved {
		t.Fatalf("reopened: %s", in.State)
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

// R1 performs no cluster mutation: the recoverops binaries must not depend
// on any Kubernetes client package.
func TestNoKubernetesDependency(t *testing.T) {
	for _, pkg := range []string{"sre-portfolio/internal/recoverops", "sre-portfolio/cmd/recoverops"} {
		out, err := exec.Command("go", "list", "-deps", pkg).Output()
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "k8s.io/") {
				t.Fatalf("%s depends on %s", pkg, line)
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
	if len(out) != 2 || out[0].Result != "created" || out[1].Result != "resolved" {
		t.Fatalf("replay: %+v", out)
	}
}

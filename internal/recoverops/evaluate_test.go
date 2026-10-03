// evaluate_test.go — R2 policy evaluation and observe/enforce proposals.
package recoverops

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func r2store(t *testing.T) (*Store, Policy) {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	pol, err := LoadPolicy("../../configs/policies/lab-rollback.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return st, pol
}

func r2labels() map[string]string {
	return map[string]string{
		"alertname": "LabBadTemplate", "service": "reservations",
		"namespace": "sre-lab", "deployment": "api-stable",
	}
}

func fresh(t *testing.T) string {
	t.Helper()
	return time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
}

func TestEvaluateEligible(t *testing.T) {
	st, pol := r2store(t)
	h, err := st.RegisterGood("sre-lab/api-stable", `{"kind":"Pod"}`)
	if err != nil {
		t.Fatal(err)
	}
	out := Evaluate(st, pol, r2labels(), fresh(t), time.Now().UTC())
	if !out.Eligible || out.SnapshotHash != h {
		t.Fatalf("outcome: %+v", out)
	}
}

func TestEvaluateRefusals(t *testing.T) {
	st, pol := r2store(t)
	if _, err := st.RegisterGood("sre-lab/api-stable", `{"kind":"Pod"}`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	cases := []struct {
		name   string
		labels map[string]string
		starts string
		want   string
	}{
		{"stale", r2labels(), now.Add(-16 * time.Minute).Format(time.RFC3339), "stale"},
		{"future", r2labels(), now.Add(time.Minute).Format(time.RFC3339), "stale"},
		{"bad target", map[string]string{"alertname": "LabBadTemplate", "service": "reservations",
			"namespace": "sre-lab", "deployment": "other"}, fresh(t), "pinned"},
		{"bad label", map[string]string{"alertname": "Other", "service": "reservations",
			"namespace": "sre-lab", "deployment": "api-stable"}, fresh(t), "mismatch"},
	}
	for _, c := range cases {
		if out := Evaluate(st, pol, c.labels, c.starts, now); out.Eligible ||
			(len(c.want) > 0 && !strings.Contains(out.Reason, c.want)) {
			t.Fatalf("%s: %+v", c.name, out)
		}
	}
	// No snapshot for the pinned target: refused.
	st2, _ := r2store(t)
	if out := Evaluate(st2, pol, r2labels(), fresh(t), now); out.Eligible ||
		!strings.Contains(out.Reason, "no known-good") {
		t.Fatalf("no-snapshot: %+v", out)
	}
	// Snapshot bound to a different target: mismatch refused.
	if _, err := st2.RegisterGood("sre-lab/other", `{"kind":"Pod"}`); err != nil {
		t.Fatal(err)
	}
	if out := Evaluate(st2, pol, r2labels(), fresh(t), now); out.Eligible {
		t.Fatalf("mismatch eligible: %+v", out)
	}
}

// insertExecuted simulates R3 executions directly (same package): the
// limits under test read only durable rows, so no executor is needed.
func insertExecuted(t *testing.T, st *Store, incidentID string, at time.Time) {
	t.Helper()
	in, created, err := st.CreateIncident(incidentID, "sre-lab/api-stable", "ph", "{}")
	if err != nil || !created {
		t.Fatalf("setup incident: %v %v", err, created)
	}
	ts := at.UTC().Format(time.RFC3339Nano)
	if _, err := st.db.Exec(`INSERT INTO actions(action_key, incident_id,
		before_hash, desired_hash, resource_version, status, error,
		created_at, updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$8)`,
		in.ID+":restore", in.ID, "", "h", "1", ActExecuted, "", ts); err != nil {
		t.Fatal(err)
	}
}

func TestCooldownAndBudget(t *testing.T) {
	st, pol := r2store(t)
	if _, err := st.RegisterGood("sre-lab/api-stable", `{"kind":"Pod"}`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	insertExecuted(t, st, "occ-exec-1", now.Add(-time.Minute))
	if out := Evaluate(st, pol, r2labels(), fresh(t), now); out.Eligible ||
		!strings.Contains(out.Reason, "cooldown") {
		t.Fatalf("cooldown: %+v", out)
	}
	// Old execution outside cooldown but inside the hour: eligible again.
	st2, _ := r2store(t)
	if _, err := st2.RegisterGood("sre-lab/api-stable", `{"kind":"Pod"}`); err != nil {
		t.Fatal(err)
	}
	insertExecuted(t, st2, "occ-exec-2", now.Add(-11*time.Minute))
	if out := Evaluate(st2, pol, r2labels(), fresh(t), now); !out.Eligible {
		t.Fatalf("post-cooldown: %+v", out)
	}
	// Three executions inside the hour: budget exhausted.
	insertExecuted(t, st2, "occ-exec-3", now.Add(-9*time.Minute))
	insertExecuted(t, st2, "occ-exec-4", now.Add(-8*time.Minute))
	if out := Evaluate(st2, pol, r2labels(), fresh(t), now); out.Eligible ||
		!strings.Contains(out.Reason, "budget") {
		t.Fatalf("budget: %+v", out)
	}
}

func TestCooldownSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "r.db")
	st, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	pol, err := LoadPolicy("../../configs/policies/lab-rollback.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RegisterGood("sre-lab/api-stable", `{"kind":"Pod"}`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	in, _, err := st.CreateIncident("occ-r", "sre-lab/api-stable", "ph", "{}")
	if err != nil {
		t.Fatal(err)
	}
	ts := now.Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	if _, err := st.db.Exec(`INSERT INTO actions(action_key, incident_id,
		status, created_at, updated_at) VALUES($1,$2,$3,$4,$4)`,
		in.ID+":restore", in.ID, ActExecuted, ts); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if out := Evaluate(st2, pol, r2labels(), fresh(t), now); out.Eligible {
		t.Fatalf("cooldown reset by restart: %+v", out)
	}
}

func TestObserveProposalRecorded(t *testing.T) {
	newSrv := func(t *testing.T, mode string) (*Server, *Store) {
		st, err := Open(filepath.Join(t.TempDir(), "r.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.Close() })
		pol, err := LoadPolicy("../../configs/policies/lab-rollback.yaml")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.RegisterGood("sre-lab/api-stable", `{"kind":"Pod"}`); err != nil {
			t.Fatal(err)
		}
		cfg := Config{Policy: pol, Token: "s", Mode: mode, MaxBody: DefaultMaxBody}
		return NewServer(cfg, st), st
	}
	for _, tc := range []struct {
		mode, wantAction string
	}{
		{"observe", ActObserved},
		{"enforce-lab", ActProposed},
	} {
		srv, st := newSrv(t, tc.mode)
		body := firing("prop-"+tc.mode, fresh(t))
		code, out := post(t, srv, "s", "["+body+"]")
		if code != 202 {
			t.Fatalf("%s code=%d", tc.mode, code)
		}
		res := out["results"].([]interface{})[0].(map[string]interface{})
		if res["result"] != "created" {
			t.Fatalf("%s: %+v", tc.mode, res)
		}
		id := res["incident_id"].(string)
		in, err := st.GetIncident(id)
		if err != nil || in.State != StObserved {
			t.Fatalf("%s state: %+v %v", tc.mode, in, err)
		}
		acts, err := st.ActionsFor(id)
		if err != nil || len(acts) != 1 || acts[0].Status != tc.wantAction {
			t.Fatalf("%s actions: %+v %v", tc.mode, acts, err)
		}
		// Re-proposal is idempotent: still one logical action.
		if err := st.RecordProposal(id, EvalOutcome{Eligible: true, Reason: "x"}, tc.wantAction); err != nil {
			t.Fatal(err)
		}
		acts, _ = st.ActionsFor(id)
		if len(acts) != 1 {
			t.Fatalf("%s dup proposal: %+v", tc.mode, acts)
		}
	}
}

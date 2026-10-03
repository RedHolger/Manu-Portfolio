// CLI surface tests (R1): policy validate, register-good, mode get/set,
// incident show, replay — all against temp files, no network, no cluster.
package main

import (
	"os"
	"path/filepath"
	"testing"

	"sre-portfolio/internal/recoverops"
)

func tempDB(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "r.db")
}

const polPath = "../../configs/policies/lab-rollback.yaml"

func TestPolicyValidateCLI(t *testing.T) {
	if err := policy([]string{"validate", "--policy", polPath}); err != nil {
		t.Fatal(err)
	}
	if err := policy([]string{"validate", "--policy", "no-such.yaml"}); err == nil {
		t.Fatal("missing policy must error")
	}
}

func TestRegisterGoodAndModeCLI(t *testing.T) {
	db := tempDB(t)
	tmpl := filepath.Join(t.TempDir(), "tmpl.json")
	if err := os.WriteFile(tmpl, []byte(`{"kind":"Pod","a":[1,2]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := registerGood([]string{"--db", db, "--target-uid", "sre-lab/api-stable", "--template", tmpl}); err != nil {
		t.Fatal(err)
	}
	if err := registerGood([]string{"--db", db, "--target-uid", "x"}); err == nil {
		t.Fatal("missing template must error")
	}
	if err := mode([]string{"--db", db, "enforce-lab"}); err != nil {
		t.Fatal(err)
	}
	if err := mode([]string{"--db", db, "sometimes"}); err == nil {
		t.Fatal("bad mode must error")
	}
	// get prints the persisted mode (no assertion on stdout; must not error).
	if err := mode([]string{"--db", db}); err != nil {
		t.Fatal(err)
	}
}

func TestIncidentShowAndReplayCLI(t *testing.T) {
	db := tempDB(t)
	batch := filepath.Join(t.TempDir(), "batch.json")
	occ := `{"status":"firing","fingerprint":"cli1","startsAt":"2026-10-03T05:00:00Z",` +
		`"labels":{"alertname":"LabBadTemplate","service":"reservations",` +
		`"namespace":"sre-lab","deployment":"api-stable"}}`
	if err := os.WriteFile(batch, []byte("["+occ+"]"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := replay([]string{"--db", db, "--policy", polPath, "--file", batch}); err != nil {
		t.Fatal(err)
	}
	// Discover the incident ID from the store, then show it positively.
	st, err := recoverops.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	list, err := st.ListIncidents(10, 0)
	_ = st.Close()
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %+v %v", list, err)
	}
	if err := incident([]string{"show", "--db", db, "--id", list[0].ID, "--events"}); err != nil {
		t.Fatal(err)
	}
	// Discover the incident ID from the store dump via show on a bad ID first.
	if err := incident([]string{"show", "--db", db, "--id", "nope"}); err == nil {
		t.Fatal("unknown incident must error")
	}
}

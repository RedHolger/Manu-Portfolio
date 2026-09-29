// oracle_test.go — F4: healthy pass + deliberately corrupted fixtures detected.
package faultlab

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// memLedger is an in-memory Ledger for offline tests.
type memLedger struct {
	init  int64
	avail int64
	rows  []ReservationRow
}

func (m *memLedger) Inventory(_ context.Context, _ string) (int64, int64, error) {
	return m.init, m.avail, nil
}

func (m *memLedger) Reservations(_ context.Context, _ string) ([]ReservationRow, error) {
	return m.rows, nil
}

type oracleFixture struct {
	Init  int64            `json:"initial_stock"`
	Avail int64            `json:"available"`
	Rows  []ReservationRow `json:"reservations"`
	Ops   []OpRecord       `json:"ops"`
}

func loadOracleFixture(t *testing.T, name string) (memLedger, []OpRecord) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "tests", "fixtures", "oracle", name))
	if err != nil {
		t.Fatal(err)
	}
	var fx oracleFixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatal(err)
	}
	return memLedger{init: fx.Init, avail: fx.Avail, rows: fx.Rows}, fx.Ops
}

func TestOracleHealthyClean(t *testing.T) {
	led, ops := loadOracleFixture(t, "healthy.json")
	f, err := Check(context.Background(), &led, "demo-item", ops)
	if err != nil {
		t.Fatal(err)
	}
	if !f.Clean() {
		t.Fatalf("violations on healthy fixture: %v", f.Violations)
	}
	if f.Ambiguous != 1 {
		t.Fatalf("ambiguous=%d, want 1 (one reconciled retry)", f.Ambiguous)
	}
}

func TestOracleCorruptedDetected(t *testing.T) {
	cases := map[string]string{
		"dup-key.json":      "duplicate key",
		"broken-stock.json": "conservation",
		"lost-write.json":   "lost write",
		"conflict.json":     "conflicting payloads",
	}
	for name, want := range cases {
		led, ops := loadOracleFixture(t, name)
		f, err := Check(context.Background(), &led, "demo-item", ops)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if f.Clean() {
			t.Fatalf("%s: corrupted fixture passed clean", name)
		}
		joined := strings.Join(f.Violations, "|")
		if !strings.Contains(joined, want) {
			t.Fatalf("%s: violations %v lack %q", name, f.Violations, want)
		}
	}
}

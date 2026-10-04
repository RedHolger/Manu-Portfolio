// store_test.go — R1 durable-state gates: reopen preserves, duplicates
// collapse, illegal transitions rejected, schema matches migrations.
package recoverops

import (
	"os"
	"path/filepath"
	"testing"
)

func tempStore(t *testing.T) (*Store, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "r.db")
	st, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st, p
}

func TestSchemaMatchesMigration(t *testing.T) {
	mig, err := os.ReadFile("../../migrations/recoverops/001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	extra, err := os.ReadFile("../../migrations/recoverops/002_execution_safety.sql")
	if err != nil {
		t.Fatal(err)
	}
	if string(mig)+string(extra) != schemaDDL {
		t.Fatal("internal/recoverops/schema.sql drifted from migrations/recoverops/001_init.sql")
	}
}

func TestReopenPreserves(t *testing.T) {
	st, p := tempStore(t)
	in, created, err := st.CreateIncident("occ-1", "", "ph", `{"a":1}`)
	if err != nil || !created {
		t.Fatalf("create: %v created=%v", err, created)
	}
	if err := st.AppendEvent(in.ID, "note", "{}"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMeta("mode", "observe"); err != nil {
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
	got, err := st2.GetIncident(in.ID)
	if err != nil || got.State != StReceived {
		t.Fatalf("reopened: %+v %v", got, err)
	}
	evs, err := st2.Events(in.ID)
	if err != nil || len(evs) != 2 || evs[1].Seq != 1 || evs[1].Kind != "note" {
		t.Fatalf("events after reopen: %+v %v", evs, err)
	}
	m, err := st2.GetMeta("mode")
	if err != nil || m != "observe" {
		t.Fatalf("meta after reopen: %q %v", m, err)
	}
	// Sequence continues monotonically after reopen.
	if err := st2.AppendEvent(in.ID, "note2", "{}"); err != nil {
		t.Fatal(err)
	}
	evs, _ = st2.Events(in.ID)
	if evs[2].Seq != 2 {
		t.Fatalf("seq after reopen: %+v", evs)
	}
}

func TestDuplicateSourceKeyCollapses(t *testing.T) {
	st, _ := tempStore(t)
	a, created, err := st.CreateIncident("occ-9", "", "ph", "{}")
	if err != nil || !created {
		t.Fatalf("first: %v %v", err, created)
	}
	b, created, err := st.CreateIncident("occ-9", "", "ph", "{}")
	if err != nil || created || b.ID != a.ID {
		t.Fatalf("dup: %+v created=%v err=%v", b, created, err)
	}
	list, err := st.ListIncidents(100, 0)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %d %v", len(list), err)
	}
}

func TestIllegalTransitionsRejected(t *testing.T) {
	st, _ := tempStore(t)
	in, _, err := st.CreateIncident("occ-t", "", "ph", "{}")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Transition(in.ID, StReceived, "{}"); err == nil {
		t.Fatal("self-transition must be rejected")
	}
	if _, err := st.Transition(in.ID, StResolved, "{}"); err == nil {
		t.Fatal("unverified resolve allowed")
	}
	res, err := st.Transition(in.ID, StSuppressed, "{}")
	if err != nil || res.State != StSuppressed {
		t.Fatalf("resolve: %v %+v", err, res)
	}
	if _, err := st.Transition(in.ID, StReceived, "{}"); err == nil {
		t.Fatal("terminal must not reopen")
	}
	if _, err := st.Transition(in.ID, StCancelled, "{}"); err == nil {
		t.Fatal("terminal must not transition")
	}
	if _, err := st.Transition("no-such-id", StCancelled, "{}"); err == nil {
		t.Fatal("unknown incident must error")
	}
}

func TestRegisterGoodRoundTrip(t *testing.T) {
	st, _ := tempStore(t)
	h, err := st.RegisterGood("sre-lab/api-stable", `{"kind":"x", "a":1}`)
	if err != nil || len(h) != 64 {
		t.Fatalf("register: %q %v", h, err)
	}
	// Whitespace-insensitive canonical bytes: same logical template, same hash.
	h2, err := st.RegisterGood("sre-lab/api-stable", "{\n  \"a\": 1,\n  \"kind\": \"x\"\n}")
	if err != nil || h2 != h {
		t.Fatalf("canonical: %q %q %v", h, h2, err)
	}
	tmpl, got, _, err := st.KnownGood("sre-lab/api-stable")
	if err != nil || got != h || tmpl != `{"a":1,"kind":"x"}` {
		t.Fatalf("roundtrip: %q %q %v", tmpl, got, err)
	}
	if _, err := st.RegisterGood("sre-lab/api-stable", "not json"); err == nil {
		t.Fatal("non-JSON template must be rejected")
	}
}

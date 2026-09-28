// Journal + state machine tests (F1). SQLite runs in-process; no cluster.
package faultlab

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openTest(t *testing.T) *Journal {
	t.Helper()
	j, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	return j
}

func TestLifecycleExactTransitions(t *testing.T) {
	j := openTest(t)
	dl := time.Now().Add(time.Hour)
	if err := j.CreateRun("r1", "hash", "reservations", 42, dl); err != nil {
		t.Fatal(err)
	}
	path := []string{StPreflight, StBaseline, StInjecting, StObserving, StCleaning, StVerifying, StPassed}
	from := StCreated
	for _, to := range path {
		if err := j.Transition("r1", from, to, "phase", "{}", ""); err != nil {
			t.Fatalf("%s->%s: %v", from, to, err)
		}
		from = to
	}
	r, err := j.GetRun("r1")
	if err != nil {
		t.Fatal(err)
	}
	if r.State != StPassed {
		t.Fatalf("state=%s, want PASSED", r.State)
	}
	if n, _ := j.EventCount("r1"); n != len(path)+1 {
		t.Fatalf("events=%d, want %d", n, len(path)+1)
	}
	// Terminal: lock released — a new run on the service may proceed.
	if err := j.CreateRun("r2", "hash", "reservations", 43, dl); err != nil {
		t.Fatalf("lock not released: %v", err)
	}
}

func TestIllegalTransitionsRejected(t *testing.T) {
	for _, tc := range [][2]string{
		{StCreated, StInjecting}, {StBaseline, StPassed},
		{StObserving, StVerifying}, {StVerifying, StCleaning},
		{StPassed, StCleaning}, {StFailed, StCreated},
	} {
		if err := Next(tc[0], tc[1]); err == nil {
			t.Fatalf("%s->%s accepted, want rejection", tc[0], tc[1])
		}
	}
	// Journal enforces too (wrong-from CAS fails).
	j := openTest(t)
	if err := j.CreateRun("r1", "h", "svc", 1, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := j.Transition("r1", StBaseline, StPassed, "x", "{}", ""); err == nil {
		t.Fatal("journal accepted illegal transition")
	}
}

func TestConcurrentLockSecondFails(t *testing.T) {
	j := openTest(t)
	dl := time.Now().Add(time.Hour)
	if err := j.CreateRun("r1", "h", "reservations", 1, dl); err != nil {
		t.Fatal(err)
	}
	err := j.CreateRun("r2", "h", "reservations", 2, dl)
	if err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("second run: err=%v, want locked", err)
	}
	// Different service is unaffected.
	if err := j.CreateRun("r3", "h", "other", 3, dl); err != nil {
		t.Fatalf("unrelated service blocked: %v", err)
	}
}

func TestJournalSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "j.db")
	j, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.CreateRun("r1", "h", "svc", 9, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := j.Transition("r1", StCreated, StPreflight, "pre", "{}", ""); err != nil {
		t.Fatal(err)
	}
	_ = j.Close()
	j2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer j2.Close()
	r, err := j2.GetRun("r1")
	if err != nil {
		t.Fatal(err)
	}
	if r.State != StPreflight || r.Seed != 9 {
		t.Fatalf("state lost across reopen: %+v", r)
	}
	if un, err := j2.Unfinished(); err != nil || len(un) != 1 {
		t.Fatalf("unfinished=%v err=%v", un, err)
	}
}

func TestFaultIntentRecorded(t *testing.T) {
	j := openTest(t)
	if err := j.CreateRun("r1", "h", "svc", 1, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := j.RecordFault("r1", "f1", FaultDelay, `{"slot":"stable"}`, "2030-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	fl, err := j.Faults("r1")
	if err != nil {
		t.Fatal(err)
	}
	if len(fl) != 1 || fl[0].AppliedAt != "" || fl[0].ClearedAt != "" {
		t.Fatalf("intent row wrong: %+v", fl)
	}
	if err := j.MarkApplied("r1", "f1"); err != nil {
		t.Fatal(err)
	}
	if err := j.MarkCleared("r1", "f1"); err != nil {
		t.Fatal(err)
	}
	fl, _ = j.Faults("r1")
	if fl[0].AppliedAt == "" || fl[0].ClearedAt == "" {
		t.Fatalf("lifecycle not recorded: %+v", fl[0])
	}
}

func TestTerminalErrorStored(t *testing.T) {
	j := openTest(t)
	if err := j.CreateRun("r1", "h", "svc", 1, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	st := StCreated
	for _, to := range []string{StPreflight, StBaseline, StCleaning} {
		if err := j.Transition("r1", st, to, "e", "{}", ""); err != nil {
			t.Fatal(err)
		}
		st = to
	}
	if err := j.Transition("r1", StCleaning, StCleanupF, "e", "{}", "gateway unreachable"); err != nil {
		t.Fatal(err)
	}
	r, _ := j.GetRun("r1")
	if r.Error != "gateway unreachable" {
		t.Fatalf("error=%q", r.Error)
	}
}

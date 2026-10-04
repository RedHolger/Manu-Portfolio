package recoverops

import (
	"testing"
	"time"
)

func goodWindows() []VerifyWindow {
	w := VerifyWindow{Eligible: 120, Success: 119, FastOK: 119}
	ws := []VerifyWindow{w, w, w}
	start := time.Now().UTC().Add(-30 * time.Second)
	for i := range ws {
		ws[i].Start = start.Add(time.Duration(i) * 10 * time.Second)
		ws[i].End = ws[i].Start.Add(10 * time.Second)
	}
	return ws
}

func TestVerifyRecovered(t *testing.T) {
	out := VerifyRecovery(VerifyInput{DesiredPresent: true, GenerationHit: true,
		ReadyReplicas: 2, WantReplicas: 2, Windows: goodWindows()})
	if !out.Recovered {
		t.Fatalf("want recovered, got %s", out.Reason)
	}
}

func TestVerifyThinWindowFails(t *testing.T) {
	ws := goodWindows()
	ws[1] = VerifyWindow{Eligible: 99, Success: 99, FastOK: 99}
	if out := VerifyRecovery(VerifyInput{DesiredPresent: true, GenerationHit: true,
		ReadyReplicas: 1, WantReplicas: 1, Windows: ws}); out.Recovered {
		t.Fatal("thin window must not verify")
	}
}

func TestVerifySlowFails(t *testing.T) {
	ws := goodWindows()
	ws[0] = VerifyWindow{Eligible: 200, Success: 199, FastOK: 150}
	if out := VerifyRecovery(VerifyInput{DesiredPresent: true, GenerationHit: true,
		ReadyReplicas: 1, WantReplicas: 1, Windows: ws}); out.Recovered {
		t.Fatal("slow window must not verify")
	}
}

func TestVerifyMissingTelemetryEscalates(t *testing.T) {
	out := VerifyRecovery(VerifyInput{TelemetryMissing: true, Windows: goodWindows(),
		DesiredPresent: true, GenerationHit: true, ReadyReplicas: 1, WantReplicas: 1})
	if out.Recovered {
		t.Fatal("missing telemetry must escalate without action")
	}
}

func TestVerifyTimeoutEscalates(t *testing.T) {
	out := VerifyRecovery(VerifyInput{TimedOut: true, Windows: goodWindows(),
		DesiredPresent: true, GenerationHit: true, ReadyReplicas: 1, WantReplicas: 1})
	if out.Recovered {
		t.Fatal("timeout must escalate without action")
	}
}

func TestVerifyNotReadyFails(t *testing.T) {
	out := VerifyRecovery(VerifyInput{DesiredPresent: true, GenerationHit: true,
		ReadyReplicas: 0, WantReplicas: 1, Windows: goodWindows()})
	if out.Recovered {
		t.Fatal("unready replicas must not verify")
	}
}

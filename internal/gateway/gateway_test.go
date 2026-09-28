// Gateway unit tests: routing versions, fault bounds/TTL, metric presence.
// All run without Docker (fake time where needed).
package gateway

import (
	"strings"
	"testing"
	"time"
)

func TestRoutingVersionConflict(t *testing.T) {
	g := New(map[string]string{"stable": "http://x", "candidate": "http://y"})
	cfg, ok := g.SetRouting(0, 20)
	if !ok || cfg.CandidatePercent != 20 || cfg.Version != 1 {
		t.Fatalf("first update: %+v ok=%v", cfg, ok)
	}
	if _, ok := g.SetRouting(0, 50); ok {
		t.Fatal("stale version must conflict")
	}
	if _, ok := g.SetRouting(1, 101); ok {
		t.Fatal("percent >100 must be rejected")
	}
	if _, ok := g.SetRouting(1, -1); ok {
		t.Fatal("percent <0 must be rejected")
	}
}

func TestFaultBoundsAndTTL(t *testing.T) {
	g := New(map[string]string{"stable": "http://x"})
	now := time.Now()
	g.now = func() time.Time { return now }
	if g.PutFault(Fault{ID: "f", Kind: "bogus", Slot: "stable"}, 60*time.Second) {
		t.Fatal("unknown fault kind accepted")
	}
	if g.PutFault(Fault{ID: "f", Kind: FaultDelay, Slot: "stable"}, 121*time.Second) {
		t.Fatal("TTL >120s accepted")
	}
	if g.PutFault(Fault{ID: "f", Kind: FaultDelay, Slot: "stable", Fraction: 1.5}, 60*time.Second) {
		t.Fatal("fraction >1 accepted")
	}
	if !g.PutFault(Fault{ID: "f", Kind: FaultDelay, Slot: "stable", Fraction: 1.0}, 60*time.Second) {
		t.Fatal("valid fault rejected")
	}
	if got := g.faultFor("stable", "op-1"); got == nil {
		t.Fatal("fraction=1.0 fault must affect every op")
	}
	if got := g.faultFor("candidate", "op-1"); got != nil {
		t.Fatal("stable-scoped fault leaked to candidate")
	}
	now = now.Add(61 * time.Second) // past TTL
	g.Sweep()
	if got := g.faultFor("stable", "op-1"); got != nil {
		t.Fatal("expired fault still applied — traffic affected after TTL")
	}
}

func TestInitSeriesZeroErrorsExist(t *testing.T) {
	g := New(map[string]string{"stable": "http://x"})
	g.InitSeries([]string{"stable"}, []string{"/v1/reservations"},
		[]string{"success", "client_error", "server_error", "timeout", "transport_error"})
	out := g.Exposition("reservations")
	for _, result := range []string{"success", "server_error", "timeout", "transport_error"} {
		frag := `result="` + result + `"`
		if !strings.Contains(out, frag) {
			t.Fatalf("exposition missing zero-initialized series for %s\n%s", result, out)
		}
	}
}

func TestMetricsExpositionPresent(t *testing.T) {
	g := New(map[string]string{"stable": "http://x"})
	g.observe("stable", "/v1/reservations", "success", 10*time.Millisecond)
	out := g.Exposition("reservations")
	for _, want := range []string{
		`lab_requests_total{service="reservations",slot="stable",route="/v1/reservations",result="success"} 1`,
		"lab_request_duration_seconds_bucket{",
		`le="0.3"`,
		`le="+Inf"`,
		"lab_request_duration_seconds_sum{",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("exposition missing %q\n%s", want, out)
		}
	}
}

func TestSlotSamplingStable(t *testing.T) {
	gw := New(map[string]string{"stable": "http://x", "candidate": "http://y"})
	if _, ok := gw.SetRouting(0, 20); !ok {
		t.Fatal("routing update failed")
	}
	// Same op IDs must map identically across calls (seeded sampling).
	a1, a2 := gw.pickSlot("op-42-7"), gw.pickSlot("op-42-7")
	if a1 != a2 {
		t.Fatalf("unstable sampling: %s vs %s", a1, a2)
	}
}

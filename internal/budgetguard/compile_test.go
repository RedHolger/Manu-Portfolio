// compile_test.go — B2: generated rules contain the required shapes.
package budgetguard

import (
	"os"
	"strings"
	"testing"
)

func mustConfig(t *testing.T) ServiceSLO {
	t.Helper()
	raw, err := os.ReadFile("../../configs/slos/reservations.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c, err := ParseConfig(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCompileHasMultiwindowAlerts(t *testing.T) {
	out := CompileRules(mustConfig(t))
	for _, want := range []string{
		"service:availability_bad_ratio:1h > 14.4",
		"service:availability_bad_ratio:5m > 14.4",
		"service:availability_bad_ratio:6h > 6",
		"service:availability_bad_ratio:30m > 6",
		"service:availability_bad_ratio:3d > 1",
		"service:availability_bad_ratio:6h > 1",
		"service:latency_bad_ratio:1h",
		"service:latency_bad_ratio:6h > 6",
		"service:latency_bad_ratio:3d > 1",
		"ReservationsAvailabilityFastBurn",
		"ReservationsAvailabilityTicketBurn",
		"ReservationsLatencyFastBurn",
		"ReservationsLatencyTicketBurn",
		"LabTelemetryMissing",
		"absent(",
		"severity: page",
		"severity: ticket",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("compiled rules missing %q", want)
		}
	}
}

func TestRulesExcludeClientError(t *testing.T) {
	out := CompileRules(mustConfig(t))
	if n := strings.Count(out, `result!="client_error"`); n < 10 {
		t.Fatalf("only %d client_error exclusions, want ≥10 (5 windows × avail+latency)", n)
	}
	// Exactly one unfiltered total may exist: the LabTelemetryMissing
	// presence check (client_error traffic still proves the pipeline alive).
	if n := strings.Count(out, `lab_requests_total{service="reservations"}[`); n != 1 {
		t.Fatalf("%d unfiltered totals, want exactly 1 (presence check)", n)
	}
	// The LabTelemetryMissing presence check intentionally stays unfiltered:
	// client_error traffic still proves the pipeline is alive.
	if !strings.Contains(out, `absent(sum by (service) (rate(lab_requests_total{service="reservations"}[5m])))`) {
		t.Fatal("telemetry presence check missing or wrongly filtered")
	}
}

func TestDemoRulesLabeled(t *testing.T) {
	out := CompileDemoRules(mustConfig(t))
	for _, want := range []string{"DEMO-ONLY", "LabReleaseRegression", "for: 10s"} {
		if !strings.Contains(out, want) {
			t.Fatalf("demo rules missing %q", want)
		}
	}
	if strings.Contains(out, "severity: page") {
		t.Fatal("demo rules must not carry production page severity")
	}
}

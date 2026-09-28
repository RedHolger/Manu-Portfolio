// compile.go — SLO → Prometheus rules (§5.3).
//
// Generates per-SLI, per-window bad-ratio recording rules plus multiwindow
// burn-rate alerts for the 30-day reference profile:
//
//	page:   14.4× budget over (1h AND 5m)
//	ticket: 6× budget over (6h AND 30m)
//	ticket-long: 1× budget over (3d AND 6h)
//
// Demo-window rules (30s/10s) are emitted to a SEPARATE file labeled
// demo-only (D-007) and never mixed into production files.
//
// Matcher values pass through encodeMatcher (no arbitrary PromQL/label
// interpolation — fixed schema only).
package budgetguard

import (
	"fmt"
	"strings"
)

// Windows for the reference profile.
var (
	PageWindows   = [2]string{"1h", "5m"}
	TicketWindows = [2]string{"6h", "30m"}
	LongWindows   = [2]string{"3d", "6h"}
	PageFactor    = 14.4
	TicketFactor  = 6.0
	LongFactor    = 1.0
)

// encodeMatcher escapes a label value for PromQL string context.
func encodeMatcher(v string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", " ")
	return `"` + r.Replace(v) + `"`
}

// CompileRules renders recording + alert rules for the SLO.
func CompileRules(c ServiceSLO) string {
	var b strings.Builder
	svc := encodeMatcher(c.Service)
	thr := fmt.Sprint(c.LatencyThreshold)
	fmt.Fprintln(&b, "groups:")
	fmt.Fprintln(&b, "- name: budgetguard-"+c.Service)
	fmt.Fprintln(&b, "  interval: 30s")
	fmt.Fprintln(&b, "  rules:")
	for _, sli := range []struct {
		name   string
		target float64
		good   string // PromQL for good-request rate numerator shape
	}{
		{"availability", c.AvailabilityTarget, "success"},
		{"latency", c.LatencyTarget, "fast_success"},
	} {
		budget := 1 - sli.target
		_ = budget
		for _, w := range []string{"5m", "30m", "1h", "6h", "3d"} {
			fmt.Fprintf(&b, "  - record: service:%s_bad_ratio:%s\n", sli.name, w)
			fmt.Fprintf(&b, "    expr: |\n")
			// Eligible population excludes client_error (§5.3): callers
			// must not be charged for their own malformed requests.
			if sli.name == "availability" {
				fmt.Fprintf(&b, "      1 - (\n")
				fmt.Fprintf(&b, "        sum by (service) (rate(lab_requests_total{service=%s,result=\"success\"}[%s]))\n", svc, w)
				fmt.Fprintf(&b, "        / sum by (service) (rate(lab_requests_total{service=%s,result!=\"client_error\"}[%s]))\n", svc, w)
				fmt.Fprintf(&b, "      )\n")
			} else {
				// Joint success-and-speed SLI from the cumulative 0.3s
				// bucket: failures AND slow successes are both bad.
				fmt.Fprintf(&b, "      1 - (\n")
				fmt.Fprintf(&b, "        sum by (service) (rate(lab_request_duration_seconds_bucket{service=%s,result=\"success\",le=%q}[%s]))\n", svc, thr, w)
				fmt.Fprintf(&b, "        / sum by (service) (rate(lab_request_duration_seconds_count{service=%s,result!=\"client_error\"}[%s]))\n", svc, w)
				fmt.Fprintf(&b, "      )\n")
			}
		}
		_ = thr
	}
	// Availability alerts (multiwindow, SRE Workbook Ch.5 shape).
	ab := 1 - c.AvailabilityTarget
	fmt.Fprintf(&b, "  - alert: %sAvailabilityFastBurn\n", title(c.Service))
	fmt.Fprintln(&b, "    expr: |")
	fmt.Fprintf(&b, "      (\n")
	fmt.Fprintf(&b, "        (service:availability_bad_ratio:1h > %g * %g)\n", PageFactor, ab)
	fmt.Fprintln(&b, "        and on(service)")
	fmt.Fprintf(&b, "        (service:availability_bad_ratio:5m > %g * %g)\n", PageFactor, ab)
	fmt.Fprintln(&b, "      )")
	fmt.Fprintln(&b, "      or on(service)")
	fmt.Fprintf(&b, "      (\n")
	fmt.Fprintf(&b, "        (service:availability_bad_ratio:6h > %g * %g)\n", TicketFactor, ab)
	fmt.Fprintln(&b, "        and on(service)")
	fmt.Fprintf(&b, "        (service:availability_bad_ratio:30m > %g * %g)\n", TicketFactor, ab)
	fmt.Fprintln(&b, "      )")
	fmt.Fprintln(&b, "    for: 2m")
	fmt.Fprintln(&b, "    labels:")
	fmt.Fprintln(&b, "      severity: page")
	// Latency alerts mirror availability at the latency budget.
	lb := 1 - c.LatencyTarget
	fmt.Fprintf(&b, "  - alert: %sLatencyFastBurn\n", title(c.Service))
	fmt.Fprintln(&b, "    expr: |")
	fmt.Fprintf(&b, "      (\n")
	fmt.Fprintf(&b, "        (service:latency_bad_ratio:1h > %g * %g)\n", PageFactor, lb)
	fmt.Fprintln(&b, "        and on(service)")
	fmt.Fprintf(&b, "        (service:latency_bad_ratio:5m > %g * %g)\n", PageFactor, lb)
	fmt.Fprintln(&b, "      )")
	fmt.Fprintln(&b, "    for: 2m")
	fmt.Fprintln(&b, "    labels:")
	fmt.Fprintln(&b, "      severity: page")
	// Missing-telemetry alert (separate from burn alerts; UNKNOWN ≠ PASS).
	fmt.Fprintln(&b, "  - alert: LabTelemetryMissing")
	fmt.Fprintln(&b, "    expr: |")
	fmt.Fprintf(&b, "      absent(sum by (service) (rate(lab_requests_total{service=%s}[5m])))\n", svc)
	fmt.Fprintln(&b, "    for: 2m")
	fmt.Fprintln(&b, "    labels:")
	fmt.Fprintln(&b, "      severity: ticket")
	fmt.Fprintf(&b, "    annotations:\n      summary: 'no telemetry for %s; release state is UNKNOWN'\n", c.Service)
	return b.String()
}

// CompileDemoRules renders the short-window demo alert (demo-only file).
func CompileDemoRules(c ServiceSLO) string {
	var b strings.Builder
	svc := encodeMatcher(c.Service)
	fmt.Fprintln(&b, "# DEMO-ONLY rules: short windows for live demonstration.")
	fmt.Fprintln(&b, "# Never evaluate production SLO compliance with this file (D-007).")
	fmt.Fprintln(&b, "groups:")
	fmt.Fprintln(&b, "- name: budgetguard-demo-"+c.Service)
	fmt.Fprintln(&b, "  interval: 5s")
	fmt.Fprintln(&b, "  rules:")
	fmt.Fprintln(&b, "  - record: service:demo_bad_ratio:30s")
	fmt.Fprintln(&b, "    expr: |")
	fmt.Fprintf(&b, "      1 - (sum by (service) (rate(lab_requests_total{service=%s,result=\"success\"}[30s]))\n", svc)
	fmt.Fprintf(&b, "        / sum by (service) (rate(lab_requests_total{service=%s}[30s])))\n", svc)
	fmt.Fprintln(&b, "  - alert: LabReleaseRegression")
	fmt.Fprintln(&b, "    expr: service:demo_bad_ratio:30s > 0.01")
	fmt.Fprintln(&b, "    for: 10s")
	fmt.Fprintln(&b, "    labels:")
	fmt.Fprintln(&b, "      severity: demo")
	return b.String()
}

func title(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

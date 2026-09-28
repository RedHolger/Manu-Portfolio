// exp.go — Prometheus exposition for gateway metrics (stdlib only).
// Classic histograms (deliberate compatibility choice); never average
// per-pod quantiles — aggregate bucket counts in PromQL.
package gateway

import (
	"fmt"
	"strings"
	"sync/atomic"
)

// Exposition renders all series in Prometheus text format.
func (g *Gateway) Exposition(service string) string {
	var b strings.Builder
	g.muMet.Lock()
	keys := make([]string, 0, len(g.count))
	for k := range g.count {
		keys = append(keys, k)
	}
	for _, k := range keys {
		parts := strings.SplitN(k, "|", 3)
		slot, route, result := parts[0], parts[1], parts[2]
		labels := fmt.Sprintf(`service=%q,slot=%q,route=%q,result=%q`,
			service, slot, route, result)
		fmt.Fprintf(&b, "lab_requests_total{%s} %d\n", labels, g.count[k])
		fmt.Fprintf(&b, "lab_request_duration_seconds_count{%s} %d\n", labels, g.count[k])
		fmt.Fprintf(&b, "lab_request_duration_seconds_sum{%s} %g\n", labels, g.sum[k])
		cum := uint64(0)
		for i, le := range Buckets {
			cum += g.bkt[k][i]
			fmt.Fprintf(&b, "lab_request_duration_seconds_bucket{%s,le=%q} %d\n",
				labels, fmt.Sprint(le), cum)
		}
		cum += g.bkt[k][len(Buckets)]
		fmt.Fprintf(&b, "lab_request_duration_seconds_bucket{%s,le=%q} %d\n",
			labels, "+Inf", cum)
	}
	for slot, p := range g.inflight {
		fmt.Fprintf(&b, "lab_inflight_requests{service=%q,slot=%q} %d\n",
			service, slot, atomic.LoadInt64(p))
	}
	for kind, n := range g.activeFaults {
		fmt.Fprintf(&b, "lab_active_faults{kind=%q} %d\n", kind, n)
	}
	g.muMet.Unlock()
	return b.String()
}

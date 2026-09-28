// Package budgetguard — SLO compiler and release evaluator (§5).
//
// Config parsing uses a STRICT fixed-schema reader (stdlib only): the core
// compiler supports the ServiceSLO schema, not arbitrary YAML. Unknown
// fields and duplicate keys are REJECTED. A maintained YAML library may
// replace the front-end at bootstrap; the validated Config struct and all
// downstream math are unaffected.
package budgetguard

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ServiceSLO is the validated contract.
type ServiceSLO struct {
	Name               string
	Service            string
	Period             string // e.g. "30d" (informational; lab cannot claim full history)
	AvailabilityTarget float64
	LatencyTarget      float64
	LatencyThreshold   float64 // seconds; must be a gateway histogram bucket
	ExcludeResults     []string
	FreshnessSeconds   int64
	Gate               ReleaseGate
}

// ReleaseGate holds demo canary heuristics (§5.4 — NOT statistical proof).
type ReleaseGate struct {
	ObservationSeconds   int64
	MinRequestsPerSlot   int64
	MaxErrorRate         float64
	MaxErrorRateIncrease float64
	MaxSlowRate          float64
	MaxSlowRateIncrease  float64
}

// Known schema keys (unknown ⇒ error). releaseGate sub-keys likewise.
var topKeys = map[string]bool{
	"apiVersion": true, "kind": true, "metadata.name": true,
	"spec.service": true, "spec.period": true,
	"spec.availability.target": true,
	"spec.latency.target":      true, "spec.latency.thresholdSeconds": true,
	"spec.excludeResults": true, "spec.freshnessSeconds": true,
	"spec.releaseGate.observationSeconds":   true,
	"spec.releaseGate.minRequestsPerSlot":   true,
	"spec.releaseGate.maxErrorRate":         true,
	"spec.releaseGate.maxErrorRateIncrease": true,
	"spec.releaseGate.maxSlowRate":          true,
	"spec.releaseGate.maxSlowRateIncrease":  true,
}

// ParseConfig reads the fixed ServiceSLO schema strictly.
func ParseConfig(raw string) (ServiceSLO, error) {
	flat, err := flattenYAML(raw)
	if err != nil {
		return ServiceSLO{}, err
	}
	for k := range flat {
		if !topKeys[k] {
			return ServiceSLO{}, fmt.Errorf("unknown field %q", k)
		}
	}
	get := func(k string) (string, bool) { v, ok := flat[k]; return v, ok }
	req := func(k string) (string, error) {
		v, ok := get(k)
		if !ok || v == "" {
			return "", fmt.Errorf("missing required field %q", k)
		}
		return v, nil
	}
	var c ServiceSLO
	var errAll error
	need := func(key string, dst *string) {
		v, e := req(key)
		if e != nil {
			errAll = e
			return
		}
		*dst = v
	}
	var name, svc, period string
	need("metadata.name", &name)
	need("spec.service", &svc)
	need("spec.period", &period)
	c.Name, c.Service, c.Period = name, svc, period
	num := func(key string, dst *float64, min, max float64) {
		if errAll != nil {
			return
		}
		v, e := req(key)
		if e != nil {
			errAll = e
			return
		}
		f, e := strconv.ParseFloat(v, 64)
		if e != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < min || f > max {
			errAll = fmt.Errorf("field %q out of range [%v,%v]: %q", key, min, max, v)
			return
		}
		*dst = f
	}
	intf := func(key string, dst *int64, min int64) {
		if errAll != nil {
			return
		}
		v, e := req(key)
		if e != nil {
			errAll = e
			return
		}
		n, e := strconv.ParseInt(v, 10, 64)
		if e != nil || n < min {
			errAll = fmt.Errorf("field %q must be integer >= %d: %q", key, min, v)
			return
		}
		*dst = n
	}
	num("spec.availability.target", &c.AvailabilityTarget, 0, 1)
	num("spec.latency.target", &c.LatencyTarget, 0, 1)
	num("spec.latency.thresholdSeconds", &c.LatencyThreshold, 0, 1e9)
	intf("spec.freshnessSeconds", &c.FreshnessSeconds, 1)
	intf("spec.releaseGate.observationSeconds", &c.Gate.ObservationSeconds, 1)
	intf("spec.releaseGate.minRequestsPerSlot", &c.Gate.MinRequestsPerSlot, 1)
	num("spec.releaseGate.maxErrorRate", &c.Gate.MaxErrorRate, 0, 1)
	num("spec.releaseGate.maxErrorRateIncrease", &c.Gate.MaxErrorRateIncrease, 0, 1)
	num("spec.releaseGate.maxSlowRate", &c.Gate.MaxSlowRate, 0, 1)
	num("spec.releaseGate.maxSlowRateIncrease", &c.Gate.MaxSlowRateIncrease, 0, 1)
	if errAll != nil {
		return ServiceSLO{}, errAll
	}
	// Targets strictly between 0 and 1 (0 or 1 are vacuous/misconfig).
	if c.AvailabilityTarget <= 0 || c.AvailabilityTarget >= 1 {
		return ServiceSLO{}, fmt.Errorf("availability.target must be strictly between 0 and 1")
	}
	if c.LatencyTarget <= 0 || c.LatencyTarget >= 1 {
		return ServiceSLO{}, fmt.Errorf("latency.target must be strictly between 0 and 1")
	}
	// Threshold must be an available histogram bucket.
	okBucket := false
	for _, b := range []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.3, 0.5, 1, 2, 5} {
		if c.LatencyThreshold == b {
			okBucket = true
			break
		}
	}
	if !okBucket {
		return ServiceSLO{}, fmt.Errorf("latency.thresholdSeconds %v is not a gateway histogram bucket",
			c.LatencyThreshold)
	}
	if ex, ok := get("spec.excludeResults"); ok && ex != "" {
		for _, r := range strings.Split(strings.Trim(ex, "[]"), ",") {
			r = strings.TrimSpace(r)
			if r != "" {
				c.ExcludeResults = append(c.ExcludeResults, r)
			}
		}
	}
	return c, nil
}

// flattenYAML parses the restricted subset used by our configs: nested
// `key:` maps by 2-space indent, scalar values, and one inline list
// (`excludeResults: [client_error]`). Tabs, anchors, multiline, flow maps,
// and duplicate keys are rejected.
func flattenYAML(raw string) (map[string]string, error) {
	out := map[string]string{}
	var stack []string // path segments by indent level
	seen := map[string]int{}
	for i, line := range strings.Split(raw, "\n") {
		nl := strings.TrimRight(line, " \r")
		t := strings.TrimSpace(nl)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if strings.Contains(line, "\t") {
			return nil, fmt.Errorf("line %d: tabs rejected", i+1)
		}
		indent := len(nl) - len(strings.TrimLeft(nl, " "))
		if indent%2 != 0 {
			return nil, fmt.Errorf("line %d: indent must be 2-space multiples", i+1)
		}
		level := indent / 2
		if level > len(stack) {
			return nil, fmt.Errorf("line %d: over-indented", i+1)
		}
		stack = stack[:level]
		if strings.HasSuffix(t, ":") {
			stack = append(stack, strings.TrimSuffix(t, ":"))
			continue
		}
		kv := strings.SplitN(t, ":", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("line %d: expected key: value", i+1)
		}
		key := strings.TrimSpace(kv[0])
		val := strings.Trim(strings.TrimSpace(kv[1]), `"'`)
		full := strings.Join(append(append([]string{}, stack...), key), ".")
		if prev, dup := seen[full]; dup {
			return nil, fmt.Errorf("duplicate key %q (lines %d and %d)", full, prev, i+1)
		}
		seen[full] = i + 1
		out[full] = val
	}
	return out, nil
}

// Package faultlab — controlled failure experiments (F1: schema, plan,
// journal, state machine, fakes. F2 adds the live runner).
package faultlab

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Fault kinds. Gateway application-layer simulations (F2), the lab-API
// dependency outage (F3, TTL-bounded), and UID-precondition pod deletion
// (F3, allowlisted Deployments only).
const (
	FaultDelay     = "gateway_delay"
	FaultConnFail  = "conn_fail"
	FaultDepOutage = "dependency_failure"
	FaultPodDelete = "pod_delete"
)

// Lab identity enforced before any mutation (H4 applies to runners too).
const (
	WantContext   = "kind-sre-lab"
	WantNamespace = "sre-lab"
	MaxFaultSecs  = 120
)

// Scenario is the validated experiment contract.
type Scenario struct {
	Name      string
	Context   string
	Namespace string
	Service   string
	Slot      string
	Rate      float64
	Seed      int64
	Timeout   int64 // seconds
	Baseline  int64 // seconds
	FaultSecs int64 // seconds
	Recover   int64 // seconds
	FaultKind string
	DelayMs   int64
	Fraction  float64
	TTL       int64 // seconds
	AbortMax  float64
	AbortN    int64
	AbortWin  int64 // seconds
	// Declared correctness assertions (optional keys). Any declared
	// assertion makes the runner integrate the F4 oracle over the run's
	// client-observed operations and its committed ledger; an assertion
	// declared without a configured ledger fails the run closed.
	// spec.assertions.* — an assertions block opts the run into the
	// correctness oracle. The two flags record which invariants the author
	// named; the oracle itself enforces the full invariant suite (never
	// weaker than declared) and fails closed without a ledger.
	AssertDuplicates   bool // spec.assertions.duplicateReservations
	AssertNegativeInv  bool // spec.assertions.negativeInventory
	AssertsDeclared    bool // any spec.assertions.* key present
	RecoveryDeadline   int64
	RecoveryDeadlineOK bool
}

var faultKeys = map[string]bool{
	"apiVersion": true, "kind": true, "metadata.name": true,
	"spec.context": true, "spec.namespace": true,
	"spec.target.service": true, "spec.target.slot": true,
	"spec.workload.rate": true, "spec.workload.seed": true,
	"spec.workload.timeoutSeconds": true,
	"spec.phases.baselineSeconds":  true, "spec.phases.faultSeconds": true,
	"spec.phases.recoverySeconds": true,
	"spec.fault.type":             true, "spec.fault.delayMilliseconds": true,
	"spec.fault.fraction": true, "spec.fault.ttlSeconds": true,
	"spec.abort.maxFailureRatio": true, "spec.abort.consecutiveWindows": true,
	"spec.abort.windowSeconds":                true,
	"spec.assertions.duplicateReservations":   true,
	"spec.assertions.negativeInventory":       true,
	"spec.assertions.recoveryDeadlineSeconds": true,
}

// ParseScenario reads the fixed FaultExperiment schema strictly: unknown
// fields, duplicate keys, tabs, and non-2-space indents are rejected.
// Independent from budgetguard's reader by design (product boundary).
func ParseScenario(raw string) (Scenario, error) {
	var c Scenario
	flat, err := flattenFaultYAML(raw)
	if err != nil {
		return c, err
	}
	for k := range flat {
		if !faultKeys[k] {
			return c, fmt.Errorf("unknown field %q", k)
		}
	}
	var firstErr error
	str := func(key string) string {
		v, ok := flat[key]
		if !ok || v == "" {
			if firstErr == nil {
				firstErr = fmt.Errorf("missing required field %q", key)
			}
			return ""
		}
		return v
	}
	num := func(key string, dst *float64, min, max float64) {
		if firstErr != nil {
			return
		}
		v, ok := flat[key]
		if !ok {
			firstErr = fmt.Errorf("missing required field %q", key)
			return
		}
		f, e := strconv.ParseFloat(v, 64)
		if e != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < min || f > max {
			firstErr = fmt.Errorf("field %q out of range [%v,%v]: %q", key, min, max, v)
			return
		}
		*dst = f
	}
	// Optional integer: absent leaves the zero value, present must parse.
	intOpt := func(key string, dst *int64, min int64) {
		v, ok := flat[key]
		if !ok {
			return
		}
		if firstErr != nil {
			return
		}
		n, e := strconv.ParseInt(v, 10, 64)
		if e != nil || n < min {
			firstErr = fmt.Errorf("field %q must be integer >= %d: %q", key, min, v)
			return
		}
		*dst = n
	}
	boolOpt := func(key string, dst *bool) {
		v, ok := flat[key]
		if !ok {
			return
		}
		if firstErr != nil {
			return
		}
		switch strings.ToLower(v) {
		case "true":
			*dst = true
		case "false":
			*dst = false
		default:
			firstErr = fmt.Errorf("field %q must be true|false: %q", key, v)
		}
	}
	intf := func(key string, dst *int64, min int64) {
		if firstErr != nil {
			return
		}
		v, ok := flat[key]
		if !ok {
			firstErr = fmt.Errorf("missing required field %q", key)
			return
		}
		n, e := strconv.ParseInt(v, 10, 64)
		if e != nil || n < min {
			firstErr = fmt.Errorf("field %q must be integer >= %d: %q", key, min, v)
			return
		}
		*dst = n
	}
	boolOpt("spec.assertions.duplicateReservations", &c.AssertDuplicates)
	boolOpt("spec.assertions.negativeInventory", &c.AssertNegativeInv)
	intOpt("spec.assertions.recoveryDeadlineSeconds", &c.RecoveryDeadline, 0)
	for _, k := range []string{
		"spec.assertions.duplicateReservations",
		"spec.assertions.negativeInventory",
		"spec.assertions.recoveryDeadlineSeconds",
	} {
		if _, ok := flat[k]; ok {
			c.AssertsDeclared = true
		}
	}
	if c.RecoveryDeadline > 0 {
		c.RecoveryDeadlineOK = true
	}
	if v := str("metadata.name"); v != "" {
		c.Name = v
	}
	if v := str("spec.context"); v != "" {
		c.Context = v
	}
	if v := str("spec.namespace"); v != "" {
		c.Namespace = v
	}
	if v := str("spec.target.service"); v != "" {
		c.Service = v
	}
	if v := str("spec.target.slot"); v != "" {
		c.Slot = v
	}
	if v := str("spec.fault.type"); v != "" {
		c.FaultKind = v
	}
	num("spec.workload.rate", &c.Rate, 0, 1e9)
	intf("spec.workload.seed", &c.Seed, -1<<62)
	intf("spec.workload.timeoutSeconds", &c.Timeout, 1)
	intf("spec.phases.baselineSeconds", &c.Baseline, 1)
	intf("spec.phases.faultSeconds", &c.FaultSecs, 1)
	intf("spec.phases.recoverySeconds", &c.Recover, 1)
	intf("spec.fault.delayMilliseconds", &c.DelayMs, 0)
	num("spec.fault.fraction", &c.Fraction, 0, 1)
	intf("spec.fault.ttlSeconds", &c.TTL, 1)
	num("spec.abort.maxFailureRatio", &c.AbortMax, 0, 1)
	intf("spec.abort.consecutiveWindows", &c.AbortN, 1)
	intf("spec.abort.windowSeconds", &c.AbortWin, 1)
	if firstErr != nil {
		return c, firstErr
	}
	if c.Context != WantContext {
		return c, fmt.Errorf("context %q != dedicated %q — refusing", c.Context, WantContext)
	}
	if c.Namespace != WantNamespace {
		return c, fmt.Errorf("namespace %q != dedicated %q — refusing", c.Namespace, WantNamespace)
	}
	if c.Slot != "stable" && c.Slot != "candidate" {
		return c, fmt.Errorf("slot %q must be stable|candidate (one target per run)", c.Slot)
	}
	switch c.FaultKind {
	case FaultDelay, FaultConnFail, FaultDepOutage, FaultPodDelete:
	default:
		return c, fmt.Errorf("unknown fault type %q", c.FaultKind)
	}
	if c.FaultSecs > MaxFaultSecs {
		return c, fmt.Errorf("faultSeconds %d > %d bound", c.FaultSecs, MaxFaultSecs)
	}
	if c.TTL > MaxFaultSecs {
		return c, fmt.Errorf("ttlSeconds %d > %d gateway bound", c.TTL, MaxFaultSecs)
	}
	if c.TTL < c.FaultSecs {
		return c, fmt.Errorf("ttlSeconds %d shorter than faultSeconds %d", c.TTL, c.FaultSecs)
	}
	if c.Rate <= 0 {
		return c, fmt.Errorf("workload.rate must be positive")
	}
	// The declared recovery deadline bounds the whole post-cleanup phase,
	// which runs for recoverySeconds: a deadline the phase cannot meet is
	// a configuration error, not a future FAILED run.
	// The deadline must leave room above the phase it bounds (scheduling
	// and bounded drain), otherwise every compliant run fails on overhead.
	if c.RecoveryDeadline > 0 && c.RecoveryDeadline <= c.Recover {
		return c, fmt.Errorf("recoveryDeadlineSeconds %d must exceed recoverySeconds %d",
			c.RecoveryDeadline, c.Recover)
	}
	return c, nil
}

// flattenFaultYAML parses the restricted subset used by fault configs.
func flattenFaultYAML(raw string) (map[string]string, error) {
	out := map[string]string{}
	var stack []string
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
		if level := indent / 2; level > len(stack) {
			return nil, fmt.Errorf("line %d: over-indented", i+1)
		} else {
			stack = stack[:level]
		}
		if strings.HasSuffix(t, ":") {
			stack = append(stack, strings.TrimSuffix(t, ":"))
			continue
		}
		kv := strings.SplitN(t, ":", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("line %d: expected key: value", i+1)
		}
		full := strings.Join(append(append([]string{}, stack...), strings.TrimSpace(kv[0])), ".")
		if prev, dup := seen[full]; dup {
			return nil, fmt.Errorf("duplicate key %q (lines %d and %d)", full, prev, i+1)
		}
		seen[full] = i + 1
		out[full] = strings.Trim(strings.TrimSpace(kv[1]), `"'`)
	}
	return out, nil
}

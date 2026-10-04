// policy.go — remediation policy file parsing and validation (R1).
// R1 validates structure and bounds; R2 evaluates alerts against it.
package recoverops

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Policy is the parsed remediation policy.
type Policy struct {
	Name         string
	Namespace    string
	Deployment   string
	MatchLabels  map[string]string
	Action       string
	PerIncident  int64
	CooldownSecs int64
	PerHour      int64
	// Hash is the sha256 of the canonical source bytes (stored on incidents
	// to bind them to the exact policy revision that created them).
	Hash string
	raw  string
}

var policyKeys = map[string]bool{
	"apiVersion": true, "kind": true, "metadata.name": true,
	"spec.target.namespace":       true,
	"spec.target.deployment":      true,
	"spec.matchLabels.alertname":  true,
	"spec.matchLabels.service":    true,
	"spec.action":                 true,
	"spec.limits.perIncident":     true,
	"spec.limits.cooldownSeconds": true,
	"spec.limits.perHour":         true,
}

// LoadPolicy reads and validates a policy file. Unknown fields, tabs, and
// non-2-space indents are rejected (same strictness family as the other
// readers; independent implementation by product-boundary design).
func LoadPolicy(path string) (Policy, error) {
	var p Policy
	raw, err := os.ReadFile(path)
	if err != nil {
		return p, err
	}
	p.raw = string(raw)
	flat, err := flattenPolicyYAML(p.raw)
	if err != nil {
		return p, err
	}
	if flat["apiVersion"] != "portfolio.sre/v1" || flat["kind"] != "RemediationPolicy" {
		return p, fmt.Errorf("unsupported policy apiVersion/kind")
	}
	for k := range flat {
		if !policyKeys[k] {
			return p, fmt.Errorf("unknown field %q", k)
		}
	}
	str := func(key string) (string, error) {
		v, ok := flat[key]
		if !ok || v == "" {
			return "", fmt.Errorf("missing required field %q", key)
		}
		return v, nil
	}
	num := func(key string, min int64) (int64, error) {
		v, ok := flat[key]
		if !ok {
			return 0, fmt.Errorf("missing required field %q", key)
		}
		n, e := strconv.ParseInt(v, 10, 64)
		if e != nil || n < min {
			return 0, fmt.Errorf("field %q must be integer >= %d: %q", key, min, v)
		}
		return n, nil
	}
	if p.Name, err = str("metadata.name"); err != nil {
		return p, err
	}
	if p.Namespace, err = str("spec.target.namespace"); err != nil {
		return p, err
	}
	if p.Namespace != "sre-lab" {
		return p, fmt.Errorf("policy namespace %q must be pinned to sre-lab", p.Namespace)
	}
	if p.Deployment, err = str("spec.target.deployment"); err != nil {
		return p, err
	}
	if p.Deployment != WantDeployment {
		return p, fmt.Errorf("deployment must be api-stable")
	}
	p.MatchLabels = map[string]string{}
	for _, k := range []string{"spec.matchLabels.alertname", "spec.matchLabels.service"} {
		v, err := str(k)
		if err != nil {
			return p, err
		}
		p.MatchLabels[strings.TrimPrefix(k, "spec.matchLabels.")] = v
	}
	if p.Action, err = str("spec.action"); err != nil {
		return p, err
	}
	if p.Action != "restore_known_good_template" {
		return p, fmt.Errorf("unsupported action %q", p.Action)
	}
	if p.PerIncident, err = num("spec.limits.perIncident", 1); err != nil {
		return p, err
	}
	if p.CooldownSecs, err = num("spec.limits.cooldownSeconds", 1); err != nil {
		return p, err
	}
	if p.PerHour, err = num("spec.limits.perHour", 1); err != nil {
		return p, err
	}
	if p.PerIncident != 1 || p.PerHour > 3 || p.CooldownSecs < 600 {
		return p, fmt.Errorf("limits require perIncident=1, perHour<=3, cooldownSeconds>=600")
	}
	sum := sha256.Sum256([]byte(p.raw))
	p.Hash = fmt.Sprintf("%x", sum)
	return p, nil
}

// flattenPolicyYAML parses the restricted 2-space subset (no tabs,
// no duplicate keys, mappings and scalars only).
func flattenPolicyYAML(raw string) (map[string]string, error) {
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
			key := strings.TrimSuffix(t, ":")
			full := strings.Join(append(append([]string{}, stack...), key), ".")
			if _, ok := seen[full]; ok {
				return nil, fmt.Errorf("duplicate mapping %s", full)
			}
			valid := false
			for k := range policyKeys {
				if strings.HasPrefix(k, full+".") {
					valid = true
				}
			}
			if !valid {
				return nil, fmt.Errorf("unknown mapping %s", full)
			}
			seen[full] = i + 1
			stack = append(stack, key)
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

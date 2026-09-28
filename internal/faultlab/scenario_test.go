// F1 tests: strict parsing, bounds, identity enforcement.
package faultlab

import (
	"os"
	"strings"
	"testing"
)

func TestParseDelayFixture(t *testing.T) {
	raw, err := os.ReadFile("../../configs/faults/delay.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c, err := ParseScenario(string(raw))
	if err != nil {
		t.Fatalf("delay fixture: %v", err)
	}
	if c.Name != "upstream-delay" || c.FaultKind != FaultDelay || c.TTL != 75 ||
		c.FaultSecs != 60 || c.Fraction != 0.5 || c.AbortN != 2 {
		t.Fatalf("mismatch: %+v", c)
	}
}

func TestParseResetFixture(t *testing.T) {
	raw, err := os.ReadFile("../../configs/faults/reset.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseScenario(string(raw)); err != nil {
		t.Fatalf("reset fixture: %v", err)
	}
}

func TestRejects(t *testing.T) {
	base, err := os.ReadFile("../../configs/faults/delay.yaml")
	if err != nil {
		t.Fatal(err)
	}
	g := string(base)
	cases := map[string]string{
		"unknown field":   g + "  extraField: 1\n",
		"unknown fault":   strings.Replace(g, "type: gateway_delay", "type: packet_loss", 1),
		"pod deferred":    strings.Replace(g, "type: gateway_delay", "type: pod_delete", 1),
		"wrong context":   strings.Replace(g, "kind-sre-lab", "kind-prod", 1),
		"wrong namespace": strings.Replace(g, "namespace: sre-lab", "namespace: default", 1),
		"both slots":      strings.Replace(g, "slot: stable", "slot: both", 1),
		"too long":        strings.Replace(g, "faultSeconds: 60", "faultSeconds: 600", 1),
		"short ttl":       strings.Replace(g, "ttlSeconds: 75", "ttlSeconds: 10", 1),
		"bad fraction":    strings.Replace(g, "fraction: 0.5", "fraction: 1.5", 1),
		"zero rate":       strings.Replace(g, "rate: 50", "rate: 0", 1),
		"tab indent":      g + "\tbad: 1\n",
	}
	for name, raw := range cases {
		if _, err := ParseScenario(raw); err == nil {
			t.Fatalf("%s: expected error, got nil", name)
		}
	}
}

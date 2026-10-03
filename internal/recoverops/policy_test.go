// policy_test.go — R1 policy parsing and bounds.
package recoverops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadLabPolicy(t *testing.T) {
	p, err := LoadPolicy("../../configs/policies/lab-rollback.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "lab-rollback" || p.Namespace != "sre-lab" || p.Deployment != "api-stable" {
		t.Fatalf("mismatch: %+v", p)
	}
	if p.Action != "restore_known_good_template" || p.PerIncident != 1 || p.CooldownSecs != 600 || p.PerHour != 3 {
		t.Fatalf("action/limits: %+v", p)
	}
	if p.MatchLabels["alertname"] != "LabBadTemplate" || p.MatchLabels["service"] != "reservations" {
		t.Fatalf("labels: %+v", p.MatchLabels)
	}
	if len(p.Hash) != 64 {
		t.Fatalf("hash=%q, want sha256 hex", p.Hash)
	}
}

func TestPolicyRejects(t *testing.T) {
	raw, err := os.ReadFile("../../configs/policies/lab-rollback.yaml")
	if err != nil {
		t.Fatal(err)
	}
	g := string(raw)
	write := func(t *testing.T, s string) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "p.yaml")
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := map[string]string{
		"unknown field":   g + "  extraField: 1\n",
		"wrong namespace": strings.Replace(g, "namespace: sre-lab", "namespace: prod", 1),
		"bad action":      strings.Replace(g, "restore_known_good_template", "restart_everything", 1),
		"zero cooldown":   strings.Replace(g, "cooldownSeconds: 600", "cooldownSeconds: 0", 1),
		"tab indent":      g + "\tbad: 1\n",
		"dup key":         strings.Replace(g, "perHour: 3", "perHour: 3\n    perHour: 4", 1),
	}
	for name, body := range cases {
		if _, err := LoadPolicy(write(t, body)); err == nil {
			t.Fatalf("%s: expected error, got nil", name)
		}
	}
}

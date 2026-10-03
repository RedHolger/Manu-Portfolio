// config.go — RecoverOps server configuration (R1).
package recoverops

import (
	"fmt"
)

// Config is the validated serve-time configuration.
type Config struct {
	Addr      string // HTTP listen address, e.g. ":8089"
	DBPath    string // SQLite path (single-writer, WAL)
	Policy    Policy // validated remediation policy
	Token     string // bearer token for /v1/* (never logged)
	Mode      string // observe | enforce-lab (R1 persists; R2 enforces)
	MaxBody   int64  // webhook body cap (default 1MiB)
	Namespace string // pinned lab namespace (from policy)
}

// DefaultMaxBody caps webhook bodies at 1MiB (contract §8 R1).
const DefaultMaxBody = 1 << 20

// LoadConfig validates serve flags. Enforce mode is restricted to the
// dedicated lab namespace; anything else is refused before listening.
func LoadConfig(addr, dbPath, policyPath, token, mode string) (Config, error) {
	var c Config
	if addr == "" || dbPath == "" {
		return c, fmt.Errorf("--addr and --db are required")
	}
	if token == "" {
		return c, fmt.Errorf("auth token is required (env, never a flag default)")
	}
	if mode != "observe" && mode != "enforce-lab" {
		return c, fmt.Errorf("mode %q must be observe|enforce-lab", mode)
	}
	pol, err := LoadPolicy(policyPath)
	if err != nil {
		return c, err
	}
	// Enforce mode is restricted to the dedicated lab namespace, pinned by
	// the policy itself (LoadPolicy refuses anything but sre-lab). R2/R3
	// gate actual mutations; R1 performs none.
	c = Config{Addr: addr, DBPath: dbPath, Policy: pol, Token: token,
		Mode: mode, MaxBody: DefaultMaxBody, Namespace: pol.Namespace}
	return c, nil
}

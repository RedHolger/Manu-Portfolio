// reconcile.go — R3 enforce-lab background reconciler (single pass per tick).
// R1 requires periodically scanning pending DB rows so losing an in-memory
// queue never loses work; R3 uses that scan to execute PROPOSED proposals.
// One bounded ExecuteOnce per eligible incident per tick; terminal states
// stop future work. Cancellation stops future actions but never undoes an
// accepted patch.
package recoverops

import (
	"log/slog"
	"time"
)

// ReconcileOnce scans for executable proposals and runs at most one
// ExecuteOnce per eligible incident. Returns counts for metrics/logs.
func ReconcileOnce(s *Store, p *LivePatcher, pol Policy, log *slog.Logger) (executed, escalated int) {
	list, err := s.ListIncidents(100, 0)
	if err != nil {
		return 0, 0
	}
	for _, in := range list {
		if in.State != StObserved && in.State != StEligible {
			continue
		}
		acts, err := s.ActionsFor(in.ID)
		if err != nil || len(acts) == 0 {
			continue
		}
		proposed := false
		for _, a := range acts {
			if a.Status == ActProposed {
				proposed = true
			}
		}
		if !proposed {
			continue
		}
		out, err := ExecuteOnce(s, p, pol, in.ID)
		if err != nil {
			log.Info("reconcile execute failed", "incident", in.ID, "err", err)
			escalated++
			continue
		}
		log.Info("reconcile executed", "incident", in.ID, "to", out.To)
		executed++
	}
	return executed, escalated
}

// ReconcileLoop ticks until ctx done. enforce-lab only (caller gates).
func ReconcileLoop(stop <-chan struct{}, s *Store, p *LivePatcher, pol Policy, log *slog.Logger, every time.Duration) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			ReconcileOnce(s, p, pol, log)
		}
	}
}

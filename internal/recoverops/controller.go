package recoverops

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
)

// RunController resumes durable pending cases on startup and polls both execution
// and recovery. A resolved webhook is neither required nor sufficient for success.
func (s *Server) RunController(ctx context.Context, log *slog.Logger) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		pending, err := s.store.PendingIncidents()
		if err != nil {
			log.Error("pending scan failed", "err", err)
		} else {
			for _, in := range pending {
				if ctx.Err() != nil {
					return
				}
				if in.State == StReceived {
					var ev struct {
						Labels   map[string]string `json:"labels"`
						StartsAt string            `json:"startsAt"`
					}
					if json.Unmarshal([]byte(in.Evidence), &ev) == nil {
						if err := s.propose(in.ID, Occurrence{Labels: ev.Labels, StartsAt: ev.StartsAt}); err != nil {
							log.Error("proposal recovery failed", "err", err)
						}
					}
				}
			}
		}
		ReconcileOnce(s.store, s.patcher, s.cfg.Policy, log)
		pending, err = s.store.PendingIncidents()
		if err == nil {
			for _, in := range pending {
				if in.State == StVerifying {
					probe, cancel := context.WithTimeout(ctx, 40*time.Second)
					out, e := s.VerifyIncident(probe, in.ID)
					cancel()
					log.Info("recovery verification", "incident", in.ID, "recovered", out.Recovered, "reason", out.Reason, "error", e)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

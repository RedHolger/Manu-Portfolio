package recoverops

import (
	"context"
	"fmt"
	"time"
)

// RegisterLiveGood is an explicit operator operation: bind the healthy current
// Deployment UID/template only after three measured windows and rollout checks.
func RegisterLiveGood(ctx context.Context, s *Store, p *LivePatcher, pol Policy, promURL string) (string, error) {
	before, _, err := p.Get(pol.Deployment)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC().Truncate(time.Second)
	ws, err := measureWindowsContext(ctx, promURL, now.Add(-31*time.Second), now)
	if err != nil {
		return "", err
	}
	live, _, err := p.Get(pol.Deployment)
	if err != nil {
		return "", err
	}
	if before.UID != live.UID || before.TemplateHash != live.TemplateHash {
		return "", fmt.Errorf("target changed during registration")
	}
	vin := VerifyInput{DesiredPresent: true, GenerationHit: live.Generation > 0 && live.ObservedGeneration >= live.Generation, ReadyReplicas: live.Ready, WantReplicas: live.Want, Windows: ws}
	out := VerifyRecovery(vin)
	if !out.Recovered || live.Updated != live.Want || live.Total != live.Want || live.Available < live.Want {
		return "", fmt.Errorf("registration requires healthy rollout and traffic: %s", out.Reason)
	}
	// Fetch full template using the same pinned client, then check identity again.
	raw, uid, hash, err := p.Template(pol.Deployment)
	if err != nil {
		return "", err
	}
	if uid != live.UID || hash != live.TemplateHash {
		return "", fmt.Errorf("target changed before registration commit")
	}
	return s.RegisterBoundGood(TargetUID(pol), uid, raw)
}

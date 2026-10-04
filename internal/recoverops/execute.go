package recoverops

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type ExecuteOutcome struct {
	Incident string `json:"incident"`
	From     string `json:"from_state"`
	To       string `json:"to_state"`
	Action   string `json:"action_status"`
	UID      string `json:"uid"`
	Before   string `json:"before_hash"`
	Desired  string `json:"desired_hash"`
	Live     string `json:"live_hash"`
	Reason   string `json:"reason"`
}

// ExecuteOnce always uses the persisted intent after the first claim. In
// particular, restart never replaces the original before hash or desired bytes.
func ExecuteOnce(s *Store, p *LivePatcher, pol Policy, id string) (ExecuteOutcome, error) {
	out := ExecuteOutcome{Incident: id}
	in, err := s.GetIncident(id)
	if err != nil {
		return out, err
	}
	out.From = in.State
	if Terminal(in.State) {
		return out, fmt.Errorf("terminal incident: %s", in.State)
	}
	if in.Policy != pol.Hash || in.TargetUID != TargetUID(pol) {
		return out, fmt.Errorf("policy/target mismatch")
	}
	mode, err := s.GetMeta("mode")
	if err != nil || mode != "enforce-lab" {
		return out, fmt.Errorf("observe mode: execution refused")
	}
	if in.State == StVerifying {
		out.To = StVerifying
		out.Reason = "already applied; verify only"
		return out, nil
	}
	if delay, e := s.GetMeta("execution_delay_seconds"); e == nil && delay == "120" {
		created, e := time.Parse(time.RFC3339Nano, in.CreatedAt)
		if e != nil {
			return out, e
		}
		if time.Since(created) < 120*time.Second {
			return out, fmt.Errorf("simulated-manual hold until %s", created.Add(120*time.Second).Format(time.RFC3339Nano))
		}
	}
	live, _, err := p.Get(pol.Deployment)
	if err != nil {
		return out, err
	}
	x, err := s.Intent(id)
	if errors.Is(err, sql.ErrNoRows) {
		if in.State == StExecuting || in.State == StReconcil {
			_, e := s.Transition(id, StEscalated, `{"reason":"legacy in-flight action has no immutable intent; operator reconciliation required"}`)
			if e != nil {
				return out, e
			}
			out.To = StEscalated
			return out, fmt.Errorf("legacy execution missing intent; refused")
		}
		x, err = s.prepareIntent(in, pol, live)
	}
	if err != nil {
		return out, err
	}
	out.UID, out.Before, out.Desired = x.UID, x.Before, x.Desired
	escalate := func(reason string) (ExecuteOutcome, error) {
		if _, e := s.Transition(id, StEscalated, fmt.Sprintf(`{"reason":%q}`, reason)); e != nil {
			return out, e
		}
		if e := s.MarkActionStatus(id, ActFailed, reason); e != nil {
			return out, e
		}
		out.To = StEscalated
		out.Reason = reason
		return out, fmt.Errorf("%s", reason)
	}
	started, err := time.Parse(time.RFC3339Nano, x.Started)
	if err != nil {
		return out, err
	}
	if time.Since(started) > 180*time.Second {
		return escalate("execution/reconciliation deadline exceeded")
	}
	switch DecideAfterRestart(live, x.UID, x.Before, x.Desired) {
	case DecEscalat:
		return escalate("target replacement or concurrent operator edit")
	case DecVerify:
		if err = s.markApplied(id); err != nil {
			return out, err
		}
		out.To = StVerifying
		out.Action = ActExecuted
		out.Live = live.TemplateHash
		return out, nil
	}
	if x.Attempts >= 2 {
		return escalate("bounded patch attempts exhausted")
	}
	if err = s.reserveAttempt(x, live.ResourceVersion); err != nil {
		return out, err
	}
	after, patchErr := p.PatchTemplate(pol.Deployment, x.UID, live.ResourceVersion, x.Before, x.Template)
	if patchErr != nil {
		// Every ambiguous response is reconciled against a fresh GET, including
		// transport errors whose concrete Go type varies across API clients.
		fresh, _, readErr := p.Get(pol.Deployment)
		if readErr == nil {
			switch DecideAfterRestart(fresh, x.UID, x.Before, x.Desired) {
			case DecVerify:
				after = fresh
				patchErr = nil
			case DecEscalat:
				return escalate("concurrent change after patch error")
			}
		}
		if patchErr != nil {
			current, e := s.GetIncident(id)
			if e != nil {
				return out, e
			}
			if current.State == StExecuting {
				if _, e = s.Transition(id, StReconcil, fmt.Sprintf(`{"error":%q}`, patchErr.Error())); e != nil {
					return out, e
				}
			}
			out.To = StReconcil
			out.Reason = patchErr.Error()
			return out, patchErr
		}
	}
	if err = s.markApplied(id); err != nil {
		return out, err
	}
	out.To = StVerifying
	out.Action = ActExecuted
	out.Live = after.TemplateHash
	out.Reason = "applied; server verification pending"
	return out, nil
}

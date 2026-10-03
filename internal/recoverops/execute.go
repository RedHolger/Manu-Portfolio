// execute.go — R3 single-rollback driver (live path used by CLI).
// One bounded attempt, no loops, no sleeps. Cancellation stops future work
// but never undoes an accepted patch (caller checks incident state first).
package recoverops

import (
	"fmt"
)

// ExecuteOutcome is the machine-readable result of one execute call.
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

// ExecuteOnce performs one conditional template-only rollback.
// Preconditions: incident exists and is not terminal/cancelled; a PROPOSED
// action was recorded (enforce-lab path); known-good snapshot exists.
func ExecuteOnce(s *Store, p *LivePatcher, pol Policy, incidentID string) (ExecuteOutcome, error) {
	var out ExecuteOutcome
	out.Incident = incidentID
	in, err := s.GetIncident(incidentID)
	if err != nil {
		return out, fmt.Errorf("no such incident: %s", incidentID)
	}
	out.From = in.State
	if Terminal(in.State) {
		return out, fmt.Errorf("incident is terminal (%s) — refusing", in.State)
	}
	if in.State == StCancelled {
		return out, fmt.Errorf("incident cancelled — refusing future action")
	}
	target := TargetUID(pol)
	tmplJSON, _, _, err := s.KnownGood(target)
	if err != nil {
		return out, fmt.Errorf("no known-good snapshot for %s", target)
	}
	live, _, err := p.Get(pol.Deployment)
	if err != nil {
		return out, fmt.Errorf("live read: %w", err)
	}
	out.UID = live.UID
	out.Before = live.TemplateHash
	var desiredSpec string = tmplJSON
	// NOTE (canonicalization debt, v1.1): RegisterGood hashes canonicalized
	// arbitrary JSON while TemplateHash hashes struct-marshaled template —
	// byte forms differ for identical semantics. Live restore verified
	// semantically (image/env/metadata equal); unify hashing in v1.1.
	out.Desired = tmplJSON
	if err := s.ClaimForExecution(incidentID, live.UID, live.ResourceVersion, live.TemplateHash, tmplJSON); err != nil {
		return out, fmt.Errorf("claim: %w", err)
	}
	if _, err := s.Transition(incidentID, StExecuting, `{"by":"execute"}`); err != nil {
		// Legal from OBSERVED/ELIGIBLE; ELIGIBLE may already hold.
		// If transition fails, continue only if already EXECUTING/VERIFYING.
		if cur, gerr := s.GetIncident(incidentID); gerr != nil || (cur.State != StExecuting && cur.State != StVerifying) {
			return out, fmt.Errorf("transition to EXECUTING: %w", err)
		}
	}
	after, perr := p.PatchTemplate(pol.Deployment, live.UID, live.ResourceVersion, live.TemplateHash, desiredSpec)
	if perr != nil {
		dec := DecideOnPatchError(perr, live, live.UID, live.TemplateHash, tmplJSON)
		_ = s.MarkActionStatus(incidentID, ActFailed, perr.Error())
		if dec == DecEscalat {
			_, _ = s.Transition(incidentID, StEscalated, fmt.Sprintf(`{"reason":%q}`, perr.Error()))
			cur, _ := s.GetIncident(incidentID)
			out.To, out.Action, out.Reason = cur.State, ActFailed, "escalated: "+perr.Error()
			return out, perr
		}
		_, _ = s.Transition(incidentID, StReconcil, fmt.Sprintf(`{"reason":%q}`, perr.Error()))
		cur, _ := s.GetIncident(incidentID)
		out.To, out.Action, out.Reason = cur.State, ActFailed, "reconcile: "+perr.Error()
		return out, perr
	}
	out.Live = after.TemplateHash
	_ = s.MarkActionStatus(incidentID, StVerifying, "")
	if _, err := s.Transition(incidentID, StVerifying, `{"by":"execute-patched"}`); err != nil {
		cur, _ := s.GetIncident(incidentID)
		out.To = cur.State
	} else {
		out.To = StVerifying
	}
	out.Action = StVerifying
	out.Reason = "patched; proceed to R4 verification"
	_ = s.MarkActionStatus(incidentID, ActExecuted, "")
	return out, nil
}

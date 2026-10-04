// verifyapi.go — R4 verification persistence (live endpoint + helper).
// RESOLVED requires a persisted rollout + telemetry verification record,
// never a bare Alertmanager resolved notification and never trusted
// caller-supplied counts: POST /v1/incidents/{id}/verify measures three
// consecutive non-overlapping 10s windows itself from Prometheus (ending
// now, all fully after the incident's EXECUTED action), checks the live
// template against the known-good snapshot, runs VerifyRecovery, and on
// pass journals a `verified` event and transitions VERIFYING → RESOLVED.
// Anything else leaves the incident open (422/409/503) so callers must
// present real post-rollback traffic. Kept in this file (not webhook.go)
// so the no-k8s ingest-path test stays narrow: only k8s.go, execute.go
// and this file may import client-go types.
package recoverops

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	corev1 "k8s.io/api/core/v1"

	"sre-portfolio/internal/telemetry"
)

// windowExprs queries per-window eligible/success/fast counts as 10s
// increases evaluated at the window end (floats preserved).
func windowExprs() (eligible, success, fast string) {
	base := `service="reservations"`
	eligible = `sum by (service) (increase(lab_requests_total{` + base + `,result!="client_error"}[10s]))`
	success = `sum by (service) (increase(lab_requests_total{` + base + `,result="success"}[10s]))`
	fast = `sum by (service) (increase(lab_request_duration_seconds_bucket{` + base + `,result="success",le="0.3"}[10s]))`
	return eligible, success, fast
}

// sumVector adds one instant-vector result (all slots) to a float.
func sumVector(ss []telemetry.Series) (float64, error) {
	var total float64
	for _, s := range ss {
		v, err := telemetry.SampleValue(s.Value[1])
		if err != nil {
			return 0, err
		}
		total += v
	}
	return total, nil
}

// measureWindows returns three consecutive 10s VerifyWindows ending at now,
// all fully after actionAt. Any query failure, thin window, or window not
// fully post-action is an error (caller answers 422/503, incident stays open).
func measureWindows(promURL string, actionAt, now time.Time) ([]VerifyWindow, error) {
	if now.Add(-30 * time.Second).Before(actionAt) {
		return nil, fmt.Errorf("windows not fully post-action (action %s)", actionAt.UTC().Format(time.RFC3339))
	}
	cl := telemetry.New(promURL)
	elExpr, okExpr, fastExpr := windowExprs()
	var out []VerifyWindow
	for i, end := range []time.Time{now.Add(-20 * time.Second), now.Add(-10 * time.Second), now} {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		el, err1 := cl.Query(ctx, elExpr, end)
		ok, err2 := cl.Query(ctx, okExpr, end)
		fa, err3 := cl.Query(ctx, fastExpr, end)
		cancel()
		if err1 != nil || err2 != nil || err3 != nil {
			return nil, fmt.Errorf("window %d telemetry unavailable", i)
		}
		eligible, err := sumVector(el)
		if err != nil {
			return nil, fmt.Errorf("window %d eligible unreadable", i)
		}
		success, err := sumVector(ok)
		if err != nil {
			return nil, fmt.Errorf("window %d success unreadable", i)
		}
		fast, err := sumVector(fa)
		if err != nil {
			return nil, fmt.Errorf("window %d fast unreadable", i)
		}
		out = append(out, VerifyWindow{Eligible: int64(eligible), Success: int64(success), FastOK: int64(fast)})
	}
	return out, nil
}

// lastExecution returns the action time and claimed resourceVersion of the
// incident's EXECUTED (or VERIFYING) action.
func lastExecution(s *Store, incidentID string) (time.Time, string, error) {
	acts, err := s.ActionsFor(incidentID)
	if err != nil {
		return time.Time{}, "", err
	}
	for _, a := range acts {
		if a.Status == ActExecuted || a.Status == StVerifying {
			for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
				if t, err := time.Parse(layout, a.UpdatedAt); err == nil {
					return t, a.RV, nil
				}
			}
		}
	}
	return time.Time{}, "", fmt.Errorf("no executed action recorded")
}

// handleVerify persists a verification record for a VERIFYING incident.
func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request) {
	if !s.authed(r) {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id := r.PathValue("id")
	in, err := s.store.GetIncident(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "no such incident")
		return
	}
	if in.State != StVerifying {
		writeErr(w, http.StatusConflict,
			fmt.Sprintf("verification requires VERIFYING, incident is %s", in.State))
		return
	}
	if s.patcher == nil {
		writeErr(w, http.StatusServiceUnavailable, "live template check unavailable")
		return
	}
	actedAt, claimRV, err := lastExecution(s.store, id)
	if err != nil {
		writeErr(w, http.StatusConflict, "no executed action to verify after")
		return
	}
	now := time.Now()
	windows, err := measureWindows(s.cfg.PrometheusURL, actedAt, now)
	if err != nil {
		writeJSON(w, 422, map[string]interface{}{"recovered": false, "reason": err.Error()})
		return
	}
	// Persisted rollout check: live template must equal the known-good
	// snapshot (struct-form hash on both sides; see BACKLOG hashing debt).
	tmplJSON, _, _, err := s.store.KnownGood(TargetUID(s.cfg.Policy))
	if err != nil {
		writeErr(w, http.StatusConflict, "no known-good snapshot")
		return
	}
	var desired corev1.PodTemplateSpec
	if err := json.Unmarshal([]byte(tmplJSON), &desired); err != nil {
		writeErr(w, http.StatusConflict, "known-good template unreadable")
		return
	}
	_, wantHash, err := TemplateHash(desired)
	if err != nil {
		writeErr(w, http.StatusConflict, "known-good template unhashable")
		return
	}
	live, _, err := s.patcher.Get(s.cfg.Policy.Deployment)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, fmt.Sprintf("live read: %v", err))
		return
	}
	if live.TemplateHash != wantHash {
		writeJSON(w, 422, map[string]interface{}{"recovered": false,
			"reason": "live template differs from known-good snapshot"})
		return
	}
	// Generation advance: resourceVersion must have moved since the claim
	// (the patch landed and the API server accepted a new revision).
	// A status-only RV drift with identical template still counts: the
	// template equality above is the load-bearing check.
	vin := VerifyInput{DesiredPresent: true,
		GenerationHit: live.ResourceVersion != claimRV,
		ReadyReplicas: live.Ready, WantReplicas: live.Want, Windows: windows}
	out := VerifyRecovery(vin)
	if !out.Recovered {
		writeJSON(w, 422, map[string]interface{}{"recovered": false, "reason": out.Reason})
		return
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"windows": windows, "measured_at": now.UTC().Format(time.RFC3339),
		"reason": out.Reason,
	})
	if err := s.store.AppendEvent(id, "verified", string(payload)); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "persistence unavailable")
		return
	}
	if _, err := s.store.Transition(id, StResolved,
		fmt.Sprintf(`{"by":"verify-record","windows":%d}`, len(windows))); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"recovered": true,
		"reason": out.Reason, "windows": windows})
}
